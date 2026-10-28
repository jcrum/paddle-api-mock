package handlers

import (
	"net/http"
	"time"

	"github.com/vlah-software-house/paddle-api-mock/internal/models"
	"github.com/vlah-software-house/paddle-api-mock/internal/store"
)

// Paddle's product tax categories.
var taxCategories = map[string]bool{
	"digital-goods": true, "ebooks": true, "implementation-services": true, "professional-services": true, "saas": true,
	"software-programming-services": true, "standard": true, "training-services": true, "website-hosting": true,
}

type ProductsHandler struct {
	Store *store.Store
}

func (h *ProductsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := resourcePath(r, "products")

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

	if r.Method == http.MethodGet {
		h.get(w, r, path)
		return
	}
	respondError(w, r, http.StatusMethodNotAllowed, "request_error", "method_not_allowed", "Method not allowed")
}

func (h *ProductsHandler) list(w http.ResponseWriter, r *http.Request) {
	products := filterIn(h.Store.ListProducts(), r.URL.Query().Get("status"), func(p *models.Product) string { return p.Status })
	respondPage(w, r, products, func(p *models.Product) string { return p.ID }, standardList)
}

func (h *ProductsHandler) get(w http.ResponseWriter, r *http.Request, id string) {
	product, ok := h.Store.GetProduct(id)
	if !ok {
		respondError(w, r, http.StatusNotFound, "request_error", "not_found", "Product not found")
		return
	}
	respond(w, r, http.StatusOK, product)
}

// create: POST /products. name and tax_category are required; type defaults to standard.
func (h *ProductsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req models.CreateProductRequest
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, r, http.StatusBadRequest, "request_error", "bad_request", "Invalid JSON body")
		return
	}
	if req.Name == "" || len(req.Name) > 200 {
		respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "name must be 1 to 200 characters")
		return
	}
	if !taxCategories[req.TaxCategory] {
		respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "tax_category is not a Paddle tax category")
		return
	}
	productType := req.Type
	if productType == "" {
		productType = "standard"
	}
	if productType != "standard" && productType != "custom" {
		respondError(w, r, http.StatusBadRequest, "request_error", "invalid_field", "type must be standard or custom")
		return
	}
	now := time.Now().UTC()
	product := &models.Product{
		ID: store.NextID("pro"), Name: req.Name, Description: req.Description, Type: productType, TaxCategory: req.TaxCategory,
		ImageURL: req.ImageURL, Status: "active", CustomData: req.CustomData, CreatedAt: now, UpdatedAt: now,
	}
	h.Store.SetProduct(product)
	respond(w, r, http.StatusCreated, product)
}
