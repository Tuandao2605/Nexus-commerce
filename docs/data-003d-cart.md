# ECOM-DATA-003D — Cart Domain Model

> Status: FINAL DESIGN — reconciled with DATA-003G remediation

## 1. Scope

DATA-003D thiết kế Cart Domain cho Nexus Commerce.

Schema V1 gồm:

```text
CART MODULE
├── carts
└── cart_items
```

Cart biểu diễn:

```text
temporary purchase intent
```

Cart **không phải**:

```text
source of truth for price
stock reservation
order
```

Các invariant quan trọng:

```text
Cart != Price Source of Truth
Cart != Inventory Reservation
Cart != Order
```

---

# 2. Design Goals

Cart Domain phải hỗ trợ:

- một User có một active shopping cart;
- cart global có thể chứa SKU từ nhiều Shop;
- một SKU chỉ xuất hiện một logical item trong cùng Cart;
- add quantity concurrent không tạo duplicate;
- quantity update không dùng unsafe read-modify-write;
- SKU inactive/archived không làm mất purchase intent;
- out-of-stock không tự xóa CartItem;
- price thay đổi không làm Cart trở thành nguồn giá;
- checkout luôn revalidate Catalog + Inventory;
- Cart không reserve Inventory;
- Cart không snapshot commercial truth của Order.

---

# 3. Core Principle

Cart chỉ trả lời:

```text
"User đang có ý định mua gì?"
```

Cart không trả lời:

```text
"Giá cuối cùng là bao nhiêu?"
"Stock chắc chắn còn không?"
"Order cuối cùng là gì?"
```

Ownership:

```text
Identity Module
    users
      │
      ▼
Cart Module
    carts
      │
      ▼
    cart_items
      │
      ▼
Catalog Module
    skus
```

---

# 4. Cart Ownership Decision

## Chosen Model

V1 chọn:

```text
GLOBAL MULTI-SHOP CART
```

Một User có tối đa:

```text
1 ACTIVE global cart
```

Ví dụ:

```text
User A
└── Active Cart
    ├── Shop A / SKU-1
    ├── Shop A / SKU-2
    └── Shop B / SKU-9
```

---

# 5. Global Cart vs Per-Shop Cart

## Option A — Per-Shop Cart

```text
User
├── Cart Shop A
└── Cart Shop B
```

### Ưu điểm

- checkout đơn giản hơn;
- shipping scope đơn giản;
- voucher theo Shop dễ hơn;
- mỗi Cart chỉ liên quan một Seller;
- Order mapping đơn giản hơn.

### Nhược điểm

UX bị phân mảnh.

User thêm sản phẩm từ nhiều Shop sẽ phải quản lý nhiều cart độc lập.

Ví dụ:

```text
Shop A cart: 2 items
Shop B cart: 3 items
```

User không có một shopping cart thống nhất.

---

## Option B — Global Multi-Shop Cart

```text
User
└── Cart
    ├── Shop A / SKU-1
    ├── Shop A / SKU-2
    └── Shop B / SKU-9
```

### Ưu điểm

- UX marketplace tự nhiên;
- một cart icon;
- một cart page;
- user có thể thêm sản phẩm từ nhiều Seller;
- Cart Domain không cần tạo nhiều Cart lifecycle song song.

### Nhược điểm

Checkout phức tạp hơn.

Checkout phải group:

```text
Shop A
├── SKU-1
└── SKU-2

Shop B
└── SKU-9
```

Sau đó xử lý riêng:

```text
seller rules
shipping
voucher
inventory
fulfillment
order splitting
```

---

# 6. Final Decision

Nexus Commerce chọn:

```text
Global Cart
```

Lý do:

```text
Cart
=
purchase intent aggregation
```

Trong khi:

```text
Checkout
=
commercial validation + seller grouping
```

và:

```text
Order
=
immutable commercial result
```

Không nên đơn giản hóa Checkout bằng cách làm UX Cart phức tạp hơn.

Architecture:

```text
Global Cart
     │
     ▼
Checkout
     │
     ├── Shop A group
     ├── Shop B group
     └── Shop C group
            │
            ▼
        Order creation
```

Việc split seller/order sẽ được xử lý ở Checkout / DATA-003E trở đi.

---

# 7. User Cart Cardinality

Một User có thể có nhiều Cart lịch sử:

```text
User
├── Cart #1 checked_out
├── Cart #2 abandoned
└── Cart #3 active
```

Nhưng tại một thời điểm chỉ được có:

```text
maximum 1 ACTIVE cart
```

Invariant:

```text
same user
→ at most one active cart
```

Không dùng:

```text
UNIQUE(user_id)
```

vì như vậy User chỉ có thể có đúng một Cart trong toàn bộ lịch sử.

Dùng partial unique index:

```sql
CREATE UNIQUE INDEX uq_carts_user_active
ON carts(user_id)
WHERE status = 'active';
```

---

# 8. Cart Lifecycle

V1 status:

```text
ACTIVE
├── CHECKED_OUT
└── ABANDONED
```

State machine:

```text
ACTIVE ───────► CHECKED_OUT

ACTIVE ───────► ABANDONED
```

Terminal:

