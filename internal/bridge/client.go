package bridge

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tmaxmax/go-sse"

	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/coolxll/lingma-tap/internal/encoding"
)

const lingmaChatURL = "https://lingma-api.tongyi.aliyun.com/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1"
const lingmaModelListURL = "https://lingma-api.tongyi.aliyun.com/algo/api/v2/model/list"

// buildLingmaChatURL constructs the Lingma chat endpoint URL with the given agentID.
// It defaults to "agent_common" if agentID is empty and properly URL-encodes all query parameters.
func buildLingmaChatURL(agentID string) string {
	if agentID == "" {
		agentID = "agent_common"
	}
	u := &url.URL{
		Scheme: "https",
		Host:   "lingma-api.tongyi.aliyun.com",
		Path:   "/algo/api/v2/service/pro/sse/agent_chat_generation",
	}
	q := u.Query()
	q.Set("FetchKeys", "llm_model_result")
	q.Set("AgentId", agentID)
	q.Set("Encode", "1")
	u.RawQuery = q.Encode()
	return u.String()
}

type LingmaClient struct {
	mu                      sync.RWMutex
	session                 *auth.Session
	client                  *http.Client
	visionUploadURL         string
	visionFetcher           func(context.Context, string) ([]byte, string, error)
	maxAttempts             int
	retryBaseDelay          time.Duration
	firstActionableTimeout  time.Duration
	thinkingRecoveryEnabled bool
	Debug                   bool
}

// streamState holds per-request state for SSE parsing.
// Created fresh for each ChatStream call to avoid concurrency issues.
type streamState struct {
	inThought bool // tracks <thought> tag state across SSE chunks

	inToolXML     bool
	toolXMLBuffer strings.Builder
	toolXMLPrefix string
	nextToolIndex int
}

func NewLingmaClient(session *auth.Session) *LingmaClient {
	maxAttempts, retryBaseDelay, firstActionableTimeout := loadLingmaUpstreamRetryConfig()
	thinkingRecoveryEnabled, _ := loadLingmaThinkingFallbackConfig()
	visionUploadURL := strings.TrimSpace(os.Getenv("LINGMA_IMAGE_UPLOAD_URL"))
	if visionUploadURL == "" {
		visionUploadURL = lingmaImageUploadURL
	}
	return &LingmaClient{
		session:                 session,
		visionUploadURL:         visionUploadURL,
		visionFetcher:           fetchRemoteVisionImage,
		maxAttempts:             maxAttempts,
		retryBaseDelay:          retryBaseDelay,
		firstActionableTimeout:  firstActionableTimeout,
		thinkingRecoveryEnabled: thinkingRecoveryEnabled,
		client:                  newLingmaStreamingHTTPClient(),
	}
}

func newLingmaHTTPClient() *http.Client {
	return newLingmaHTTPClientWithHTTP2(lingmaHTTP2Enabled())
}

func newLingmaHTTPClientWithHTTP2(enabled bool) *http.Client {
	return &http.Client{
		Timeout:   5 * time.Minute,
		Transport: newLingmaTransport(enabled),
	}
}

func newLingmaTransport(http2Enabled bool) http.RoundTripper {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	tr := base.Clone()
	if http2Enabled {
		tr.ForceAttemptHTTP2 = true
		return tr
	}

	tr.ForceAttemptHTTP2 = false
	tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	return tr
}

func lingmaHTTP2Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LINGMA_HTTP2"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		// Default to HTTP/2 disabled to align with test expectations and streaming stability.
		return false
	}
}

func DefaultLingmaHTTP2Enabled() bool {
	return lingmaHTTP2Enabled()
}

func (c *LingmaClient) httpClient() *http.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client
}

func (c *LingmaClient) SetHTTP2Enabled(enabled bool) {
	c.mu.Lock()
	oldClient := c.client
	if enabled {
		c.client = newLingmaHTTPClientWithHTTP2(enabled)
	} else {
		c.client = newLingmaStreamingHTTPClient()
	}
	c.mu.Unlock()

	if oldClient != nil {
		oldClient.CloseIdleConnections()
	}
}

