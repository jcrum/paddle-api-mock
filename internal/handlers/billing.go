package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/vlah-software-house/paddle-api-mock/internal/models"
	"github.com/vlah-software-house/paddle-api-mock/internal/store"
)

// Mock processing fee on completed transactions: 5% plus 50 in the lowest currency unit (Paddle's published
// standard rate). Real fees vary by account; the CRM only needs fee and earnings to be present and consistent.
const (
	feePercent = 5
	feeFixed   = 50
)

// subscriptionItemsToTransaction turns a subscription's items into transaction items.
func subscriptionItemsToTransaction(sub *models.Subscription) []models.TransactionItem {
	items := make([]models.TransactionItem, 0, len(sub.Items))
	for _, item := range sub.Items {
		items = append(items, models.TransactionItem{PriceID: item.Price.ID, Quantity: item.Quantity, Price: item.Price, Product: item.Product})
	}
	return items
}

// newTransaction builds and stores a transaction created from a subscription: renewals, activations and charges.
// The subscription's custom data is copied to it (Paddle: "copied to any transactions created from it").
// status is "completed" (payment captured, fee and earnings set) or "past_due" (payment attempt in error).
func newTransaction(s *store.Store, sub *models.Subscription, items []models.TransactionItem, period *models.BillingPeriodDates,
	origin, status string) *models.Transaction {
	now := time.Now().UTC()
	total := 0
	for _, item := range items {
		total += parseAmount(item.Price.UnitPrice.Amount) * item.Quantity
	}
	completed := status == "completed"
	txn := &models.Transaction{
		ID:             store.NextID("txn"),
		Status:         status,
		CustomerID:     sub.CustomerID,
		AddressID:      sub.AddressID,
		SubscriptionID: &sub.ID,
		BillingPeriod:  copyPeriod(period),
		CurrencyCode:   sub.CurrencyCode,
		CollectionMode: sub.CollectionMode,
		Origin:         origin,
		Items:          items,
		Details:        transactionDetails(total, sub.CurrencyCode, completed),
		Payments:       []models.Payment{newPayment(total, completed, now)},
		CreatedAt:      now,
		UpdatedAt:      now,
		CustomData:     copyMap(sub.CustomData),
	}
	if completed {
		txn.BilledAt = &now
	}
	s.SetTransaction(txn)
	return txn
}

// completeTransaction marks a past-due transaction paid: a new captured payment, fee and earnings set.
func completeTransaction(s *store.Store, txn *models.Transaction) {
	now := time.Now().UTC()
	total := parseAmount(txn.Details.Totals.Total)
	txn.Status = "completed"
	txn.BilledAt = &now
	txn.UpdatedAt = now
	txn.Details = transactionDetails(total, txn.CurrencyCode, true)
	txn.Payments = append(txn.Payments, newPayment(total, true, now))
	s.SetTransaction(txn)
}

// transactionDetails: tax is not calculated by the mock (always 0). fee and earnings are null until completed.
func transactionDetails(total int, currency string, completed bool) models.TransactionDetails {
	amount := formatAmount(total)
	totals := models.TransactionTotals{
		Subtotal: amount, Discount: "0", Tax: "0", Total: amount, Credit: "0", GrandTotal: amount, CurrencyCode: currency,
	}
	if completed {
		fee := total*feePercent/100 + feeFixed
		if fee > total {
			fee = total
		}
		feeText, earningsText := formatAmount(fee), formatAmount(total-fee)
		totals.Fee, totals.Earnings = &feeText, &earningsText
		totals.Balance = "0"
	} else {
		totals.Balance = amount
	}
	return models.TransactionDetails{TaxRatesUsed: []models.TaxRateUsed{}, Totals: totals}
}

func newPayment(total int, captured bool, now time.Time) models.Payment {
	payment := models.Payment{PaymentAttemptID: newUUID(), Amount: formatAmount(total), Status: "error", CreatedAt: now}
	if captured {
		payment.Status = "captured"
		payment.CapturedAt = &now
	}
	return payment
}

// errInvalidItems is returned when a subscription cannot be built from the requested items.
var errInvalidItems = errors.New("invalid items")

// buildSubscription creates and stores a subscription for a customer from recurring prices. A price with a trial
// starts it trialing; otherwise it is active with a first billing period from now.
func buildSubscription(s *store.Store, customerID string, addressID *string, items []models.CreateSubItemReq,
	currency, collectionMode string, customData map[string]string) (*models.Subscription, string, error) {
	now := time.Now().UTC()
	if currency == "" {
		currency = "USD"
	}
	if collectionMode == "" {
		collectionMode = "automatic"
	}
	sub := &models.Subscription{
		ID:             store.NextID("sub"),
		Status:         "trialing",
		CustomerID:     customerID,
		AddressID:      addressID,
		CurrencyCode:   currency,
		CreatedAt:      now,
		UpdatedAt:      now,
		StartedAt:      &now,
		CollectionMode: collectionMode,
		CustomData:     copyMap(customData),
		Items:          make([]models.SubscriptionItem, 0, len(items)),
	}

	for _, item := range items {
		price, ok := s.GetPrice(item.PriceID)
		if !ok {
			return nil, "Price not found: " + item.PriceID, errInvalidItems
		}
		if price.BillingCycle == nil {
			return nil, "Price is not recurring: " + item.PriceID, errInvalidItems
		}
		qty := item.Quantity
		if qty == 0 {
			qty = 1
		}
		subItem := models.SubscriptionItem{Status: "trialing", Quantity: qty, Recurring: true, CreatedAt: now, UpdatedAt: now, Price: *price}
		sub.BillingCycle = *price.BillingCycle
		if price.TrialPeriod != nil {
			trialEnd := addPeriod(now, price.TrialPeriod.Interval, price.TrialPeriod.Frequency)
			subItem.TrialDates = &models.BillingPeriodDates{StartsAt: now, EndsAt: trialEnd}
			subItem.NextBilledAt = &trialEnd
			sub.NextBilledAt = &trialEnd
			sub.CurrentBillingPeriod = &models.BillingPeriodDates{StartsAt: now, EndsAt: trialEnd}
		} else {
			sub.Status = "active"
			subItem.Status = "active"
			nextBill := addPeriod(now, price.BillingCycle.Interval, price.BillingCycle.Frequency)
			subItem.NextBilledAt = &nextBill
			sub.NextBilledAt = &nextBill
			sub.FirstBilledAt = &now
			sub.CurrentBillingPeriod = &models.BillingPeriodDates{StartsAt: now, EndsAt: nextBill}
		}
		if prod, ok := s.GetProduct(price.ProductID); ok {
			subItem.Product = prod
		}
		sub.Items = append(sub.Items, subItem)
	}
	s.SetSubscription(sub)
	return sub, "", nil
}

// startNextPeriod moves a subscription's billing period forward from start.
func startNextPeriod(sub *models.Subscription, start time.Time) {
	now := time.Now().UTC()
	nextBill := addPeriod(start, sub.BillingCycle.Interval, sub.BillingCycle.Frequency)
	sub.NextBilledAt = &nextBill
	sub.CurrentBillingPeriod = &models.BillingPeriodDates{StartsAt: start, EndsAt: nextBill}
	sub.UpdatedAt = now
	for i := range sub.Items {
		sub.Items[i].PreviouslyBilledAt = &start
		sub.Items[i].NextBilledAt = &nextBill
		sub.Items[i].UpdatedAt = now
	}
}

func copyMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func copyPeriod(period *models.BillingPeriodDates) *models.BillingPeriodDates {
	if period == nil {
		return nil
	}
	copied := *period
	return &copied
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
