package auth

import (
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
