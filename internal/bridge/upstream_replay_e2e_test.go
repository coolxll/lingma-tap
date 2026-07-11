//go:build integration

package bridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type replayE2ECase struct {
	name      string
	http2     bool
	transform func(map[string]any) string
}

type replayE2EOutcome struct {
	Success           bool   `json:"success"`
	ErrorClass        string `json:"error_class,omitempty"`
	Error             string `json:"error,omitempty"`
	DurationMS        int64  `json:"duration_ms"`
	FirstPayloadMS    int64  `json:"first_payload_ms"`
	ContentEvents     int    `json:"content_events"`
	ReasoningEvents   int    `json:"reasoning_events"`
	ToolCallEvents    int    `json:"tool_call_events"`
	DoneReceived      bool   `json:"done_received"`
	UpstreamErrorCode string `json:"upstream_error_code,omitempty"`
	UpstreamError     string `json:"upstream_error,omitempty"`
}

// TestIntegration_ReplayCapturedRequestMatrix replays a captured gateway body
// against the real Lingma upstream. It is opt-in and never logs auth or payloads.
func TestIntegration_ReplayCapturedRequestMatrix(t *testing.T) {
	if strings.TrimSpace(os.Getenv("LINGMA_E2E_CONFIRM")) != "1" {
		t.Skip("set LINGMA_E2E_CONFIRM=1 to allow real upstream requests")
	}
	logID, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("LINGMA_REPLAY_ID")), 10, 64)
	if err != nil || logID <= 0 {
		t.Skip("set LINGMA_REPLAY_ID to a gateway_logs id")
	}

	body, sourceStatus, sourceError := loadReplayE2EBody(t, replayE2EDBPath(t), logID)
	session := auth.NewSession(loadReplayE2ECredentials(t))
	selected := replayE2ESelectedVariants()
	timeout := replayE2ETimeout(t)

	cases := []replayE2ECase{
		{name: "original_http1"},
		{name: "original_http2", http2: true},
		{
			name: "reasoning_off_keep_agent",
			transform: func(body map[string]any) string {
				setReplayE2EReasoning(body, false)
				return "reasoning=false; agent preserved"
			},
		},
		{
			name: "agent_common_non_reasoning",
			transform: func(body map[string]any) string {
				disableLingmaThinking(body)
				return "reasoning=false; agent_id=agent_common"
			},
		},
		{
			name: "latest_tool_turn_redacted",
			transform: func(body map[string]any) string {
				return fmt.Sprintf("changed=%v", redactLatestToolTurn(body))
			},
		},
		{
			name: "latest_2_tool_turns_redacted",
			transform: func(body map[string]any) string {
				return fmt.Sprintf("changed=%d", redactLatestReplayE2EToolTurns(body, 2))
			},
		},
		{
			name: "latest_3_tool_turns_redacted",
			transform: func(body map[string]any) string {
				return fmt.Sprintf("changed=%d", redactLatestReplayE2EToolTurns(body, 3))
			},
		},
		{
			name: "latest_2_tool_call_arguments_redacted",
			transform: func(body map[string]any) string {
				return fmt.Sprintf("changed=%d", redactLatestReplayE2EToolCallArguments(body, 2))
			},
		},
		{
			name: "latest_2_tool_results_redacted",
			transform: func(body map[string]any) string {
				return fmt.Sprintf("changed=%d", redactLatestReplayE2EToolResults(body, 2))
			},
		},
		{
			name: "all_tool_call_arguments_redacted",
			transform: func(body map[string]any) string {
				return fmt.Sprintf("changed=%d", redactAllReplayE2EToolCallArguments(body))
			},
		},
		{
			name: "all_tool_results_redacted",
			transform: func(body map[string]any) string {
				return fmt.Sprintf("changed=%d", redactAllReplayE2EToolResults(body))
			},
		},
		{
			name: "all_tool_results_truncated_4096",
			transform: func(body map[string]any) string {
				return fmt.Sprintf("changed=%d", truncateReplayE2EToolResults(body, 4096))
			},
		},
		{
			name: "all_tool_turns_redacted",
			transform: func(body map[string]any) string {
				return fmt.Sprintf("changed=%d", redactAllReplayE2EToolTurns(body))
			},
		},
		{
			name: "tool_descriptions_removed",
			transform: func(body map[string]any) string {
				return fmt.Sprintf("changed=%d", removeReplayE2EToolDescriptions(body))
			},
		},
		{
			name: "tool_definitions_removed",
			transform: func(body map[string]any) string {
				delete(body, "tools")
				delete(body, "tool_choice")
				return "tools removed"
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		if selected != nil && !selected[tc.name] {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			replayBody, ok := cloneLingmaBody(body)
			if !ok {
				t.Fatal("clone captured request")
			}
			note := "unchanged"
			if tc.transform != nil {
				note = tc.transform(replayBody)
			}
			refreshReplayE2EIDs(replayBody)

			client := NewLingmaClient(session)
			client.SetHTTP2Enabled(tc.http2)
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			outcome := runReplayE2E(ctx, client, replayBody)

			raw, _ := json.Marshal(map[string]any{
				"variant":        tc.name,
				"source_log_id":  logID,
				"source_status":  sourceStatus,
				"source_error":   sourceError,
				"http2_enabled":  tc.http2,
				"body_bytes":     replayE2EBodyBytes(replayBody),
				"messages":       len(lingmaMessages(replayBody)),
				"tools":          replayE2EToolCount(replayBody),
				"reasoning":      replayE2EReasoning(replayBody),
				"agent_id":       roleString(replayBody["agent_id"]),
				"transform_note": note,
				"outcome":        outcome,
			})
			t.Logf("LINGMA_E2E_RESULT %s", raw)
		})
	}
}

