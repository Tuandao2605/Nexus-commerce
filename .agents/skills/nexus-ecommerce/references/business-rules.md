# Commerce Business Rules

Use this reference for Catalog, Inventory, Cart, Voucher, Checkout, Order, Payment, and the tests that prove their invariants.

## Product, Variant, and SKU

```text
Product: listing/concept
  -> ProductVariant: a grouped variation such as Black
    -> SKU: the final sellable option such as Black / Size M
```

- Even a product with no visible options has one Default Variant and one Default SKU. Downstream modules always use `sku_id`.
- Product owns listing metadata and moderation lifecycle.
- Variant owns grouped attributes/image.
- SKU owns seller SKU code, sellability, current price, currency, SKU attributes, and optional shipping overrides.
- SKU code is unique per Shop. Price uses integer minor units and matches the Shop currency in V1.
- Catalog contains no stock fields.
- Archive Product/Variant/SKU in normal workflows; preserve references and historical Orders.
- Product moderation is application-enforced: Seller submits `draft -> pending_review`; Admin accepts/rejects; Seller cannot activate a draft directly.

## Inventory

Inventory distinguishes at least available and reserved quantities and owns all reservation/commit/release operations.

```text
AVAILABLE -> RESERVED -> COMMITTED/SOLD
                    \-> RELEASED
```

- Reservation expiry is mandatory; checkout architecture currently specifies 15 minutes.
- Reserving multiple cart lines is all-or-nothing for the checkout attempt unless a later explicit design changes that contract.
- Reserve, commit, release, and expiration are idempotent.
- Seller requests stock changes through Inventory; Seller never writes Inventory tables.
- PostgreSQL locking/conditional atomic SQL enforces correctness across API instances.
- Stock never becomes negative and successful reservations never exceed available stock.
- Every stock mutation should be traceable through a stock movement/ledger when that module is implemented.

## Cart

- Cart items reference SKUs and quantities.
- A cached/displayed Cart price is never a purchase source of truth.
- Checkout reloads current SKU price, active status, availability, and relevant Catalog data.
- Checkout clears only selected purchased items, after Order persistence succeeds.
- A price/status change is surfaced through a stable checkout conflict; the client does not force its stale value.

## Voucher

- Support percentage/fixed discount, minimum order, maximum discount, expiry, total limit, per-user limit, seller-specific, and platform scopes.
- Voucher validates against server-calculated eligible totals and ownership/scope.
- Usage limit enforcement is transactional and safe under concurrency.
- Reservation/consumption/release semantics must be explicit when checkout or payment fails; do not increment a counter in an unrecoverable intermediate step.

## Checkout

The Order module's application-layer CheckoutService orchestrates:

```text
selected Cart items
  -> reload/validate Catalog SKU and current price
  -> validate/calculate Voucher
  -> reserve Inventory (15-minute TTL)
  -> persist Order and snapshots
  -> clear selected Cart items
  -> initialize Payment
```

Critical rules:

- `Idempotency-Key` prevents duplicate Order and Payment creation.
- Server computes prices, discounts, totals, actor, shop, and address ownership.
- Reservation failure creates no Order.
- Order persistence failure triggers immediate release; TTL expiry recovers from a process crash before release.
- Payment failure/timeout cannot reserve inventory forever.
- Each step has a defined timeout and typed failure; retries are bounded and only for safe/transient operations.

## Order

Order owns its entity, items, status history, snapshots, and every status transition. Payment cannot update Order tables.

The requirements and architecture notes currently use different pre-payment names:

- requirements: `CREATED -> AWAITING_PAYMENT -> PAID`;
- architecture V2 checkout: create as `PENDING_PAYMENT`.

Do not implement both aliases accidentally. Before the first Order migration/domain constants, finalize one state vocabulary in the API/domain documentation or an ADR. Regardless of names:

- invalid transitions are rejected;
- repeated transition/event handling is idempotent;
- status history records accepted transitions;
- only Order code mutates status;
- cancellation/refund behavior preserves audit history.

Order snapshots must reconstruct history without live lookups. Snapshot product, variant, SKU, attributes, unit/original price, quantity, relevant seller/shop identity, and shipping address. Retain original IDs for audit/traceability.

## Payment

- Payment owns provider transactions, attempts/results, and refunds.
- Mock provider states include success, failed, pending, and timeout.
- Webhook processing verifies signature and replay protection, deduplicates provider events, and is idempotent.
- One provider success produces exactly one payment business effect: no double charge, inventory commit, Order transition, notification, refund, or accounting entry.
- Payment announces `PaymentSucceeded`/`PaymentFailed` through a durable boundary/event. Order consumes and performs its own valid transition.
- Delayed webhook and reconciliation use the same state machine and deduplication rules.

## Event Effects

Publish state-change events through the owner module's transactional outbox. Consumers accept at-least-once delivery and record processed identities. Duplicate and out-of-order events must not repeat business effects or regress state.

## Acceptance Tests

Choose tests based on the change; critical flows require real PostgreSQL integration rather than only mocked repositories.

Inventory:

- stock 50 with 1,000 concurrent attempts yields at most 50 successes;
- available/reserved stock never becomes negative;
- duplicate reserve/commit/release does not repeat the effect;
- expired reservations are released after worker retry/restart.

Voucher:

- one remaining use with 100 concurrent attempts yields one success;
- duplicate/retried checkout does not consume twice.

Checkout/idempotency:

- same actor/key/body creates one Order and one Payment and returns the same result;
- same key with a different body is rejected;
- Order insert failure releases reservation or expiry later recovers it;
- process/payment/broker failure causes no lost Order and no permanent reservation.

Payment/events:

- duplicate success webhook/event transitions Order once and commits stock once;
- forged/replayed webhook has no effect;
- consumer crash before/after acknowledgement is safe;
- outbox retries after broker outage without losing or duplicating the business effect.

Authorization/history:

- cross-shop seller mutation is forbidden;
- changing or archiving Catalog/User data does not change historical Order rendering;
- every accepted Order transition is valid and auditable.

## Repository Sources

- `notes/architect.md`: exact ownership, checkout order, 15-minute reservation, compensation, snapshots, and Payment-to-Order boundary.
- `docs/data-003b-inventory.md`: Product/Variant/SKU, seller/shop, lifecycle, price, tenant, and archive decisions.
- `nexus_commerce_golang_requirements.txt`: Inventory, Cart, Voucher, Order, Payment, idempotency, event, and test requirements.
