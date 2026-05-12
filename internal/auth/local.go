package auth

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Credentials struct {
	MachineID          string `json:"machine_id"`
	UID                string `json:"uid"`
	OrganizationID     string `json:"organization_id"`
	CosyKey            string `json:"key"`
	EncryptUserInfo    string `json:"encrypt_user_info"`
	UserType           string `json:"user_type"`
	SecurityOAuthToken string `json:"security_oauth_token"`
	RefreshToken       string `json:"refresh_token"`
	ExpireTime         int64  `json:"expire_time"`
	Name               string `json:"name"`
}

// LoadCredentials reads and decrypts the local Lingma IDE auth files.
func LoadCredentials() (*Credentials, error) {
	dir, err := findAuthDir()
	if err != nil {
		return nil, err
	}
	return loadFromDir(dir)
}

func findAuthDir() (string, error) {
	candidates := authDirCandidates()
	for _, dir := range candidates {
		idFile := filepath.Join(dir, "id")
		userFile := filepath.Join(dir, "user")
		if _, err := os.Stat(idFile); err == nil {
			if _, err := os.Stat(userFile); err == nil {
				return dir, nil
			}
		}
	}
	return "", fmt.Errorf("lingma auth files not found, searched: %s", strings.Join(candidates, ", "))
}

func authDirCandidates() []string {
	var candidates []string

	home, _ := os.UserHomeDir()

	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates,
			filepath.Join(home, "Library", "Application Support", "lingma", "SharedClientCache", "cache"),
		)
	case "linux":
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			candidates = append(candidates, filepath.Join(xdg, "lingma", "SharedClientCache", "cache"))
		}
		candidates = append(candidates,
			filepath.Join(home, ".config", "lingma", "SharedClientCache", "cache"),
		)
	case "windows":
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			candidates = append(candidates, filepath.Join(appdata, "lingma", "SharedClientCache", "cache"))
		}
	}

	// Fallback: VSCode extension path (all platforms)
	candidates = append(candidates,
		filepath.Join(home, ".lingma", "vscode", "sharedClientCache", "cache"),
	)

	return candidates
}

func loadFromDir(dir string) (*Credentials, error) {
	// Read machineId
	machineID, err := readTrimmed(filepath.Join(dir, "id"))
	if err != nil {
		return nil, fmt.Errorf("read machine id: %w", err)
	}

	// Read and decrypt user file
	userB64, err := readTrimmed(filepath.Join(dir, "user"))
	if err != nil {
		return nil, fmt.Errorf("read user file: %w", err)
	}

	userJSON, err := decryptUser(userB64, machineID)
	if err != nil {
		return nil, fmt.Errorf("decrypt user file: %w", err)
	}

	var user struct {
		Name               string `json:"name"`
		UID                string `json:"uid"`
		OrganizationID     string `json:"organization_id"`
		UserType           string `json:"user_type"`
		Key                string `json:"key"`
		EncryptUserInfo    string `json:"encrypt_user_info"`
		SecurityOAuthToken string `json:"security_oauth_token"`
		RefreshToken       string `json:"refresh_token"`
		ExpireTime         int64  `json:"expire_time"`
	}
	if err := json.Unmarshal(userJSON, &user); err != nil {
		return nil, fmt.Errorf("parse user json: %w", err)
	}

	return &Credentials{
		MachineID:          machineID,
		UID:                user.UID,
		OrganizationID:     user.OrganizationID,
		CosyKey:            user.Key,
		EncryptUserInfo:    user.EncryptUserInfo,
		UserType:           user.UserType,
		SecurityOAuthToken: user.SecurityOAuthToken,
		RefreshToken:       user.RefreshToken,
		ExpireTime:         user.ExpireTime,
		Name:               user.Name,
	}, nil
}

func decryptUser(b64, machineID string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	if len(machineID) < 16 {
		return nil, fmt.Errorf("machineId too short: %d chars", len(machineID))
	}
	key := []byte(machineID[:16])

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes new cipher: %w", err)
	}

	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext not block-aligned: len=%d", len(ciphertext))
	}

	mode := cipher.NewCBCDecrypter(block, key)
	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)

	// PKCS7 unpadding
	if len(plaintext) == 0 {
		return nil, fmt.Errorf("empty plaintext after decryption")
	}
	pad := int(plaintext[len(plaintext)-1])
	if pad == 0 || pad > aes.BlockSize {
		return nil, fmt.Errorf("invalid pkcs7 padding: %d", pad)
	}
	for i := len(plaintext) - pad; i < len(plaintext); i++ {
		if plaintext[i] != byte(pad) {
			return nil, fmt.Errorf("invalid pkcs7 padding byte at %d", i)
		}
	}
	plaintext = plaintext[:len(plaintext)-pad]

	return plaintext, nil
}

