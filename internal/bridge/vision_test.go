package bridge

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/coolxll/lingma-tap/internal/proto"
)

const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestPrepareVisionMessagesUploadsOnceAndUsesNativeContents(t *testing.T) {
	imageData, err := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	uploads := 0
	uploadHandler := func(r *http.Request) (*http.Response, error) {
		uploads++
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != "/algo/api/v2/image/upload" || r.URL.Query().Get("request_id") == "" {
			t.Errorf("upload URL = %s", r.URL.String())
		}
		rawBody, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("read multipart body: %v", readErr)
			return nil, readErr
		}
		signatureBody := strconv.Itoa(len(rawBody))
		if got, want := r.Header.Get("Cosy-BodyLength"), strconv.Itoa(len(signatureBody)); got != want {
			t.Errorf("Cosy-BodyLength = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Cosy-BodyHash"), fmt.Sprintf("%x", md5.Sum([]byte(signatureBody))); got != want {
			t.Errorf("Cosy-BodyHash = %q, want %q", got, want)
		}
		if got := r.Header.Get("Cosy-SigPath"); got != "/api/v2/image/upload" {
			t.Errorf("Cosy-SigPath = %q", got)
		}
		if r.Header.Get("Authorization") == "" || r.Header.Get("AI-CLIENT-TIMESTAMP") == "" {
			t.Error("missing upload authorization headers")
		}

		_, params, parseErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if parseErr != nil {
			t.Errorf("parse multipart content type: %v", parseErr)
			return nil, parseErr
		}
		reader := multipart.NewReader(bytes.NewReader(rawBody), params["boundary"])
		part, partErr := reader.NextPart()
		if partErr != nil {
			t.Errorf("read file part: %v", partErr)
			return nil, partErr
		}
		uploaded, _ := io.ReadAll(part)
		if part.FormName() != "file" || part.FileName() != "image.png" || !bytes.Equal(uploaded, imageData) {
			t.Errorf("unexpected upload part name=%q filename=%q size=%d", part.FormName(), part.FileName(), len(uploaded))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"result":{"url":"https://lingma-vl.example/image.png"},"success":true}`)),
		}, nil
	}

	client := NewLingmaClient(&auth.Session{CosyKey: "test-key", UID: "test-user"})
	client.client = &http.Client{Transport: &mockTransport{roundTripFunc: uploadHandler}}
	client.visionUploadURL = "https://upload.test/algo/api/v2/image/upload"
	dataURI := "data:image/png;base64," + tinyPNGBase64
	messages := []map[string]any{{
		"role": "user",
		"content": []map[string]any{
			{"type": "text", "text": "name the object"},
			{"type": "image_url", "image_url": map[string]any{"url": dataURI}},
			{"type": "input_image", "image_url": dataURI},
		},
	}}

	prepared, imageURLs, requestErr := client.prepareVisionMessages(context.Background(), messages)
	if requestErr != nil {
		t.Fatalf("prepareVisionMessages: %v", requestErr)
	}
	if uploads != 1 {
		t.Fatalf("uploads = %d, want one deduplicated upload", uploads)
	}
	if len(imageURLs) != 2 || imageURLs[0] != imageURLs[1] {
		t.Fatalf("image URLs = %#v", imageURLs)
	}
	if got := prepared[0]["content"]; got != "name the object" {
		t.Fatalf("content = %#v", got)
	}
	if _, exists := prepared[0]["parts"]; exists {
		t.Fatal("legacy parts field must not be sent upstream")
	}
	contents, ok := prepared[0]["contents"].([]map[string]any)
	if !ok || len(contents) != 3 {
		t.Fatalf("contents = %#v", prepared[0]["contents"])
	}
	for _, part := range contents[1:] {
		imageURL, _ := part["image_url"].(map[string]any)
		if part["type"] != "image_url" || imageURL["url"] != imageURLs[0] {
			t.Fatalf("image part = %#v", part)
		}
	}
}

func TestBuildLingmaBodyVisionUsesNativeRouteAndModelMetadata(t *testing.T) {
	model := &ModelInfo{
		Key:            "qmodel_latest",
		DisplayName:    "Qwen3.7-Max",
		Format:         "dashscope",
		Source:         "system",
		IsVL:           true,
		MaxInputTokens: 180000,
	}
	body := BuildLingmaBodyWithOptions(
		[]map[string]any{{"role": "user", "content": "what is this?"}},
		nil,
		model.Key,
		nil,
		nil,
		LingmaBodyOptions{IsVL: true, ImageURLs: []string{"https://lingma-vl.example/image.png"}, ModelInfo: model},
	)

	if body["request_set_id"] == "" || body["request_set_id"] != body["chat_record_id"] {
		t.Fatalf("request_set_id = %#v, chat_record_id = %#v", body["request_set_id"], body["chat_record_id"])
	}
	if body["source"] != 1 || body["task_id"] != "common" || body["chat_task"] != "common" || body["session_type"] != "assistant" {
		t.Fatalf("native VL route metadata is incomplete: %#v", body)
	}
	config := body["model_config"].(map[string]any)
	if config["is_vl"] != true || config["enable"] != true || config["source"] != "system" || config["format"] != "dashscope" {
		t.Fatalf("model_config = %#v", config)
	}
	if config["display_name"] != model.DisplayName || config["max_input_tokens"] != model.MaxInputTokens {
		t.Fatalf("model metadata = %#v", config)
	}
}

func TestPrepareVisionRequestRejectsNonVLModelBeforeUpload(t *testing.T) {
	oldCache, oldTime, oldValid := modelCache, modelCacheTime, modelCacheValid
	t.Cleanup(func() {
		modelCache, modelCacheTime, modelCacheValid = oldCache, oldTime, oldValid
	})
	modelCache = []ModelInfo{{Key: "text-only", IsVL: false}}
	modelCacheTime = time.Now()
	modelCacheValid = true

	handler := NewBridgeHandler(&auth.Session{CosyKey: "test-key"}, nil)
	// The model capability check happens before image decoding/uploading.
	_, _, _, requestErr := handler.prepareVisionRequest(context.Background(), "text-only", []map[string]any{{
		"role": "user",
		"content": []map[string]any{{
			"type":      "image_url",
			"image_url": "data:image/png;base64," + tinyPNGBase64,
		}},
	}})
	if requestErr == nil || requestErr.Status != http.StatusBadRequest || requestErr.Code != "vision_model_unsupported" {
		t.Fatalf("request error = %#v", requestErr)
	}
}

func TestAnthropicToolResultImageHitsCurrentVisionBoundary(t *testing.T) {
	messages := anthropicToOpenAIMessages(nil, []map[string]any{{
		"role": "user",
		"content": []any{map[string]any{
			"type":        "tool_result",
			"tool_use_id": "toolu_read_image",
			"content": []any{map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": "image/png",
					"data":       tinyPNGBase64,
				},
			}},
		}},
	}})
	if len(messages) != 1 || messages[0]["role"] != "tool" {
		t.Fatalf("converted tool-result messages = %#v", messages)
	}
	hasImages, requestErr := validateVisionMessageInputs(messages)
	if !hasImages || requestErr == nil || requestErr.Code != "unsupported_image_location" {
		t.Fatalf("hasImages = %t, request error = %#v", hasImages, requestErr)
	}
}

func TestVisionValidationAndRedaction(t *testing.T) {
	_, _, err := decodeVisionDataURI("data:image/png,not-base64")
	if err == nil {
		t.Fatal("expected non-base64 data URI rejection")
	}
	if isPublicVisionIP(net.ParseIP("127.0.0.1")) {
		t.Fatal("loopback address must not be accepted for remote image fetching")
	}

	_, requestErr := validateVisionMessageInputs([]map[string]any{{
		"role": "assistant",
		"content": []map[string]any{{
			"type":      "image_url",
			"image_url": "data:image/png;base64," + tinyPNGBase64,
		}},
	}})
	if requestErr == nil || requestErr.Code != "unsupported_image_location" {
		t.Fatalf("assistant image error = %#v", requestErr)
	}

	redacted := redactVisionLogValue(map[string]any{
		"image_urls": []string{"https://lingma-vl.example/image.png"},
		"messages": []any{map[string]any{
			"image_url": "data:image/png;base64," + tinyPNGBase64,
		}},
	}).(map[string]any)
	if strings.Contains(fmt.Sprint(redacted), tinyPNGBase64) || strings.Contains(fmt.Sprint(redacted), "lingma-vl.example") {
		t.Fatalf("sensitive image payload was not redacted: %#v", redacted)
	}
}

func TestVisionTranslationAcrossCompatibleProtocols(t *testing.T) {
	oldCache, oldTime, oldValid := modelCache, modelCacheTime, modelCacheValid
	t.Cleanup(func() {
		modelCache, modelCacheTime, modelCacheValid = oldCache, oldTime, oldValid
	})
	modelCache = []ModelInfo{{
		Key:            "qmodel_latest",
		DisplayName:    "Qwen3.7-Max",
		Format:         "dashscope",
		Source:         "system",
		IsVL:           true,
		MaxInputTokens: 180000,
	}}
	modelCacheTime = time.Now()
	modelCacheValid = true

	dataURI := "data:image/png;base64," + tinyPNGBase64
	scenarios := []struct {
		name    string
		path    string
		body    string
		handler func(*BridgeHandler, http.ResponseWriter, *http.Request)
	}{
		{
			name:    "chat_completions",
			path:    "/v1/chat/completions",
			body:    fmt.Sprintf(`{"model":"qmodel_latest","stream":false,"messages":[{"role":"user","content":[{"type":"text","text":"name it"},{"type":"image_url","image_url":{"url":%q}}]}]}`, dataURI),
			handler: (*BridgeHandler).HandleOpenAIChat,
		},
		{
			name:    "responses",
			path:    "/v1/responses",
			body:    fmt.Sprintf(`{"model":"qmodel_latest","stream":false,"input":[{"role":"user","content":[{"type":"input_text","text":"name it"},{"type":"input_image","image_url":%q}]}]}`, dataURI),
			handler: (*BridgeHandler).HandleOpenAIResponses,
		},
		{
			name:    "anthropic_messages",
			path:    "/v1/messages",
			body:    fmt.Sprintf(`{"model":"qmodel_latest","stream":false,"max_tokens":128,"messages":[{"role":"user","content":[{"type":"text","text":"name it"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":%q}}]}]}`, tinyPNGBase64),
			handler: (*BridgeHandler).HandleAnthropicMessages,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			handler := NewBridgeHandler(&auth.Session{CosyKey: "test-key", UID: "test-user"}, func(*proto.GatewayLog) {})
			handler.client.visionUploadURL = "https://upload.test/algo/api/v2/image/upload"
			handler.client.maxAttempts = 1
			uploads := 0
			var captured map[string]any
			handler.client.client = &http.Client{Transport: &mockTransport{roundTripFunc: func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPut {
					uploads++
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"result":{"url":"https://lingma-vl.example/tower.png"},"success":true}`)),
					}, nil
				}
				payload, err := decodeLingmaRequestBody(req)
				if err != nil {
					t.Fatalf("decode Lingma request: %v", err)
				}
				captured = payload
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(successLingmaStreamBody())),
				}, nil
			}}}

			req := httptest.NewRequest(http.MethodPost, scenario.path, strings.NewReader(scenario.body))
			resp := httptest.NewRecorder()
			scenario.handler(handler, resp, req)
			if resp.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", resp.Code, resp.Body.String())
			}
			if uploads != 1 {
				t.Fatalf("uploads = %d, want 1", uploads)
			}
			if captured == nil {
				t.Fatal("chat request was not sent")
			}
			config, _ := captured["model_config"].(map[string]any)
			if config["is_vl"] != true || config["enable"] != true || config["source"] != "system" {
				t.Fatalf("model_config = %#v", config)
			}
			if captured["task_id"] != "common" || captured["source"] != float64(1) {
				t.Fatalf("VL route metadata = %#v", captured)
			}
			messages, _ := captured["messages"].([]any)
			last, _ := messages[len(messages)-1].(map[string]any)
			contents, _ := last["contents"].([]any)
			if len(contents) < 2 || last["parts"] != nil {
				t.Fatalf("native multimodal message = %#v", last)
			}
		})
	}
}