```text
CHECKED_OUT
ABANDONED
```

Không reuse terminal cart.

Sau khi Cart terminal, lần add-to-cart tiếp theo tạo active Cart mới.

---

# 9. TABLE: `carts`

**Owner:** Cart Module

## Purpose

Đại diện cho một shopping intent container của User.

Cart global nên không có:

```text
shop_id
```

vì một Cart có thể chứa nhiều Shop.

---

## Columns

| Column           | Type        | Null | Default    | Constraint |
| ---------------- | ----------- | ---: | ---------- | ---------- |
| `id`             | UUID        |   NO | —          | PK         |
| `user_id`        | UUID        |   NO | —          | FK         |
| `status`         | VARCHAR(20) |   NO | `'active'` | CHECK      |
| `created_at`     | TIMESTAMPTZ |   NO | `now()`    |            |
| `updated_at`     | TIMESTAMPTZ |   NO | `now()`    | CHECK      |
| `checked_out_at` | TIMESTAMPTZ |  YES | NULL       | CHECK      |
| `abandoned_at`   | TIMESTAMPTZ |  YES | NULL       | CHECK      |

---

# 10. Cart Primary Key

```sql
PRIMARY KEY (id)
```

UUIDv7 generated by Go application.

---

# 11. Cart Foreign Key

```sql
FOREIGN KEY (user_id)
REFERENCES users(id)
ON DELETE RESTRICT
```

Cart phải reference existing User.

Không dùng:

```text
ON DELETE CASCADE
```

vì accidental User deletion không nên silently delete Cart records.

Identity lifecycle nên được xử lý qua status/archive thay vì destructive cascading delete.

---

# 12. Cart Status Constraint

```sql
CHECK (
    status IN (
        'active',
        'checked_out',
        'abandoned'
    )
)
```

---

# 13. Cart Terminal Timestamp Constraints

Checked out:

```sql
CHECK (
    (
        status = 'checked_out'
        AND checked_out_at IS NOT NULL
        AND abandoned_at IS NULL
    )
    OR
    (
        status <> 'checked_out'
        AND checked_out_at IS NULL
    )
)
```

Abandoned:

```sql
CHECK (
    (
        status = 'abandoned'
        AND abandoned_at IS NOT NULL
        AND checked_out_at IS NULL
    )
    OR
    (
        status <> 'abandoned'
        AND abandoned_at IS NULL
    )
)
```

Timestamp:

```sql
CHECK (
    updated_at >= created_at
)
```

```sql
CHECK (
    checked_out_at IS NULL
    OR checked_out_at >= created_at
)
```

```sql
CHECK (
    abandoned_at IS NULL
    OR abandoned_at >= created_at
)
```

---

# 14. Cart Unique Constraint

Không dùng normal constraint cho active Cart vì uniqueness phụ thuộc `status`.

Dùng:

```sql
CREATE UNIQUE INDEX uq_carts_user_active
ON carts(user_id)
WHERE status = 'active';
```

DB invariant:

```text
User
→ maximum one ACTIVE Cart
```

Concurrent creation của hai active carts:

```text
Request A ── INSERT active cart
Request B ── INSERT active cart
```

chỉ một INSERT có thể thành công.

---

# 15. Cart Indexes

Active Cart lookup đã được hỗ trợ bởi:

```sql
CREATE UNIQUE INDEX uq_carts_user_active
ON carts(user_id)
WHERE status = 'active';
```

History lookup:

```sql
CREATE INDEX idx_carts_user_created
ON carts(user_id, created_at DESC);
```

---

# 16. Cart Reasoning

Không lưu trên Cart:

```text
total_price
subtotal
tax
shipping_fee
discount_total
stock_reserved
```

vì tất cả đều có thể stale hoặc thuộc domain khác.

Cart chỉ là container của purchase intent.

---

# 17. TABLE: `cart_items`

**Owner:** Cart Module

## Purpose

Đại diện cho intent mua một SKU với quantity cụ thể trong Cart.

Ví dụ:

```text
Cart
├── SKU-A ×2
├── SKU-B ×1
└── SKU-C ×5
```

Một logical SKU chỉ xuất hiện một lần trong cùng Cart.

---

# 18. Cart Item Columns

| Column       | Type        | Null | Default | Constraint   |
| ------------ | ----------- | ---: | ------- | ------------ |
| `id`         | UUID        |   NO | —       | PK           |
| `cart_id`    | UUID        |   NO | —       | FK           |
| `shop_id`    | UUID        |   NO | —       | composite FK |
| `sku_id`     | UUID        |   NO | —       | composite FK |
| `quantity`   | BIGINT      |   NO | —       | CHECK        |
| `created_at` | TIMESTAMPTZ |   NO | `now()` |              |
| `updated_at` | TIMESTAMPTZ |   NO | `now()` | CHECK        |

---

# 19. Why `shop_id` Exists on CartItem

Cart là global nên:

```text
carts
```

không có `shop_id`.

Nhưng từng item thuộc chính xác một Shop.

CartItem lưu:

```text
shop_id
sku_id
```

và DB enforce:

```text
CartItem.shop_id
=
SKU.shop_id
```

Điều này có hai lợi ích.