func runReplayE2E(ctx context.Context, client *LingmaClient, body map[string]any) replayE2EOutcome {
	started := time.Now()
	outcome := replayE2EOutcome{FirstPayloadMS: -1}
	err := client.ChatStream(ctx, body, func(event SSEEvent) error {
		if event.HasError {
			outcome.UpstreamErrorCode = event.ErrorCode
			outcome.UpstreamError = event.ErrorMsg
			return errorFromSSEEvent(event)
		}
		if outcome.FirstPayloadMS < 0 && (event.Content != "" || event.ReasoningContent != "" || len(event.ToolCalls) > 0) {
			outcome.FirstPayloadMS = time.Since(started).Milliseconds()
		}
		if event.Content != "" {
			outcome.ContentEvents++
		}
		if event.ReasoningContent != "" {
			outcome.ReasoningEvents++
		}
		if len(event.ToolCalls) > 0 {
			outcome.ToolCallEvents++
		}
		if event.Type == "done" {
			outcome.DoneReceived = true
		}
		return nil
	})
	outcome.DurationMS = time.Since(started).Milliseconds()
	outcome.Success = err == nil && outcome.DoneReceived
	if err != nil {
		outcome.Error = err.Error()
		outcome.ErrorClass = classifyReplayE2EError(err)
	} else if !outcome.DoneReceived {
		outcome.ErrorClass = "missing_done"
	}
	return outcome
}

func classifyReplayE2EError(err error) string {
	if err == nil {
		return ""
	}
	var upstreamErr lingmaUpstreamEventError
	if errors.As(err, &upstreamErr) {
		return "upstream_sse_error"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "context"
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "unexpected_eof"
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "internal_error") || strings.Contains(msg, "stream error") {
		return "stream_reset"
	}
	if strings.Contains(msg, "http request") {
		return "transport"
	}
	return "other"
}

func loadReplayE2EBody(t *testing.T, dbPath string, logID int64) (map[string]any, int, string) {
	t.Helper()
	dsn := "file:" + filepath.ToSlash(dbPath) + "?mode=ro&_pragma=busy_timeout(10000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open replay database: %v", err)
	}
	defer db.Close()

	var rawBody, sourceError string
	var sourceStatus int
	err = db.QueryRow(`select request_body, status, coalesce(error, '') from gateway_logs where id = ?`, logID).
		Scan(&rawBody, &sourceStatus, &sourceError)
	if err != nil {
		t.Fatalf("load gateway_logs id=%d: %v", logID, err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(rawBody), &body); err != nil {
		t.Fatalf("decode gateway_logs id=%d request: %v", logID, err)
	}
	return body, sourceStatus, sourceError
}

func replayE2EDBPath(t *testing.T) string {
	t.Helper()
	if path := strings.TrimSpace(os.Getenv("LINGMA_REPLAY_DB")); path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home directory: %v", err)
	}
	return filepath.Join(home, ".lingma-tap", "lingma-tap.db")
}

func loadReplayE2ECredentials(t *testing.T) *auth.Credentials {
	t.Helper()
	authDir := strings.TrimSpace(os.Getenv("LINGMA_REPLAY_AUTH_DIR"))
	if authDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			authDir = filepath.Join(home, ".lingma-tap", "auth")
		}
	}
	if authDir != "" {
		if creds, err := auth.LoadCredentialsFromDir(authDir); err == nil {
			return creds
		}
	}
	creds, err := auth.LoadCredentials()
	if err != nil {
		t.Fatalf("load Lingma credentials: %v", err)
	}
	return creds
}

func replayE2ETimeout(t *testing.T) time.Duration {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LINGMA_REPLAY_TIMEOUT"))
	if raw == "" {
		return 90 * time.Second
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		t.Fatalf("invalid LINGMA_REPLAY_TIMEOUT=%q", raw)
	}
	return timeout
}

