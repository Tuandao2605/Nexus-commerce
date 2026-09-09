# ECOM-DATA-003F — Voucher + Payment Domain Model

> Status: FINAL DESIGN — reconciled with DATA-003G remediation  
> Scope: data/domain design only — **chưa viết migration, Go, sqlc hay RabbitMQ**.

---

# 1. Scope

DATA-003F thiết kế hai domain có yêu cầu concurrency và financial correctness cao:

```text
VOUCHER MODULE
├── vouchers
└── voucher_usages

PAYMENT MODULE
├── payment_transactions
├── payment_refunds
└── payment_webhook_events
```

Quyết định V1: **thêm `payment_webhook_events`**.

Lý do:

- webhook có thể duplicate;
- webhook có thể tới out-of-order;
- cần replay protection;
- cần audit provider events;
- cần tách `event identity` khỏi `payment current state`;
- cần đảm bảo một provider event chỉ tạo side effect logic một lần.

`payment_status_histories` chưa thêm ở V1. `payment_webhook_events` cung cấp audit cho inbound provider events, còn các thay đổi nội bộ vẫn được bảo vệ bởi state machine và timestamps trên `payment_transactions`. Nếu sau này cần audit đầy đủ mọi transition kể cả non-webhook, có thể thêm history table riêng.

---

# 2. Core Domain Boundaries

## Voucher

Voucher Module chịu trách nhiệm:

```text
voucher definition
eligibility
effective usability
discount calculation
usage reservation
usage commitment/release
global usage concurrency
per-user usage concurrency
voucher idempotency
```

Voucher không được mutate Order totals sau khi Order đã snapshot.

```text
Checkout
   │
   ├── validate voucher
   ├── calculate discount
   ├── reserve voucher usage
   │
   ▼
Order
   └── snapshot accepted discount
```

## Payment

Payment Module chịu trách nhiệm:

```text
payment attempts
provider correlation
payment state
webhook verification/deduplication metadata
refund allocation
refund state
reconciliation
```

Payment **không sở hữu `Order.status`**.

```text
Provider
   │
   ▼
Payment Module
   │
   │ PaymentSucceeded fact/event
   ▼
Order Module
   │
   ▼
Order policy transition
```

Không:

```text
Payment Repository
→ UPDATE orders.status
```

---

# PART A — VOUCHER

# 3. Voucher Scope Decision

V1 hỗ trợ cả:

```text
platform-wide
shop-specific
```

Model:

```text
scope = platform
shop_id = NULL
```

```text
scope = shop
shop_id = Shop A
```

Không tạo hai bảng voucher riêng.

Structural invariant:

```sql
CHECK (
    (scope = 'platform' AND shop_id IS NULL)
    OR
    (scope = 'shop' AND shop_id IS NOT NULL)
)
```

V1 không hỗ trợ một voucher áp dụng cho arbitrary set nhiều Shop. Nếu cần sau này, thêm mapping table thay vì làm `shop_id` ambiguous.

---

# 4. Percentage Representation

Không dùng FLOAT.

V1 dùng **basis points**:

```text
1 basis point = 0.01%
100 bp = 1%
1000 bp = 10%
10000 bp = 100%
```

Ưu điểm so với integer percentage:

- hỗ trợ 7.5%, 12.25%, ...;
- deterministic;
- integer arithmetic;
- tránh floating-point rounding.

`discount_value` được dùng theo `discount_type`:

```text
fixed_amount
→ discount_value = minor currency units

percentage
→ discount_value = basis points
```

Constraint:

```sql
CHECK (
    (
        discount_type = 'fixed_amount'
        AND discount_value > 0
    )
    OR
    (
        discount_type = 'percentage'
        AND discount_value > 0
        AND discount_value <= 10000
    )
)
```

---

# 5. Voucher Currency Decision

Money sử dụng:

```text
BIGINT minor units
+
currency_code CHAR(3)
```

V1 chọn rule đơn giản:

```text
Every voucher has exactly one currency.
```

Kể cả platform voucher.

Voucher chỉ áp dụng khi:

```text
checkout/order currency = voucher.currency_code
```

Điều này tránh ambiguity khi:

```text
minimum_order_amount
max_discount_amount
fixed_amount
```

được áp dụng trên nhiều currency.

Multi-currency platform voucher trong tương lai nên là nhiều voucher definitions hoặc một policy/model riêng.

---

# 6. Voucher Lifecycle

Persisted administrative status:

```text
draft
active
inactive
```

Không persist `expired` như source of truth.

Voucher effectively usable khi:

```text
status = active
AND starts_at <= current_time
AND (ends_at IS NULL OR ends_at > current_time)
```

`ends_at` là business deadline.

Background worker không quyết định voucher còn hạn.

Giống Inventory TTL:

```text
persisted status
+
temporal condition
=
effective state
```

---

# 7. TABLE — `vouchers`

**Owner:** Voucher Module

## Purpose

Lưu voucher definition, eligibility policy cơ bản và concurrency counters.

## Columns

| Column | PostgreSQL type | Null | Default | Constraints / Meaning |
|---|---|---:|---|---|
| `id` | UUID | NO | — | PK |
| `code` | VARCHAR(64) | NO | — | UNIQUE, nonblank |
| `scope` | VARCHAR(20) | NO | — | `platform`, `shop` |
| `shop_id` | UUID | YES | NULL | FK shops |
| `discount_type` | VARCHAR(20) | NO | — | `fixed_amount`, `percentage` |
| `discount_value` | BIGINT | NO | — | fixed minor units or basis points |
| `max_discount_amount` | BIGINT | YES | NULL | percentage cap |
| `minimum_order_amount` | BIGINT | NO | `0` | >= 0 |
| `currency_code` | CHAR(3) | NO | — | voucher currency |
| `usage_limit` | BIGINT | YES | NULL | NULL = unlimited |
| `usage_limit_per_user` | BIGINT | YES | NULL | NULL = unlimited |
| `allocated_usage_count` | BIGINT | NO | `0` | RESERVED + COMMITTED slots |
| `starts_at` | TIMESTAMPTZ | NO | — | |
| `ends_at` | TIMESTAMPTZ | YES | NULL | |
| `status` | VARCHAR(20) | NO | `'draft'` | lifecycle |
| `created_at` | TIMESTAMPTZ | NO | `now()` | |
| `updated_at` | TIMESTAMPTZ | NO | `now()` | |

## Primary Key

```sql
PRIMARY KEY (id)
```

## Foreign Key

```sql
FOREIGN KEY (shop_id)
REFERENCES shops(id)
ON DELETE RESTRICT
```

## UNIQUE

V1 voucher code namespace là global:

```sql
UNIQUE(code)
```

Code được canonicalize trước khi persist và DB enforce:

```sql
CHECK (
    code = upper(btrim(code))
    AND char_length(code) BETWEEN 1 AND 64
)
```

Do đó `SAVE10` không thể đồng thời tồn tại ở hai Shop.

Đây là lựa chọn UX/API đơn giản cho V1. Nếu sau này cần same code per Shop, đổi lookup identity thành `(scope, shop_id, code)`.

## CHECK Constraints

```sql
CHECK (scope IN ('platform', 'shop'))
```

```sql
CHECK (
    (scope = 'platform' AND shop_id IS NULL)
    OR
    (scope = 'shop' AND shop_id IS NOT NULL)
)
```

```sql
CHECK (
    discount_type IN (
        'fixed_amount',
        'percentage'
    )
)
```

```sql
CHECK (
    (
        discount_type = 'fixed_amount'
        AND discount_value > 0
    )
    OR
    (
        discount_type = 'percentage'
        AND discount_value > 0
        AND discount_value <= 10000
    )
)
```

Discount cap compatibility:

```sql
CHECK (
    (discount_type = 'fixed_amount' AND max_discount_amount IS NULL)
    OR
    (discount_type = 'percentage'
      AND (max_discount_amount IS NULL OR max_discount_amount >= 0))
)
```

Other checks:

```sql
CHECK (minimum_order_amount >= 0)
CHECK (usage_limit IS NULL OR usage_limit > 0)
CHECK (usage_limit_per_user IS NULL OR usage_limit_per_user > 0)
CHECK (allocated_usage_count >= 0)
CHECK (usage_limit IS NULL OR allocated_usage_count <= usage_limit)
CHECK (ends_at IS NULL OR ends_at > starts_at)
CHECK (status IN ('draft', 'active', 'inactive'))
CHECK (updated_at >= created_at)
```

## Indexes

```sql
CREATE INDEX idx_vouchers_shop_status
ON vouchers(shop_id, status)
WHERE scope = 'shop';
```

