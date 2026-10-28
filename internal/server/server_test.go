package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const apiKey = "test_key"

type client struct {
	t      *testing.T
	server *httptest.Server
}

func newClient(t *testing.T) *client {
	t.Helper()
	handler, _ := New(Config{APIKey: apiKey, NoSeed: true, SigningSecret: "secret"})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &client{t: t, server: server}
}

func (c *client) request(method, path string, body interface{}, authorized bool) (int, map[string]interface{}) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	req, _ := http.NewRequest(method, c.server.URL+path, reader)
	req.Header.Set("Content-Type", "application/json")
	if authorized {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var decoded map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp.StatusCode, decoded
}

func (c *client) data(method, path string, body interface{}) map[string]interface{} {
	c.t.Helper()
	status, decoded := c.request(method, path, body, true)
	if status >= 300 {
		c.t.Fatalf("%s %s: status %d %v", method, path, status, decoded)
	}
	return decoded["data"].(map[string]interface{})
}

func (c *client) list(path string) ([]interface{}, map[string]interface{}) {
	c.t.Helper()
	status, decoded := c.request(http.MethodGet, path, nil, true)
	if status != http.StatusOK {
		c.t.Fatalf("GET %s: status %d %v", path, status, decoded)
	}
	return decoded["data"].([]interface{}), decoded["meta"].(map[string]interface{})["pagination"].(map[string]interface{})
}

func (c *client) monthlyPrice(amount string) string {
	product := c.data(http.MethodPost, "/products", map[string]interface{}{"name": "Boards pro", "tax_category": "saas"})
	price := c.data(http.MethodPost, "/prices", map[string]interface{}{
		"product_id": product["id"], "description": "Monthly", "unit_price": map[string]string{"amount": amount, "currency_code": "USD"},
		"billing_cycle": map[string]interface{}{"interval": "month", "frequency": 1},
	})
	return price["id"].(string)
}

func (c *client) checkout(priceID string, customData map[string]string) map[string]interface{} {
	return c.data(http.MethodPost, "/admin/checkout", map[string]interface{}{
		"price_id": priceID, "email": "owner@example.com", "custom_data": customData, "country_code": "US", "region": "WA",
	})
}

func (c *client) eventTypes() []string {
	events, _ := c.list("/events?order_by=id[ASC]&per_page=200")
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.(map[string]interface{})["event_type"].(string))
	}
	return types
}

func dig(value interface{}, keys ...string) interface{} {
	for _, key := range keys {
		m, ok := value.(map[string]interface{})
		if !ok {
			return nil
		}
		value = m[key]
	}
	return value
}

// Gap 1: paths without a version prefix; /v1 stays an alias; API paths need the key, test helpers do not.
func TestPathsWithoutVersionPrefix(t *testing.T) {
	c := newClient(t)
	if status, _ := c.request(http.MethodGet, "/products", nil, true); status != http.StatusOK {
		t.Fatalf("GET /products = %d", status)
	}
	if status, _ := c.request(http.MethodGet, "/v1/products", nil, true); status != http.StatusOK {
		t.Fatalf("GET /v1/products = %d", status)
	}
	if status, _ := c.request(http.MethodGet, "/products", nil, false); status != http.StatusUnauthorized {
		t.Fatalf("unauthorized GET /products = %d", status)
	}
	if status, _ := c.request(http.MethodGet, "/ping", nil, false); status != http.StatusOK {
		t.Fatalf("GET /ping = %d", status)
	}
}

// Gap 2: id[DESC] by default, after cursor, per_page, meta.pagination with next.
func TestCursorPagination(t *testing.T) {
	c := newClient(t)
	ids := []string{}
	for i := 0; i < 3; i++ {
		product := c.data(http.MethodPost, "/products", map[string]interface{}{"name": "P", "tax_category": "standard"})
		ids = append(ids, product["id"].(string))
	}

	page, pagination := c.list("/products?per_page=2")
	if len(page) != 2 || dig(page[0], "id") != ids[2] || dig(page[1], "id") != ids[1] {
		t.Fatalf("default order is not id[DESC]: %v", page)
	}
	if pagination["has_more"] != true || pagination["estimated_total"].(float64) != 3 || pagination["per_page"].(float64) != 2 {
		t.Fatalf("pagination = %v", pagination)
	}
	next, _ := url.Parse(pagination["next"].(string))
	if next.Query().Get("after") != ids[1] {
		t.Fatalf("next = %v", pagination["next"])
	}
	rest, pagination := c.list("/products?per_page=2&after=" + ids[1])
	if len(rest) != 1 || dig(rest[0], "id") != ids[0] || pagination["has_more"] != false {
		t.Fatalf("after page = %v %v", rest, pagination)
	}
	ascending, _ := c.list("/products?order_by=id[ASC]")
	if dig(ascending[0], "id") != ids[0] {
		t.Fatalf("id[ASC] = %v", ascending)
	}
}

