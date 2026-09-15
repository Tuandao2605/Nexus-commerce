# ECOM-DB-004G — Voucher Schema

Status: DONE

## Source Design

- DATA-003F — Voucher + Payment Domain Model (Part A: Voucher)
- DATA-003G — Full ERD Review (Sections 2, 7, 18.2, 19, 20, 21, 29, 41)

## Goal

Implement PostgreSQL schema for:

- `vouchers`
- `voucher_usages`

## Important Invariants

### 1. Voucher Scope & Currency Consistency

- Voucher Module owns voucher definitions, eligibility parameters, and usage lifecycle (reservation, commitment, release, expiration).
- Scope partitioning:
  - `scope = 'platform'` must have `shop_id IS NULL`.
  - `scope = 'shop'` must have `shop_id IS NOT NULL`.
- Shop-currency locking at DB level:
  - Composite FK `fk_vouchers_shop_currency` references `shops(id, currency_code)` ON DELETE RESTRICT.
  - Rejects any Shop voucher whose `currency_code` disagrees with the Shop's operating currency.
  - Platform vouchers naturally bypass this constraint because `shop_id IS NULL`.
- Global canonical code namespace in V1:
  - `code = upper(btrim(code))` and `char_length(code) BETWEEN 1 AND 64`.
  - `UNIQUE(code)` enforced globally (e.g. `SAVE10` cannot exist in multiple shops).

### 2. VoucherUsage Currency Locking

- Supporting key on `vouchers`: `UNIQUE (id, currency_code)`.
- Composite FK `fk_voucher_usages_voucher_currency` on `voucher_usages`:
  - `FOREIGN KEY (voucher_id, currency_code) REFERENCES vouchers(id, currency_code) ON DELETE RESTRICT`.
  - Rejects any usage whose currency diverges from its parent voucher definition.

### 3. Discount Rules & Monetary Conventions

- All monetary columns use non-negative `BIGINT` minor units (no floating-point or numeric types).
- Discount type constraints:
  - `fixed_amount`: `discount_value > 0` (minor units) and `max_discount_amount IS NULL`.
  - `percentage`: `discount_value BETWEEN 1 AND 10000` (integer basis points, 10000 = 100%) and `max_discount_amount IS NULL OR max_discount_amount >= 0`.
- Thresholds: `minimum_order_amount >= 0`.

### 4. Allocated Quota Slots & Concurrency Ceiling

- `allocated_usage_count >= 0` represents **allocated quota slots** (`reserved` + `committed` usages):
  - `reserved`: holds a quota slot temporarily during checkout.
  - `committed`: consumes the quota slot permanently upon successful order payment.
  - `released` and `expired`: returns the quota slot (`allocated_usage_count` decremented by 1).
- Concurrency ceiling enforced by DB CHECK:
  - `usage_limit IS NULL OR allocated_usage_count <= usage_limit`.
- Quota mutation rule: counter update and usage state transition must commit atomically in the same database transaction.

### 5. Per-User Concurrency Strategy

- `usage_limit_per_user`: NULL means unlimited; if specified, must be > 0.
- Strategy: Invariant enforced via transaction-scoped advisory lock:
  - The reservation workflow acquires `pg_advisory_xact_lock(hashtext('voucher:' || voucher_id || ':user:' || user_id))` prior to evaluating per-user usage limits.
  - Counts active (`reserved` + `committed`) usages for that `(voucher_id, user_id)` before allocating a global slot.
  - Serializes concurrent checkout attempts by the same user to prevent racing past `usage_limit_per_user`.

### 6. Checkout Idempotency & Retry Semantics

- Database safety net: `UNIQUE(voucher_id, checkout_reference_id)` on `voucher_usages`.
- Idempotency semantic:
  - Retrying with the same `checkout_reference_id` must resolve the existing `voucher_usage` row and return the existing reservation.
  - Retry does not increment `allocated_usage_count` a second time.
- Idempotent release/expiration:
  - Releasing or expiring a usage multiple times does not decrement `allocated_usage_count` more than once.
  - Committing a usage multiple times does not create new redemptions.

### 7. Reservation Deadline (15-Minute TTL)