```sql
CREATE INDEX idx_vouchers_status_time
ON vouchers(status, starts_at, ends_at);
```

Code lookup được hỗ trợ bởi UNIQUE index.

## ON DELETE

```text
shops → vouchers = RESTRICT
```

Voucher đã tham gia commercial history không nên biến mất vì Shop bị xóa.

Shop lifecycle dùng archive/status.

---

# 8. Voucher Discount Semantics

Eligibility order:

```text
1. voucher exists
2. structural scope matches
3. effectively usable
4. currency matches
5. minimum order satisfied
6. user/global limits available
7. calculate discount
8. reserve usage
```

Fixed:

```text
discount =
min(discount_value, eligible_amount)
```

Percentage:

```text
raw =
eligible_amount * basis_points / 10000
```

Then:

```text
discount =
min(raw, max_discount_amount)
```

nếu cap tồn tại.

Rounding policy phải deterministic. V1 dùng integer minor units và round-down/truncate khi division tạo fraction nhỏ hơn minor unit.

Order snapshot là commercial truth sau khi Checkout chấp nhận discount.

---

# 9. Voucher Eligible Amount Semantics

`eligible_amount` depends on voucher scope.

This is mandatory because Cart is global multi-shop and Order creation splits commercial data by Seller.

Given:

```text
Global Cart
├── Shop A = 100k
└── Shop B = 900k

Checkout total = 1m
```

For a platform voucher:

```text
scope = platform
→ eligible base = eligible checkout-wide amount
```

For a Shop voucher:

```text
scope = shop
shop_id = Shop A
→ eligible base = eligible amount belonging only to Shop A
```

Example:

```text
Shop A eligible amount = 100k
Voucher minimum_order_amount = 500k
```

Expected:

```text
reject
```

Even if:

```text
global checkout total = 1m
```

Never use Parent/global amount to satisfy a Shop voucher minimum.

After Checkout groups Cart by seller:

```text
Parent Checkout
├── Seller Group A
└── Seller Group B
```

Shop voucher calculation must operate only on the matching Seller Group.

Required rules:

```text
scope = platform
→ minimum threshold and discount base use checkout-wide eligible amount
```

```text
scope = shop
→ minimum threshold and discount base use only voucher.shop_id eligible amount
```

The definition of "eligible" may later exclude categories/items according to richer targeting rules, but **scope partitioning happens before minimum/discount calculation**.

---

# 10. Voucher Usage Semantic

Không tạo usage khi user chỉ nhập code hoặc preview voucher.

Phân biệt:

```text
VALIDATION
!=
RESERVATION
!=
COMMITMENT
```

State machine:

```text
RESERVED
   │
   ├────► COMMITTED
   ├────► RELEASED
   └────► EXPIRED
```

`reserved` giữ một usage slot trong lúc Checkout đang tiến hành.

`committed` nghĩa là usage đã trở thành commercial redemption gắn với successful Order creation boundary theo Checkout orchestration.

`released` nghĩa là reservation được business flow chủ động giải phóng trước deadline.

`expired` nghĩa là reservation vượt business deadline và quota phải được thu hồi.

Temporal validity:

```text
status = reserved
AND expires_at > wall-clock now
→ effectively reserved / consumes quota
```

```text
status = reserved
AND expires_at <= wall-clock now
→ logically expired
→ không còn hợp lệ cho commit
→ worker/inline cleanup phải chuyển sang EXPIRED và release counter
```

Core rule:

```text
business deadline
!=
worker execution time
```

Terminal:

```text
committed
released
expired
```

V1 giữ usage ở `reserved` trong lúc Order chờ thanh toán. PaymentSucceeded hợp
lệ trước hold deadline là trigger orchestration để Voucher Module commit usage
vào Parent Order bằng command an toàn/idempotent.

---

# 10.1 TABLE — `voucher_usages`

**Owner:** Voucher Module

## Purpose

Lưu một logical voucher redemption attempt đã được reserve, rồi commit hoặc release.

## Columns

| Column | PostgreSQL type | Null | Default | Constraints / Meaning |
|---|---|---:|---|---|
| `id` | UUID | NO | — | PK |
| `voucher_id` | UUID | NO | — | FK |
| `user_id` | UUID | NO | — | FK |
| `checkout_reference_id` | UUID | NO | — | business correlation |
| `order_id` | UUID | YES | NULL | Parent Order after commit |
| `parent_order_type` | VARCHAR(20) | YES | NULL | composite Parent discriminator |
| `discount_amount` | BIGINT | NO | — | accepted discount snapshot |
| `currency_code` | CHAR(3) | NO | — | snapshot |
| `status` | VARCHAR(20) | NO | `'reserved'` | state |
| `expires_at` | TIMESTAMPTZ | NO | — | reservation business deadline |
| `created_at` | TIMESTAMPTZ | NO | `now()` | |
| `updated_at` | TIMESTAMPTZ | NO | `now()` | |
| `committed_at` | TIMESTAMPTZ | YES | NULL | |
| `released_at` | TIMESTAMPTZ | YES | NULL | |
| `expired_at` | TIMESTAMPTZ | YES | NULL | |

## FKs

```sql
FOREIGN KEY (voucher_id)
REFERENCES vouchers(id)
ON DELETE RESTRICT
```

```sql
FOREIGN KEY (user_id)
REFERENCES users(id)
ON DELETE RESTRICT
```

```sql
FOREIGN KEY (
    order_id,
    user_id,
    checkout_reference_id,
    currency_code,
    parent_order_type
)
REFERENCES orders(
    id,
    user_id,
    checkout_reference_id,
    currency,
    order_type
)
ON DELETE RESTRICT
```

Supporting key trên Order:

```sql
UNIQUE (id, user_id, checkout_reference_id, currency, order_type)
```

Composite FK buộc VoucherUsage commit vào đúng Parent Order của cùng User,
Checkout và currency.

## Idempotency UNIQUE

```sql
UNIQUE(voucher_id, checkout_reference_id)
```

Invariant:

```text
same checkout
+ same voucher
→ one logical usage
```

Retry không allocate thêm quota.

## CHECK

```sql
CHECK (discount_amount >= 0)
```

```sql
CHECK (
    status IN (
        'reserved',
        'committed',
        'released',
        'expired'
    )
)
```

Timestamp-state consistency:

```sql
CHECK (
    (
        status = 'reserved'
        AND order_id IS NULL
        AND parent_order_type IS NULL
        AND committed_at IS NULL
        AND released_at IS NULL
        AND expired_at IS NULL
    )
    OR
    (
        status = 'committed'
        AND order_id IS NOT NULL
        AND parent_order_type = 'parent'
        AND committed_at IS NOT NULL
        AND released_at IS NULL
        AND expired_at IS NULL
    )
    OR
    (
        status = 'released'
        AND order_id IS NULL
        AND parent_order_type IS NULL
        AND committed_at IS NULL
        AND released_at IS NOT NULL
        AND expired_at IS NULL
    )
    OR
    (
        status = 'expired'
        AND order_id IS NULL
        AND parent_order_type IS NULL
        AND committed_at IS NULL
        AND released_at IS NULL
        AND expired_at IS NOT NULL
    )
)
```

```sql
CHECK (updated_at >= created_at)
```

```sql
CHECK (expires_at > created_at)
CHECK (committed_at IS NULL OR committed_at >= created_at)
CHECK (released_at IS NULL OR released_at >= created_at)
CHECK (expired_at IS NULL OR expired_at >= expires_at)
CHECK (
    currency_code = upper(currency_code)
    AND char_length(currency_code) = 3
)
```

## Indexes

```sql
CREATE INDEX idx_voucher_usages_voucher_status
ON voucher_usages(voucher_id, status);
```

```sql
CREATE INDEX idx_voucher_usages_user_voucher
ON voucher_usages(user_id, voucher_id);
```

```sql
CREATE INDEX idx_voucher_usages_order
ON voucher_usages(order_id)
WHERE order_id IS NOT NULL;
```

Expiration worker:

```sql
CREATE INDEX idx_voucher_usages_reserved_expires
ON voucher_usages(expires_at)
WHERE status = 'reserved';
```

## ON DELETE

All historical references use `RESTRICT`.

---

# 11. Global Usage Limit Concurrency Strategy

Không dùng:

```text
SELECT COUNT(*)
if count < limit
    INSERT
```

vì concurrent transactions có thể cùng thấy slot cuối.

V1 chọn **atomic counter trên `vouchers`**:

```text
allocated_usage_count
=
reserved + committed logical usage slots
```

Reserve conceptually:

```sql
UPDATE vouchers
SET
    allocated_usage_count = allocated_usage_count + 1,
    updated_at = now()
WHERE id = $1
  AND status = 'active'
  AND starts_at <= clock_timestamp()
  AND (ends_at IS NULL OR ends_at > clock_timestamp())
  AND (
      usage_limit IS NULL
      OR allocated_usage_count < usage_limit
  );
```

Rows affected:

```text
1 → global slot allocated
0 → unavailable / limit reached / not effectively active
```

`clock_timestamp()` được dùng cho strict wall-clock eligibility thay vì dựa vào transaction-start semantics của PostgreSQL `now()`.

Reservation row insert và counter increment phải ở **cùng transaction**.

Nếu insert usage fail:

```text
ROLLBACK
```

counter increment cũng rollback.

---

# 12. Why Counter Instead of COUNT + Lock?

Option:

```text
SELECT voucher FOR UPDATE
COUNT usages
INSERT
```

correct nếu serialized đầy đủ, nhưng:

- count ngày càng đắt;
- lock duration dài hơn;
- source of quota availability phụ thuộc aggregate scan.

Atomic counter:

- O(1);
- row update tự serialize trên voucher row;
- predicate kiểm tra limit cùng mutation;
- dễ reason under concurrency.

Trade-off:

```text
voucher row becomes contention point
```

cho hot voucher.

Đây là acceptable V1 correctness-first design.

Scale lớn hơn có thể partition quota/bucket hoặc dùng allocation architecture khác.

---

# 13. Per-User Usage Limit Concurrency

`usage_limit_per_user` có thể lớn hơn 1, vì vậy partial unique:

```text
UNIQUE(voucher_id, user_id)
WHERE status IN (...)
```

chỉ giải quyết chính xác trường hợp limit = 1, không giải quyết generic N.

V1 chọn một **transaction-scoped PostgreSQL advisory lock** theo logical key:

```text
(voucher_id, user_id)
```

trước khi kiểm tra active allocation của user.

Conceptual transaction:

```text
BEGIN

1. acquire advisory xact lock(voucher_id, user_id)

2. check existing usage for same
   (voucher_id, checkout_reference_id)
   → if exists, return idempotently

3. count user's RESERVED + COMMITTED usages

4. reject if count >= usage_limit_per_user

5. atomic UPDATE vouchers to allocate global slot

6. INSERT voucher_usages RESERVED

COMMIT
```

Vì mọi reserve cho cùng `(voucher_id,user_id)` phải dùng cùng advisory-lock discipline, concurrent requests của cùng user serialize.

Global users vẫn có thể chạy concurrent, chỉ contention ở voucher counter row khi allocate global quota.

Nếu muốn tránh advisory lock trong future schema, có thể thêm dedicated `voucher_user_counters` table. Không cần thêm bảng đó ở V1.

---

# 14. Release Voucher Usage

Release chỉ từ:

```text
reserved → released
```

Transaction:

```text
lock usage
verify status = reserved

decrement vouchers.allocated_usage_count

mark usage released
```

Counter decrement và status transition phải cùng transaction.

Repeated release:

```text
released → no-op/idempotent result
```

Không decrement lần hai.

Committed usage không release quota.

---

# 15. Expire Voucher Usage

Voucher reservation phải có TTL/recovery để tránh quota leakage nếu process crash sau khi reservation transaction đã commit.

Example failure:

```text
ReserveVoucher()
↓
allocated_usage_count += 1
↓
voucher_usage = RESERVED
↓
process crash
```

Nếu không có expiry:

```text
usage_limit = 100

80 committed
20 zombie RESERVED
──────────────────
allocated = 100
```

voucher có thể bị block vĩnh viễn.

Required invariant:

```text
RESERVED + expires_at > wall-clock now
→ effectively reserved
```

```text
RESERVED + expires_at <= wall-clock now
→ logically expired
→ commit phải reject
→ cleanup eventually releases quota
```

Conceptual expiration transaction:

```text
BEGIN

lock voucher_usage

require:
status = reserved
expires_at <= clock_timestamp()

decrement vouchers.allocated_usage_count

status = expired
expired_at = clock_timestamp()
updated_at = clock_timestamp()

COMMIT
```

Repeated expiration phải idempotent và không decrement counter lần hai.

Worker discovery:

```sql
SELECT id
FROM voucher_usages
WHERE status = 'reserved'
  AND expires_at <= clock_timestamp()
ORDER BY expires_at
FOR UPDATE SKIP LOCKED
LIMIT 100;
```

Multiple workers có thể chạy an toàn với `SKIP LOCKED`.

Commit phải require:

```text
status = reserved
AND expires_at > wall-clock now
```

Nếu deadline đã qua nhưng worker chưa chạy:

```text
commit rejects
```

vì deadline, không phải worker, quyết định business validity.

Trong khoảng cleanup chưa chạy, expired logical usage vẫn có thể giữ
`allocated_usage_count` và tạm thời chặn quota mới. Đây là conservative blocking,
không phải over-allocation. Worker phải có SLO/alert rõ ràng; reserve path nên
opportunistically expire usage hết hạn của cùng voucher/user trước khi báo
`limit reached`.

---

# 16. Commit Voucher Usage

Transition:

```text
reserved → committed
```

Không thay đổi:

```text
allocated_usage_count
```

vì slot đã được giữ từ RESERVED.

Commit stores:

```text
order_id
committed_at
```

Commit chỉ được gọi khi:

```text
PaymentSucceeded
AND status = reserved
AND expires_at > wall-clock now
AND order_id is the correlated Parent Order
```

Payment failure còn trong retry window không commit hoặc release usage. Khi
checkout/payment deadline hết, usage chuyển `expired` (hoặc `released` nếu hủy
chủ động trước deadline). Late payment success sau deadline không được resurrect
usage; nó đi vào refund/reconciliation cùng policy của Payment.

Repeated commit phải idempotent.

`committed → released` không phải normal transition.

Nếu cần refund voucher/reissue policy, đó là domain ticket khác.

---

# 16.1 Voucher Idempotency

Key:

```text
voucher_id
+
checkout_reference_id
```

Same logical Checkout retry:

```text
ReserveVoucher(checkout-X)
ReserveVoucher(checkout-X)
```

returns same logical usage.

Quan trọng: kiểm tra idempotency **trước khi allocate quota mới**.

Nếu existing usage:

```text
reserved → return it
committed → return committed result
released → return released/conflict according to Checkout policy
```

Không increment counter lần hai.

---

# PART B — PAYMENT

# 17. Payment Attempt Model Decision

Một Parent Order có:

```text
1 → many PaymentTransactions
```

Không giới hạn một row/order.

Example:

```text
Parent P100
├── Attempt 1 — failed
├── Attempt 2 — timeout/failed
└── Attempt 3 — succeeded
```

Lý do:

- retry payment;
- payment-method switch;
- provider retry;
- preserve failed attempts;
- reconciliation/audit.

`payment_transactions` là **payment attempt records**, không phải một mutable payment slot duy nhất cho Order.

---

# 18. Live Payment Attempt Policy

Một Parent Order có thể có nhiều historical PaymentTransactions, nhưng V1 simple-capture model chỉ cho phép:

```text
maximum one economically live payment attempt per Parent Order
```

Economically live states:

```text
pending
processing
succeeded
```

Failed/cancelled attempts không block retry.

Required partial unique index:

```sql
CREATE UNIQUE INDEX uq_payment_parent_live_attempt
ON payment_transactions(parent_order_id)
WHERE status IN (
    'pending',
    'processing',
    'succeeded'
);
```

Meaning:

```text
Attempt 1 failed
→ Attempt 2 allowed
```

```text
Attempt 2 processing
→ another live attempt rejected
```

```text
Attempt 2 succeeded
→ future attempts blocked
```

This is intentionally strict for V1.

It prevents:

```text
Parent P100
├── Attempt A processing
└── Attempt B processing
```

which could otherwise become:

```text
Attempt A succeeded
Attempt B succeeded
→ double charge
```

If future architecture supports:

```text
split tender
gift card + card
multiple partial captures
```

this invariant must be redesigned rather than weakened ad hoc.

---

# 19. Payment State Machine

V1 mock/simple capture model:

```text
pending
processing
succeeded
failed
cancelled
```

Không thêm `authorized` ở V1.

Allowed transitions:

```text
pending → processing
pending → failed
pending → cancelled

processing → succeeded
processing → failed
processing → cancelled
```

Terminal:

```text
succeeded
failed
cancelled
```

Không:

```text
succeeded → processing
succeeded → failed
failed → succeeded
```

Nếu provider semantics sau này có recoverable/async failure khác, state machine phải được mở rộng explicit thay vì overwrite mù quáng.

---

# 19.1 TABLE — `payment_transactions`

