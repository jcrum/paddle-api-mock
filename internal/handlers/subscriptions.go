package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vlah-software-house/paddle-api-mock/internal/models"
	"github.com/vlah-software-house/paddle-api-mock/internal/store"
	"github.com/vlah-software-house/paddle-api-mock/internal/webhook"
)

type SubscriptionsHandler struct {
	Store   *store.Store
	Webhook *webhook.Notifier
}

func (h *SubscriptionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := resourcePath(r, "subscriptions")

	if path == "" {
		switch r.Method {
		case http.MethodGet:
			h.list(w, r)
		case http.MethodPost:
			h.create(w, r)
		default:
			respondError(w, r, http.StatusMethodNotAllowed, "request_error", "method_not_allowed", "Method not allowed")
		}
		return
	}

	// Check for sub-routes: {id}/activate, {id}/charge
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]

	if len(parts) == 2 {
		switch parts[1] {
		case "activate":
			if r.Method == http.MethodPost {
				h.activate(w, r, id)
				return
			}
		case "charge":
			if r.Method == http.MethodPost {
				h.charge(w, r, id)
				return
			}
		}
		respondError(w, r, http.StatusNotFound, "request_error", "not_found", "Route not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.get(w, r, id)
	case http.MethodPatch:
		h.update(w, r, id)
	default:
		respondError(w, r, http.StatusMethodNotAllowed, "request_error", "method_not_allowed", "Method not allowed")
	}
}

func (h *SubscriptionsHandler) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	subs := h.Store.ListSubscriptions()
	subs = filterIn(subs, query.Get("customer_id"), func(s *models.Subscription) string { return s.CustomerID })
	subs = filterIn(subs, query.Get("status"), func(s *models.Subscription) string { return s.Status })
	respondPage(w, r, subs, func(s *models.Subscription) string { return s.ID }, standardList)
}

func (h *SubscriptionsHandler) get(w http.ResponseWriter, r *http.Request, id string) {
	sub, ok := h.Store.GetSubscription(id)
	if !ok {
		respondError(w, r, http.StatusNotFound, "request_error", "not_found", "Subscription not found")
		return
	}
	respond(w, r, http.StatusOK, sub)
}

// create is a test helper of the original mock (real Paddle creates subscriptions from checkouts; see
// POST /admin/checkout).
func (h *SubscriptionsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req models.CreateSubscriptionRequest
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, r, http.StatusBadRequest, "request_error", "bad_request", "Invalid JSON body")
		return
	}

	if req.CustomerID == "" || len(req.Items) == 0 {
		respondError(w, r, http.StatusBadRequest, "request_error", "validation_error", "customer_id and items are required")
		return
	}

	if _, ok := h.Store.GetCustomer(req.CustomerID); !ok {
		respondError(w, r, http.StatusBadRequest, "request_error", "validation_error", "Customer not found")
		return
	}

	sub, detail, err := buildSubscription(h.Store, req.CustomerID, nil, req.Items, req.CurrencyCode, req.CollectionMode, req.CustomData)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "request_error", "validation_error", detail)
		return
	}

	// Initial transaction
	newTransaction(h.Store, sub, subscriptionItemsToTransaction(sub), sub.CurrentBillingPeriod, "subscription_recurring", "completed")

	h.Webhook.Fire("subscription.created", sub)

	respond(w, r, http.StatusCreated, sub)
}

