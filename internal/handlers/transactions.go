package handlers

import (
	"net/http"
	"strings"

	"github.com/vlah-software-house/paddle-api-mock/internal/models"
	"github.com/vlah-software-house/paddle-api-mock/internal/store"
)

type TransactionsHandler struct {
	Store *store.Store
}

func (h *TransactionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := resourcePath(r, "transactions")

	if r.Method != http.MethodGet {
		respondError(w, r, http.StatusMethodNotAllowed, "request_error", "method_not_allowed", "Method not allowed")
		return
	}

	if path == "" {
		h.list(w, r)
		return
	}

	h.get(w, r, path)
}

func (h *TransactionsHandler) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	txns := h.Store.ListTransactions()
	txns = filterIn(txns, query.Get("subscription_id"), func(t *models.Transaction) string {
		if t.SubscriptionID == nil {
			return "null"
		}
		return *t.SubscriptionID
	})
	txns = filterIn(txns, query.Get("customer_id"), func(t *models.Transaction) string { return t.CustomerID })
	txns = filterIn(txns, query.Get("status"), func(t *models.Transaction) string { return t.Status })
	presented := make([]*models.Transaction, 0, len(txns))
	for _, t := range txns {
		presented = append(presented, h.present(t, query.Get("include")))
	}
	respondPage(w, r, presented, func(t *models.Transaction) string { return t.ID }, transactionList)
}

func (h *TransactionsHandler) get(w http.ResponseWriter, r *http.Request, id string) {
	txn, ok := h.Store.GetTransaction(id)
	if !ok {
		respondError(w, r, http.StatusNotFound, "request_error", "not_found", "Transaction not found")
		return
	}
	respond(w, r, http.StatusOK, h.present(txn, r.URL.Query().Get("include")))
}

// present returns a copy with the related address when include=address asks for it.
func (h *TransactionsHandler) present(txn *models.Transaction, include string) *models.Transaction {
	copied := *txn
	copied.Address = nil
	if strings.Contains(include, "address") && txn.AddressID != nil {
		if address, ok := h.Store.GetAddress(*txn.AddressID); ok {
			copied.Address = address
		}
	}
	return &copied
}
