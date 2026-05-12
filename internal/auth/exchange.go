package auth

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coolxll/lingma-tap/internal/encoding"
)

const (
	// signatureSecret is a common secret used in the Lingma protocol for API signature.
	// It is NOT a user-specific private key.
	signatureSecret = "d2FyLCB3YXIgbmV2ZXIgY2hhbmdlcw=="
	apiBaseURL      = "https://lingma-api.tongyi.aliyun.com"
)

// ExchangeCallback handles the final step of OAuth credential exchange.
// It takes the raw parameters from the callback URL and performs the grantAuthInfos handshake.
func ExchangeCallback(userId, securityOauthToken, machineID string) (*Credentials, error) {
	// 1. Initialize temporary credentials with what we have
	creds := &Credentials{
		UID:                userId,
		SecurityOAuthToken: securityOauthToken,
		MachineID:          machineID,
	}


	// 2. Generate a fresh CosyKey (16-byte random, RSA encrypted)
	session, err := NewSessionWithFreshKey(creds)
	if err != nil {
		return nil, fmt.Errorf("generate fresh key: %w", err)
	}
	creds.CosyKey = session.CosyKey

	// 3. Call grantAuthInfos to "activate" the tokens and the new key
	if err := grantAuthInfos(creds); err != nil {
		return nil, fmt.Errorf("grant auth infos: %w", err)
	}

	// 4. Verify status and get full user info
	fullCreds, err := fetchUserStatus(creds)
	if err != nil {
		return nil, fmt.Errorf("fetch user status: %w", err)
	}

	// 5. Final login call (often required to finalize session on server)
	if err := userLogin(fullCreds); err != nil {
		// Log but continue, as we already have the tokens
		fmt.Printf("Warning: final login call failed: %v\n", err)
	}

	// 6. "Activate" CosyKey with a V2 call (like model/list)
	if err := activateCosyKey(fullCreds); err != nil {
		fmt.Printf("Warning: CosyKey activation via V2 call failed: %v\n", err)
	}

	return fullCreds, nil
}

func activateCosyKey(creds *Credentials) error {
	session := NewSession(creds)
	url := apiBaseURL + "/algo/api/v2/model/list"
	
	// GET request with Bearer/Cosy headers
	headers, err := session.BuildHeaders("", url)
	if err != nil {
		return err
	}

	req, _ := http.NewRequest("GET", url, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("activation API error (%d): %s", resp.StatusCode, string(b))
	}
	return nil
}

func userLogin(creds *Credentials) error {
	payload := map[string]interface{}{
		"userId":          creds.UID,
		"orgId":           creds.OrganizationID,
		"clientIp":        "127.0.0.1",
		"encryptUserInfo": creds.EncryptUserInfo,
	}

	_, err := callV3API("POST", "/algo/api/v3/user/login", payload, creds)
	return err
}

func grantAuthInfos(creds *Credentials) error {
	payload := map[string]interface{}{
		"userId":             creds.UID,
		"personalToken":      "",
		"securityOauthToken": creds.SecurityOAuthToken,
		"refreshToken":       "",
		"needRefresh":        false,
		"authInfo": map[string]string{
			"key": creds.CosyKey,
		},
	}

	body, err := callV3API("POST", "/algo/api/v3/user/grantAuthInfos", payload, creds)
	if err != nil {
		return err
	}

	var resp []struct {
		OrgId string `json:"orgId"`
	}
	if err := json.Unmarshal(body, &resp); err == nil && len(resp) > 0 {
		creds.OrganizationID = resp[0].OrgId
	}

	return nil
}

func fetchUserStatus(creds *Credentials) (*Credentials, error) {
	payload := map[string]interface{}{
		"userId":             creds.UID,
		"personalToken":      "",
		"securityOauthToken": creds.SecurityOAuthToken,
		"refreshToken":       "",
		"needRefresh":        true,
		"authInfo":           map[string]interface{}{},
	}

	// The status API returns detailed user info in JSON
	body, err := callV3API("POST", "/algo/api/v3/user/status", payload, creds)
	if err != nil {
		return nil, err
	}
	
	fmt.Printf("DEBUG: user/status raw body: %s\n", string(body))

	var resp struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		UserType        string `json:"userType"`
		EncryptUserInfo string `json:"encryptUserInfo"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse status response: %w", err)
	}

	creds.UID = resp.ID
	creds.Name = resp.Name
	creds.UserType = resp.UserType
	creds.EncryptUserInfo = resp.EncryptUserInfo

	return creds, nil
}

func callV3API(method, path string, payload map[string]interface{}, creds *Credentials) ([]byte, error) {
	innerJSON, _ := json.Marshal(payload)
	
	// Match the working logic in cmd/test_sig/main.go
	// wrapper: {"payload":"<inner_json_as_string>","encodeVersion":"1"}
	// then QoderEncode the entire wrapper
	wrapper := map[string]string{
		"payload":       string(innerJSON),
		"encodeVersion": "1",
	}
	wrapperJSON, _ := json.Marshal(wrapper)
	encodedBody := encoding.Encode(wrapperJSON)

	url := apiBaseURL + path + "?Encode=1"
	date := time.Now().UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
	
	// MD5("cosy&" + secret + "&" + date)
	sigInput := "cosy&" + signatureSecret + "&" + date
	h := md5.Sum([]byte(sigInput))
	signature := fmt.Sprintf("%x", h)

	req, _ := http.NewRequest(method, url, strings.NewReader(encodedBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Appcode", "cosy")
	req.Header.Set("Date", date)
	req.Header.Set("Signature", signature)
	req.Header.Set("Cosy-Machineid", creds.MachineID)
	req.Header.Set("Login-Version", "v2")
	req.Header.Set("Cosy-Version", "0.11.0")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(b))
	}

	return io.ReadAll(resp.Body)
}
