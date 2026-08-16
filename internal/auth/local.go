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
	AID                string `json:"aid"`
	OrganizationID     string `json:"organization_id"`
	CosyKey            string `json:"key"`
	EncryptUserInfo    string `json:"encrypt_user_info"`
	UserType           string `json:"user_type"`
	SecurityOAuthToken string `json:"security_oauth_token"`
	RefreshToken       string `json:"refresh_token"`
	ExpireTime         int64  `json:"expire_time"`
	Name               string `json:"name"`
}

type storedCredentials struct {
	Name               string `json:"name"`
	UID                string `json:"uid"`
	AID                string `json:"aid"`
	OrganizationID     string `json:"organization_id"`
	UserType           string `json:"user_type"`
	Key                string `json:"key"`
	EncryptUserInfo    string `json:"encrypt_user_info"`
	SecurityOAuthToken string `json:"security_oauth_token"`
	RefreshToken       string `json:"refresh_token"`
	ExpireTime         int64  `json:"expire_time"`
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
		filepath.Join(home, ".lingma", "cache"),
		filepath.Join(home, ".lingma", "vscode", "sharedClientCache", "cache"),
	)

	return candidates
}

func loadFromDir(dir string) (*Credentials, error) {
	// Read machineId
	machineIDContent, err := readTrimmed(filepath.Join(dir, "id"))
	if err != nil {
		return nil, fmt.Errorf("read machine id: %w", err)
	}
	machineID := parseMachineID(machineIDContent)

	// Read and decrypt user file
	userB64, err := readTrimmed(filepath.Join(dir, "user"))
	if err != nil {
		return nil, fmt.Errorf("read user file: %w", err)
	}

	userJSON, err := decryptUser(userB64, machineID)
	if err != nil {
		return nil, fmt.Errorf("decrypt user file: %w", err)
	}

	var user storedCredentials
	if err := json.Unmarshal(userJSON, &user); err != nil {
		return nil, fmt.Errorf("parse user json: %w", err)
	}

	return credentialsFromStored(machineID, user), nil
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

func parseMachineID(content string) string {
	machineID := strings.TrimSpace(content)
	if !strings.HasPrefix(machineID, "{") {
		return machineID
	}

	var legacy struct {
		MachineID string `json:"machine_id"`
	}
	if err := json.Unmarshal([]byte(machineID), &legacy); err == nil && legacy.MachineID != "" {
		return legacy.MachineID
	}
	return machineID
}

func credentialsFromStored(machineID string, user storedCredentials) *Credentials {
	return &Credentials{
		MachineID:          machineID,
		UID:                user.UID,
		AID:                user.AID,
		OrganizationID:     user.OrganizationID,
		CosyKey:            user.Key,
		EncryptUserInfo:    user.EncryptUserInfo,
		UserType:           user.UserType,
		SecurityOAuthToken: user.SecurityOAuthToken,
		RefreshToken:       user.RefreshToken,
		ExpireTime:         user.ExpireTime,
		Name:               user.Name,
	}
}

// LoadCredentialsFromBytes loads credentials from raw file content strings
// instead of reading from the local filesystem. Used for server mode where
// auth files are uploaded via HTTP.
func LoadCredentialsFromBytes(idContent, userContent string) (*Credentials, error) {
	machineID := parseMachineID(idContent)
	userB64 := strings.TrimSpace(userContent)

	userJSON, err := decryptUser(userB64, machineID)
	if err != nil {
		return nil, fmt.Errorf("decrypt user: %w", err)
	}

	var user storedCredentials
	if err := json.Unmarshal(userJSON, &user); err != nil {
		return nil, fmt.Errorf("parse user json: %w", err)
	}

	return credentialsFromStored(machineID, user), nil
}

// SaveExchangedCredentials serializes and encrypts credentials for persistent storage.
func SaveExchangedCredentials(creds *Credentials, dataDir string) error {
	if creds == nil {
		return fmt.Errorf("credentials are required")
	}

	userObj := storedCredentials{
		Name:               creds.Name,
		UID:                creds.UID,
		AID:                creds.AID,
		OrganizationID:     creds.OrganizationID,
		UserType:           creds.UserType,
		Key:                creds.CosyKey,
		EncryptUserInfo:    creds.EncryptUserInfo,
		SecurityOAuthToken: creds.SecurityOAuthToken,
		RefreshToken:       creds.RefreshToken,
		ExpireTime:         creds.ExpireTime,
	}
	userJSON, _ := json.Marshal(userObj)
	encryptedUser, err := encryptUser(userJSON, creds.MachineID)
	if err != nil {
		return fmt.Errorf("encrypt user data: %w", err)
	}

	return SaveCredentialsToDir(dataDir, creds.MachineID, encryptedUser)
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
	if err := os.MkdirAll(authDir, 0700); err != nil {
		return fmt.Errorf("create auth dir: %w", err)
	}
	if err := os.Chmod(authDir, 0700); err != nil {
		return fmt.Errorf("secure auth dir: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(authDir, "id"), []byte(strings.TrimSpace(idContent)), 0600); err != nil {
		return fmt.Errorf("write id file: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(authDir, "user"), []byte(strings.TrimSpace(userContent)), 0600); err != nil {
		return fmt.Errorf("write user file: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, contents []byte, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()

	if err = tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err = tmp.Write(contents); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// LoadCredentialsFromDir loads credentials from a specific directory.
// Used to reload persisted auth files in server mode.
func LoadCredentialsFromDir(dir string) (*Credentials, error) {
	return loadFromDir(dir)
}
