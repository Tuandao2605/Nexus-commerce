# ECOM-DATA-003G — Full ERD Review

> **Purpose:** ghép toàn bộ data model DATA-003A → DATA-003F và review consistency xuyên module trước khi viết PostgreSQL migrations.
>
> **Ticket type:** review / consolidation only.
>
> **Không mở rộng feature scope.** Chỉ đưa ra các correction/hardening cần thiết để schema hiện tại không tự mâu thuẫn khi triển khai.

---

# 1. Review Result

## Current verdict

```text
DATA-003G
=
PASS (DESIGN)
```

Data model tổng thể có boundary rõ và các cross-domain invariants đã được đồng
bộ trong DATA-003A → DATA-003F. Bốn finding pre-migration đã được resolve:

```text
RESOLUTION-01
All Parent-safe Order references
must be database-safe:
orders.parent_order_id,
voucher_usages.order_id,
payment_transactions.parent_order_id.

RESOLUTION-02
VoucherUsage.user_id
must not disagree with committed Parent Order.user_id.

RESOLUTION-03
One Checkout / Parent Order hierarchy
must have exactly one currency.

RESOLUTION-04
Inventory Reservation correlation semantics
must be standardized around checkout identity.
```

`PASS (DESIGN)` xác nhận specification đã sẵn sàng làm đầu vào cho migration;
không có nghĩa migration/sqlc/Go implementation đã tồn tại.

---

# 2. Modules In Scope

```text
IDENTITY
├── users
├── credentials
├── sessions
└── user_addresses

SELLER
├── seller_accounts
├── shops
└── shop_memberships

CATALOG
├── categories
├── brands
├── products
├── product_variants
└── skus

INVENTORY
├── warehouses
├── inventory_stocks
├── inventory_reservations
├── inventory_reservation_items
└── stock_movements

CART
├── carts
└── cart_items

ORDER
├── orders
├── order_items
└── order_status_histories

VOUCHER
├── vouchers
└── voucher_usages

PAYMENT
├── payment_transactions
├── payment_refunds
└── payment_webhook_events
```

`IDENTITY` ở sơ đồ chỉ là bounded-area umbrella. Logical ownership vẫn tách:
User Module owns `users`, `user_addresses`; Auth Module owns `credentials`,
`sessions`.

---

# 3. Global ERD — Logical View

```text
┌──────────────────────── IDENTITY ────────────────────────┐

users
 │
 ├──── 1:1 ─── credentials
 ├──── 1:* ─── sessions
 ├──── 1:* ─── user_addresses
 │
 └──── 1:0..1 ─ seller_accounts
                   │
                   │
                   ▼

└──────────────────────────────────────────────────────────┘


┌──────────────────────── SELLER ──────────────────────────┐

seller_accounts
      │
      │ *
      ▼
shop_memberships
      ▲
      │ *
      │
    shops
      │
      ├─────────────────────────────────────────────┐
      │                                             │
      ▼                                             ▼

└──────────────────────────────────────────────────────────┘


┌──────────────────────── CATALOG ─────────────────────────┐

shops
 │
 ▼ *
products ─────► brands
 │
 │
 └────► categories
 │
 ▼
product_variants
 │
 ▼
skus
 │
 │ shop_id
 │ currency_code
 ▼

└──────────────────────────────────────────────────────────┘


┌─────────────────────── INVENTORY ────────────────────────┐

shops
 │
 ▼
warehouses
 │
 │
 └─────────────┐
               ▼
            inventory_stocks ◄──────── skus
               │
               ├────► stock_movements
               │
               ▲
               │
inventory_reservation_items
               ▲
               │
inventory_reservations

└──────────────────────────────────────────────────────────┘


┌───────────────────────── CART ───────────────────────────┐

users
 │
 ▼
carts
 │
 ▼
cart_items ─────────────────────────────► skus
     │
     └──────────────────────────────────► shops

└──────────────────────────────────────────────────────────┘


┌──────────────────────── ORDER ───────────────────────────┐

users
 │
 ▼
Parent Order
 │
 ├──────────────────┐
 ▼                  ▼
Seller Order A      Seller Order B
 │                  │
 ▼                  ▼
order_items         order_items
 │                  │
 └──────► skus      └──────► skus

orders
 │
 └────► order_status_histories

└──────────────────────────────────────────────────────────┘


┌─────────────────────── VOUCHER ──────────────────────────┐

shops ───────────────► vouchers
                         │
users ──────────────────┼────► voucher_usages
                         │            │
checkout_reference ─────┘            ▼
                                Parent Order

└──────────────────────────────────────────────────────────┘


┌──────────────────────── PAYMENT ─────────────────────────┐

Parent Order
     │
     │ 1
     ▼
     * payment_transactions
          │
          ├────────► * payment_refunds
          │
          └────────► * payment_webhook_events

└──────────────────────────────────────────────────────────┘
```

---

# 4. Primary Aggregate Flow

```text
User
 │
 ▼
Global Cart
 │
 │ purchase intent
 ▼
Checkout
 │
 ├── Catalog validation
 ├── Seller validation
 ├── Voucher reservation
 ├── Inventory reservation
 ├── money calculation
 │
 ▼
Parent Order
 │
 ├── Seller Order A
 │   └── OrderItems
 │
 └── Seller Order B
     └── OrderItems
 │
 ▼
PaymentTransaction
 │
 ▼
Payment Provider
```

Critical correlation:

```text
checkout_reference_id
```

is the business identity joining the retry-sensitive Checkout flow.

---

# 5. Tenant Root

For seller-owned commerce data, tenant root is:

```text
shops.id
```

Relevant tables carrying or deriving Shop identity:

```text
shops
warehouses
products
product_variants
skus
inventory_stocks
cart_items
seller orders
order_items
shop vouchers
```

Core invariant:

```text
a sellable SKU belongs to exactly one Shop
```

and that Shop identity must not silently change as the SKU moves across modules.

---

# 6. SKU Tenant Chain

Required chain:

```text
SKU.shop_id
=
InventoryStock.shop_id
=
CartItem.shop_id
=
SellerOrder.shop_id
=
OrderItem.shop_id
```

Not every table needs a direct FK to every other table.

The goal is database-enforced consistency where the relationship is persisted.

---

# 7. Catalog SKU Invariants

Catalog defines sellable identity.

Required support:

```text
skus.id
skus.shop_id
```

with:

```sql
UNIQUE(id, shop_id)
```

and:

```sql
UNIQUE(shop_id, sku_code)
```

Meaning:

```text
SKU ID
→ one Shop
```

SKU code only needs uniqueness inside Shop.

Downstream modules depend on `UNIQUE(id, shop_id)` for composite tenant-safe FKs.

---

# 8. Inventory Tenant Consistency

`inventory_stocks` carries:

```text
shop_id
warehouse_id
sku_id
```

Required:

```sql
FOREIGN KEY (warehouse_id, shop_id)
REFERENCES warehouses(id, shop_id)
ON DELETE RESTRICT
```

```sql
FOREIGN KEY (sku_id, shop_id)
REFERENCES skus(id, shop_id)
ON DELETE RESTRICT
```

Therefore:

```text
Warehouse.shop_id
=
InventoryStock.shop_id
=
SKU.shop_id
```

This blocks:

```text
Warehouse Shop A
+
SKU Shop B
→ InventoryStock
```

at DB level.

---

# 9. Cart Tenant Consistency

Global Cart intentionally has no `shop_id`.

Each `cart_item` has:

```text
cart_id
shop_id
sku_id
```

Required composite FK:

```sql
FOREIGN KEY (sku_id, shop_id)
REFERENCES skus(id, shop_id)
ON DELETE RESTRICT
```

Thus:

```text
CartItem.shop_id
=
SKU.shop_id
```

Logical item uniqueness:

```sql
UNIQUE(cart_id, sku_id)
```

```sql
CHECK (quantity BETWEEN 1 AND 99)
```

A global Cart can safely hold:

```text
Shop A SKU
Shop B SKU
Shop C SKU
```

without allowing one item to claim the wrong Shop.

---

# 10. Order Tenant Consistency

Seller Order has:

```text
shop_id NOT NULL
```

OrderItem has:

```text
order_id
shop_id
product_id
variant_id
sku_id
attributes_snapshot
```

Required:

```sql
UNIQUE(id, shop_id)
```

on `orders`, plus:

```sql
FOREIGN KEY (order_id, shop_id)
REFERENCES orders(id, shop_id)
ON DELETE RESTRICT
```

and the Catalog identity chain:

```sql
FOREIGN KEY (variant_id, product_id, shop_id)
REFERENCES product_variants(id, product_id, shop_id)
ON DELETE RESTRICT

FOREIGN KEY (sku_id, variant_id, shop_id)
REFERENCES skus(id, variant_id, shop_id)
ON DELETE RESTRICT
```

Result:

```text
SellerOrder.shop_id
=
OrderItem.shop_id
=
ProductVariant.shop_id
=
SKU.shop_id
```

Because Parent Orders structurally have:

```text
shop_id = NULL
```

and OrderItems require non-null `shop_id`, this composite FK also prevents an OrderItem from attaching directly to a Parent Order.

Therefore OrderItem → SellerOrder type safety is already indirectly DB-enforced.

---

# 11. Cross-Shop Attachment Review

## PASS

The following dangerous states are blocked by the intended composite-FK design:

```text
InventoryStock:
Warehouse Shop A + SKU Shop B

CartItem:
shop_id Shop A + SKU Shop B

OrderItem:
Seller Order Shop A + SKU Shop B
```

## Migration prerequisite

Before child FKs are created, parent tables need:

```text
warehouses UNIQUE(id, shop_id)
skus UNIQUE(id, shop_id)
skus UNIQUE(id, variant_id, shop_id)
product_variants UNIQUE(id, product_id, shop_id)
orders UNIQUE(id, shop_id)
```

---

# 12. User Ownership Chain

Primary User ownership:

```text
users
├── credentials
├── sessions
├── user_addresses
├── seller_accounts
├── carts
├── orders
└── voucher_usages
```

User Module remains source of truth for user identity/profile; Auth Module owns
authentication material and session lifecycle.

Seller business status is not stored on `users`.

---

# 13. Seller Authorization Chain

Seller authorization source:

```text
seller_accounts
      │
      ▼
shop_memberships
      │
      ▼
shops
```

Seller access must not rely only on:

```text
user.role = seller
```

Operational authorization is:

```text
seller_account
+
active shop_membership
+
membership role
```

This applies especially to:

```text
Catalog mutations
Inventory mutations
Seller Order reads/transitions
Shop voucher management
```

---

# 14. Cart Ownership

V1:

```text
one ACTIVE global Cart per User
```

enforced by:

```sql
CREATE UNIQUE INDEX uq_carts_user_active
ON carts(user_id)
WHERE status = 'active';
```

CartItem mutations require an ACTIVE Cart.

Lifecycle transition and CartItem mutation must serialize on Cart row.

---

# 15. Cart → Checkout → Order Correlation

Cart itself is not the commercial record.

Flow:

```text
Cart
→ Checkout
→ Parent Order
```

Parent Order has:

```text
checkout_reference_id
```

Required invariant:

```text
one successful Checkout
→ exactly one Parent Order hierarchy
```

Enforced by:

```sql
CREATE UNIQUE INDEX uq_parent_order_checkout_reference
ON orders(checkout_reference_id)
WHERE order_type = 'parent';
```

Retry:

```text
Checkout X
Checkout X retry
Checkout X retry
        │
        ▼
same Parent Order
```

not multiple commercial transactions.

Checkout input contains non-empty `selected_cart_item_ids`. After Order hierarchy
and PaymentTransaction initialization succeed, Cart removes only those selected
items. Cart remains ACTIVE when items remain and becomes CHECKED_OUT only when
empty. `MAX_CART_ITEM_QUANTITY = 99` is enforced by DB and atomic mutations.

Retry resolves `checkout_reference_id` before treating already-removed CartItems
as missing, so it returns the same hierarchy rather than creating new effects.

---

# 16. Parent vs Seller Order Structural Model

`orders` stores two row types:

```text
parent
seller
```

Parent:

```text
parent_order_id = NULL
shop_id = NULL
checkout_reference_id NOT NULL
```

Seller:

```text
parent_order_id NOT NULL
shop_id NOT NULL
checkout_reference_id = NULL
```

Seller uniqueness:

```sql
CREATE UNIQUE INDEX uq_orders_parent_shop
ON orders(parent_order_id, shop_id)
WHERE order_type = 'seller';
```

---

# 17. Parent/Seller Status Space

Shared column:

```text
orders.status
```

but type-aware DB constraint is required.

