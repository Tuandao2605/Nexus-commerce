# ECOM-DATA-003E — Order Domain Model

> Status: FINAL DESIGN — reconciled with DATA-003G remediation  
> Scope: design only; migrations/sqlc/Go implementation chưa tồn tại.

## 1. Scope

DATA-003E thiết kế Order Domain cho Nexus Commerce.

Ticket này là design-only.

Chưa thực hiện:

PostgreSQL migration

sqlc queries

Go repository/service

HTTP handlers

Checkout orchestration

Payment integration

Inventory commit/release implementation

Refund/return/shipment implementation

Schema V1 tối thiểu gồm:

ORDER MODULE
├── orders
├── order_items
└── order_status_histories

Do Cart V1 là global multi-shop cart, DATA-003E phải giải quyết cách biến một Checkout chứa nhiều Shop thành Order model có thể:

giữ đúng commercial history;

phân quyền Seller;

hỗ trợ fulfillment/cancel theo Seller;

giữ trace tới User/Shop/SKU;

không bị thay đổi lịch sử khi Catalog/User/Seller thay đổi;

giữ được monetary snapshot;

hỗ trợ retry/idempotency ở boundary Checkout → Order;

hỗ trợ payment/refund/settlement ở các ticket sau.

## 2. Core Principle

Cart chỉ là purchase intent.

# Cart

mutable purchase intent

Order là transaction history.

# Order

commercial transaction snapshot

Order phải giữ đủ dữ liệu để sau nhiều tháng hoặc nhiều năm vẫn reconstruct được transaction ở thời điểm tạo Order.

Acceptance invariant quan trọng:

Order created at T1

Catalog/User/Seller changes at T2

GET Order at T3

Commercial/history representation tại T3 phải vẫn phản ánh T1.

Core distinction:

snapshot for historical truth
≠
remove every FK

Order có thể giữ:

IDs / FKs

để trace/audit, đồng thời giữ:

snapshot fields

để reconstruct lịch sử.

## 3. Global Cart → Order Architecture Problem

Cart có thể chứa:

Cart
├── Shop A / SKU-1
├── Shop A / SKU-2
├── Shop B / SKU-9
└── Shop C / SKU-4

Checkout phải chọn một trong ba model:

A. One Order contains multiple Shops

B. One independent Order per Shop

C. Parent Order + child Seller Orders

## 4. Option A — One Order Contains Multiple Shops

Ví dụ:

Order O-100
├── Shop A
│ ├── SKU-1
│ └── SKU-2
├── Shop B
│ └── SKU-9
└── Shop C
└── SKU-4

Advantages

một Checkout tạo đúng một Order;

Customer nhìn thấy một order number;

payment tổng dễ biểu diễn;

schema ban đầu nhìn đơn giản.

Problems

Một orders.status duy nhất rất khó mô tả thực tế.

Ví dụ:

Shop A = shipped
Shop B = cancelled
Shop C = processing

Order tổng đang là gì?

Nếu chỉ có:

orders.status

thì mất information.

Seller authorization cũng phức tạp:

Seller A mở Order O-100

Seller A chỉ được xem phần Shop A, nhưng Order chứa thông tin của Shop B/C.

Shipping cũng không còn là một state duy nhất:

Shop A → HCM
Shop B → Hanoi

mỗi Seller có fulfillment khác nhau.

Cancel cũng mơ hồ:

Cancel Order O-100

là cancel toàn bộ hay chỉ một seller segment?

Decision

Không chọn Option A.

## 5. Option B — One Independent Order per Shop

Checkout:

Global Cart
│
▼
Checkout
│
├── Order OA — Shop A
├── Order OB — Shop B
└── Order OC — Shop C

Advantages

orders.status rõ;

seller ownership rõ;

fulfillment đơn giản;

cancel theo Shop đơn giản;

shipping per Shop tự nhiên.

Problems

Một Checkout multi-shop tạo nhiều Order hoàn toàn độc lập.

Nếu không có entity phía trên, khó trả lời:

Những Order nào được tạo từ cùng một Checkout?

Payment tổng cũng khó correlate.

Ví dụ:

Payment P100 = 500,000

nhưng:

Order A = 200,000
Order B = 300,000

Nếu Payment retry/refund/reconcile, cần một aggregate boundary chung.

Customer UX cũng cần biết nhiều Seller Orders này xuất phát từ cùng một Checkout.

Decision

Không chọn Option B độc lập.

## 6. Option C — Parent Order + Child Seller Orders

Conceptual model:

Customer Checkout
│
▼
Parent Order
│
├── Seller Order A
│ ├── SKU-1
│ └── SKU-2
│
├── Seller Order B
│ └── SKU-9
│
└── Seller Order C
└── SKU-4

Parent Order biểu diễn:

customer checkout aggregate
business correlation
payment correlation
overall customer-facing identity

Seller Order biểu diễn:

seller-owned operational sub-order
shipping
fulfillment
seller cancellation
seller subtotal
seller items

Advantages

giữ một aggregate identity cho global Checkout;

mỗi Seller có Order riêng để operate;

seller permissions đơn giản;

fulfillment/cancel/status per Seller rõ;

payment có thể correlate với Parent;

Customer vẫn xem được toàn bộ checkout;

dễ mở rộng refund/settlement sau này.

Cost

Schema phức tạp hơn.

Cần phân biệt:

Parent Order status
vs
Seller Order status

và aggregate state phải được derive/transition từ child states.

## 7. Final Architecture Decision

Nexus Commerce chọn:

# OPTION C

Parent Order + Child Seller Orders

V1 dùng một bảng orders self-referencing để biểu diễn cả Parent Order và Seller Order.

orders
├── Parent Order row
└── Seller Order rows

Seller Order có:

parent_order_id
shop_id

Parent Order có:

parent_order_id = NULL
shop_id = NULL

OrderItem chỉ thuộc Seller Order.

Không attach OrderItem trực tiếp vào Parent Order.

## 8. Order Hierarchy

Ví dụ:

Order P100
type = parent
customer = U1

├── Order S101
│ type = seller
│ shop = Shop A
│ ├── Item SKU-A
│ └── Item SKU-B
│
└── Order S102
type = seller
shop = Shop B
└── Item SKU-X

Invariant:

Parent Order
→ parent_order_id = NULL
→ shop_id = NULL
→ no direct order_items

