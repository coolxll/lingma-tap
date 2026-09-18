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
	"strconv"
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
	AccessToken        string `json:"access_token,omitempty"`
	RefreshToken       string `json:"refresh_token"`
	ExpireTime         int64  `json:"expire_time"`
	Name               string `json:"name"`
	Source             string `json:"source,omitempty"`
	IsQoder            bool   `json:"is_qoder,omitempty"`
}

type storedCredentials struct {
	Name               string `json:"name"`
	UID                string `json:"uid"`
	AID                string `json:"aid"`
	OrganizationID     string `json:"organization_id"`
	UserType           string `json:"user_type"`
	Key                string `json:"key"`
	EncryptUserInfo    string `json:"encrypt_user_info"`
	EncryptUserInfoAlt string `json:"encryptUserInfo"`
	SecurityOAuthToken string `json:"security_oauth_token"`
	SecurityOAuthAlt   string `json:"securityOAuthToken"`
	AccessToken        string `json:"access_token"`
	RefreshToken       string `json:"refresh_token"`
	RefreshTokenAlt    string `json:"refreshToken"`
	ExpireTime         any    `json:"expire_time"`
	ExpireTimeAlt      any    `json:"expireTime"`
}

var candidateIDRelPaths = []string{
	"id",
	filepath.Join("cache", "id"),
	filepath.Join(".auth", "id"),
	filepath.Join(".auth", "machine_id"),
	filepath.Join("cli", ".auth", "id"),
}

var candidateUserRelPaths = []string{
	"user",
	filepath.Join("cache", "user"),
	filepath.Join(".auth", "user"),
}

type authFileLocation struct {
	Dir      string
	IDFile   string
	UserFile string
	IsQoder  bool
}

// LoadCredentials reads and decrypts the local QoderCN or Lingma IDE auth files.
// It prioritizes QoderCN directories, then falls back to Lingma directories.
func LoadCredentials() (*Credentials, error) {
	loc, err := findAuthLocation()
	if err != nil {
		return nil, err
	}
	return loadFromFiles(loc.IDFile, loc.UserFile, loc.IsQoder)
}

func findAuthLocation() (*authFileLocation, error) {
	candidates := authDirCandidates()
	for _, dir := range candidates {
		var idFile, userFile string
		for _, rel := range candidateIDRelPaths {
			p := filepath.Join(dir, rel)
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				idFile = p
				break
			}
		}
		if idFile == "" {
			continue
		}
		for _, rel := range candidateUserRelPaths {
			p := filepath.Join(dir, rel)
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				userFile = p
				break
			}
		}
		if userFile == "" {
			continue
		}

		isQoder := strings.Contains(strings.ToLower(dir), "qoder") ||
			strings.Contains(strings.ToLower(userFile), "qoder")
		return &authFileLocation{
			Dir:      dir,
			IDFile:   idFile,
			UserFile: userFile,
			IsQoder:  isQoder,
		}, nil
	}
	return nil, fmt.Errorf("qoder/lingma auth files not found, searched: %s", strings.Join(candidates, ", "))
}

func findAuthDir() (string, error) {
	loc, err := findAuthLocation()
	if err != nil {
		return "", err
	}
	return loc.Dir, nil
}