func (h *SubscriptionsHandler) update(w http.ResponseWriter, r *http.Request, id string) {
	sub, ok := h.Store.GetSubscription(id)
	if !ok {
		respondError(w, r, http.StatusNotFound, "request_error", "not_found", "Subscription not found")
		return
	}

	var req models.UpdateSubscriptionRequest
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, r, http.StatusBadRequest, "request_error", "bad_request", "Invalid JSON body")
		return
	}

	if req.ScheduledChange != nil {
		switch req.ScheduledChange.Action {
		case "cancel":
			effectiveAt := time.Now().UTC()
			if sub.CurrentBillingPeriod != nil {
				effectiveAt = sub.CurrentBillingPeriod.EndsAt
			}
			sub.ScheduledChange = &models.ScheduledChange{
				Action:      "cancel",
				EffectiveAt: effectiveAt,
			}
		case "pause":
			effectiveAt := time.Now().UTC()
			if sub.CurrentBillingPeriod != nil {
				effectiveAt = sub.CurrentBillingPeriod.EndsAt
			}
			sub.ScheduledChange = &models.ScheduledChange{
				Action:      "pause",
				EffectiveAt: effectiveAt,
			}
		case "resume":
			sub.ScheduledChange = nil
			if sub.Status == "paused" {
				sub.Status = "active"
				sub.PausedAt = nil
			}
		}
	}

	if req.CustomData != nil {
		sub.CustomData = req.CustomData
	}

	// Handle item changes (price change)
	if len(req.Items) > 0 {
		now := time.Now().UTC()
		newItems := make([]models.SubscriptionItem, 0)
		for _, item := range req.Items {
			price, ok := h.Store.GetPrice(item.PriceID)
			if !ok {
				respondError(w, r, http.StatusBadRequest, "request_error", "validation_error", "Price not found: "+item.PriceID)
				return
			}
			qty := item.Quantity
			if qty == 0 {
				qty = 1
			}
			subItem := models.SubscriptionItem{
				Status:    sub.Status,
				Quantity:  qty,
				Recurring: true,
				CreatedAt: now,
				UpdatedAt: now,
				Price:     *price,
			}
			if prod, ok := h.Store.GetProduct(price.ProductID); ok {
				subItem.Product = prod
			}
			newItems = append(newItems, subItem)
		}
		sub.Items = newItems
	}

	sub.UpdatedAt = time.Now().UTC()
	h.Store.SetSubscription(sub)
	h.Webhook.Fire("subscription.updated", sub)

	respond(w, r, http.StatusOK, sub)
}

func (h *SubscriptionsHandler) activate(w http.ResponseWriter, r *http.Request, id string) {
	sub, ok := h.Store.GetSubscription(id)
	if !ok {
		respondError(w, r, http.StatusNotFound, "request_error", "not_found", "Subscription not found")
		return
	}

	if sub.Status != "trialing" {
		respondError(w, r, http.StatusConflict, "request_error", "conflict", "Subscription is not trialing")
		return
	}

	now := time.Now().UTC()
	sub.Status = "active"
	sub.FirstBilledAt = &now
	startNextPeriod(sub, now)
	for i := range sub.Items {
		sub.Items[i].Status = "active"
		sub.Items[i].TrialDates = nil
	}

	h.Store.SetSubscription(sub)

	// Transaction for the first billing
	newTransaction(h.Store, sub, subscriptionItemsToTransaction(sub), sub.CurrentBillingPeriod, "subscription_recurring", "completed")

	h.Webhook.Fire("subscription.activated", sub)

	respond(w, r, http.StatusOK, sub)
}

func (h *SubscriptionsHandler) charge(w http.ResponseWriter, r *http.Request, id string) {
	sub, ok := h.Store.GetSubscription(id)
	if !ok {
		respondError(w, r, http.StatusNotFound, "request_error", "not_found", "Subscription not found")
		return
	}

	var req models.ChargeRequest
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, r, http.StatusBadRequest, "request_error", "bad_request", "Invalid JSON body")
		return
	}

	items := make([]models.TransactionItem, 0, len(req.Items))
	for _, item := range req.Items {
		price, ok := h.Store.GetPrice(item.PriceID)
		if !ok {
			respondError(w, r, http.StatusBadRequest, "request_error", "validation_error", "Price not found: "+item.PriceID)
			return
		}
		qty := item.Quantity
		if qty == 0 {
			qty = 1
		}
		txnItem := models.TransactionItem{PriceID: price.ID, Quantity: qty, Price: *price}
		if prod, ok := h.Store.GetProduct(price.ProductID); ok {
			txnItem.Product = prod
		}
		items = append(items, txnItem)
	}

	txn := newTransaction(h.Store, sub, items, nil, "subscription_charge", "completed")
	h.Webhook.Fire("transaction.completed", txn)

	respond(w, r, http.StatusCreated, sub)
}

func addPeriod(t time.Time, interval string, frequency int) time.Time {
	switch interval {
	case "day":
		return t.AddDate(0, 0, frequency)
	case "week":
		return t.AddDate(0, 0, 7*frequency)
	case "month":
		return t.AddDate(0, frequency, 0)
	case "year":
		return t.AddDate(frequency, 0, 0)
	}
	return t.AddDate(0, frequency, 0) // default to month
}

func parseAmount(s string) int {
	var n int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

func formatAmount(n int) string {
	return strconv.Itoa(n)
}