Seller Order
→ parent_order_id required
→ shop_id required
→ owns order_items

## 9. Why One orders Table?

Một schema khác có thể là:

orders
seller_orders
order_items

V1 chọn self-reference vì:

Parent và Seller Order có nhiều metadata chung;

tránh duplicate common columns;

status history có thể reference cùng một table;

query hierarchy vẫn rõ;

dễ giữ một Order aggregate model trong Order Module.

Nếu parent/child diverge mạnh về sau, có thể tách.

## 10. Module Ownership

Order Module sở hữu:

orders
order_items
order_status_histories

References:

Identity:
users.id

Seller:
shops.id

Catalog:
skus.id

Order Module không sở hữu User, Shop hoặc SKU lifecycle.

## 11. Who Owns Order.status?

orders.status thuộc:

Order Module

Không thuộc:

Payment Module
Inventory Module
Seller Module
Shipping Module

Các module khác phát sinh facts/events.

Ví dụ:

Payment succeeded
Inventory committed
Shipment delivered
Seller requested cancellation

Nhưng Order Module quyết định transition hợp lệ.

Rule:

external domain fact
↓
Order policy
↓
Order status transition

Không module khác UPDATE trực tiếp orders.status.

## 12. Order Types

V1:

parent
seller

Constraint:

CHECK (
order_type IN (
'parent',
'seller'
)
)

## 13. Parent Order State Space

Parent states:

awaiting_payment
confirmed
partially_completed
completed
partially_cancelled
cancelled

Conceptual lifecycle:

AWAITING_PAYMENT
│
▼
CONFIRMED
│
├────────► PARTIALLY_COMPLETED
│
▼
COMPLETED

AWAITING_PAYMENT / CONFIRMED
│
├────────► CANCELLED
│
└────────► PARTIALLY_CANCELLED

Parent state là aggregate customer-facing state.

Nó không phải fulfillment state của một Seller.

`CREATED` là domain event của việc tạo hierarchy, không phải một persisted
`orders.status`. Cả Parent và Seller Order được insert trực tiếp ở
`awaiting_payment`.

## 14. Seller Order State Space

Seller states:

awaiting_payment
confirmed
processing
shipped
delivered
cancelled

Lifecycle:

AWAITING_PAYMENT
│
▼
CONFIRMED
│
▼
PROCESSING
│
▼
SHIPPED
│
▼
DELIVERED

Cancellation paths:

AWAITING_PAYMENT ───────► CANCELLED
CONFIRMED ─────► CANCELLED
PROCESSING ────► CANCELLED

V1 không cho normal:

SHIPPED → CANCELLED
DELIVERED → CANCELLED

Các trường hợp sau shipping sẽ thuộc return/refund workflow.

Payment ownership được tách rõ:

PaymentSucceeded trước hold deadline
→ Order Module atomically chuyển Parent và mọi Seller Order đang
`awaiting_payment` sang `confirmed`, đồng thời ghi status histories.

PaymentFailed của một attempt
→ Order vẫn `awaiting_payment` nếu payment/hold deadline còn hiệu lực và policy
cho retry; attempt mới thuộc Payment Module.

Payment deadline hết
→ cancel hierarchy, release/expire Inventory và Voucher holds.

Late PaymentSucceeded sau hold deadline không được confirm hay fulfill Order;
Payment phải đi qua idempotent refund/reconciliation và hierarchy bị cancel.

## 15. Status Must Be Compatible With order_type

Parent và Seller dùng chung:

orders.status

nhưng state space khác nhau.

Không được chỉ CHECK tất cả status chung trong một list.

Database phải enforce:

status value
compatible with
order_type

Required constraint:

CHECK (
(
order_type = 'parent'
AND status IN (
'awaiting_payment',
'confirmed',
'partially_completed',
'completed',
'partially_cancelled',
'cancelled'
)
)
OR
(
order_type = 'seller'
AND status IN (
'awaiting_payment',
'confirmed',
'processing',
'shipped',
'delivered',
'cancelled'
)
)
)

DB reject:

parent.status = shipped

và:

seller.status = completed

Phân chia trách nhiệm:

Database:
valid state space

Order Service:
valid transition graph

## 16. Seller Valid Transitions

Allowed:

awaiting_payment → confirmed
awaiting_payment → cancelled

confirmed → processing
confirmed → cancelled

processing → shipped
processing → cancelled

shipped → delivered

Terminal:

delivered
cancelled

Không backward transition.

## 17. Parent Aggregate Rules

Examples.

Nếu tất cả Seller Orders:

cancelled

Parent:

cancelled

Nếu một số cancelled và một số vẫn active/completed:

partially_cancelled

Nếu tất cả Seller Orders hoàn tất fulfillment:

completed

Nếu một số delivered và một số vẫn progressing:

partially_completed

Exact transition policy thuộc Order service implementation.

## 18. TABLE: orders

Owner: Order Module

Purpose

Stores both:

Parent commercial checkout aggregate
Seller-specific child Order

## 19. orders Columns

Column

Type

Null

Default

Constraint

id

UUID

NO

—

PK

order_number

VARCHAR(64)

NO

—

UNIQUE

order_type

VARCHAR(20)

NO

—

CHECK

parent_order_id

UUID

YES

NULL

FK

parent_order_type

VARCHAR(20)

YES

NULL

composite FK / structural CHECK

checkout_reference_id

UUID

YES

NULL

structural CHECK + partial UNIQUE

user_id

UUID

NO

—

FK

shop_id

UUID

YES

NULL

FK / structural CHECK

status

VARCHAR(32)

NO

'awaiting_payment'

type-aware CHECK

currency

CHAR(3)

NO

—

CHECK

subtotal_amount

BIGINT

NO

0

CHECK

discount_amount

BIGINT

NO

0

CHECK

tax_amount

BIGINT

NO

0

CHECK

shipping_amount

BIGINT

NO

0

CHECK

total_amount

BIGINT

NO

0

CHECK

customer_name_snapshot

VARCHAR(160)

NO

—

CHECK

customer_email_snapshot

VARCHAR(320)

YES

NULL

customer_phone_snapshot

VARCHAR(40)

YES

NULL

shipping_recipient_name

VARCHAR(160)

YES

NULL

shipping_phone

VARCHAR(40)

YES

NULL

shipping_address_line1

VARCHAR(255)

YES

NULL

shipping_address_line2

VARCHAR(255)

YES

NULL