Parent state space:

```text
awaiting_payment
confirmed
partially_completed
completed
partially_cancelled
cancelled
```

Seller state space:

```text
awaiting_payment
confirmed
processing
shipped
delivered
cancelled
```

DB validates:

```text
status compatible with order_type
```

Order Service validates transition graph.

Both row types are created at `awaiting_payment`. `CREATED` is a domain event,
not a persisted state. Timely PaymentSucceeded confirms the hierarchy;
PaymentFailed leaves it awaiting while retry window remains; deadline expiry
cancels it. Late success is refunded/reconciled and never fulfills expired
Inventory/Voucher holds.

---

# 18. RESOLUTION-01 — Parent-safe Order References

There are **three** relations that must prove the referenced row is a Parent Order:

```text
1. orders.parent_order_id
2. voucher_usages.order_id
3. payment_transactions.parent_order_id
```

A plain FK:

```sql
REFERENCES orders(id)
```

only proves:

```text
the Order row exists
```

It does not prove:

```text
order_type = 'parent'
```

Without hardening, the DB could allow:

```text
Parent P1
└── Seller S1
    └── Seller S2   ❌
```

or:

```text
VoucherUsage
→ Seller Order      ❌
```

or:

```text
PaymentTransaction
→ Seller Order      ❌
```

The required hierarchy is always:

```text
Parent
├── Seller A
├── Seller B
└── Seller C
```

## 18.1 Strong self-FK for Parent → Seller hierarchy

For the self-reference, do more than type safety.

Seller Order must prove simultaneously:

```text
referenced row is Parent Order
AND same user
AND same currency
```

Add to `orders`:

```text
parent_order_type VARCHAR(20) NULL
```

Parent:

```text
order_type = 'parent'
parent_order_id = NULL
parent_order_type = NULL
```

Seller:

```text
order_type = 'seller'
parent_order_id NOT NULL
parent_order_type = 'parent'
```

Supporting parent identity:

```sql
UNIQUE (
    id,
    user_id,
    currency,
    order_type
);
```

Composite self-FK:

```sql
FOREIGN KEY (
    parent_order_id,
    user_id,
    currency,
    parent_order_type
)
REFERENCES orders (
    id,
    user_id,
    currency,
    order_type
)
ON DELETE RESTRICT;
```

Structural CHECK:

```sql
CHECK (
    (
        order_type = 'parent'
        AND parent_order_id IS NULL
        AND parent_order_type IS NULL
    )
    OR
    (
        order_type = 'seller'
        AND parent_order_id IS NOT NULL
        AND parent_order_type = 'parent'
    )
);
```

This upgrades three former service invariants into DB facts:

```text
SellerOrder.parent is Parent       ✅
SellerOrder.user_id = Parent.user  ✅
SellerOrder.currency = Parent.currency ✅
```

Therefore a chain:

```text
Seller → Seller
```

is impossible.

## 18.2 VoucherUsage → Parent

VoucherUsage needs stronger proof than only `order_id`.

On commit it must prove:

```text
Parent type
same User
same checkout_reference_id
same currency
```

Recommended supporting Order key:

```sql
UNIQUE (
    id,
    user_id,
    checkout_reference_id,
    currency,
    order_type
);
```

VoucherUsage carries:

```text
order_id
user_id
checkout_reference_id
currency_code
parent_order_type = 'parent'
```

Composite FK:

```sql
FOREIGN KEY (
    order_id,
    user_id,
    checkout_reference_id,
    currency_code,
    parent_order_type
)
REFERENCES orders (
    id,
    user_id,
    checkout_reference_id,
    currency,
    order_type
)
ON DELETE RESTRICT;
```

with:

```sql
CHECK (
    parent_order_type = 'parent'
)
```

for committed rows / according to the final nullable lifecycle design.

This closes both:

```text
VoucherUsage → Seller Order
```

and:

```text
VoucherUsage User A
→ Parent Order User B
```

and also guarantees the usage commits to the Parent created from the same Checkout.

## 18.3 PaymentTransaction → Parent

Payment must prove:

```text
Parent type
same currency
```

Recommended supporting Order key:

```sql
UNIQUE (
    id,
    currency,
    order_type
);
```

PaymentTransaction carries:

```text
parent_order_id
currency_code
parent_order_type = 'parent'
```

Composite FK:

```sql
FOREIGN KEY (
    parent_order_id,
    currency_code,
    parent_order_type
)
REFERENCES orders (
    id,
    currency,
    order_type
)
ON DELETE RESTRICT;
```

This upgrades:

```text
Payment.currency = Parent.currency
```

from a service-only invariant to a DB invariant.

## 18.4 Generic `UNIQUE(id, order_type)`

A generic:

```sql
UNIQUE(id, order_type)
```

is valid if a downstream FK only needs type safety.

However, after adopting the stronger composite FKs above, do **not** add it automatically if no FK uses it.

Reason:

```text
PK(id)
+
multiple composite UNIQUE keys
```

already create several indexes.

DATA-003G should prefer the strongest required composite identity and avoid redundant supporting indexes.

---

# 19. Voucher Usage → Parent Order

Voucher usage is created before Order commit and therefore:

```text
order_id NULL
```

while RESERVED.

On COMMITTED:

```text
order_id NOT NULL
```

and it must be the Parent Order generated from the same Checkout.

Required logical relation:

```text
voucher_usage.checkout_reference_id
=
parent_order.checkout_reference_id
```

Current `UNIQUE(voucher_id, checkout_reference_id)` correctly protects retry identity.

---

# 20. RESOLUTION-02 — Voucher User/Order Ownership Consistency

The earlier design had independent:

```text
user_id
order_id
```

That could theoretically allow:

```text
voucher_usage.user_id = User A
order_id = Parent Order of User B
```

The finalized composite FK below removes that cross-user commercial integrity
risk.

## Required invariant

On Voucher commit:

```text
VoucherUsage.user_id
=
ParentOrder.user_id
```

and:

```text
VoucherUsage.checkout_reference_id
=
ParentOrder.checkout_reference_id
```

## Required migration hardening

Use the composite Parent FK defined in RESOLUTION-01:

```text
(order_id,
 user_id,
checkout_reference_id,
 currency_code,
 parent_order_type)
```

→

```text
(orders.id,
 orders.user_id,
 orders.checkout_reference_id,
 orders.currency,
 orders.order_type)
```

