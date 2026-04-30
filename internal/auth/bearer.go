package auth

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const serverPubKeyPEM = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDA8iMH5c02LilrsERw9t6Pv5Nc
4k6Pz1EaDicBMpdpxKduSZu5OANqUq8er4GM95omAGIOPOh+Nx0spthYA2BqGz+l
6HRkPJ7S236FZz73In/KVuLnwI8JJ2CbuJap8kvheCCZpmAWpb/cPx/3Vr/J6I17
XcW+ML9FoCI6AOvOzwIDAQAB
-----END PUBLIC KEY-----`

var serverPubKey *rsa.PublicKey

func init() {
	block, _ := pem.Decode([]byte(serverPubKeyPEM))
	if block == nil {
		panic("failed to parse server public key PEM")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		panic("failed to parse server public key: " + err.Error())
	}
	serverPubKey = pub.(*rsa.PublicKey)
}

type Session struct {
	CosyKey  string
	Info     string // encrypt_user_info from auth file
	UID      string
	OrgID    string
	Mid      string
	UserType string
}

func NewSession(creds *Credentials) *Session {
	return &Session{
		CosyKey:  creds.CosyKey,
		Info:     creds.EncryptUserInfo,
		UID:      creds.UID,
		OrgID:    creds.OrganizationID,
		Mid:      creds.MachineID,
		UserType: creds.UserType,
	}
}

// NewSessionWithFreshKey generates a new tempKey, RSA-encrypts it, and creates a session.
// Use this when the CosyKey from the auth file may be stale.
func NewSessionWithFreshKey(creds *Credentials) (*Session, error) {
	tempKey := make([]byte, 16)
	if _, err := rand.Read(tempKey); err != nil {
		return nil, fmt.Errorf("generate temp key: %w", err)
	}

	encrypted, err := rsa.EncryptPKCS1v15(rand.Reader, serverPubKey, tempKey)
	if err != nil {
		return nil, fmt.Errorf("rsa encrypt: %w", err)
	}
	cosyKey := base64.StdEncoding.EncodeToString(encrypted)

	return &Session{
		CosyKey:  cosyKey,
		Info:     creds.EncryptUserInfo,
		UID:      creds.UID,
		OrgID:    creds.OrganizationID,
		Mid:      creds.MachineID,
		UserType: creds.UserType,
	}, nil
}

// SignRequest computes the Bearer signature for a Lingma API request.
// encodedBody: QoderEncoding-encoded request body
// fullURL: the full request URL (used to extract pathWithoutAlgo)
func (s *Session) SignRequest(encodedBody, fullURL string) (payloadB64, sig string, err error) {
	payloadB64, err = s.buildPayloadB64()
	if err != nil {
		return "", "", fmt.Errorf("build payload b64: %w", err)
	}

	pathWithoutAlgo := extractPathWithoutAlgo(fullURL)
	cosyDate := fmt.Sprintf("%d", time.Now().Unix())

	sigInput := payloadB64 + "\n" + s.CosyKey + "\n" + cosyDate + "\n" + encodedBody + "\n" + pathWithoutAlgo
	sig = md5Hex(sigInput)

	return payloadB64, sig, nil
}

// BuildBearer returns the full Authorization header value.
func (s *Session) BuildBearer(encodedBody, fullURL string) (string, string, error) {
	payloadB64, sig, err := s.SignRequest(encodedBody, fullURL)
	if err != nil {
		return "", "", err
	}
	bearer := "Bearer COSY." + payloadB64 + "." + sig
	return bearer, fmt.Sprintf("%d", time.Now().Unix()), nil
}

// BuildHeaders returns all required headers for a Lingma API request.
func (s *Session) BuildHeaders(encodedBody, fullURL string) (map[string]string, error) {
	bearer, cosyDate, err := s.BuildBearer(encodedBody, fullURL)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"Content-Type":           "application/json",
		"Accept":                 "text/event-stream",
		"Accept-Encoding":        "identity",
		"Cache-Control":          "no-cache",
		"Login-Version":          "v2",
		"Authorization":          bearer,
		"Cosy-Date":              cosyDate,
		"Cosy-Key":               s.CosyKey,
		"Cosy-Version":           "0.11.0",
		"Cosy-Clienttype":        "0",
		"Cosy-Machineid":         s.Mid,
		"Cosy-User":              s.UID,
		"Cosy-Organization-Id":   s.OrgID,
		"User-Agent":             "Go-http-client/1.1",
	}, nil
}

func (s *Session) buildPayloadB64() (string, error) {
	payload := map[string]string{
		"cosyVersion": "0.11.0",
		"ideVersion":  "",
		"info":        s.Info,
		"requestId":   newUUID(),
		"version":     "v1",
	}
	// Sort keys for consistent output (Go's json.Marshal sorts map keys)
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func extractPathWithoutAlgo(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	path := u.Path
	if strings.HasPrefix(path, "/algo") {
		path = path[len("/algo"):]
	}
	return path
}

func md5Hex(s string) string {
	h := md5.Sum([]byte(s))
	return fmt.Sprintf("%x", h)
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 1
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