shipping_ward

VARCHAR(120)

YES

NULL

shipping_district

VARCHAR(120)

YES

NULL

shipping_city

VARCHAR(120)

YES

NULL

shipping_country_code

CHAR(2)

YES

NULL

shop_name_snapshot

VARCHAR(160)

YES

NULL

structural CHECK

created_at

TIMESTAMPTZ

NO

now()

updated_at

TIMESTAMPTZ

NO

now()

CHECK

confirmed_at

TIMESTAMPTZ

YES

NULL

completed_at

TIMESTAMPTZ

YES

NULL

cancelled_at

TIMESTAMPTZ

YES

NULL

Required lifecycle constraints:

CHECK (updated_at >= created_at)

CHECK (confirmed_at IS NULL OR confirmed_at >= created_at)

CHECK (completed_at IS NULL OR completed_at >= created_at)

CHECK (cancelled_at IS NULL OR cancelled_at >= created_at)

CHECK (
  (status = 'awaiting_payment' AND confirmed_at IS NULL
    AND completed_at IS NULL AND cancelled_at IS NULL)
  OR
  (status IN ('confirmed', 'processing', 'shipped',
              'partially_completed', 'partially_cancelled')
    AND confirmed_at IS NOT NULL
    AND completed_at IS NULL AND cancelled_at IS NULL)
  OR
  (status IN ('completed', 'delivered')
    AND confirmed_at IS NOT NULL
    AND completed_at IS NOT NULL AND cancelled_at IS NULL)
  OR
  (status = 'cancelled'
    AND completed_at IS NULL AND cancelled_at IS NOT NULL)
)

Money uses integer minor units.

Không dùng floating point.

## 20. Primary Key

PRIMARY KEY (id)

UUIDv7 generated by Go.

## 21. Order Number

Human-facing identifier:

order_number

Example:

NC-20260909-8R4K2

Constraint:

UNIQUE(order_number)

Internal relation/query uses id.

## 22. Parent Self-Reference

UNIQUE (id, user_id, currency, order_type)

FOREIGN KEY (
parent_order_id,
user_id,
currency,
parent_order_type
)
REFERENCES orders(id, user_id, currency, order_type)
ON DELETE RESTRICT

Parent:

parent_order_id = NULL
parent_order_type = NULL

Seller:

parent_order_id IS NOT NULL
parent_order_type = 'parent'

Composite FK buộc referenced row là Parent Order đồng thời có cùng User và
currency. Đây là DB-enforced invariant, không còn là service-only check.

Hai downstream composite references cần supporting keys:

UNIQUE (id, user_id, checkout_reference_id, currency, order_type)

UNIQUE (id, currency, order_type)

## 23. Checkout Correlation Is Required

Order creation phải có business correlation mạnh với Checkout.

Invariant:

one successful Checkout
→ exactly one Parent Order hierarchy

Parent Order:

checkout_reference_id NOT NULL

Seller Order:

checkout_reference_id = NULL

Seller Order inherit correlation qua Parent.

## 24. Why checkout_reference_id Matters

Scenario:

POST /checkout
│
▼
Order hierarchy created
│
X response lost
│
Client retries
▼
POST /checkout

Không được:

Checkout C100 → P100
Retry C100 → P101

Phải là:

Checkout C100 ─┐
Retry #1 ├──→ P100
Retry #2 ┘

## 25. Unique Checkout → Parent Order

Required partial unique index:

CREATE UNIQUE INDEX uq_parent_order_checkout_reference
ON orders(checkout_reference_id)
WHERE order_type = 'parent';

Invariant:

same checkout_reference_id
→ maximum one Parent Order

## 26. HTTP Idempotency vs Business Correlation

Hai layer khác nhau.

# HTTP Idempotency-Key

request-level retry protection

External key phải được scope theo authenticated actor + operation và đi kèm
request hash. Không đặt global UNIQUE trực tiếp trên raw client key.

# checkout_reference_id

business-level Checkout/Order correlation

Có thể cùng tồn tại.

Order correctness không nên phụ thuộc hoàn toàn vào HTTP idempotency.

## 27. User FK

FOREIGN KEY (user_id)
REFERENCES users(id)
ON DELETE RESTRICT

user_id dùng cho:

trace
authorization
audit

Historical customer rendering dùng snapshot.

## 28. Shop FK

Seller Order:

FOREIGN KEY (shop_id)
REFERENCES shops(id)
ON DELETE RESTRICT

Parent:

shop_id = NULL

Seller:

shop_id NOT NULL

## 29. Parent/Seller Structural CHECK

Required conceptual constraint:

CHECK (
(
order_type = 'parent'
AND parent_order_id IS NULL
AND parent_order_type IS NULL
AND shop_id IS NULL
AND shop_name_snapshot IS NULL
AND checkout_reference_id IS NOT NULL
)
OR
(
order_type = 'seller'
AND parent_order_id IS NOT NULL
AND parent_order_type = 'parent'
AND shop_id IS NOT NULL
AND shop_name_snapshot IS NOT NULL
AND checkout_reference_id IS NULL
)
)

This gives:

Parent
→ owns checkout correlation
→ no Shop

Seller
→ belongs to Parent
→ belongs to Shop
→ no duplicated checkout correlation

## 30. Seller Order Uniqueness

Invariant:

For Seller Orders:

same parent_order_id

- same shop_id
  → maximum one Seller Order

Use partial unique index:

CREATE UNIQUE INDEX uq_orders_parent_shop
ON orders(
parent_order_id,
shop_id
)
WHERE order_type = 'seller';

Không dùng generic:

UNIQUE(parent_order_id, shop_id)

vì partial index mô tả domain rule chính xác hơn.

## 31. Currency

V1 mỗi Parent hierarchy sử dụng một currency.

Examples:

VND
USD

currency:

CHAR(3)

CHECK (
currency = upper(currency)
AND char_length(currency) = 3
)

Parent và tất cả child Seller Orders phải cùng currency; composite Parent FK ở
section 22 enforce invariant này ở DB.

## 32. Monetary Semantics

Seller Order persist:

subtotal_amount
discount_amount
tax_amount
shipping_amount
total_amount

Expected:

# total_amount

subtotal_amount

- discount_amount

+ tax_amount
+ shipping_amount

Checks:

CHECK (subtotal_amount >= 0)
CHECK (discount_amount >= 0)
CHECK (tax_amount >= 0)
CHECK (shipping_amount >= 0)
CHECK (total_amount >= 0)