This makes the equality a DB invariant rather than only a service check.

---

# 21. Voucher Scope / Order Correlation

Platform voucher:

```text
scope = platform
```

may apply across eligible Seller Groups in the same Checkout.

Shop voucher:

```text
scope = shop
```

must calculate minimum and discount only from:

```text
Seller Group where shop_id = voucher.shop_id
```

Never use Parent total to satisfy a Shop voucher threshold.

Order snapshots final accepted discount amounts.

Voucher remains source of truth for quota/redemption lifecycle, not historical Order pricing.

---

# 22. Payment → Parent Order

PaymentTransaction belongs to Parent Order.

Cardinality:

```text
Parent Order
1 → many historical PaymentTransactions
```

but V1 permits only one economically live attempt:

```text
pending
processing
succeeded
```

Required:

```sql
CREATE UNIQUE INDEX uq_payment_parent_live_attempt
ON payment_transactions(parent_order_id)
WHERE status IN ('pending', 'processing', 'succeeded');
```

This prevents concurrent double-charge attempts in V1 simple-capture model.

Together with RESOLUTION-01, Payment must both:

```text
reference Parent Order
AND
allow at most one live payment per Parent Order.
```

---

# 23. Payment Retry Identities

Three identities must not be conflated:

```text
checkout_reference_id
=
business Checkout identity
```

```text
payment_transactions.idempotency_key
=
merchant payment command identity
```

```text
(provider, provider_payment_id)
=
provider payment identity
```

Each solves a different retry boundary.

Required uniqueness:

```text
Parent checkout reference
→ one Parent hierarchy

payment idempotency key
→ one logical merchant payment command

provider + provider_payment_id
→ one local provider transaction
```

---

# 24. Webhook Identity

Webhook dedup identity:

```text
(provider, provider_event_id)
```

Required UNIQUE.

Pipeline:

```text
verify signature
→ timestamp tolerance
→ durable dedup
→ resolve payment
→ lock
→ validate transition
→ apply or ignore
```

Webhook current-state mutation alone is not sufficient deduplication.

---

# 25. Refund Identity

Refund command:

```text
idempotency_key
+
request_hash
```

Same key + same semantic request:

```text
same refund
```

Same key + different semantic request:

```text
IDEMPOTENCY_KEY_REUSED
```

Provider refund identity is separately unique.

---

# 26. Inventory Reservation Correlation

Inventory reservation is created **before** the Parent Order may exist.

Therefore Inventory should not require `order_id` as its initial business identity.

Existing generic correlation:

```text
reference_type
reference_id
```

is appropriate only if its semantic is standardized.

---

# 27. RESOLUTION-04 — Standardized Inventory Reservation Reference

Before migrations, define V1 semantics:

```text
Inventory checkout reservation:

reference_type = 'checkout'
reference_id   = checkout_reference_id
```

Then flow is:

```text
Checkout X
├── InventoryReservation.reference = X
├── VoucherUsage.checkout_reference_id = X
└── ParentOrder.checkout_reference_id = X
```

This gives one correlation spine without requiring Order to exist before Inventory reserve.

Do not mix:

```text
sometimes reference_id = cart_id
sometimes = order_id
sometimes = checkout_reference_id
```

for the same reservation type.

V1 constrains `reference_type = 'checkout'`; `reference_id` is always the
`checkout_reference_id`. There is no Checkout table to reference in V1, so this
correlation remains contract-enforced and covered by integration tests.

---

# 28. Inventory Reservation TTL

Inventory reservation validity:

```text
status = active
AND expires_at > wall-clock deadline
```

Worker execution time is not business validity.

Expired reservation cannot be committed even before cleanup worker executes.

Worker later releases:

```text
reserved_quantity
```

and marks reservation EXPIRED.

This model is consistent with Voucher TTL semantics.

---

# 29. Voucher Reservation TTL

Voucher usage:

```text
RESERVED
├── COMMITTED
├── RELEASED
└── EXPIRED
```

Effective reservation:

```text
status = reserved
AND expires_at > wall-clock now
```

Expired:

```text
status = reserved
AND expires_at <= wall-clock now
```

must not commit.

Cleanup releases:

```text
allocated_usage_count
```

exactly once.

Thus both temporary-hold domains use:

```text
business deadline
!=
worker timing
```

---

# 30. Currency Model — Current Design

Catalog:

```text
SKU has:
price_amount_minor
currency_code
```

Seller/Catalog V1 rule:

```text
one currency per Shop
SKU.currency = Shop.currency
```

Order:

```text
one Parent Order hierarchy
=
one currency
```

Voucher:

```text
one voucher
=
one currency
```

Payment:

```text
PaymentTransaction.amount/currency
=
Parent Order amount/currency snapshot
```

Refund:

```text
refund.currency
=
payment.currency
```

---

# 31. RESOLUTION-03 — Global Cart vs Multi-Shop Currency

Global Cart can contain items from multiple Shops.

Catalog only guarantees:

```text
one currency per Shop
```

It does **not** imply:

```text
all Shops use same currency
```

Therefore a Cart could theoretically contain:

```text
Shop A = VND
Shop B = USD
```

But one Parent Order stores:

```text
one currency
one total
```

and Payment is:

```text
one amount + one currency
```

A mixed-currency Parent Order is invalid.

## Final V1 rule

```text
one Checkout
→ one Parent Order hierarchy
→ exactly one currency
```

Checkout must reject or partition a mixed-currency Cart selection.

Recommended V1 behavior:

```text
Global Cart may retain multi-Shop intent,
but one checkout command may include only items
whose SKU/Shop currency is identical.
```

Then:

```text
Parent.currency
=
every SellerOrder.currency
=
every OrderItem price currency semantic
=
accepted Voucher.currency
=
Payment.currency
=
Refund.currency
```

No FX conversion in V1.

---

# 32. Money Representation Review

## PASS

All commerce/financial modules use:

```text
BIGINT minor units
+
CHAR(3) currency
```

Percentage discounts use:

```text
integer basis points
```

No domain in the reviewed model requires:

```text
FLOAT
DOUBLE PRECISION
REAL
```

for money.

This should become a migration lint/review invariant.

---

# 33. Money Source-of-Truth Boundaries

Catalog:

```text
current SKU selling price
```

Cart:

```text
does not own authoritative price
```

Checkout:

```text
calculates accepted commercial result
```

Order:

```text
snapshots commercial price/discount/tax/shipping
```