### Tenant consistency

Không thể tạo:

```text
shop_id = Shop A
sku_id  = SKU của Shop B
```

### Checkout grouping

Có thể group:

```text
cart_items
GROUP BY shop_id
```

khi Checkout cần chia theo Seller.

---

# 20. Cart Item Primary Key

```sql
PRIMARY KEY (id)
```

---

# 21. Cart Item → Cart FK

```sql
FOREIGN KEY (cart_id)
REFERENCES carts(id)
ON DELETE RESTRICT
```

Không cascade history một cách implicit.

Cart cleanup nếu cần sẽ được thực hiện explicit trong application/maintenance job.

---

# 22. Cart Item → SKU Tenant-Safe FK

```sql
FOREIGN KEY (sku_id, shop_id)
REFERENCES skus(id, shop_id)
ON DELETE RESTRICT
```

Parent prerequisite từ Catalog:

```sql
UNIQUE (id, shop_id)
```

trên `skus`.

DB đảm bảo:

```text
CartItem.shop_id
=
SKU.shop_id
```

---

# 23. Duplicate Logical Item Invariant

Không được tồn tại:

```text
Cart C1
├── SKU-A ×2
└── SKU-A ×3
```

Phải normalize thành:

```text
Cart C1
└── SKU-A ×5
```

Constraint:

```sql
UNIQUE (
    cart_id,
    sku_id
)
```

Invariant:

```text
same cart + same SKU
→ exactly one logical cart item
```

---

# 24. Quantity Constraint

```sql
CHECK (
    quantity BETWEEN 1 AND 99
)
```

V1 chốt `MAX_CART_ITEM_QUANTITY = 99`. API và mọi mutation atomic phải dùng
cùng constant này; không để DB và service lệch policy.

CartItem quantity `0` không có semantic riêng.

Nếu User set:

```text
quantity = 0
```

API nên hiểu là:

```text
remove item
```

thay vì persist:

```text
quantity = 0
```

---

# 25. Cart Item Timestamp

```sql
CHECK (
    updated_at >= created_at
)
```

---

# 26. Cart Item Indexes

Unique constraint:

```sql
UNIQUE(cart_id, sku_id)
```

đã hỗ trợ:

```text
load SKU inside Cart
```

Checkout group theo Shop:

```sql
CREATE INDEX idx_cart_items_cart_shop
ON cart_items(cart_id, shop_id);
```

SKU reverse lookup:

```sql
CREATE INDEX idx_cart_items_sku
ON cart_items(sku_id);
```

Reverse lookup hữu ích cho maintenance/lifecycle analysis nhưng không phải hot checkout path.

---

# 27. Price Semantics

## Critical Rule

```text
Cart price
!=
authoritative price
```

V1 **không persist price** trong `cart_items`.

Không có:

```text
unit_price
price_snapshot
subtotal
discount_price
```

---

# 28. Why Cart Does Not Store Price

Catalog Price có thể thay đổi:

```text
10:00
SKU-A = 100,000

10:05
User adds SKU-A to Cart

11:00
Seller updates price:
SKU-A = 120,000

12:00
User checks out
```

Cart không được yêu cầu:

```text
100,000
```

chỉ vì đó là giá lúc add-to-cart.

Checkout phải lấy:

```text
current authoritative Catalog price
```

---

# 29. Price Change Behavior

Nếu Catalog price thay đổi:

```text
CartItem
```

không cần mutation.

Ví dụ:

```text
Cart:
SKU-A ×2
```

Catalog:

```text
old = 100
new = 120
```

Cart vẫn:

```text
SKU-A ×2
```

Checkout sẽ calculate:

```text
2 × current Catalog price
```

---

# 30. Why No Price Cache in V1

Có thể thiết kế:

```text
last_seen_unit_price
```

để UI hiển thị:

```text
"Price changed since you added this item."
```

Nhưng đây chỉ là UX cache.

Nó không cần thiết cho correctness.

V1 chọn:

```text
DO NOT STORE CART PRICE CACHE
```

để tránh:

- stale duplicated data;
- confusion về source of truth;
- synchronization logic không cần thiết;
- developer vô tình dùng Cart price khi checkout.

Nếu tương lai thêm cache:

```text
Cart price cache
=
display hint only
```

Checkout vẫn không được trust nó.

---

# 31. Order Price Is Different

Cart không snapshot commercial price.

Order sẽ phải snapshot.

Flow:

```text
Catalog
current price
     │
     ▼
Checkout
validate/recalculate
     │
     ▼
Order
price snapshot
```

DATA-003E sẽ xử lý Order snapshot.

---

# 32. SKU Lifecycle Handling

CartItem reference SKU bằng FK.

Catalog SKU có thể chuyển:

```text
active
→ inactive
→ archived
```

CartItem không tự delete.

---

# 33. SKU Inactive

Ví dụ:

```text
Cart
└── SKU-A ×2
```

Sau đó:

```text
SKU-A.status = inactive
```

Cart vẫn giữ:

```text
SKU-A ×2
```

Lý do:

Cart thể hiện purchase intent.

Không nên silently mutate User Cart chỉ vì Catalog lifecycle thay đổi.

---

# 34. SKU Archived

