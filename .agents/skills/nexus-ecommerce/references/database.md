# Database, Transactions, and Migrations

Use this reference for PostgreSQL schema work, sqlc queries, repository methods, transaction boundaries, concurrency control, and migrations.

## Current Status

DB-004A through DB-004F are implemented: the repository has a pgxpool adapter,
golang-migrate tooling, `000001_identity`, `000002_seller`,
`000003_catalog`, `000004_inventory`, `000005_cart`, and `000006_order`.
Their migrations plus executable PostgreSQL tests are the implemented source of truth for
Identity, Seller, Catalog, Inventory, Cart, and Order tables. Voucher and later domain schemas,
SQL queries, and sqlc config remain planned until their own tickets implement them.

DATA-003A → DATA-003F have been reconciled by the DATA-003G review. Treat
`docs/erd.md` as the cross-domain review and each DATA document as the detailed
design target until migrations exist.

## Established Conventions

- New primary keys: PostgreSQL `UUID`, generated as UUIDv7 in the Go application.
- Absolute time: `TIMESTAMPTZ`; convert to a display timezone outside storage.
- `created_at` and `updated_at` default to `now()` where specified. Update `updated_at` explicitly in mutation SQL; no automatic timestamp trigger in V1.
- Mutable lifecycle states: `VARCHAR` plus `CHECK`, not PostgreSQL ENUM.
- Money: integer minor units such as `price_amount_minor BIGINT`, never floating point.
- Media: persist object-storage keys, not permanent public URLs.
- Normal Product, Variant, SKU, and historical-record removal uses archive/soft-delete semantics rather than destructive deletion.
- Application validates semantics. PostgreSQL enforces durable invariants: PK, FK, UNIQUE, ownership/tenant consistency, allowed state, non-negative money/quantity, and timestamp relationships.

## Known Schema Rules

Identity:

- User owns `users` and `user_addresses`; Auth owns `credentials` and `sessions`.
- Login email belongs to Auth, is trimmed/lowercased, and is unique.
- One user has at most one email/password credential.
- A user has at most one default address, enforced with a partial unique index.
- Session expiry is after creation; revocation is not before creation.
- Address changes never rewrite an Order's shipping snapshot.

Seller/Catalog:

- Seller owns `seller_accounts`, `shops`, and `shop_memberships`.
- Catalog owns `categories`, `brands`, `products`, `product_variants`, and `skus`.
- Shop membership is the source of truth for seller-to-shop authorization; do not use a shortcut owner column as the source of truth.
- Composite keys/FKs enforce `Variant.shop_id = Product.shop_id`, `SKU.shop_id = Variant.shop_id`, and `SKU.currency_code = Shop.currency_code`.
- Product slug is unique per Shop. SKU code is unique per Shop.
- SKU owns current price. Product and Variant do not.
- SKU contains no stock quantity. Inventory owns stock by `sku_id`.

Cart:

- One User has at most one active global Cart; historical terminal Carts remain allowed.
- Global Cart has no `shop_id`; each CartItem carries `shop_id` and `sku_id`.
- Composite FK enforces `CartItem.shop_id = SKU.shop_id`.
- One SKU appears once per Cart and quantity is `1..99`.
- Cart stores no price, total, currency, stock, or reservation source of truth.
- CartItem mutations and lifecycle transitions must lock/validate the active Cart in the future repository transaction.

Order:

- Option C: Parent Order aggregates Checkout (no `shop_id`, no direct `order_items`).
- Seller Order owns operational fulfillment for one Shop (`parent_order_id`, `shop_id` required).
- Seller Order buyer (`user_id`) and `currency` must match Parent Order via composite self-FK.
- Seller Order `currency` must match `shops(id, currency_code)`.
- At most one Seller Order per Parent Order and Shop (`uq_orders_parent_shop`).
- At most one Parent Order per Checkout (`checkout_reference_id`).
- Supporting key `UNIQUE(id, order_type)` enables downstream Parent-safe FKs for Voucher and Payment.
- OrderItem belongs to exactly one Seller Order and Shop via `(order_id, shop_id)` and preserves the Catalog trace chain with composite Variant/Product/Shop and SKU/Variant/Shop FKs.
- Order and OrderItem preserve immutable commercial and entity snapshots (names, SKU codes, prices, addresses).
- Statuses start at `'awaiting_payment'` and partition strictly between Parent and Seller.
- The future Order repository must update lifecycle status and append `order_status_histories` atomically in one transaction.