Conceptual:

CHECK (
total_amount =
subtotal_amount - discount_amount + tax_amount + shipping_amount
)

V1 có thể enforce công thức này nếu pricing model giữ đơn giản như thiết kế hiện tại.

## 33. Parent Monetary Aggregation

Parent amounts:

# Parent.subtotal

SUM(child.subtotal)

Tương tự:

discount
tax
shipping
total

Simple CHECK không enforce cross-row sum.

Đây là transaction/service invariant.

Parent totals được persist để:

customer read nhanh;

payment correlation;

reconciliation;

audit.

## 34. Customer Snapshot

Order phải survive User changes.

T1:

User name = Nguyen Van A
phone = 090...

T2 User đổi profile.

T3 GET Order phải vẫn show T1.

Snapshot:

customer_name_snapshot
customer_email_snapshot
customer_phone_snapshot

user_id vẫn giữ để trace.

## 35. Shipping Snapshot

Không chỉ lưu:

address_id

vì User có thể sửa/xóa saved address.

Order lưu:

shipping_recipient_name
shipping_phone
shipping_address_line1
shipping_address_line2
shipping_ward
shipping_district
shipping_city
shipping_country_code

Historical truth là snapshot.

## 36. Seller Snapshot

Seller có thể đổi tên Shop.

Seller Order lưu:

shop_id
shop_name_snapshot

T3 GET Order phải show tên Shop ở T1.

shop_id chỉ dùng trace/admin/auth.

## 37. Parent vs Seller Snapshot Placement

Recommended V1:

Parent:
customer identity snapshot
aggregate monetary snapshot
checkout correlation

Seller Order:
customer identity snapshot
shipping snapshot
shop snapshot
seller monetary snapshot

Việc duplicate một số customer snapshot xuống Seller Order là intentional.

Mỗi Seller Order nên tự reconstruct được độc lập.

## 38. TABLE: order_items

Owner: Order Module

Purpose

Immutable commercial line-item snapshot thuộc một Seller Order.

OrderItem phải preserve:

what was sold at T1

## 39. order_items Columns

Column

Type

Null

Default

Constraint

id

UUID

NO

—

PK

order_id

UUID

NO

—

composite FK

shop_id

UUID

NO

—

composite consistency

product_id

UUID

NO

—

composite FK

variant_id

UUID

NO

—

composite FK

sku_id

UUID

NO

—

composite FK

sku_code_snapshot

VARCHAR(100)

NO

—

CHECK

product_name_snapshot

VARCHAR(255)

NO

—

CHECK

variant_name_snapshot

VARCHAR(255)

YES

NULL

sku_name_snapshot

VARCHAR(255)

YES

NULL

attributes_snapshot

JSONB

NO

'{}'

CHECK JSON object

quantity

BIGINT

NO

—

CHECK

unit_price_amount

BIGINT

NO

—

CHECK

discount_amount

BIGINT

NO

0

CHECK

tax_amount

BIGINT

NO

0

CHECK

line_subtotal_amount

BIGINT

NO

—

CHECK

line_total_amount

BIGINT

NO

—

CHECK

created_at

TIMESTAMPTZ

NO

now()

No updated_at in V1.

OrderItem is immutable commercial data.

## 40. OrderItem Primary Key

PRIMARY KEY (id)

UNIQUE (order_id, sku_id)

Một Seller Order chỉ có một immutable commercial line cho mỗi SKU.

## 41. Should OrderItem Keep FK to SKU?

Final decision:

YES

Keep both:

sku_id

- historical snapshot fields

Meaning:

# sku_id

trace / audit / correlation

# snapshot

historical source of truth

Do not use current Catalog fields to reconstruct historical Order.

## 42. SKU FK Nullability

V1 uses:

sku_id NOT NULL

because Catalog lifecycle uses archive/status rather than physical delete.

FK:

ON DELETE RESTRICT

Therefore historical OrderItem can keep a valid SKU reference.

## 43. Tenant-Safe OrderItem → SKU FK

OrderItem stores:

shop_id
sku_id

Composite FK:

FOREIGN KEY (variant_id, product_id, shop_id)
REFERENCES product_variants(id, product_id, shop_id)
ON DELETE RESTRICT

FOREIGN KEY (sku_id, variant_id, shop_id)
REFERENCES skus(id, variant_id, shop_id)
ON DELETE RESTRICT

Requires Catalog:

product_variants UNIQUE(id, product_id, shop_id)

skus UNIQUE(id, variant_id, shop_id)

Invariant:

# OrderItem.shop_id

SKU.shop_id

## 44. OrderItem → Seller Order Shop Consistency

Independent FKs không đủ.

Bad state:

Seller Order = Shop A

OrderItem:
shop_id = Shop B
sku_id = SKU B

Để prevent:

On orders:

UNIQUE(id, shop_id)

OrderItem:

FOREIGN KEY (order_id, shop_id)
REFERENCES orders(id, shop_id)
ON DELETE RESTRICT

Result:

# SellerOrder.shop_id

# OrderItem.shop_id

SKU.shop_id

Critical multi-tenant invariant.

## 45. Parent Order Must Not Own Items

OrderItem chỉ được attach vào:

order_type = seller

Composite FK `(order_id, shop_id) → orders(id, shop_id)` cùng structural rule
`Parent.shop_id IS NULL` và `OrderItem.shop_id NOT NULL` khiến Parent không thể
own item. Invariant này được DB enforce.

## 46. OrderItem Quantity

CHECK (
quantity BETWEEN 1 AND 99
)

Order quantity immutable sau creation.

Không arbitrary update quantity sau purchase.

Changes later dùng:

cancel
return
refund

workflows.

## 47. Unit Price Snapshot

OrderItem stores:

unit_price_amount

T1:

Catalog price = 100
Order unit_price = 100

T2:

Catalog price = 120

T3 Order remains:

100

## 48. Item Discount Snapshot

Persist:

discount_amount

là total discount allocated cho line.

Example:

quantity = 2
unit price = 100

line subtotal = 200
discount = 30
tax = 10

line total = 180

## 49. Item Tax Snapshot

Persist:

tax_amount

Historical Order không recalculate old tax bằng future tax rules.

## 50. OrderItem Monetary Formula

# line_subtotal_amount

unit_price_amount \* quantity

# line_total_amount