Nếu SKU:

```text
status = archived
```

CartItem vẫn giữ reference.

Do Catalog dùng archive thay vì physical delete nên FK vẫn hợp lệ.

Checkout phải reject item đó.

Ví dụ:

```text
SKU_NOT_AVAILABLE
```

Cart UI có thể hiển thị:

```text
This item is no longer available.
```

---

# 35. Out-of-Stock Handling

Nếu:

```text
available stock = 0
```

CartItem vẫn tồn tại.

Không auto-delete.

Quan trọng:

```text
Cart != reservation
```

Cart chứa:

```text
SKU-A ×5
```

không có nghĩa Inventory đã giữ 5 units.

---

# 36. Why Out-of-Stock Item Is Kept

Stock có thể phục hồi:

```text
10:00 stock = 0
11:00 new receipt = 20
```

Nếu Cart tự xóa item lúc stock = 0:

```text
purchase intent bị mất
```

V1 giữ item và checkout/UI revalidate availability.

---

# 37. Effective Cart Item Purchasability

Một CartItem tồn tại không có nghĩa nó checkout được.

Conceptually:

```text
Purchasable =
Shop active
AND
Product active
AND
Variant active
AND
SKU active
AND
current price valid
AND
Inventory available
```

Cart không persist:

```text
is_available
```

vì đây là derived cross-domain state.

---

# 38. Cart Does Not Reserve Stock

Ví dụ:

```text
Inventory:
available = 1

User A:
Cart contains SKU ×1

User B:
Cart contains SKU ×1
```

Điều này hoàn toàn hợp lệ.

Cart không làm:

```text
reserved_quantity += 1
```

Stock chỉ được reserve khi Checkout gọi Inventory reservation flow.

Flow:

```text
Add to Cart
     │
     X
No inventory reservation
```

Sau đó:

```text
Checkout
     │
     ▼
ReserveInventory()
```

---

# 39. Concurrent Add Item

Giả sử initial:

```text
Cart has no SKU-A
```

Hai request cùng lúc:

```text
Request A:
Add SKU-A ×1

Request B:
Add SKU-A ×1
```

Không được tạo:

```text
SKU-A ×1
SKU-A ×1
```

Final expected:

```text
SKU-A ×2
```

---

# 40. Atomic Add / Increment

Không làm:

```text
SELECT cart_item

if exists:
    quantity = quantity + incoming

UPDATE
```

vì concurrent requests có thể lost update.

Sử dụng PostgreSQL UPSERT conceptually:

```sql
INSERT INTO cart_items (
    id,
    cart_id,
    shop_id,
    sku_id,
    quantity,
    created_at,
    updated_at
)
VALUES (
    $1,
    $2,
    $3,
    $4,
    $5,
    now(),
    now()
)
ON CONFLICT (cart_id, sku_id)
DO UPDATE
SET
    quantity = cart_items.quantity + EXCLUDED.quantity,
    updated_at = now()
WHERE cart_items.quantity + EXCLUDED.quantity <= 99
RETURNING *;
```

Database:

```text
UNIQUE(cart_id, sku_id)
```

đảm bảo không duplicate logical item.

Atomic increment tránh lost update.

---

# 41. Concurrent Example

Initial:

```text
quantity = 3
```

Request A:

```text
add +2
```

Request B:

```text
add +4
```

Expected:

```text
quantity = 9
```

Không được:

```text
5
```

hoặc:

```text
7
```

do lost update.

Atomic SQL:

```text
quantity = quantity + EXCLUDED.quantity
```

serialize correctly.

---

# 42. Absolute Quantity Update

API operation:

```text
SetCartItemQuantity(
    SKU-A,
    quantity = 5
)
```

không cần read current quantity trước.

Dùng:

```sql
UPDATE cart_items
SET
    quantity = $quantity,
    updated_at = now()
WHERE id = $item_id
  AND cart_id = $cart_id
  AND $quantity BETWEEN 1 AND 99;
```

Một SQL mutation.

---

# 43. Concurrent Absolute Updates

Nếu hai devices cùng lúc:

```text
Device A:
set quantity = 3

Device B:
set quantity = 5
```

V1 semantic:

```text
last committed write wins
```

Đây không tạo invalid database state.

Nếu sau này cần detect stale UI update, có thể bổ sung optimistic concurrency:

```text
version
```

nhưng không cần cho V1 correctness.

Điểm cần tránh hiện tại là:

```text
read quantity
→ calculate in Go
→ write stale quantity
```

cho increment operation.

---

# 44. Concurrent Cart Creation

Hai requests của cùng User đều không tìm thấy active Cart:

```text
Request A:
create cart

Request B:
create cart
```

Partial unique index:

```sql
UNIQUE user_id
WHERE status = 'active'
```

đảm bảo chỉ một active Cart được tạo.

Loser xử lý unique conflict rồi reload active Cart hiện có.

---

# 45. Remove Item

Remove explicit:

```sql
DELETE FROM cart_items
WHERE cart_id = $1
  AND sku_id = $2;
```

Ở Cart Domain, physical delete CartItem là chấp nhận được vì CartItem là mutable temporary intent, không phải financial/audit history.