func newLingmaStreamingHTTPClient() *http.Client {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	transport := base.Clone()
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	return &http.Client{Transport: transport}
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
	// Usage contains token usage info (from finish/usage events)
	Usage *Usage
	// FirstTokenDuration is time-to-first-token in milliseconds from Lingma finish metadata.
	FirstTokenDuration int
	// HasError indicates the event contains an error
	HasError bool
	// ErrorMsg is the error message if HasError is true
	ErrorMsg string
	// ErrorType is the error type if HasError is true
	ErrorType string
	// ErrorCode is the upstream error code if present.
	ErrorCode string
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
	InputTokensDetails      *TokenDetails `json:"input_tokens_details,omitempty"`
	OutputTokensDetails     *TokenDetails `json:"output_tokens_details,omitempty"`
}

func (u *Usage) UnmarshalJSON(data []byte) error {
	type usageAlias Usage
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var alias usageAlias
	_ = json.Unmarshal(data, &alias)
	*u = Usage(alias)

	u.PromptTokens = firstInt(raw, u.PromptTokens, "prompt_tokens", "promptTokens", "input_tokens", "inputTokens", "inputTokenCount")
	u.CompletionTokens = firstInt(raw, u.CompletionTokens, "completion_tokens", "completionTokens", "output_tokens", "outputTokens", "outputTokenCount")
	u.TotalTokens = firstInt(raw, u.TotalTokens, "total_tokens", "totalTokens", "totalTokenCount")
	u.InputTokens = firstInt(raw, u.InputTokens, "input_tokens", "inputTokens", "prompt_tokens", "promptTokens", "inputTokenCount")
	u.OutputTokens = firstInt(raw, u.OutputTokens, "output_tokens", "outputTokens", "completion_tokens", "completionTokens", "outputTokenCount")
	u.CachedTokens = firstInt(raw, u.CachedTokens, "cached_tokens", "cachedTokens")
	if u.CachedTokens == 0 {
		u.CachedTokens = firstInt(raw, 0, "cache_read_input_tokens", "cacheReadInputTokens")
	}
	u.ReasoningTokens = firstInt(raw, u.ReasoningTokens, "reasoning_tokens", "reasoningTokens", "thinking_tokens", "thinkingTokens")

	u.Consolidate()
	return nil
}