- Database enforces structural deadline: `expires_at > created_at`.
- Checkout service assigns `expires_at` matching the checkout hold deadline (default 15 minutes, synchronized with Inventory reservation TTL).
- Expired reservation invariant: a usage whose `expires_at` has elapsed cannot transition to `committed`, even if the expiration worker has not yet transitioned the row to `'expired'`.

### 8. Cross-Module Consistency on Commit (RESOLUTION-01 & RESOLUTION-02)

- When transitioning to `committed`, `order_id` is required and must reference a Parent Order.
- Composite FK `fk_voucher_usages_order_parent`:
  - `FOREIGN KEY (order_id, user_id, checkout_reference_id, currency_code, parent_order_type) REFERENCES orders(id, user_id, checkout_reference_id, currency, order_type) ON DELETE RESTRICT`.
  - Proves at DB level that the committed Parent Order belongs to the exact same `user_id`, `checkout_reference_id`, and `currency`, with `order_type = 'parent'`.
- States other than `committed` (`reserved`, `released`, `expired`) must have `order_id IS NULL` and `parent_order_type IS NULL`.

### 9. State-Timestamp Compatibility

- `reserved`: `committed_at IS NULL`, `released_at IS NULL`, `expired_at IS NULL`.
- `committed`: `committed_at IS NOT NULL`, `released_at IS NULL`, `expired_at IS NULL`.
- `released`: `released_at IS NOT NULL`, `committed_at IS NULL`, `expired_at IS NULL`.
- `expired`: `expired_at IS NOT NULL`, `committed_at IS NULL`, `released_at IS NULL`.

### 10. Foreign Key Protection

- `shops → vouchers`: ON DELETE RESTRICT
- `vouchers → voucher_usages`: ON DELETE RESTRICT
- `users → voucher_usages`: ON DELETE RESTRICT
- `orders → voucher_usages`: ON DELETE RESTRICT

## Implementation

Migrations:

- `000007_voucher.up.sql` creates `vouchers`, `voucher_usages`, constraints, check rules, and indexes.
- `000007_voucher.down.sql` rolls back Voucher tables in reverse dependency-safe order.

Integration tests:

- `voucher_migration_test.go` proves Voucher schema, invariants, state transitions, idempotency, and concurrency on PostgreSQL.
- `migration_test_helpers_test.go` supplies shared pool, transaction, UUIDv7, and SQLSTATE helpers.
- Fixtures from `seller_catalog_migration_test.go` and `order_migration_test.go` supply User, Shop, and Parent Order prerequisites.

Important DB constraints:

- `uq_vouchers_code` UNIQUE (global uppercase canonical code)
- `uq_vouchers_id_currency` UNIQUE on `vouchers(id, currency_code)`
- `fk_vouchers_shop_currency` composite FK to `shops(id, currency_code)` ON DELETE RESTRICT
- `ck_vouchers_scope_shop` enforcing shop_id presence/absence matching scope
- `ck_vouchers_code_canonical` enforcing uppercase trimmed non-empty code
- `ck_vouchers_discount_type_value` and `ck_vouchers_discount_cap`
- `ck_vouchers_minimum_order_amount` (>= 0)
- `ck_vouchers_usage_limit_positive` and `ck_vouchers_usage_limit_per_user_positive` (positive limits when set)
- `ck_vouchers_allocated_usage` (`allocated_usage_count >= 0` and `<= usage_limit`)
- `ck_vouchers_validity_period` (`ends_at IS NULL OR ends_at > starts_at`)
- `uq_voucher_usages_voucher_checkout` UNIQUE(voucher_id, checkout_reference_id)
- `fk_voucher_usages_voucher_currency` composite FK to `vouchers(id, currency_code)` ON DELETE RESTRICT
- `fk_voucher_usages_user` to `users(id)` ON DELETE RESTRICT
- `fk_voucher_usages_order_parent` composite FK to `orders(id, user_id, checkout_reference_id, currency, order_type)` ON DELETE RESTRICT
- `ck_voucher_usages_state_timestamps` strictly validating timestamps matching status

## Tests