Điều này khác:

```text
OrderItem
StockMovement
```

là historical records.

`ON DELETE RESTRICT` vẫn được dùng ở FK để tránh implicit cascading deletion.

Explicit Cart service có thể remove CartItem.

---

# 46. Cart Cleanup

Abandoned Cart có thể được cleanup theo retention policy trong tương lai.

DATA-003D không định nghĩa retention duration.

Nếu cleanup được triển khai:

```text
explicit application/job operation
```

không dựa vào cross-domain cascade.

---

# 47. Checkout Boundary

Checkout không được trust Cart như finalized commercial data.

Input:

```text
cart_id
selected_cart_item_ids
checkout_reference_id
```

`selected_cart_item_ids` phải không rỗng, không trùng lặp và tất cả item phải
thuộc Cart/actor đang checkout. Checkout hỗ trợ **partial checkout**; không mặc
định lấy toàn bộ Cart.

Checkout phải lock active Cart row, load đúng selected CartItems rồi revalidate.

Conceptual flow:

```text
Cart
 │
 │ purchase intent
 ▼
Checkout
 │
 ├── validate Cart ACTIVE
 ├── load CartItems
 ├── load current Catalog state
 ├── load current prices
 ├── validate Shop/SKU lifecycle
 ├── group items by Shop
 ├── calculate promotions
 ├── calculate shipping
 ├── reserve Inventory
 ├── create/reuse Parent + Seller Orders
 ├── create/reuse PaymentTransaction
 └── remove selected CartItems idempotently
```

Sau khi Order hierarchy và PaymentTransaction đã được initialize thành công:

```text
selected items removed
remaining items > 0  → Cart remains ACTIVE
remaining items = 0  → Cart becomes CHECKED_OUT, checked_out_at = now()
```

Không mark toàn Cart `checked_out` khi chỉ checkout một phần. Retry cùng
`checkout_reference_id` phải trả lại cùng checkout result kể cả selected items
đã được remove; coordinator phải resolve correlation trước khi kết luận item
không còn trong Cart là request mới/invalid.

---

# 48. Checkout Price Boundary

Cart:

```text
SKU-A ×2
```

không gửi authoritative:

```text
unit_price = 100
```

Checkout lấy current Catalog price:

```text
SKU-A current price = 120
```

Final calculation:

```text
2 × 120
```

Không:

```text
2 × stale Cart price
```

---

# 49. Checkout Inventory Boundary

Cart quantity:

```text
SKU-A ×10
```

không đảm bảo Inventory có 10.

Checkout:

```text
ReserveInventory(SKU-A ×10)
```

Inventory có thể trả:

```text
OUT_OF_STOCK
```

Cart không bị xem là inventory guarantee.

---

# 50. Checkout SKU Lifecycle Boundary

Nếu Cart chứa:

```text
SKU-A
```

nhưng SKU hiện tại:

```text
inactive
```

Checkout phải detect và reject/update checkout result.

CartItem không cần bị xóa trước đó.

---

# 51. Global Cart Checkout Grouping

Ví dụ:

```text
Cart
├── Shop A / SKU-1 ×2
├── Shop A / SKU-2 ×1
└── Shop B / SKU-9 ×3
```

Checkout group:

```text
Shop A
├── SKU-1 ×2
└── SKU-2 ×1

Shop B
└── SKU-9 ×3
```

Từ đây future logic có thể áp dụng:

```text
per-shop shipping
per-shop voucher
per-shop fulfillment
per-shop seller validation
per-shop order splitting
```

Cart không xử lý các rule này.

---

# 52. Cart Ownership Model

Final model:

```text
User
│
├── historical Cart
├── historical Cart
└── maximum one ACTIVE global Cart
```

DB enforcement:

```sql
CREATE UNIQUE INDEX uq_carts_user_active
ON carts(user_id)
WHERE status = 'active';
```

Cart không thuộc một Shop.

CartItem mới thuộc Shop thông qua SKU.

---

# 53. Price Semantics

Final invariant:

```text
Cart does not store authoritative price.
```

V1:

```text
no price column in cart_items
```

Price source:

```text
Catalog
```

Final commercial snapshot:

```text
Order
```

Flow:

```text
Cart intent
    │
    ▼
Catalog current price
    │
    ▼
Checkout validation
    │
    ▼
Order snapshot
```

---

# 54. SKU Lifecycle Handling

CartItem không bị auto-delete khi SKU:

```text
inactive
archived
out_of_stock
```

Cart giữ purchase intent.

Checkout revalidate current reality.

Concept:

```text
Cart state
!=
Catalog state
!=
Inventory state
```

---

# 55. Concurrency / Duplicate Item Handling

Four main invariants:

### Active Cart

```text
same User
→ max one active Cart
```

Partial unique index.

### Logical CartItem

```text
same Cart + same SKU
→ max one row
```

Unique constraint.

### Concurrent Add

```text
INSERT ... ON CONFLICT
quantity = quantity + incoming
```

Atomic increment.

### Absolute Update

```text
single UPDATE
```

V1 uses last-write-wins.

Không read-modify-write trong Go cho additive operations.

---

# 56. ON DELETE Policy

## User → Cart

