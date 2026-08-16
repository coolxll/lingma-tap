package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const (
	oauthCustomAlphabet   = "_doRTgHZBKcGVjlvpC,@aFSx#DPuNJme&i*MzLOEn)sUrthbf%Y^w.(kIQyXqWA!"
	oauthStandardAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
)

// OAuthCallback is the decoded V2 callback payload returned by Lingma's
// browser login flow. It intentionally contains no raw URL or callback state.
type OAuthCallback struct {
	UID                string
	AID                string
	Name               string
	SecurityOAuthToken string
	RefreshToken       string
	ExpireTime         int64
}

// OAuthLoginStatus contains only display-safe state for the desktop UI.
type OAuthLoginStatus struct {
	InProgress bool
	ExpiresAt  time.Time
	Error      string
	LoginURL   string
}

// ParseOAuthCallback decodes the Lingma V2 callback values. auth and token
// each contain exactly three newline-separated values after custom decoding.
func ParseOAuthCallback(authParam, tokenParam string) (OAuthCallback, error) {
	authRaw, err := decodeOAuthValue(authParam)
	if err != nil {
		return OAuthCallback{}, fmt.Errorf("decode auth: %w", err)
	}
	tokenRaw, err := decodeOAuthValue(tokenParam)
	if err != nil {
		return OAuthCallback{}, fmt.Errorf("decode token: %w", err)
	}

	authParts, err := splitOAuthParts(string(authRaw))
	if err != nil {
		return OAuthCallback{}, fmt.Errorf("parse auth: %w", err)
	}
	tokenParts, err := splitOAuthParts(string(tokenRaw))
	if err != nil {
		return OAuthCallback{}, fmt.Errorf("parse token: %w", err)
	}
	expireTime, err := strconv.ParseInt(tokenParts[2], 10, 64)
	if err != nil || expireTime <= 0 {
		return OAuthCallback{}, fmt.Errorf("parse token expiry")
	}
	if !strings.HasPrefix(tokenParts[0], "pt-") || !strings.HasPrefix(tokenParts[1], "rt-") {
		return OAuthCallback{}, fmt.Errorf("unexpected OAuth token format")
	}

	return OAuthCallback{
		UID:                authParts[0],
		AID:                authParts[1],
		Name:               authParts[2],
		SecurityOAuthToken: tokenParts[0],
		RefreshToken:       tokenParts[1],
		ExpireTime:         expireTime,
	}, nil
}

func splitOAuthParts(value string) ([]string, error) {
	parts := strings.Split(value, "\n")
	if len(parts) != 3 {
		return nil, fmt.Errorf("expected three fields")
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if parts[i] == "" {
			return nil, fmt.Errorf("field %d is empty", i+1)
		}
	}
	return parts, nil
}

func decodeOAuthValue(value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("value is empty")
	}
	if dollar := strings.IndexByte(value, '$'); dollar >= 0 {
		end := dollar
		for end < len(value) && value[end] == '$' {
			end++
		}
		value = value[:dollar] + value[end:]
	}
	if value == "" {
		return nil, fmt.Errorf("value is empty after padding removal")
	}

	blockSize := (len(value) + 2) / 3
	lastBlockSize := len(value) - 2*blockSize
	if lastBlockSize < 0 || lastBlockSize+blockSize > len(value) {
		return nil, fmt.Errorf("invalid block layout")
	}
	b2 := value[:lastBlockSize]
	b1 := value[lastBlockSize : lastBlockSize+blockSize]
	b0 := value[lastBlockSize+blockSize:]

	std := make([]byte, len(value))
	for i, c := range []byte(b0 + b1 + b2) {
		index := strings.IndexByte(oauthCustomAlphabet, c)
		if index < 0 {
			return nil, fmt.Errorf("invalid OAuth encoding character")
		}
		std[i] = oauthStandardAlphabet[index]
	}
	return base64.RawStdEncoding.DecodeString(string(std))
}

// CredentialsFromOAuth creates the locally generated COSY credentials that
// the gateway uses for Bearer signing after browser login completes.
func CredentialsFromOAuth(callback OAuthCallback, machineID string) (*Credentials, error) {
	if len(machineID) < aes.BlockSize {
		return nil, fmt.Errorf("machine ID is too short")
	}
	creds := &Credentials{
		MachineID:          machineID,
		UID:                callback.UID,
		AID:                callback.AID,
		Name:               callback.Name,
		UserType:           "personal_standard",
		SecurityOAuthToken: callback.SecurityOAuthToken,
		RefreshToken:       callback.RefreshToken,
		ExpireTime:         callback.ExpireTime,
	}
	if err := generateCosyCredentials(creds, rand.Reader); err != nil {
		return nil, err
	}
	return creds, nil
}

func generateCosyCredentials(creds *Credentials, random io.Reader) error {
	keyBytes := make([]byte, 8)
	if _, err := io.ReadFull(random, keyBytes); err != nil {
		return fmt.Errorf("generate COSY key: %w", err)
	}
	aesKey := []byte(hex.EncodeToString(keyBytes))

	encryptedKey, err := rsa.EncryptPKCS1v15(random, serverPubKey, aesKey)
	if err != nil {
		return fmt.Errorf("encrypt COSY key: %w", err)
	}
	inner := map[string]string{
		"name":                 creds.Name,
		"aid":                  creds.AID,
		"uid":                  creds.UID,
		"yx_uid":               "",
		"organization_id":      creds.OrganizationID,
		"organization_name":    "",
		"user_type":            creds.UserType,
		"security_oauth_token": creds.SecurityOAuthToken,
		"refresh_token":        creds.RefreshToken,
	}
	innerJSON, err := json.Marshal(inner)
	if err != nil {
		return fmt.Errorf("encode COSY user info: %w", err)
	}
	encryptedInfo, err := encryptWithAESKey(innerJSON, aesKey)
	if err != nil {
		return fmt.Errorf("encrypt COSY user info: %w", err)
	}

	creds.CosyKey = base64.StdEncoding.EncodeToString(encryptedKey)
	creds.EncryptUserInfo = base64.StdEncoding.EncodeToString(encryptedInfo)
	return nil
}

func encryptWithAESKey(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := make([]byte, len(plaintext)+padding)
	copy(padded, plaintext)
	for i := len(plaintext); i < len(padded); i++ {
		padded[i] = byte(padding)
	}
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, key).CryptBlocks(ciphertext, padded)
	return ciphertext, nil
}