Voucher:

```text
owns eligibility/quota/redemption,
not historical Order total
```

Payment:

```text
snapshots amount/currency for financial record,
does not own commercial pricing calculation
```

No source-of-truth duplication exists if these boundaries are respected.

---

# 34. Price Snapshot Chain

```text
Catalog SKU current price
        │
        ▼
Checkout validation
        │
        ▼
OrderItem.unit_price_amount
```

At T2 Catalog may change.

Old Order remains unchanged.

Payment uses Parent Order total at payment creation and snapshots it again as financial history.

This is intentional snapshot duplication, not conflicting source-of-truth duplication.

---

# 35. Historical Snapshot Boundaries

Historical truth belongs in Order for:

```text
customer identity at purchase
shipping address
shop name
product name
variant/SKU display
product_id / variant_id / sku_id trace chain
selected variant attributes
SKU code
quantity
unit price
discount
tax
shipping amount
currency
totals
```

Current FKs remain for:

```text
trace
audit
authorization/navigation
correlation
```

Rule:

```text
FK
!=
historical rendering source
```

---

# 36. Historical Delete Safety

Commercial/audit tables must not be cascade-deleted.

Required RESTRICT direction includes:

```text
users → orders
shops → orders
orders → seller child orders
orders → order_items
orders → order_status_histories
skus → order_items
product_variants → order_items

shops → warehouses
skus → inventory_stocks
inventory_stocks → stock_movements
inventory_reservations → reservation_items

vouchers → voucher_usages
orders → committed voucher usages

orders → payment_transactions
payment_transactions → payment_refunds
payment_transactions → payment_webhook_events
```

Business lifecycle uses:

```text
status/archive
```

instead of destructive parent delete.

---

# 37. CASCADE Review

## Historical domains

```text
Order
Inventory ledger/reservations
Voucher committed history
Payment/refund/webhook audit
```

should not use destructive CASCADE.

## Ephemeral Identity/Auth rows

Sessions/credentials may have different deletion semantics because they are not commercial history.

Those rules belong to Identity/security lifecycle and must not be copied into commerce history.

## Cart

CartItem is ephemeral intent and explicit deletion is allowed.

Current design still prefers explicit mutation / RESTRICT instead of accidental cascade.

---

# 38. Status Ownership Matrix

| State | Owner |
|---|---|
| User/account identity lifecycle | Identity |
| Seller account / membership / Shop lifecycle | Seller |
| Product / Variant / SKU lifecycle | Catalog |
| Inventory reservation lifecycle | Inventory |
| Cart lifecycle | Cart |
| Parent/Seller Order lifecycle | Order |
| Voucher definition + usage lifecycle | Voucher |
| Payment/refund/webhook processing lifecycle | Payment |

Forbidden:

```text
Payment repository
→ directly UPDATE Order.status
```

Forbidden:

```text
Order service
→ directly mutate Voucher usage counter
```

Forbidden:

```text
Cart
→ directly reserve Inventory
without Checkout orchestration
```

Cross-module coordination happens through service calls/events/orchestration.

---

# 39. Order Status History

Current status:

```text
orders.status
```

Audit:

```text
order_status_histories
```

Every transition:

```text
lock Order
validate graph
update status
insert history
commit
```

same DB transaction.

This is not duplicate source-of-truth:

```text
status = current state
history = immutable audit
```

---

# 40. Inventory Source-of-Truth Review

```text
inventory_stocks
=
current stock state
```

Stored:

```text
on_hand_quantity
reserved_quantity
```

Derived:

```text
available_quantity
=
on_hand_quantity - reserved_quantity
```

`available_quantity` is not persisted as third mutable field.

```text
stock_movements
=
physical stock history
```

Reservation does not create stock movement because physical stock does not change.

This boundary is consistent.

---

# 41. Voucher Source-of-Truth Review

```text
vouchers
=
definition + global allocation counter
```

```text
voucher_usages
=
individual reserved/committed/released/expired redemption records
```

Counter:

```text
allocated_usage_count
```

is a concurrency-maintained aggregate, intentionally duplicated for O(1) quota allocation.

It is acceptable only because every increment/decrement must occur transactionally with the usage state change.

This should be covered by invariant tests.

---

# 42. Payment Source-of-Truth Review

```text
payment_transactions.status
=
current provider/payment attempt state
```

```text
payment_webhook_events
=
provider inbound event inbox/audit
```

```text
payment_refunds
=
refund states
```

`payment_webhook_events` does not replace PaymentTransaction current state.

No full `payment_status_histories` table exists in V1 by design.

---

# 43. Minor Schema Correction — Provider Refund Namespace

Payment/provider identities already use:

```text
(provider, provider_payment_id)
```

Webhook identities use:

```text
(provider, provider_event_id)
```

Refund identity must follow the same namespace rule.

Do **not** use globally unique:

```text
UNIQUE(provider_refund_id)
```

because two providers may legally produce:

```text
Provider A → re_123
Provider B → re_123
```

Add to `payment_refunds`:

```text
provider VARCHAR(40) NOT NULL
provider_refund_id VARCHAR(191) NULL
```

Required unique index:

```sql
CREATE UNIQUE INDEX uq_refund_provider_refund
ON payment_refunds(
    provider,
    provider_refund_id
)
WHERE provider_refund_id IS NOT NULL;
```

Refund must not claim a provider or currency different from its
PaymentTransaction.

Recommended DB hardening:

On `payment_transactions`:

```sql
UNIQUE(id, provider, currency_code)
```

Then:

```sql
FOREIGN KEY (
    payment_transaction_id,
    provider,
    currency_code
)
REFERENCES payment_transactions(
    id,
    provider,
    currency_code
)
ON DELETE RESTRICT;
```

Result:

```text
PaymentRefund.provider
=
PaymentTransaction.provider

PaymentRefund.currency_code
=
PaymentTransaction.currency_code
```

This is a minor schema correction, not a new feature.

---

# 44. Idempotency Identity Matrix

| Boundary | Business identity | Protection |
|---|---|---|
| Inventory reservation | `idempotency_key` + `request_hash` | UNIQUE key + hash comparison |
| Order creation | `checkout_reference_id` | partial UNIQUE on Parent |
| Voucher reservation | `(voucher_id, checkout_reference_id)` | UNIQUE |
| Payment command | `idempotency_key` + `request_hash` | UNIQUE key + hash comparison |
| Provider payment | `(provider, provider_payment_id)` | partial UNIQUE |
| Webhook | `(provider, provider_event_id)` | UNIQUE |
| Refund command | `idempotency_key` + `request_hash` | UNIQUE key + hash comparison |
| Provider refund | `(provider, provider_refund_id)` | partial UNIQUE |