// Gap 2: per_page is capped at the endpoint maximum (transactions: 30).
func TestTransactionsPerPageCap(t *testing.T) {
	c := newClient(t)
	_, pagination := c.list("/transactions?per_page=500")
	if pagination["per_page"].(float64) != 30 {
		t.Fatalf("per_page = %v", pagination["per_page"])
	}
}

// Gap 5: create products and prices, validation, archive; prices list active by default.
func TestProductsAndPrices(t *testing.T) {
	c := newClient(t)
	if status, _ := c.request(http.MethodPost, "/products", map[string]interface{}{"name": "P", "tax_category": "food"}, true); status != http.StatusBadRequest {
		t.Fatalf("bad tax_category = %d", status)
	}
	product := c.data(http.MethodPost, "/products", map[string]interface{}{"name": "P", "tax_category": "saas"})
	if !strings.HasPrefix(product["id"].(string), "pro_") || product["type"] != "standard" {
		t.Fatalf("product = %v", product)
	}
	status, _ := c.request(http.MethodPost, "/prices", map[string]interface{}{
		"product_id": product["id"], "description": "Monthly", "unit_price": map[string]string{"amount": "15.00", "currency_code": "USD"},
	}, true)
	if status != http.StatusBadRequest {
		t.Fatalf("decimal amount = %d", status)
	}
	priceID := c.monthlyPrice("4900")
	price := c.data(http.MethodGet, "/prices/"+priceID, nil)
	if price["tax_mode"] != "account_setting" || dig(price, "unit_price", "amount") != "4900" {
		t.Fatalf("price = %v", price)
	}
	archived := c.data(http.MethodPatch, "/prices/"+priceID, map[string]string{"status": "archived"})
	if archived["status"] != "archived" {
		t.Fatalf("archive = %v", archived)
	}
	active, _ := c.list("/prices")
	all, _ := c.list("/prices?status=archived")
	if len(active) != 0 || len(all) != 1 {
		t.Fatalf("active %d, archived %d", len(active), len(all))
	}
}

// Gaps 7, 8 and 12: checkout copies custom data to the subscription and its transaction, stores the address
// (include=address), reuses the customer by email, and the transaction carries period, payments, fee and earnings.
func TestCheckout(t *testing.T) {
	c := newClient(t)
	priceID := c.monthlyPrice("1000")
	result := c.checkout(priceID, map[string]string{"paxfish_app_key": "boards"})
	sub := result["subscription"].(map[string]interface{})
	txn := result["transaction"].(map[string]interface{})

	if sub["status"] != "active" || dig(sub, "custom_data", "paxfish_app_key") != "boards" {
		t.Fatalf("subscription = %v", sub)
	}
	if txn["status"] != "completed" || txn["origin"] != "web" || dig(txn, "custom_data", "paxfish_app_key") != "boards" || txn["subscription_id"] != sub["id"] {
		t.Fatalf("transaction = %v", txn)
	}
	if dig(txn, "billing_period", "starts_at") == nil || dig(txn, "details", "totals", "fee") != "100" || dig(txn, "details", "totals", "earnings") != "900" {
		t.Fatalf("transaction details = %v", txn)
	}
	payment := txn["payments"].([]interface{})[0].(map[string]interface{})
	if payment["status"] != "captured" || payment["captured_at"] == nil {
		t.Fatalf("payment = %v", payment)
	}
	if got := c.data(http.MethodGet, "/transactions/"+txn["id"].(string), nil); got["address"] != nil {
		t.Fatalf("address without include = %v", got["address"])
	}
	withAddress := c.data(http.MethodGet, "/transactions/"+txn["id"].(string)+"?include=address", nil)
	if dig(withAddress, "address", "country_code") != "US" || dig(withAddress, "address", "region") != "WA" {
		t.Fatalf("include=address = %v", withAddress["address"])
	}
	again := c.checkout(priceID, nil)
	if dig(again, "customer", "id") != dig(result, "customer", "id") {
		t.Fatalf("customer not reused by email")
	}
	types := c.eventTypes()
	if types[0] != "subscription.created" || types[1] != "transaction.completed" {
		t.Fatalf("events = %v", types)
	}
}