**Owner:** Payment Module

## Purpose

Lưu một logical merchant payment attempt và correlation với provider.

## Columns

| Column | PostgreSQL type | Null | Default | Constraints / Meaning |
|---|---|---:|---|---|
| `id` | UUID | NO | — | PK |
| `parent_order_id` | UUID | NO | — | FK |
| `parent_order_type` | VARCHAR(20) | NO | — | composite Parent discriminator |
| `provider` | VARCHAR(40) | NO | — | provider identity |
| `provider_payment_id` | VARCHAR(191) | YES | NULL | provider transaction identity |
| `idempotency_key` | VARCHAR(128) | NO | — | merchant command identity |
| `request_hash` | CHAR(64) | NO | — | semantic request hash |
| `amount` | BIGINT | NO | — | payment snapshot |
| `currency_code` | CHAR(3) | NO | — | snapshot |
| `allocated_refund_amount` | BIGINT | NO | `0` | active/succeeded refund allocation |
| `status` | VARCHAR(20) | NO | `'pending'` | current state |
| `failure_code` | VARCHAR(100) | YES | NULL | |
| `failure_message` | VARCHAR(512) | YES | NULL | sanitized provider failure |
| `expires_at` | TIMESTAMPTZ | NO | — | payment/hold deadline |
| `created_at` | TIMESTAMPTZ | NO | `now()` | |
| `updated_at` | TIMESTAMPTZ | NO | `now()` | |
| `processing_at` | TIMESTAMPTZ | YES | NULL | |
| `succeeded_at` | TIMESTAMPTZ | YES | NULL | |
| `failed_at` | TIMESTAMPTZ | YES | NULL | |
| `cancelled_at` | TIMESTAMPTZ | YES | NULL | |

Không biến table thành arbitrary provider JSON dump.

## FK

```sql
FOREIGN KEY (
    parent_order_id,
    currency_code,
    parent_order_type
)
REFERENCES orders(id, currency, order_type)
ON DELETE RESTRICT
```

Supporting key trên Order:

```sql
UNIQUE (id, currency, order_type)
```

```sql
CHECK (parent_order_type = 'parent')
```

DB buộc Payment reference Parent Order có cùng currency.

## Merchant Idempotency

```sql
UNIQUE(idempotency_key)
```

Key này là internal server-generated command identity có operation namespace,
ví dụ `payment:create:<parent_order_id>:<attempt>`, không phải raw external HTTP
key. Nếu lưu raw client key, uniqueness phải scope theo actor + operation.

Same key + same semantic request:

```text
return same PaymentTransaction
```

Same key + different semantic request:

```text
IDEMPOTENCY_KEY_REUSED
```

`request_hash` canonicalizes tối thiểu:

```text
parent_order_id
provider
amount
currency
payment method semantic identity if applicable
```

Không hash timestamps/provider-generated IDs.

## Provider Identity

Provider payment ID chỉ unique trong provider namespace:

```sql
CREATE UNIQUE INDEX uq_payment_provider_payment
ON payment_transactions(provider, provider_payment_id)
WHERE provider_payment_id IS NOT NULL;
```

Live-attempt protection:

```sql
CREATE UNIQUE INDEX uq_payment_parent_live_attempt
ON payment_transactions(parent_order_id)
WHERE status IN ('pending', 'processing', 'succeeded');
```

Invariant:

```text
same provider payment
→ one local PaymentTransaction
```

## CHECK

```sql
CHECK (amount > 0)
```

```sql
CHECK (
    status IN (
        'pending',
        'processing',
        'succeeded',
        'failed',
        'cancelled'
    )
)
```

```sql
CHECK (updated_at >= created_at)
```

```sql
CHECK (expires_at > created_at)
CHECK (
    currency_code = upper(currency_code)
    AND char_length(currency_code) = 3
)
CHECK (char_length(request_hash) = 64)
CHECK (char_length(btrim(provider)) BETWEEN 1 AND 40)
CHECK (
    (status = 'pending'
      AND processing_at IS NULL AND succeeded_at IS NULL
      AND failed_at IS NULL AND cancelled_at IS NULL)
    OR
    (status = 'processing'
      AND processing_at IS NOT NULL AND succeeded_at IS NULL
      AND failed_at IS NULL AND cancelled_at IS NULL)
    OR
    (status = 'succeeded'
      AND processing_at IS NOT NULL AND succeeded_at IS NOT NULL
      AND failed_at IS NULL AND cancelled_at IS NULL)
    OR
    (status = 'failed'
      AND succeeded_at IS NULL AND failed_at IS NOT NULL
      AND cancelled_at IS NULL)
    OR
    (status = 'cancelled'
      AND succeeded_at IS NULL AND failed_at IS NULL
      AND cancelled_at IS NOT NULL)
)
CHECK (processing_at IS NULL OR processing_at >= created_at)
CHECK (succeeded_at IS NULL OR succeeded_at >= created_at)
CHECK (failed_at IS NULL OR failed_at >= created_at)
CHECK (cancelled_at IS NULL OR cancelled_at >= created_at)
```

## Indexes

```sql
CREATE INDEX idx_payment_parent_created
ON payment_transactions(parent_order_id, created_at DESC);
```

Reconciliation:

```sql
CREATE INDEX idx_payment_reconciliation
ON payment_transactions(status, created_at)
WHERE status IN ('pending', 'processing');
```

Provider lookup được unique index hỗ trợ.

## ON DELETE

```text
orders → payment_transactions = RESTRICT
```

Financial history không cascade delete.

---

# 20. Payment Amount Boundary

Khi create PaymentTransaction:

```text
payment.amount
=
authoritative Parent Order.total_amount
```

và:

```text
payment.currency_code
=
Parent Order.currency
```

PaymentTransaction vẫn snapshot:

```text
amount
currency
```

Lý do:

```text
Order commercial truth
!=
Payment financial record ownership
```

Payment history phải reconstructable độc lập.

`payment_transactions.expires_at` và provider payment URL/intent expiry không
được muộn hơn Inventory/Voucher hold deadline của cùng Checkout. Payment attempt
không được mở một capture window dài hơn resource validity window.

Nếu provider báo success sau deadline:

```text
record/deduplicate financial success
do not commit expired Inventory/Voucher holds
do not confirm or fulfill Order
create idempotent refund/reconciliation work item
cancel awaiting_payment Order hierarchy
alert if automated refund cannot be completed
```

Late success là financial exception cần audit, không phải lý do resurrect một
Checkout đã hết hạn.

---

# 21. Payment Creation Idempotency

Flow:

```text
CreatePayment(P100, key=X)
        │
        ▼
local PaymentTransaction
        │
        ▼
provider create payment
```

Nếu network timeout:

```text
retry key=X
```

phải resolve về same local logical attempt.

Provider request cũng phải dùng stable provider-supported idempotency identity khi provider hỗ trợ, để crash/timeout giữa local write và provider response không tạo double charge.

DATA-003F chỉ ghi architecture requirement; provider adapter implementation nằm ở ticket sau.

---

# 22. Payment Status Ownership

Current Payment status source of truth:

```text
payment_transactions.status
```

Owner:

```text
Payment Module
```

Order Module không mutate Payment status.

Payment Module không mutate Order status.

Communication:

```text
Payment fact/event
→ Order policy
```

---

# 23. Dedicated Webhook Event Table Decision

V1 **có `payment_webhook_events`**.

Chỉ current-state idempotency không đủ tốt vì:

- duplicate event có thể không đổi status nhưng vẫn trigger side effect;
- cần biết provider event nào đã nhận;
- cần replay protection;
- cần retry failed webhook processing;
- cần audit;
- cần distinguish two different events producing same target status.

Do đó:

```text
provider event identity
```

được persist riêng.

---

# 24. TABLE — `payment_webhook_events`

**Owner:** Payment Module

## Purpose

Durable inbox/deduplication record cho provider webhook.

## Columns

| Column | PostgreSQL type | Null | Default | Constraints / Meaning |
|---|---|---:|---|---|
| `id` | UUID | NO | — | PK |
| `provider` | VARCHAR(40) | NO | — | |
| `provider_event_id` | VARCHAR(191) | NO | — | provider event identity |
| `provider_payment_id` | VARCHAR(191) | YES | NULL | correlation |
| `payment_transaction_id` | UUID | YES | NULL | resolved local payment |
| `event_type` | VARCHAR(100) | NO | — | provider event kind |
| `payload_hash` | CHAR(64) | NO | — | SHA-256 canonical/raw-body hash policy |
| `processing_status` | VARCHAR(20) | NO | `'received'` | inbox lifecycle |
| `received_at` | TIMESTAMPTZ | NO | `now()` | |
| `processed_at` | TIMESTAMPTZ | YES | NULL | |
| `last_error` | VARCHAR(512) | YES | NULL | sanitized |

