# paddle-mock-api

A lightweight Go mock server that simulates the [Paddle Billing API](https://developer.paddle.com/api-reference/overview) for local development and E2E testing.

No dependencies. No database. Single binary.

## Quick Start

```bash
# Download from releases (macOS ARM64 example)
curl -L -o paddle-mock-api \
  https://github.com/vlah-software-house/paddle-api-mock/releases/latest/download/paddle-mock-api-darwin-arm64
chmod +x paddle-mock-api

# Run with defaults (port 8081, auth enabled, seed data loaded)
./paddle-mock-api

# Or disable auth for quick local testing
./paddle-mock-api -no-auth
```

```bash
# Build from source
go build -o paddle-mock-api ./cmd/server
./paddle-mock-api
```

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | `8081` | Server port |
| `-no-auth` | `false` | Disable API key validation |
| `-no-seed` | `false` | Start with an empty store |
| `-webhook-url` | — | Register a webhook URL on startup |
| `-signing-secret` | `pdl_test_signing_secret` | Webhook signing secret |
| `-api-key` | `test_paddle_api_key` | API key for Bearer auth |

## Authentication

All API endpoints require a `Authorization: Bearer <api_key>` header (default key: `test_paddle_api_key`). Admin, health
and `/paddle.js` are unauthenticated.

## Paths

Real Paddle paths have no version prefix (`https://api.paddle.com/products`; the version is the `Paddle-Version`
header), so the mock serves every endpoint at `/products`, `/prices`, ... and keeps `/v1/...` as an alias. Point a
client at `http://localhost:8081` as its Paddle base URL.

Disable auth entirely with `-no-auth`.

## API Endpoints

### Products & Prices

```
GET   /products               # status filter
POST  /products               # name, tax_category required; ids pro_
GET   /products/{id}
GET   /prices                 # product_id and status filters; status defaults to active
POST  /prices                 # product_id, description, unit_price required; tax_mode defaults to account_setting
GET   /prices/{id}
PATCH /prices/{id}            # description, name, status (active or archived), custom_data
```

### Customers

```
POST  /v1/customers
GET   /v1/customers
GET   /v1/customers/{id}
PATCH /v1/customers/{id}
```

### Subscriptions

```
POST  /v1/subscriptions
GET   /v1/subscriptions
GET   /v1/subscriptions/{id}
PATCH /v1/subscriptions/{id}
POST  /v1/subscriptions/{id}/activate
POST  /v1/subscriptions/{id}/charge
```

### Transactions

```
GET /transactions             # subscription_id, customer_id, status filters; include=address
GET /transactions/{id}        # include=address
```

Transactions carry `billing_period`, `payments`, `address_id`, `details.tax_rates_used` and `details.totals` with
`fee` and `earnings` once completed (mock fee: 5% plus 50; tax is always 0). Transactions created from a
subscription copy its `custom_data`. A failed renewal is `past_due` with a payment attempt in `error`.

### Adjustments

```
GET  /adjustments             # id, transaction_id, subscription_id, customer_id, action, status filters
POST /adjustments             # action refund (pending_approval) or credit (approved); type full or partial (items)
```

### Events & Notification Settings

```
GET  /events                  # event_type filter (comma-separated)
GET  /v1/notification-settings
POST /v1/notification-settings
```

### Admin (test helpers)

```
POST /admin/reset                               # Reset to seed state
POST /admin/checkout                            # Simulated Paddle Checkout (see below)
POST /admin/advance-time/{subscription_id}      # Simulate time passing
POST /admin/approve-adjustment/{adjustment_id}  # Paddle approves a pending refund
POST /admin/chargeback/{transaction_id}         # Paddle records a chargeback
POST /admin/trigger-webhook/{event_type}        # Manually fire a webhook
GET  /ping                                      # Health check
GET  /paddle.js                                 # Development stand-in for Paddle.js
```

`POST /admin/checkout` takes `{price_id | items, email, custom_data, country_code, region, postal_code}` and acts
like Paddle Checkout for recurring prices: it reuses the customer by email, stores the address, creates the
subscription and a completed transaction (both with the checkout's `custom_data`), and fires
`subscription.created` and `transaction.completed`. It answers CORS preflight so the `paddle.js` stand-in can call
it: `Paddle.Initialize({ eventCallback })` and `Paddle.Checkout.open({ items: [{ priceId }], customer: { email },
customData, settings: { successUrl } })` complete the checkout at once and follow `successUrl`.

## Seed Data

On startup (unless `-no-seed`), the store is populated with:

| Type | ID | Details |
|------|----|---------|
| Product | `prod_yieldly_base` | Yieldly Base Plan |
| Price | `pri_yieldly_monthly` | $5.00/month, 3-month trial |
| Customer | `ctm_test_alice` | alice@test.com, has trialing subscription |
| Customer | `ctm_test_bob` | bob@test.com, no subscription |
| Subscription | `sub_test_alice` | trialing, trial ends in 90 days |

## Subscription Lifecycle

The mock tracks subscription state through:

```
trialing → active → past_due → canceled
                             → paused → active
                             → active
```

Use `POST /admin/advance-time/{id}` to move a subscription to its next state:

- **trialing** → activates (trial ends, first billing)
- **active** → next billing cycle (or `?fail=true` for payment failure → past_due)
- **past_due** → payment recovery ends: `?recovery=cancel` (default, Paddle without Retain), `?recovery=pause`
  (Retain set to pause; the past-due renewal is canceled) or `?recovery=paid` (a retry succeeds)
- **paused** → resumed (active) with a new billing period billed immediately

## Webhooks

Register a webhook URL via the API or the `-webhook-url` flag. When subscription or transaction state changes, the mock POSTs to all registered URLs with:

- Paddle's event payload format
- `Paddle-Signature` header (`ts=...;h1=...`) signed with HMAC-SHA256 using the configured signing secret

Event types fired: `subscription.created`, `subscription.updated`, `subscription.activated`, `subscription.canceled`, `subscription.past_due`, `transaction.completed`, `transaction.payment_failed`.

Event `data` is a snapshot of the entity when the event occurred, in `GET /events` as in the webhook body.

## Response Format

All responses use Paddle's standard envelope:

```json
{
  "data": { ... },
  "meta": { "request_id": "req_00000001" }
}
```

List endpoints paginate like Paddle: `after` (an id), `order_by=id[ASC]` or `id[DESC]` (default), and `per_page`
(capped at the endpoint maximum: 200, transactions 30, adjustments 50). Pagination is in `meta.pagination`, with a
`next` URL; the top-level `pagination` of earlier versions is still sent:

```json
{
  "data": [ ... ],
  "meta": {
    "request_id": "req_00000002",
    "pagination": { "per_page": 50, "next": "http://localhost:8081/events?after=evt_00000009", "has_more": false, "estimated_total": 2 }
  },
  "pagination": { "per_page": 50, "next": "...", "has_more": false, "estimated_total": 2 }
}
```

## Differences from Paddle closed on this branch

`GAPS.md` lists each change with the Paddle documentation it follows. Only confirmed differences from the real API
are changed. Run the tests with `go test ./...`.

## Disclaimer

**This is not an official Paddle product.** This project is an independent mock server created for local development and testing purposes. It is not affiliated with, endorsed by, or supported by [Paddle.com](https://www.paddle.com).

The user is fully responsible for how this tool is used. The authors make no warranties regarding accuracy, completeness, or fitness for any particular purpose. Always test against Paddle's real sandbox environment before going to production.

## License

MIT License. See [LICENSE](LICENSE).