Inventory, Payment and Refund `idempotency_key` values in this matrix are
server-generated, operation-namespaced command identities. A raw external HTTP
key must instead be scoped by authenticated actor + operation and protected by
request-hash comparison.

Important distinction:

```text
request identity
!=
business checkout identity
!=
provider identity
!=
provider event identity
```

---

# 44.1 Retry Review

## Inventory

Same key/same semantic request:

```text
one hold
```

## Order

Same Checkout reference:

```text
one Parent hierarchy
```

## Voucher

Same Voucher + Checkout:

```text
one quota allocation
```

## Payment

Same payment key:

```text
one logical attempt
```

and only one live attempt per Parent Order.

## Webhook

Same provider event:

```text
one logical processing effect
```

## Refund

Same refund key:

```text
one refund allocation
```

### Result

No obvious retry path should create duplicate business effect **provided the unique constraints and transaction ordering are implemented exactly as designed**.

---

# 45. TTL Matrix

| Domain | Deadline field | Effective validity | Worker role |
|---|---|---|---|
| Inventory Reservation | `expires_at` | active + deadline not passed | cleanup/release |
| Voucher Usage | `expires_at` | reserved + deadline not passed | cleanup/release |
| Payment attempt | `expires_at` | attempt window not passed | reconciliation/cancel |

Rule:

```text
deadline determines validity
worker restores materialized counters/state
```

Never:

```text
worker has not run
→ reservation is still valid
```

---

# 46. Parent Order Downstream Matrix

| Downstream relation | Must target |
|---|---|
| `orders.parent_order_id` | Parent Order |
| `order_items.order_id` | Seller Order |
| `voucher_usages.order_id` | Parent Order |
| `payment_transactions.parent_order_id` | Parent Order |

Review result:

```text
orders.parent_order_id → Parent
requires strong composite self-FK.

OrderItem → Seller Order
is enforced via non-null shop_id composite FK.

VoucherUsage → Parent
requires strong Parent/User/Checkout composite FK.

PaymentTransaction → Parent
requires strong Parent/Currency composite FK.
```

---

# 47. Currency Consistency Matrix

```text
Shop.currency
    │
    ▼
SKU.currency
    │
    ▼
Checkout currency
    │
    ├────► Voucher.currency
    │
    ▼
Parent Order.currency
    │
    ├────► Seller Order.currency
    │
    ▼
PaymentTransaction.currency
    │
    ▼
PaymentRefund.currency
```

Required equalities:

```text
SKU.currency = Shop.currency
```

```text
all checked-out Seller Groups use same currency
```

```text
Voucher.currency = checkout currency
```

```text
Parent.currency = every child SellerOrder.currency
```

```text
Payment.currency = Parent.currency
```

```text
Refund.currency = Payment.currency
```

---

# 48. Index Review — Clearly Required

## Identity / ownership

```text
credentials lookup identities
sessions token/session lookup
user_addresses(user_id)
seller_accounts(user_id UNIQUE)
shop_memberships(shop_id, status)
shop_memberships(seller_account_id, status)
```

Exact Identity indexes should follow the finalized DATA-003A schema.

## Catalog

```text
skus UNIQUE(id, shop_id)
skus UNIQUE(shop_id, sku_code)
skus(shop_id, status)
```

## Inventory

```text
warehouses UNIQUE(id, shop_id)
warehouses UNIQUE(shop_id, code)

inventory_stocks UNIQUE(warehouse_id, sku_id)
inventory_stocks(shop_id, sku_id)

inventory_reservations UNIQUE(idempotency_key)
inventory_reservations(expires_at)
WHERE status='active'

reservation_items UNIQUE(reservation_id, inventory_stock_id)

stock_movements(inventory_stock_id, created_at DESC)
```

## Cart

```text
carts(user_id)
WHERE status='active' UNIQUE

cart_items UNIQUE(cart_id, sku_id)
cart_items(cart_id, shop_id)
```

## Order

```text
orders UNIQUE(id, shop_id)

orders UNIQUE(id, user_id, currency, order_type)
-- self-FK: Parent type + same User + same currency

orders UNIQUE(id, user_id, checkout_reference_id, currency, order_type)
-- VoucherUsage Parent/User/Checkout safety

orders UNIQUE(id, currency, order_type)
-- Payment Parent/currency safety

uq_parent_order_checkout_reference

uq_orders_parent_shop

orders(user_id, created_at DESC)

orders(shop_id, status, created_at DESC)
WHERE order_type='seller'

order_items(order_id)

order_status_histories(order_id, created_at)
```

## Voucher

```text
vouchers UNIQUE(code)

voucher_usages UNIQUE(voucher_id, checkout_reference_id)

voucher_usages(expires_at)
WHERE status='reserved'

voucher_usages(user_id, voucher_id)
```

## Payment

```text
payment_transactions UNIQUE(idempotency_key)

uq_payment_parent_live_attempt

UNIQUE(provider, provider_payment_id)
WHERE provider_payment_id IS NOT NULL

payment_transactions(status, created_at)
WHERE status IN ('pending','processing')

payment_refunds UNIQUE(idempotency_key)

payment_transactions UNIQUE(id, provider)
-- supports webhook/provider consistency FK

payment_transactions UNIQUE(id, provider, currency_code)
-- supports refund/provider/currency consistency FK

payment_refunds UNIQUE(provider, provider_refund_id)
WHERE provider_refund_id IS NOT NULL

payment_refunds(payment_transaction_id, created_at DESC)

payment_webhook_events UNIQUE(provider, provider_event_id)

payment_webhook_events(processing_status, received_at)
WHERE processing_status IN ('received','failed')
```

---

# 49. Index Review — Potential Redundancy

Before writing migrations, avoid blindly creating indexes for every FK.

PostgreSQL:

```text
PK / UNIQUE
→ automatically creates index

FK
→ does NOT automatically create child-side index
```

Therefore review based on query path.

Examples:

```text
UNIQUE(cart_id, sku_id)
```

already supports queries by:

```text
cart_id
```

so a separate plain:

```text
INDEX(cart_id)
```

would usually be redundant.