Không lưu:

```text
webhook secret
signature secret
private key
```

Raw payload storage không bắt buộc trong core table. Nếu compliance/debug yêu cầu, lưu ở controlled storage với retention/redaction policy riêng.

## UNIQUE

```sql
UNIQUE(provider, provider_event_id)
```

Đây là primary deduplication invariant.

## FK

To prevent a PaymentWebhookEvent from claiming the wrong provider, add
supporting identity on PaymentTransaction:

```sql
UNIQUE(id, provider)
```

Then use:

```sql
FOREIGN KEY (
    payment_transaction_id,
    provider
)
REFERENCES payment_transactions(
    id,
    provider
)
ON DELETE RESTRICT
```

Invariant:

```text
PaymentWebhookEvent.provider
=
PaymentTransaction.provider
```

## Processing Status

```text
received
processed
ignored
failed
```

```sql
CHECK (
    processing_status IN (
        'received',
        'processed',
        'ignored',
        'failed'
    )
)
```

`ignored` dùng cho authenticated/valid event không tạo transition vì stale/no-op/unsupported policy.

## Indexes

```sql
CREATE INDEX idx_payment_webhook_processing
ON payment_webhook_events(processing_status, received_at)
WHERE processing_status IN ('received', 'failed');
```

```sql
CREATE INDEX idx_payment_webhook_payment
ON payment_webhook_events(payment_transaction_id, received_at DESC)
WHERE payment_transaction_id IS NOT NULL;
```

## ON DELETE

```text
payment_transactions → webhook_events = RESTRICT
```

---

# 25. Webhook Processing Pipeline

Required order:

```text
receive raw request
↓
verify provider signature
↓
validate timestamp tolerance
↓
extract provider_event_id
↓
durably deduplicate event
↓
resolve PaymentTransaction
↓
lock PaymentTransaction
↓
validate allowed state transition
↓
apply current-state mutation if valid
↓
persist webhook processed/ignored state
↓
commit
↓
emit/dispatch logical side effect once
```

Khi Payment-to-Order communication chạy async, Payment state mutation và owner
module outbox event phải commit trong cùng transaction. Consumer Order dùng
durable event identity/inbox để duplicate delivery không lặp transition.

Trong V1 modular monolith dùng chung PostgreSQL, consumer finalizes Inventory
hold, VoucherUsage và Order hierarchy trong **một local DB transaction**, qua
owner-module methods nhận shared transaction handle. Điều này tránh trạng thái
Inventory committed nhưng Voucher/Order chưa commit (hoặc ngược lại). Provider
network call không bao giờ nằm trong transaction này.

Important:

```text
duplicate delivery
!=
new logical payment event
```

---

# 26. Webhook Signature and Replay Protection

Before trusting event content:

```text
signature verification
+
timestamp tolerance
```

Then persistent replay/dedup:

```text
UNIQUE(provider, provider_event_id)
```

`payload_hash` helps detect anomalous case:

```text
same provider_event_id
+
different payload
```

This should be treated as suspicious/provider inconsistency and must not silently process as a new event.

No secrets are persisted in webhook event rows.

---

# 27. Duplicate Webhook

Example:

```text
payment_succeeded(pay_123)
```

arrives 100 times.

Expected:

```text
one payment transition
one logical success side effect
one provider event identity
duplicates become no-op/dedup result
```

Unique `(provider, provider_event_id)` is final DB protection.

---

# 28. Out-of-Order Webhook

Example:

```text
SUCCESS processed
then delayed PROCESSING arrives
```

Payment is already:

```text
succeeded
```

State machine rejects:

```text
succeeded → processing
```

Delayed event may be recorded:

```text
processing_status = ignored
```

Payment remains succeeded.

Never:

```text
status = incoming_event_status
```

without transition validation.

---

# 29. Side-Effect Exactly-Once Semantics

Database can provide:

```text
durable deduplication
+
atomic state transition
```

External message delivery is not magically exactly-once.

When the asynchronous Payment boundary is implemented, it must use an Outbox
pattern to publish:

```text
PaymentSucceeded
PaymentFailed
RefundSucceeded
```

reliably.

DATA-003F invariant:

```text
one logical provider event
→ at most one accepted local state transition
```

and event dispatch must be idempotent/outbox-backed. DATA-003F specifies this
reliability gate but does not add the outbox migration in this design ticket.

---

# 30. Reconciliation

Local state có thể lệch provider do:

```text
webhook lost
process crash
network timeout
provider timeout
```

Future reconciliation job queries:

```text
pending/processing
older than threshold
```

using:

```text
idx_payment_reconciliation(status, created_at)
```

Then provider API is queried and result still passes the same Payment state machine.

Reconciliation không bypass transition rules.

---

# PART C — REFUND

# 31. Refund Model

Một succeeded Payment có:

```text
1 → many refunds
```

Partial refunds supported.

Example:

```text
Payment = 500000

Refund R1 = 100000
Refund R2 = 200000

total allocated/refunded = 300000
```

Must never allocate beyond original Payment amount.

---

# 32. Refund State Machine

V1:

```text
pending
processing
succeeded
failed
cancelled
```

Allowed:

```text
pending → processing
pending → failed
pending → cancelled

processing → succeeded
processing → failed
processing → cancelled
```

Terminal:

```text
succeeded
failed
cancelled
```

A refund allocation consumes refundable capacity while:

```text
pending
processing
succeeded
```

Failed/cancelled releases its allocation.

---

# 33. Refund Concurrency Strategy

Cross-row:

```text
SUM(active refund allocations)
<= payment amount
```

khó enforce bằng simple CHECK.

V1 tránh concurrent SUM bằng **refund allocation counter trên `payment_transactions`**.

Bổ sung required column vào `payment_transactions`:

```text
allocated_refund_amount BIGINT NOT NULL DEFAULT 0
```

Meaning:

```text
sum of refund amounts currently allocated by
pending + processing + succeeded refunds
```

Constraint:

```sql
CHECK (allocated_refund_amount >= 0)
CHECK (allocated_refund_amount <= amount)
```

Atomic allocation:

```sql
UPDATE payment_transactions
SET
    allocated_refund_amount = allocated_refund_amount + $refund_amount,
    updated_at = now()
WHERE id = $payment_id
  AND status = 'succeeded'
  AND allocated_refund_amount + $refund_amount <= amount;
```

Rows affected:

```text
1 → refundable capacity reserved
0 → invalid payment / insufficient refundable capacity
```

Refund row insert và allocation increment cùng transaction.

Nếu insert fail:

```text
ROLLBACK
```

allocation rollback.

---

# 34. Why Count Pending Refunds?

Nếu chỉ count successful refunds:

```text
remaining = 100
```

Concurrent:

```text
Refund A requests 100
Refund B requests 100
```

cả hai có thể gửi request ra provider trước khi một cái succeeded.

Do đó capacity phải được giữ ngay từ refund request acceptance:

```text
pending/processing
=
allocated
```

Nếu refund fails/cancels:

```text
release allocation
```

Nếu succeeds:

```text
keep allocation permanently consumed
```

---

# 35. TABLE — `payment_refunds`

**Owner:** Payment Module

## Purpose

Lưu từng logical full/partial refund attempt.

## Columns

| Column | PostgreSQL type | Null | Default | Constraints / Meaning |
|---|---|---:|---|---|
| `id` | UUID | NO | — | PK |
| `payment_transaction_id` | UUID | NO | — | composite FK |
| `provider` | VARCHAR(40) | NO | — | must match PaymentTransaction.provider |
| `provider_refund_id` | VARCHAR(191) | YES | NULL | provider-scoped identity |
| `idempotency_key` | VARCHAR(128) | NO | — | command identity |
| `request_hash` | CHAR(64) | NO | — | semantic request hash |
| `amount` | BIGINT | NO | — | refund minor units |
| `currency_code` | CHAR(3) | NO | — | snapshot |
| `status` | VARCHAR(20) | NO | `'pending'` | |
| `reason` | VARCHAR(512) | YES | NULL | |
| `failure_code` | VARCHAR(100) | YES | NULL | |
| `failure_message` | VARCHAR(512) | YES | NULL | |
| `created_at` | TIMESTAMPTZ | NO | `now()` | |
| `updated_at` | TIMESTAMPTZ | NO | `now()` | |
| `processing_at` | TIMESTAMPTZ | YES | NULL | |
| `succeeded_at` | TIMESTAMPTZ | YES | NULL | |
| `failed_at` | TIMESTAMPTZ | YES | NULL | |
| `cancelled_at` | TIMESTAMPTZ | YES | NULL | |

