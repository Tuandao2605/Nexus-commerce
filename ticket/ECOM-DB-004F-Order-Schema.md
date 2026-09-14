# ECOM-DB-004F — Order Schema

Status: DONE

## Source Design

- DATA-003E — Order Domain Model
- DATA-003G — Full ERD corrections

## Goal

Implement PostgreSQL schema for:

- orders
- order_items
- order_status_histories

## Important Invariants

- Architecture uses Option C: Parent Order + child Seller Orders in a self-referencing `orders` table
- Parent Order represents the customer checkout aggregate:
  - `order_type = 'parent'`
  - `parent_order_id IS NULL`, `shop_id IS NULL`, `shop_name_snapshot IS NULL`
  - `checkout_reference_id` is required
  - Intentionally owns no direct `order_items`
- Seller Order represents the shop-scoped operational order:
  - `order_type = 'seller'`
  - `parent_order_id` is required and references a Parent Order
  - `shop_id` and `shop_name_snapshot` are required
  - `checkout_reference_id IS NULL`
- Supporting unique keys on `orders`:
  - `UNIQUE(id, shop_id)` for OrderItem composite FK
  - `UNIQUE(id, order_type)` for downstream Parent-safe FKs (Voucher DB-004G, Payment DB-004H)
  - `UNIQUE(id, user_id, currency, order_type)` for Parent/Seller identity enforcement
- Composite self-FK `(parent_order_id, user_id, currency, parent_order_type)` enforces that Seller Order shares the same buyer and currency as the Parent Order
- Composite FK `(shop_id, currency)` enforces that Seller Order currency matches the Shop's operating currency
- Partial UNIQUE `(checkout_reference_id) WHERE order_type = 'parent'` enforces checkout idempotency at DB level
- Partial UNIQUE `(parent_order_id, shop_id) WHERE order_type = 'seller'` enforces at most one Seller Order per shop in a checkout
- Order items attach strictly to Seller Orders via composite FK `(order_id, shop_id) REFERENCES orders(id, shop_id)`
- Order items preserve the complete Catalog trace chain through composite FKs `(variant_id, product_id, shop_id)` and `(sku_id, variant_id, shop_id)`
- Product/Variant/SKU IDs remain audit references while immutable snapshot fields remain the historical rendering source
- One SKU appears at most once per Seller Order (`UNIQUE(order_id, sku_id)`)
- Order item quantity is between 1 and 99
- All monetary columns use non-negative `BIGINT` minor units
- Financial balance formulas are strictly enforced:
  - Order: `total_amount = subtotal_amount - discount_amount + tax_amount + shipping_amount`
  - OrderItem: `line_subtotal_amount = unit_price_amount * quantity` and `line_total_amount = line_subtotal_amount - discount_amount + tax_amount`
- Status spaces are strictly partitioned by `order_type`:
  - Parent: `awaiting_payment`, `confirmed`, `partially_completed`, `completed`, `partially_cancelled`, `cancelled`
  - Seller: `awaiting_payment`, `confirmed`, `processing`, `shipped`, `delivered`, `cancelled`
  - Column default is `'awaiting_payment'`
- Lifecycle timestamp chronology and status compatibility are strictly checked (`confirmed_at`, `completed_at`, `cancelled_at`)
- Commercial snapshots (buyer info, shop name, SKU code/name, product/variant names, unit price, attributes JSON) preserve historical truth regardless of live entity changes
- Live SKUs cannot be hard deleted while referenced by orders (`ON DELETE RESTRICT`)
- Future Order repository transitions must update status and append `order_status_histories` in one transaction

## Implementation

Migrations:

- `000006_order.up.sql` creates Order hierarchy, items, and status history tables with constraints and indexes
- `000006_order.down.sql` rolls back Order tables in dependency-safe order

Integration tests:

- `order_migration_test.go` proves Order schema, hierarchy, lifecycle, snapshot, transaction and concurrency invariants on PostgreSQL
- `migration_test_helpers_test.go` supplies shared pool, transaction, UUIDv7 and SQLSTATE helpers
- `seller_catalog_migration_test.go` supplies User, Shop, Product, Variant and SKU fixtures required by Order tests

Important DB constraints:

- `uq_orders_order_number` UNIQUE
- `uq_orders_id_shop_id` UNIQUE for composite OrderItem FK
- `uq_orders_id_order_type` UNIQUE for downstream Parent-safe FKs
- `uq_orders_id_user_currency_type` UNIQUE for Parent self-FK reference
- `fk_orders_parent_identity` composite self-FK with `ON DELETE RESTRICT`
- `fk_orders_shop_currency` composite FK to `shops(id, currency_code)` with `ON DELETE RESTRICT`
- `uq_parent_order_checkout_reference` partial UNIQUE index on Parent Orders
- `uq_orders_parent_shop` partial UNIQUE index on Seller Orders
- `ck_orders_structure` enforcing Parent vs Seller column invariants
- `ck_orders_status_for_type` enforcing allowed state space per order type
- `ck_orders_total_formula` and `ck_order_items_line_total_formula`
- `fk_order_items_order_shop` composite FK preventing direct attachment to Parent
- `fk_order_items_variant_product_shop` composite FK preserving Variant/Product/Shop consistency
- `fk_order_items_sku_variant_shop` composite FK preserving SKU/Variant/Shop consistency
- `uq_order_items_order_sku` UNIQUE
- `ck_order_items_quantity` BETWEEN 1 AND 99
- `ck_orders_status_timestamps`
- `ON DELETE RESTRICT` on all Order FKs

## Tests

- [x] Three Order tables and critical indexes exist
- [x] Canonical UUIDv7-by-Go, BIGINT money, TIMESTAMPTZ, and RESTRICT FKs verified
- [x] Valid multi-Shop Parent + Seller Orders hierarchy accepted
- [x] Status space partitioning enforced (parent cannot use seller status, seller cannot use parent status)
- [x] Parent-Seller hierarchy enforced (seller cannot ref seller, user and currency must match parent, currency must match shop, parent rejects shop ownership)
- [x] Checkout idempotency enforced (one Parent per checkout_reference_id)
- [x] At most one Seller Order per Parent and Shop enforced
- [x] Order lifecycle timestamps and total formula enforced
- [x] OrderItem cannot attach directly to Parent Order
- [x] Cross-shop OrderItem rejected
- [x] SKU-Shop mismatch rejected
- [x] Duplicate SKU in Seller Order rejected
- [x] OrderItem quantity outside 1..99 rejected
- [x] OrderItem line subtotal and total formulas enforced
- [x] OrderItem attributes snapshot must be JSON object
- [x] Historical snapshots preserved when live User, Shop, Product, and SKU change
- [x] Live SKU deletion rejected when referenced by OrderItem
- [x] Status update and status history atomic transition verified
- [x] Invalid status history and actor type rejected
- [x] 32 concurrent checkout attempts produce exactly one Parent Order
- [x] Order migration UP/DOWN/UP cycle works cleanly
- [x] Development and test migration version is `6` with clean dirty state
- [x] `go test ./...` passes
- [x] `go vet ./...` passes
- [x] `go test -race ./internal/database` passes

## Commands

```bash
go test ./...
go vet ./...
make migrate-integration-cycle
make test-integration
go test -race ./internal/database
make migrate-up
```

## Decisions

- UUIDv7 generated by Go application
- Option C chosen: Parent Order aggregates Checkout; child Seller Orders handle fulfillment per Shop
- Single `orders` table with `order_type IN ('parent', 'seller')` and self-referencing composite FK
- OrderItem belongs only to Seller Order; no items attached to Parent Order
- OrderItem keeps Product/Variant/SKU trace IDs and protects them with tenant-safe composite FKs
- Canonical statuses start at `'awaiting_payment'`
- Unique constraint on `(id, order_type)` provided for downstream Parent-safe FKs
- Immutable commercial and entity snapshots stored on Order and OrderItem
- Monetary values use BIGINT in minor units (no float/numeric)
- Status tracking in `order_status_histories` for audit and observability
- No PostgreSQL ENUM; CHECK constraints used for status domain validation
- Parent monetary/status aggregation and the valid transition graph remain Order service invariants because they depend on multiple rows/current state
- OrderItem and status-history append-only behavior will be enforced by repository permissions/API design; this schema ticket does not add mutation-blocking triggers
- sqlc queries, repositories, services, HTTP handlers, Checkout orchestration and Payment integration remain outside DB-004F

## Result

PASS

## Next

ECOM-DB-004G — Voucher Schema