line_subtotal_amount

- discount_amount

+ tax_amount

Checks:

CHECK (unit_price_amount >= 0)
CHECK (discount_amount >= 0)
CHECK (tax_amount >= 0)
CHECK (line_subtotal_amount >= 0)
CHECK (line_total_amount >= 0)

Multiplication equality có thể enforce ở service layer nếu muốn tránh complexity/overflow trong DB constraint.

## 51. Product Identity Snapshot

OrderItem stores:

sku_code_snapshot
product_name_snapshot
variant_name_snapshot
sku_name_snapshot
attributes_snapshot

CHECK (
jsonb_typeof(attributes_snapshot) = 'object'
)

T1:

Product = "Keyboard Pro"
Variant = "Black"
SKU = KB-PRO-BLK

T2 Catalog rename.

T3 Order vẫn show T1.

## 52. Optional Future Snapshot Fields

Không bắt buộc cho correctness V1:

image_url_snapshot
product_slug_snapshot
brand_snapshot

Có thể thêm sau cho UX.

Minimum historical reconstruction cần:

product_id / variant_id / sku_id để trace;

product/variant/SKU identifying text;

selected variant attributes ở thời điểm mua;

quantity;

price;

discount;

tax;

Seller;

shipping destination;

currency;

totals.

## 53. OrderItem Immutability

Normal API không expose:

UPDATE order_items
DELETE order_items

OrderItem là commercial history.

Nếu cần correction, dùng explicit workflow/audit record thay vì silently overwrite.

## 54. TABLE: order_status_histories

Owner: Order Module

Purpose

Immutable audit trail của Order state transitions.

Current:

orders.status

History:

order_status_histories

## 55. order_status_histories Columns

Column

Type

Null

Default

Constraint

id

UUID

NO

—

PK

order_id

UUID

NO

—

FK

from_status

VARCHAR(32)

YES

NULL

to_status

VARCHAR(32)

NO

—

actor_type

VARCHAR(30)

NO

—

CHECK

actor_id

UUID

YES

NULL

reason_code

VARCHAR(64)

YES

NULL

reason

VARCHAR(512)

YES

NULL

created_at

TIMESTAMPTZ

NO

now()

Không có updated_at.

History immutable.

## 56. Status History FK

FOREIGN KEY (order_id)
REFERENCES orders(id)
ON DELETE RESTRICT

## 57. Actor Semantics

Possible:

customer
seller
system
admin
payment
shipping

Conceptual:

CHECK (
actor_type IN (
'customer',
'seller',
'system',
'admin',
'payment',
'shipping'
)
)

actor_id có thể NULL nếu system-generated.

## 58. Initial Status History

Order creation:

from_status = NULL
to_status = awaiting_payment

Mỗi later transition:

UPDATE orders.status

- INSERT order_status_histories

trong cùng transaction.

## 59. Current State vs History

# orders.status

current state source of truth

# order_status_histories

immutable transition audit

Không reconstruct current state trên hot path bằng scan history.

## 60. Valid Transition Enforcement

DB CHECK đảm bảo state value phù hợp với order_type.

Full transition graph do Order service enforce.

Pattern:

BEGIN

SELECT Order
FOR UPDATE

validate current state
validate requested transition

UPDATE orders.status

INSERT order_status_histories

COMMIT

## 61. Status Transition Atomicity

Invariant:

# successful status change

current status update

- history insert

same transaction.

Không được:

UPDATE orders.status

mà thiếu history.

Không được:

INSERT history

nếu status update fail.

## 62. Seller Visibility

Seller chỉ được access Seller Orders của Shop mà Seller được authorized.

Query scope:

order_type = seller
AND shop_id IN authorized_shop_ids

Seller không được xem:

other Sellers' OrderItems
other Sellers' shipping detail
other Sellers' operational state

Seller chỉ có thể thấy Parent correlation nếu thực sự cần.

## 63. Customer Visibility

Customer có thể xem:

Parent Order

- all child Seller Orders

Authorization:

orders.user_id = authenticated_user.id

## 64. Admin Visibility

Admin/support có thể inspect:

Parent
└── all Seller Orders
└── all items

## 65. Cancellation Semantics

Global multi-shop Checkout cần hỗ trợ:

cancel one Seller Order

và:

cancel whole Parent aggregate

Đây là hai operations khác nhau.

## 66. Cancel One Seller Order

Example:

Parent P100
├── Seller A CONFIRMED
└── Seller B PROCESSING

Cancel Seller A nếu transition policy cho phép.

Result:

Seller A → CANCELLED
Seller B unchanged
Parent → PARTIALLY_CANCELLED

Inventory/payment compensation thuộc later orchestration ticket.

## 67. Cancel Entire Parent Order

Parent cancellation command nghĩa là:

attempt cancellation of all cancellable active child Seller Orders

Không phải:

UPDATE parent.status = cancelled

Nếu all child cancel:

Parent → CANCELLED

Nếu only some cancel:

Parent → PARTIALLY_CANCELLED

depending on policy.

## 68. Shipping Semantics

Shipping belongs to Seller Order level.

Reason:

different Shop
→ different warehouse
→ different carrier
→ different shipping fee
→ different fulfillment timeline

Parent shipping:

SUM(child.shipping_amount)

## 69. Tax Semantics

Tax calculation rules thuộc service/tax engine khác.

Order persists result:

tax_amount

Historical Order không recalculate bằng future tax policy.

## 70. Discount Semantics

Checkout computes final accepted discounts.

Order snapshots:

order_items.discount_amount
orders.discount_amount

Nếu future cần promotion provenance, có thể add:

order_adjustments
order_discounts

Không required cho DATA-003E V1.

## 71. Historical Reconstruction

T1 Order snapshots commercial truth.

T2:

User changes name/address
Shop changes name
Product changes name
SKU code/price/status changes
SKU archived

T3 GET Order uses:

Order snapshots

not current external domain values.

FKs chỉ dùng cho:

trace
audit
admin navigation
correlation
authorization

## 72. Snapshot Source Matrix

Historical Field

Snapshot Location

Current ID/FK Kept?

Customer identity

orders

user_id

Shipping address

Seller orders

no historical dependency on address row

Shop name

Seller orders

shop_id

Product name

order_items

sku_id trace

Variant/SKU display

order_items

sku_id

SKU code

order_items

sku_id

Unit price

order_items

Catalog not trusted later

