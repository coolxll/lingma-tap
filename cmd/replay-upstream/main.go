package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/coolxll/lingma-tap/internal/encoding"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type gatewayRow struct {
	ID          int
	TS          string
	Model       string
	Status      int
	Latency     int
	Error       string
	RequestBody string
}

type requestProfile struct {
	BodyBytes   int
	Messages    int
	Tools       int
	ToolCalls   int
	ToolResults int
	Reasoning   bool
	AgentID     string
	ModelKey    string
}

func main() {
	var (
		dbPath     = flag.String("db", defaultDBPath(), "path to lingma-tap SQLite database")
		authDir    = flag.String("auth-dir", defaultAuthDir(), "path to lingma-tap auth directory")
		id         = flag.Int("id", 0, "gateway_logs id to replay; defaults to latest row with request_body")
		mode       = flag.String("mode", "original", "original, fallback, or both")
		agent      = flag.String("agent", "", "override agent_id before sending")
		reasoning  = flag.String("reasoning", "keep", "override model_config.is_reasoning: keep, true, or false")
		source     = flag.String("source", "__keep__", "override model_config.source; use empty string to clear")
		http2      = flag.Bool("http2", false, "allow HTTP/2 for the upstream request")
		freshIDs   = flag.Bool("fresh-ids", false, "replace request_id/chat_record_id/session_id/business.id before sending")
		repairTool = flag.Bool("repair-tool-order", false, "move matching tool messages directly after assistant tool_calls before sending")
		maxMsgs    = flag.Int("max-messages", 0, "keep only the first N messages before sending; 0 keeps all")
		redactTool = flag.Int("redact-tool-message", -1, "replace the content of the message at this index before sending")
		redactCall = flag.Int("redact-tool-call-message", -1, "replace tool_call arguments on the assistant message at this index before sending")
		timeout    = flag.Duration("timeout", 90*time.Second, "per-request timeout")
		maxEvents  = flag.Int("max-events", 12, "maximum SSE data events to print")
		maxPreview = flag.Int("max-preview", 800, "maximum bytes printed per SSE data event")
	)
	flag.Parse()

	row, err := loadGatewayRow(*dbPath, *id)
	if err != nil {
		log.Fatal(err)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(row.RequestBody), &body); err != nil {
		log.Fatalf("parse request_body from gateway_logs id=%d: %v", row.ID, err)
	}

	creds, err := loadCredentials(*authDir)
	if err != nil {
		log.Fatal(err)
	}
	session := auth.NewSession(creds)

	fmt.Printf("gateway_logs id=%d ts=%s status=%d latency_ms=%d model=%s\n", row.ID, row.TS, row.Status, row.Latency, row.Model)
	if row.Error != "" {
		fmt.Printf("recorded_error=%s\n", row.Error)
	}
	printProfile("loaded", profileRequest(body))

	switch strings.ToLower(strings.TrimSpace(*mode)) {
	case "original":
		prepareBody(body, *freshIDs, *repairTool, *maxMsgs, *redactTool, *redactCall, *agent, *reasoning, *source)
		runReplay(session, body, "original", *http2, *timeout, *maxEvents, *maxPreview)
	case "fallback":
		fallbackBody := cloneBody(body)
		disableThinking(fallbackBody)
		prepareBody(fallbackBody, *freshIDs, *repairTool, *maxMsgs, *redactTool, *redactCall, *agent, *reasoning, *source)
		runReplay(session, fallbackBody, "fallback", *http2, *timeout, *maxEvents, *maxPreview)
	case "both":
		runReplay(session, body, "original", *http2, *timeout, *maxEvents, *maxPreview)
		fallbackBody := cloneBody(body)
		disableThinking(fallbackBody)
		prepareBody(fallbackBody, *freshIDs, *repairTool, *maxMsgs, *redactTool, *redactCall, *agent, *reasoning, *source)
		runReplay(session, fallbackBody, "fallback", *http2, *timeout, *maxEvents, *maxPreview)
	default:
		log.Fatalf("invalid -mode %q; use original, fallback, or both", *mode)
	}
}

func prepareBody(body map[string]any, freshIDs, repairTool bool, maxMessages, redactToolMessageIndex, redactToolCallMessageIndex int, agentID, reasoning, source string) {
	if repairTool {
		repairToolOrder(body)
	}
	limitMessages(body, maxMessages)
	redactToolMessage(body, redactToolMessageIndex)
	redactToolCallMessage(body, redactToolCallMessageIndex)
	if freshIDs {
		refreshIDs(body)
	}
	applyOverrides(body, agentID, reasoning, source)
}

