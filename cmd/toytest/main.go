package main

import (
	"bufio"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/coolxll/lingma-tap/internal/encoding"
)

func md5Hex(s string) string {
	h := md5.Sum([]byte(s))
	return fmt.Sprintf("%x", h)
}

func main() {
	// Fresh credentials from record #616
	cosyKey := "vL1gXMp2fBYzMp1bwMDYTBpL1skqZTKbmJy1xoBf98nZqz0tMPSSaeU47+v6vf0f1EyBFLOzeo+fkkYjRc0wjNIHjmoeSKWesjYO9FLevTItdryK0PaXVvL0qubzEMruZG3Qb8vwTGFFNy8dfsGKJV24BBUlNQqk+juTjEr9HoI="
	cosyDate := "1777525787"
	cosyUser := "206119456452928225"
	cosyOrg := "6135b3ce57c27abcb6458304"
	cosyMid := "48344c30-3456-412d-b134-4d6d48344c2d"
	payloadB64 := "eyJjb3N5VmVyc2lvbiI6IjAuMTEuMCIsImlkZVZlcnNpb24iOiIiLCJpbmZvIjoiK2hEd3U3Y091eEY5T25ld2UzQUFET3B1U0FHQUlFMzZmeEI3Ykl1K0xma1lxeFRPOGpxUmFTbU9UdG5pTVlEcEd6REhwV1BZS1pGZkF5ZHFnZUwvVWI2ekJHTHFBTFVkckFjSktxMHRMdEZrUjI1eEZJU25SZnQ1RFd4YXBwdXliQ0M0NWt0V2ZHMnhlNzFIanN1eEVxYk8ybEdpLzlwK0llTFluU1JUam9rcENjK3VtNzFMc2cwWjU2a2xJSTdTTDQ5Z2J1eWI4UDBrL1dZb3lmc2ZsZ29OOGdxZENZVERQUjRGb1lwWjlzV3NRU1FCRWQ3L3E0dStXeG53WVdpMG95ZHNLSm5GdkJPSFZBeWllRkErbjhwTUR6SEVSNmxGclpGVysxdzlQVjBQMEpJbmd5SVFzTnBSdzVoUzNFRWtnSVdhVWVnZCt2aFZEcXpsNkkwSzZKN0NndHdGSjVxRmhHOFEzVWd1WjNLbzVzL3hXMmlNd2FKV29Qd2RVNHRYY1lFNEVNWlRxVFAzaEZrYU40UDJBbXdQNkNHVmI4a0NRVXg2WGMxTVNYZThiT3F0ZTVuNTN4ZGhNWFhUNFprZlpzTHJtcjU3dHVreEpBdmRaaGRHUXpqSUt5OEluMFppTFFEeElyb2M0dWljM2xOQnlOSHZYYU1wb0dWZEh2bWtaS2UwcmRiN01vUWk5QzVCT25VYlY3WE1uYzBLbkRBNHBZMGtETzkwRjNQUzhxOU1hb0Q3QXliZjRVQmVFbitrYWM2QjVVb3dCbnFBeFh4MWI2bmYyZlBXOVYxZUErQnh6c2M4ZGF2R2VRdjV4U2hvM3dmai9RaVJRelhHbXc1d3RGWGp3ZHE3eW1RTnRNcFpJbmJ2TXRXdklPbUU3UDJNT2Fqblo2Y1NEamhsMEZnZjk2R2QzWlJnem5nbElpQlVjVWFxQUs5Mmh5cm9uS2FBOHlHb0FxSGdHSHdWek9Xa3ZWOFdobFM5a05zPSIsInJlcXVlc3RJZCI6ImQ4OTZlMWFiLWY3OTItNGExZC1hMzFiLWZhY2MyMDkyZDk4MCIsInZlcnNpb24iOiJ2MSJ9"

	pathWithoutAlgo := "/api/v2/service/pro/sse/agent_chat_generation"
	url := "https://lingma-api.tongyi.aliyun.com/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1"

	tests := []struct {
		name string
		body string
	}{
		{
			"simple chat",
			`{"request_id":"test-tool-001","request_set_id":"","chat_record_id":"test-tool-001","stream":true,"image_urls":null,"is_reply":false,"is_retry":false,"session_id":"test-session-tool-001","code_language":"","source":0,"version":"3","chat_prompt":"","parameters":{"temperature":0.1},"aliyun_user_type":"enterprise_standard","agent_id":"agent_common","task_id":"question_refine","model_config":{"key":"","display_name":"","model":"","format":"","is_vl":false,"is_reasoning":false,"api_key":"","url":"","source":"","max_input_tokens":0,"enable":false,"price_factor":0,"original_price_factor":0,"is_default":false,"is_new":false,"exclude_tags":null,"tags":null,"icon":null,"strategies":null},"messages":[{"role":"system","content":"You are a helpful assistant."},{"role":"user","content":"What is the weather in Beijing?"}],"tools":[{"type":"function","function":{"name":"get_weather","description":"Get the current weather for a location","parameters":{"type":"object","properties":{"location":{"type":"string","description":"The city name"}},"required":["location"]}}}],"business":{"product":"ide","version":"0.11.0","type":"chat","id":"test-biz-tool-001","begin_at":0,"stage":"start","name":"test","relation":{}}}`,
		},
	}

	for _, tt := range tests {
		encoded := encoding.Encode([]byte(tt.body))
		sigInput := payloadB64 + "\n" + cosyKey + "\n" + cosyDate + "\n" + encoded + "\n" + pathWithoutAlgo
		sig := md5Hex(sigInput)
		bearer := "Bearer COSY." + payloadB64 + "." + sig

		req, _ := http.NewRequest("POST", url, strings.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Accept-Encoding", "identity")
		req.Header.Set("Login-Version", "v2")
		req.Header.Set("Authorization", bearer)
		req.Header.Set("Cosy-Date", cosyDate)
		req.Header.Set("Cosy-Key", cosyKey)
		req.Header.Set("Cosy-Version", "0.11.0")
		req.Header.Set("Cosy-Clienttype", "0")
		req.Header.Set("Cosy-Machineid", cosyMid)
		req.Header.Set("Cosy-User", cosyUser)
		req.Header.Set("Cosy-Organization-Id", cosyOrg)
		req.Header.Set("User-Agent", "Go-http-client/1.1")
		req.Header.Set("X-Request-Id", "test-tool-001")
		req.Header.Set("Cache-Control", "no-cache")

		fmt.Printf("=== %s (len=%d) ===\n", tt.name, len(encoded))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Printf("Error: %v\n\n", err)
			continue
		}
		fmt.Printf("HTTP %d\n", resp.StatusCode)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 64*1024)
		count := 0
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "data:") {
				var w struct {
					Body string `json:"body"`
				}
				json.Unmarshal([]byte(line[5:]), &w)
				if w.Body != "" {
					fmt.Printf("  %s\n", w.Body)
					count++
					if count > 20 {
						fmt.Println("  ...")
						break
					}
					continue
				}
			}
			fmt.Printf("  %s\n", line)
		}
		resp.Body.Close()
		fmt.Println()
	}
}
