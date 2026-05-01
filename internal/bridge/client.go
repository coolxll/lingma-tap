package bridge

import (
	"bufio"
	"context"
	crypto_rand "crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/coolxll/lingma-tap/internal/encoding"
)

const lingmaChatURL = "https://lingma-api.tongyi.aliyun.com/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1"
const lingmaModelListURL = "https://lingma-api.tongyi.aliyun.com/algo/api/v2/model/list"

type LingmaClient struct {
	session *auth.Session
	client  *http.Client
}

func NewLingmaClient(session *auth.Session) *LingmaClient {
	return &LingmaClient{
		session: session,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

// SSEEvent represents a parsed SSE event from the Lingma API.
type SSEEvent struct {
	// Type is "data", "finish", or "done"
	Type string
	// Content is the delta.content text (for text streaming)
	Content string
	// ToolCalls contains tool call deltas
	ToolCalls []ToolCallDelta
	// FinishReason is set when the model finishes (e.g., "tool_calls", "stop")
	FinishReason string
	// Usage contains token usage info (from finish event)
	Usage *Usage
	// Raw is the raw inner JSON bytes
	Raw []byte
}

type ToolCallDelta struct {
	Index    int
	ID       string
	Name     string
	Arguments string
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// Aliyun/Lingma aliases
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (u *Usage) Consolidate() {
	if u.PromptTokens == 0 && u.InputTokens != 0 {
		u.PromptTokens = u.InputTokens
	}
	if u.CompletionTokens == 0 && u.OutputTokens != 0 {
		u.CompletionTokens = u.OutputTokens
	}
	if u.TotalTokens == 0 {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
}

// ChatStream sends a chat request to Lingma and streams SSE events.
func (c *LingmaClient) ChatStream(ctx context.Context, body map[string]any, cb func(SSEEvent) error) error {
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	encodedBody := encoding.Encode(bodyJSON)

	headers, err := c.session.BuildHeaders(encodedBody, lingmaChatURL)
	if err != nil {
		return fmt.Errorf("build headers: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", lingmaChatURL, strings.NewReader(encodedBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("lingma API returned HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return c.readSSE(resp.Body, cb)
}

func (c *LingmaClient) readSSE(body io.Reader, cb func(SSEEvent) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimSpace(data)

			if data == "[DONE]" {
				return cb(SSEEvent{Type: "done"})
			}

			event, err := c.parseSSEData(data)
			if err != nil {
				continue // skip unparseable events
			}
			if err := cb(event); err != nil {
				return err
			}
		} else if strings.HasPrefix(line, "event:") {
			// event:finish is followed by a data: line with timing info
			// We handle it when we see the data line
		}
	}

	return scanner.Err()
}

func (c *LingmaClient) parseSSEData(data string) (SSEEvent, error) {
	// Try to parse as the double-JSON envelope: {"headers":{...},"body":"...","statusCodeValue":200,"statusCode":"OK"}
	var envelope struct {
		Headers        map[string]any `json:"headers"`
		Body           string         `json:"body"`
		StatusCode     any            `json:"statusCode"`
		StatusCodeVal  any            `json:"statusCodeValue"`
	}
	if err := json.Unmarshal([]byte(data), &envelope); err == nil && envelope.Body != "" {
		if envelope.Body == "[DONE]" {
			return SSEEvent{Type: "done"}, nil
		}
		return c.parseInnerJSON(envelope.Body)
	}

	// Try to parse as direct OpenAI format (what Lingma actually returns)
	var direct struct {
		Choices []struct {
			Delta struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *Usage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &direct); err == nil && (len(direct.Choices) > 0 || direct.Usage != nil) {
		event := SSEEvent{Type: "data", Raw: []byte(data)}
		for _, choice := range direct.Choices {
			if choice.Delta.Content != "" {
				event.Content = choice.Delta.Content
			}
			if choice.FinishReason != "" {
				event.FinishReason = choice.FinishReason
			}
			for _, tc := range choice.Delta.ToolCalls {
				event.ToolCalls = append(event.ToolCalls, ToolCallDelta{
					Index:     tc.Index,
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
		}
		if direct.Usage != nil {
			direct.Usage.Consolidate()
			event.Usage = direct.Usage
		}
		return event, nil
	}

	// Try to parse as finish event: {"firstTokenDuration":...,"totalDuration":...,"serverDuration":...}
	var finish struct {
		FirstTokenDuration int `json:"firstTokenDuration"`
		TotalDuration      int `json:"totalDuration"`
		ServerDuration     int `json:"serverDuration"`
	}
	if err := json.Unmarshal([]byte(data), &finish); err == nil && finish.TotalDuration > 0 {
		return SSEEvent{Type: "finish"}, nil
	}

	return SSEEvent{}, fmt.Errorf("unrecognized SSE data format")
}

func (c *LingmaClient) parseInnerJSON(body string) (SSEEvent, error) {
	var inner struct {
		Choices []struct {
			Delta struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *Usage `json:"usage"`
	}

	if err := json.Unmarshal([]byte(body), &inner); err != nil {
		return SSEEvent{}, err
	}

	event := SSEEvent{Type: "data", Raw: []byte(body)}

	for _, choice := range inner.Choices {
		if choice.Delta.Content != "" {
			event.Content = choice.Delta.Content
		}
		if choice.FinishReason != "" {
			event.FinishReason = choice.FinishReason
		}
		for _, tc := range choice.Delta.ToolCalls {
			event.ToolCalls = append(event.ToolCalls, ToolCallDelta{
				Index:     tc.Index,
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
	}

	if inner.Usage != nil {
		inner.Usage.Consolidate()
		event.Usage = inner.Usage
	}

	return event, nil
}

// BuildLingmaBody constructs the full Lingma request body from translated fields.
func BuildLingmaBody(messages []map[string]any, tools []map[string]any, modelKey string, params map[string]any) map[string]any {
	requestID := newUUID()

	body := map[string]any{
		"request_id":        requestID,
		"request_set_id":    "",
		"chat_record_id":    requestID,
		"stream":            true,
		"image_urls":        nil,
		"is_reply":          false,
		"is_retry":          false,
		"session_id":        newUUID(),
		"code_language":     "",
		"source":            0,
		"version":           "3",
		"chat_prompt":       "",
		"aliyun_user_type":  "enterprise_standard",
		"agent_id":          "agent_common",
		"task_id":           "question_refine",
		"model_config": map[string]any{
			"key":                modelKey,
			"display_name":      "",
			"model":             "",
			"format":            "",
			"is_vl":             false,
			"is_reasoning":      false,
			"api_key":           "",
			"url":               "",
			"source":            "",
			"max_input_tokens":  0,
			"enable":            false,
			"price_factor":      0,
			"original_price_factor": 0,
			"is_default":        false,
			"is_new":            false,
			"exclude_tags":      nil,
			"tags":              nil,
			"icon":              nil,
			"strategies":        nil,
		},
		"messages": messages,
		"business": map[string]any{
			"product":  "ide",
			"version":  "0.11.0",
			"type":     "chat",
			"id":       newUUID(),
			"begin_at": 0,
			"stage":    "start",
			"name":     "api-bridge",
			"relation": map[string]any{},
		},
	}

	if params != nil {
		body["parameters"] = params
	} else {
		body["parameters"] = map[string]any{"temperature": 0.1}
	}

	if len(tools) > 0 {
		body["tools"] = tools
	}

	return body
}

// ModelInfo represents a model from the Lingma model list API.
type ModelInfo struct {
	Key            string `json:"key"`
	DisplayName    string `json:"display_name"`
	Format         string `json:"format"`
	Source         string `json:"source"`
	Order          int    `json:"order"`
	IsVL           bool   `json:"is_vl"`
	IsReasoning    bool   `json:"is_reasoning"`
	MaxInputTokens int    `json:"max_input_tokens"`
}

// FetchModels queries the Lingma model list API and returns models for the "chat" category.
func (c *LingmaClient) FetchModels(ctx context.Context) ([]ModelInfo, error) {
	encodedBody := ""

	headers, err := c.session.BuildHeaders(encodedBody, lingmaModelListURL)
	if err != nil {
		return nil, fmt.Errorf("build headers: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", lingmaModelListURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("model list API returned HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		Chat      []ModelInfo `json:"chat"`
		Developer []ModelInfo `json:"developer"`
		Assistant []ModelInfo `json:"assistant"`
		Inline    []ModelInfo `json:"inline"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return result.Chat, nil
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = crypto_rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