## FK

To prevent a Refund from claiming the wrong provider or currency, add supporting
identity on PaymentTransaction:

```sql
UNIQUE(id, provider, currency_code)
```

Then use:

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
ON DELETE RESTRICT
```

Invariant:

```text
PaymentRefund.provider
=
PaymentTransaction.provider

PaymentRefund.currency_code
=
PaymentTransaction.currency_code
```

## Idempotency

```sql
UNIQUE(idempotency_key)
```

Đây là internal server-generated, operation-namespaced command key; không dùng
raw external `Idempotency-Key` làm global identity.

Same key + same request:

```text
same refund
```

Same key + different request:

```text
IDEMPOTENCY_KEY_REUSED
```

`request_hash` canonicalizes:

```text
payment_transaction_id
amount
currency
reason_code if semantically relevant
```

Do not hash generated IDs/timestamps.

## Provider Identity

```sql
CREATE UNIQUE INDEX uq_refund_provider_refund
ON payment_refunds(provider, provider_refund_id)
WHERE provider_refund_id IS NOT NULL;
```

Refund persists `provider` and `currency_code`; the composite FK proves both
values match its PaymentTransaction. Provider refund identity remains scoped by
`(provider, provider_refund_id)`.

## CHECK

```sql
CHECK (amount > 0)
```

```sql
CHECK (
    status IN (
        'pending',
        'processing',
        'succeeded',
        'failed',
        'cancelled'
    )
)
```

```sql
CHECK (updated_at >= created_at)
```

```sql
CHECK (
    currency_code = upper(currency_code)
    AND char_length(currency_code) = 3
)
CHECK (char_length(request_hash) = 64)
CHECK (
    (status = 'pending'
      AND processing_at IS NULL AND succeeded_at IS NULL
      AND failed_at IS NULL AND cancelled_at IS NULL)
    OR
    (status = 'processing'
      AND processing_at IS NOT NULL AND succeeded_at IS NULL
      AND failed_at IS NULL AND cancelled_at IS NULL)
    OR
    (status = 'succeeded'
      AND processing_at IS NOT NULL AND succeeded_at IS NOT NULL
      AND failed_at IS NULL AND cancelled_at IS NULL)
    OR
    (status = 'failed'
      AND succeeded_at IS NULL AND failed_at IS NOT NULL AND cancelled_at IS NULL)
    OR
    (status = 'cancelled'
      AND succeeded_at IS NULL AND failed_at IS NULL AND cancelled_at IS NOT NULL)
)
CHECK (processing_at IS NULL OR processing_at >= created_at)
CHECK (succeeded_at IS NULL OR succeeded_at >= created_at)
CHECK (failed_at IS NULL OR failed_at >= created_at)
CHECK (cancelled_at IS NULL OR cancelled_at >= created_at)
```

## Indexes

```sql
CREATE INDEX idx_payment_refunds_payment_created
ON payment_refunds(payment_transaction_id, created_at DESC);
```

```sql
CREATE INDEX idx_payment_refunds_status_created
ON payment_refunds(status, created_at)
WHERE status IN ('pending', 'processing');
```

## ON DELETE

```text
payment_transactions → refunds = RESTRICT
```

---

# 36. Refund Allocation Release

If:

```text
pending/processing → failed
```

or:

```text
pending/processing → cancelled
```

then in same transaction:

```text
payment_transactions.allocated_refund_amount -= refund.amount
```

Repeated failed/cancelled webhook/command must not decrement twice.

If:

```text
processing → succeeded
```

allocation stays consumed.

---

# 37. Refund Idempotency Flow

Request:

```text
Refund(
  payment=P1,
  amount=100,
  key=R1
)
```

Before allocating new refundable capacity:

```text
lookup/attempt idempotency identity
```

If same key exists:

```text
same request_hash
→ return existing refund

different request_hash
→ IDEMPOTENCY_KEY_REUSED
```

Only new logical refund performs atomic allocation.

This prevents retry from consuming capacity twice.

---

# 38. Payment/Refund Financial Immutability

Financial records are historical.

Normal application does not:

```text
DELETE succeeded payment
DELETE succeeded refund
rewrite amount/currency
```

Corrections happen through explicit compensating financial workflows, not silent mutation.

---

# 39. Payment Current State vs Audit

V1 stores:

```text
payment_transactions.status
=
current payment state
```

and:

```text
payment_webhook_events
=
provider inbound event audit/dedup inbox
```

Trade-off of not adding `payment_status_histories`:

### Advantages

- fewer tables;
- enough for V1 provider webhook debugging;
- current-state query simple.

### Limitation

Internal transitions not caused by webhook are not represented as a dedicated transition ledger.

Decision:

```text
do not add payment_status_histories in DATA-003F V1
```

Revisit when financial audit requirements become stricter.

---

# 40. Validation vs Reservation vs Commitment vs History

Always distinguish:

```text
VALIDATION
RESERVATION
COMMITMENT
HISTORICAL RECORD
```

Voucher:

```text
validate voucher
!=
reserve quota
!=
commit usage
```

Payment:

```text
create payment attempt
!=
payment succeeded
```

Refund:

```text
request/allocate refund
!=
refund succeeded
```

Order:

```text
commercial snapshot
!=
Payment provider settlement state
```

---

# 41. Required Architecture Questions — Final Answers

## 1. Voucher global hay per-Shop?

```text
Both.
```

`scope=platform → shop_id=NULL`.

`scope=shop → shop_id NOT NULL`.

---

## 2. Percentage lưu bằng kiểu gì?

```text
BIGINT basis points.
```

```text
10% = 1000 bp
7.5% = 750 bp
```

No FLOAT.

---

## 3. Voucher effective-active tính thế nào?

```text
status = active
AND starts_at <= wall-clock now
AND (ends_at IS NULL OR ends_at > wall-clock now)
```

Expiry derived temporally, không phụ thuộc worker.

---

## 4. Usage tính lúc validate hay reserve/commit?

Validation không consume quota.

Quota được allocate khi usage chuyển vào:

```text
RESERVED
```

Commit giữ allocation.

Release trả allocation.

---

## 5. Global usage limit chống concurrency thế nào?

Atomic conditional mutation:

```text
vouchers.allocated_usage_count
```

Không `SELECT COUNT → INSERT`.

---

## 6. Per-user usage limit chống concurrency thế nào?

Transaction-scoped advisory lock theo:

```text
(voucher_id, user_id)
```

sau đó count active allocations và reserve trong cùng transaction.

Future alternative: dedicated per-user counter table.

---

## 7. Retry Checkout có consume voucher hai lần không?

Không.

```text
UNIQUE(voucher_id, checkout_reference_id)
```

và idempotency được kiểm tra trước quota allocation.

---

## 8. Một Parent Order có bao nhiêu PaymentTransaction?

```text
1 → many attempts
```

Failed/retried attempts được giữ.

---

## 9. Payment status owner là module nào?

```text
Payment Module.
```

Payment không sở hữu Order status.

---

## 10. `provider_payment_id` unique scope thế nào?

```text
UNIQUE(provider, provider_payment_id)
```

khi provider ID tồn tại.

---

## 11. Duplicate webhook xử lý thế nào?

Dedicated inbox:

```text
payment_webhook_events
UNIQUE(provider, provider_event_id)
```

plus state-machine idempotency.

---

## 12. Out-of-order webhook xử lý thế nào?

Webhook không overwrite status trực tiếp.

```text
verify
→ dedupe
→ lock payment
→ validate transition
→ apply or ignore
```

Terminal state không regress.

---

## 13. Webhook replay protection ở đâu?

Hai layer:

```text
signature + timestamp tolerance
```

và durable:

```text
UNIQUE(provider, provider_event_id)
```

`payload_hash` hỗ trợ anomaly detection.

---

## 14. Partial refund model thế nào?

```text
PaymentTransaction 1 → many PaymentRefunds
```

Mỗi refund có amount riêng.

---

## 15. Concurrent refunds làm sao không vượt Payment amount?

Atomic counter:

```text
payment_transactions.allocated_refund_amount
```

Conditional UPDATE ensures:

```text
allocated_refund_amount + requested <= payment.amount
```

Pending/processing refunds đã consume capacity.

---

## 16. Retry refund làm sao không refund hai lần?

```text
UNIQUE(idempotency_key)
+
request_hash
```

Same key/same semantic request returns existing refund.

Same key/different amount/request returns:

```text
IDEMPOTENCY_KEY_REUSED
```

---

# 42. Critical Database Invariants

```text
- Voucher scope and shop_id must be structurally compatible.

