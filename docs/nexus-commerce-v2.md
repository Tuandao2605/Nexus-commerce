# NEXUS-COMMERCE V2

## Production-Grade Event-Driven Multi-Vendor E-Commerce Platform

**Previous Stage:** Nexus-Commerce V1 — Core Commerce
**Current Stage:** V2 — Business-Complete Event-Driven Modular Monolith
**Future Stage:** V3 — Selective Distributed Microservices

**Backend:** Go
**Primary Database:** PostgreSQL
**Cache:** Redis
**Message Broker:** RabbitMQ
**Search Engine:** Elasticsearch
**Object Storage:** MinIO / S3-compatible
**Architecture:** Event-Driven Modular Monolith
**Primary Goals:** Backend Engineering, Distributed-System Readiness, Security, Reliability, Observability, Testing and Production Engineering

---

# 1. V2 VISION

Nexus-Commerce V2 không phải microservices.

V2 là:

```text
Production-Grade
Event-Driven
Modular Monolith
```

Mục tiêu chính:

> Xây dựng một nền tảng thương mại điện tử multi-vendor hoàn chỉnh về business capability, có module boundary đủ mạnh để sau này tách thành microservices mà không phải thiết kế lại toàn bộ hệ thống.

V2 phải giải quyết đồng thời:

* Authentication;
* Authorization;
* User management;
* Seller management;
* Product catalog;
* Inventory;
* Shopping cart;
* Voucher/promotion;
* Checkout;
* Order lifecycle;
* Payment;
* Shipping;
* Return/refund;
* Review/rating;
* Notification;
* Search;
* Analytics;
* Media;
* Event-driven architecture;
* Concurrency;
* Idempotency;
* Failure recovery;
* Security;
* Observability;
* Production testing.

---

# 2. ARCHITECTURE EVOLUTION

## V1

```text
Modular Monolith
```

Focus:

```text
Core Commerce
```

Bao gồm:

```text
Auth
User
Seller
Catalog
Inventory
Cart
Order
Payment
Voucher
Notification
```

---

## V2

```text
Event-Driven Modular Monolith
```

Bổ sung:

```text
Shipping
Review
Return
Analytics
Search
Media

Transactional Outbox
Reliable Consumers
Inbox
Idempotency
Observability
Failure Recovery
Production Testing
```

---

## V3

```text
Selective Distributed Microservices
```

Tách một số module có boundary rõ thành service độc lập.

---

# 3. V2 DESIGN PRINCIPLES

## 3.1 Modular Monolith

Tất cả business modules vẫn có thể chạy trong:

```text
nexus-commerce-api
```

và:

```text
nexus-commerce-worker
```

nhưng code phải được chia theo bounded context rõ ràng.

---

## 3.2 Single Data Ownership

Mỗi table chỉ có một module chịu trách nhiệm chính.

Ví dụ:

```text
orders
```

thuộc:

```text
Order
```

Payment không được update trực tiếp bảng `orders`.

---

## 3.3 No Cross-Module Repository Access

Không:

```text
paymentService
    ↓
orderRepository.UpdateStatus()
```

Thay vào đó:

```text
Payment
   ↓
PaymentSucceeded
   ↓
Event Bus
   ↓
Order Handler
```

hoặc sử dụng application boundary được định nghĩa rõ.

---

## 3.4 No Circular Dependency

Dependency phải hướng một chiều.

Không được:

```text
Order → Payment → Order
```

qua direct package dependencies.

---

## 3.5 Historical Data Must Be Reconstructable

Order cũ không được phụ thuộc vào dữ liệu Catalog hiện tại.

Phải snapshot:

```text
product_name
sku_name
unit_price
seller
attributes
shipping_address
discount
shipping_fee
```

---

## 3.6 Never Trust the Client

Không tin:

```text
price
seller_id
discount
stock
order_total
shipping_fee
payment_status
role
```

do frontend gửi lên.

Server phải tự xác minh.

---

# 4. V2 BUSINESS MODULES

V2 có **14 business modules chính**.

```text
1. Auth
2. User
3. Seller
4. Catalog
5. Inventory
6. Cart
7. Voucher
8. Order
9. Payment
10. Shipping
11. Return
12. Review
13. Notification
14. Analytics
```

Ngoài ra có supporting capabilities:

```text
Search
Media
Audit
Idempotency
Outbox
Inbox
Observability
Rate Limiting
```

---

# 5. HIGH-LEVEL ARCHITECTURE

```text
                         CLIENT
                           |
                           v
                    +-------------+
                    |   HTTP API  |
                    +------+------+
                           |
          +----------------+----------------+
          |                                 |
          v                                 v
   Application Layer                    Middleware
          |                                 |
          |                      Auth / RateLimit / Trace
          |
          v
+------------------------------------------------------+
|                  BUSINESS MODULES                    |
|                                                      |
| Auth        User        Seller       Catalog         |
| Inventory   Cart        Voucher      Order           |
| Payment     Shipping    Return       Review          |
| Notification            Analytics                    |
+------------------------------------------------------+
          |
          +-------------+-------------+
          |             |             |
          v             v             v
      PostgreSQL       Redis       RabbitMQ
                                      |
                                      v
                                   Workers
                                      |
                +---------------------+----------------+
                |                     |                |
                v                     v                v
          Elasticsearch             MinIO         External APIs
```

---

# 6. PROPOSED PROJECT STRUCTURE

```text
nexus-commerce/
│
├── cmd/
│   ├── api/
│   │   └── main.go
│   ├── worker/
│   │   └── main.go
│   └── migrate/
│       └── main.go
│
├── internal/
│
│   ├── platform/
│   │   ├── config/
│   │   ├── database/
│   │   ├── redis/
│   │   ├── messaging/
│   │   ├── logging/
│   │   ├── telemetry/
│   │   ├── httpserver/
│   │   ├── security/
│   │   └── shutdown/
│   │
│   ├── auth/
│   ├── user/
│   ├── seller/
│   ├── catalog/
│   ├── inventory/
│   ├── cart/
│   ├── voucher/
│   ├── order/
│   ├── payment/
│   ├── shipping/
│   ├── return/
│   ├── review/
│   ├── notification/
│   └── analytics/
│
├── internal/support/
│   ├── audit/
│   ├── idempotency/
│   ├── outbox/
│   ├── inbox/
│   ├── search/
│   ├── media/
│   └── ratelimit/
│
├── migrations/
├── sql/
│   └── queries/
│
├── contracts/
│   ├── http/
│   └── events/
│
├── tests/
│   ├── integration/
│   ├── concurrency/
│   ├── security/
│   ├── failure/
│   ├── e2e/
│   └── load/
│
├── deployments/
│   ├── docker/
│   └── kubernetes/
│
├── scripts/
├── docs/
│   ├── requirements.md
│   ├── architecture.md
│   ├── erd.md
│   ├── api.md
│   ├── security.md
│   ├── testing.md
│   ├── deployment.md
│   └── adr/
│
├── docker-compose.yml
├── Dockerfile
├── Makefile
├── go.mod
└── go.sum
```

---

# 7. STANDARD MODULE STRUCTURE

Mỗi business module nên có cấu trúc:

```text
internal/order/
├── domain/
├── application/
├── infrastructure/
└── transport/
    └── http/
```

## Domain

Chứa:

```text
entities
value objects
domain errors
business rules
state machines
domain events
```

Không phụ thuộc HTTP/PostgreSQL/Redis/RabbitMQ.

---

## Application

Chứa:

```text
use cases
commands
queries
orchestration
ports/interfaces
```

