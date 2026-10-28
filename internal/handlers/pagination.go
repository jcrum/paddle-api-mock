package handlers

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/vlah-software-house/paddle-api-mock/internal/middleware"
	"github.com/vlah-software-house/paddle-api-mock/internal/models"
)

// listOptions are an endpoint's pagination limits from Paddle's API reference.
type listOptions struct {
	defaultPerPage int
	maxPerPage     int
}

var (
	// Events, subscriptions, prices, products and customers: default 50, maximum 200.
	standardList = listOptions{defaultPerPage: 50, maxPerPage: 200}
	// Transactions: default 30, maximum 30.
	transactionList = listOptions{defaultPerPage: 30, maxPerPage: 30}
	// Adjustments: default 10, maximum 50.
	adjustmentList = listOptions{defaultPerPage: 10, maxPerPage: 50}
)

// respondPage sorts items by id (order_by=id[ASC|DESC], default id[DESC]), starts after the `after` id, cuts
// one page of per_page items (capped at the maximum, as Paddle does) and writes meta.pagination with a `next`
// URL. The top-level `pagination` is kept for consumers of the original mock.
func respondPage[T any](w http.ResponseWriter, r *http.Request, items []T, idOf func(T) string, opts listOptions) {
	query := r.URL.Query()
	descending := !strings.EqualFold(query.Get("order_by"), "id[ASC]")
	sort.SliceStable(items, func(i, j int) bool {
		if descending {
			return idOf(items[i]) > idOf(items[j])
		}
		return idOf(items[i]) < idOf(items[j])
	})
	total := len(items)

	if after := query.Get("after"); after != "" {
		kept := items[:0:0]
		for _, item := range items {
			id := idOf(item)
			if (descending && id < after) || (!descending && id > after) {
				kept = append(kept, item)
			}
		}
		items = kept
	}

	perPage := opts.defaultPerPage
	if value, err := strconv.Atoi(query.Get("per_page")); err == nil && value > 0 {
		perPage = value
	}
	if perPage > opts.maxPerPage {
		perPage = opts.maxPerPage
	}

	page := items
	hasMore := len(items) > perPage
	if hasMore {
		page = items[:perPage]
	}
	pagination := &models.PaginationInfo{PerPage: perPage, HasMore: hasMore, EstimatedTotal: total}
	if len(page) > 0 {
		pagination.Next = nextURL(r, idOf(page[len(page)-1]))
	}
	if page == nil {
		page = []T{}
	}

	writeJSON(w, http.StatusOK, models.PaddleListResponse{
		Data:       page,
		Meta:       models.Meta{RequestID: middleware.GetRequestID(r.Context()), Pagination: pagination},
		Pagination: pagination,
	})
}

func nextURL(r *http.Request, after string) string {
	query := r.URL.Query()
	query.Set("after", after)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return (&url.URL{Scheme: scheme, Host: r.Host, Path: r.URL.Path, RawQuery: query.Encode()}).String()
}

// filterIn keeps items whose field is in the comma-separated query value; no value keeps everything.
func filterIn[T any](items []T, value string, field func(T) string) []T {
	if value == "" {
		return items
	}
	allowed := map[string]bool{}
	for _, part := range strings.Split(value, ",") {
		allowed[strings.TrimSpace(part)] = true
	}
	kept := make([]T, 0, len(items))
	for _, item := range items {
		if allowed[field(item)] {
			kept = append(kept, item)
		}
	}
	return kept
}

// sortByID orders items by id, oldest first (ids sort in creation order).
func sortByID[T any](items []T, idOf func(T) string) {
	sort.SliceStable(items, func(i, j int) bool { return idOf(items[i]) < idOf(items[j]) })
}