- Voucher money uses BIGINT minor units.

- Percentage uses basis points, never FLOAT.

- Voucher has a deterministic currency.

- Effective voucher usability depends on status + starts_at + ends_at.

- Background cleanup never defines expiry truth.

- Validation alone never consumes quota.

- RESERVED usage with unexpired deadline consumes global/per-user quota.

- RESERVED usage past expires_at is logically expired and cannot commit.

- COMMITTED usage continues consuming quota.

- RELEASED/EXPIRED usage does not consume quota.

- Expiration releases allocated_usage_count exactly once.

- allocated_usage_count never exceeds usage_limit.

- Same voucher + same checkout creates one logical usage.

- Global quota allocation is atomic.

- Per-user concurrent reservations serialize on logical user/voucher key.

- Release decrements voucher counter exactly once.

- Commit does not increment quota a second time.

- Committed usage references the Parent Order of the same User, Checkout and currency.

- Parent Order may have multiple Payment attempts.

- Payment Module owns Payment status.

- Payment Module never directly owns Order status.

- Payment amount/currency snapshot matches authoritative Parent Order at creation.

- Same merchant idempotency key identifies one logical Payment command.

- Same provider + provider_payment_id identifies one local PaymentTransaction.

- Provider event identity is unique per provider.

- Duplicate webhook cannot cause duplicate logical side effects.

- Out-of-order webhook cannot regress terminal Payment state.

- Webhook secret is never stored in event rows.

- Payment reconciliation can find stale pending/processing attempts.

- A Payment supports multiple partial refunds.

- allocated_refund_amount never exceeds Payment amount.

- Pending/processing refund consumes refundable capacity.

- Failed/cancelled refund releases capacity exactly once.

- Succeeded refund keeps capacity consumed.

- Refund.provider must equal PaymentTransaction.provider.

- Same `(provider, provider_refund_id)` identifies one provider refund.

- Same refund idempotency key + same request returns same refund.

- Same refund key + different request is rejected.

- Financial history uses ON DELETE RESTRICT.

- Refund.currency_code must equal PaymentTransaction.currency_code.

- Payment/Refund state transitions must follow explicit state machines and
    lifecycle timestamps.

- Payment expiry cannot be later than the shared Inventory/Voucher hold deadline.

- Late PaymentSucceeded never fulfills expired holds and enters refund/reconciliation.

- Internal command keys are operation-namespaced; raw external keys are not
    globally unique identities.
```

---

# 43. Critical Index Summary

## Voucher

```sql
UNIQUE(vouchers.code)
```

```sql
CREATE INDEX idx_vouchers_shop_status
ON vouchers(shop_id, status)
WHERE scope = 'shop';
```

```sql
CREATE INDEX idx_vouchers_status_time
ON vouchers(status, starts_at, ends_at);
```

## Voucher Usage

```sql
UNIQUE(voucher_id, checkout_reference_id)
```

```sql
CREATE INDEX idx_voucher_usages_reserved_expires
ON voucher_usages(expires_at)
WHERE status = 'reserved';
```

```sql
CREATE INDEX idx_voucher_usages_voucher_status
ON voucher_usages(voucher_id, status);
```

```sql
CREATE INDEX idx_voucher_usages_user_voucher
ON voucher_usages(user_id, voucher_id);
```

## Payment

Supporting FK identities:

```sql
UNIQUE(orders.id, orders.user_id, orders.checkout_reference_id,
       orders.currency, orders.order_type)

UNIQUE(orders.id, orders.currency, orders.order_type)

UNIQUE(payment_transactions.id, provider)

UNIQUE(payment_transactions.id, provider, currency_code)
```

```sql
UNIQUE(payment_transactions.idempotency_key)
```

```sql
CREATE UNIQUE INDEX uq_payment_provider_payment
ON payment_transactions(provider, provider_payment_id)
WHERE provider_payment_id IS NOT NULL;
```

```sql
CREATE UNIQUE INDEX uq_payment_parent_live_attempt
ON payment_transactions(parent_order_id)
WHERE status IN ('pending', 'processing', 'succeeded');
```

```sql
CREATE INDEX idx_payment_parent_created
ON payment_transactions(parent_order_id, created_at DESC);
```

```sql
CREATE INDEX idx_payment_reconciliation
ON payment_transactions(status, created_at)
WHERE status IN ('pending', 'processing');
```

## Webhook

```sql
UNIQUE(provider, provider_event_id)
```

```sql
CREATE INDEX idx_payment_webhook_processing
ON payment_webhook_events(processing_status, received_at)
WHERE processing_status IN ('received', 'failed');
```

## Refund

```sql
UNIQUE(payment_refunds.idempotency_key)
```

```sql
CREATE UNIQUE INDEX uq_refund_provider_refund
ON payment_refunds(provider, provider_refund_id)
WHERE provider_refund_id IS NOT NULL;
```

```sql
CREATE INDEX idx_payment_refunds_payment_created
ON payment_refunds(payment_transaction_id, created_at DESC);
```

---

# 44. Relationship Diagram

```text
SHOP
 │
 └──────────────► VOUCHER
                    │
USER ───────────────┼────► VOUCHER_USAGE
                    │           │
CHECKOUT_REFERENCE ─┘           │
                                ▼
                         PARENT ORDER
```

Payment:

```text
PARENT ORDER
     │
     │ 1
     ▼
     * PAYMENT_TRANSACTION
          │
          ├────────► * PAYMENT_REFUND
          │
          └────────► * PAYMENT_WEBHOOK_EVENT
```

Ownership:

```text
Order Module
    │
    │ facts only
    ▼
Payment Module

No cross-module direct status mutation.
```

---

# 45. Acceptance Tests — Voucher

## Test 1 — Global Usage Limit

Setup:

```text
usage_limit = 1
allocated_usage_count = 0
```

100 concurrent users reserve.

Expected:

```text
1 success
99 rejected
allocated_usage_count = 1
one RESERVED usage
```

No oversubscription.

---

## Test 2 — Per-User Limit

Setup:

```text
usage_limit_per_user = 1
```

Same user sends 100 concurrent Checkout attempts with distinct checkout references.

Expected:

```text
maximum one RESERVED/COMMITTED allocation
remaining requests rejected
```

---

## Test 3 — Voucher Retry

```text
ReserveVoucher(voucher=V1, checkout=X)
→ response lost

