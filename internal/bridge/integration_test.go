//go:build integration

package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coolxll/lingma-tap/internal/auth"
	"github.com/coolxll/lingma-tap/internal/encoding"
	"github.com/joho/godotenv"
)

func init() {
	// Try to load .env for OAuth tests if present
	_ = godotenv.Load("../../.env")
}

func TestIntegration_RealChatStream(t *testing.T) {
	creds, err := auth.LoadCredentials()
	if err != nil {
		t.Skip("Skipping: no local credentials found")
	}

	session := auth.NewSession(creds)
	client := NewLingmaClient(session)
	// client.Debug = true

	messages := []map[string]any{
		{"role": "user", "content": "Ping! Reply with 'Pong' only."},
	}
	modelKey := "dashscope_qmodel"
	body := BuildLingmaBody(messages, nil, modelKey, nil, nil, false, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	receivedContent := false
	err = client.ChatStream(ctx, body, func(ev SSEEvent) error {
		if ev.Type == "data" && ev.Content != "" {
			receivedContent = true
		}
		return nil
	})

	if err != nil {
		t.Errorf("ChatStream failed: %v", err)
	} else if !receivedContent {
		t.Errorf("Failed to receive any content")
	}
}

func TestIntegration_CodingAgentLongToolHistory(t *testing.T) {
	if os.Getenv("LINGMA_LONG_HISTORY_INTEGRATION") != "1" {
		t.Skip("set LINGMA_LONG_HISTORY_INTEGRATION=1 to run the long-history recovery test")
	}
	creds, err := auth.LoadCredentials()
	if err != nil {
		t.Skip("Skipping: no local credentials found")
	}

	client := NewLingmaClient(auth.NewSession(creds))
	transport := &profileObservingTransport{base: http.DefaultTransport}
	client.client.Transport = transport

	messages := syntheticCodingAgentHistory()
	tools := []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name":        "read_file",
			"description": "Read a repository file",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
				"required": []string{"path"},
			},
		},
	}}
	body := BuildLingmaBody(messages, tools, "gm51model", map[string]any{"max_tokens": 1024}, []byte(`{"agent":"synthetic-long-history"}`), true, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var stats LingmaUpstreamStats
	actionable := false
	done := false
	err = client.ChatStreamObserved(ctx, body, func(event SSEEvent) error {
		if event.Content != "" || len(event.ToolCalls) > 0 || event.FinishReason != "" {
			actionable = true
		}
		if event.Type == "done" {
			done = true
		}
		return nil
	}, func(current LingmaUpstreamStats) {
		stats = current
	})
	if err != nil {
		t.Fatalf("long-history ChatStream failed: %v (stats=%+v profiles=%v)", err, stats, transport.Profiles())
	}
	if !actionable || !done {
		t.Fatalf("long-history stream incomplete: actionable=%v done=%v stats=%+v profiles=%v", actionable, done, stats, transport.Profiles())
	}
	if stats.Attempts < 1 || stats.FirstActionableMS <= 0 {
		t.Fatalf("long-history stats incomplete: %+v", stats)
	}
	t.Logf("long-history completed attempts=%d recovery=%v first_actionable_ms=%d reasoning_only_bytes=%d error_class=%s profiles=%v",
		stats.Attempts, stats.RecoveryApplied, stats.FirstActionableMS, stats.ReasoningOnlyBytes, stats.ErrorClass, transport.Profiles())
}

func syntheticCodingAgentHistory() []map[string]any {
	messages := []map[string]any{
		{"role": "system", "content": "You are Claude Code, a coding agent. Use repository tools when needed."},
		{"role": "user", "content": strings.Repeat("synthetic repository context line\n", 5000)},
	}
	for i := 0; i < 10; i++ {
		callID := fmt.Sprintf("call_%d", i)
		messages = append(messages,
			map[string]any{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []map[string]any{{
					"id":   callID,
					"type": "function",
					"function": map[string]any{
						"name":      "read_file",
						"arguments": fmt.Sprintf(`{"path":"synthetic/file_%d.go"}`, i),
					},
				}},
			},
			map[string]any{
				"role":         "tool",
				"tool_call_id": callID,
				"content":      fmt.Sprintf("synthetic file %d inspected successfully", i),
			},
		)
	}
	return append(messages, map[string]any{"role": "user", "content": "The inspection is complete. Reply READY and do not call another tool."})
}

type profileObservingTransport struct {
	base     http.RoundTripper
	mu       sync.Mutex
	profiles []string
}

func (t *profileObservingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	encodedBody, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(encodedBody))
	if decodedBody, decodeErr := encoding.Decode(string(encodedBody)); decodeErr == nil {
		var body map[string]any
		if json.Unmarshal(decodedBody, &body) == nil {
			t.mu.Lock()
			t.profiles = append(t.profiles, lingmaProfileString(body))
			t.mu.Unlock()
		}
	}
	return t.base.RoundTrip(req)
}

func (t *profileObservingTransport) Profiles() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.profiles...)
}