func authDirCandidates() []string {
	var candidates []string

	home, _ := os.UserHomeDir()

	// 1. QoderCN candidates (preferred)
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".qoder-cn"),
			filepath.Join(home, ".qoder-cn", "shared_client"),
			filepath.Join(home, ".qoder-cn", "vscode", "sharedClientCache"),
			filepath.Join(home, ".qodercn"),
			filepath.Join(home, ".qodercn", "vscode", "sharedClientCache"),
		)
	}

	switch runtime.GOOS {
	case "darwin":
		if home != "" {
			candidates = append(candidates,
				filepath.Join(home, "Library", "Application Support", "QoderCN", "SharedClientCache"),
				filepath.Join(home, "Library", "Application Support", "Qoder", "SharedClientCache"),
				filepath.Join(home, "Library", "Application Support", "qoder-cn"),
			)
		}
	case "linux":
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			candidates = append(candidates,
				filepath.Join(xdg, "QoderCN"),
				filepath.Join(xdg, "QoderCN", "SharedClientCache"),
				filepath.Join(xdg, "Qoder", "SharedClientCache"),
			)
		}
		if home != "" {
			candidates = append(candidates,
				filepath.Join(home, ".config", "QoderCN"),
				filepath.Join(home, ".config", "QoderCN", "SharedClientCache"),
				filepath.Join(home, ".config", "Qoder", "SharedClientCache"),
				filepath.Join(home, ".local", "share", "QoderCN"),
			)
		}
	case "windows":
		for _, envName := range []string{"APPDATA", "LOCALAPPDATA"} {
			if val := os.Getenv(envName); val != "" {
				candidates = append(candidates,
					filepath.Join(val, "QoderCN"),
					filepath.Join(val, "qodercn"),
					filepath.Join(val, "QoderCN", "SharedClientCache"),
					filepath.Join(val, "Qoder", "SharedClientCache"),
				)
			}
		}
		if userProf := os.Getenv("USERPROFILE"); userProf != "" {
			candidates = append(candidates, filepath.Join(userProf, ".qoder-cn"))
		}
	}

	// 2. Lingma candidates (legacy fallback)
	switch runtime.GOOS {
	case "darwin":
		if home != "" {
			candidates = append(candidates,
				filepath.Join(home, "Library", "Application Support", "lingma", "SharedClientCache", "cache"),
				filepath.Join(home, "Library", "Application Support", "Lingma", "SharedClientCache"),
			)
		}
	case "linux":
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			candidates = append(candidates,
				filepath.Join(xdg, "lingma", "SharedClientCache", "cache"),
				filepath.Join(xdg, "Lingma", "SharedClientCache"),
			)
		}
		if home != "" {
			candidates = append(candidates,
				filepath.Join(home, ".config", "lingma", "SharedClientCache", "cache"),
				filepath.Join(home, ".config", "Lingma", "SharedClientCache"),
			)
		}
	case "windows":
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			candidates = append(candidates,
				filepath.Join(appdata, "lingma", "SharedClientCache", "cache"),
				filepath.Join(appdata, "Lingma", "SharedClientCache"),
			)
		}
	}

	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".lingma", "cache"),
			filepath.Join(home, ".lingma", "vscode", "sharedClientCache", "cache"),
			filepath.Join(home, ".lingma"),
		)
	}

	return candidates
}

func loadFromDir(dir string) (*Credentials, error) {
	idFile := filepath.Join(dir, "id")
	userFile := filepath.Join(dir, "user")
	if _, err := os.Stat(idFile); err != nil {
		for _, rel := range candidateIDRelPaths {
			p := filepath.Join(dir, rel)
			if info, errStat := os.Stat(p); errStat == nil && !info.IsDir() {
				idFile = p
				break
			}
		}
	}
	if _, err := os.Stat(userFile); err != nil {
		for _, rel := range candidateUserRelPaths {
			p := filepath.Join(dir, rel)
			if info, errStat := os.Stat(p); errStat == nil && !info.IsDir() {
				userFile = p
				break
			}
		}
	}
	isQoder := strings.Contains(strings.ToLower(dir), "qoder")
	return loadFromFiles(idFile, userFile, isQoder)
}

func loadFromFiles(idFile, userFile string, isQoder bool) (*Credentials, error) {
	// Read machineId
	machineIDContent, err := readTrimmed(idFile)
	if err != nil {
		return nil, fmt.Errorf("read machine id: %w", err)
	}
	machineID := parseMachineID(machineIDContent)

	// Read and decrypt user file
	userB64, err := readTrimmed(userFile)
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

	creds := credentialsFromStored(machineID, user)
	creds.Source = userFile
	creds.IsQoder = isQoder
	return creds, nil
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

func parseExpireTime(val any) int64 {
	switch v := val.(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

func credentialsFromStored(machineID string, user storedCredentials) *Credentials {
	secToken := user.SecurityOAuthToken
	if secToken == "" {
		secToken = user.SecurityOAuthAlt
	}
	encUserInfo := user.EncryptUserInfo
	if encUserInfo == "" {
		encUserInfo = user.EncryptUserInfoAlt
	}
	refreshToken := user.RefreshToken
	if refreshToken == "" {
		refreshToken = user.RefreshTokenAlt
	}
	expireTime := parseExpireTime(user.ExpireTime)
	if expireTime == 0 {
		expireTime = parseExpireTime(user.ExpireTimeAlt)
	}

	return &Credentials{
		MachineID:          machineID,
		UID:                user.UID,
		AID:                user.AID,
		OrganizationID:     user.OrganizationID,
		CosyKey:            user.Key,
		EncryptUserInfo:    encUserInfo,
		UserType:           user.UserType,
		SecurityOAuthToken: secToken,
		AccessToken:        user.AccessToken,
		RefreshToken:       refreshToken,
		ExpireTime:         expireTime,
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
