package bridge

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/coolxll/lingma-tap/internal/encoding"
	"github.com/coolxll/lingma-tap/internal/proto"
)

func TestModelPassthrough_OpenAI(t *testing.T) {
	session := &auth.Session{CosyKey: "test-key"}
	handler := NewBridgeHandler(session, func(log *proto.GatewayLog) {})

	// Mock the LingmaClient to return a 404 for an unknown model
	handler.client.client.Transport = &mockTransport{
		roundTripFunc: func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				// Mock model list response
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"chat":[]}`)),
					Header:     make(http.Header),
				}, nil
			}

			// Check if the requested model in the body is our unknown model
			var body map[string]any
			bodyBytes, _ := io.ReadAll(req.Body)
			
			// The body is encoded by LingmaClient
			decodedBytes, err := encoding.Decode(string(bodyBytes))
			if err == nil {
				json.Unmarshal(decodedBytes, &body)
			} else {
				// Fallback for unencoded body (if any)
				json.Unmarshal(bodyBytes, &body)
			}

			modelConfig, _ := body["model_config"].(map[string]any)
			modelKey, _ := modelConfig["key"].(string)

			if modelKey == "unknown-model-abc" {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`{"error":"model not found"}`)),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`data: {"choices":[{"delta":{"content":"OK"}}]}` + "\n\ndata: [DONE]\n\n")),
				Header:     make(http.Header),
			}, nil
		},
	}

	reqBody := `{"model":"unknown-model-abc","messages":[{"role":"user","content":"Hi"}],"stream":true}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(reqBody))
	w := httptest.NewRecorder()

	handler.HandleOpenAIChat(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	
	t.Logf("Got status code: %d, body: %s", resp.StatusCode, string(body))

	if strings.Contains(string(body), "model not found") {
		t.Log("Successfully passed through and received error from backend")
	} else if strings.Contains(string(body), "OK") {
		t.Errorf("Should not have received OK for unknown model")
	}
}

func TestModelFallback_Anthropic(t *testing.T) {
	session := &auth.Session{CosyKey: "test-key"}
	handler := NewBridgeHandler(session, func(log *proto.GatewayLog) {})

	// Setup a mapping
	handler.UpdateAnthropicMapping(map[string]string{"sonnet": "real-sonnet-model"}, "default-fallback-model")

	handler.client.client.Transport = &mockTransport{
		roundTripFunc: func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"chat":[]}`)),
					Header:     make(http.Header),
				}, nil
			}

			var body map[string]any
			bodyBytes, _ := io.ReadAll(req.Body)
			decodedBytes, err := encoding.Decode(string(bodyBytes))
			if err == nil {
				json.Unmarshal(decodedBytes, &body)
			} else {
				json.Unmarshal(bodyBytes, &body)
			}
			
			modelConfig, _ := body["model_config"].(map[string]any)
			modelKey, _ := modelConfig["key"].(string)

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"model: " + modelKey + "\"}}]}\n\ndata: [DONE]\n\n")),
				Header:     make(http.Header),
			}, nil
		},
	}

	// Case 1: Unrecognized model name -> should use default-fallback-model
	reqBody := `{"model":"unknown-abc","messages":[{"role":"user","content":"Hi"}],"max_tokens":1024,"stream":true}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(reqBody))
	w := httptest.NewRecorder()
	handler.HandleAnthropicMessages(w, req)
	body, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), "default-fallback-model") {
		t.Errorf("Expected fallback model 'default-fallback-model', got: %s", string(body))
	}

	// Case 2: Recognized keyword -> should use mapped model
	reqBody = `{"model":"my-custom-sonnet","messages":[{"role":"user","content":"Hi"}],"max_tokens":1024,"stream":true}`
	req = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(reqBody))
	w = httptest.NewRecorder()
	handler.HandleAnthropicMessages(w, req)
	body, _ = io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), "real-sonnet-model") {
		t.Errorf("Expected mapped model 'real-sonnet-model', got: %s", string(body))
	}
}
