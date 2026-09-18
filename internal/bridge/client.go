package bridge

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	lingmawire "github.com/coolxll/lingma-protocol-go"
	"github.com/google/uuid"
	"github.com/tmaxmax/go-sse"

	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/coolxll/lingma-tap/internal/encoding"
)

const (
	defaultLingmaBaseURL  = "https://lingma-api.tongyi.aliyun.com"
	defaultQoderCNBaseURL = "https://gateway.qoder.com.cn"
	lingmaChatURL         = "https://lingma-api.tongyi.aliyun.com/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1"
	lingmaModelListURL    = "https://lingma-api.tongyi.aliyun.com/algo/api/v2/model/list"
)

func resolveBaseURL(session *auth.Session) (string, bool) {
	if envURL := strings.TrimRight(strings.TrimSpace(os.Getenv("UPSTREAM_BASE_URL")), "/"); envURL != "" {
		isQoder := strings.Contains(envURL, "qoder.com.cn") || (session != nil && session.IsQoder)
		return envURL, isQoder
	}
	if envURL := strings.TrimRight(strings.TrimSpace(os.Getenv("QODERCN_REMOTE_BASE_URL")), "/"); envURL != "" {
		return envURL, true
	}
	if envURL := strings.TrimRight(strings.TrimSpace(os.Getenv("QODERCN_BASE_URL")), "/"); envURL != "" {
		return envURL, true
	}
	if envURL := strings.TrimRight(strings.TrimSpace(os.Getenv("LINGMA_API_BASE_URL")), "/"); envURL != "" {
		isQoder := strings.Contains(envURL, "qoder.com.cn") || (session != nil && session.IsQoder)
		return envURL, isQoder
	}
	if session != nil && session.IsQoder {
		return defaultQoderCNBaseURL, true
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("QODERCN_MODE"))); v == "1" || v == "true" || v == "yes" || v == "on" {
		return defaultQoderCNBaseURL, true
	}
	return defaultLingmaBaseURL, false
}

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
	baseURL                 string
	isQoder                 bool
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
	sanitizer *lingmawire.Sanitizer
}

func newStreamState(body map[string]any) *streamState {
	return &streamState{sanitizer: lingmawire.NewSanitizer(lingmawire.SanitizeOptions{
		DSMLMode:    lingmawire.DSMLRecover,
		Role:        lingmawire.RoleAssistant,
		KnownTools:  declaredLingmaTools(body),
		ThoughtTags: true,
	})}
}

func (s *streamState) contentSanitizer() *lingmawire.Sanitizer {
	if s.sanitizer == nil {
		s.sanitizer = lingmawire.NewSanitizer(lingmawire.SanitizeOptions{
			DSMLMode:    lingmawire.DSMLRecover,
			Role:        lingmawire.RoleAssistant,
			ThoughtTags: true,
		})
	}
	return s.sanitizer
}

func declaredLingmaTools(body map[string]any) map[string]bool {
	tools, ok := body["tools"].([]map[string]any)
	if !ok {
		if generic, genericOK := body["tools"].([]any); genericOK {
			tools = make([]map[string]any, 0, len(generic))
			for _, value := range generic {
				if tool, toolOK := value.(map[string]any); toolOK {
					tools = append(tools, tool)
				}
			}
		}
	}
	if len(tools) == 0 {
		return nil
	}
	known := make(map[string]bool, len(tools))
	for _, tool := range tools {
		function, _ := tool["function"].(map[string]any)
		name, _ := function["name"].(string)
		if name == "" {
			name, _ = tool["name"].(string)
		}
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			known[name] = true
		}
	}
	if len(known) == 0 {
		return nil
	}
	return known
}

func NewLingmaClient(session *auth.Session) *LingmaClient {
	maxAttempts, retryBaseDelay, firstActionableTimeout := loadLingmaUpstreamRetryConfig()
	thinkingRecoveryEnabled, _ := loadLingmaThinkingFallbackConfig()
	baseURL, isQoder := resolveBaseURL(session)
	visionUploadURL := strings.TrimSpace(os.Getenv("LINGMA_IMAGE_UPLOAD_URL"))
	if visionUploadURL == "" {
		visionUploadURL = baseURL + "/algo/api/v2/image/upload"
	}
	return &LingmaClient{
		session:                 session,
		baseURL:                 baseURL,
		isQoder:                 isQoder,
		visionUploadURL:         visionUploadURL,
		visionFetcher:           fetchRemoteVisionImage,
		maxAttempts:             maxAttempts,
		retryBaseDelay:          retryBaseDelay,
		firstActionableTimeout:  firstActionableTimeout,
		thinkingRecoveryEnabled: thinkingRecoveryEnabled,
		client:                  newLingmaStreamingHTTPClient(),
	}
}