// Gaps 3 and 4: event_type filter; an event keeps the entity as it was when it occurred.
func TestEventsFilterAndSnapshot(t *testing.T) {
	c := newClient(t)
	sub := c.checkout(c.monthlyPrice("1000"), nil)["subscription"].(map[string]interface{})
	c.data(http.MethodPost, "/admin/advance-time/"+sub["id"].(string)+"?fail=true", nil)

	created, _ := c.list("/events?event_type=subscription.created")
	if len(created) != 1 || dig(created[0], "data", "status") != "active" {
		t.Fatalf("subscription.created = %v", created)
	}
	both, _ := c.list("/events?event_type=subscription.created,subscription.past_due")
	if len(both) != 2 {
		t.Fatalf("two types = %d", len(both))
	}
}

// Gaps 7, 9, 10 and 11, and 13: renewals copy custom data; a failed renewal is past_due with an errored payment;
// recovery ends in a pause (cancelling the renewal) or a payment; resuming bills a new period.
func TestRenewalFailureRecoveryAndResume(t *testing.T) {
	c := newClient(t)
	sub := c.checkout(c.monthlyPrice("1000"), map[string]string{"k": "v"})["subscription"].(map[string]interface{})
	id := sub["id"].(string)

	c.data(http.MethodPost, "/admin/advance-time/"+id, nil)
	renewals, _ := c.list("/transactions?subscription_id=" + id + "&status=completed")
	if len(renewals) != 2 || dig(renewals[0], "custom_data", "k") != "v" {
		t.Fatalf("renewal = %v", renewals)
	}

	failed := c.data(http.MethodPost, "/admin/advance-time/"+id+"?fail=true", nil)
	if failed["status"] != "past_due" {
		t.Fatalf("subscription = %v", failed["status"])
	}
	pastDue, _ := c.list("/transactions?subscription_id=" + id + "&status=past_due")
	if len(pastDue) != 1 || dig(pastDue[0], "details", "totals", "fee") != nil {
		t.Fatalf("past due = %v", pastDue)
	}
	if dig(pastDue[0].(map[string]interface{})["payments"].([]interface{})[0], "status") != "error" {
		t.Fatalf("payment status = %v", pastDue[0])
	}
	if status, _ := c.request(http.MethodPost, "/admin/advance-time/"+id+"?recovery=bogus", nil, true); status != http.StatusBadRequest {
		t.Fatalf("bogus recovery = %d", status)
	}

	paused := c.data(http.MethodPost, "/admin/advance-time/"+id+"?recovery=pause", nil)
	if paused["status"] != "paused" {
		t.Fatalf("paused = %v", paused["status"])
	}
	canceled := c.data(http.MethodGet, "/transactions/"+dig(pastDue[0], "id").(string), nil)
	if canceled["status"] != "canceled" {
		t.Fatalf("renewal after pause = %v", canceled["status"])
	}

	resumed := c.data(http.MethodPost, "/admin/advance-time/"+id, nil)
	if resumed["status"] != "active" {
		t.Fatalf("resumed = %v", resumed["status"])
	}
	completed, _ := c.list("/transactions?subscription_id=" + id + "&status=completed")
	if len(completed) != 3 {
		t.Fatalf("completed after resume = %d", len(completed))
	}
	types := c.eventTypes()
	if types[len(types)-2] != "subscription.updated" || types[len(types)-1] != "transaction.completed" {
		t.Fatalf("resume events = %v", types)
	}
	last, _ := c.list("/events?event_type=transaction.completed")
	if dig(last[0], "data", "id") != dig(completed[0], "id") {
		t.Fatalf("transaction.completed carries %v, newest transaction is %v", dig(last[0], "data", "id"), dig(completed[0], "id"))
	}
}