func replayE2ESelectedVariants() map[string]bool {
	raw := strings.TrimSpace(os.Getenv("LINGMA_REPLAY_VARIANTS"))
	if raw == "" || raw == "all" {
		return nil
	}
	selected := make(map[string]bool)
	for _, item := range strings.Split(raw, ",") {
		if name := strings.TrimSpace(item); name != "" {
			selected[name] = true
		}
	}
	return selected
}

func refreshReplayE2EIDs(body map[string]any) {
	requestID := uuid.NewString()
	body["request_id"] = requestID
	body["chat_record_id"] = requestID
	body["session_id"] = uuid.NewString()
	if business, ok := body["business"].(map[string]any); ok {
		business["id"] = uuid.NewString()
	}
}

func setReplayE2EReasoning(body map[string]any, enabled bool) {
	if config, ok := body["model_config"].(map[string]any); ok {
		config["is_reasoning"] = enabled
		if !enabled {
			config["source"] = ""
		}
	}
}

func replayE2EReasoning(body map[string]any) bool {
	if config, ok := body["model_config"].(map[string]any); ok {
		enabled, _ := config["is_reasoning"].(bool)
		return enabled
	}
	return false
}

func replayE2EBodyBytes(body map[string]any) int {
	raw, _ := json.Marshal(body)
	return len(raw)
}

func replayE2EToolCount(body map[string]any) int {
	switch tools := body["tools"].(type) {
	case []any:
		return len(tools)
	case []map[string]any:
		return len(tools)
	default:
		return 0
	}
}

func truncateReplayE2EToolResults(body map[string]any, limit int) int {
	changed := 0
	for _, message := range lingmaMessages(body) {
		if !strings.EqualFold(roleString(message["role"]), "tool") {
			continue
		}
		content, ok := message["content"].(string)
		if !ok || len(content) <= limit {
			continue
		}
		message["content"] = content[:limit] + "\n[tool result truncated for upstream E2E]"
		changed++
	}
	return changed
}

func redactAllReplayE2EToolTurns(body map[string]any) int {
	return redactLatestReplayE2EToolTurns(body, int(^uint(0)>>1))
}

func redactLatestReplayE2EToolTurns(body map[string]any, limit int) int {
	messages := lingmaMessages(body)
	changed := 0
	for toolIdx := len(messages) - 1; toolIdx >= 0; toolIdx-- {
		if changed >= limit {
			break
		}
		toolMessage := messages[toolIdx]
		if !strings.EqualFold(roleString(toolMessage["role"]), "tool") {
			continue
		}
		toolCallID := roleString(toolMessage["tool_call_id"])
		for assistantIdx := toolIdx - 1; toolCallID != "" && assistantIdx >= 0; assistantIdx-- {
			assistantMessage := messages[assistantIdx]
			if !strings.EqualFold(roleString(assistantMessage["role"]), "assistant") {
				continue
			}
			if redactToolCallByID(assistantMessage, toolCallID) {
				toolMessage["content"] = "[tool result redacted for upstream E2E]"
				changed++
			}
			break
		}
	}
	return changed
}

func redactAllReplayE2EToolCallArguments(body map[string]any) int {
	return redactLatestReplayE2EToolCallArguments(body, int(^uint(0)>>1))
}

func redactLatestReplayE2EToolCallArguments(body map[string]any, limit int) int {
	changed := 0
	messages := lingmaMessages(body)
	for index := len(messages) - 1; index >= 0; index-- {
		if changed >= limit {
			break
		}
		message := messages[index]
		if !strings.EqualFold(roleString(message["role"]), "assistant") {
			continue
		}
		switch calls := message["tool_calls"].(type) {
		case []any:
			for _, item := range calls {
				call, ok := item.(map[string]any)
				if ok {
					redactToolCallArguments(call)
					changed++
				}
			}
		case []map[string]any:
			for _, call := range calls {
				redactToolCallArguments(call)
				changed++
			}
		}
	}
	return changed
}

func redactAllReplayE2EToolResults(body map[string]any) int {
	return redactLatestReplayE2EToolResults(body, int(^uint(0)>>1))
}

func redactLatestReplayE2EToolResults(body map[string]any, limit int) int {
	changed := 0
	messages := lingmaMessages(body)
	for index := len(messages) - 1; index >= 0; index-- {
		if changed >= limit {
			break
		}
		message := messages[index]
		if !strings.EqualFold(roleString(message["role"]), "tool") {
			continue
		}
		message["content"] = "[tool result redacted for upstream E2E]"
		changed++
	}
	return changed
}

func removeReplayE2EToolDescriptions(body map[string]any) int {
	changed := 0
	tools, _ := body["tools"].([]any)
	for _, item := range tools {
		tool, ok := item.(map[string]any)
		if !ok {
			continue
		}
		function, ok := tool["function"].(map[string]any)
		if !ok {
			continue
		}
		if _, exists := function["description"]; exists {
			delete(function, "description")
			changed++
		}
	}
	return changed
}