Quantity

order_items

immutable

Discount

Order/item snapshot

promotion source not trusted later

Tax

Order/item snapshot

tax engine not trusted later

Shipping fee

Seller Order

shipping engine not trusted later

Currency

orders

immutable

## 73. Checkout → Order Boundary

Cart gives:

purchase intent

Checkout must revalidate:

current Catalog
current prices
Shop/SKU lifecycle
Inventory
shipping
discount
tax

Then:

Checkout Result
│
▼
Order snapshot creation

Order không phụ thuộc Cart để render lịch sử.

## 74. Inventory Boundary

Order creation và Inventory reservation/commit là liên quan nhưng khác domain.

Conceptual:

Checkout
├── validate commercial data
├── reserve Inventory
├── create Order hierarchy
└── coordinate Payment

Exact saga/transaction ordering nằm ở later Checkout/Payment ticket.

Order không UPDATE trực tiếp Inventory tables.

V1 ordering contract:

1. validate selected CartItems and compute one hierarchy currency;
2. reserve Inventory and Voucher using the same checkout deadline;
3. create/reuse Order hierarchy in `awaiting_payment`;
4. create/reuse PaymentTransaction whose provider expiry is not later than the
   hold deadline;
5. on PaymentSucceeded before deadline, commit Inventory + Voucher and confirm
   Order atomically in the V1 shared-PostgreSQL finalization transaction;
6. on cancellation/timeout, release or expire both holds;
7. late success after deadline never fulfills; route it to refund/reconciliation.

Mỗi failure phải compensate mọi prior committed step: Inventory failure releases
an earlier Voucher hold; Order/Payment initialization failure cancels any created
`awaiting_payment` hierarchy and releases both holds. TTL workers remain the
crash-recovery safety net, không thay thế immediate compensation.

Finalization transaction chỉ bao gồm DB side effects sau provider success và gọi
qua owner-module repository/service methods với shared transaction handle:

```text
lock/dedupe PaymentSucceeded inbox
lock + validate unexpired Inventory/Voucher holds
commit Inventory and write SALE movements
commit VoucherUsage to correlated Parent Order
confirm Parent + Seller Orders and write histories
write required owner outbox records
COMMIT
```

External provider call không nằm trong transaction. Nếu precondition/deadline
fail trước transaction, không có commerce side effect nào commit và Payment đi
refund/reconciliation.

## 75. Cart Boundary

Sau khi Order + PaymentTransaction initialize thành công, Cart Module remove
đúng selected CartItems trong transaction đã lock Cart:

remaining items > 0 → Cart remains ACTIVE

remaining items = 0 → Cart ACTIVE → CHECKED_OUT

Cart không phải historical source.

Ngay cả khi Cart cleanup sau này:

Order remains reconstructable

## 76. Order Creation Atomicity Within Order DB

Parent + children + items + initial histories phải được tạo trong một PostgreSQL transaction.

Conceptual:

BEGIN

INSERT Parent Order
INSERT Seller Order A
INSERT Seller Order B
INSERT OrderItems
INSERT initial status histories

COMMIT

Nếu Seller B creation fail:

ROLLBACK

Không để partial hierarchy.

## 77. Order Creation Idempotency

Core invariant:

one successful Checkout
→ one Parent Order hierarchy

Protection:

checkout_reference_id

- partial unique index

Retry cùng Checkout phải:

return/reuse existing Parent hierarchy

không tạo duplicate commercial transaction.

## 78. Delete Policy

Orders là commercial history.

Use:

ON DELETE RESTRICT

for:

users → orders
shops → seller orders
orders → child orders
orders → order_items
orders → order_status_histories
skus → order_items

Normal application không physical-delete completed commercial records.

Retention/anonymization policy thuộc later ticket.

## 79. orders Indexes

UNIQUE(order_number)

CREATE UNIQUE INDEX uq_parent_order_checkout_reference
ON orders(checkout_reference_id)
WHERE order_type = 'parent';

CREATE UNIQUE INDEX uq_orders_parent_shop
ON orders(
parent_order_id,
shop_id
)
WHERE order_type = 'seller';

CREATE INDEX idx_orders_parent
ON orders(parent_order_id);

CREATE INDEX idx_orders_user_created
ON orders(user_id, created_at DESC);

CREATE INDEX idx_orders_shop_status_created
ON orders(
shop_id,
status,
created_at DESC
)
WHERE order_type = 'seller';

Supporting tenant key:

UNIQUE(id, shop_id)

## 80. order_items Indexes

CREATE INDEX idx_order_items_order
ON order_items(order_id);

CREATE INDEX idx_order_items_sku
ON order_items(sku_id);

## 81. order_status_histories Indexes

CREATE INDEX idx_order_status_histories_order_created
ON order_status_histories(
order_id,
created_at ASC
);

## 82. Multi-Tenant Invariants

For Seller Orders:

# SellerOrder.shop_id

# OrderItem.shop_id

SKU.shop_id

DB support:

orders UNIQUE(id, shop_id)

skus UNIQUE(id, shop_id)

OrderItem FKs:

(order_id, shop_id)
→ orders(id, shop_id)

(sku_id, shop_id)
→ skus(id, shop_id)

This blocks cross-Shop item attachment.

## 83. Historical Snapshot Invariants

Customer

User changes
→ historical Order customer snapshot unchanged

Seller

Shop changes
→ shop_name_snapshot unchanged

Catalog

Product/SKU changes
→ OrderItem snapshot unchanged

Commercial values

Price/discount/tax/shipping changes in external systems
→ Order monetary snapshot unchanged

## 84. Monetary Invariants

All money:

BIGINT integer minor units

Never:

FLOAT
DOUBLE PRECISION

Seller Order:

# total

subtotal

- discount

* tax
* shipping

Parent:

# parent totals

sum child totals

Parent aggregation is service-enforced.

## 85. Critical Database Invariants

1. Order always references an existing User.

1. Seller Order references an existing Shop.

1. order_type is parent or seller.

1. Parent status must belong to Parent state space.

1. Seller status must belong to Seller state space.

1. Parent has parent_order_id = NULL.

1. Parent has shop_id = NULL.

1. Parent has checkout_reference_id NOT NULL.

1. Parent has no shop_name_snapshot.

1. Seller has parent_order_id NOT NULL.

1. Seller has shop_id NOT NULL.

1. Seller has shop_name_snapshot NOT NULL.