func rawInt(raw map[string]json.RawMessage, key string) (int, bool) {
	v, ok := raw[key]
	if !ok || string(v) == "null" {
		return 0, false
	}
	var n int
	if err := json.Unmarshal(v, &n); err == nil {
		return n, true
	}
	var f float64
	if err := json.Unmarshal(v, &f); err == nil {
		return int(f), true
	}
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		var parsed int
		if _, err := fmt.Sscanf(s, "%d", &parsed); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func firstInt(raw map[string]json.RawMessage, current int, keys ...string) int {
	if current != 0 {
		return current
	}
	foundZero := false
	for _, key := range keys {
		n, ok := rawInt(raw, key)
		if !ok {
			continue
		}
		if n != 0 {
			return n
		}
		foundZero = true
	}
	if foundZero {
		return 0
	}
	return current
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
	if u.InputTokens == 0 && u.PromptTokens != 0 {
		u.InputTokens = u.PromptTokens
	}
	if u.CompletionTokens == 0 && u.OutputTokens != 0 {
		u.CompletionTokens = u.OutputTokens
	}
	if u.OutputTokens == 0 && u.CompletionTokens != 0 {
		u.OutputTokens = u.CompletionTokens
	}
	if u.TotalTokens == 0 {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	// Extract cached tokens from nested details.
	if u.CachedTokens == 0 && u.InputTokensDetails != nil && u.InputTokensDetails.CachedTokens > 0 {
		u.CachedTokens = u.InputTokensDetails.CachedTokens
	}
	if u.CachedTokens == 0 && u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0 {
		u.CachedTokens = u.PromptTokensDetails.CachedTokens
	}
	// Extract reasoning tokens from nested details.
	if u.ReasoningTokens == 0 && u.OutputTokensDetails != nil && u.OutputTokensDetails.ReasoningTokens > 0 {
		u.ReasoningTokens = u.OutputTokensDetails.ReasoningTokens
	}
	if u.ReasoningTokens == 0 && u.CompletionTokensDetails != nil && u.CompletionTokensDetails.ReasoningTokens > 0 {
		u.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
}

// chatStreamOnce sends one upstream request. ChatStream wraps this with
// retry/recovery behavior before exposing events to the downstream client.
func (c *LingmaClient) chatStreamOnce(ctx context.Context, body map[string]any, cb func(SSEEvent) error) error {
	state := &streamState{} // per-request state

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	encodedBody := encoding.Encode(bodyJSON)

	// Determine the correct URL based on the agent_id in the body
	agentID, _ := body["agent_id"].(string)
	chatURL := buildLingmaChatURL(agentID)

	headers, err := c.session.BuildHeaders(encodedBody, chatURL)
	if err != nil {
		return fmt.Errorf("build headers: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", chatURL, strings.NewReader(encodedBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	if c.Debug {
		fmt.Printf("[debug] Lingma response: status=%d proto=%s\n", resp.StatusCode, resp.Proto)
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return &lingmaHTTPError{
			StatusCode: resp.StatusCode,
			Body:       string(bodyBytes),
			RetryAfter: parseLingmaRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
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
			for _, event := range state.flushPendingContentEvents() {
				if err := cb(event); err != nil {
					return err
				}
			}
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
	if doneReceived {
		return nil
	}

	for _, event := range state.flushPendingContentEvents() {
		if err := cb(event); err != nil {
			return err
		}
	}
	return io.ErrUnexpectedEOF
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

	if event, ok := parseLingmaErrorEnvelope([]byte(data)); ok {
		return []SSEEvent{event}, nil
	}

	// 2. Try to parse as finish event: {"firstTokenDuration":...,"totalDuration":...,"serverDuration":...,"usage":...}
	var finish struct {
		FirstTokenDuration *int   `json:"firstTokenDuration"`
		TotalDuration      *int   `json:"totalDuration"`
		ServerDuration     *int   `json:"serverDuration"`
		Usage              *Usage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &finish); err == nil &&
		(finish.TotalDuration != nil || finish.ServerDuration != nil || finish.FirstTokenDuration != nil) {
		firstTokenDuration := 0
		if finish.FirstTokenDuration != nil {
			firstTokenDuration = *finish.FirstTokenDuration
		}
		event := SSEEvent{Type: "finish", Raw: []byte(data), FirstTokenDuration: firstTokenDuration}
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
				Type:      "data",
				HasError:  true,
				ErrorMsg:  direct.Error.Message,
				ErrorType: direct.Error.Type,
				Raw:       []byte(data),
			}}, nil
		}

		return c.buildEventsFromChoices(direct.Choices, direct.Usage, []byte(data), state)
	}

	return nil, fmt.Errorf("unrecognized SSE data format")
}

func (c *LingmaClient) parseInnerJSON(body string, state *streamState) ([]SSEEvent, error) {
	if event, ok := parseLingmaErrorEnvelope([]byte(body)); ok {
		return []SSEEvent{event}, nil
	}

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

func parseLingmaErrorEnvelope(raw []byte) (SSEEvent, bool) {
	var upstream struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
		Type    string `json:"type"`
	}
	if err := json.Unmarshal(raw, &upstream); err != nil || upstream.Message == "" || upstream.Code == nil {
		return SSEEvent{}, false
	}
	return SSEEvent{
		Type:      "data",
		HasError:  true,
		ErrorMsg:  upstream.Message,
		ErrorType: upstream.Type,
		ErrorCode: fmt.Sprint(upstream.Code),
		Raw:       raw,
	}, true
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
			events = append(events, state.splitContentEvents(content, len(choice.Delta.ToolCalls) == 0)...)
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
			events = append(events, state.flushPendingContentEvents()...)
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

func (s *streamState) splitContentEvents(content string, parseToolXML bool) []SSEEvent {
	if !parseToolXML {
		s.discardPendingToolXML()
		return s.splitThoughtTags(stripCompleteToolXML(content))
	}

	if s.toolXMLPrefix != "" {
		content = s.toolXMLPrefix + content
		s.toolXMLPrefix = ""
	}

	var events []SSEEvent
	remaining := content
	appendText := func(text string) {
		if text != "" {
			events = append(events, s.splitThoughtTags(text)...)
		}
	}

	for len(remaining) > 0 {
		if s.inToolXML {
			// Search the buffered content plus the new chunk together so that a
			// closing tag split across the buffer/chunk boundary is detected.
			combined := s.toolXMLBuffer.String() + remaining
			closeIdx, closeLen := findToolXMLClose(combined)
			if closeIdx == -1 {
				s.toolXMLBuffer.WriteString(remaining)
				return events
			}
			fullBlock := combined[:closeIdx+closeLen]
			if tc, ok := parseToolCallXML(fullBlock, s.nextToolIndex); ok {
				s.nextToolIndex++
				events = append(events, SSEEvent{Type: "data", ToolCalls: []ToolCallDelta{tc}})
			}
			s.toolXMLBuffer.Reset()
			s.inToolXML = false
			remaining = combined[closeIdx+closeLen:]
			continue
		}

		startIdx := findToolXMLStart(remaining)
		if startIdx == -1 {
			keep := trailingToolXMLStartPrefixLen(remaining)
			if keep > 0 {
				appendText(remaining[:len(remaining)-keep])
				s.toolXMLPrefix = remaining[len(remaining)-keep:]
			} else {
				appendText(remaining)
			}
			return events
		}
		appendText(remaining[:startIdx])
		remaining = remaining[startIdx:]
		closeIdx, closeLen := findToolXMLClose(remaining)
		if closeIdx == -1 {
			s.inToolXML = true
			s.toolXMLBuffer.WriteString(remaining)
			return events
		}
		xmlBlock := remaining[:closeIdx+closeLen]
		if tc, ok := parseToolCallXML(xmlBlock, s.nextToolIndex); ok {
			s.nextToolIndex++
			events = append(events, SSEEvent{Type: "data", ToolCalls: []ToolCallDelta{tc}})
		}
		remaining = remaining[closeIdx+closeLen:]
	}

	return events
}

func (s *streamState) discardPendingToolXML() {
	s.inToolXML = false
	s.toolXMLBuffer.Reset()
	s.toolXMLPrefix = ""
}

func (s *streamState) flushPendingContentEvents() []SSEEvent {
	var events []SSEEvent
	// Flush any buffered incomplete tool-call XML as normal text, since no
	// complete tool call was ever parsed.
	if s.inToolXML && s.toolXMLBuffer.Len() > 0 {
		events = append(events, s.splitThoughtTags(s.toolXMLBuffer.String())...)
		s.toolXMLBuffer.Reset()
		s.inToolXML = false
	}
	if s.toolXMLPrefix != "" {
		events = append(events, s.splitThoughtTags(s.toolXMLPrefix)...)
		s.toolXMLPrefix = ""
	}
	return events
}

func findToolXMLStart(s string) int {
	idx := -1
	for _, marker := range []string{"<tool_call", "<function_call"} {
		if i := strings.Index(s, marker); i >= 0 && (idx == -1 || i < idx) {
			idx = i
		}
	}
	return idx
}

func findToolXMLClose(s string) (int, int) {
	idx := -1
	markerLen := 0
	for _, marker := range []string{"</tool_call>", "</function_call>"} {
		if i := strings.Index(s, marker); i >= 0 && (idx == -1 || i < idx) {
			idx = i
			markerLen = len(marker)
		}
	}
	return idx, markerLen
}

func stripCompleteToolXML(content string) string {
	var out strings.Builder
	remaining := content
	for len(remaining) > 0 {
		startIdx := findToolXMLStart(remaining)
		if startIdx == -1 {
			out.WriteString(remaining)
			break
		}
		out.WriteString(remaining[:startIdx])
		afterStart := remaining[startIdx:]
		closeIdx, closeLen := findToolXMLClose(afterStart)
		if closeIdx == -1 {
			out.WriteString(afterStart)
			break
		}
		remaining = afterStart[closeIdx+closeLen:]
	}
	return out.String()
}

func trailingToolXMLStartPrefixLen(s string) int {
	maxLen := 0
	for _, marker := range []string{"<tool_call", "<function_call"} {
		limit := len(marker) - 1
		if len(s) < limit {
			limit = len(s)
		}
		for n := limit; n >= 1; n-- {
			if strings.HasSuffix(s, marker[:n]) && n > maxLen {
				maxLen = n
				break
			}
		}
	}
	return maxLen
}

func parseToolCallXML(xmlBlock string, index int) (ToolCallDelta, bool) {
	inner := stripOuterToolTag(xmlBlock)
	attrName := extractXMLAttr(xmlBlock, "name")
	attrID := extractXMLAttr(xmlBlock, "id")
	var payload map[string]any
	if err := json.Unmarshal([]byte(inner), &payload); err != nil {
		payload = map[string]any{
			"name":      firstNonEmpty(attrName, extractXMLField(inner, "name")),
			"arguments": firstNonEmpty(extractXMLField(inner, "arguments"), extractXMLField(inner, "input"), extractXMLField(inner, "parameters")),
		}
	}

	name, _ := payload["name"].(string)
	args := payload["arguments"]
	if name == "" {
		name, _ = payload["tool_name"].(string)
	}
	if name == "" {
		name, _ = payload["tool"].(string)
	}
	if name == "" {
		name, _ = payload["toolName"].(string)
	}
	if name == "" {
		name = attrName
	}
	if name == "" {
		if fn, ok := payload["function"].(map[string]any); ok {
			name, _ = fn["name"].(string)
			args = fn["arguments"]
		}
	}
	if args == nil {
		args = firstPresent(payload, "input", "parameters", "args")
	}
	if args == nil && attrName != "" && len(payload) > 0 {
		args = payload
	}
	if args == nil && len(payload) > 0 {
		if extra := toolArgumentFields(payload); len(extra) > 0 {
			args = extra
		}
	}
	if name == "" {
		return ToolCallDelta{}, false
	}

	return ToolCallDelta{
		Index:     index,
		ID:        firstNonEmpty(stringValue(payload["id"]), attrID),
		Name:      name,
		Arguments: normalizeToolArguments(args),
	}, true
}

func stripOuterToolTag(xmlBlock string) string {
	start := indexXMLTagEnd(xmlBlock)
	end, _ := findToolXMLClose(xmlBlock)
	if start == -1 || end == -1 || end <= start {
		return strings.TrimSpace(xmlBlock)
	}
	return strings.TrimSpace(html.UnescapeString(xmlBlock[start+1 : end]))
}

func extractXMLField(s, name string) string {
	startTag := "<" + name + ">"
	endTag := "</" + name + ">"
	start := strings.Index(s, startTag)
	end := strings.Index(s, endTag)
	if start == -1 || end == -1 || end <= start {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(s[start+len(startTag) : end]))
}

func extractXMLAttr(s, name string) string {
	tagEnd := indexXMLTagEnd(s)
	if tagEnd == -1 {
		return ""
	}
	openTag := s[:tagEnd]
	for _, quote := range []byte{'"', '\''} {
		prefix := name + "=" + string(quote)
		start := strings.Index(openTag, prefix)
		if start == -1 {
			continue
		}
		start += len(prefix)
		end := strings.IndexByte(openTag[start:], quote)
		if end == -1 {
			return ""
		}
		return strings.TrimSpace(html.UnescapeString(openTag[start : start+end]))
	}
	return ""
}

// indexXMLTagEnd finds the position of '>' that closes an XML open tag,
// skipping '>' characters inside quoted attribute values.
func indexXMLTagEnd(s string) int {
	inQuote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
			}
		} else {
			if c == '"' || c == '\'' {
				inQuote = c
			} else if c == '>' {
				return i
			}
		}
	}
	return -1
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstPresent(payload map[string]any, keys ...string) any {
	for _, key := range keys {
		if v, ok := payload[key]; ok {
			return v
		}
	}
	return nil
}

func toolArgumentFields(payload map[string]any) map[string]any {
	args := make(map[string]any)
	for key, value := range payload {
		switch key {
		case "id", "name", "tool", "tool_name", "toolName", "function", "arguments", "input", "parameters", "args":
			continue
		default:
			args[key] = value
		}
	}
	return args
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func normalizeToolArguments(v any) string {
	switch t := v.(type) {
	case nil:
		return "{}"
	case string:
		if strings.TrimSpace(t) == "" {
			return "{}"
		}
		return t
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return "{}"
		}
		return string(b)
	}
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
// LingmaBodyOptions controls request metadata that is independent from the
// translated messages and sampling parameters.
type LingmaBodyOptions struct {
	IsReasoning bool
	IsVL        bool
	ImageURLs   []string
	ModelInfo   *ModelInfo
	ToolChoice  any
}

func BuildLingmaBodyWithOptions(messages []map[string]any, tools []map[string]any, modelKey string, params map[string]any, rawRequestJSON []byte, options LingmaBodyOptions) map[string]any {
	requestID := newUUID()
	messages = mergeReasoningContentIntoMessages(messages)

	var sessionID string
	if len(rawRequestJSON) > 0 {
		sessionID = generateSessionID(rawRequestJSON)
	} else {
		sessionID = newUUID()
	}

	// Determine agent_id and source based on model and reasoning status.
	// kmodel and mmodel always use agent_common with empty source.
	// All other models default to agent_chat when reasoning, agent_common otherwise.
	var agentID, modelConfigSource string
	switch modelKey {
	case "kmodel", "mmodel":
		agentID = "agent_common"
		modelConfigSource = ""
	default:
		if options.IsReasoning {
			agentID = "agent_chat"
			modelConfigSource = "system"
		} else {
			agentID = "agent_common"
			modelConfigSource = ""
		}
	}

	var imageURLs any
	if len(options.ImageURLs) > 0 {
		imageURLs = append([]string(nil), options.ImageURLs...)
	}
	requestSetID := ""
	taskID := "question_refine"
	source := 0
	modelConfig := map[string]any{
		"key":                   modelKey,
		"display_name":          "",
		"model":                 "",
		"format":                "",
		"is_vl":                 options.IsVL,
		"is_reasoning":          options.IsReasoning,
		"api_key":               "",
		"url":                   "",
		"source":                modelConfigSource,
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
	}
	if options.IsVL {
		// Native VL requests use the common task route and a fully populated
		// model configuration. The upstream silently treats the request as
		// text-only when only is_vl/image_urls are present.
		requestSetID = requestID
		taskID = "common"
		source = 1
		if options.ModelInfo != nil {
			modelConfig["display_name"] = options.ModelInfo.DisplayName
			modelConfig["format"] = options.ModelInfo.Format
			modelConfig["source"] = options.ModelInfo.Source
			modelConfig["max_input_tokens"] = options.ModelInfo.MaxInputTokens
			modelConfig["enable"] = true
		}
	}
	body := map[string]any{
		"request_id":       requestID,
		"request_set_id":   requestSetID,
		"chat_record_id":   requestID,
		"stream":           true,
		"image_urls":       imageURLs,
		"is_reply":         false,
		"is_retry":         false,
		"session_id":       sessionID,
		"code_language":    "",
		"source":           source,
		"version":          "3",
		"chat_prompt":      "",
		"aliyun_user_type": "enterprise_standard",
		"agent_id":         agentID,
		"task_id":          taskID,
		"model_config":     modelConfig,
		"messages":         messages,
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
	if options.IsVL {
		body["chat_task"] = "common"
		body["session_type"] = "assistant"
	}

	if len(params) > 0 {
		body["parameters"] = params
	} else {
		body["parameters"] = map[string]any{"temperature": 0.1}
	}

	if len(tools) > 0 {
		body["tools"] = tools
	}

	if options.ToolChoice != nil {
		body["tool_choice"] = options.ToolChoice
	}

	return body
}

// BuildLingmaBody is kept as a compatibility wrapper for internal replay and
// legacy tests. New request paths should use BuildLingmaBodyWithOptions.
func BuildLingmaBody(messages []map[string]any, tools []map[string]any, modelKey string, params map[string]any, rawRequestJSON []byte, isReasoning bool, toolChoice any) map[string]any {
	return BuildLingmaBodyWithOptions(messages, tools, modelKey, params, rawRequestJSON, LingmaBodyOptions{
		IsReasoning: isReasoning,
		ToolChoice:  toolChoice,
	})
}

// mergeReasoningContentIntoMessages preserves prior-turn reasoning for Lingma,
// whose chat protocol carries thought text inside message content rather than
// an OpenAI reasoning_content field.
func mergeReasoningContentIntoMessages(messages []map[string]any) []map[string]any {
	if len(messages) == 0 {
		return messages
	}
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			result = append(result, nil)
			continue
		}
		copyMessage := make(map[string]any, len(message))
		for key, value := range message {
			copyMessage[key] = value
		}
		reasoning, ok := copyMessage["reasoning_content"].(string)
		if !ok || strings.TrimSpace(reasoning) == "" {
			result = append(result, copyMessage)
			continue
		}
		delete(copyMessage, "reasoning_content")
		thought := "<thought>" + reasoning + "</thought>"
		switch content := copyMessage["content"].(type) {
		case string:
			if content == "" {
				copyMessage["content"] = thought
			} else {
				copyMessage["content"] = thought + "\n" + content
			}
		case []map[string]any:
			parts := make([]any, 0, len(content)+1)
			parts = append(parts, map[string]any{"type": "text", "text": thought})
			for _, part := range content {
				parts = append(parts, part)
			}
			copyMessage["content"] = parts
		case []any:
			parts := make([]any, 0, len(content)+1)
			parts = append(parts, map[string]any{"type": "text", "text": thought})
			parts = append(parts, content...)
			copyMessage["content"] = parts
		case nil:
			copyMessage["content"] = thought
		default:
			copyMessage["content"] = thought + "\n" + fmt.Sprint(content)
		}
		result = append(result, copyMessage)
	}
	return result
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

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	if c.Debug {
		fmt.Printf("[debug] Lingma model list response: status=%d proto=%s\n", resp.StatusCode, resp.Proto)
	}

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
