package bridge

import (
	"net/http/httptest"
	"testing"

	"github.com/coolxll/lingma-tap/internal/auth"
)

func TestClientConversationSessionIDClaudeCode(t *testing.T) {
	h := NewBridgeHandler(&auth.Session{UID: "user-a"}, nil)
	conversationID := "22222222-2222-4222-8222-222222222222"

	first := httptest.NewRequest("POST", "/v1/messages", nil)
	first.Header.Set("X-Claude-Code-Session-Id", conversationID)
	second := httptest.NewRequest("POST", "/v1/messages", nil)
	second.Header.Set("X-Claude-Code-Session-Id", conversationID)

	firstID := h.clientConversationSessionID(first, nil)
	secondID := h.clientConversationSessionID(second, nil)
	if firstID == "" || firstID != secondID {
		t.Fatalf("same Claude Code conversation produced %q and %q", firstID, secondID)
	}
	if len(firstID) != 32 {
		t.Fatalf("session ID length = %d, want 32", len(firstID))
	}
}

func TestClientConversationSessionIDCodexUsesThreadNotTurn(t *testing.T) {
	h := NewBridgeHandler(&auth.Session{UID: "user-a"}, nil)
	threadID := "33333333-3333-4333-8333-333333333333"

	first := httptest.NewRequest("POST", "/v1/responses", nil)
	first.Header.Set("X-Codex-Turn-Metadata", `{"thread_id":"`+threadID+`","turn_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}`)
	second := httptest.NewRequest("POST", "/v1/responses", nil)
	second.Header.Set("X-Codex-Turn-Metadata", `{"thread_id":"`+threadID+`","turn_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}`)

	firstID := h.clientConversationSessionID(first, nil)
	secondID := h.clientConversationSessionID(second, nil)
	if firstID == "" || firstID != secondID {
		t.Fatalf("same Codex thread produced %q and %q", firstID, secondID)
	}
}

func TestClientConversationSessionIDNamespacesClientsAndUsers(t *testing.T) {
	conversationID := "44444444-4444-4444-8444-444444444444"
	claudeRequest := httptest.NewRequest("POST", "/v1/messages", nil)
	claudeRequest.Header.Set("X-Claude-Code-Session-Id", conversationID)
	codexRequest := httptest.NewRequest("POST", "/v1/responses", nil)
	codexRequest.Header.Set("X-Codex-Turn-Metadata", `{"thread_id":"`+conversationID+`"}`)

	userA := NewBridgeHandler(&auth.Session{UID: "user-a"}, nil)
	userB := NewBridgeHandler(&auth.Session{UID: "user-b"}, nil)
	claudeA := userA.clientConversationSessionID(claudeRequest, nil)
	codexA := userA.clientConversationSessionID(codexRequest, nil)
	claudeB := userB.clientConversationSessionID(claudeRequest, nil)
	if claudeA == codexA || claudeA == claudeB || codexA == claudeB {
		t.Fatalf("session namespaces collided: claudeA=%q codexA=%q claudeB=%q", claudeA, codexA, claudeB)
	}
}