---

## Infrastructure

Chứa:

```text
PostgreSQL repository
Redis adapter
RabbitMQ publisher
external provider adapters
```

---

## Transport

Chứa:

```text
HTTP handlers
request DTO
response DTO
validation
```

---

# 8. MODULE 1 — AUTH

Auth chịu trách nhiệm:

```text
credentials
password hashes
sessions
access tokens
refresh tokens
token revocation
email verification
password reset
authentication
```

Không sở hữu:

```text
profile
address
seller shop
```

---

# 9. AUTH ENTITIES

```text
Credential
Session
RefreshToken
VerificationToken
PasswordResetToken
```

Security requirements:

* password hashing;
* access token short-lived;
* refresh token rotation;
* refresh token revocation;
* session tracking;
* brute-force protection;
* credential stuffing protection;
* rate limiting;
* secure cookies where relevant;
* no token logging.

---

# 10. AUTH FLOW

```text
POST /auth/login
        |
        v
Validate Credentials
        |
        v
Check Rate Limit
        |
        v
Verify Password
        |
        v
Create Session
        |
        v
Access Token
+
Refresh Token
```

---

# 11. REFRESH TOKEN ROTATION

```text
Refresh Token A
       |
       v
Use
       |
       +--> revoke A
       |
       +--> issue B
```

Reuse of A can indicate token theft.

System should be able to revoke the token family/session.

---

# 12. MODULE 2 — USER

User owns:

```text
users
profiles
addresses
preferences
```

Capabilities:

```text
Get Profile
Update Profile
Address CRUD
Default Address
Account Preferences
```

---

# 13. USER AUTHORIZATION

Example:

```text
GET /users/{id}
```

must ensure:

```text
authenticatedUser.ID == id
```

unless authorized administrator.

Prevent:

```text
IDOR
Broken Access Control
```

---

# 14. MODULE 3 — SELLER

Seller owns:

```text
seller_accounts
shops
shop_memberships
seller_status_histories
```

Seller lifecycle:

```text
PENDING
   |
   v
APPROVED
   |
   +--> SUSPENDED
   |
   +--> REJECTED
```

---

# 15. SHOP

Shop contains:

```text
id
seller_id
name
slug
description
logo
status
created_at
```

Catalog products belong to a Shop.

Seller module owns shop identity.

Catalog only references:

```text
shop_id
```

---

# 16. SELLER OWNERSHIP

Seller A must never modify:

```text
Seller B product
Seller B inventory
Seller B shipment
Seller B order operation
```

Authorization must validate ownership server-side.

---

# 17. MODULE 4 — CATALOG

Catalog owns:

```text
categories
brands
products
product_variants
skus
attributes
attribute_values
product_images metadata
```

Catalog is source of truth for:

```text
current product status
current SKU information
current selling price
```

---

# 18. PRODUCT MODEL

```text
Product
 ├── ProductVariant
 │      └── SKU
 ├── Images
 ├── Category
 ├── Brand
 └── Attributes
```

---

# 19. PRODUCT STATE

Example:

```text
DRAFT
  |
  v
PENDING_REVIEW
  |
  v
ACTIVE
  |
  +--> SUSPENDED
  |
  +--> ARCHIVED
```

Products not ACTIVE cannot normally be purchased.

---

# 20. SKU

SKU contains:

```text
sku_id
product_id
variant attributes
current price
status
weight
dimensions
```

Catalog does not own stock.

---

# 21. SEARCH

Search is a supporting capability.

Source of truth:

```text
Catalog PostgreSQL
```

Read model:

```text
Elasticsearch
```

Flow:

```text
Catalog transaction
      |
ProductUpdated
      |
Outbox
      |
RabbitMQ
      |
Search Indexer
      |
Elasticsearch
```

---

# 22. SEARCH API

Example:

```text
GET /products/search
```

Support:

```text
keyword
category
brand
price range
rating
seller
sort
pagination
```

Search result can be eventually consistent.

Checkout must never trust search index for current price.

---

# 23. MEDIA CAPABILITY

Media handles:

```text
product images
shop logos
review images
return evidence
```

Object storage:

```text
MinIO
```

development.

Production-compatible:

```text
S3
```

---

# 24. MEDIA SECURITY

Validate:

```text
size
content type
extension
authorization
generated filename
metadata
```

Do not trust filename from user.

Need orphan cleanup strategy.

---

# 25. MODULE 5 — INVENTORY

Inventory owns:

```text
warehouses
inventory_stocks
inventory_reservations
stock_movements
```

Core invariant:

```text
available_quantity >= 0
reserved_quantity >= 0
```

---

# 26. INVENTORY MODEL

```text
InventoryStock

sku_id
warehouse_id
available_quantity
reserved_quantity
version
updated_at
```

---

# 27. INVENTORY RESERVATION

State:

```text
ACTIVE
   |
   +--> COMMITTED
   |
   +--> RELEASED
   |
   +--> EXPIRED
```

Reservation has:

```text
expires_at
```

---

# 28. ATOMIC STOCK RESERVATION

Example SQL:

```sql
UPDATE inventory_stocks
SET
    available_quantity = available_quantity - $1,
    reserved_quantity = reserved_quantity + $1
WHERE sku_id = $2
  AND warehouse_id = $3
  AND available_quantity >= $1;
```

If:

```text
rows affected = 1
```

reservation succeeds.

Otherwise:

```text
OUT_OF_STOCK
```

---

# 29. INVENTORY CONCURRENCY TEST

Scenario:

```text
Stock = 50

1000 simultaneous purchase attempts
```

Expected:

```text
success <= 50
failure >= 950
stock never negative
```

Must remain correct across:

```text
multiple API instances
```

A Go mutex alone is insufficient.

---

# 30. INVENTORY MOVEMENT

Every stock change should produce history:

```text
INBOUND
RESERVE
RELEASE
COMMIT
ADJUSTMENT
RETURN
```

Stock movement improves:

```text
auditability
reconciliation
debugging
```

---

# 31. MODULE 6 — CART

Cart owns:

```text
carts
cart_items
```

Capabilities:

```text
Add Item
Update Quantity
Remove Item
Select Item
Clear Selected Items
Get Cart
```

---

# 32. CART IS NOT SOURCE OF TRUTH

Cart may store:

```text
display price
product name
thumbnail
```

as cache.

But checkout must reload:

```text
SKU
price
status
seller
availability
```

from authoritative modules.

---

# 33. MODULE 7 — VOUCHER

Voucher owns:

```text
vouchers
voucher_conditions
voucher_usages
voucher_reservations
```

Voucher types:

```text
percentage
fixed amount
seller voucher
platform voucher
shipping voucher
```

---

# 34. VOUCHER RULES

Possible constraints:

```text
minimum order
maximum discount
start time
expiry
total usage limit
per-user limit
seller restriction
category restriction
SKU restriction
```

---

# 35. VOUCHER CONCURRENCY

Scenario:

```text
voucher uses remaining = 1

100 concurrent checkouts
```

Expected:

```text
exactly <= 1 successful reservation
```

Must be database-safe.

---

# 36. VOUCHER RESERVATION

During checkout:

```text
AVAILABLE
   |
   v
RESERVED
   |
   +--> USED
   |
   +--> RELEASED
   |
   +--> EXPIRED
```

Prevents voucher oversubscription.

---

# 37. MODULE 8 — ORDER

Order owns:

```text
orders
order_items
order_status_histories
```

Order is source of truth for:

```text
order lifecycle
historical purchase data
```

---

# 38. ORDER STATE MACHINE