ReserveVoucher(voucher=V1, checkout=X)
```

Expected:

```text
same logical voucher_usage
allocated_usage_count incremented once
```

---

## Test 4 — Expired Voucher

```text
status = active
ends_at < wall-clock now
```

Expected:

```text
reject
```

even if no cleanup worker ran.

---

## Test 5 — Future Voucher

```text
status = active
starts_at > wall-clock now
```

Expected:

```text
reject
```

---

## Test 6 — Wrong Currency

```text
Voucher = VND
Checkout = USD
```

Expected:

```text
reject
```

---

## Test 7 — Release Idempotency

Reserve usage then call Release twice.

Expected:

```text
status = released
allocated_usage_count decremented exactly once
```

---

## Test 8 — Commit Idempotency

Reserve usage then Commit twice.

Expected:

```text
status = committed
counter unchanged by repeated commit
one logical redemption
```

---


## Test 9 — Voucher Reservation Expiry Recovery

Setup:

```text
usage_limit = 100
allocated_usage_count = 100
20 usages are RESERVED but expires_at <= now
```

Expiration workers process zombie reservations.

Expected:

```text
20 usages → EXPIRED
allocated_usage_count decremented by 20
voucher capacity becomes available again
```

Repeated worker execution:

```text
no double decrement
```

## Test 10 — Commit After Voucher Expiry

Usage:

```text
status = reserved
expires_at <= wall-clock now
```

Commit attempt before cleanup worker.

Expected:

```text
reject as expired
no COMMITTED transition
worker/inline cleanup later releases quota
```

## Test 11 — Shop Voucher Eligible Amount Isolation

Cart:

```text
Shop A = 100k
Shop B = 900k
global = 1m
```

Voucher:

```text
scope = shop
shop_id = Shop A
minimum_order_amount = 500k
```

Expected:

```text
reject
```

because Shop A eligible base is only 100k.


# 46. Acceptance Tests — Payment


## Test 12 — Prevent Concurrent Live Payment Attempts

Parent P100 has:

```text
Attempt A = processing
```

Attempt B creation is requested.

Expected:

```text
uq_payment_parent_live_attempt violation / domain conflict
```

After A becomes failed:

```text
Attempt B may be created
```

If A succeeds:

```text
no future live PaymentTransaction may be created for P100
```


## Test 13 — Multiple Payment Attempts

Order P100:

```text
Attempt 1 → failed
Attempt 2 → failed
Attempt 3 → succeeded
```

Expected:

```text
three PaymentTransaction rows preserved
one succeeded attempt
```

---

## Test 14 — Payment Retry

```text
CreatePayment(P100, key=X)
response timeout
retry key=X
```

Expected:

```text
one logical PaymentTransaction
one logical provider payment
```

Same key with different semantic request:

```text
IDEMPOTENCY_KEY_REUSED
```

---

## Test 15 — Duplicate Webhook

Payment:

```text
status = processing
```

100 deliveries of same provider SUCCESS event.

Expected:

```text
Payment = succeeded
one provider event identity
one accepted transition
one logical success side effect
```

---

## Test 16 — Out-of-Order Webhook

Process:

```text
SUCCESS
```

then delayed:

```text
PROCESSING
```

Expected:

```text
Payment remains succeeded
delayed event recorded as ignored/no-op
```

---

## Test 17 — Provider Payment Identity

Two local rows attempt same:

```text
provider = stripe-like
provider_payment_id = pay_123
```

Expected:

```text
unique constraint rejects duplicate logical provider payment
```

---

## Test 18 — Reconciliation Query

Old:

```text
pending/processing
```

payments must be efficiently discoverable by status/time index.

---

# 47. Acceptance Tests — Refund

## Test 19 — Concurrent Full Refunds

Payment:

```text
amount = 100
allocated_refund_amount = 0
status = succeeded
```

Concurrent:

```text
Refund A = 100
Refund B = 100
```

Expected:

```text
only one allocation succeeds
allocated_refund_amount = 100
```

---

## Test 20 — Partial Refunds

Payment:

```text
amount = 500
```

Refunds:

```text
100 succeeded
200 succeeded
```

Expected:

```text
allocated_refund_amount = 300
remaining refundable = 200
```

---

## Test 21 — Failed Refund Releases Capacity

Payment amount:

```text
100
```

Refund 80 allocated then fails.

Expected:

```text
allocated_refund_amount returns to 0
```

A later Refund 100 may allocate.

---

## Test 22 — Refund Retry

```text
Refund(P1, amount=40, key=R1)
timeout
retry same request
```

Expected:

```text
one refund
40 allocated once
```

Retry:

```text
key=R1
amount=70
```

Expected:

```text
IDEMPOTENCY_KEY_REUSED
```

No extra allocation.

---

# 48. Final Schema Summary

```text
VOUCHER MODULE
────────────────────────────────────

vouchers
├── id
├── code
├── scope
├── shop_id
├── discount_type
├── discount_value
├── max_discount_amount
├── minimum_order_amount
├── currency_code
├── usage_limit
├── usage_limit_per_user
├── allocated_usage_count
├── starts_at
├── ends_at
├── status
├── created_at
└── updated_at

voucher_usages
├── id
├── voucher_id
├── user_id
├── checkout_reference_id
├── order_id
├── parent_order_type
├── discount_amount
├── currency_code
├── status
├── expires_at
├── created_at
├── updated_at
├── committed_at
├── released_at
└── expired_at
```

```text
PAYMENT MODULE
────────────────────────────────────

payment_transactions
├── id
├── parent_order_id
├── parent_order_type
├── provider
├── provider_payment_id
├── idempotency_key
├── request_hash
├── amount
├── currency_code
├── allocated_refund_amount
├── status
├── failure_code
├── failure_message
├── expires_at
├── created_at
├── updated_at
├── processing_at
├── succeeded_at
├── failed_at
└── cancelled_at

payment_refunds
├── id
├── payment_transaction_id
├── provider
├── provider_refund_id
├── idempotency_key
├── request_hash
├── amount
├── currency_code
├── status
├── reason
├── failure_code
├── failure_message
├── created_at
├── updated_at
├── processing_at
├── succeeded_at
├── failed_at
└── cancelled_at

payment_webhook_events
├── id
├── provider
├── provider_event_id
├── provider_payment_id
├── payment_transaction_id
├── event_type
├── payload_hash
├── processing_status
├── received_at
├── processed_at
└── last_error
```

---

# 49. Architecture Decisions

## AD-003F-01 — Voucher Supports Platform + Shop Scope

Single `vouchers` table with structural scope invariant.

## AD-003F-02 — Percentage Uses Basis Points

Integer arithmetic only.

## AD-003F-03 — Voucher Has One Currency

V1 deterministic money policy.

## AD-003F-04 — Expiry Is Temporal

`active + time window` determines effective usability.

## AD-003F-05 — Voucher Uses Reservation Lifecycle

```text
reserved → committed/released
```

Validation alone does not consume.

## AD-003F-06 — Global Quota Uses Atomic Counter

No `COUNT then INSERT`.

## AD-003F-07 — Per-User Quota Uses Logical Advisory Lock

Correct generic `usage_limit_per_user = N` without another table.

## AD-003F-08 — Voucher Reservation Has TTL/Recovery

Every RESERVED usage has `expires_at`.

Expired reservations cannot commit and must release quota exactly once.

## AD-003F-09 — Voucher Eligible Base Depends on Scope

Platform voucher uses checkout-wide eligible amount.

Shop voucher uses only the matching Seller Group eligible amount.

## AD-003F-10 — Voucher Retry Correlates to Checkout

```text
(voucher_id, checkout_reference_id)
```

is unique.

## AD-003F-10A — Voucher Commits on Timely Payment Success

Usage remains RESERVED while Order is `awaiting_payment`; only a timely
PaymentSucceeded commits it to the correlated Parent Order.

## AD-003F-11 — Parent Order Has Many Payment Attempts

Failed/retried attempts remain historical.

## AD-003F-12 — Payment Owns Payment Status Only

Order status remains Order Module responsibility.

## AD-003F-13 — Payment Command Identity and Provider Identity Are Separate

```text
idempotency_key
!=
provider_payment_id
```

## AD-003F-14 — One Economically Live Payment Attempt per Parent

V1 enforces a partial unique index over:

```text
pending / processing / succeeded
```

to prevent double-charge races.

## AD-003F-14A — Payment Window Cannot Outlive Holds

Payment/provider expiry is bounded by the shared Inventory/Voucher deadline.
Late success follows refund/reconciliation and never fulfills expired holds.

## AD-003F-15 — Dedicated Webhook Inbox Is Required

`payment_webhook_events` is part of V1.

## AD-003F-16 — Webhook Dedup Uses Provider Event Identity

```text
(provider, provider_event_id)
```

unique.

## AD-003F-17 — Webhooks Must Pass State Machine

Out-of-order events cannot regress terminal states.

## AD-003F-18 — Partial Refund Is First-Class

One payment may have many refunds.

## AD-003F-19 — Refund Capacity Is Reserved Atomically

`allocated_refund_amount` prevents concurrent over-refund.

## AD-003F-20 — Refund Retry Uses Key + Request Hash

Same key/different semantic request is conflict.

Payment and Refund internal idempotency keys are server-generated and
operation-namespaced; external raw keys require actor + operation scope.

## AD-003F-21 — Financial Rows Use RESTRICT

No destructive cascade of payment/refund history.

## AD-003F-22 — Reconciliation Is Required Architecture

Stale pending/processing attempts must be queryable.

## AD-003F-23 — No Payment Status History Table in V1

Webhook inbox provides provider-event audit; dedicated full transition history can be added later.

---

# 50. Out of Scope

DATA-003F does not implement:

```text
migration SQL
Go structs
sqlc
repositories
services
RabbitMQ
Outbox
Saga orchestration
provider SDK
webhook cryptography
refund provider adapter
reconciliation worker
settlement
chargeback/dispute
voucher targeting rules
promotion stacking engine
payment_status_histories
```

---

# 51. DATA-003F Completion State

DATA-003F is complete at design level when these are approved:

```text
Voucher scope
Voucher percentage representation
Voucher currency semantics
Voucher effective lifecycle
Voucher reservation lifecycle
Global quota concurrency
Per-user quota concurrency
Voucher idempotency
Payment attempt model
Payment ownership
Payment state machine
Merchant idempotency
Provider identity
Webhook table decision
Webhook deduplication
Webhook replay protection
Out-of-order handling
Reconciliation
Partial refunds
Refund concurrency
Refund idempotency
Financial historical integrity
```

Final source-of-truth model:

```text
Catalog
→ current product/price

Inventory
→ current stock

Voucher
→ voucher eligibility + redemption allocation

Order
→ commercial transaction snapshot

Payment
→ provider/payment/refund financial state
```

Core distinction:

```text
Validation
!=
Reservation
!=
Commitment
!=
Historical Record
```
