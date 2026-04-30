package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/lynn/lingma-tap/internal/auth"
	"github.com/lynn/lingma-tap/internal/bridge"
)

func main() {
	creds, err := auth.LoadCredentials()
	if err != nil {
		log.Fatalf("LoadCredentials: %v", err)
	}
	fmt.Printf("Auth: user=%s uid=%s mid=%s\n", creds.Name, creds.UID, creds.MachineID)

	session := auth.NewSession(creds)
	bridgeHandler := bridge.NewBridgeHandler(session)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", bridgeHandler.HandleModels)
	mux.HandleFunc("/v1/models/", bridgeHandler.HandleModels)
	mux.HandleFunc("/v1/chat/completions", bridgeHandler.HandleOpenAIChat)
	mux.HandleFunc("/v1/responses", bridgeHandler.HandleOpenAIResponses)
	mux.HandleFunc("/v1/messages", bridgeHandler.HandleAnthropicMessages)

	port := "9091"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	fmt.Printf("Bridge test server on http://127.0.0.1:%s\n", port)
	fmt.Println("Endpoints:")
	fmt.Println("  GET  /v1/models            (OpenAI Models)")
	fmt.Println("  POST /v1/chat/completions  (OpenAI Chat)")
	fmt.Println("  POST /v1/responses         (OpenAI Responses)")
	fmt.Println("  POST /v1/messages          (Anthropic)")

	log.Fatal(http.ListenAndServe("127.0.0.1:"+port, mux))
}