Main flow:

```text
CREATED
   |
   v
AWAITING_PAYMENT
   |
   v
PAID
   |
   v
PROCESSING
   |
   v
READY_TO_SHIP
   |
   v
SHIPPED
   |
   v
DELIVERED
```

Failure/cancellation states:

```text
CANCELLED
PAYMENT_FAILED
RETURN_REQUESTED
RETURNED
REFUND_PENDING
REFUNDED
```

---

# 39. ORDER DOMAIN RULE

Only Order domain can transition order state.

Not:

```text
Payment
Shipping
Return
Admin SQL
```

Instead they communicate requested facts/events.

Example:

```text
PaymentSucceeded
      |
      v
Order
      |
MarkPaid()
```

---

# 40. INVALID TRANSITION

Example:

```text
DELIVERED → AWAITING_PAYMENT
```

must fail.

State transitions should be domain methods:

```go
order.MarkPaid()
order.MarkProcessing()
order.MarkShipped()
order.MarkDelivered()
order.Cancel()
```

---

# 41. ORDER SNAPSHOT

OrderItem snapshot:

```text
product_id
sku_id
shop_id

product_name
sku_name
seller_name

original_price
unit_price
discount_amount
quantity

attributes
```

Order snapshot:

```text
shipping_address
billing details
subtotal
voucher_discount
shipping_fee
total
currency
```

---

# 42. CHECKOUT APPLICATION SERVICE

Checkout is an application-level orchestrator.

It is not an Order entity responsibility.

Flow:

```text
Customer
   |
   v
CheckoutService
   |
   +--> Cart
   |
   +--> Catalog
   |
   +--> Voucher
   |
   +--> Inventory
   |
   +--> Shipping
   |
   +--> Order
   |
   +--> Payment
```

---

# 43. CHECKOUT FLOW V2

```text
1. Authenticate Customer

2. Load Selected Cart Items

3. Load Current SKU Data

4. Validate Product/SKU Status

5. Recalculate Current Price

6. Validate Seller

7. Calculate Shipping

8. Validate Voucher

9. Reserve Voucher

10. Reserve Inventory

11. Calculate Final Total

12. Create Order + Snapshots

13. Create Payment Transaction

14. Clear Selected Cart Items

15. Commit Transaction Boundaries

16. Return Payment URL / Order
```

---

# 44. CHECKOUT FAILURE MATRIX

## Catalog invalid

```text
Stop checkout
```

No reservation.

---

## Voucher invalid

```text
Stop checkout
```

---

## Inventory insufficient

```text
Release voucher
Stop checkout
```

---

## Order creation fails

```text
Release inventory
Release voucher
```

---

## Payment creation fails

Depending on policy:

```text
Order remains AWAITING_PAYMENT
```

and customer can retry payment,

or:

```text
Cancel Order
Release inventory
Release voucher
```

Policy must be explicit.

---

# 45. CHECKOUT CRASH RECOVERY

Suppose:

```text
Inventory reserved
```

then API crashes before Order creation.

Immediate compensation is impossible.

Safety mechanism:

```text
Reservation TTL
+
Expiration Worker
```

Worker scans expired reservations and releases them.

---

# 46. MODULE 9 — PAYMENT

Payment owns:

```text
payment_transactions
payment_attempts
payment_webhooks
refund_transactions
```

Payment states:

```text
PENDING
SUCCESS
FAILED
EXPIRED
CANCELLED
```

---

# 47. PAYMENT PROVIDER ABSTRACTION

```go
type Provider interface {
    CreatePayment(...)
    QueryPayment(...)
    Refund(...)
    VerifyWebhook(...)
}
```

Initial:

```text
Mock Provider
```

Optional real integration:

```text
VNPay
MoMo
Stripe
```

---

# 48. PAYMENT TIMEOUT RULE

Critical principle:

```text
TIMEOUT != PAYMENT_FAILED
```

A timeout only means:

```text
we do not know the result
```

Payment may already have succeeded at provider.

Therefore transaction may stay:

```text
PENDING
```

until reconciliation.

---

# 49. PAYMENT WEBHOOK

Endpoint:

```text
POST /webhooks/payments/{provider}
```

Must verify:

```text
signature
timestamp
provider transaction ID
duplicate webhook
replay attempt
```

---

# 50. PAYMENT IDEMPOTENCY

If:

```text
PaymentSucceeded
```

arrives 10 times:

Expected:

```text
payment marked success once
order marked paid once
inventory committed once
notification generated once
```

---

# 51. PAYMENT RECONCILIATION

Worker:

```text
Find stale PENDING payments
       |
       v
Query Provider
       |
   +---+---+
   |       |
SUCCESS  FAILED
```

Handles:

```text
webhook lost
HTTP response lost
provider delay
temporary network failure
```

---

# 52. MODULE 10 — SHIPPING

Shipping is a first-class business module in V2.

It owns:

```text
shipments
shipment_items
shipping_rates
tracking_events
shipping_addresses snapshot
shipping_provider_transactions
```

---

# 53. SHIPPING RESPONSIBILITIES

Shipping handles:

```text
shipping fee calculation
carrier selection
shipment creation
tracking number
pickup
shipping lifecycle
delivery confirmation
failed delivery
return-to-sender
```

---

# 54. SHIPPING STATE MACHINE

```text
PENDING
   |
   v
READY_FOR_PICKUP
   |
   v
PICKED_UP
   |
   v
IN_TRANSIT
   |
   v
OUT_FOR_DELIVERY
   |
   +--> DELIVERED
   |
   +--> DELIVERY_FAILED
             |
             v
      RETURN_TO_SENDER
```

---

# 55. ORDER VS SHIPPING OWNERSHIP

Shipping does not update Order table directly.

Example:

```text
ShipmentDelivered
       |
       v
Order Handler
       |
       v
order.MarkDelivered()
```

---

# 56. SHIPPING FEE

Shipping fee must be calculated server-side.

Inputs may include:

```text
origin
destination
weight
dimensions
carrier
shipping method
seller
voucher
```

Frontend-provided shipping fee cannot be trusted.

---

# 57. MULTI-SELLER SHIPMENT

One order may contain items from multiple shops.

Therefore:

```text
Order
 ├── Shipment A → Seller A
 └── Shipment B → Seller B
```

Do not assume:

```text
one order = one shipment
```

---

# 58. MODULE 11 — RETURN

Return owns the post-delivery return workflow.

Tables:

```text
return_requests
return_items
return_evidence
return_status_histories
return_shipments
```

Payment still owns monetary refund transaction.

---

# 59. RETURN STATE MACHINE

```text
REQUESTED
   |
   v
UNDER_REVIEW
   |
   +--> REJECTED
   |
   v
APPROVED
   |
   v
RETURNING
   |
   v
RECEIVED
   |
   v
REFUND_PENDING
   |
   v
COMPLETED
```

---

# 60. RETURN RESPONSIBILITIES

Handles:

```text
return eligibility
return reason
evidence
seller/admin approval
return shipment
inspection result
refund request
```

---

# 61. RETURN VS PAYMENT

Return determines:

```text
whether refund should happen
how much should be refunded
```

Payment executes:

```text
money refund
```

Flow:

```text
Return Approved
     |
     v
RefundRequested
     |
     v
Payment
     |
     v
RefundSucceeded
     |
     v
Return Completed
```

---

# 62. PARTIAL RETURN

Order:

```text
3 x SKU-A
2 x SKU-B
```

Customer can potentially return:

```text
1 x SKU-A
```

Return must operate at:

```text
OrderItem quantity
```

not only entire Order.

---

# 63. RETURN INVENTORY

Returned item does not automatically become sellable stock.

Possible state:

```text
RETURN_RECEIVED
       |
       +--> RESTOCK
       |
       +--> DAMAGED
       |
       +--> QUARANTINE
```

Inventory decides final stock movement.

---

# 64. MODULE 12 — REVIEW

Review owns:

```text
product_reviews
review_images
review_votes
seller_responses
review_moderation
```

---

# 65. REVIEW ELIGIBILITY

Only customer who actually purchased an item should normally review it.

Validation:

```text
OrderItem
+
Order DELIVERED
+
user_id
```

---

# 66. REVIEW MODEL

```text
review_id
user_id
product_id
sku_id
order_item_id

rating
content

status
created_at
updated_at
```

Rating:

```text
1..5
```

---

# 67. REVIEW SECURITY

Prevent:

```text
reviewing products never purchased
reviewing another user's order
mass assignment
spam
abuse
```

Review content must also be handled safely when rendered.

---

# 68. REVIEW AGGREGATION

Catalog can expose:

```text
average_rating
review_count
```

but Review remains authoritative.

Can maintain projection via events:

```text
ReviewCreated
ReviewUpdated
ReviewDeleted
```

---

# 69. MODULE 13 — NOTIFICATION

Notification owns:

```text
notifications
notification_templates
delivery_attempts
delivery_preferences
```

Channels:

```text
in-app
email
```

Optional:

```text
SMS
push
```

---

# 70. NOTIFICATION SHOULD BE EVENT-DRIVEN

Examples:

```text
OrderCreated
PaymentSucceeded
OrderShipped
ShipmentDelivered
ReturnApproved
RefundSucceeded
SellerApproved
PasswordResetRequested
```

Notification consumes these asynchronously.

---

# 71. NOTIFICATION FAILURE

Email provider outage must not make:

```text
checkout fail
payment fail
order fail
```

Notification is normally eventually delivered.

Use:

```text
retry
backoff
DLQ
```

---

# 72. MODULE 14 — ANALYTICS

Analytics consumes business events to build business metrics.

It should not run heavy reporting queries directly against transactional paths.

---

# 73. ANALYTICS EVENTS

Examples:

```text
UserRegistered
ProductViewed
ProductAddedToCart
CheckoutStarted
OrderCreated
OrderPaid
ShipmentDelivered
ReturnRequested
RefundSucceeded
```

---

# 74. ANALYTICS METRICS

Examples:

```text
GMV
orders/day
conversion rate
average order value
payment success rate
return rate
seller sales
top products
voucher usage
cart abandonment
```

---

# 75. ANALYTICS DATA MODEL

Initial implementation can use PostgreSQL tables:

```text
analytics_daily_sales
analytics_product_metrics
analytics_seller_metrics
analytics_funnel
```

V3 may later extract Analytics into independent service/store.

---

# 76. DOMAIN EVENTS

V2 introduces standardized domain events.

Examples:

```text
UserRegistered

SellerApproved

ProductCreated
ProductUpdated
ProductPriceChanged

InventoryReserved
InventoryReleased
InventoryCommitted

VoucherReserved
VoucherUsed

OrderCreated
OrderPaid
OrderCancelled
OrderDelivered

PaymentSucceeded
PaymentFailed
RefundSucceeded

ShipmentCreated
ShipmentShipped
ShipmentDelivered

ReturnRequested
ReturnApproved
ReturnCompleted

ReviewCreated
ReviewUpdated
```

---

# 77. EVENT ENVELOPE

```json
{
  "event_id": "uuid",
  "event_type": "OrderPaid",
  "event_version": 1,
  "aggregate_id": "order-id",
  "occurred_at": "...",
  "producer": "order",
  "correlation_id": "...",
  "payload": {}
}
```

---

# 78. TRANSACTIONAL OUTBOX

Problem:

```text
DB COMMIT success
RabbitMQ publish fail
```

Without Outbox:

```text
business state saved
event lost
```

---

# 79. OUTBOX FLOW

Inside same DB transaction:

```text
BEGIN

UPDATE business data

INSERT outbox_events

COMMIT
```

Worker:

```text
Outbox
   |
   v
RabbitMQ
   |
   v
mark published
```

---

# 80. OUTBOX TABLE

Example:

```text
id
aggregate_type
aggregate_id
event_type
event_version
payload
status
attempt_count
created_at
published_at
next_attempt_at
```

---

# 81. OUTBOX WORKER

Can use:

```sql
SELECT ...
FROM outbox_events
WHERE status = 'PENDING'
ORDER BY created_at
FOR UPDATE SKIP LOCKED
LIMIT 100;
```

Multiple workers can safely process batches.

---

# 82. AT-LEAST-ONCE DELIVERY

RabbitMQ may deliver duplicate messages.

Therefore:

```text
consumer must be idempotent
```

Never assume:

```text
exactly once
```

---

# 83. INBOX PATTERN

Consumer stores:

```text
consumer_name
event_id
processed_at
```

Constraint:

```text
UNIQUE(consumer_name, event_id)
```

Duplicate event becomes no-op.

---

# 84. EVENT VERSIONING

Never silently break event contracts.

Use:

```text
OrderPaid v1
OrderPaid v2
```

or backward-compatible schema evolution.

---

# 85. API IDEMPOTENCY

Critical write endpoints should support:

```text
Idempotency-Key
```

Examples:

```text
Checkout
Create Payment
Refund
Return Request
```

---

# 86. IDEMPOTENCY STORAGE

```text
idempotency_key
user_id
operation
request_hash
status
response_status
response_body
expires_at
```

---

# 87. IDEMPOTENCY BEHAVIOR

Same key + same body:

```text
return original result
```

Same key + different body:

```text
409 Conflict
```

Concurrent same-key requests:

```text
only one operation executes
```

---

# 88. REDIS

Redis may be used for:

```text
cache
rate limiting
session support
distributed coordination
short-lived data
```

Do not move critical source-of-truth data to Redis unnecessarily.

---

# 89. CACHE-ASIDE

```text
Request
  |
Redis
  |
 HIT ─────→ Response
  |
 MISS
  |
PostgreSQL
  |
Redis SET
  |
Response
```

---

# 90. CACHE INVALIDATION

Example:

```text
ProductUpdated
      |
      +--> delete product cache
      |
      +--> update Elasticsearch
```

Define key convention:

```text
catalog:product:{id}
catalog:sku:{id}
seller:shop:{id}
```

---

# 91. CACHE STAMPEDE

Hot key expiration can cause many DB queries.

Possible protections:

```text
TTL jitter
single-flight
short lock
background refresh
```

Apply only where needed.

---

# 92. RATE LIMITING

Distributed rate limiter via Redis.

Policies for:

```text
login
register
password reset
search
checkout
payment webhook
general API
```

Identity can be:

```text
IP
user ID
API client
```

---

# 93. RATE LIMITING ALGORITHM

Recommended:

```text
Token Bucket
```

Atomic operation through Redis Lua script.

Properties:

```text
distributed
atomic
configurable
multi-instance safe
```

---

# 94. HTTP REQUEST PIPELINE

```text
Request
  |
  v
Request ID
  |
  v
Tracing
  |
  v
Recovery
  |
  v
Security Headers
  |
  v
CORS
  |
  v
Rate Limit
  |
  v
Authentication
  |
  v
Authorization
  |
  v
Validation
  |
  v
Handler
  |
  v
Application
  |
  v
Domain
  |
  v
Repository
```

