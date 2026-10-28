package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/vlah-software-house/paddle-api-mock/internal/models"
	"github.com/vlah-software-house/paddle-api-mock/internal/seed"
	"github.com/vlah-software-house/paddle-api-mock/internal/store"
	"github.com/vlah-software-house/paddle-api-mock/internal/webhook"
)

type AdminHandler struct {
	Store             *store.Store
	Webhook           *webhook.Notifier
	SeedEnabled       bool
	DefaultWebhookURL string
}

func (h *AdminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/admin/")

	switch {
	case path == "reset" && r.Method == http.MethodPost:
		h.reset(w, r)
	case path == "checkout" && (r.Method == http.MethodPost || r.Method == http.MethodOptions):
		h.checkout(w, r)
	case strings.HasPrefix(path, "advance-time/") && r.Method == http.MethodPost:
		subID := strings.TrimPrefix(path, "advance-time/")
		h.advanceTime(w, r, subID)
	case strings.HasPrefix(path, "approve-adjustment/") && r.Method == http.MethodPost:
		h.approveAdjustment(w, r, strings.TrimPrefix(path, "approve-adjustment/"))
	case strings.HasPrefix(path, "chargeback/") && r.Method == http.MethodPost:
		h.chargeback(w, r, strings.TrimPrefix(path, "chargeback/"))
	case strings.HasPrefix(path, "trigger-webhook/") && r.Method == http.MethodPost:
		eventType := strings.TrimPrefix(path, "trigger-webhook/")
		h.triggerWebhook(w, r, eventType)
	default:
		respondError(w, r, http.StatusNotFound, "request_error", "not_found", "Admin route not found")
	}
}

func (h *AdminHandler) reset(w http.ResponseWriter, r *http.Request) {
	h.Store.Reset()
	if h.SeedEnabled {
		seed.Load(h.Store)
	}
	// Re-register the default webhook URL so webhooks continue to fire after reset.
	if h.DefaultWebhookURL != "" {
		now := time.Now().UTC()
		h.Store.SetNotificationSetting(&models.NotificationSetting{
			ID:          store.NextID("ntfset"),
			Description: "Default webhook (from CLI)",
			Destination: h.DefaultWebhookURL,
			Active:      true,
			APIVersion:  1,
			SubscribedEvents: []string{
				"subscription.created",
				"subscription.updated",
				"subscription.activated",
				"subscription.canceled",
				"subscription.past_due",
				"transaction.completed",
				"transaction.payment_failed",
			},
			Type:      "url",
			CreatedAt: now,
			UpdatedAt: now,
		})
	}
	respond(w, r, http.StatusOK, map[string]string{"status": "reset"})
}

// advanceTime simulates time passing for a subscription:
//   - trialing: the trial ends and the first period is billed.
//   - active: the next period is billed; ?fail=true makes the payment fail (past_due, payment recovery starts).
//   - past_due: payment recovery ends. ?recovery=cancel (default, Paddle without Retain) cancels;
//     ?recovery=pause (Retain set to pause) pauses and cancels the past-due renewal; ?recovery=paid collects it.
//   - paused: the subscription resumes with a new period billed immediately (Paddle's default resume).
func (h *AdminHandler) advanceTime(w http.ResponseWriter, r *http.Request, subID string) {
	sub, ok := h.Store.GetSubscription(subID)
	if !ok {
		respondError(w, r, http.StatusNotFound, "request_error", "not_found", "Subscription not found")
		return
	}

	now := time.Now().UTC()

	switch sub.Status {
	case "trialing":
		sub.Status = "active"
		sub.FirstBilledAt = &now
		startNextPeriod(sub, now)
		for i := range sub.Items {
			sub.Items[i].Status = "active"
			sub.Items[i].TrialDates = nil
		}
		h.Store.SetSubscription(sub)
		txn := newTransaction(h.Store, sub, subscriptionItemsToTransaction(sub), sub.CurrentBillingPeriod, "subscription_recurring", "completed")
		h.Webhook.Fire("subscription.activated", sub)
		h.Webhook.Fire("transaction.completed", txn)

	case "active":
		prevEnd := now
		if sub.CurrentBillingPeriod != nil {
			prevEnd = sub.CurrentBillingPeriod.EndsAt
		}
		if r.URL.Query().Get("fail") == "true" {
			// The renewal for the next period fails: the transaction is past due and payment recovery starts.
			period := &models.BillingPeriodDates{StartsAt: prevEnd, EndsAt: addPeriod(prevEnd, sub.BillingCycle.Interval, sub.BillingCycle.Frequency)}
			sub.Status = "past_due"
			sub.UpdatedAt = now
			h.Store.SetSubscription(sub)
			txn := newTransaction(h.Store, sub, subscriptionItemsToTransaction(sub), period, "subscription_recurring", "past_due")
			h.Webhook.Fire("subscription.past_due", sub)
			h.Webhook.Fire("transaction.payment_failed", txn)
		} else {
			startNextPeriod(sub, prevEnd)
			h.Store.SetSubscription(sub)
			txn := newTransaction(h.Store, sub, subscriptionItemsToTransaction(sub), sub.CurrentBillingPeriod, "subscription_recurring", "completed")
			h.Webhook.Fire("subscription.updated", sub)
			h.Webhook.Fire("transaction.completed", txn)
		}

	case "past_due":
		switch r.URL.Query().Get("recovery") {
		case "", "cancel":
			sub.Status = "canceled"
			sub.CanceledAt = &now
			sub.UpdatedAt = now
			sub.ScheduledChange = nil
			h.Store.SetSubscription(sub)
			h.Webhook.Fire("subscription.canceled", sub)
		case "pause":
			for _, txn := range h.pastDueRenewals(sub) {
				txn.Status = "canceled"
				txn.UpdatedAt = now
				h.Store.SetTransaction(txn)
			}
			sub.Status = "paused"
			sub.PausedAt = &now
			sub.NextBilledAt = nil
			sub.ScheduledChange = nil
			sub.UpdatedAt = now
			h.Store.SetSubscription(sub)
			h.Webhook.Fire("subscription.paused", sub)
			h.Webhook.Fire("subscription.updated", sub)
		case "paid":
			renewals := h.pastDueRenewals(sub)
			if len(renewals) == 0 {
				respondError(w, r, http.StatusConflict, "request_error", "conflict", "No past-due renewal to collect")
				return
			}
			txn := renewals[len(renewals)-1]
			completeTransaction(h.Store, txn)
			if txn.BillingPeriod != nil {
				startNextPeriod(sub, txn.BillingPeriod.StartsAt)
			}
			sub.Status = "active"
			sub.UpdatedAt = now
			h.Store.SetSubscription(sub)
			h.Webhook.Fire("transaction.completed", txn)
			h.Webhook.Fire("subscription.updated", sub)
		default:
			respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "recovery must be cancel, pause or paid")
			return
		}

	case "paused":
		sub.Status = "active"
		sub.PausedAt = nil
		startNextPeriod(sub, now)
		h.Store.SetSubscription(sub)
		txn := newTransaction(h.Store, sub, subscriptionItemsToTransaction(sub), sub.CurrentBillingPeriod, "subscription_recurring", "completed")
		h.Webhook.Fire("subscription.updated", sub)
		h.Webhook.Fire("transaction.completed", txn)

	default:
		respondError(w, r, http.StatusConflict, "request_error", "conflict", "Cannot advance subscription in status: "+sub.Status)
		return
	}

	respond(w, r, http.StatusOK, sub)
}