func TestIntegration_ModelDifferentiation(t *testing.T) {
	creds, err := auth.LoadCredentials()
	if err != nil {
		t.Skip("No credentials")
	}

	session := auth.NewSession(creds)
	client := NewLingmaClient(session)

	// Test 1: Identify self
	question := "你是谁？"
	models := []string{"kmodel", "mmodel", "dashscope_qmodel"}

	for _, m := range models {
		t.Run(m, func(t *testing.T) {
			body := BuildLingmaBody([]map[string]any{{"role": "user", "content": question}}, nil, m, nil, nil, false, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			answer := ""
			err := client.ChatStream(ctx, body, func(ev SSEEvent) error {
				answer += ev.Content
				return nil
			})
			if err != nil {
				t.Logf("Model %s failed: %v (expected if backend rejects it)", m, err)
			} else {
				t.Logf("Model %s Answer: %s", m, strings.TrimSpace(answer))
			}
		})
	}
}

func TestIntegration_ThinkingBehavior(t *testing.T) {
	creds, err := auth.LoadCredentials()
	if err != nil {
		t.Skip("No credentials")
	}

	session := auth.NewSession(creds)
	client := NewLingmaClient(session)

	thinkingModel := "dashscope_qwen_plus_20250428_thinking"
	messages := []map[string]any{{"role": "user", "content": "1.3 和 1.11 哪个大？请详细分析。"}}

	t.Run("ThinkingEnabled", func(t *testing.T) {
		body := BuildLingmaBody(messages, nil, thinkingModel, nil, nil, true, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		hasReasoning := false
		err := client.ChatStream(ctx, body, func(ev SSEEvent) error {
			raw := string(ev.Raw)
			if strings.Contains(raw, "reasoning_content") || strings.Contains(raw, "thinking") {
				hasReasoning = true
			}
			return nil
		})
		if err != nil {
			t.Skipf("Thinking model request failed: %v", err)
		}
		t.Logf("Found reasoning metadata: %v", hasReasoning)
	})

	t.Run("FakeModelNoThinking", func(t *testing.T) {
		fakeModel := "this-is-a-fake-model-abc-123"
		body := BuildLingmaBody(messages, nil, fakeModel, nil, nil, false, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		hasReasoning := false
		err := client.ChatStream(ctx, body, func(ev SSEEvent) error {
			raw := string(ev.Raw)
			if strings.Contains(raw, "reasoning_content") || strings.Contains(raw, "thinking") {
				hasReasoning = true
			}
			return nil
		})
		if err != nil {
			t.Logf("Fake model failed as expected: %v", err)
		}
		if hasReasoning {
			t.Errorf("Fake model should not have reasoning metadata")
		}
	})
}

func TestIntegration_UsageStats(t *testing.T) {
	creds, err := auth.LoadCredentials()
	if err != nil {
		t.Skip("No credentials")
	}

	session := auth.NewSession(creds)
	client := NewLingmaClient(session)

	body := BuildLingmaBody([]map[string]any{{"role": "user", "content": "Ping"}}, nil, "dashscope_qmodel", nil, nil, false, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var lastUsage *Usage
	err = client.ChatStream(ctx, body, func(ev SSEEvent) error {
		if ev.Usage != nil {
			lastUsage = ev.Usage
		}
		return nil
	})

	if err != nil {
		t.Fatalf("ChatStream failed: %v", err)
	}
	if lastUsage == nil {
		t.Error("No usage stats received")
	} else {
		t.Logf("Usage: Prompt=%d, Completion=%d, Total=%d", lastUsage.PromptTokens, lastUsage.CompletionTokens, lastUsage.TotalTokens)
	}
}

func TestIntegration_OAuthExchange(t *testing.T) {
	userId := os.Getenv("LINGMA_UID")
	securityToken := os.Getenv("LINGMA_TOKEN")
	machineID := os.Getenv("LINGMA_MID")

	if userId == "" || securityToken == "" || machineID == "" {
		t.Skip("Skipping OAuth test: env vars not set")
	}

	creds, err := auth.ExchangeCallback(userId, securityToken, machineID)
	if err != nil {
		t.Fatalf("ExchangeCallback failed: %v", err)
	}

	if creds.UID == "" || creds.OrganizationID == "" {
		t.Errorf("Exchanged credentials missing basic info: %+v", creds)
	}

	t.Logf("OAuth Exchange Success: UID=%s", creds.UID)
}

func TestIntegration_ListModels(t *testing.T) {
	creds, err := auth.LoadCredentials()
	if err != nil {
		t.Skip("No credentials")
	}

	client := NewLingmaClient(auth.NewSession(creds))
	models, err := client.FetchModels(context.Background())
	if err != nil {
		t.Fatalf("Failed to fetch models: %v", err)
	}

	t.Log(">>> Available Models:")
	for _, m := range models {
		t.Logf("Key: %-30s | Name: %s", m.Key, m.DisplayName)
	}
}

func TestIntegration_ReasoningMathProblem(t *testing.T) {
	creds, err := auth.LoadCredentials()
	if err != nil {
		t.Skip("No credentials")
	}

	session := auth.NewSession(creds)
	client := NewLingmaClient(session)

	question := "某公司在10个省有123家连锁店，每个省的连锁店数量不等，数量由多到少 排名第5的省有12家连锁店，那么连锁店数量最多的省至少有几家连锁店？请直接给出最终答案的数字。"

	models := []string{
		"dashscope_qmodel",
		"gm51model",
		"dashscope_qwen_plus_20250428_thinking",
		"kmodel",
		"mmodel",
		"org_auto",
	}

	type modelResult struct {
		Model           string
		HasReasoning    bool
		ReasoningLen    int
		Answer          string
		ContainsCorrect bool
		Err             error
	}

	var results []modelResult

	chatURL := "https://lingma-api.tongyi.aliyun.com/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_chat&Encode=1"

	for _, modelKey := range models {
		t.Run(modelKey, func(t *testing.T) {
			messages := []map[string]any{
				{"role": "user", "content": question},
			}
			body := map[string]any{
				"request_id":     newUUID(),
				"chat_record_id": newUUID(),
				"stream":         true,
				"is_reply":       false,
				"is_retry":       false,
				"session_id":     newUUID(),
				"version":        "3",
				"agent_id":       "agent_chat",
				"task_id":        "common_chat",
				"messages":       messages,
				"model_config": map[string]any{
					"key":          modelKey,
					"is_reasoning": true,
					"source":       "system",
				},
				"parameters": map[string]any{"max_new_tokens": 16384, "max_tokens": 16384},
			}

			bodyJSON, _ := json.Marshal(body)
			encodedBody := encoding.Encode(bodyJSON)
			headers, _ := session.BuildHeaders(encodedBody, chatURL)

			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "POST", chatURL, strings.NewReader(encodedBody))
			for k, v := range headers {
				req.Header.Set(k, v)
			}

			resp, err := client.client.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			state := &streamState{}
			var reasoningBuilder strings.Builder
			var contentBuilder strings.Builder

			err = client.readSSE(resp.Body, func(ev SSEEvent) error {
				if ev.ReasoningContent != "" {
					reasoningBuilder.WriteString(ev.ReasoningContent)
				}
				if ev.Content != "" {
					contentBuilder.WriteString(ev.Content)
				}
				return nil
			}, state)

			r := modelResult{
				Model:        modelKey,
				HasReasoning: reasoningBuilder.Len() > 0,
				ReasoningLen: reasoningBuilder.Len(),
				Answer:       strings.TrimSpace(contentBuilder.String()),
				Err:          err,
			}
			r.ContainsCorrect = strings.Contains(r.Answer, "18")

			if err != nil {
				t.Logf("[%s] Error: %v", modelKey, err)
			}
			t.Logf("[%s] HasReasoning=%v ReasoningLen=%d Contains18=%v", modelKey, r.HasReasoning, r.ReasoningLen, r.ContainsCorrect)
			t.Logf("[%s] Answer: %s", modelKey, truncate(r.Answer, 1000))
			if r.HasReasoning {
				t.Logf("[%s] Reasoning (first 300 chars): %s", modelKey, truncate(reasoningBuilder.String(), 300))
			}

			results = append(results, r)
		})
	}

	// Summary
	t.Log("\n=== Reasoning Test Summary ===")
	t.Logf("%-45s %-10s %-12s %-10s %-8s", "Model", "Reasoning", "ReasonLen", "Answer18", "Error")
	t.Logf("%-45s %-10s %-12s %-10s %-8s", "-----", "---------", "---------", "--------", "-----")
	for _, r := range results {
		errStr := "-"
		if r.Err != nil {
			errStr = "yes"
		}
		t.Logf("%-45s %-10v %-12d %-10v %-8s", r.Model, r.HasReasoning, r.ReasoningLen, r.ContainsCorrect, errStr)
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// TestIntegration_ReasoningParameterIsolation tests each parameter individually
// to find which one triggers reasoning_content in the response.
func TestIntegration_ReasoningParameterIsolation(t *testing.T) {
	creds, err := auth.LoadCredentials()
	if err != nil {
		t.Skip("No credentials")
	}

	session := auth.NewSession(creds)
	client := NewLingmaClient(session)

	question := "某公司在10个省有123家连锁店，每个省的连锁店数量不等，数量由多到少 排名第5的省有12家连锁店，那么连锁店数量最多的省至少有几家连锁店？请直接给出最终答案的数字。"
	modelKey := "dashscope_qwen_plus_20250428_thinking"

	// baseline body — our current BuildLingmaBody output
	makeBaseline := func() map[string]any {
		return BuildLingmaBody(
			[]map[string]any{{"role": "user", "content": question}},
			nil, modelKey, nil, nil, true, nil,
		)
	}

	type testCase struct {
		name   string
		mutate func(body map[string]any)
	}

	tests := []testCase{
		{
			name:   "0_baseline_ours",
			mutate: func(body map[string]any) {}, // no change
		},
		{
			name: "1_agent_id_agent_chat",
			mutate: func(body map[string]any) {
				body["agent_id"] = "agent_chat"
			},
		},
		{
			name: "2_task_id_common_chat",
			mutate: func(body map[string]any) {
				body["task_id"] = "common_chat"
			},
		},
		{
			name: "3_chat_task_FREE_INPUT",
			mutate: func(body map[string]any) {
				body["chat_task"] = "FREE_INPUT"
			},
		},
		{
			name: "4_source_1",
			mutate: func(body map[string]any) {
				body["source"] = 1
			},
		},
		{
			name: "5_task_definition_type_system",
			mutate: func(body map[string]any) {
				body["task_definition_type"] = "system"
			},
		},
		{
			name: "6_session_type_assistant",
			mutate: func(body map[string]any) {
				body["session_type"] = "assistant"
			},
		},
		{
			name: "7_model_config_full",
			mutate: func(body map[string]any) {
				body["model_config"] = map[string]any{
					"key":                   modelKey,
					"display_name":          "Qwen3-Thinking",
					"model":                 "",
					"format":                "dashscope",
					"is_vl":                 false,
					"is_reasoning":          true,
					"api_key":               "",
					"url":                   "",
					"source":                "system",
					"max_input_tokens":      96000,
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
			},
		},
		{
			name: "8_params_max_tokens",
			mutate: func(body map[string]any) {
				body["parameters"] = map[string]any{
					"max_new_tokens": 16384,
					"max_tokens":     16384,
				}
			},
		},
		{
			name: "9_chat_context_text",
			mutate: func(body map[string]any) {
				body["chat_context"] = map[string]any{
					"text": question,
				}
			},
		},
		{
			name: "10_agent_task_combo",
			mutate: func(body map[string]any) {
				body["agent_id"] = "agent_chat"
				body["task_id"] = "common_chat"
				body["chat_task"] = "FREE_INPUT"
				body["source"] = 1
				body["task_definition_type"] = "system"
				body["session_type"] = "assistant"
			},
		},
	}

	type result struct {
		Name         string
		HasReasoning bool
		ReasoningLen int
		Answer       string
		Err          error
	}
	var results []result

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := makeBaseline()
			tc.mutate(body)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			var reasoningBuilder, contentBuilder strings.Builder
			var rawReasoningHits int
			var firstRawEvents []string

			err := client.ChatStream(ctx, body, func(ev SSEEvent) error {
				if ev.ReasoningContent != "" {
					reasoningBuilder.WriteString(ev.ReasoningContent)
				}
				if ev.Content != "" {
					contentBuilder.WriteString(ev.Content)
				}
				// Check raw bytes for reasoning_content
				raw := string(ev.Raw)
				if strings.Contains(raw, "reasoning_content") {
					rawReasoningHits++
				}
				// Capture first few raw events for debugging
				if len(firstRawEvents) < 5 && len(raw) > 10 {
					firstRawEvents = append(firstRawEvents, truncate(raw, 300))
				}
				return nil
			})

			r := result{
				Name:         tc.name,
				HasReasoning: reasoningBuilder.Len() > 0,
				ReasoningLen: reasoningBuilder.Len(),
				Answer:       strings.TrimSpace(contentBuilder.String()),
				Err:          err,
			}

			if err != nil {
				t.Logf("[%s] Error: %v", tc.name, err)
			}
			t.Logf("[%s] HasReasoning=%v ReasoningLen=%d RawReasoningHits=%d", tc.name, r.HasReasoning, r.ReasoningLen, rawReasoningHits)
			t.Logf("[%s] Answer: %s", tc.name, truncate(r.Answer, 500))
			if r.HasReasoning {
				t.Logf("[%s] Reasoning: %s", tc.name, truncate(reasoningBuilder.String(), 300))
			}
			t.Logf("[%s] First raw events:", tc.name)
			for i, raw := range firstRawEvents {
				t.Logf("[%s]   [%d] %s", tc.name, i, raw)
			}

			results = append(results, r)
		})
	}

	// Also test with AgentId=agent_chat in the URL
	t.Run("11_url_agent_chat", func(t *testing.T) {
		body := makeBaseline()
		chatClient := &LingmaClient{
			session: session,
			client:  client.client,
		}

		// Build request with agent_chat URL
		bodyJSON, _ := json.Marshal(body)
		encodedBody := encoding.Encode(bodyJSON)
		chatURL := "https://lingma-api.tongyi.aliyun.com/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_chat&Encode=1"
		headers, _ := session.BuildHeaders(encodedBody, chatURL)

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		req, _ := http.NewRequestWithContext(ctx, "POST", chatURL, strings.NewReader(encodedBody))
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := chatClient.client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		state := &streamState{}
		var reasoningBuilder, contentBuilder strings.Builder
		var rawReasoningHits int

		err = chatClient.readSSE(resp.Body, func(ev SSEEvent) error {
			if ev.ReasoningContent != "" {
				reasoningBuilder.WriteString(ev.ReasoningContent)
			}
			if ev.Content != "" {
				contentBuilder.WriteString(ev.Content)
			}
			raw := string(ev.Raw)
			if strings.Contains(raw, "reasoning_content") {
				rawReasoningHits++
			}
			return nil
		}, state)

		if err != nil {
			t.Logf("[11_url_agent_chat] Error: %v", err)
		}
		t.Logf("[11_url_agent_chat] HasReasoning=%v ReasoningLen=%d RawReasoningHits=%d",
			reasoningBuilder.Len() > 0, reasoningBuilder.Len(), rawReasoningHits)
		t.Logf("[11_url_agent_chat] Answer: %s", truncate(strings.TrimSpace(contentBuilder.String()), 500))
		if reasoningBuilder.Len() > 0 {
			t.Logf("[11_url_agent_chat] Reasoning: %s", truncate(reasoningBuilder.String(), 300))
		}

		results = append(results, result{
			Name:         "11_url_agent_chat",
			HasReasoning: reasoningBuilder.Len() > 0,
			ReasoningLen: reasoningBuilder.Len(),
		})
	})

	// Test 12: Replay exact HAR body with our auth
	t.Run("12_replay_har_exact", func(t *testing.T) {
		// Read and decode HAR body
		harData, err := os.ReadFile("../../assets/Untitled.har")
		if err != nil {
			t.Fatalf("read HAR: %v", err)
		}
		var har struct {
			Log struct {
				Entries []struct {
					Request struct {
						PostData struct {
							Text string `json:"text"`
						} `json:"postData"`
					} `json:"request"`
				} `json:"entries"`
			} `json:"log"`
		}
		json.Unmarshal(harData, &har)

		// Decode Qoder-encoded body
		decoded, err := encoding.Decode(har.Log.Entries[0].Request.PostData.Text)
		if err != nil {
			t.Fatalf("decode qoder: %v", err)
		}

		// Use the decoded body directly as the request body
		var harBody map[string]any
		json.Unmarshal(decoded, &harBody)

		// Send with our auth
		bodyJSON, _ := json.Marshal(harBody)
		encodedBody := encoding.Encode(bodyJSON)
		chatURL := "https://lingma-api.tongyi.aliyun.com/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_chat&Encode=1"
		headers, _ := session.BuildHeaders(encodedBody, chatURL)

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		req, _ := http.NewRequestWithContext(ctx, "POST", chatURL, strings.NewReader(encodedBody))
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Fatalf("HTTP %d", resp.StatusCode)
		}

		state := &streamState{}
		var reasoningBuilder, contentBuilder strings.Builder
		var rawReasoningHits int
		var firstRawWithReasoning []string

		err = client.readSSE(resp.Body, func(ev SSEEvent) error {
			if ev.ReasoningContent != "" {
				reasoningBuilder.WriteString(ev.ReasoningContent)
			}
			if ev.Content != "" {
				contentBuilder.WriteString(ev.Content)
			}
			raw := string(ev.Raw)
			if strings.Contains(raw, "reasoning_content") {
				rawReasoningHits++
				if len(firstRawWithReasoning) < 3 {
					firstRawWithReasoning = append(firstRawWithReasoning, truncate(raw, 400))
				}
			}
			return nil
		}, state)

		if err != nil {
			t.Logf("[12_replay_har] Error: %v", err)
		}
		t.Logf("[12_replay_har] HasReasoning=%v ReasoningLen=%d RawReasoningHits=%d",
			reasoningBuilder.Len() > 0, reasoningBuilder.Len(), rawReasoningHits)
		t.Logf("[12_replay_har] Answer: %s", truncate(strings.TrimSpace(contentBuilder.String()), 500))
		if reasoningBuilder.Len() > 0 {
			t.Logf("[12_replay_har] Reasoning: %s", truncate(reasoningBuilder.String(), 300))
		}
		for i, raw := range firstRawWithReasoning {
			t.Logf("[12_replay_har] RawWithReasoning[%d]: %s", i, raw)
		}

		results = append(results, result{
			Name:         "12_replay_har_exact",
			HasReasoning: reasoningBuilder.Len() > 0,
			ReasoningLen: reasoningBuilder.Len(),
		})
	})

	// Test 13-17: Binary search - strip fields from HAR body to find the trigger
	loadHARBody := func() map[string]any {
		harData, _ := os.ReadFile("../../assets/Untitled.har")
		var har struct {
			Log struct {
				Entries []struct {
					Request struct {
						PostData struct {
							Text string `json:"text"`
						} `json:"postData"`
					} `json:"request"`
				} `json:"entries"`
			} `json:"log"`
		}
		json.Unmarshal(harData, &har)
		decoded, _ := encoding.Decode(har.Log.Entries[0].Request.PostData.Text)
		var body map[string]any
		json.Unmarshal(decoded, &body)
		return body
	}

	type stripCase struct {
		name   string
		mutate func(body map[string]any)
	}

	stripTests := []stripCase{
		{
			name: "13_strip_tools",
			mutate: func(body map[string]any) {
				delete(body, "tools")
			},
		},
		{
			name: "14_strip_system_msg",
			mutate: func(body map[string]any) {
				msgs := body["messages"].([]any)
				body["messages"] = msgs[1:] // remove system message
			},
		},
		{
			name: "15_simplify_user_msg",
			mutate: func(body map[string]any) {
				// Replace contents array with simple content string
				msgs := body["messages"].([]any)
				userMsg := msgs[len(msgs)-1].(map[string]any)
				// Extract the actual question from contents
				contents := userMsg["contents"].([]any)
				var question string
				for _, c := range contents {
					text := c.(map[string]any)["text"].(string)
					if strings.Contains(text, "<user_query>") {
						start := strings.Index(text, "<user_query>") + len("<user_query>")
						end := strings.Index(text, "</user_query>")
						question = text[start:end]
					}
				}
				delete(userMsg, "contents")
				userMsg["content"] = question
				userMsg["extra"] = nil
				userMsg["response_meta"] = nil
				userMsg["reasoning_content_signature"] = ""
			},
		},
		{
			name: "16_strip_system_and_simplify_user",
			mutate: func(body map[string]any) {
				msgs := body["messages"].([]any)
				// Remove system, simplify user
				userMsg := msgs[1].(map[string]any)
				contents := userMsg["contents"].([]any)
				var question string
				for _, c := range contents {
					text := c.(map[string]any)["text"].(string)
					if strings.Contains(text, "<user_query>") {
						start := strings.Index(text, "<user_query>") + len("<user_query>")
						end := strings.Index(text, "</user_query>")
						question = text[start:end]
					}
				}
				body["messages"] = []any{
					map[string]any{"role": "user", "content": question},
				}
			},
		},
		{
			name: "17_har_body_our_messages",
			mutate: func(body map[string]any) {
				// Keep all HAR fields but replace messages with our simple format
				body["messages"] = []any{
					map[string]any{"role": "user", "content": question},
				}
			},
		},
	}

	for _, tc := range stripTests {
		t.Run(tc.name, func(t *testing.T) {
			body := loadHARBody()
			tc.mutate(body)

			bodyJSON, _ := json.Marshal(body)
			encodedBody := encoding.Encode(bodyJSON)
			chatURL := "https://lingma-api.tongyi.aliyun.com/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_chat&Encode=1"
			headers, _ := session.BuildHeaders(encodedBody, chatURL)

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "POST", chatURL, strings.NewReader(encodedBody))
			for k, v := range headers {
				req.Header.Set(k, v)
			}

			resp, err := client.client.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			state := &streamState{}
			var reasoningBuilder, contentBuilder strings.Builder
			var rawReasoningHits int

			err = client.readSSE(resp.Body, func(ev SSEEvent) error {
				if ev.ReasoningContent != "" {
					reasoningBuilder.WriteString(ev.ReasoningContent)
				}
				if ev.Content != "" {
					contentBuilder.WriteString(ev.Content)
				}
				if strings.Contains(string(ev.Raw), "reasoning_content") {
					rawReasoningHits++
				}
				return nil
			}, state)

			if err != nil {
				t.Logf("[%s] Error: %v", tc.name, err)
			}
			t.Logf("[%s] HasReasoning=%v ReasoningLen=%d RawReasoningHits=%d",
				tc.name, reasoningBuilder.Len() > 0, reasoningBuilder.Len(), rawReasoningHits)

			results = append(results, result{
				Name:         tc.name,
				HasReasoning: reasoningBuilder.Len() > 0,
				ReasoningLen: reasoningBuilder.Len(),
			})
		})
	}

	// Test 18-21: Add HAR structural fields to our baseline to find the trigger combo
	addTests := []stripCase{
		{
			name: "18_add_all_struct_fields",
			mutate: func(body map[string]any) {
				// Add ALL non-message HAR structural fields to our baseline
				body["agent_id"] = "agent_chat"
				body["task_id"] = "common_chat"
				body["chat_task"] = "FREE_INPUT"
				body["source"] = 1
				body["task_definition_type"] = "system"
				body["session_type"] = "assistant"
				body["chat_context"] = map[string]any{"text": question}
				body["model_config"] = map[string]any{
					"key": modelKey, "display_name": "Qwen3-Thinking",
					"format": "dashscope", "is_reasoning": true,
					"source": "system", "max_input_tokens": 96000,
				}
				body["parameters"] = map[string]any{"max_new_tokens": 16384, "max_tokens": 16384}
				// Use agent_chat URL
			},
		},
		{
			name: "19_add_agent_task_chat_task_only",
			mutate: func(body map[string]any) {
				body["agent_id"] = "agent_chat"
				body["task_id"] = "common_chat"
				body["chat_task"] = "FREE_INPUT"
			},
		},
		{
			name: "20_add_model_config_and_params",
			mutate: func(body map[string]any) {
				body["model_config"] = map[string]any{
					"key": modelKey, "display_name": "Qwen3-Thinking",
					"format": "dashscope", "is_reasoning": true,
					"source": "system", "max_input_tokens": 96000,
				}
				body["parameters"] = map[string]any{"max_new_tokens": 16384, "max_tokens": 16384}
			},
		},
		{
			name: "21_add_source_session_type_task_def",
			mutate: func(body map[string]any) {
				body["source"] = 1
				body["task_definition_type"] = "system"
				body["session_type"] = "assistant"
			},
		},
	}

	for _, tc := range addTests {
		t.Run(tc.name, func(t *testing.T) {
			body := makeBaseline()
			tc.mutate(body)

			bodyJSON, _ := json.Marshal(body)
			encodedBody := encoding.Encode(bodyJSON)
			// Use agent_chat URL for tests that set agent_id=agent_chat
			chatURL := lingmaChatURL
			if body["agent_id"] == "agent_chat" {
				chatURL = "https://lingma-api.tongyi.aliyun.com/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_chat&Encode=1"
			}
			headers, _ := session.BuildHeaders(encodedBody, chatURL)

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "POST", chatURL, strings.NewReader(encodedBody))
			for k, v := range headers {
				req.Header.Set(k, v)
			}

			resp, err := client.client.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			state := &streamState{}
			var reasoningBuilder strings.Builder
			var rawReasoningHits int

			err = client.readSSE(resp.Body, func(ev SSEEvent) error {
				if ev.ReasoningContent != "" {
					reasoningBuilder.WriteString(ev.ReasoningContent)
				}
				if strings.Contains(string(ev.Raw), "reasoning_content") {
					rawReasoningHits++
				}
				return nil
			}, state)

			if err != nil {
				t.Logf("[%s] Error: %v", tc.name, err)
			}
			t.Logf("[%s] HasReasoning=%v ReasoningLen=%d RawReasoningHits=%d",
				tc.name, reasoningBuilder.Len() > 0, reasoningBuilder.Len(), rawReasoningHits)

			results = append(results, result{
				Name:         tc.name,
				HasReasoning: reasoningBuilder.Len() > 0,
				ReasoningLen: reasoningBuilder.Len(),
			})
		})
	}

	// Test 22-24: Pairwise combinations to narrow down
	pairTests := []stripCase{
		{
			name: "22_agent+model",
			mutate: func(body map[string]any) {
				body["agent_id"] = "agent_chat"
				body["task_id"] = "common_chat"
				body["chat_task"] = "FREE_INPUT"
				body["model_config"] = map[string]any{
					"key": modelKey, "display_name": "Qwen3-Thinking",
					"format": "dashscope", "is_reasoning": true,
					"source": "system", "max_input_tokens": 96000,
				}
				body["parameters"] = map[string]any{"max_new_tokens": 16384, "max_tokens": 16384}
			},
		},
		{
			name: "23_agent+source",
			mutate: func(body map[string]any) {
				body["agent_id"] = "agent_chat"
				body["task_id"] = "common_chat"
				body["chat_task"] = "FREE_INPUT"
				body["source"] = 1
				body["task_definition_type"] = "system"
				body["session_type"] = "assistant"
			},
		},
		{
			name: "24_model+source",
			mutate: func(body map[string]any) {
				body["model_config"] = map[string]any{
					"key": modelKey, "display_name": "Qwen3-Thinking",
					"format": "dashscope", "is_reasoning": true,
					"source": "system", "max_input_tokens": 96000,
				}
				body["parameters"] = map[string]any{"max_new_tokens": 16384, "max_tokens": 16384}
				body["source"] = 1
				body["task_definition_type"] = "system"
				body["session_type"] = "assistant"
			},
		},
	}

	for _, tc := range pairTests {
		t.Run(tc.name, func(t *testing.T) {
			body := makeBaseline()
			tc.mutate(body)

			bodyJSON, _ := json.Marshal(body)
			encodedBody := encoding.Encode(bodyJSON)
			chatURL := lingmaChatURL
			if body["agent_id"] == "agent_chat" {
				chatURL = "https://lingma-api.tongyi.aliyun.com/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_chat&Encode=1"
			}
			headers, _ := session.BuildHeaders(encodedBody, chatURL)

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "POST", chatURL, strings.NewReader(encodedBody))
			for k, v := range headers {
				req.Header.Set(k, v)
			}

			resp, err := client.client.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			state := &streamState{}
			var reasoningBuilder strings.Builder
			var rawReasoningHits int

			err = client.readSSE(resp.Body, func(ev SSEEvent) error {
				if ev.ReasoningContent != "" {
					reasoningBuilder.WriteString(ev.ReasoningContent)
				}
				if strings.Contains(string(ev.Raw), "reasoning_content") {
					rawReasoningHits++
				}
				return nil
			}, state)

			if err != nil {
				t.Logf("[%s] Error: %v", tc.name, err)
			}
			t.Logf("[%s] HasReasoning=%v ReasoningLen=%d RawReasoningHits=%d",
				tc.name, reasoningBuilder.Len() > 0, reasoningBuilder.Len(), rawReasoningHits)

			results = append(results, result{
				Name:         tc.name,
				HasReasoning: reasoningBuilder.Len() > 0,
				ReasoningLen: reasoningBuilder.Len(),
			})
		})
	}

	// Test 25-27: Narrow down model_config fields
	minimalTests := []stripCase{
		{
			name: "25_agent+model_minimal",
			mutate: func(body map[string]any) {
				body["agent_id"] = "agent_chat"
				body["task_id"] = "common_chat"
				body["chat_task"] = "FREE_INPUT"
				// Only set is_reasoning in model_config
				body["model_config"] = map[string]any{
					"key": modelKey, "is_reasoning": true,
				}
			},
		},
		{
			name: "26_agent+model_format_only",
			mutate: func(body map[string]any) {
				body["agent_id"] = "agent_chat"
				body["task_id"] = "common_chat"
				body["chat_task"] = "FREE_INPUT"
				body["model_config"] = map[string]any{
					"key": modelKey, "is_reasoning": true,
					"format": "dashscope",
				}
			},
		},
		{
			name: "27_agent+model_source_only",
			mutate: func(body map[string]any) {
				body["agent_id"] = "agent_chat"
				body["task_id"] = "common_chat"
				body["chat_task"] = "FREE_INPUT"
				body["model_config"] = map[string]any{
					"key": modelKey, "is_reasoning": true,
					"source": "system",
				}
			},
		},
	}

	for _, tc := range minimalTests {
		t.Run(tc.name, func(t *testing.T) {
			body := makeBaseline()
			tc.mutate(body)

			bodyJSON, _ := json.Marshal(body)
			encodedBody := encoding.Encode(bodyJSON)
			chatURL := "https://lingma-api.tongyi.aliyun.com/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_chat&Encode=1"
			headers, _ := session.BuildHeaders(encodedBody, chatURL)

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "POST", chatURL, strings.NewReader(encodedBody))
			for k, v := range headers {
				req.Header.Set(k, v)
			}

			resp, err := client.client.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			state := &streamState{}
			var reasoningBuilder strings.Builder
			var rawReasoningHits int

			err = client.readSSE(resp.Body, func(ev SSEEvent) error {
				if ev.ReasoningContent != "" {
					reasoningBuilder.WriteString(ev.ReasoningContent)
				}
				if strings.Contains(string(ev.Raw), "reasoning_content") {
					rawReasoningHits++
				}
				return nil
			}, state)

			if err != nil {
				t.Logf("[%s] Error: %v", tc.name, err)
			}
			t.Logf("[%s] HasReasoning=%v ReasoningLen=%d RawReasoningHits=%d",
				tc.name, reasoningBuilder.Len() > 0, reasoningBuilder.Len(), rawReasoningHits)

			results = append(results, result{
				Name:         tc.name,
				HasReasoning: reasoningBuilder.Len() > 0,
				ReasoningLen: reasoningBuilder.Len(),
			})
		})
	}

	// Test 28: Minimal trigger - agent_chat + model_config.source=system only
	t.Run("28_minimal_trigger", func(t *testing.T) {
		body := makeBaseline()
		body["agent_id"] = "agent_chat"
		body["model_config"] = map[string]any{
			"key": modelKey, "is_reasoning": true, "source": "system",
		}

		bodyJSON, _ := json.Marshal(body)
		encodedBody := encoding.Encode(bodyJSON)
		chatURL := "https://lingma-api.tongyi.aliyun.com/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_chat&Encode=1"
		headers, _ := session.BuildHeaders(encodedBody, chatURL)

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		req, _ := http.NewRequestWithContext(ctx, "POST", chatURL, strings.NewReader(encodedBody))
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		state := &streamState{}
		var reasoningBuilder, contentBuilder strings.Builder
		var rawReasoningHits int

		err = client.readSSE(resp.Body, func(ev SSEEvent) error {
			if ev.ReasoningContent != "" {
				reasoningBuilder.WriteString(ev.ReasoningContent)
			}
			if ev.Content != "" {
				contentBuilder.WriteString(ev.Content)
			}
			if strings.Contains(string(ev.Raw), "reasoning_content") {
				rawReasoningHits++
			}
			return nil
		}, state)

		if err != nil {
			t.Logf("[28_minimal] Error: %v", err)
		}
		t.Logf("[28_minimal] HasReasoning=%v ReasoningLen=%d RawReasoningHits=%d",
			reasoningBuilder.Len() > 0, reasoningBuilder.Len(), rawReasoningHits)
		t.Logf("[28_minimal] Answer: %s", truncate(strings.TrimSpace(contentBuilder.String()), 500))

		results = append(results, result{
			Name:         "28_minimal_trigger",
			HasReasoning: reasoningBuilder.Len() > 0,
			ReasoningLen: reasoningBuilder.Len(),
		})
	})

	t.Log("\n=== Parameter Isolation Summary ===")
	t.Logf("%-35s %-10s %-12s %-8s", "TestCase", "Reasoning", "ReasonLen", "Error")
	t.Logf("%-35s %-10s %-12s %-8s", "--------", "---------", "---------", "-----")
	for _, r := range results {
		errStr := "-"
		if r.Err != nil {
			errStr = "yes"
		}
		t.Logf("%-35s %-10v %-12d %-8s", r.Name, r.HasReasoning, r.ReasoningLen, errStr)
	}
}
