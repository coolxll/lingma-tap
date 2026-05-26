package auth

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func fixtureSession() *Session {
	return &Session{
		CosyKey:  "AAAAAAAAAAAAAAAAAAAAAA==", // 16 zero bytes, base64
		Info:     "encrypted-user-info-payload",
		UID:      "uid-123",
		OrgID:    "org-456",
		Mid:      "machine-789",
		UserType: "internal",
	}
}

func TestExtractPathWithoutAlgo(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://x.y/algo/v1/chat", "/v1/chat"},
		{"https://x.y/algo", ""},
		{"https://x.y/v1/chat", "/v1/chat"},
		{"https://x.y/algo/", "/"},
		{"https://x.y/algoritmic", "ritmic"}, // confirms the strip is a literal "/algo" prefix
		{"https://x.y/algo/v1?Encode=1", "/v1"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := extractPathWithoutAlgo(tc.in); got != tc.want {
				t.Fatalf("extractPathWithoutAlgo(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMD5HexKnownVectors(t *testing.T) {
	cases := map[string]string{
		"":      "d41d8cd98f00b204e9800998ecf8427e",
		"hello": "5d41402abc4b2a76b9719d911017c592",
	}
	for in, want := range cases {
		if got := md5Hex(in); got != want {
			t.Fatalf("md5Hex(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestNewUUIDIsV4(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := make(map[string]bool)
	for i := 0; i < 16; i++ {
		u := newUUID()
		if !re.MatchString(u) {
			t.Fatalf("newUUID() = %q, not a valid v4 UUID", u)
		}
		if seen[u] {
			t.Fatalf("newUUID returned duplicate %q", u)
		}
		seen[u] = true
	}
}

func TestSessionBuildPayloadB64(t *testing.T) {
	s := fixtureSession()
	b64, err := s.buildPayloadB64()
	if err != nil {
		t.Fatalf("buildPayloadB64 error: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("payload not valid base64: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v (raw=%s)", err, raw)
	}
	if payload["cosyVersion"] != "0.11.0" {
		t.Errorf("cosyVersion = %q, want 0.11.0", payload["cosyVersion"])
	}
	if payload["version"] != "v1" {
		t.Errorf("version = %q, want v1", payload["version"])
	}
	if payload["ideVersion"] != "" {
		t.Errorf("ideVersion = %q, want empty", payload["ideVersion"])
	}
	if payload["info"] != s.Info {
		t.Errorf("info = %q, want %q", payload["info"], s.Info)
	}
	if payload["requestId"] == "" {
		t.Error("requestId is empty")
	}

	// Two calls must produce different payloads (different requestId).
	b64b, err := s.buildPayloadB64()
	if err != nil {
		t.Fatalf("buildPayloadB64 second error: %v", err)
	}
	if b64 == b64b {
		t.Error("two buildPayloadB64 calls returned identical payload")
	}
}

func TestSessionSignRequestRecomputable(t *testing.T) {
	s := fixtureSession()
	encodedBody := "encoded-body-token"
	fullURL := "https://lingma-api.tongyi.aliyun.com/algo/chat?Encode=1"
	cosyDate := "1700000000"

	payloadB64, sig, err := s.SignRequest(encodedBody, fullURL, cosyDate)
	if err != nil {
		t.Fatalf("SignRequest error: %v", err)
	}
	if payloadB64 == "" {
		t.Fatal("payloadB64 is empty")
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(sig) {
		t.Fatalf("sig %q is not 32 hex chars", sig)
	}

	pathWithoutAlgo := extractPathWithoutAlgo(fullURL)
	expected := md5.Sum([]byte(payloadB64 + "\n" + s.CosyKey + "\n" + cosyDate + "\n" + encodedBody + "\n" + pathWithoutAlgo))
	want := fmt.Sprintf("%x", expected)
	if sig != want {
		t.Fatalf("sig = %s, want %s (recomputed from documented inputs)", sig, want)
	}
}

func TestSessionBuildBearerFormat(t *testing.T) {
	s := fixtureSession()
	bearer, err := s.BuildBearer("body", "https://lingma-api.tongyi.aliyun.com/algo/chat", "1700000000")
	if err != nil {
		t.Fatalf("BuildBearer error: %v", err)
	}
	const prefix = "Bearer COSY."
	if !strings.HasPrefix(bearer, prefix) {
		t.Fatalf("bearer %q missing prefix %q", bearer, prefix)
	}
	rest := strings.TrimPrefix(bearer, prefix)
	parts := strings.Split(rest, ".")
	if len(parts) != 2 {
		t.Fatalf("bearer body %q does not have exactly two dot-separated parts: %v", rest, parts)
	}
	if parts[0] == "" {
		t.Error("bearer payloadB64 segment is empty")
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(parts[1]) {
		t.Errorf("bearer signature segment %q is not 32 hex chars", parts[1])
	}
}

func TestSessionBuildHeaders(t *testing.T) {
	s := fixtureSession()
	headers, err := s.BuildHeaders("body", "https://lingma-api.tongyi.aliyun.com/algo/chat")
	if err != nil {
		t.Fatalf("BuildHeaders error: %v", err)
	}
	required := []string{
		"Content-Type", "Accept", "Accept-Encoding", "Cache-Control",
		"Login-Version", "Authorization", "Cosy-Date", "Cosy-Key",
		"Cosy-Version", "Cosy-Clienttype", "Cosy-Machineid", "Cosy-User",
		"Cosy-Organization-Id", "User-Agent",
	}
	for _, k := range required {
		if _, ok := headers[k]; !ok {
			t.Errorf("missing header %q", k)
		}
	}
	if headers["Cosy-Key"] != s.CosyKey {
		t.Errorf("Cosy-Key = %q, want %q", headers["Cosy-Key"], s.CosyKey)
	}
	if headers["Cosy-User"] != s.UID {
		t.Errorf("Cosy-User = %q, want %q", headers["Cosy-User"], s.UID)
	}
	if headers["Cosy-Organization-Id"] != s.OrgID {
		t.Errorf("Cosy-Organization-Id = %q, want %q", headers["Cosy-Organization-Id"], s.OrgID)
	}
	if headers["Cosy-Machineid"] != s.Mid {
		t.Errorf("Cosy-Machineid = %q, want %q", headers["Cosy-Machineid"], s.Mid)
	}
	if !strings.HasPrefix(headers["Authorization"], "Bearer COSY.") {
		t.Errorf("Authorization = %q, missing Bearer COSY. prefix", headers["Authorization"])
	}
	if headers["Cosy-Version"] != "0.11.0" {
		t.Errorf("Cosy-Version = %q, want 0.11.0", headers["Cosy-Version"])
	}
}

func TestNewSessionCopiesCredsFields(t *testing.T) {
	creds := &Credentials{
		CosyKey:         "ck",
		EncryptUserInfo: "info",
		UID:             "u",
		OrganizationID:  "o",
		MachineID:       "m",
		UserType:        "t",
	}
	s := NewSession(creds)
	if s.CosyKey != "ck" || s.Info != "info" || s.UID != "u" || s.OrgID != "o" || s.Mid != "m" || s.UserType != "t" {
		t.Fatalf("NewSession did not copy fields: %+v", s)
	}
}

func TestNewSessionWithFreshKeyShape(t *testing.T) {
	creds := &Credentials{
		CosyKey:         "should-be-replaced",
		EncryptUserInfo: "info",
		UID:             "u",
		OrganizationID:  "o",
		MachineID:       "m",
		UserType:        "t",
	}
	s, encryptedKey, err := NewSessionWithFreshKey(creds)
	if err != nil {
		t.Fatalf("NewSessionWithFreshKey error: %v", err)
	}
	rawKey, err := base64.StdEncoding.DecodeString(s.CosyKey)
	if err != nil {
		t.Fatalf("session CosyKey not valid base64: %v", err)
	}
	if len(rawKey) != 16 {
		t.Fatalf("session CosyKey decoded length = %d, want 16", len(rawKey))
	}
	if s.CosyKey == creds.CosyKey {
		t.Error("session.CosyKey unexpectedly equals creds.CosyKey (should be freshly generated)")
	}
	if s.Info != creds.EncryptUserInfo || s.UID != creds.UID || s.OrgID != creds.OrganizationID || s.Mid != creds.MachineID || s.UserType != creds.UserType {
		t.Errorf("session metadata not copied from creds: %+v", s)
	}

	encBytes, err := base64.StdEncoding.DecodeString(encryptedKey)
	if err != nil {
		t.Fatalf("encryptedKey not valid base64: %v", err)
	}
	expectedLen := (serverPubKey.N.BitLen() + 7) / 8
	if len(encBytes) != expectedLen {
		t.Errorf("encryptedKey byte length = %d, want %d (RSA modulus size)", len(encBytes), expectedLen)
	}

	// Two calls must produce different keys (random).
	s2, enc2, err := NewSessionWithFreshKey(creds)
	if err != nil {
		t.Fatalf("NewSessionWithFreshKey second call error: %v", err)
	}
	if s.CosyKey == s2.CosyKey {
		t.Error("two NewSessionWithFreshKey calls produced identical CosyKey")
	}
	if encryptedKey == enc2 {
		t.Error("two NewSessionWithFreshKey calls produced identical encryptedKey")
	}

	if got := serverPubKey.Size(); got != expectedLen {
		t.Fatalf("test precondition: serverPubKey.Size() = %d, want %d", got, expectedLen)
	}
}
