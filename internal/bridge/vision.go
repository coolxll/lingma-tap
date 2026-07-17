package bridge

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	lingmaImageUploadURL = "https://lingma-api.tongyi.aliyun.com/algo/api/v2/image/upload"
	maxVisionImageBytes  = 10 << 20
)

type visionRequestError struct {
	Status  int
	Code    string
	Message string
	Cause   error
}

func (e *visionRequestError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func newVisionRequestError(status int, code, message string, cause error) *visionRequestError {
	return &visionRequestError{Status: status, Code: code, Message: message, Cause: cause}
}

// prepareVisionRequest validates a multimodal request, checks model capability,
// uploads every image to Lingma's VL CDN, and rewrites messages to the native
// content/contents shape expected by the upstream chat endpoint.
func (h *BridgeHandler) prepareVisionRequest(ctx context.Context, modelKey string, messages []map[string]any) ([]map[string]any, []string, *ModelInfo, *visionRequestError) {
	hasImages, validationErr := validateVisionMessageInputs(messages)
	if validationErr != nil {
		return nil, nil, nil, validationErr
	}
	if !hasImages {
		return messages, nil, nil, nil
	}
	if h == nil || h.client == nil {
		return nil, nil, nil, newVisionRequestError(http.StatusServiceUnavailable, "vision_unavailable", "Vision support is unavailable", nil)
	}

	models, err := h.fetchModelsWithCache(ctx)
	if err != nil {
		return nil, nil, nil, newVisionRequestError(http.StatusServiceUnavailable, "vision_models_unavailable", "Unable to verify vision model capability", err)
	}
	var model *ModelInfo
	for i := range models {
		if models[i].Key == modelKey {
			model = &models[i]
			break
		}
	}
	if model == nil {
		return nil, nil, nil, newVisionRequestError(http.StatusServiceUnavailable, "vision_model_unknown", "Vision capability metadata is unavailable for model "+modelKey, nil)
	}
	if !model.IsVL {
		return nil, nil, nil, newVisionRequestError(http.StatusBadRequest, "vision_model_unsupported", "Model "+modelKey+" does not support image input", nil)
	}

	prepared, imageURLs, prepareErr := h.client.prepareVisionMessages(ctx, messages)
	if prepareErr != nil {
		return nil, nil, nil, prepareErr
	}
	modelInfo := *model
	return prepared, imageURLs, &modelInfo, nil
}

func validateVisionMessageInputs(messages []map[string]any) (bool, *visionRequestError) {
	hasImages := false
	for _, message := range messages {
		role, _ := message["role"].(string)
		for _, field := range []string{"content", "parts"} {
			parts, ok := visionContentParts(message[field])
			if !ok {
				continue
			}
			for _, part := range parts {
				_, _, isImage, err := visionPartSource(part)
				if !isImage {
					continue
				}
				hasImages = true
				if role != "user" {
					return true, newVisionRequestError(http.StatusBadRequest, "unsupported_image_location", "Image input is only supported in user messages", nil)
				}
				if err != nil {
					return true, err
				}
			}
		}
	}
	return hasImages, nil
}

func (c *LingmaClient) prepareVisionMessages(ctx context.Context, messages []map[string]any) ([]map[string]any, []string, *visionRequestError) {
	prepared := make([]map[string]any, 0, len(messages))
	allImageURLs := make([]string, 0)
	uploadedByHash := make(map[[sha256.Size]byte]string)

	for _, message := range messages {
		copyMessage := make(map[string]any, len(message)+1)
		for key, value := range message {
			copyMessage[key] = value
		}

		parts, ok := selectedVisionParts(message)
		if !ok {
			prepared = append(prepared, copyMessage)
			continue
		}

		normalizedParts := make([]map[string]any, 0, len(parts)+1)
		textParts := make([]string, 0, len(parts))
		messageHasImage := false
		for _, part := range parts {
			source, detail, isImage, parseErr := visionPartSource(part)
			if !isImage {
				if partType, _ := part["type"].(string); partType == "text" || partType == "input_text" {
					if text, ok := part["text"].(string); ok && text != "" {
						textParts = append(textParts, text)
						normalizedParts = append(normalizedParts, map[string]any{"type": "text", "text": text})
						continue
					}
				}
				normalizedParts = append(normalizedParts, cloneStringAnyMap(part))
				continue
			}
			if parseErr != nil {
				return nil, nil, parseErr
			}
			messageHasImage = true

			data, mimeType, err := c.fetchVisionSource(ctx, source)
			if err != nil {
				return nil, nil, newVisionRequestError(http.StatusBadRequest, "invalid_image", "Unable to read image input", err)
			}
			hash := sha256.Sum256(data)
			imageURL := uploadedByHash[hash]
			if imageURL == "" {
				imageURL, err = c.uploadVisionImage(ctx, data, mimeType)
				if err != nil {
					return nil, nil, newVisionRequestError(http.StatusBadGateway, "image_upload_failed", "Lingma image upload failed", err)
				}
				uploadedByHash[hash] = imageURL
			}
			allImageURLs = append(allImageURLs, imageURL)
			normalizedParts = append(normalizedParts, map[string]any{
				"type": "image_url",
				"image_url": map[string]any{
					"url":    imageURL,
					"detail": detail,
				},
			})
		}

		if !messageHasImage {
			prepared = append(prepared, copyMessage)
			continue
		}
		if len(textParts) == 0 {
			if text, ok := message["content"].(string); ok && text != "" {
				textParts = append(textParts, text)
				normalizedParts = append([]map[string]any{{"type": "text", "text": text}}, normalizedParts...)
			}
		}
		copyMessage["content"] = strings.Join(textParts, "\n")
		// The current native definition.Message names its multimodal field
		// "contents". Older reverse-engineered clients used "parts", which the
		// current agent_chat_generation endpoint silently ignores.
		delete(copyMessage, "parts")
		copyMessage["contents"] = normalizedParts
		prepared = append(prepared, copyMessage)
	}

	return prepared, allImageURLs, nil
}

func selectedVisionParts(message map[string]any) ([]map[string]any, bool) {
	if parts, ok := visionContentParts(message["parts"]); ok && containsVisionPart(parts) {
		return parts, true
	}
	if parts, ok := visionContentParts(message["content"]); ok && containsVisionPart(parts) {
		return parts, true
	}
	return nil, false
}

func containsVisionPart(parts []map[string]any) bool {
	for _, part := range parts {
		_, _, isImage, _ := visionPartSource(part)
		if isImage {
			return true
		}
	}
	return false
}

func visionContentParts(value any) ([]map[string]any, bool) {
	switch parts := value.(type) {
	case []map[string]any:
		return parts, true
	case []any:
		result := make([]map[string]any, 0, len(parts))
		for _, value := range parts {
			part, ok := value.(map[string]any)
			if !ok {
				continue
			}
			result = append(result, part)
		}
		return result, true
	default:
		return nil, false
	}
}

func visionPartSource(part map[string]any) (source, detail string, isImage bool, requestErr *visionRequestError) {
	partType, _ := part["type"].(string)
	if partType != "image_url" && partType != "input_image" {
		return "", "", false, nil
	}
	detail, _ = part["detail"].(string)
	if detail == "" {
		detail = "auto"
	}
	// TODO(vision): resolve Responses file_id through a file store once the
	// gateway has a supported file upload/retrieval API.
	if fileID, _ := part["file_id"].(string); fileID != "" {
		return "", detail, true, newVisionRequestError(http.StatusBadRequest, "unsupported_image_file", "Responses image file_id is not supported; provide image_url instead", nil)
	}

	switch image := part["image_url"].(type) {
	case string:
		source = image
	case map[string]any:
		source, _ = image["url"].(string)
		if nestedDetail, _ := image["detail"].(string); nestedDetail != "" {
			detail = nestedDetail
		}
	}
	if source == "" {
		source, _ = part["url"].(string)
	}
	if strings.TrimSpace(source) == "" {
		return "", detail, true, newVisionRequestError(http.StatusBadRequest, "invalid_image", "Image input requires a non-empty image_url", nil)
	}
	return strings.TrimSpace(source), detail, true, nil
}

func cloneStringAnyMap(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func (c *LingmaClient) fetchVisionSource(ctx context.Context, source string) ([]byte, string, error) {
	if strings.HasPrefix(strings.ToLower(source), "data:") {
		return decodeVisionDataURI(source)
	}
	if c != nil && c.visionFetcher != nil {
		return c.visionFetcher(ctx, source)
	}
	return fetchRemoteVisionImage(ctx, source)
}

func decodeVisionDataURI(source string) ([]byte, string, error) {
	comma := strings.IndexByte(source, ',')
	if comma < 0 {
		return nil, "", fmt.Errorf("invalid data URI")
	}
	metadata := source[len("data:"):comma]
	segments := strings.Split(metadata, ";")
	mimeType := strings.ToLower(strings.TrimSpace(segments[0]))
	isBase64 := false
	for _, segment := range segments[1:] {
		if strings.EqualFold(strings.TrimSpace(segment), "base64") {
			isBase64 = true
			break
		}
	}
	if !isBase64 {
		return nil, "", fmt.Errorf("image data URI must use base64 encoding")
	}
	encoded := strings.TrimSpace(source[comma+1:])
	if base64.StdEncoding.DecodedLen(len(encoded)) > maxVisionImageBytes {
		return nil, "", fmt.Errorf("image exceeds %d bytes", maxVisionImageBytes)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, "", fmt.Errorf("decode image base64: %w", err)
	}
	return validateVisionImage(data, mimeType)
}

func fetchRemoteVisionImage(ctx context.Context, source string) ([]byte, string, error) {
	u, err := url.Parse(source)
	if err != nil {
		return nil, "", fmt.Errorf("parse image URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, "", fmt.Errorf("image URL scheme must be http or https")
	}
	if u.Hostname() == "" || u.User != nil {
		return nil, "", fmt.Errorf("invalid image URL")
	}

	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		ResponseHeaderTimeout: 15 * time.Second,
		DialContext:           safeVisionDialContext,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many image URL redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirected image URL scheme must be http or https")
			}
			if req.URL.Hostname() == "" || req.URL.User != nil {
				return fmt.Errorf("invalid redirected image URL")
			}
			return nil
		},
	}
	defer transport.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("create image request: %w", err)
	}
	req.Header.Set("Accept", "image/jpeg,image/png,image/webp")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download image: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxVisionImageBytes {
		return nil, "", fmt.Errorf("image exceeds %d bytes", maxVisionImageBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxVisionImageBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read image: %w", err)
	}
	if len(data) > maxVisionImageBytes {
		return nil, "", fmt.Errorf("image exceeds %d bytes", maxVisionImageBytes)
	}
	return validateVisionImage(data, resp.Header.Get("Content-Type"))
}

func safeVisionDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve image host: %w", err)
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var rejected bool
	for _, address := range addresses {
		if !isPublicVisionIP(address.IP) {
			rejected = true
			continue
		}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(address.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		err = dialErr
	}
	if rejected {
		return nil, fmt.Errorf("image URL resolves to a non-public address")
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("image host has no usable address")
}

func isPublicVisionIP(ip net.IP) bool {
	return ip != nil &&
		!ip.IsLoopback() &&
		!ip.IsPrivate() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsUnspecified() &&
		!ip.IsMulticast()
}

func validateVisionImage(data []byte, declaredMime string) ([]byte, string, error) {
	if len(data) == 0 {
		return nil, "", fmt.Errorf("image is empty")
	}
	if len(data) > maxVisionImageBytes {
		return nil, "", fmt.Errorf("image exceeds %d bytes", maxVisionImageBytes)
	}
	detected := strings.ToLower(strings.TrimSpace(strings.SplitN(http.DetectContentType(data), ";", 2)[0]))
	switch detected {
	case "image/jpeg", "image/png", "image/webp":
		return data, detected, nil
	default:
		if declaredMime = strings.ToLower(strings.TrimSpace(strings.SplitN(declaredMime, ";", 2)[0])); declaredMime != "" {
			return nil, "", fmt.Errorf("unsupported image content type %s (declared %s)", detected, declaredMime)
		}
		return nil, "", fmt.Errorf("unsupported image content type %s", detected)
	}
}

func (c *LingmaClient) uploadVisionImage(ctx context.Context, data []byte, mimeType string) (string, error) {
	if c == nil || c.session == nil {
		return "", fmt.Errorf("Lingma client is not initialized")
	}
	requestID := strings.ReplaceAll(newUUID(), "-", "")
	baseURL := c.visionUploadURL
	if baseURL == "" {
		baseURL = lingmaImageUploadURL
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse upload URL: %w", err)
	}
	query := u.Query()
	query.Set("request_id", requestID)
	u.RawQuery = query.Encode()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	filename := "image" + visionImageExtension(mimeType)
	filePart, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("create image upload form: %w", err)
	}
	if _, err := filePart.Write(data); err != nil {
		return "", fmt.Errorf("write image upload form: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close image upload form: %w", err)
	}
	bodyBytes := body.Bytes()
	// Lingma's upload signer does not sign the multipart bytes directly. The
	// native client signs their decimal byte length, then exposes a hash and
	// length for that signature input in dedicated Cosy headers.
	signatureBody := strconv.Itoa(len(bodyBytes))
	headers, err := c.session.BuildHeaders(signatureBody, u.String())
	if err != nil {
		return "", fmt.Errorf("build image upload headers: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create image upload request: %w", err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("AI-CLIENT-TIMESTAMP", strconv.FormatInt(time.Now().UnixMilli(), 10))
	req.Header.Set("Cosy-BodyHash", fmt.Sprintf("%x", md5.Sum([]byte(signatureBody))))
	req.Header.Set("Cosy-BodyLength", strconv.Itoa(len(signatureBody)))
	req.Header.Set("Cosy-SigPath", visionSignaturePath(u.Path))

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("upload image: %w", err)
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return "", fmt.Errorf("read image upload response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("upload image: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var response any
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return "", fmt.Errorf("parse image upload response: %w", err)
	}
	imageURL, success, foundSuccess := findVisionUploadResult(response)
	if foundSuccess && !success {
		return "", fmt.Errorf("upstream reported image upload failure")
	}
	if imageURL == "" {
		return "", fmt.Errorf("image upload response did not contain image URL")
	}
	return imageURL, nil
}

func visionSignaturePath(path string) string {
	if trimmed := strings.TrimPrefix(path, "/algo"); trimmed != path {
		return trimmed
	}
	return path
}

func visionImageExtension(mimeType string) string {
	switch mimeType {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}

func findVisionUploadResult(value any) (imageURL string, success bool, foundSuccess bool) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			switch strings.ToLower(key) {
			case "imageurl", "image_url", "url":
				if text, ok := child.(string); ok && text != "" {
					imageURL = text
				}
			case "success":
				if flag, ok := child.(bool); ok {
					success = flag
					foundSuccess = true
				}
			}
		}
		for _, child := range current {
			childURL, childSuccess, childFound := findVisionUploadResult(child)
			if imageURL == "" && childURL != "" {
				imageURL = childURL
			}
			if !foundSuccess && childFound {
				success = childSuccess
				foundSuccess = true
			}
		}
	case []any:
		for _, child := range current {
			childURL, childSuccess, childFound := findVisionUploadResult(child)
			if imageURL == "" && childURL != "" {
				imageURL = childURL
			}
			if !foundSuccess && childFound {
				success = childSuccess
				foundSuccess = true
			}
		}
	}
	return imageURL, success, foundSuccess
}

func redactVisionLogValue(value any) any {
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current))
		for key, child := range current {
			if strings.EqualFold(key, "image_urls") {
				if urls, ok := child.([]string); ok {
					redacted := make([]string, len(urls))
					for i := range redacted {
						redacted[i] = "[redacted]"
					}
					result[key] = redacted
					continue
				}
			}
			result[key] = redactVisionLogValue(child)
		}
		return result
	case []map[string]any:
		result := make([]any, 0, len(current))
		for _, child := range current {
			result = append(result, redactVisionLogValue(child))
		}
		return result
	case []any:
		result := make([]any, 0, len(current))
		for _, child := range current {
			result = append(result, redactVisionLogValue(child))
		}
		return result
	case []string:
		result := make([]string, len(current))
		for i, child := range current {
			if isSensitiveVisionURL(child) {
				result[i] = "[redacted]"
			} else {
				result[i] = child
			}
		}
		return result
	case string:
		if isSensitiveVisionURL(current) {
			return "[redacted]"
		}
		return current
	default:
		return value
	}
}

func isSensitiveVisionURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "data:image/") || strings.Contains(lower, "lingma-vl.")
}
