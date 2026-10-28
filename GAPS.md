# Gaps closed on the `paxfish` branch

Each change below closes a difference between this mock and the real Paddle Billing API, confirmed against
Paddle's developer documentation on 2026-10-21. Changes that are not a confirmed difference do not belong here.
Paxfish uses the mock for development (`pfcrm` and the apps' checkout page); see `_doc/plans/paddle-billing/`.

| # | Gap in the mock | Paddle behaviour | Source |
| --- | --- | --- | --- |
| 1 | Paths only under `/v1/` | Paths have no version prefix (`https://api.paddle.com/products`); the version is the `Paddle-Version` header. `/v1/` is kept as an alias for existing consumers. | API reference pages list paths without a prefix (`/prices/{price_id}`, `/products`); base URLs `https://api.paddle.com`, `https://sandbox-api.paddle.com` |
| 2 | Lists ignore `after`, `order_by`, `per_page`; pagination is top level; map order is random | Cursor pagination with `after` (a Paddle id), `order_by=id[ASC\|DESC]` (default `id[DESC]`), `per_page` capped at the endpoint maximum, and `meta.pagination` with `per_page`, `next`, `has_more`, `estimated_total`. The top-level `pagination` is kept for existing consumers. | List events, list subscriptions, list prices, list transactions, list adjustments |
| 3 | `GET /events` ignores `event_type` | `event_type` filters by a comma-separated list | List events |
| 4 | Event `data` is a live pointer, so `GET /events` shows later state | An event's `data` is the entity as it was when the event occurred (the webhook body the mock already sent) | List events / notifications; the mock's own webhook payload |
| 5 | Products and prices are read-only | `POST /products` (201; `name`, `tax_category` required; ids `pro_`), `POST /prices` (201; `product_id`, `description`, `unit_price` required; `tax_mode` default `account_setting`), `PATCH /prices/{id}` (`status` `active`/`archived`); `GET /prices` filters `product_id` and `status` (default `active`) | Create product, create price, update price, list prices |
| 6 | No adjustments | `GET /adjustments` (filters `id`, `transaction_id`, `subscription_id`, `customer_id`, `action`, `status`; `per_page` default 10, max 50); `POST /adjustments` creates `refund` (starts `pending_approval`) or `credit` (`approved`); totals carry `fee` and `earnings`; Paddle creates chargebacks itself | List adjustments, create adjustment |
| 7 | Renewal and charge transactions have empty `custom_data` | A subscription's custom data is copied to every transaction created from it (renewals, changes, one-time charges) | Build → Transactions → Custom data |
| 8 | Transactions lack `billing_period`, `payments`, `address_id`, `details.tax_rates_used`, `fee`/`earnings`, `include=address` | All are transaction fields; `fee` and `earnings` are null until the transaction is `completed`; `include=address` adds the address | Get transaction, list transactions |
| 9 | A failed renewal transaction has status `failed` | There is no `failed` status: a failed automatic payment leaves the transaction `past_due`, with a payment attempt in status `error` | Get transaction (status values, payment status values) |
| 10 | Payment recovery always ends in cancellation | With Retain, recovery ends in a pause or a cancel (configurable); pausing cancels past-due renewal transactions; a successful retry returns the subscription to `active` | Retain → Configure payment recovery; Pause subscriptions; subscription.past_due |
| 11 | Resuming a paused subscription bills nothing | By default a new billing period starts and Paddle creates a transaction to collect for it immediately | Pause subscriptions |
| 12 | No checkout | Paddle Checkout (Paddle.js) creates or reuses the customer, an address, a completed transaction and, for recurring prices, a subscription; checkout `customData` is copied to the transaction and the subscription | Paddle.js checkout; Build → Transactions → Custom data |
| 13 | `transaction.completed` fired from admin advance-time could carry a random transaction (map order) | The event carries the transaction that completed | Defect; events describe the entity that changed |

Not modelled (no Paxfish need yet): webhook-only `notification` endpoints beyond the existing ones, discounts,
businesses, tax calculation (tax stays `0`), product and price events.
