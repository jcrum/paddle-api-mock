package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/vlah-software-house/paddle-api-mock/internal/models"
	"github.com/vlah-software-house/paddle-api-mock/internal/store"
)

// checkout simulates Paddle Checkout for recurring prices (POST /admin/checkout; also called by the paddle.js
// shim from a browser, hence CORS). Paddle reuses a customer by email, stores the address the customer entered,
// completes a transaction and creates a subscription from it; the checkout's custom data is copied to both.
func (h *AdminHandler) checkout(w http.ResponseWriter, r *http.Request) {
	allowBrowser(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var req models.CheckoutRequest
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, r, http.StatusBadRequest, "request_error", "bad_request", "Invalid JSON body")
		return
	}
	items := req.Items
	if len(items) == 0 && req.PriceID != "" {
		items = []models.CreateSubItemReq{{PriceID: req.PriceID, Quantity: 1}}
	}
	email := strings.TrimSpace(req.Email)
	if email == "" || len(items) == 0 {
		respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "email and items (or price_id) are required")
		return
	}
	for _, item := range items {
		price, ok := h.Store.GetPrice(item.PriceID)
		if !ok || price.Status != "active" {
			respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "price is not an active price: "+item.PriceID)
			return
		}
	}

	now := time.Now().UTC()
	customer, ok := h.Store.FindCustomerByEmail(email)
	if !ok {
		customer = &models.Customer{ID: store.NextID("ctm"), Email: email, Locale: "en", Status: "active", CustomData: map[string]string{}, CreatedAt: now, UpdatedAt: now}
		h.Store.SetCustomer(customer)
	}
	country := req.CountryCode
	if country == "" {
		country = "US"
	}
	address := &models.Address{
		ID: store.NextID("add"), CustomerID: customer.ID, Region: optional(req.Region), PostalCode: optional(req.PostalCode),
		CountryCode: strings.ToUpper(country), CustomData: map[string]string{}, Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	h.Store.SetAddress(address)

	sub, detail, err := buildSubscription(h.Store, customer.ID, &address.ID, items, "", "automatic", req.CustomData)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", detail)
		return
	}
	txn := newTransaction(h.Store, sub, subscriptionItemsToTransaction(sub), sub.CurrentBillingPeriod, "web", "completed")

	h.Webhook.Fire("subscription.created", sub)
	h.Webhook.Fire("transaction.completed", txn)
	respond(w, r, http.StatusCreated, map[string]interface{}{"customer": customer, "address": address, "subscription": sub, "transaction": txn})
}

func allowBrowser(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