---

# 95. CONTEXT PROPAGATION

Go:

```text
HTTP Request
   |
context.Context
   |
Handler
   |
Application
   |
Repository
   |
pgx
```

Never replace request context with:

```go
context.Background()
```

inside request path unless intentional.

---

# 96. STRUCTURED LOGGING

Use:

```text
log/slog
```

Fields:

```text
timestamp
level
service
module
operation
request_id
trace_id
user_id
resource_id
status
latency_ms
error
```

---

# 97. DO NOT LOG

Never log:

```text
password
access token
refresh token
payment secrets
webhook secret
authorization header
raw credit-card data
```

---

# 98. AUDIT LOG

Audit:

```text
WHO
WHAT
WHEN
RESOURCE
RESULT
IP
REQUEST_ID
```

Important operations:

```text
login
password change
seller approval
product moderation
stock adjustment
order status change
refund
return decision
admin operation
```

---

# 99. SECURITY REQUIREMENTS

Protect against:

```text
SQL Injection
Broken Access Control
IDOR
XSS
CSRF where applicable
JWT attacks
credential stuffing
brute force
mass assignment
rate-limit bypass
webhook forgery
replay attacks
upload attacks
sensitive data exposure
```

---

# 100. AUTHORIZATION MODEL

Use:

```text
RBAC
+
Ownership
```

Roles:

```text
CUSTOMER
SELLER
ADMIN
SUPPORT
WAREHOUSE_MANAGER
```

Example:

```text
SELLER
```

permission alone is insufficient.

Need:

```text
seller owns shop
shop owns product
```

---

# 101. DATABASE ENGINEERING

PostgreSQL requirements:

```text
foreign keys
unique constraints
check constraints
indexes
transactions
MVCC understanding
locking
isolation
connection pooling
query analysis
```

---

# 102. DATABASE CONSTRAINTS

Examples:

```sql
CHECK (available_quantity >= 0)
```

```sql
CHECK (rating BETWEEN 1 AND 5)
```

```sql
UNIQUE (consumer_name, event_id)
```

Business correctness should be protected at multiple layers.

---

# 103. TRANSACTION RULE

Transactions belong to use cases requiring atomicity.

Not:

```text
one transaction per HTTP request blindly
```

Keep transactions:

```text
short
bounded
intentional
```

Avoid external network calls inside long DB transactions.

---

# 104. CONNECTION POOL

Configure:

```text
MaxConns
MinConns
MaxConnLifetime
MaxConnIdleTime
HealthCheckPeriod
```

Observe:

```text
pool utilization
wait duration
acquire failures
```

---

# 105. QUERY PERFORMANCE

Use:

```text
EXPLAIN
EXPLAIN ANALYZE
```

Monitor:

```text
full table scans
missing indexes
slow joins
lock waits
N+1 queries
```

---

# 106. OBSERVABILITY

V2 uses:

```text
OpenTelemetry
Prometheus
Grafana
Loki
```

Optional trace backend:

```text
Jaeger
Tempo
```

---

# 107. METRICS

HTTP:

```text
request count
error rate
latency
p50
p95
p99
```

Database:

```text
connections
query latency
transaction errors
deadlocks
```

Redis:

```text
hit ratio
latency
errors
```

RabbitMQ:

```text
queue depth
publish failures
consumer rate
redeliveries
DLQ depth
```

---

# 108. BUSINESS METRICS

Track:

```text
checkout success
checkout failure by reason
inventory reservation conflicts
payment success
payment pending
payment reconciliation
voucher failure
shipment delay
return rate
refund success
```

---

# 109. DISTRIBUTED-READY TRACING

Even though V2 is monolithic, spans should follow logical boundaries.

Example:

```text
Checkout
 |
 +-- Cart.GetItems
 |
 +-- Catalog.GetSKUs
 |
 +-- Voucher.Validate
 |
 +-- Inventory.Reserve
 |
 +-- Shipping.Calculate
 |
 +-- Order.Create
 |
 +-- Payment.Create
```

This makes V3 extraction much easier.

---

# 110. ERROR MODEL

Standard application errors:

```text
VALIDATION_ERROR
UNAUTHENTICATED
FORBIDDEN
NOT_FOUND
CONFLICT
OUT_OF_STOCK
VOUCHER_INVALID
INVALID_ORDER_TRANSITION
PAYMENT_PENDING
RATE_LIMITED
INTERNAL_ERROR
```

---

# 111. RETRY CLASSIFICATION

Errors should indicate:

```text
retryable
non-retryable
```

Examples:

```text
network timeout → maybe retryable

invalid voucher → non-retryable

invalid order transition → non-retryable
```

---

# 112. RETRY

Use:

```text
bounded retry
exponential backoff
jitter
```

Never infinite retry.

Do not retry non-idempotent external operations unless protected by idempotency.

---

# 113. DEAD LETTER QUEUE

Consumer failures after max attempts:

```text
main queue
    |
retry
    |
retry
    |
DLQ
```

DLQ needs operational visibility.

---

# 114. FAILURE IS EXPECTED

V2 must explicitly test:

```text
PostgreSQL unavailable
Redis unavailable
RabbitMQ unavailable
Elasticsearch unavailable
MinIO unavailable
payment provider timeout
email provider timeout
```

---

# 115. GRACEFUL DEGRADATION

Examples:

## Elasticsearch unavailable

Fallback:

```text
basic PostgreSQL search
```

or temporary Search unavailable response.

Checkout remains operational.

---

## Notification unavailable

Orders still complete.

Notifications retry later.

---

## Redis unavailable

Core transactional operations should remain correct where possible.

Performance may degrade.

---

# 116. HEALTH ENDPOINT

```text
GET /health
```

Answers:

```text
process alive?
```

Should usually be lightweight.

---

# 117. READINESS ENDPOINT

```text
GET /ready
```

Checks critical dependencies required to serve traffic.

Examples:

```text
PostgreSQL
critical configuration
```

---

# 118. GRACEFUL SHUTDOWN

On SIGTERM:

```text
mark not ready
      |
stop accepting traffic
      |
finish active HTTP requests
      |
stop accepting new jobs
      |
finish current jobs
      |
close RabbitMQ
      |
close Redis
      |
close PostgreSQL
      |
exit
```

---

# 119. WORKERS

V2 worker process handles:

```text
Outbox Publishing
Inbox Consumers
Inventory Expiration
Voucher Expiration
Payment Reconciliation
Notification Delivery
Search Indexing
Analytics Aggregation
Media Cleanup
```

Workers must support graceful shutdown.

---

# 120. WORKER CONCURRENCY

Use intentionally:

```text
goroutines
channels
errgroup
worker pools
semaphores
```

Avoid:

```text
go func()
```

everywhere without ownership/control.

---

# 121. TESTING PYRAMID

```text
           E2E
         /     \
   Integration
      /       \
   Unit Tests
```

Plus special test categories:

```text
concurrency
security
failure
load
```

---

# 122. UNIT TESTS

Test:

```text
domain rules
state machines
voucher calculations
shipping calculations
return eligibility
review eligibility
payment state changes
```

---

# 123. REPOSITORY INTEGRATION TESTS

Use real PostgreSQL through:

```text
testcontainers-go
```

Test:

```text
transactions
constraints
locks
queries
concurrency
```

---

# 124. API INTEGRATION TESTS

Test:

```text
authentication
authorization
validation
status codes
JSON contracts
idempotency
```

---

# 125. INVENTORY CONCURRENCY TEST

Mandatory:

```text
stock = 50

1000 clients
```

Expected:

```text
<= 50 reserved
stock >= 0
```

---

# 126. VOUCHER CONCURRENCY TEST

Mandatory:

```text
remaining uses = 1

100 clients
```

Expected:

```text
<= 1 succeeds
```

---

# 127. IDEMPOTENCY CONCURRENCY TEST

Send:

```text
100 Checkout requests
```

with identical:

```text
Idempotency-Key
```

Expected:

```text
one business operation
same response semantics
```

---

# 128. PAYMENT DUPLICATE TEST

Deliver:

```text
PaymentSucceeded
```

multiple times.

Expected:

```text
one order payment transition
one stock commit
one notification
```

---

# 129. SHIPPING DUPLICATE TEST

Deliver duplicate:

```text
ShipmentDelivered
```

Expected:

```text
Order marked delivered once
```

---

# 130. RETURN TESTS

Test:

```text
return another user's order
return non-delivered order
return quantity > purchased quantity
duplicate refund
partial return
expired return window
```

---

# 131. REVIEW TESTS

Test:

```text
review without purchase
review another user's order item
rating outside 1..5
duplicate review
deleted product
```

---

# 132. SECURITY TESTS

Include:

```text
IDOR
Broken Access Control
invalid JWT
expired JWT
refresh reuse
rate-limit bypass attempts
SQLi payloads
mass assignment
webhook forged signature
webhook replay
unsafe file upload
```

---

# 133. FAILURE TESTING

Manually or automated:

```text
kill RabbitMQ
kill Redis
kill Elasticsearch
stop Payment mock
kill worker
restart API
```

Verify recovery.

---

# 134. OUTBOX FAILURE TEST

Scenario:

```text
RabbitMQ DOWN

create order
```

Expected:

```text
order commits
outbox event stays pending
```

Start RabbitMQ.

Expected:

```text
event eventually published
```

---

# 135. CONSUMER CRASH TEST

Scenario:

```text
consumer writes business effect
crashes before ACK
```

Message redelivered.

Expected:

```text
Inbox prevents duplicate business effect
```

---

# 136. PAYMENT UNCERTAINTY TEST

Scenario:

```text
Provider executes payment
HTTP response lost
webhook delayed
```

Expected:

```text
payment remains pending
reconciliation resolves it
```

---

# 137. LOAD TESTING

Use:

```text
k6
```

Flows:

```text
browse
search
login
product details
cart
checkout
order history
```

Report:

```text
RPS
p50
p95
p99
error rate
CPU
RAM
DB pool
Redis
RabbitMQ
```

---

# 138. SYNTHETIC DATA

Optional benchmark dataset:

```text
100k users
10k sellers
1M products
multiple SKUs
1M orders
```

Goal is not to claim unrealistic production scale.

Goal is measuring behavior.

---

# 139. OBJECT STORAGE TESTS

Test:

```text
oversized file
wrong MIME type
unauthorized upload
duplicate upload
object upload succeeds but DB insert fails
orphan cleanup
```

---

# 140. DOCKER DEVELOPMENT STACK

```text
api
worker
postgres
redis
rabbitmq
elasticsearch
minio
prometheus
grafana
loki
```

Optional:

```text
jaeger/tempo
```

---

# 141. CI PIPELINE

GitHub Actions:

```text
format
   |
lint
   |
unit tests
   |
integration tests
   |
security checks
   |
build
   |
Docker image
   |
smoke test
```

---

# 142. STATIC ANALYSIS

Include:

```text
go vet
golangci-lint
```

Security:

```text
gosec
dependency vulnerability scanning
secret scanning
```

---

# 143. DATABASE MIGRATIONS

Use:

```text
golang-migrate
```

Migration files must be version-controlled.

Never manually change production schema without migration.

---

# 144. API DOCUMENTATION

Maintain:

```text
docs/api.md
```

or OpenAPI.

Document:

```text
method
path
authentication
authorization
request
response
errors
idempotency
rate limits
```

---

# 145. EVENT DOCUMENTATION

Each event:

```text
name
producer
consumers
payload
version
delivery semantics
idempotency expectations
```

---

# 146. ARCHITECTURE DECISION RECORDS

Recommended:

```text
ADR-001 PostgreSQL
ADR-002 Modular Monolith
ADR-003 sqlc vs ORM
ADR-004 Inventory Concurrency
ADR-005 RabbitMQ
ADR-006 Transactional Outbox
ADR-007 Inbox Pattern
ADR-008 Order Snapshots
ADR-009 Shipping Boundary
ADR-010 Return vs Payment Ownership
ADR-011 Search as Read Model
ADR-012 Why Not Microservices Yet
```

---

# 147. V2 MODULE COMMUNICATION MATRIX

```text
Auth
  → User

Seller
  → User

Catalog
  → Seller

Cart
  → Catalog

Checkout
  → Cart
  → Catalog
  → Voucher
  → Inventory
  → Shipping
  → Order
  → Payment

Payment
  → Order via event

Shipping
  → Order via event

Return
  → Order
  → Payment via workflow/event
  → Inventory via event

Review
  → Order verification
  → Catalog reference

Notification
  ← domain events

Analytics
  ← domain events

Search
  ← Catalog/Review events
```

No circular repository dependency.

---

# 148. DATA OWNERSHIP MATRIX

```text
Auth
credentials
sessions
refresh_tokens

User
users
profiles
addresses

Seller
seller_accounts
shops
shop_memberships

Catalog
products
skus
categories
brands
attributes

Inventory
inventory_stocks
reservations
stock_movements

Cart
carts
cart_items

Voucher
vouchers
usages
reservations

Order
orders
order_items
status_histories

Payment
payment_transactions
refund_transactions
webhooks

Shipping
shipments
shipment_items
tracking_events

Return
return_requests
return_items
return_status_histories

Review
reviews
review_images
review_votes

Notification
notifications
delivery_attempts

Analytics
analytics projections
```

---

# 149. CORE BUSINESS INVARIANTS

The system must guarantee:

```text
Inventory never negative.

Voucher usage never exceeds limit.

Order total is calculated server-side.

Historical order data never changes with Catalog.

Only Order changes Order lifecycle.

Payment success produces one business effect.

Refund executes at most once.

Seller cannot access another seller's resources.

Customer cannot access another customer's private resources.

One purchased order item cannot be reviewed by unauthorized user.

Return quantity cannot exceed eligible purchased quantity.

Shipment cannot belong to unrelated seller/order items.

Duplicate events cannot duplicate side effects.

Committed DB changes cannot lose required domain events.
```

---

# 150. V2 ROADMAP

---

## PHASE V2.1 — FOUNDATION & ARCHITECTURE

```text
ECOM-ARCH-001 Requirements Baseline
ECOM-ARCH-002 Module Boundaries
ECOM-ARCH-003 Dependency Rules
ECOM-ARCH-004 Application Layer Design
ECOM-ARCH-005 Domain Event Standard
ECOM-ARCH-006 Error Model
ECOM-ARCH-007 HTTP Architecture
ECOM-ARCH-008 Worker Architecture
ECOM-ARCH-009 Observability Architecture
ECOM-ARCH-010 Security Architecture
```

---

## PHASE V2.2 — DATA FOUNDATION