// pastDueRenewals returns the subscription's past-due renewal transactions, oldest first.
func (h *AdminHandler) pastDueRenewals(sub *models.Subscription) []*models.Transaction {
	renewals := make([]*models.Transaction, 0)
	for _, txn := range h.Store.ListTransactions() {
		if txn.SubscriptionID != nil && *txn.SubscriptionID == sub.ID && txn.Status == "past_due" && txn.Origin == "subscription_recurring" {
			renewals = append(renewals, txn)
		}
	}
	sortByID(renewals, func(t *models.Transaction) string { return t.ID })
	return renewals
}

// approveAdjustment simulates Paddle's review of a refund (sandbox approves automatically every ten minutes).
func (h *AdminHandler) approveAdjustment(w http.ResponseWriter, r *http.Request, id string) {
	adjustment, ok := h.Store.GetAdjustment(id)
	if !ok {
		respondError(w, r, http.StatusNotFound, "request_error", "not_found", "Adjustment not found")
		return
	}
	if adjustment.Status != "pending_approval" {
		respondError(w, r, http.StatusConflict, "request_error", "conflict", "Adjustment is not pending approval")
		return
	}
	adjustment.Status = "approved"
	adjustment.UpdatedAt = time.Now().UTC()
	h.Store.SetAdjustment(adjustment)
	h.Webhook.Fire("adjustment.updated", adjustment)
	respond(w, r, http.StatusOK, adjustment)
}

// chargeback simulates a customer's bank disputing a completed transaction; Paddle creates the adjustment.
func (h *AdminHandler) chargeback(w http.ResponseWriter, r *http.Request, transactionID string) {
	txn, ok := h.Store.GetTransaction(transactionID)
	if !ok {
		respondError(w, r, http.StatusNotFound, "request_error", "not_found", "Transaction not found")
		return
	}
	if txn.Status != "completed" {
		respondError(w, r, http.StatusConflict, "request_error", "conflict", "Only a completed transaction can be charged back")
		return
	}
	adjustment := newAdjustment(txn, "chargeback", "full", "approved", "Chargeback", parseAmount(txn.Details.Totals.GrandTotal), nil)
	h.Store.SetAdjustment(adjustment)
	h.Webhook.Fire("adjustment.created", adjustment)
	respond(w, r, http.StatusCreated, adjustment)
}

func (h *AdminHandler) triggerWebhook(w http.ResponseWriter, r *http.Request, eventType string) {
	// Read optional JSON body as event data
	var data interface{}
	if r.Body != nil {
		_ = decodeJSON(r, &data)
	}
	if data == nil {
		data = map[string]string{"triggered": "manual"}
	}

	h.Webhook.Fire(eventType, data)
	respond(w, r, http.StatusOK, map[string]string{"status": "triggered", "event_type": eventType})
}
