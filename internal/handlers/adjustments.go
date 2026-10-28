package handlers

import (
	"net/http"
	"time"

	"github.com/vlah-software-house/paddle-api-mock/internal/models"
	"github.com/vlah-software-house/paddle-api-mock/internal/store"
	"github.com/vlah-software-house/paddle-api-mock/internal/webhook"
)

// AdjustmentsHandler serves GET /adjustments and POST /adjustments. The API creates refunds (pending_approval
// until Paddle reviews them) and credits (approved at once); Paddle creates chargebacks itself (see the admin
// chargeback and approve-adjustment helpers).
type AdjustmentsHandler struct {
	Store   *store.Store
	Webhook *webhook.Notifier
}

func (h *AdjustmentsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if resourcePath(r, "adjustments") != "" {
		respondError(w, r, http.StatusNotFound, "request_error", "not_found", "Route not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.list(w, r)
	case http.MethodPost:
		h.create(w, r)
	default:
		respondError(w, r, http.StatusMethodNotAllowed, "request_error", "method_not_allowed", "Method not allowed")
	}
}

func (h *AdjustmentsHandler) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	adjustments := h.Store.ListAdjustments()
	adjustments = filterIn(adjustments, query.Get("id"), func(a *models.Adjustment) string { return a.ID })
	adjustments = filterIn(adjustments, query.Get("transaction_id"), func(a *models.Adjustment) string { return a.TransactionID })
	adjustments = filterIn(adjustments, query.Get("subscription_id"), func(a *models.Adjustment) string {
		if a.SubscriptionID == nil {
			return ""
		}
		return *a.SubscriptionID
	})
	adjustments = filterIn(adjustments, query.Get("customer_id"), func(a *models.Adjustment) string { return a.CustomerID })
	adjustments = filterIn(adjustments, query.Get("action"), func(a *models.Adjustment) string { return a.Action })
	adjustments = filterIn(adjustments, query.Get("status"), func(a *models.Adjustment) string { return a.Status })
	respondPage(w, r, adjustments, func(a *models.Adjustment) string { return a.ID }, adjustmentList)
}

// create: action (refund or credit), transaction_id and reason are required. type defaults to partial, which needs
// items; the mock sums the item amounts (it does not model transaction line item ids).
func (h *AdjustmentsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req models.CreateAdjustmentRequest
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, r, http.StatusBadRequest, "request_error", "bad_request", "Invalid JSON body")
		return
	}
	if req.Action != "refund" && req.Action != "credit" {
		respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "action must be refund or credit; Paddle creates other actions")
		return
	}
	if req.Reason == "" {
		respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "reason is required")
		return
	}
	txn, ok := h.Store.GetTransaction(req.TransactionID)
	if !ok {
		respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "transaction_id does not exist")
		return
	}
	if txn.Status != "completed" {
		respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "only a completed transaction can be adjusted")
		return
	}
	adjustmentType := req.Type
	if adjustmentType == "" {
		adjustmentType = "partial"
	}
	grandTotal := parseAmount(txn.Details.Totals.GrandTotal)
	amount := grandTotal
	switch adjustmentType {
	case "full":
	case "partial":
		if len(req.Items) == 0 {
			respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "items are required for a partial adjustment")
			return
		}
		amount = 0
		for _, item := range req.Items {
			amount += parseAmount(item.Amount)
		}
		if amount <= 0 || amount > grandTotal {
			respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "item amounts must add up to between 1 and the transaction total")
			return
		}
	default:
		respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "type must be full or partial")
		return
	}

	status := "pending_approval"
	if req.Action == "credit" {
		status = "approved"
	}
	adjustment := newAdjustment(txn, req.Action, adjustmentType, status, req.Reason, amount, req.Items)
	h.Store.SetAdjustment(adjustment)
	h.Webhook.Fire("adjustment.created", adjustment)
	respond(w, r, http.StatusCreated, adjustment)
}

// newAdjustment splits amount into tax and fee in the transaction's proportions. Earnings are what we give back.
func newAdjustment(txn *models.Transaction, action, adjustmentType, status, reason string, amount int, items []models.AdjustmentItem) *models.Adjustment {
	totals := txn.Details.Totals
	grandTotal := parseAmount(totals.GrandTotal)
	share := func(value string) int {
		if grandTotal == 0 {
			return 0
		}
		return parseAmount(value) * amount / grandTotal
	}
	tax := share(totals.Tax)
	fee := 0
	if totals.Fee != nil {
		fee = share(*totals.Fee)
	}
	if items == nil {
		items = []models.AdjustmentItem{}
	}
	now := time.Now().UTC()
	return &models.Adjustment{
		ID: store.NextID("adj"), Action: action, Type: adjustmentType, TransactionID: txn.ID, SubscriptionID: txn.SubscriptionID,
		CustomerID: txn.CustomerID, Reason: reason, CurrencyCode: txn.CurrencyCode, Status: status, Items: items,
		Totals: models.AdjustmentTotals{
			Subtotal: formatAmount(amount - tax), Tax: formatAmount(tax), Total: formatAmount(amount), Fee: formatAmount(fee),
			Earnings: negative(amount - tax - fee), CurrencyCode: txn.CurrencyCode,
		},
		CreatedAt: now, UpdatedAt: now,
	}
}

func negative(amount int) string {
	if amount == 0 {
		return "0"
	}
	return formatAmount(-amount)
}