```text
ECOM-DATA-001 PostgreSQL Bootstrap
ECOM-DATA-002 Migration Infrastructure
ECOM-DATA-003 Core Schema
ECOM-DATA-003A Identity Data
ECOM-DATA-003B Commerce Data
ECOM-DATA-003C Catalog & Inventory Data
ECOM-DATA-003D Order & Payment Data
ECOM-DATA-003E Shipping & Return Data
ECOM-DATA-003F Review & Notification Data
ECOM-DATA-004 Constraints
ECOM-DATA-005 Index Strategy
ECOM-DATA-006 sqlc Setup
ECOM-DATA-007 Repository Integration Tests
```

---

## PHASE V2.3 — IDENTITY & SECURITY

```text
ECOM-AUTH-001 Registration
ECOM-AUTH-002 Password Hashing
ECOM-AUTH-003 Login
ECOM-AUTH-004 Access Token
ECOM-AUTH-005 Refresh Token
ECOM-AUTH-006 Rotation
ECOM-AUTH-007 Revocation
ECOM-AUTH-008 Sessions
ECOM-AUTH-009 Password Reset
ECOM-AUTH-010 Email Verification
ECOM-AUTH-011 RBAC
ECOM-AUTH-012 Permission Authorization
ECOM-AUTH-013 Ownership Authorization
ECOM-AUTH-014 Auth Rate Limiting
ECOM-AUTH-015 Security Tests
```

---

## PHASE V2.4 — SELLER & CATALOG

```text
ECOM-SELLER-001 Seller Registration
ECOM-SELLER-002 Seller Approval
ECOM-SELLER-003 Shop Management

ECOM-CAT-001 Category
ECOM-CAT-002 Brand
ECOM-CAT-003 Product
ECOM-CAT-004 Product Variant
ECOM-CAT-005 SKU
ECOM-CAT-006 Attributes
ECOM-CAT-007 Product Images
ECOM-CAT-008 Product Lifecycle
ECOM-CAT-009 Seller Ownership
ECOM-CAT-010 Catalog Query
ECOM-CAT-011 Catalog Integration Tests
```

---

## PHASE V2.5 — INVENTORY & CART

```text
ECOM-INV-001 Inventory Model
ECOM-INV-002 Stock Adjustment
ECOM-INV-003 Stock Movement
ECOM-INV-004 Atomic Reservation
ECOM-INV-005 Commit Reservation
ECOM-INV-006 Release Reservation
ECOM-INV-007 Reservation Expiry
ECOM-INV-008 Concurrent Reservation Test
ECOM-INV-009 Inventory Failure Tests

ECOM-CART-001 Cart CRUD
ECOM-CART-002 Cart Validation
ECOM-CART-003 Checkout Selection
```

---

## PHASE V2.6 — VOUCHER

```text
ECOM-VOUCHER-001 Voucher Model
ECOM-VOUCHER-002 Validation Engine
ECOM-VOUCHER-003 Discount Calculation
ECOM-VOUCHER-004 Usage Limits
ECOM-VOUCHER-005 Reservation
ECOM-VOUCHER-006 Release
ECOM-VOUCHER-007 Concurrent Usage Test
```

---

## PHASE V2.7 — ORDER & CHECKOUT

```text
ECOM-ORDER-001 Order Model
ECOM-ORDER-002 Order Snapshot
ECOM-ORDER-003 State Machine
ECOM-ORDER-004 Status History

ECOM-CHECKOUT-001 Cart Revalidation
ECOM-CHECKOUT-002 Price Calculation
ECOM-CHECKOUT-003 Voucher Integration
ECOM-CHECKOUT-004 Inventory Reservation
ECOM-CHECKOUT-005 Shipping Calculation
ECOM-CHECKOUT-006 Order Creation
ECOM-CHECKOUT-007 Failure Compensation
ECOM-CHECKOUT-008 End-to-End Checkout
```

---

## PHASE V2.8 — PAYMENT

```text
ECOM-PAY-001 Payment Model
ECOM-PAY-002 Mock Provider
ECOM-PAY-003 Create Payment
ECOM-PAY-004 Webhook
ECOM-PAY-005 Signature Verification
ECOM-PAY-006 Duplicate Protection
ECOM-PAY-007 Payment Events
ECOM-PAY-008 Reconciliation
ECOM-PAY-009 Refund
```

---

## PHASE V2.9 — SHIPPING

```text
ECOM-SHIP-001 Shipping Domain
ECOM-SHIP-002 Shipping Rate
ECOM-SHIP-003 Multi-Seller Shipment
ECOM-SHIP-004 Shipment Creation
ECOM-SHIP-005 Tracking
ECOM-SHIP-006 Shipment State Machine
ECOM-SHIP-007 Order Integration
ECOM-SHIP-008 Delivery Failure
ECOM-SHIP-009 Shipping Tests
```

---

## PHASE V2.10 — RETURN & REFUND

```text
ECOM-RETURN-001 Return Model
ECOM-RETURN-002 Eligibility
ECOM-RETURN-003 Return Request
ECOM-RETURN-004 Seller/Admin Decision
ECOM-RETURN-005 Return Shipment
ECOM-RETURN-006 Inspection
ECOM-RETURN-007 Partial Return
ECOM-RETURN-008 Refund Request
ECOM-RETURN-009 Inventory Restock
ECOM-RETURN-010 Return Tests
```

---

## PHASE V2.11 — REVIEW

```text
ECOM-REVIEW-001 Review Model
ECOM-REVIEW-002 Purchase Verification
ECOM-REVIEW-003 Create Review
ECOM-REVIEW-004 Update/Delete Review
ECOM-REVIEW-005 Review Images
ECOM-REVIEW-006 Rating Aggregation
ECOM-REVIEW-007 Moderation
ECOM-REVIEW-008 Review Security Tests
```

---

## PHASE V2.12 — EVENT-DRIVEN FOUNDATION

```text
ECOM-EVT-001 RabbitMQ Bootstrap
ECOM-EVT-002 Event Envelope
ECOM-EVT-003 Outbox Schema
ECOM-EVT-004 Outbox Publisher
ECOM-EVT-005 Retry Strategy
ECOM-EVT-006 DLQ
ECOM-EVT-007 Inbox
ECOM-EVT-008 Idempotent Consumer
ECOM-EVT-009 Event Versioning
ECOM-EVT-010 Event Failure Tests
```

---

## PHASE V2.13 — NOTIFICATION

```text
ECOM-NOTIF-001 Notification Model
ECOM-NOTIF-002 Templates
ECOM-NOTIF-003 Event Consumers
ECOM-NOTIF-004 Email Adapter
ECOM-NOTIF-005 Retry
ECOM-NOTIF-006 Delivery History
```

---

## PHASE V2.14 — SEARCH & MEDIA

```text
ECOM-SEARCH-001 Elasticsearch
ECOM-SEARCH-002 Catalog Index
ECOM-SEARCH-003 Event Indexer
ECOM-SEARCH-004 Search API
ECOM-SEARCH-005 Filtering
ECOM-SEARCH-006 Reindex

ECOM-MEDIA-001 MinIO
ECOM-MEDIA-002 Upload
ECOM-MEDIA-003 Validation
ECOM-MEDIA-004 Authorization
ECOM-MEDIA-005 Orphan Cleanup
```

---

## PHASE V2.15 — ANALYTICS

```text
ECOM-ANALYTICS-001 Analytics Events
ECOM-ANALYTICS-002 Event Consumer
ECOM-ANALYTICS-003 Sales Metrics
ECOM-ANALYTICS-004 Seller Metrics
ECOM-ANALYTICS-005 Funnel Metrics
ECOM-ANALYTICS-006 Analytics API
```

---

## PHASE V2.16 — CACHE & IDEMPOTENCY

