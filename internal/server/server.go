// Package server builds the mock's HTTP handler, shared by the binary and the tests.
package server

import (
	"log"
	"net/http"
	"time"

	"github.com/vlah-software-house/paddle-api-mock/internal/handlers"
	"github.com/vlah-software-house/paddle-api-mock/internal/middleware"
	"github.com/vlah-software-house/paddle-api-mock/internal/models"
	"github.com/vlah-software-house/paddle-api-mock/internal/seed"
	"github.com/vlah-software-house/paddle-api-mock/internal/store"
	"github.com/vlah-software-house/paddle-api-mock/internal/webhook"
)

type Config struct {
	APIKey        string
	NoAuth        bool
	NoSeed        bool
	WebhookURL    string
	SigningSecret string
}

// DefaultEvents are the events a default webhook URL subscribes to.
var DefaultEvents = []string{
	"subscription.created",
	"subscription.updated",
	"subscription.activated",
	"subscription.canceled",
	"subscription.past_due",
	"transaction.completed",
	"transaction.payment_failed",
}

// New returns the handler with middleware applied, and its store.
func New(cfg Config) (http.Handler, *store.Store) {
	s := store.New()
	if !cfg.NoSeed {
		seed.Load(s)
		log.Println("Seed data loaded")
	}

	notifier := webhook.New(s, cfg.SigningSecret)

	if cfg.WebhookURL != "" {
		now := time.Now().UTC()
		s.SetNotificationSetting(&models.NotificationSetting{
			ID:               store.NextID("ntfset"),
			Description:      "Default webhook (from CLI)",
			Destination:      cfg.WebhookURL,
			Active:           true,
			APIVersion:       1,
			SubscribedEvents: DefaultEvents,
			Type:             "url",
			CreatedAt:        now,
			UpdatedAt:        now,
		})
		log.Printf("Registered default webhook URL: %s", cfg.WebhookURL)
	}

	resources := map[string]http.Handler{
		"products":              &handlers.ProductsHandler{Store: s},
		"prices":                &handlers.PricesHandler{Store: s},
		"customers":             &handlers.CustomersHandler{Store: s},
		"subscriptions":         &handlers.SubscriptionsHandler{Store: s, Webhook: notifier},
		"transactions":          &handlers.TransactionsHandler{Store: s},
		"adjustments":           &handlers.AdjustmentsHandler{Store: s, Webhook: notifier},
		"events":                &handlers.EventsHandler{Store: s},
		"notification-settings": &handlers.NotificationSettingsHandler{Store: s},
	}

	mux := http.NewServeMux()
	// Paddle's paths have no version prefix; /v1/ stays as an alias for existing consumers.
	for name, handler := range resources {
		for _, prefix := range []string{"", "/v1"} {
			mux.Handle(prefix+"/"+name, handler)
			mux.Handle(prefix+"/"+name+"/", handler)
		}
	}

	mux.Handle("/admin/", &handlers.AdminHandler{Store: s, Webhook: notifier, SeedEnabled: !cfg.NoSeed, DefaultWebhookURL: cfg.WebhookURL})
	mux.HandleFunc("/paddle.js", handlers.PaddleJS)
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	var handler http.Handler = mux
	handler = middleware.Auth(cfg.APIKey, cfg.NoAuth)(handler)
	handler = middleware.JSON(handler)
	handler = middleware.RequestID(handler)
	return handler, s
}