- [x] Two Voucher tables (`vouchers`, `voucher_usages`) and required indexes created
- [x] UUIDv7-by-Go, BIGINT monetary/counter values, TIMESTAMPTZ, and RESTRICT FKs verified
- [x] Empty, whitespace-only, lowercase, or non-canonical voucher code rejected
- [x] Platform voucher (`shop_id IS NULL`) accepted; platform voucher with `shop_id` rejected
- [x] Shop voucher without `shop_id` rejected; Shop voucher with valid `shop_id` and matching currency accepted
- [x] Shop voucher currency mismatching Shop operating currency rejected by `fk_vouchers_shop_currency`
- [x] Duplicate voucher code rejected (global uniqueness)
- [x] Fixed discount: positive value accepted, cap rejected
- [x] Percentage discount: 1..10000 basis points accepted, >10000 rejected, non-negative cap accepted
- [x] Negative `minimum_order_amount` rejected (< 0)
- [x] Non-positive `usage_limit` (<= 0) and `usage_limit_per_user` (<= 0) rejected
- [x] Negative `allocated_usage_count` (< 0) rejected
- [x] Concurrency counter ceiling: `allocated_usage_count > usage_limit` rejected by DB CHECK
- [x] Validity dates: `ends_at <= starts_at` rejected
- [x] Reserve from draft, inactive, future, or expired Voucher rejected by the canonical eligibility predicate
- [x] VoucherUsage currency mismatching Voucher currency rejected by `fk_voucher_usages_voucher_currency`
- [x] Reservation hold idempotency: duplicate `(voucher_id, checkout_reference_id)` insert rejected by DB UNIQUE
- [x] Retry idempotency: same checkout reference returns existing usage without double-incrementing `allocated_usage_count`
- [x] Per-user concurrency: with `usage_limit_per_user = 1`, 100 concurrent checkout attempts by same user yield exactly 1 reservation (advisory lock)
- [x] Global quota concurrency: with remaining quota = 1, 100 concurrent reservation attempts yield exactly 1 success and 99 rejected (`allocated_usage_count = 1`)
- [x] Composite Parent Order FK: attaching committed usage to mismatched user, currency, checkout_reference_id, or seller order rejected
- [x] Committed usage attaching to Seller Order (`order_type = 'seller'`) rejected
- [x] Timestamp-state compatibility enforced for reserved, committed, released, and expired states
- [x] Commit after `expires_at` rejected even if row status is still reserved
- [x] Release idempotency: releasing an already released/committed usage does not decrement `allocated_usage_count` twice
- [x] Expiration idempotency: expiring an already expired usage does not decrement `allocated_usage_count` twice
- [x] Live Shop, User, Voucher, or Parent Order hard deletion rejected when referenced (`ON DELETE RESTRICT`)
- [x] Migration UP/DOWN/UP cycle works cleanly without leaving dirty migration state
- [x] `go test ./...` passes
- [x] `go vet ./...` passes
- [x] `go test -race ./internal/database` passes

## Commands

```bash
go test ./...
go vet ./...
make migrate-integration-cycle
make test-integration-target DB_TEST_NAME=TestVoucher
go test -race ./internal/database
make migrate-up
```

## Decisions

- UUIDv7 generated by Go application
- Global voucher code namespace in V1 (`UNIQUE(code)`)
- Integer basis points for percentage discounts (1% = 100 bp, 100% = 10000 bp)
- No floating-point or numeric types; minor units (`BIGINT`) for all discount and threshold amounts
- V1 single-currency per voucher matching shop currency or platform order currency
- Currency consistency locked at DB level:
  - `fk_vouchers_shop_currency` locks Shop voucher currency to Shop currency
  - `fk_voucher_usages_voucher_currency` locks VoucherUsage currency to Voucher currency
- Voucher allocation counter (`allocated_usage_count`) represents allocated quota slots (`reserved` + `committed`)
- Per-user limit concurrency serialized using transaction-scoped advisory locks on `(voucher_id, user_id)`
- 15-minute reservation deadline (`expires_at`) aligned with Inventory reservation hold
- Resolution of Parent Order attachment via composite FK `(order_id, user_id, checkout_reference_id, currency_code, parent_order_type)` to enforce data consistency across modules
- No PostgreSQL ENUMs; `VARCHAR` with `CHECK` constraints used throughout

## Result

PASS

## Next

ECOM-DB-004H — Payment Schema