1. Seller has checkout_reference_id = NULL.

1. Same checkout_reference_id
   → maximum one Parent Order.

1. Same Parent + same Shop
   → maximum one Seller Order.

1. SellerOrder.shop_id =
   OrderItem.shop_id =
   SKU.shop_id.

1. OrderItem quantity nằm trong [1, 99].

1. Seller Parent composite FK enforce cùng User, currency và referenced
   order_type = parent.

1. Voucher/Payment downstream references có supporting composite Parent keys.

1. Lifecycle timestamps phải tương thích với status và không trước created_at.

1. OrderItem monetary values are non-negative.

1. Order monetary totals are non-negative.

1. OrderItem historical snapshot is not replaced by live Catalog.

1. User FK does not replace customer snapshot.

1. Shop FK does not replace Seller snapshot.

1. SKU FK does not replace Catalog snapshot.

1. Parent monetary totals equal child aggregates.

1. Parent and child currencies match.

1. Current state lives in orders.status.

1. Every status transition writes immutable history.

1. Invalid transition graph is rejected by Order Service.

1. Seller may access only authorized Seller Orders.

1. Parent cancellation orchestrates child cancellation.

1. Seller Order may be cancelled independently when valid.

1. Shipping snapshot belongs at Seller Order level.

1. Orders use ON DELETE RESTRICT.

1. Order commercial snapshot is not refreshed from live domains.

1. Parent + children + items + initial histories are created atomically.

1. One successful Checkout must not create duplicate Order hierarchies.

## 86. Acceptance Test — Parent Cannot Use Seller Status

Attempt:

order_type = parent
status = shipped

Expected:

CHECK constraint violation

## 87. Acceptance Test — Seller Cannot Use Parent Aggregate Status

Attempt:

order_type = seller
status = partially_completed

Expected:

CHECK constraint violation

## 88. Acceptance Test — Duplicate Seller Order

Existing:

Parent P100
└── Seller Order Shop A

Attempt second:

Parent P100
└── another Seller Order Shop A

Expected:

uq_orders_parent_shop violation

## 89. Acceptance Test — Checkout Retry

First:

checkout_reference_id = C100

creates:

Parent P100

Retry same Checkout:

checkout_reference_id = C100

Expected:

reuse/return P100

No P101.

No duplicate Seller Orders.

No duplicate OrderItems.

## 90. Acceptance Test — Catalog Changes After Order

T1:

Product = "Keyboard Pro"
SKU code = KB-PRO-BLK
price = 1,000,000

Order created.

T2:

Product = "Keyboard Pro Gen2"
SKU code changed
price = 1,200,000

T3 GET Order.

Expected:

product = Keyboard Pro
sku_code = KB-PRO-BLK
unit_price = 1,000,000

## 91. Acceptance Test — User Address Changes

T1:

shipping = 123 Street A, HCM

Order created.

T2 User updates:

999 Street B, Hanoi

T3 GET Order.

Expected:

123 Street A, HCM

## 92. Acceptance Test — Shop Renamed

T1:

shop = Nexus Tech

Order created.

T2:

shop = Nexus Digital

T3 GET Seller Order:

Nexus Tech

## 93. Acceptance Test — SKU Archived

Order created with SKU-A.

Later:

SKU-A.status = archived

Expected:

Order remains readable
OrderItem remains traceable
snapshot unchanged

## 94. Acceptance Test — Cross-Shop Item Rejection

Setup:

Seller Order = Shop A
SKU B = Shop B

Attempt:

OrderItem:
order = Seller Order A
shop_id = Shop B
sku_id = SKU B

Expected:

composite FK violation

## 95. Acceptance Test — Multi-Shop Checkout

Cart:

Shop A / 2 items
Shop B / 1 item

Expected:

1 Parent Order
2 Seller Orders
3 Order Items

No OrderItem directly under Parent.

## 96. Acceptance Test — Seller Visibility

Seller A requests Order data.

Expected:

Seller A can access Seller Order A
Seller A cannot access Seller Order B

Customer can access Parent + all own child orders.

## 97. Acceptance Test — Partial Cancellation

Initial:

Parent CONFIRMED
├── Seller A CONFIRMED
└── Seller B PROCESSING

Cancel Seller A.

Expected:

Seller A CANCELLED
Seller B PROCESSING
Parent PARTIALLY_CANCELLED

## 98. Acceptance Test — Status History Atomicity

Transition:

confirmed → processing

Expected same transaction:

orders.status = processing

history:
from = confirmed
to = processing

Crash/rollback:

neither persists

## 99. Acceptance Test — Historical Price Truth

T1:

Catalog price = 100

Checkout validates and Order snapshots:

unit_price_amount = 100

T2:

Catalog price = 150

T3 GET old Order:

100

New checkout:

150

## 100. Schema Summary — orders

orders
────────────────────────────────────────
id UUID PK
order_number VARCHAR(64) UNIQUE

order_type VARCHAR(20)

parent_order_id UUID NULL FK
parent_order_type VARCHAR(20) NULL
checkout_reference_id UUID NULL

user_id UUID FK
shop_id UUID NULL FK

status VARCHAR(32)
currency CHAR(3)

subtotal_amount BIGINT
discount_amount BIGINT
tax_amount BIGINT
shipping_amount BIGINT
total_amount BIGINT

customer_name_snapshot VARCHAR(160)
customer_email_snapshot VARCHAR(320) NULL
customer_phone_snapshot VARCHAR(40) NULL

shipping_recipient_name VARCHAR(160) NULL
shipping_phone VARCHAR(40) NULL
shipping_address_line1 VARCHAR(255) NULL
shipping_address_line2 VARCHAR(255) NULL
shipping_ward VARCHAR(120) NULL
shipping_district VARCHAR(120) NULL
shipping_city VARCHAR(120) NULL
shipping_country_code CHAR(2) NULL

shop_name_snapshot VARCHAR(160) NULL

created_at TIMESTAMPTZ
updated_at TIMESTAMPTZ

confirmed_at TIMESTAMPTZ NULL
completed_at TIMESTAMPTZ NULL
cancelled_at TIMESTAMPTZ NULL

UNIQUE(id, shop_id)
UNIQUE(id, user_id, currency, order_type)
UNIQUE(id, user_id, checkout_reference_id, currency, order_type)
UNIQUE(id, currency, order_type)