```text
ON DELETE RESTRICT
```

## Cart → CartItem

```text
ON DELETE RESTRICT
```

Implicit cascade không được dùng.

Cart service có thể explicit delete CartItems khi cleanup.

## SKU → CartItem

```text
ON DELETE RESTRICT
```

Catalog sử dụng archive/status lifecycle.

Nhờ vậy Cart không mất SKU reference vì accidental physical delete.

---

# 57. Critical Database Invariants

```text
1. Cart luôn reference existing User.

2. Một User có tối đa một ACTIVE Cart.

3. Cart global không có shop_id.

4. CartItem luôn reference existing Cart.

5. CartItem luôn reference existing SKU.

6. CartItem.shop_id phải match SKU.shop_id.

7. Same Cart + same SKU chỉ có một CartItem.

8. CartItem.quantity nằm trong [1, 99].

9. updated_at >= created_at.

10. Cart terminal timestamp phải match terminal status.

11. Add/increment CartItem phải atomic.

12. Cart không persist authoritative price.

13. Cart không persist stock reservation.

14. SKU inactive/archive không tự delete CartItem.

15. Out-of-stock không tự delete CartItem.

16. Checkout phải revalidate current Catalog price.

17. Checkout phải revalidate SKU/Shop lifecycle.

18. Checkout phải reserve Inventory separately.

19. Cart không phải Order.

20. Global Cart được group by Shop ở Checkout boundary.

21. Partial checkout chỉ remove selected items; Cart chỉ terminal khi hết item.

22. Retry cùng checkout_reference_id reuse cùng checkout result.
```

---

# 58. Index Summary

```text
carts
────────────────────────────────

UNIQUE INDEX(user_id)
WHERE status = 'active'

INDEX(user_id, created_at DESC)
```

```text
cart_items
────────────────────────────────

UNIQUE(cart_id, sku_id)

INDEX(cart_id, shop_id)

INDEX(sku_id)
```

---

# 59. Schema Summary

```text
carts
────────────────────────────────
id                  UUID PK
user_id             UUID FK
status              VARCHAR(20)
created_at          TIMESTAMPTZ
updated_at          TIMESTAMPTZ
checked_out_at      TIMESTAMPTZ NULL
abandoned_at        TIMESTAMPTZ NULL

UNIQUE ACTIVE CART:
(user_id)
WHERE status = 'active'
```

```text
cart_items
────────────────────────────────
id                  UUID PK
cart_id             UUID FK
shop_id             UUID
sku_id              UUID
quantity            BIGINT
created_at          TIMESTAMPTZ
updated_at          TIMESTAMPTZ

FK(cart_id)
→ carts(id)

FK(sku_id, shop_id)
→ skus(id, shop_id)

UNIQUE(cart_id, sku_id)
```

---

# 60. Relationship Diagram

```text
USER
 │
 │ 1
 ▼ *
CART
 │
 │ 1
 ▼ *
CART_ITEM
 │
 │ *
 ▼ 1
SKU
 │
 │ *
 ▼ 1
SHOP
```

Logical global Cart:

```text
User A
└── Cart
    ├── CartItem ── SKU-A ── Shop A
    ├── CartItem ── SKU-B ── Shop A
    └── CartItem ── SKU-X ── Shop B
```

---

# 61. Architecture Decisions

## AD-003D-01 — One Active Cart Per User

Một User có nhiều historical Cart nhưng tối đa một ACTIVE Cart.

Enforced bằng partial unique index.

---

## AD-003D-02 — Global Multi-Shop Cart

Cart không có `shop_id`.

Một Cart có thể chứa nhiều Seller.

Checkout chịu trách nhiệm group theo Shop.

---

## AD-003D-03 — CartItem Is SKU-Based

Cart reference:

```text
SKU
```

không reference Product hay Variant.

Lý do SKU là purchasable inventory unit.

---

## AD-003D-04 — Tenant-Safe SKU Reference

CartItem lưu:

```text
shop_id
sku_id
```

với composite FK:

```text
(sku_id, shop_id)
→ skus(id, shop_id)
```

---

## AD-003D-05 — No Price Snapshot in Cart V1

Cart không persist unit price.

Checkout dùng current Catalog price.

Order mới snapshot final commercial price.

---

## AD-003D-06 — Cart Is Not Inventory Reservation

Add-to-cart không mutate:

```text
inventory_stocks.reserved_quantity
```

Reservation chỉ bắt đầu ở Checkout.

---

## AD-003D-07 — Preserve Unavailable Items

SKU inactive/archive/out-of-stock không tự xóa CartItem.

UI/Checkout phản ánh availability hiện tại.

---

## AD-003D-08 — Atomic Add

Concurrent increment sử dụng:

```text
INSERT ... ON CONFLICT DO UPDATE
```

và:

```text
quantity = quantity + incoming
```

---

## AD-003D-09 — Duplicate Item Prevention

```text
UNIQUE(cart_id, sku_id)
```

enforce một logical item per SKU per Cart.

---

## AD-003D-10 — Checkout Owns Revalidation

Checkout phải revalidate:

```text
Catalog
Price
Shop status
SKU status
Inventory
Shipping
Promotions
```