func (c *LingmaClient) IsQoder() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.isQoder
}

func (c *LingmaClient) BaseURL() string {
	if c == nil {
		return defaultLingmaBaseURL
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.baseURL != "" {
		return c.baseURL
	}
	return defaultLingmaBaseURL
}

func (c *LingmaClient) buildChatURL(agentID string) string {
	baseURL := c.BaseURL()
	if c.IsQoder() {
		agentID = "agent_common"
	} else if agentID == "" {
		agentID = "agent_common"
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		u = &url.URL{
			Scheme: "https",
			Host:   "lingma-api.tongyi.aliyun.com",
		}
	}
	u.Path = "/algo/api/v2/service/pro/sse/agent_chat_generation"
	q := u.Query()
	q.Set("FetchKeys", "llm_model_result")
	q.Set("AgentId", agentID)
	q.Set("Encode", "1")
	u.RawQuery = q.Encode()
	return u.String()
}

func (c *LingmaClient) modelListURL() string {
	return c.BaseURL() + "/algo/api/v2/model/list"
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

type SSEEvent = lingmawire.SSEEvent
type ToolCallDelta = lingmawire.ToolCallDelta
type Usage = lingmawire.Usage
type TokenDetails = lingmawire.TokenDetails

// chatStreamOnce sends one upstream request. ChatStream wraps this with
// retry/recovery behavior before exposing events to the downstream client.
func (c *LingmaClient) chatStreamOnce(ctx context.Context, body map[string]any, cb func(SSEEvent) error) error {
	state := newStreamState(body)

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	encodedBody := encoding.Encode(bodyJSON)

	// Determine the correct URL based on the agent_id in the body
	agentID, _ := body["agent_id"].(string)
	chatURL := c.buildChatURL(agentID)

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
		log.Printf(
			"[bridge-debug] Lingma response status=%d proto=%s content_type=%q content_length=%d trace=%q",
			resp.StatusCode,
			resp.Proto,
			resp.Header.Get("Content-Type"),
			resp.ContentLength,
			lingmaResponseTrace(resp.Header),
		)
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
	frames := 0
	dataBytes := 0
	parseFailures := 0
	for ev, err := range sse.Read(body, nil) {
		if err != nil {
			if c.Debug {
				log.Printf("[bridge-debug] Lingma SSE read failed frames=%d bytes=%d parse_failures=%d err=%q", frames, dataBytes, parseFailures, err)
			}
			return err
		}

		if len(ev.Data) == 0 {
			continue
		}
		frames++
		dataBytes += len(ev.Data)

		if ev.Data == "[DONE]" {
			doneReceived = true
			for _, event := range state.flushPendingContentEvents() {
				if err := cb(event); err != nil {
					return err
				}
			}
			c.logSanitizerDiagnostics(state)
			if c.Debug {
				log.Printf("[bridge-debug] Lingma SSE completed frames=%d bytes=%d parse_failures=%d done=explicit", frames, dataBytes, parseFailures)
			}
			return cb(SSEEvent{Type: "done"})
		}

		events, err := c.parseSSEData(ev.Data, state)
		if err != nil {
			parseFailures++
			if c.Debug {
				log.Printf(
					"[bridge-debug] Lingma SSE frame ignored frame=%d event=%q bytes=%d shape=%s err=%q",
					frames,
					ev.Type,
					len(ev.Data),
					lingmaJSONShape(ev.Data),
					err,
				)
			}
			continue // skip unparseable events
		}
		for _, event := range events {
			if c.Debug && event.HasError {
				log.Printf(
					"[bridge-debug] Lingma SSE error frame=%d code=%q type=%q message=%q shape=%s",
					frames,
					event.ErrorCode,
					event.ErrorType,
					truncateDebugValue(event.ErrorMsg, 512),
					lingmaJSONShape(ev.Data),
				)
			}
			if event.Type == "done" {
				doneReceived = true
			}
			if err := cb(event); err != nil {
				return err
			}
		}
	}
	if doneReceived {
		if c.Debug {
			log.Printf("[bridge-debug] Lingma SSE completed frames=%d bytes=%d parse_failures=%d done=event", frames, dataBytes, parseFailures)
		}
		return nil
	}

	for _, event := range state.flushPendingContentEvents() {
		if err := cb(event); err != nil {
			return err
		}
	}
	if c.Debug {
		log.Printf("[bridge-debug] Lingma SSE closed without done frames=%d bytes=%d parse_failures=%d", frames, dataBytes, parseFailures)
	}
	return io.ErrUnexpectedEOF
}

func lingmaResponseTrace(header http.Header) string {
	for _, name := range []string{"X-Request-Id", "X-Trace-Id", "Trace-Id", "Eagleeye-Traceid", "Request-Id"} {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return name + "=" + truncateDebugValue(value, 160)
		}
	}
	return ""
}

// lingmaJSONShape describes an SSE frame without logging conversation content.
func lingmaJSONShape(data string) string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return "non-json"
	}
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := []string{"keys=" + strings.Join(keys, ",")}
	if errorRaw, ok := raw["error"]; ok {
		var nested map[string]json.RawMessage
		if json.Unmarshal(errorRaw, &nested) == nil {
			nestedKeys := make([]string, 0, len(nested))
			for key := range nested {
				nestedKeys = append(nestedKeys, key)
			}
			sort.Strings(nestedKeys)
			parts = append(parts, "error_keys="+strings.Join(nestedKeys, ","))
		}
	}
	if bodyRaw, ok := raw["body"]; ok {
		var nestedBody string
		if json.Unmarshal(bodyRaw, &nestedBody) == nil && nestedBody != data {
			parts = append(parts, "body_"+lingmaJSONShape(nestedBody))
		}
	}
	return strings.Join(parts, " ")
}

