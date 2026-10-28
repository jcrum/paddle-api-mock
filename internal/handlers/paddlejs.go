package handlers

import (
	_ "embed"
	"net/http"
	"strings"
)

//go:embed paddle.js
var paddleJS string

// PaddleJS serves a development stand-in for Paddle.js. Paddle.Checkout.open completes a checkout at once
// through POST /admin/checkout (no card form), reports checkout.completed to the eventCallback and follows
// settings.successUrl, as Paddle.js does after a successful checkout.
func PaddleJS(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	w.Header().Set("Content-Type", "application/javascript")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write([]byte(strings.Replace(paddleJS, "__MOCK_ORIGIN__", scheme+"://"+r.Host, 1)))
}
