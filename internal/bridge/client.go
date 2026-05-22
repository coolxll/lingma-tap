package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tmaxmax/go-sse"

	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/coolxll/lingma-tap/internal/encoding"
)

const lingmaChatURL = "https://lingma-api.tongyi.aliyun.com/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1"
const lingmaModelListURL = "https://lingma-api.tongyi.aliyun.com/algo/api/v2/model/list"

type LingmaClient struct {
	session *auth.Session
	client  *http.Client
	Debug   bool
}

// streamState holds per-request state for SSE parsing.
// Created fresh for each ChatStream call to avoid concurrency issues.
type streamState struct {
	inThought bool // tracks <thought> tag state across SSE chunks
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
	// ReasoningContent is the delta.reasoning_content text (for thinking/reasoning)
	ReasoningContent string
	// ToolCalls contains tool call deltas
	ToolCalls []ToolCallDelta
	// FinishReason is set when the model finishes (e.g., "tool_calls", "stop")
	FinishReason string
	// Usage contains token usage info (from finish event)
	Usage *Usage
	// HasError indicates the event contains an error
	HasError bool
	// ErrorMsg is the error message if HasError is true
	ErrorMsg string
	// ErrorType is the error type if HasError is true
	ErrorType string
	// Raw is the raw inner JSON bytes
	Raw []byte
}

type ToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// Aliyun/Lingma aliases
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	// Cached and reasoning tokens
	CachedTokens    int `json:"cached_tokens,omitempty"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	// Nested details (for extraction from lingma response)
	PromptTokensDetails     *TokenDetails `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *TokenDetails `json:"completion_tokens_details,omitempty"`
}