func redactToolCallMessage(body map[string]any, index int) {
	if index < 0 {
		return
	}
	messages, ok := body["messages"].([]any)
	if !ok || index >= len(messages) {
		return
	}
	message, ok := messages[index].(map[string]any)
	if !ok {
		return
	}
	calls, ok := message["tool_calls"].([]any)
	if !ok {
		return
	}
	for _, item := range calls {
		call, ok := item.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := call["function"].(map[string]any)
		if !ok {
			continue
		}
		fn["arguments"] = `{"redacted":true}`
	}
}

func redactToolMessage(body map[string]any, index int) {
	if index < 0 {
		return
	}
	messages, ok := body["messages"].([]any)
	if !ok || index >= len(messages) {
		return
	}
	message, ok := messages[index].(map[string]any)
	if !ok {
		return
	}
	message["content"] = "[tool result redacted for upstream replay]"
}

func limitMessages(body map[string]any, maxMessages int) {
	if maxMessages <= 0 {
		return
	}
	messages, ok := body["messages"].([]any)
	if !ok || len(messages) <= maxMessages {
		return
	}
	body["messages"] = messages[:maxMessages]
}

func repairToolOrder(body map[string]any) {
	messages, ok := body["messages"].([]any)
	if !ok || len(messages) == 0 {
		return
	}

	used := make([]bool, len(messages))
	repaired := make([]any, 0, len(messages))
	for i, item := range messages {
		if used[i] {
			continue
		}
		used[i] = true
		repaired = append(repaired, item)

		message, ok := item.(map[string]any)
		if !ok || stringValue(message["role"]) != "assistant" {
			continue
		}
		callIDs := toolCallIDs(message)
		if len(callIDs) == 0 {
			continue
		}
		for _, callID := range callIDs {
			for j := i + 1; j < len(messages); j++ {
				if used[j] {
					continue
				}
				candidate, ok := messages[j].(map[string]any)
				if !ok || stringValue(candidate["role"]) != "tool" || stringValue(candidate["tool_call_id"]) != callID {
					continue
				}
				used[j] = true
				repaired = append(repaired, candidate)
				break
			}
		}
	}

	body["messages"] = repaired
}

func toolCallIDs(message map[string]any) []string {
	calls, ok := message["tool_calls"].([]any)
	if !ok {
		return nil
	}
	var ids []string
	for _, item := range calls {
		call, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if id := stringValue(call["id"]); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "lingma-tap.db"
	}
	return filepath.Join(home, ".lingma-tap", "lingma-tap.db")
}

func defaultAuthDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "auth"
	}
	return filepath.Join(home, ".lingma-tap", "auth")
}

func loadGatewayRow(dbPath string, id int) (gatewayRow, error) {
	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		return gatewayRow{}, err
	}
	defer db.Close()

	query := `
select id, ts, model, status, latency, coalesce(error, ''), request_body
from gateway_logs
where request_body is not null and request_body != ''
order by id desc
limit 1`
	args := []any{}
	if id > 0 {
		query = `
select id, ts, model, status, latency, coalesce(error, ''), request_body
from gateway_logs
where id = ?`
		args = append(args, id)
	}

	var row gatewayRow
	if err := db.QueryRow(query, args...).Scan(&row.ID, &row.TS, &row.Model, &row.Status, &row.Latency, &row.Error, &row.RequestBody); err != nil {
		return gatewayRow{}, err
	}
	return row, nil
}

func loadCredentials(authDir string) (*auth.Credentials, error) {
	if strings.TrimSpace(authDir) != "" {
		if creds, err := auth.LoadCredentialsFromDir(authDir); err == nil {
			return creds, nil
		}
	}
	return auth.LoadCredentials()
}

func runReplay(session *auth.Session, body map[string]any, label string, http2Enabled bool, timeout time.Duration, maxEvents, maxPreview int) {
	fmt.Printf("\n== replay %s ==\n", label)
	printProfile(label, profileRequest(body))

	rawBody, err := json.Marshal(body)
	if err != nil {
		fmt.Printf("marshal_error=%v\n", err)
		return
	}
	encodedBody := encoding.Encode(rawBody)
	chatURL := buildChatURL(stringValue(body["agent_id"]))
	headers, err := session.BuildHeaders(encodedBody, chatURL)
	if err != nil {
		fmt.Printf("headers_error=%v\n", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatURL, strings.NewReader(encodedBody))
	if err != nil {
		fmt.Printf("request_error=%v\n", err)
		return
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{
		Timeout:   timeout + 5*time.Second,
		Transport: newTransport(http2Enabled),
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("http_error after_ms=%d err=%v\n", time.Since(start).Milliseconds(), err)
		return
	}
	defer resp.Body.Close()

	fmt.Printf("http_status=%d proto=%s header_ms=%d content_type=%q\n",
		resp.StatusCode,
		resp.Proto,
		time.Since(start).Milliseconds(),
		resp.Header.Get("Content-Type"),
	)

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, int64(maxPreview)))
		fmt.Printf("non_200_body=%s\n", sanitizePreview(data, maxPreview))
		return
	}

	readSSE(resp.Body, start, maxEvents, maxPreview)
}