func TestClientConversationSessionIDRejectsInvalidMetadata(t *testing.T) {
	tests := []struct {
		name   string
		uid    string
		header string
		value  string
	}{
		{name: "missing UID", header: "X-Claude-Code-Session-Id", value: "55555555-5555-4555-8555-555555555555"},
		{name: "invalid Claude identity", uid: "user-a", header: "X-Claude-Code-Session-Id", value: "not a valid identity"},
		{name: "invalid Codex JSON", uid: "user-a", header: "X-Codex-Turn-Metadata", value: "{"},
		{name: "Codex turn only", uid: "user-a", header: "X-Codex-Turn-Metadata", value: `{"turn_id":"55555555-5555-4555-8555-555555555555"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewBridgeHandler(&auth.Session{UID: tt.uid}, nil)
			r := httptest.NewRequest("POST", "/v1/messages", nil)
			r.Header.Set(tt.header, tt.value)
			if got := h.clientConversationSessionID(r, nil); got != "" {
				t.Fatalf("got %q, want fallback", got)
			}
		})
	}
}

func TestClientConversationSessionIDClaudeSubagentsAreIsolated(t *testing.T) {
	h := NewBridgeHandler(&auth.Session{UID: "user-a"}, nil)
	mainRequest := httptest.NewRequest("POST", "/v1/messages", nil)
	mainRequest.Header.Set("X-Claude-Code-Session-Id", "session-a")
	agentOne := httptest.NewRequest("POST", "/v1/messages", nil)
	agentOne.Header.Set("X-Claude-Code-Session-Id", "session-a")
	agentOne.Header.Set("X-Claude-Code-Agent-Id", "agent-one")
	agentTwo := httptest.NewRequest("POST", "/v1/messages", nil)
	agentTwo.Header.Set("X-Claude-Code-Session-Id", "session-a")
	agentTwo.Header.Set("X-Claude-Code-Agent-Id", "agent-two")

	mainID := h.clientConversationSessionID(mainRequest, nil)
	agentOneID := h.clientConversationSessionID(agentOne, nil)
	agentTwoID := h.clientConversationSessionID(agentTwo, nil)
	if mainID == agentOneID || mainID == agentTwoID || agentOneID == agentTwoID {
		t.Fatalf("Claude identities collided: main=%q agentOne=%q agentTwo=%q", mainID, agentOneID, agentTwoID)
	}
}

func TestClientConversationSessionIDRejectsAmbiguousClaudeHeaders(t *testing.T) {
	h := NewBridgeHandler(&auth.Session{UID: "user-a"}, nil)

	duplicateSession := httptest.NewRequest("POST", "/v1/messages", nil)
	duplicateSession.Header.Add("X-Claude-Code-Session-Id", "session-a")
	duplicateSession.Header.Add("X-Claude-Code-Session-Id", "session-b")
	if got := h.clientConversationSessionID(duplicateSession, nil); got != "" {
		t.Fatalf("duplicate session headers produced %q", got)
	}

	malformedAgent := httptest.NewRequest("POST", "/v1/messages", nil)
	malformedAgent.Header.Set("X-Claude-Code-Session-Id", "session-a")
	malformedAgent.Header.Set("X-Claude-Code-Agent-Id", "agent one")
	if got := h.clientConversationSessionID(malformedAgent, nil); got != "" {
		t.Fatalf("malformed agent header produced %q", got)
	}

	parentWithoutAgent := httptest.NewRequest("POST", "/v1/messages", nil)
	parentWithoutAgent.Header.Set("X-Claude-Code-Session-Id", "session-a")
	parentWithoutAgent.Header.Set("X-Claude-Code-Parent-Agent-Id", "agent-parent")
	if got := h.clientConversationSessionID(parentWithoutAgent, nil); got != "" {
		t.Fatalf("ambiguous parent header produced %q", got)
	}
}

func TestClientConversationSessionIDCodexBodyMetadata(t *testing.T) {
	h := NewBridgeHandler(&auth.Session{UID: "user-a"}, nil)
	r := httptest.NewRequest("POST", "/v1/responses", nil)
	body := []byte(`{"client_metadata":{"x-codex-turn-metadata":"{\"session_id\":\"root-session\",\"thread_id\":\"thread-a\",\"turn_id\":\"turn-a\"}"}}`)

	got := h.clientConversationSessionID(r, body)
	want := namespacedConversationSessionID("codex", "user-a", "thread-a")
	if got != want {
		t.Fatalf("session ID = %q, want %q", got, want)
	}
}

func TestClientConversationSessionIDCodexFallsBackToMetadataSession(t *testing.T) {
	h := NewBridgeHandler(&auth.Session{UID: "user-a"}, nil)
	r := httptest.NewRequest("POST", "/v1/responses", nil)
	r.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"root-session","turn_id":"turn-a"}`)

	got := h.clientConversationSessionID(r, nil)
	want := namespacedConversationSessionID("codex", "user-a", "root-session")
	if got != want {
		t.Fatalf("session ID = %q, want %q", got, want)
	}
}

func TestBuildLingmaBodyExplicitSessionIDOverridesRequestFingerprint(t *testing.T) {
	const sessionID = "0123456789abcdef0123456789abcdef"
	body := BuildLingmaBodyWithOptions(
		[]map[string]any{{"role": "user", "content": "hello"}},
		nil,
		"qwen3.5-plus",
		nil,
		[]byte(`{"messages":[{"role":"user","content":"changes"}]}`),
		LingmaBodyOptions{SessionID: sessionID},
	)
	if got := body["session_id"]; got != sessionID {
		t.Fatalf("session_id = %#v, want %q", got, sessionID)
	}
}