Similarly:

```text
UNIQUE(warehouse_id, sku_id)
```

already supports leading-column lookup by `warehouse_id`.

But it does **not** replace:

```text
INDEX(sku_id)
```

if reverse SKU lookup is required.

Do this redundancy check in each migration before adding indexes.

---

# 50. Index Review — Missing Before Migration

Clearly add/confirm the **composite Order identities actually used by FKs**:

```text
orders UNIQUE(id, user_id, currency, order_type)

orders UNIQUE(id, user_id, checkout_reference_id, currency, order_type)

orders UNIQUE(id, currency, order_type)
```

Do not add a generic `UNIQUE(id, order_type)` unless an actual FK references exactly that key.

Also confirm:

```text
voucher_usages reserved-expiry partial index
```

exists in final migration.

Confirm:

```text
uq_payment_parent_live_attempt
```

exists; it is essential financial correctness, not merely performance.

Confirm Inventory has:

```text
inventory_stocks(shop_id, sku_id)
```

for tenant + SKU availability queries.

---

# 51. Cross-Domain Constraint Classification

## Database-enforced

Should include:

```text
PK/FK existence
tenant composite FKs
status value sets
status/order_type compatibility
money non-negative constraints
quantity invariants
unique business identities
one live payment attempt
one active cart
one seller order/shop/parent
TTL timestamp structure
Parent/child user and currency equality
Parent-safe Voucher and Payment references
Refund provider and currency equality
Order/Payment/Refund status timestamp compatibility
```

## Transaction/service-enforced

Includes:

```text
state transition graphs
Parent aggregate status derivation
Parent totals = child totals
Cart mutation only while ACTIVE
partial checkout selected-item cleanup
Checkout sequencing
Voucher eligible amount calculation
Inventory multi-row all-or-nothing reserve
webhook transition ordering
```

## Orchestration-enforced

Includes:

```text
Inventory reserve
+
Voucher reserve
+
Order create
+
Payment initiation bounded by hold deadline
+
selected CartItem cleanup / terminal transition
```

because these cross module/transaction boundaries.

Failure compensation follows reverse order: release any earlier Voucher hold on
Inventory failure; release both holds and cancel an initialized
`awaiting_payment` hierarchy on Order/Payment initialization failure. TTL workers
recover crashes but are not the primary failure path.

After a timely PaymentSucceeded, V1 uses one shared-PostgreSQL finalization
transaction—through owner-module interfaces—to dedupe the event, validate both
holds, commit Inventory/Voucher, confirm the Order hierarchy, write histories
and persist required outbox records. Provider I/O remains outside this local
transaction. A failed precondition commits none of these commerce effects.

---

# 52. Source-of-Truth Duplication Review

## Intentional snapshots — PASS

```text
Order customer snapshot
Order seller snapshot
Order SKU/product snapshot
Order monetary snapshot
Payment amount snapshot
Voucher discount usage snapshot
```

These are historical records, not current-domain duplicates.

## Mutable source-of-truth — one owner each

```text
User current profile
→ Identity

Shop membership
→ Seller

SKU current price
→ Catalog

Stock
→ Inventory

Cart quantity
→ Cart

Order current state
→ Order

Voucher quota/redemption
→ Voucher

Payment/refund current state
→ Payment
```

No other module should update those fields directly.

---

# 53. Cross-Domain Review Questions — Answers

## 1. Is `SKU.shop_id` consistent across Catalog / Inventory / Cart / Order?

```text
YES
```

provided all planned composite FKs are implemented.

Required chain:

```text
SKU
= InventoryStock
= CartItem
= SellerOrder/OrderItem Shop
```

---

## 2. Any FK allowing accidental cross-Shop attachment?

The intended Inventory, Cart and Order item composite FKs prevent the major cases.

```text
PASS
```

Migration must include supporting composite UNIQUE constraints first.

---

## 3. Can Parent and Seller Orders be misused in downstream FKs?

```text
NO — the finalized design closes this risk.
```

OrderItems are safe through Shop composite FK.

`orders.parent_order_id`, VoucherUsage and PaymentTransaction use the
**RESOLUTION-01** composite Parent identities.

---

## 4. Does VoucherUsage reference correct Parent Order?

Yes, both semantically and structurally.

```text
PASS
```

The Parent-safe FK enforces user/checkout/currency consistency.

---

## 5. Does Payment reference correct Parent Order?

Yes. The Parent-safe composite reference enforces Parent type and currency.

---

## 6. Can currency conflict from Shop → SKU → Order → Voucher → Payment?

```text
NO for a valid Checkout.
```

RESOLUTION-03 requires:

```text
one Checkout hierarchy = one currency
```

---

## 7. Any domain still using FLOAT for money?

```text
NO
```

Use:

```text
BIGINT minor units
```

Percentage:

```text
basis points
```

---

## 8. Any business history ON DELETE CASCADE?

None should be in the finalized commerce design.

All commercial/audit paths must use:

```text
ON DELETE RESTRICT
```

Verify migration DDL before merge.

---

## 9. Any duplicated source of truth?

No problematic duplication identified.

Historical snapshots/counters are intentional.

Each mutable domain has one logical owner.

---

## 10. Any retry still able to create duplicate business effect?

The major paths are protected if all unique constraints and state transitions are implemented.

Remaining correctness depends on:

```text
transaction ordering
provider idempotency support
future Outbox/event idempotency
```

No schema-level duplicate path is intentionally left open after the blockers are resolved.

---

## 11. Do TTLs use deadline rather than worker timing?

```text
YES
```

for Inventory and Voucher.

Commit must validate wall-clock deadline even before cleanup worker runs.

---

## 12. Any clearly redundant/missing indexes?

Yes:

```text
avoid duplicate prefix indexes already covered by UNIQUE
```

and ensure the missing critical indexes listed in Sections 48–50 are present.

---

# 54. Resolved Pre-Migration Correction Checklist

Encoded in the DATA design specifications:

```text
[x] Add strong Parent-safe self-FK for orders.parent_order_id.

[x] Enforce SellerOrder.user_id = ParentOrder.user_id.

[x] Enforce SellerOrder.currency = ParentOrder.currency.

[x] Add strong Parent-safe FK for VoucherUsage.

[x] Add strong Parent-safe FK for PaymentTransaction.

[x] Enforce VoucherUsage.user_id
    equals committed ParentOrder.user_id.

[x] Enforce VoucherUsage.checkout_reference_id and currency_code
    equals committed ParentOrder.checkout_reference_id.

[x] Define one Checkout/Parent hierarchy = one currency.

[x] Reject mixed-currency selected CartItems in V1.

[x] Standardize InventoryReservation:
    reference_type='checkout'
    reference_id=checkout_reference_id.

[x] Confirm skus UNIQUE(id, shop_id) and SKU/Variant chain keys.

[x] Confirm warehouses UNIQUE(id, shop_id).

[x] Confirm orders UNIQUE(id, shop_id).

[x] Confirm the exact supporting composite UNIQUE keys
    required by the Order self/Voucher/Payment FKs.

[x] Confirm payment_refunds provider is scoped with
    (provider, provider_refund_id).

[x] Confirm PaymentRefund.provider and currency_code =
    PaymentTransaction.provider.

[x] Confirm voucher reserved-expiry partial index.

[x] Confirm one-live-payment partial unique index.

[x] Specify RESTRICT for all historical FK paths.

[x] Verify no FLOAT/REAL/DOUBLE money columns.

[x] Define partial checkout Cart cleanup and MAX_CART_ITEM_QUANTITY = 99.

[x] Standardize Order initial state as awaiting_payment.

[x] Commit Voucher/Inventory only on timely PaymentSucceeded; reconcile/refund
    late success.

[x] Enforce Payment/Refund lifecycle timestamps and Refund currency equality.
```

---

# 55. Migration Dependency Order

Recommended PostgreSQL migration order:

```text
001 Identity
    users
    credentials
    sessions
    user_addresses

002 Seller
    seller_accounts
    shops
    shop_memberships

003 Catalog
    categories
    brands
    products
    product_variants
    skus

004 Inventory
    warehouses
    inventory_stocks
    inventory_reservations
    inventory_reservation_items
    stock_movements

005 Cart
    carts
    cart_items

006 Order
    orders
    order_items
    order_status_histories

007 Voucher
    vouchers
    voucher_usages

008 Payment
    payment_transactions
    payment_refunds
    payment_webhook_events
```

Supporting UNIQUE constraints must be created before child composite FKs.

---

# 56. Implementation Phase After PASS

After DATA-003G PASS, stop the design-only sequence.

Next pipeline:

```text
ERD approved
    │
    ▼
PostgreSQL migrations
    │
    ▼
schema validation
    │
    ▼
sqlc schema + queries
    │
    ▼
repositories/services
    │
    ▼
integration tests
    │
    ▼
concurrency/idempotency tests
```

Do not implement all modules at once without verifying each migration dependency.

---

# 57. Integration Tests Required Across Domains

At minimum:

```text
Cross-Shop FK rejection

Cart → same Checkout retry
→ one Parent hierarchy

Voucher checkout retry
→ one usage allocation

Inventory checkout retry
→ one stock reservation

Expired Inventory hold
→ cannot commit

Expired Voucher hold
→ cannot commit

Wrong Voucher user/order correlation
→ reject

Payment pointing Seller Order
→ reject

Voucher usage pointing Seller Order
→ reject

Concurrent Payment attempts
→ max one live attempt

Duplicate webhook
→ one logical side effect

Concurrent refunds
→ never over-refund

Mixed-currency checkout
→ reject/partition according to V1 rule

Historical Order after User/Shop/SKU mutation
→ T1 representation unchanged
```

---

# 58. Full Source of Truth Matrix

| Concern | Source of Truth |
|---|---|
| Authentication identity | Identity / `credentials`, `sessions` |
| User profile | Identity / `users` |
| User addresses | Identity / `user_addresses` |
| Seller business account | Seller / `seller_accounts` |
| Shop ownership / membership | Seller / `shop_memberships` |
| Shop current state | Seller / `shops` |
| Category hierarchy | Catalog / `categories` |
| Brand current metadata | Catalog / `brands` |
| Product current metadata | Catalog / `products` |
| Variant current metadata | Catalog / `product_variants` |
| Current sellable SKU / price | Catalog / `skus` |
| Current physical stock | Inventory / `inventory_stocks` |
| Temporary stock holds | Inventory / `inventory_reservations` |
| Physical stock audit | Inventory / `stock_movements` |
| Purchase intent | Cart / `carts`, `cart_items` |
| Commercial transaction | Order / `orders`, `order_items` |
| Order state audit | Order / `order_status_histories` |
| Voucher definition | Voucher / `vouchers` |
| Voucher quota / redemption | Voucher / `voucher_usages` + allocation counter |
| Payment attempt state | Payment / `payment_transactions` |
| Provider webhook inbox/audit | Payment / `payment_webhook_events` |
| Refund state | Payment / `payment_refunds` |

Compact form:

```text
Authentication identity      → Identity
User profile                 → Identity
Shop ownership/membership    → Seller
Product/current SKU price    → Catalog
Current stock                → Inventory
Purchase intent              → Cart
Commercial transaction       → Order
Voucher quota/redemption     → Voucher
Payment/refund state         → Payment
```

---

# 59. Final Architecture Boundary

```text
IDENTITY
Who is the user?
        │
        ▼
SELLER
Which Shops may this seller operate?
        │
        ▼
CATALOG
What is being sold and at what current price?
        │
        ▼
INVENTORY
Where is it and how much is currently available?
        │
        ▼
CART
What does the user currently intend to buy?
        │
        ▼
CHECKOUT
What is valid right now?
        │
        ├── reserve Inventory
        ├── reserve Voucher
        ├── calculate commercial result
        ▼
ORDER
What commercial transaction was created?
        │
        ▼
PAYMENT
What is the financial/provider state of that transaction?
```

---

# 60. Final DATA-003G Decision

The domain design is coherent enough to leave design-only phase. The four
review findings are now encoded in DATA-003B → DATA-003F:

```text
1. Parent-safe Order references, including the orders self-FK,
   with Parent/Seller User + currency consistency.
2. VoucherUsage ↔ Parent User/Checkout consistency.
3. Single-currency Checkout hierarchy.
4. Standard Inventory checkout correlation.

Additional resolved corrections include partial Cart checkout, quantity cap 99,
`awaiting_payment` Order initialization, timely-payment hold commitment,
provider-scoped refund identity and Refund currency equality.
```

```text
DATA-003G = PASS

Next:
PostgreSQL migrations
→ sqlc
→ integration tests
→ concurrency tests
```