func newTransport(http2Enabled bool) http.RoundTripper {
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

func buildChatURL(agentID string) string {
	if strings.TrimSpace(agentID) == "" {
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

func readSSE(r io.Reader, start time.Time, maxEvents, maxPreview int) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	events := 0
	var dataLines []string
	flush := func() bool {
		if len(dataLines) == 0 {
			return false
		}
		events++
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		fmt.Printf("event=%d elapsed_ms=%d data=%s\n", events, time.Since(start).Milliseconds(), sanitizePreview([]byte(data), maxPreview))
		return data == "[DONE]" || events >= maxEvents
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if flush() {
				return
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if flush() {
		return
	}
	if err := scanner.Err(); err != nil {
		fmt.Printf("stream_error elapsed_ms=%d err=%v\n", time.Since(start).Milliseconds(), err)
		return
	}
	fmt.Printf("stream_closed_without_done elapsed_ms=%d\n", time.Since(start).Milliseconds())
}

func profileRequest(body map[string]any) requestProfile {
	raw, _ := json.Marshal(body)
	profile := requestProfile{
		BodyBytes: len(raw),
		AgentID:   stringValue(body["agent_id"]),
	}
	if modelConfig, ok := body["model_config"].(map[string]any); ok {
		profile.Reasoning, _ = modelConfig["is_reasoning"].(bool)
		profile.ModelKey = stringValue(modelConfig["key"])
	}
	if messages, ok := body["messages"].([]any); ok {
		profile.Messages = len(messages)
		for _, item := range messages {
			message, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if calls, ok := message["tool_calls"].([]any); ok {
				profile.ToolCalls += len(calls)
			}
			if strings.EqualFold(stringValue(message["role"]), "tool") {
				profile.ToolResults++
			}
		}
	}
	if tools, ok := body["tools"].([]any); ok {
		profile.Tools = len(tools)
	}
	return profile
}

func printProfile(label string, profile requestProfile) {
	fmt.Printf(
		"profile=%s model=%s agent=%s reasoning=%v body_bytes=%d messages=%d tools=%d tool_history=%d\n",
		label,
		profile.ModelKey,
		profile.AgentID,
		profile.Reasoning,
		profile.BodyBytes,
		profile.Messages,
		profile.Tools,
		profile.ToolCalls+profile.ToolResults,
	)
}

func cloneBody(body map[string]any) map[string]any {
	raw, _ := json.Marshal(body)
	var cloned map[string]any
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func disableThinking(body map[string]any) {
	if modelConfig, ok := body["model_config"].(map[string]any); ok {
		modelConfig["is_reasoning"] = false
		modelConfig["source"] = ""
	}
	body["agent_id"] = "agent_common"
}

func applyOverrides(body map[string]any, agentID, reasoning, source string) {
	if strings.TrimSpace(agentID) != "" {
		body["agent_id"] = strings.TrimSpace(agentID)
	}
	modelConfig, _ := body["model_config"].(map[string]any)
	if modelConfig == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(reasoning)) {
	case "true", "1", "yes", "on":
		modelConfig["is_reasoning"] = true
	case "false", "0", "no", "off":
		modelConfig["is_reasoning"] = false
	}
	if source != "__keep__" {
		modelConfig["source"] = source
	}
}

func refreshIDs(body map[string]any) {
	requestID := uuid.NewString()
	body["request_id"] = requestID
	body["chat_record_id"] = requestID
	body["session_id"] = uuid.NewString()
	if business, ok := body["business"].(map[string]any); ok {
		business["id"] = uuid.NewString()
	}
}

func sanitizePreview(data []byte, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = 800
	}
	truncated := false
	if len(data) > maxBytes {
		data = data[:maxBytes]
		truncated = true
	}
	data = bytes.ReplaceAll(data, []byte("\r"), []byte("\\r"))
	data = bytes.ReplaceAll(data, []byte("\n"), []byte("\\n"))
	out := string(data)
	if truncated {
		out += "...<truncated>"
	}
	return out
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}
