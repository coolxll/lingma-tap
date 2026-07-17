package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"strings"

	"github.com/coolxll/lingma-tap/internal/proto"
)

type artifactInput struct {
	Field    string
	Filename string
	MIME     string
	Body     []byte
}

func extractCorrelationKeys(rec *proto.Record) []string {
	seen := make(map[string]struct{})
	add := func(kind, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := kind + ":" + value
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
		}
	}

	if rec.Session != "" {
		add("transport", rec.Session)
	}
	if parsed, err := url.Parse(rec.URL); err == nil {
		add("request_id", parsed.Query().Get("request_id"))
		if strings.Contains(parsed.Path, "image") && strings.Contains(rec.RespMime, "image/") {
			add("image_url", parsed.String())
		}
	}

	for _, body := range [][]byte{rec.ReqBodyBlob, rec.RespBodyBlob} {
		if len(body) == 0 {
			continue
		}
		var root interface{}
		if json.Unmarshal(body, &root) == nil {
			extractJSONKeys(root, func(kind, value string) {
				if kind == "resource_url" && rec.EndpointType == proto.EndpointImageUpload {
					kind = "image_url"
				}
				add(kind, value)
			})
		}
	}
	if rec.EndpointType == proto.EndpointChat || strings.Contains(rec.ReqMime, "json") {
		for _, key := range []string{"session_id", "conversation_id", "chat_session_id"} {
			if value := findJSONField(rec.ReqBodyBlob, key); value != "" {
				add(key, value)
			}
		}
	}
	if strings.Contains(rec.RespMime, "image/") {
		add("image_url", rec.URL)
	}
	result := make([]string, 0, len(seen))
	for key := range seen {
		result = append(result, key)
	}
	// Stable order keeps raw_json and tests deterministic.
	sortStrings(result)
	return result
}

func extractJSONKeys(value interface{}, add func(string, string)) {
	switch v := value.(type) {
	case map[string]interface{}:
		for key, item := range v {
			switch key {
			case "session_id", "conversation_id", "chat_session_id", "request_id":
				if text, ok := item.(string); ok {
					add(key, text)
				}
			case "image_urls", "imageUrls":
				if values, ok := item.([]interface{}); ok {
					for _, entry := range values {
						if text, ok := entry.(string); ok {
							add("image_url", text)
						}
					}
				}
			case "url":
				if text, ok := item.(string); ok && strings.HasPrefix(text, "http") {
					add("resource_url", text)
				}
			}
			extractJSONKeys(item, add)
		}
	case []interface{}:
		for _, item := range v {
			extractJSONKeys(item, add)
		}
	}
}

func findJSONField(body []byte, field string) string {
	var root interface{}
	if len(body) == 0 || json.Unmarshal(body, &root) != nil {
		return ""
	}
	var found string
	var walk func(interface{})
	walk = func(value interface{}) {
		if found != "" {
			return
		}
		switch v := value.(type) {
		case map[string]interface{}:
			if text, ok := v[field].(string); ok {
				found = text
				return
			}
			for _, item := range v {
				walk(item)
			}
		case []interface{}:
			for _, item := range v {
				walk(item)
			}
		}
	}
	walk(root)
	return found
}

func parseImageArtifacts(rec *proto.Record) []artifactInput {
	// An incomplete capture may end in the middle of a multipart file. Never
	// persist that prefix as a downloadable artifact.
	if rec.EndpointType != proto.EndpointImageUpload || len(rec.ReqBodyBlob) == 0 ||
		!rec.BodyComplete || rec.BodyTruncated ||
		(rec.ReqSize > 0 && rec.ReqSize != int64(len(rec.ReqBodyBlob))) {
		return nil
	}
	mediaType, params, err := mime.ParseMediaType(rec.ReqMime)
	if err != nil || !strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		return nil
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil
	}
	reader := multipart.NewReader(bytes.NewReader(rec.ReqBodyBlob), boundary)
	var result []artifactInput
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result
		}
		body, err := io.ReadAll(io.LimitReader(part, proto.BodyCaptureLimit))
		if err != nil || len(body) == 0 {
			continue
		}
		if part.FileName() == "" && part.FormName() != "file" {
			continue
		}
		result = append(result, artifactInput{
			Field: part.FormName(), Filename: part.FileName(), MIME: part.Header.Get("Content-Type"), Body: body,
		})
	}
	return result
}

func artifactSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