PARTIAL UNIQUE:
(checkout_reference_id)
WHERE order_type = 'parent'

PARTIAL UNIQUE:
(parent_order_id, shop_id)
WHERE order_type = 'seller'

## 101. Schema Summary — order_items

order_items
────────────────────────────────────────
id UUID PK

order_id UUID
shop_id UUID
product_id UUID
variant_id UUID
sku_id UUID

sku_code_snapshot VARCHAR(100)
product_name_snapshot VARCHAR(255)
variant_name_snapshot VARCHAR(255) NULL
sku_name_snapshot VARCHAR(255) NULL
attributes_snapshot JSONB

quantity BIGINT

unit_price_amount BIGINT
discount_amount BIGINT
tax_amount BIGINT
line_subtotal_amount BIGINT
line_total_amount BIGINT

created_at TIMESTAMPTZ

FK(order_id, shop_id)
→ orders(id, shop_id)

FK(sku_id, variant_id, shop_id)
→ skus(id, variant_id, shop_id)

FK(variant_id, product_id, shop_id)
→ product_variants(id, product_id, shop_id)

## 102. Schema Summary — order_status_histories

order_status_histories
────────────────────────────────────────
id UUID PK
order_id UUID FK

from_status VARCHAR(32) NULL
to_status VARCHAR(32)

actor_type VARCHAR(30)
actor_id UUID NULL

reason_code VARCHAR(64) NULL
reason VARCHAR(512) NULL

created_at TIMESTAMPTZ

## 103. Relationship Diagram

USER
│
▼
PARENT ORDER
│
├────────────────────────┐
▼ ▼
SELLER ORDER A SELLER ORDER B
│ │
▼ ▼
ORDER ITEMS ORDER ITEMS
│ │
▼ ▼
SKU SKU

Each Order:

ORDER
│
▼
ORDER_STATUS_HISTORIES

Tenant relation:

SHOP
│
▼
SELLER ORDER
│
▼
ORDER ITEM
│
▼
SKU

Invariant:

Shop IDs match end-to-end.

## 104. Architecture Decisions

AD-003E-01 — Parent + Child Seller Orders

Global Cart Checkout creates:

one Parent Order

- one Seller Order per Shop

AD-003E-02 — One orders Table

Parent and Seller Orders share one self-referencing table in V1.

AD-003E-03 — Parent Owns Checkout Correlation

Only Parent Order stores:

checkout_reference_id

Seller Orders inherit correlation through parent_order_id.

AD-003E-04 — Seller Orders Own Items

OrderItems belong only to Seller Orders.

Parent is aggregate-only.

AD-003E-05 — Order Module Owns Status

External modules emit facts.

Only Order Module performs Order state transitions.

AD-003E-06 — Status Space Depends on Order Type

DB enforces:

Parent uses Parent states
Seller uses Seller states

Order Service enforces transition graph.

AD-003E-07 — Seller Order Partial Uniqueness

same Parent + same Shop
→ one Seller Order

enforced by partial unique index.

AD-003E-08 — Checkout Idempotency Correlation Is Mandatory

same checkout_reference_id
→ one Parent Order hierarchy

AD-003E-09 — Snapshot + FK

Snapshots preserve history.

FKs preserve trace/audit.

Both remain.

AD-003E-10 — Seller Tenant Safety

Composite FKs enforce:

# SellerOrder.shop_id

# OrderItem.shop_id

SKU.shop_id

AD-003E-11 — Monetary Snapshot

Order stores:

price
discount
tax
shipping
total
currency

as historical commercial truth.

AD-003E-12 — Shipping Snapshot per Seller Order

Different Shops may have different shipping/fulfillment.

AD-003E-13 — Immutable Order Items

Catalog changes never rewrite historical OrderItem data.

AD-003E-14 — Current Status + Immutable History

# orders.status

current state

# order_status_histories

audit trail

AD-003E-15 — Same-Transaction Status Mutation

Status update and history insert must commit together.

AD-003E-16 — Seller-Level Cancellation

Seller Orders can cancel independently when valid.

AD-003E-17 — Parent Cancellation Is Orchestration

Parent cancellation coordinates child cancellations.

AD-003E-18 — External Domain Changes Do Not Rewrite Order

Changes to:

User
Shop
Product
Variant
SKU
Price
Address

must not alter historical Order representation.

## 105. Checkout Boundary

Global Cart
│
▼
Checkout
│
├── validate Catalog
├── validate Seller
├── calculate current price
├── calculate discount
├── calculate tax
├── calculate shipping
├── group by Shop
├── reserve Inventory
│
▼
Order Creation
│
├── Parent Order
├── Seller Orders
├── Order Items
└── Initial Status Histories

Order creation uses:

checkout_reference_id

to prevent duplicate hierarchy.

## 106. Final Source-of-Truth Model

Purchase Intent
────────────────────
Cart

Current Product / Price
────────────────────
Catalog

Current Stock
────────────────────
Inventory

Commercial Transaction History
────────────────────
Order

Payment Settlement Truth
────────────────────
Payment Domain

Order snapshots commercial amounts Payment operates against, nhưng Payment Domain vẫn là source of truth cho payment settlement state.

## 107. Out of Scope

DATA-003E không implement:

PostgreSQL migration
sqlc
Go models
repositories
services
Checkout saga
Payment capture
Payment webhook handling
Refunds
Returns
Shipment tables
Seller settlement
Promotion detail tables
Tax detail tables
Order adjustment tables

Các phần này nằm ở implementation/domain tickets sau.

## 108. DATA-003E Completion State

DATA-003E hoàn thành ở design level khi các quyết định sau được approve:

Global Cart → Order architecture
Parent/Seller hierarchy
Order ownership
Status ownership
Parent/Seller state spaces
type-aware status constraint
Seller visibility
Cancellation semantics
Customer snapshot
Shipping snapshot
Seller snapshot
Catalog/SKU snapshot
Price/discount/tax/shipping snapshots
SKU FK + historical snapshot strategy
Tenant consistency
Status history
Checkout correlation/idempotency
Checkout boundary
Acceptance invariants

Final architecture:

Global Multi-Shop Cart
↓
Checkout
↓
Parent Order
↓
Seller Orders per Shop
↓
Order Items

Historical truth:

T1 Order snapshot

- # T2 external domain changes
  T3 Order representation remains T1

Business uniqueness:

# one successful Checkout

one Parent Order hierarchy