```text
ECOM-CACHE-001 Redis Cache
ECOM-CACHE-002 Invalidation
ECOM-CACHE-003 Stampede Protection

ECOM-IDEMP-001 Idempotency Storage
ECOM-IDEMP-002 HTTP Middleware
ECOM-IDEMP-003 Concurrent Idempotency Test
```

---

## PHASE V2.17 — OBSERVABILITY

```text
ECOM-OBS-001 Structured Logging
ECOM-OBS-002 Prometheus
ECOM-OBS-003 OpenTelemetry
ECOM-OBS-004 Grafana
ECOM-OBS-005 Loki
ECOM-OBS-006 Tracing
ECOM-OBS-007 Business Metrics
ECOM-OBS-008 Alerts
```

---

## PHASE V2.18 — PRODUCTION TESTING

```text
ECOM-TEST-001 Integration Suite
ECOM-TEST-002 Inventory Concurrency
ECOM-TEST-003 Voucher Concurrency
ECOM-TEST-004 Idempotency
ECOM-TEST-005 Payment Failure
ECOM-TEST-006 Broker Failure
ECOM-TEST-007 Consumer Crash
ECOM-TEST-008 Security
ECOM-TEST-009 Load Test
ECOM-TEST-010 Full E2E
```

---

## PHASE V2.19 — DEPLOYMENT & HARDENING

```text
ECOM-OPS-001 Production Dockerfile
ECOM-OPS-002 Docker Compose
ECOM-OPS-003 Configuration Validation
ECOM-OPS-004 Graceful Shutdown
ECOM-OPS-005 Health
ECOM-OPS-006 Readiness
ECOM-OPS-007 CI
ECOM-OPS-008 Security Scanning
ECOM-OPS-009 Backup
ECOM-OPS-010 Restore Test
ECOM-OPS-011 Deployment Documentation
```

---

# 151. TICKET TEMPLATE

Every engineering ticket should contain:

```text
Ticket ID
Title

Problem
Context

Goal

Functional Requirements
Non-Functional Requirements

Scope
Out of Scope

Architecture

Domain Rules

Data Model

API Contract

Transaction Boundary

Concurrency Concerns

Failure Cases

Security Concerns

Observability

Implementation Tasks

Unit Tests
Integration Tests
Concurrency Tests
Failure Tests

Acceptance Criteria

Definition of Done
```

---

# 152. ENGINEERING WORKFLOW

Every significant feature:

```text
Research
   ↓
Design
   ↓
Review
   ↓
Implement
   ↓
Unit Test
   ↓
Integration Test
   ↓
Failure Test
   ↓
Benchmark
   ↓
Code Review
   ↓
Documentation
```

---

# 153. REVIEW QUESTIONS

Every ticket should answer:

```text
Why does this feature belong to this module?

Who owns the data?

What is the transaction boundary?

What happens under concurrency?

What happens when PostgreSQL fails?

What happens when Redis fails?

What happens when RabbitMQ fails?

Can requests be duplicated?

Can events be duplicated?

Can operations be retried safely?

What authorization is required?

What should be logged?

What should be measured?

How can we prove correctness?
```

---

# 154. V2 DEFINITION OF DONE

Nexus-Commerce V2 is complete only when:

## Business

```text
Customer can complete purchase lifecycle.

Seller can manage products and inventory.

Payment works reliably.

Orders can be shipped.

Customer can receive shipment.

Customer can request returns.

Refund can be completed.

Customer can review purchased products.

Search works.

Notifications work.

Analytics projections exist.
```

## Correctness

```text
No negative inventory.

No voucher oversubscription.

No duplicated payment effect.

No duplicated refund.

No invalid order transition.

No unauthorized seller access.

No unauthorized customer access.
```

## Reliability

```text
Outbox prevents lost events.

Inbox prevents duplicate effects.

Workers recover after crash.

Reservations expire safely.

Payment reconciliation works.
```

## Security

```text
Authentication secure.

Authorization + ownership enforced.

Rate limits enabled.

Webhooks verified.

Uploads validated.

Secrets not committed.

Audit logging exists.
```

## Observability

```text
Structured logs.

Metrics.

Tracing.

Dashboards.

Business metrics.

Failure visibility.
```

## Testing

```text
Unit tests.

Integration tests.

Concurrency tests.

Security tests.

Failure tests.

E2E tests.

Load tests.
```

## Operations

```text
Docker stable.

Docker Compose stable.

CI passes.

Graceful shutdown works.

Health/readiness work.

Backup and restore tested.
```

---

# 155. V2 → V3 GATE

Do not start V3 until V2 proves:

```text
Module boundaries are stable.

No cross-module direct DB mutation.

Core invariants have concurrency tests.

Outbox is production-ready.

Consumers are idempotent.

Event contracts are documented.

Observability works.

Failure recovery works.

Docker environment is stable.

Business workflow is complete.
```

Then perform:

```text
Measure
   ↓
Find high-value extraction candidate
   ↓
Extract one module
   ↓
Observe
   ↓
Stabilize
   ↓
Extract next
```

---

# 156. EXPECTED V3 EXTRACTION ORDER

Recommended:

```text
1. Notification
2. Search
3. Payment
4. API Gateway
5. Inventory
6. Order
7. Shipping
```

Not every V2 module needs to become a service.

Possible V3 Commerce Core:

```text
User
Seller
Cart
Voucher
Review
Return
```

until further separation is justified.

---

# 157. FINAL V2 ARCHITECTURE

```text
                         CLIENT
                           |
                           v
                    NEXUS API
                           |
      +--------------------+---------------------+
      |                    |                     |
      v                    v                     v

 Identity Domains      Commerce Domains       Supporting
      |                    |                     |
 Auth                  Seller                Search
 User                  Catalog               Media
                       Inventory             Audit
                       Cart                  Idempotency
                       Voucher               Outbox
                       Order                 Inbox
                       Payment               Rate Limit
                       Shipping              Observability
                       Return
                       Review
                       Notification
                       Analytics

                           |
          +----------------+----------------+
          |                |                |
          v                v                v
     PostgreSQL          Redis          RabbitMQ
                                            |
                         +------------------+---------------+
                         |                  |               |
                         v                  v               v
                    Elasticsearch         MinIO          Workers
```

---

# 158. WHAT V2 SHOULD TEACH

After completing V2, the developer should be able to explain:

```text
How to design modular boundaries.

How to establish data ownership.

Why Order snapshots are necessary.

How database concurrency protects inventory.

Why a mutex is insufficient across instances.

How idempotency prevents duplicate writes.

Why payment timeout is not payment failure.

How payment reconciliation works.

Why Transactional Outbox exists.

Why at-least-once delivery requires idempotency.

How Inbox works.

How Eventual Consistency works.

How Search differs from source of truth.

How shipping differs from Order.

How Return differs from Refund.

How ownership authorization prevents IDOR.

How Redis should and should not be used.

How graceful degradation works.

How observability helps debug failures.

How to test partial failure.

Why microservices are intentionally postponed.
```

---

# 159. FINAL V2 GOAL

Nexus-Commerce V2 should ultimately be describable as:

> **A production-grade, business-complete, event-driven multi-vendor e-commerce backend implemented as a modular monolith, designed to guarantee transactional correctness, concurrency safety, payment reliability, secure authorization, asynchronous event delivery, operational observability and clean future migration toward distributed microservices.**

The defining philosophy is:

```text
Correctness before scale.

Boundaries before microservices.

Transactions before distributed transactions.

Recovery before retries.

Idempotency before asynchronous processing.

Observability before distribution.

Measure first.

Distribute later.
```
