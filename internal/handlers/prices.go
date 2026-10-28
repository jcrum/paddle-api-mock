package handlers

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/vlah-software-house/paddle-api-mock/internal/models"
	"github.com/vlah-software-house/paddle-api-mock/internal/store"
)

var (
	amountFormat   = regexp.MustCompile(`^\d+$`)
	currencyFormat = regexp.MustCompile(`^[A-Z]{3}$`)
	taxModes       = map[string]bool{"account_setting": true, "external": true, "internal": true, "location": true}
	intervals      = map[string]bool{"day": true, "week": true, "month": true, "year": true}
)

type PricesHandler struct {
	Store *store.Store
}

func (h *PricesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := resourcePath(r, "prices")

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

	switch r.Method {
	case http.MethodGet:
		h.get(w, r, path)
	case http.MethodPatch:
		h.update(w, r, path)
	default:
		respondError(w, r, http.StatusMethodNotAllowed, "request_error", "method_not_allowed", "Method not allowed")
	}
}

// list: product_id and status filters; status defaults to active, as in Paddle.
func (h *PricesHandler) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	status := query.Get("status")
	if status == "" {
		status = "active"
	}
	prices := h.Store.ListPrices()
	prices = filterIn(prices, query.Get("product_id"), func(p *models.Price) string { return p.ProductID })
	prices = filterIn(prices, status, func(p *models.Price) string { return p.Status })

	if strings.Contains(query.Get("include"), "product") {
		for _, p := range prices {
			if prod, ok := h.Store.GetProduct(p.ProductID); ok {
				p.Product = prod
			}
		}
	}

	respondPage(w, r, prices, func(p *models.Price) string { return p.ID }, standardList)
}

func (h *PricesHandler) get(w http.ResponseWriter, r *http.Request, id string) {
	price, ok := h.Store.GetPrice(id)
	if !ok {
		respondError(w, r, http.StatusNotFound, "request_error", "not_found", "Price not found")
		return
	}

	if strings.Contains(r.URL.Query().Get("include"), "product") {
		if prod, ok := h.Store.GetProduct(price.ProductID); ok {
			price.Product = prod
		}
	}

	respond(w, r, http.StatusOK, price)
}

// create: POST /prices. product_id, description (2 to 500 characters) and unit_price are required;
// billing_cycle is optional (one-time when absent); tax_mode defaults to account_setting.
func (h *PricesHandler) create(w http.ResponseWriter, r *http.Request) {
	var req models.CreatePriceRequest
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, r, http.StatusBadRequest, "request_error", "bad_request", "Invalid JSON body")
		return
	}
	if _, ok := h.Store.GetProduct(req.ProductID); !ok {
		respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "product_id does not exist")
		return
	}
	if len(req.Description) < 2 || len(req.Description) > 500 {
		respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "description must be 2 to 500 characters")
		return
	}
	if !amountFormat.MatchString(req.UnitPrice.Amount) || !currencyFormat.MatchString(req.UnitPrice.CurrencyCode) {
		respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "unit_price needs a string amount in the lowest unit and a currency_code")
		return
	}
	if req.BillingCycle != nil && (!intervals[req.BillingCycle.Interval] || req.BillingCycle.Frequency < 1) {
		respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "billing_cycle needs an interval of day, week, month or year and a frequency")
		return
	}
	taxMode := req.TaxMode
	if taxMode == "" {
		taxMode = "account_setting"
	}
	if !taxModes[taxMode] {
		respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "tax_mode is not a Paddle tax mode")
		return
	}
	priceType := req.Type
	if priceType == "" {
		priceType = "standard"
	}
	quantity := models.Quantity{Minimum: 1, Maximum: 100}
	if req.Quantity != nil {
		quantity = *req.Quantity
	}
	now := time.Now().UTC()
	price := &models.Price{
		ID: store.NextID("pri"), ProductID: req.ProductID, Name: req.Name, Description: req.Description, Type: priceType,
		BillingCycle: req.BillingCycle, TrialPeriod: req.TrialPeriod, TaxMode: taxMode, UnitPrice: req.UnitPrice,
		UnitPriceOverrides: []interface{}{}, Quantity: quantity, Status: "active", CustomData: req.CustomData, CreatedAt: now, UpdatedAt: now,
	}
	h.Store.SetPrice(price)
	respond(w, r, http.StatusCreated, price)
}

// update: PATCH /prices/{id}. Supports description, name, status (active or archived) and custom_data.
func (h *PricesHandler) update(w http.ResponseWriter, r *http.Request, id string) {
	price, ok := h.Store.GetPrice(id)
	if !ok {
		respondError(w, r, http.StatusNotFound, "request_error", "not_found", "Price not found")
		return
	}
	var req models.UpdatePriceRequest
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, r, http.StatusBadRequest, "request_error", "bad_request", "Invalid JSON body")
		return
	}
	if req.Status != nil && *req.Status != "active" && *req.Status != "archived" {
		respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "status must be active or archived")
		return
	}
	if req.Description != nil {
		price.Description = *req.Description
	}
	if req.Name != nil {
		price.Name = req.Name
	}
	if req.Status != nil {
		price.Status = *req.Status
	}
	if req.CustomData != nil {
		price.CustomData = req.CustomData
	}
	price.UpdatedAt = time.Now().UTC()
	h.Store.SetPrice(price)
	respond(w, r, http.StatusOK, price)
}