func truncateDebugValue(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, strings.TrimSpace(value))
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
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
			Message string          `json:"message"`
			Type    string          `json:"type"`
			Code    any             `json:"code"`
			Details json.RawMessage `json:"details"`
		} `json:"error"`
		Usage *Usage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &direct); err == nil && (len(direct.Choices) > 0 || direct.Usage != nil || direct.Error != nil) {
		// Check for error
		if direct.Error != nil {
			message, errorType, code := resolveLingmaErrorDetails(
				direct.Error.Message,
				direct.Error.Type,
				stringifyLingmaErrorCode(direct.Error.Code),
				direct.Error.Details,
			)
			return []SSEEvent{{
				Type:      "data",
				HasError:  true,
				ErrorMsg:  message,
				ErrorType: errorType,
				ErrorCode: code,
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
			Message string          `json:"message"`
			Type    string          `json:"type"`
			Code    any             `json:"code"`
			Details json.RawMessage `json:"details"`
		} `json:"error"`
		Usage *Usage `json:"usage"`
	}

	if err := json.Unmarshal([]byte(body), &inner); err != nil {
		return nil, err
	}

	// Check for error
	if inner.Error != nil {
		message, errorType, code := resolveLingmaErrorDetails(
			inner.Error.Message,
			inner.Error.Type,
			stringifyLingmaErrorCode(inner.Error.Code),
			inner.Error.Details,
		)
		return []SSEEvent{{
			Type:      "data",
			HasError:  true,
			ErrorMsg:  message,
			ErrorType: errorType,
			ErrorCode: code,
			Raw:       []byte(body),
		}}, nil
	}

	return c.buildEventsFromChoices(inner.Choices, inner.Usage, []byte(body), state)
}

func stringifyLingmaErrorCode(code any) string {
	if code == nil {
		return ""
	}
	return fmt.Sprint(code)
}

func parseLingmaErrorEnvelope(raw []byte) (SSEEvent, bool) {
	info, ok := lingmawire.ParseError(raw)
	if !ok {
		return SSEEvent{}, false
	}
	return SSEEvent{
		Type:      "data",
		HasError:  true,
		ErrorMsg:  info.Message,
		ErrorType: info.Type,
		ErrorCode: info.Code,
		Raw:       raw,
	}, true
}

// resolveLingmaErrorDetails unwraps the provider error nested in Lingma's
// details field. Lingma may encode details as either an object or a JSON
// string containing an object. Non-JSON detail text is still more actionable
// than the generic outer "Error in upstream response" message.
func resolveLingmaErrorDetails(message, errorType, code string, details json.RawMessage) (string, string, string) {
	return lingmawire.ResolveErrorDetails(message, errorType, code, details)
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

		// Register native calls before feeding text so the shared sanitizer can
		// suppress a DSML copy of the same call instead of executing it twice.
		var nativeCalls []ToolCallDelta
		if len(choice.Delta.ToolCalls) > 0 {
			for _, tc := range choice.Delta.ToolCalls {
				nativeCalls = append(nativeCalls, ToolCallDelta{
					Index:     tc.Index,
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
			state.contentSanitizer().NoteNativeToolCalls(nativeCalls)
		}

		if choice.Delta.Content != "" {
			events = append(events, state.splitContentEvents(choice.Delta.Content, len(nativeCalls) == 0)...)
		}

		if len(nativeCalls) > 0 {
			events = append(events, SSEEvent{Type: "data", ToolCalls: nativeCalls, Raw: raw})
		}

		// Finish reason
		if choice.FinishReason != "" {
			events = append(events, state.flushPendingContentEvents()...)
			c.logSanitizerDiagnostics(state)
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

func (s *streamState) splitContentEvents(content string, recoverMarkup ...bool) []SSEEvent {
	if len(recoverMarkup) > 0 && !recoverMarkup[0] {
		// Compatibility path for callers that already have an authoritative
		// native tool call: strip a textual duplicate without recovering it.
		s.sanitizer = nil
		stripper := lingmawire.NewSanitizer(lingmawire.SanitizeOptions{
			DSMLMode:    lingmawire.DSMLStrip,
			Role:        lingmawire.RoleAssistant,
			ThoughtTags: true,
		})
		return append(stripper.Feed(content), stripper.Flush()...)
	}
	return s.contentSanitizer().Feed(content)
}

func (s *streamState) flushPendingContentEvents() []SSEEvent {
	return s.contentSanitizer().Flush()
}

func (c *LingmaClient) logSanitizerDiagnostics(state *streamState) {
	if state == nil || state.sanitizer == nil {
		return
	}
	diagnostics := state.sanitizer.Diagnostics()
	if len(diagnostics) == 0 {
		return
	}
	counts := make(map[string]int)
	for _, diagnostic := range diagnostics {
		counts[diagnostic.Kind.String()]++
	}
	kinds := make([]string, 0, len(counts))
	for kind, count := range counts {
		kinds = append(kinds, fmt.Sprintf("%s=%d", kind, count))
	}
	sort.Strings(kinds)
	log.Printf("[bridge] sanitized Lingma tool markup %s", strings.Join(kinds, " "))
}

// BuildLingmaBody constructs the full Lingma request body from translated fields.
// LingmaBodyOptions.SessionID takes precedence. Otherwise rawRequestJSON is
// used to derive a deterministic session_id; pass nil to use a random UUID.
// LingmaBodyOptions controls request metadata that is independent from the
// translated messages and sampling parameters.
type LingmaBodyOptions struct {
	IsReasoning bool
	IsVL        bool
	IsQoder     bool
	ImageURLs   []string
	ModelInfo   *ModelInfo
	ToolChoice  any
	SessionID   string
}

func BuildLingmaBodyWithOptions(messages []map[string]any, tools []map[string]any, modelKey string, params map[string]any, rawRequestJSON []byte, options LingmaBodyOptions) map[string]any {
	requestID := newUUID()
	messages = mergeReasoningContentIntoMessages(messages)
	messages = lingmawire.NormalizeAssistantToolCallContent(messages)

	var sessionID string
	if options.SessionID != "" {
		sessionID = options.SessionID
	} else if len(rawRequestJSON) > 0 {
		sessionID = generateSessionID(rawRequestJSON)
	} else {
		sessionID = newUUID()
	}

	var agentID, modelConfigSource, taskID string
	var source int
	requestSetID := ""

	if options.IsQoder {
		// QoderCN protocol:
		// Official qoderclicn and community gateways use agent_common for all models.
		agentID = "agent_common"
		taskID = "common"
		source = 1
		requestSetID = requestID
		if options.IsReasoning {
			modelConfigSource = "system"
		} else {
			modelConfigSource = ""
		}
	} else {
		// Determine agent_id and source based on model and reasoning status.
		// kmodel and mmodel always use agent_common with empty source.
		// All other models default to agent_chat when reasoning, agent_common otherwise.
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

		taskID = "question_refine"
		source = 0
		if options.IsVL {
			// Native VL requests use the common task route and a fully populated
			// model configuration. The upstream silently treats the request as
			// text-only when only is_vl/image_urls are present.
			requestSetID = requestID
			taskID = "common"
			source = 1
		}
	}

	var imageURLs any
	if len(options.ImageURLs) > 0 {
		imageURLs = append([]string(nil), options.ImageURLs...)
	}
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
	if options.IsVL && options.ModelInfo != nil {
		modelConfig["display_name"] = options.ModelInfo.DisplayName
		modelConfig["format"] = options.ModelInfo.Format
		modelConfig["source"] = options.ModelInfo.Source
		modelConfig["max_input_tokens"] = options.ModelInfo.MaxInputTokens
		modelConfig["enable"] = true
	}

	businessProduct := "ide"
	if options.IsQoder {
		businessProduct = "qoderclicn"
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
			"product":  businessProduct,
			"version":  "0.11.0",
			"type":     "chat",
			"id":       newUUID(),
			"begin_at": 0,
			"stage":    "start",
			"name":     "api-bridge",
			"relation": map[string]any{},
		},
	}
	if options.IsQoder {
		body["session_type"] = "qoderclicn"
	} else if options.IsVL {
		body["chat_task"] = "common"
		body["session_type"] = "assistant"
	}

	mergedParams := make(map[string]any)
	if len(params) > 0 {
		for k, v := range params {
			mergedParams[k] = v
		}
	} else {
		mergedParams["temperature"] = 0.1
	}

	if options.IsQoder {
		if options.IsReasoning {
			mergedParams["enable_thinking"] = true
			if _, ok := mergedParams["reasoning_effort"]; !ok {
				mergedParams["reasoning_effort"] = "high"
			}
		} else {
			mergedParams["enable_thinking"] = false
		}
	}
	body["parameters"] = mergedParams

	if len(tools) > 0 {
		body["tools"] = tools
	}

	if options.ToolChoice != nil {
		body["tool_choice"] = options.ToolChoice
	}

	return body
}

// normalizeLingmaToolCallContent adapts OpenAI-compatible assistant tool-call
// history to the stricter provider behind Lingma. OpenAI permits content=null
// when tool_calls is present, but that provider fails to recognize the
// tool_calls and then rejects the following tool result as orphaned. The empty
// string preserves the message semantics while satisfying both schemas.
//
// BuildLingmaBody calls this after mergeReasoningContentIntoMessages, which
// already clones every message, so callers' request maps are not mutated.
func normalizeLingmaToolCallContent(messages []map[string]any) []map[string]any {
	return lingmawire.NormalizeAssistantToolCallContent(messages)
}

func stringValue(v any) string {
	value, _ := v.(string)
	return value
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
	Key                 string   `json:"key"`
	DisplayName         string   `json:"display_name"`
	Format              string   `json:"format"`
	Source              string   `json:"source"`
	Order               int      `json:"order"`
	IsVL                bool     `json:"is_vl"`
	IsReasoning         bool     `json:"is_reasoning"`
	MaxInputTokens      int      `json:"max_input_tokens"`
	PriceFactor         *float64 `json:"price_factor,omitempty"`
	OriginalPriceFactor *float64 `json:"original_price_factor,omitempty"`
}

// FetchModels queries the Lingma model list API and returns models for the "chat" category.
func (c *LingmaClient) FetchModels(ctx context.Context) ([]ModelInfo, error) {
	encodedBody := ""
	endpointURL := c.BaseURL() + "/algo/api/v2/model/list"

	headers, err := c.session.BuildHeaders(encodedBody, endpointURL)
	if err != nil {
		return nil, fmt.Errorf("build headers: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", endpointURL, nil)
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