// TokenDetails holds nested token detail fields.
type TokenDetails struct {
	CachedTokens    int `json:"cached_tokens,omitempty"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

func (u *Usage) Consolidate() {
	if u == nil {
		return
	}
	if u.PromptTokens == 0 && u.InputTokens != 0 {
		u.PromptTokens = u.InputTokens
	}
	if u.CompletionTokens == 0 && u.OutputTokens != 0 {
		u.CompletionTokens = u.OutputTokens
	}
	if u.TotalTokens == 0 {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	// Extract cached tokens from nested details
	if u.CachedTokens == 0 && u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0 {
		u.CachedTokens = u.PromptTokensDetails.CachedTokens
	}
	// Extract reasoning tokens from nested details
	if u.ReasoningTokens == 0 && u.CompletionTokensDetails != nil && u.CompletionTokensDetails.ReasoningTokens > 0 {
		u.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
}

// ChatStream sends a chat request to Lingma and streams SSE events.
func (c *LingmaClient) ChatStream(ctx context.Context, body map[string]any, cb func(SSEEvent) error) error {
	state := &streamState{} // per-request state

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

	return c.readSSE(resp.Body, cb, state)
}

func (c *LingmaClient) readSSE(body io.Reader, cb func(SSEEvent) error, state *streamState) error {
	doneReceived := false
	for ev, err := range sse.Read(body, nil) {
		if err != nil {
			return err
		}

		if len(ev.Data) == 0 {
			continue
		}

		if c.Debug {
			fmt.Printf("[debug] SSE Event: Type=%s, Data=%s\n", ev.Type, ev.Data)
		}

		if ev.Data == "[DONE]" {
			doneReceived = true
			return cb(SSEEvent{Type: "done"})
		}

		events, err := c.parseSSEData(ev.Data, state)
		if err != nil {
			continue // skip unparseable events
		}
		for _, event := range events {
			if event.Type == "done" {
				doneReceived = true
			}
			if err := cb(event); err != nil {
				return err
			}
		}
	}
	// Safety: inject [DONE] if the stream ended without one
	if !doneReceived {
		return cb(SSEEvent{Type: "done"})
	}
	return nil
}

func (c *LingmaClient) parseSSEData(data string, state *streamState) ([]SSEEvent, error) {
	// 1. Try to parse as the double-JSON envelope: {"headers":{...},"body":"...","statusCodeValue":200,"statusCode":"OK"}
	var envelope struct {
		Headers       map[string]any `json:"headers"`
		Body          string         `json:"body"`
		StatusCode    any            `json:"statusCode"`
		StatusCodeVal any            `json:"statusCodeValue"`
	}
	if err := json.Unmarshal([]byte(data), &envelope); err == nil && envelope.Body != "" {
		if envelope.Body == "[DONE]" {
			return []SSEEvent{{Type: "done"}}, nil
		}
		return c.parseInnerJSON(envelope.Body, state)
	}

	// 2. Try to parse as finish event: {"firstTokenDuration":...,"totalDuration":...,"serverDuration":...,"usage":...}
	var finish struct {
		FirstTokenDuration int    `json:"firstTokenDuration"`
		TotalDuration      int    `json:"totalDuration"`
		ServerDuration     int    `json:"serverDuration"`
		Usage              *Usage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &finish); err == nil && finish.TotalDuration > 0 {
		event := SSEEvent{Type: "finish", Raw: []byte(data)}
		if finish.Usage != nil {
			finish.Usage.Consolidate()
			event.Usage = finish.Usage
		}
		return []SSEEvent{event}, nil
	}

	// 3. Try to parse as direct OpenAI format (what Lingma actually returns)
	var direct struct {
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
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
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
		Usage *Usage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &direct); err == nil && (len(direct.Choices) > 0 || direct.Usage != nil || direct.Error != nil) {
		// Check for error
		if direct.Error != nil {
			return []SSEEvent{{
				Type:     "data",
				HasError: true,
				ErrorMsg: direct.Error.Message,
				ErrorType: direct.Error.Type,
				Raw:      []byte(data),
			}}, nil
		}

		return c.buildEventsFromChoices(direct.Choices, direct.Usage, []byte(data), state)
	}

	return nil, fmt.Errorf("unrecognized SSE data format")
}

func (c *LingmaClient) parseInnerJSON(body string, state *streamState) ([]SSEEvent, error) {
	var inner struct {
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
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
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
		Usage *Usage `json:"usage"`
	}

	if err := json.Unmarshal([]byte(body), &inner); err != nil {
		return nil, err
	}

	// Check for error
	if inner.Error != nil {
		return []SSEEvent{{
			Type:      "data",
			HasError:  true,
			ErrorMsg:  inner.Error.Message,
			ErrorType: inner.Error.Type,
			Raw:       []byte(body),
		}}, nil
	}

	return c.buildEventsFromChoices(inner.Choices, inner.Usage, []byte(body), state)
}

// buildEventsFromChoices processes choices array and produces SSEEvents with thought tag extraction.
func (c *LingmaClient) buildEventsFromChoices(choices []struct {
	Delta struct {
		Content          string `json:"content"`
		ReasoningContent string `json:"reasoning_content"`
		ToolCalls        []struct {
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
}, usage *Usage, raw []byte, state *streamState) ([]SSEEvent, error) {
	var events []SSEEvent

	for _, choice := range choices {
		// Extract native reasoning_content
		if choice.Delta.ReasoningContent != "" {
			events = append(events, SSEEvent{
				Type:             "data",
				ReasoningContent: choice.Delta.ReasoningContent,
				Raw:              raw,
			})
		}

		content := choice.Delta.Content
		if content != "" {
			// Split content by <thought> tags
			thoughtEvents := state.splitThoughtTags(content)
			events = append(events, thoughtEvents...)
		}

		// Tool calls
		if len(choice.Delta.ToolCalls) > 0 {
			ev := SSEEvent{Type: "data", Raw: raw}
			for _, tc := range choice.Delta.ToolCalls {
				ev.ToolCalls = append(ev.ToolCalls, ToolCallDelta{
					Index:     tc.Index,
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
			events = append(events, ev)
		}

		// Finish reason
		if choice.FinishReason != "" {
			events = append(events, SSEEvent{
				Type:         "data",
				FinishReason: choice.FinishReason,
				Raw:          raw,
			})
		}
	}

	if usage != nil {
		usage.Consolidate()
		if len(events) > 0 {
			// Attach usage to the last event
			events[len(events)-1].Usage = usage
		} else {
			events = append(events, SSEEvent{Type: "data", Usage: usage, Raw: raw})
		}
	}

	if len(events) == 0 {
		events = append(events, SSEEvent{Type: "data", Raw: raw})
	}

	return events, nil
}

// splitThoughtTags splits content by <thought>...</thought> tags.
// Uses per-stream state to handle tags spanning chunk boundaries.
func (s *streamState) splitThoughtTags(content string) []SSEEvent {
	var events []SSEEvent
	remaining := content

	for len(remaining) > 0 {
		if !s.inThought {
			startIdx := strings.Index(remaining, "<thought>")
			if startIdx == -1 {
				// No start tag — emit as content
				events = append(events, SSEEvent{Type: "data", Content: remaining})
				return events
			}
			// Emit content before the tag
			if startIdx > 0 {
				events = append(events, SSEEvent{Type: "data", Content: remaining[:startIdx]})
			}
			s.inThought = true
			remaining = remaining[startIdx+len("<thought>"):]
		} else {
			endIdx := strings.Index(remaining, "</thought>")
			if endIdx == -1 {
				// No end tag — emit as reasoning (may span to next chunk)
				events = append(events, SSEEvent{Type: "data", ReasoningContent: remaining})
				return events
			}
			// Emit reasoning content
			if endIdx > 0 {
				events = append(events, SSEEvent{Type: "data", ReasoningContent: remaining[:endIdx]})
			}
			s.inThought = false
			remaining = remaining[endIdx+len("</thought>"):]
		}
	}

	return events
}

// BuildLingmaBody constructs the full Lingma request body from translated fields.
// rawRequestJSON is used to derive a deterministic session_id; pass nil to use a random UUID.
// isReasoning controls model_config.is_reasoning.
// toolChoice is the tool_choice field (may be nil).
func BuildLingmaBody(messages []map[string]any, tools []map[string]any, modelKey string, params map[string]any, rawRequestJSON []byte, isReasoning bool, toolChoice any) map[string]any {
	requestID := newUUID()

	var sessionID string
	if len(rawRequestJSON) > 0 {
		sessionID = generateSessionID(rawRequestJSON)
	} else {
		sessionID = newUUID()
	}

	body := map[string]any{
		"request_id":       requestID,
		"request_set_id":   "",
		"chat_record_id":   requestID,
		"stream":           true,
		"image_urls":       nil,
		"is_reply":         false,
		"is_retry":         false,
		"session_id":       sessionID,
		"code_language":    "",
		"source":           0,
		"version":          "3",
		"chat_prompt":      "",
		"aliyun_user_type": "enterprise_standard",
		"agent_id":         "agent_common",
		"task_id":          "question_refine",
		"model_config": map[string]any{
			"key":                   modelKey,
			"display_name":          "",
			"model":                 "",
			"format":                "",
			"is_vl":                 false,
			"is_reasoning":          isReasoning,
			"api_key":               "",
			"url":                   "",
			"source":                "",
			"max_input_tokens":      0,
			"enable":                false,
			"price_factor":          0,
			"original_price_factor": 0,
			"is_default":            false,
			"is_new":                false,
			"exclude_tags":          nil,
			"tags":                  nil,
			"icon":                  nil,
			"strategies":            nil,
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

	if toolChoice != nil {
		body["tool_choice"] = toolChoice
	}

	return body
}

// generateSessionID produces a deterministic session ID from the request content.
func generateSessionID(rawJSON []byte) string {
	hash := sha256.Sum256(rawJSON)
	return hex.EncodeToString(hash[:16])
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

var uuidGenerator = func() string {
	return uuid.New().String()
}

func newUUID() string {
	return uuidGenerator()
}