Cart data không phải final commercial truth.

---

# 62. Acceptance Tests

## Test 1 — One Active Cart Per User

Hai concurrent requests:

```text
CreateActiveCart(User A)
CreateActiveCart(User A)
```

Expected:

```text
one active Cart
```

Không:

```text
two active Carts
```

---

## Test 2 — Duplicate SKU

Request:

```text
Add SKU-A ×1
Add SKU-A ×2
```

Expected:

```text
one CartItem

quantity = 3
```

Không:

```text
CartItem SKU-A ×1
CartItem SKU-A ×2
```

---

## Test 3 — Concurrent Add

Initial:

```text
Cart has no SKU-A
```

99 concurrent requests:

```text
Add SKU-A ×1
```

Expected:

```text
one CartItem
quantity = 99
```

---

## Test 4 — Concurrent Increment Existing Item

Initial:

```text
quantity = 9
```

90 concurrent:

```text
+1
```

Expected:

```text
quantity = 99
```

Không lost increment.

---

## Test 5 — Price Changed

Cart contains:

```text
SKU-A ×2
```

Price when item added:

```text
100
```

Current Catalog price:

```text
120
```

Checkout:

```text
must use 120
```

Không trust stale Cart/UI price.

---

## Test 6 — SKU Inactive

Cart:

```text
SKU-A ×2
```

Catalog:

```text
SKU-A.status = inactive
```

Expected:

```text
CartItem remains
```

Checkout:

```text
reject SKU-A as unavailable
```

---

## Test 7 — SKU Archived

CartItem reference vẫn tồn tại.

Expected:

```text
Cart retains intent/reference
Checkout rejects unavailable SKU
```

---

## Test 8 — Out of Stock

Cart:

```text
SKU-A ×5
```

Inventory:

```text
available = 0
```

Expected:

```text
CartItem remains
```

Checkout Inventory reservation fails.

---

## Test 9 — Cart Does Not Reserve Inventory

Inventory:

```text
on_hand = 10
reserved = 0
```

User adds:

```text
SKU-A ×5
```

Expected Inventory:

```text
on_hand = 10
reserved = 0
```

---

## Test 10 — Multi-Shop Cart

Cart:

```text
Shop A / SKU-1
Shop B / SKU-9
```

Expected:

```text
same global Cart
```

Checkout can group:

```text
Shop A
Shop B
```

---

## Test 11 — Partial Checkout

Cart chứa A, B, C; request chỉ chọn A và B. Sau khi Order + PaymentTransaction
được initialize thành công:

```text
A, B removed
C remains
Cart remains ACTIVE
```

Retry cùng `checkout_reference_id` trả lại hierarchy cũ và không tạo Order,
PaymentTransaction hay reservation thứ hai.

---

# 63. Cart Ownership Model

```text
One User
→ many historical Carts
→ maximum one ACTIVE global Cart
```

Global Cart may contain multiple Shops.

---

# 64. Price Semantics

```text
Cart
=
quantity intent
```

```text
Catalog
=
current authoritative price
```

```text
Order
=
final price snapshot
```

Cart V1 stores no price snapshot/cache.

---

# 65. SKU Lifecycle Handling

```text
inactive SKU
archived SKU
out-of-stock SKU
```

do not automatically delete CartItem.

Cart preserves intent.

Checkout revalidates current purchasability.

---

# 66. Concurrency / Duplicate Item Handling

```text
One active Cart per User
→ partial unique index
```

```text
One logical SKU per Cart
→ UNIQUE(cart_id, sku_id)
```

```text
Concurrent add
→ atomic UPSERT increment
```

```text
Absolute update
→ single SQL UPDATE
```

Không dùng unsafe:

```text
SELECT
→ calculate quantity in Go
→ UPDATE
```

cho additive changes.

---

# 67. Checkout Boundary

Cart kết thúc trách nhiệm tại:

```text
purchase intent
```

Checkout bắt đầu trách nhiệm tại:

```text
commercial validation
```

Boundary:

```text
Cart
    │
    ▼
Current Catalog state
Current Catalog price
Current Seller state
Inventory reservation
Promotion validation
Shipping calculation
    │
    ▼
Checkout
    │
    ▼
Order
```

Cart không được bypass những validation này.

---

# 68. Final Source-of-Truth Model

```text
Purchase Intent
────────────────────
carts
cart_items
```

```text
Current Product / Price
────────────────────
Catalog
```

```text
Current Availability
────────────────────
Inventory
```

```text
Final Commercial Record
────────────────────
Order
```

Do đó:

```text
Cart != Catalog
Cart != Inventory
Cart != Order
```

---

# 69. Out of Scope

DATA-003D là design ticket.

Chưa thực hiện:

```text
PostgreSQL migration
sqlc queries
Go repository
Cart service
HTTP handlers
Checkout implementation
Inventory integration
Order integration
cleanup worker
```

Các phần implementation sẽ được thực hiện sau khi DATA-003D được review và approve.

---

# 70. DATA-003D Completion State

DATA-003D hoàn thành ở mức:

```text
Cart ownership model
Global vs per-Shop decision
Schema design
SKU relationship
Tenant consistency
Price semantics
SKU lifecycle semantics
Concurrency strategy
Duplicate handling
Checkout boundary
Acceptance test design
```