func readTrimmed(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// LoadCredentialsFromBytes loads credentials from raw file content strings
// instead of reading from the local filesystem. Used for server mode where
// auth files are uploaded via HTTP.
func LoadCredentialsFromBytes(idContent, userContent string) (*Credentials, error) {
	machineID := strings.TrimSpace(idContent)
	userB64 := strings.TrimSpace(userContent)

	userJSON, err := decryptUser(userB64, machineID)
	if err != nil {
		return nil, fmt.Errorf("decrypt user: %w", err)
	}

	var user struct {
		Name               string `json:"name"`
		UID                string `json:"uid"`
		OrganizationID     string `json:"organization_id"`
		UserType           string `json:"user_type"`
		Key                string `json:"key"`
		EncryptUserInfo    string `json:"encrypt_user_info"`
		SecurityOAuthToken string `json:"security_oauth_token"`
		RefreshToken       string `json:"refresh_token"`
		ExpireTime         int64  `json:"expire_time"`
	}
	if err := json.Unmarshal(userJSON, &user); err != nil {
		return nil, fmt.Errorf("parse user json: %w", err)
	}

	return &Credentials{
		MachineID:          machineID,
		UID:                user.UID,
		OrganizationID:     user.OrganizationID,
		CosyKey:            user.Key,
		EncryptUserInfo:    user.EncryptUserInfo,
		UserType:           user.UserType,
		SecurityOAuthToken: user.SecurityOAuthToken,
		RefreshToken:       user.RefreshToken,
		ExpireTime:         user.ExpireTime,
		Name:               user.Name,
	}, nil
}

// SaveExchangedCredentials serializes and encrypts credentials for persistent storage.
func SaveExchangedCredentials(creds *Credentials, dataDir string) error {
	// 1. Prepare id file content
	idObj := map[string]string{"machine_id": creds.MachineID}
	idJSON, _ := json.Marshal(idObj)

	// 2. Prepare user file content (encrypted)
	userObj := map[string]interface{}{
		"name":                 creds.Name,
		"uid":                  creds.UID,
		"organization_id":      creds.OrganizationID,
		"user_type":            creds.UserType,
		"key":                  creds.CosyKey,
		"encrypt_user_info":    creds.EncryptUserInfo,
		"security_oauth_token": creds.SecurityOAuthToken,
		"refresh_token":        creds.RefreshToken,
		"expire_time":         creds.ExpireTime,
	}
	userJSON, _ := json.Marshal(userObj)
	encryptedUser, err := encryptUser(userJSON, creds.MachineID)
	if err != nil {
		return fmt.Errorf("encrypt user data: %w", err)
	}

	return SaveCredentialsToDir(dataDir, string(idJSON), encryptedUser)
}

func encryptUser(plaintext []byte, machineID string) (string, error) {
	if len(machineID) < 16 {
		return "", fmt.Errorf("machineId too short")
	}
	key := []byte(machineID[:16])

	// PKCS7 padding
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	plaintext = append(plaintext, padtext...)

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	ciphertext := make([]byte, len(plaintext))
	mode := cipher.NewCBCEncrypter(block, key)
	mode.CryptBlocks(ciphertext, plaintext)

	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// SaveCredentialsToDir persists uploaded auth files to disk so they survive restarts.
func SaveCredentialsToDir(dataDir, idContent, userContent string) error {
	authDir := filepath.Join(dataDir, "auth")
	if err := os.MkdirAll(authDir, 0755); err != nil {
		return fmt.Errorf("create auth dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "id"), []byte(strings.TrimSpace(idContent)), 0600); err != nil {
		return fmt.Errorf("write id file: %w", err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "user"), []byte(strings.TrimSpace(userContent)), 0600); err != nil {
		return fmt.Errorf("write user file: %w", err)
	}
	return nil
}

// LoadCredentialsFromDir loads credentials from a specific directory.
// Used to reload persisted auth files in server mode.
func LoadCredentialsFromDir(dir string) (*Credentials, error) {
	return loadFromDir(dir)
}
