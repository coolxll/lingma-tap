package bridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
)

const (
	maxExplicitIdentityBytes   = 512
	maxCodexTurnMetadataBytes  = 16 << 10
	codexTurnMetadataFieldName = "x-codex-turn-metadata"
)

type identityHeaderState uint8

const (
	identityHeaderMissing identityHeaderState = iota
	identityHeaderValid
	identityHeaderInvalid
)

// clientConversationSessionID returns a privacy-preserving Lingma session ID
// when the caller exposes an explicit, stable conversation identifier.
//
// The authenticated Lingma UID and client namespace are part of the digest so
// identical client-generated IDs cannot join conversations across users or
// client implementations. Invalid or absent metadata deliberately falls back
// to the existing request-fingerprint behavior.
func (h *BridgeHandler) clientConversationSessionID(r *http.Request, rawBody []byte) string {
	if h == nil || h.session == nil || strings.TrimSpace(h.session.UID) == "" || r == nil {
		return ""
	}

	if identity, present := claudeCodeConversationIdentity(r.Header); present {
		if identity == "" {
			return ""
		}
		return namespacedConversationSessionID("claude-code", h.session.UID, identity)
	}

	metadataJSON, present := codexTurnMetadataJSON(r.Header, rawBody)
	if present {
		if identity := codexConversationIdentity(metadataJSON); identity != "" {
			return namespacedConversationSessionID("codex", h.session.UID, identity)
		}
	}

	return ""
}

// claudeCodeConversationIdentity keeps concurrent subagents isolated from the
// main Claude Code session. A malformed agent/parent tuple invalidates the
// whole explicit identity instead of silently downgrading it to the main one.
func claudeCodeConversationIdentity(headers http.Header) (string, bool) {
	sessionID, sessionState := readIdentityHeader(headers, "X-Claude-Code-Session-Id")
	agentID, agentState := readIdentityHeader(headers, "X-Claude-Code-Agent-Id")
	_, parentState := readIdentityHeader(headers, "X-Claude-Code-Parent-Agent-Id")

	present := sessionState != identityHeaderMissing || agentState != identityHeaderMissing || parentState != identityHeaderMissing
	if !present {
		return "", false
	}
	if sessionState != identityHeaderValid || agentState == identityHeaderInvalid || parentState == identityHeaderInvalid {
		return "", true
	}
	if agentState == identityHeaderValid {
		return sessionID + "\x00agent\x00" + agentID, true
	}
	if parentState == identityHeaderValid {
		return "", true
	}
	return sessionID, true
}

func readIdentityHeader(headers http.Header, name string) (string, identityHeaderState) {
	values := headers.Values(name)
	if len(values) == 0 {
		return "", identityHeaderMissing
	}
	if len(values) != 1 {
		return "", identityHeaderInvalid
	}
	value := strings.Trim(values[0], " 	")
	if !validExplicitIdentity(value) {
		return "", identityHeaderInvalid
	}
	return value, identityHeaderValid
}

func validExplicitIdentity(value string) bool {
	if value == "" || len(value) > maxExplicitIdentityBytes || strings.Contains(value, ",") {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

// codexTurnMetadataJSON prefers the direct compatibility header. Newer Codex
// versions treat client_metadata["x-codex-turn-metadata"] as the canonical
// carrier, so accept that body representation when the header is absent.
func codexTurnMetadataJSON(headers http.Header, rawBody []byte) ([]byte, bool) {
	headerValues := headers.Values("X-Codex-Turn-Metadata")
	if len(headerValues) > 0 {
		if len(headerValues) != 1 {
			return nil, true
		}
		value := strings.TrimSpace(headerValues[0])
		if value == "" || len(value) > maxCodexTurnMetadataBytes {
			return nil, true
		}
		return []byte(value), true
	}
	if len(rawBody) == 0 {
		return nil, false
	}

	var envelope struct {
		ClientMetadata map[string]json.RawMessage `json:"client_metadata"`
	}
	if err := json.Unmarshal(rawBody, &envelope); err != nil || envelope.ClientMetadata == nil {
		return nil, false
	}
	raw, ok := envelope.ClientMetadata[codexTurnMetadataFieldName]
	if !ok {
		return nil, false
	}
	if len(raw) > maxCodexTurnMetadataBytes {
		return nil, true
	}

	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		encoded = strings.TrimSpace(encoded)
		if encoded == "" || len(encoded) > maxCodexTurnMetadataBytes {
			return nil, true
		}
		return []byte(encoded), true
	}
	return raw, true
}

func codexConversationIdentity(metadataJSON []byte) string {
	if len(metadataJSON) == 0 || len(metadataJSON) > maxCodexTurnMetadataBytes {
		return ""
	}
	var metadata struct {
		ThreadID       string `json:"thread_id"`
		ThreadIDCamel  string `json:"threadId"`
		SessionID      string `json:"session_id"`
		SessionIDCamel string `json:"sessionId"`
	}
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
		return ""
	}
	for _, candidate := range []string{metadata.ThreadID, metadata.ThreadIDCamel, metadata.SessionID, metadata.SessionIDCamel} {
		candidate = strings.TrimSpace(candidate)
		if validExplicitIdentity(candidate) {
			return candidate
		}
	}
	return ""
}

func namespacedConversationSessionID(client, uid, conversationID string) string {
	digest := sha256.Sum256([]byte(client + "\x00" + uid + "\x00" + conversationID))
	return hex.EncodeToString(digest[:16])
}
