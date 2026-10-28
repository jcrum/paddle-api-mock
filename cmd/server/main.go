package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/vlah-software-house/paddle-api-mock/internal/server"
)

func main() {
	port := flag.Int("port", 8081, "Server port")
	noAuth := flag.Bool("no-auth", false, "Disable API key validation")
	noSeed := flag.Bool("no-seed", false, "Start with empty store")
	webhookURL := flag.String("webhook-url", "", "Default webhook URL to register on startup")
	signingSecret := flag.String("signing-secret", "pdl_test_signing_secret", "Webhook signing secret")
	apiKey := flag.String("api-key", "test_paddle_api_key", "API key for authentication")
	flag.Parse()

	handler, _ := server.New(server.Config{APIKey: *apiKey, NoAuth: *noAuth, NoSeed: *noSeed, WebhookURL: *webhookURL, SigningSecret: *signingSecret})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("Paddle Mock API starting on %s (auth=%v, seed=%v)", addr, !*noAuth, !*noSeed)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