For exact columns, lifecycle checks, and indexes, read
`docs/data-003a-identity.md`, `docs/data-003b-seller-catalog.md`, and the relevant
DATA-003C → DATA-003F document.

## Transaction Boundaries

Use one database transaction for invariants owned by one module when the writes must be atomic. Pass the transaction handle explicitly through the relevant repository operations.

Examples:

- Shop creation or ownership transfer and its active-owner membership constraint.
- Inventory reservation and stock movement.
- Voucher usage increment and limit enforcement.
- Order plus OrderItems, status history, idempotency result, and any owner-module outbox record that must commit with it.
- Payment webhook result, deduplication record, payment state transition, and outbox record.

Cross-module checkout is an application workflow, not a transaction that spans
provider/network calls. Inventory reservation followed by Order failure uses
immediate compensation plus TTL recovery. In V1's shared PostgreSQL, the
post-PaymentSucceeded commerce finalization (Inventory commit + Voucher commit +
Order confirmation/history/outbox) uses one local transaction through the owner
module methods so those database effects cannot partially commit.

## Concurrency

Correctness must hold across multiple processes and API instances. A Go mutex is not inventory or voucher concurrency control.

For stock and limited voucher usage, choose and document a PostgreSQL-safe operation such as a conditional atomic update or row lock inside a transaction. The observable invariants are:

- available/reserved values never become negative;
- successful reservations never exceed stock;
- a limited voucher never records more successful usage than its limit;
- commit and release are idempotent;
- deadlock/serialization errors are retried only with a bounded policy at the transaction boundary.

Use `EXPLAIN`/`EXPLAIN ANALYZE` and evidence from real queries before adding speculative indexes.

## Idempotency and Outbox Persistence

Critical write APIs store at least:

- idempotency key and operation/actor scope;
- canonical request hash;
- processing/completed status;
- stable response or result reference;
- expiry timestamp.

The same key and same request returns the original result. The same key with a different request is rejected.

An owner-module state change and its outbox event are inserted in the same transaction. Consumers use a durable uniqueness constraint on provider event/message identity or an inbox equivalent.

## Migration Rules

The repository uses `golang-migrate`, with direct SQL and planned sqlc rather than an ORM. Follow the established migration naming and rollback policy; do not invent competing conventions.

For each migration:

1. Name and scope it to one coherent schema change.
2. Add constraints and indexes that enforce the documented invariant.
3. Check existing data compatibility before tightening a constraint.
4. Update sqlc queries/generated code when the schema affects them.
5. Test applying migrations to an empty PostgreSQL database and test the affected repository behavior.
6. Document any intentionally irreversible data transformation and its recovery plan.

Never edit an already-applied shared migration to disguise a new change; add a new migration once shared history exists.

## Repository Sources

- `migrations/000001_identity.*.sql`: implemented Identity schema.
- `internal/database/identity_migration_test.go`: executable Identity invariants.
- `migrations/000002_seller.*.sql`: implemented Seller schema.
- `migrations/000003_catalog.*.sql`: implemented Catalog schema.
- `internal/database/seller_catalog_migration_test.go`: executable Seller/Catalog invariants.
- `migrations/000004_inventory.*.sql`: implemented Inventory schema.
- `internal/database/inventory_migration_test.go`: executable Inventory schema, transaction, and concurrency invariants.
- `migrations/000005_cart.*.sql`: implemented Cart schema.
- `internal/database/cart_migration_test.go`: executable Cart schema, lifecycle, transaction, and concurrency invariants.
- `migrations/000006_order.*.sql`: implemented Order schema.
- `internal/database/order_migration_test.go`: executable Order schema, hierarchy, snapshot, lifecycle, transaction, and concurrency invariants.
- `docs/data-003a-identity.md`: Auth/User ERD V1.
- `docs/data-003b-seller-catalog.md`: Seller/Catalog ERD V1.
- `docs/data-003d-cart.md`: Cart ERD V1 and implemented DB-004E boundary.
- `docs/data-003e-order.md`: Order ERD V1 and implemented DB-004F boundary.
- `docs/erd.md`: reconciled cross-domain ERD review.
- `notes/architect.md`: ownership and checkout compensation decisions.
- `nexus_commerce_golang_requirements.txt`: PostgreSQL, pgx, sqlc, golang-migrate, concurrency, outbox, and testing targets.