Decision quan trọng nhất:

```text
Nexus Commerce V1
=
one ACTIVE global multi-shop Cart per User
```

với boundary:

```text
Cart
=
purchase intent

Checkout
=
revalidation + grouping + reservation

Order
=
immutable commercial snapshot
```

# 71. Implementation Backlog

Hai invariant sau chưa cần thay đổi schema design của DATA-003D, nhưng bắt buộc phải được bảo vệ khi implementation.

## 71.1 Terminal Cart Must Be Immutable to Item Mutation

Schema hiện tại có thể enforce:

```text
same cart + same SKU
→ one logical CartItem
```

nhưng không thể chỉ bằng `CHECK` constraint enforce cross-table rule:

```text
Cart.status != active
→ CartItems cannot mutate
```

Do đó đây là **transaction/service invariant**.

### Race cần tránh

```text
TX-A                       TX-B

AddToCart()
                            Checkout()
                            ACTIVE → CHECKED_OUT

UPSERT CartItem
```

Nếu `AddToCart()` chỉ:

```text
1. SELECT Cart
2. kiểm tra status = active
3. sau đó mới UPSERT CartItem
```

mà không giữ lock xuyên suốt transaction, Cart có thể chuyển sang terminal giữa bước 2 và bước 3.

Kết quả nguy hiểm:

```text
CHECKED_OUT Cart
+
CartItem mới được mutate
```

### Required Implementation Discipline

Mọi mutation của CartItem phải serialize với Cart lifecycle.

Conceptual transaction:

```sql
BEGIN;

SELECT id
FROM carts
WHERE id = $1
  AND user_id = $2
  AND status = 'active'
FOR UPDATE;
```

Nếu không có row:

```text
reject mutation
```

Nếu có:

```text
Cart row lock acquired
```

sau đó mới:

```text
INSERT / UPSERT / UPDATE / DELETE cart_items
```

rồi:

```sql
COMMIT;
```

Checkout / Abandon Cart cũng phải lock Cart row theo cùng discipline trước khi
remove selected items hoặc transition:

```text
ACTIVE → CHECKED_OUT (chỉ khi không còn item)
```

hoặc:

```text
ACTIVE → ABANDONED
```

### Required Invariant

```text
ACTIVE Cart
→ CartItem mutation allowed
```

```text
CHECKED_OUT Cart
→ CartItem mutation forbidden
```

```text
ABANDONED Cart
→ CartItem mutation forbidden
```

Hay ngắn gọn:

```text
Terminal Cart
=
immutable CartItem set
```

---

## 71.2 Cart Item Quantity Upper Bound

Schema design enforce:

```sql
CHECK (quantity BETWEEN 1 AND 99)
```

API không được cho phép quantity tăng vô hạn tới giới hạn `BIGINT`.

Ví dụ không hợp lệ về business:

```text
Add SKU-A × 9,223,372,036,854,775,000
```

Ngoài abusive input, concurrent increments cũng có thể làm quantity tăng tới mức không hợp lý hoặc overflow.

### Business Invariant

Implementation phải có:

```text
1 <= quantity <= MAX_CART_ITEM_QUANTITY = 99
```

### Atomic Increment Requirement

Không được chỉ implement:

```sql
quantity = quantity + EXCLUDED.quantity
```

mà không bảo vệ upper bound.

Conceptual requirement:

```text
current quantity
+
incoming quantity
<= MAX_CART_ITEM_QUANTITY
```

Mutation phải fail nếu vượt giới hạn.

Ví dụ:

```text
current = 98
incoming = 2
MAX = 99
```

Expected:

```text
reject
```

không:

```text
quantity = 100
```

### Absolute Update

Tương tự:

```text
SetCartItemQuantity(5000)
```

phải validate against:

```text
MAX_CART_ITEM_QUANTITY
```

trước hoặc trong atomic database mutation.

### Required Invariant

```text
CartItem.quantity
∈
[1, MAX_CART_ITEM_QUANTITY]
```

Upper bound là policy V1 dùng chung ở DB, service validation và API error mapping.

---

# 72. Implementation Invariants Summary

Khi DATA-003D chuyển sang implementation, ngoài các database invariants đã thiết kế, service phải đảm bảo thêm:

```text
1. CartItem mutation chỉ được phép khi Cart vẫn ACTIVE.

2. Cart lifecycle transition và CartItem mutation phải serialize
   bằng Cart-row locking / transaction discipline.

3. CHECKED_OUT Cart không được Add/Update/Remove item.

4. ABANDONED Cart không được Add/Update/Remove item.

5. CartItem quantity phải có business upper bound.

6. Concurrent UPSERT increment phải kiểm tra upper bound
   trong cùng atomic mutation.

7. Absolute quantity update cũng phải enforce upper bound.

8. MAX_CART_ITEM_QUANTITY = 99 ở V1 và phải đồng bộ giữa DB/service/API.

9. Partial checkout chỉ remove selected items; Cart chỉ CHECKED_OUT khi hết item.

10. Retry checkout resolve checkout_reference_id trước khi re-read CartItems.
```