// Gap 10: a successful retry during recovery completes the renewal and reactivates the subscription.
func TestRecoveryPaid(t *testing.T) {
	c := newClient(t)
	id := c.checkout(c.monthlyPrice("1000"), nil)["subscription"].(map[string]interface{})["id"].(string)
	c.data(http.MethodPost, "/admin/advance-time/"+id+"?fail=true", nil)

	paid := c.data(http.MethodPost, "/admin/advance-time/"+id+"?recovery=paid", nil)
	if paid["status"] != "active" {
		t.Fatalf("paid = %v", paid["status"])
	}
	pastDue, _ := c.list("/transactions?subscription_id=" + id + "&status=past_due")
	if len(pastDue) != 0 {
		t.Fatalf("still past due = %v", pastDue)
	}
}

// Gap 6: refunds start pending approval, credits are approved, partial needs items; Paddle creates chargebacks.
func TestAdjustments(t *testing.T) {
	c := newClient(t)
	txn := c.checkout(c.monthlyPrice("1000"), nil)["transaction"].(map[string]interface{})
	txnID := txn["id"].(string)

	if status, _ := c.request(http.MethodPost, "/adjustments", map[string]interface{}{"action": "refund", "transaction_id": txnID, "reason": "x"}, true); status != http.StatusBadRequest {
		t.Fatalf("partial without items = %d", status)
	}
	if status, _ := c.request(http.MethodPost, "/adjustments", map[string]interface{}{"action": "chargeback", "transaction_id": txnID, "reason": "x", "type": "full"}, true); status != http.StatusBadRequest {
		t.Fatalf("chargeback via API = %d", status)
	}
	refund := c.data(http.MethodPost, "/adjustments", map[string]interface{}{
		"action": "refund", "transaction_id": txnID, "reason": "Requested", "type": "partial", "items": []map[string]string{{"item_id": "x", "type": "partial", "amount": "400"}},
	})
	if refund["status"] != "pending_approval" || dig(refund, "totals", "total") != "400" || dig(refund, "totals", "fee") != "40" {
		t.Fatalf("refund = %v", refund)
	}
	approved := c.data(http.MethodPost, "/admin/approve-adjustment/"+refund["id"].(string), nil)
	if approved["status"] != "approved" {
		t.Fatalf("approved = %v", approved)
	}
	credit := c.data(http.MethodPost, "/adjustments", map[string]interface{}{"action": "credit", "transaction_id": txnID, "reason": "Goodwill", "type": "full"})
	if credit["status"] != "approved" {
		t.Fatalf("credit = %v", credit)
	}
	chargeback := c.data(http.MethodPost, "/admin/chargeback/"+txnID, nil)
	if chargeback["action"] != "chargeback" || chargeback["status"] != "approved" {
		t.Fatalf("chargeback = %v", chargeback)
	}

	refunds, pagination := c.list("/adjustments?action=refund&transaction_id=" + txnID)
	if len(refunds) != 1 || pagination["per_page"].(float64) != 10 {
		t.Fatalf("refunds = %v %v", refunds, pagination)
	}
	byID, _ := c.list("/adjustments?id=" + chargeback["id"].(string))
	if len(byID) != 1 {
		t.Fatalf("by id = %v", byID)
	}
	types := c.eventTypes()
	want := []string{"adjustment.created", "adjustment.updated", "adjustment.created", "adjustment.created"}
	if strings.Join(types[len(types)-4:], ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v", types)
	}
}

// Gap 12: the paddle.js stand-in is public and points at this server; checkout answers CORS preflight.
func TestPaddleJSAndCORS(t *testing.T) {
	c := newClient(t)
	resp, err := http.Get(c.server.URL + "/paddle.js")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), c.server.URL) || !strings.Contains(resp.Header.Get("Content-Type"), "javascript") {
		t.Fatalf("paddle.js = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	req, _ := http.NewRequest(http.MethodOptions, c.server.URL+"/admin/checkout", nil)
	preflight, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	preflight.Body.Close()
	if preflight.StatusCode != http.StatusNoContent || preflight.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("preflight = %d", preflight.StatusCode)
	}
}
