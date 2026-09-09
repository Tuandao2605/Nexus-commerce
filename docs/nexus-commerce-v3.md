# NEXUS-COMMERCE V3

## Distributed Microservices, Event Streaming & Cloud-Native E-Commerce Platform

**Previous Stage:** Nexus-Commerce V2 — Business-Complete Event-Driven Modular Monolith
**Current Stage:** V3 — Selective Distributed Microservices
**Future Stage:** V3+ — Intelligent Commerce / Large-Scale Data Platform

**Backend:** Go
**Architecture:** Distributed Event-Driven Microservices
**Transactional Broker:** RabbitMQ
**Event Streaming Platform:** Apache Kafka
**Primary Database:** PostgreSQL
**Cache / Distributed Coordination:** Redis
**Search Engine:** Elasticsearch
**Object Storage:** MinIO / S3-compatible
**Observability:** OpenTelemetry + Prometheus + Grafana + Loki + Tempo/Jaeger
**Orchestration:** Kubernetes
**Deployment:** Docker + Helm
**Infrastructure as Code:** Terraform

---

# 1. V3 VISION

Nexus-Commerce V3 là bước chuyển từ:

```text id="44138"
Event-Driven Modular Monolith
```

sang:

```text id="49564"
Distributed Microservices
+
Event Streaming Platform
```

V3 không đơn giản là:

```text id="67002"
move folders into different repositories
```

V3 phải giải quyết những vấn đề thực sự chỉ xuất hiện khi hệ thống bị phân tán qua network:

```text id="80588"
Network failure

Partial failure

Service timeout

Retry storms

Distributed transactions

Event duplication

Event ordering

Eventual consistency

Service discovery

Distributed authentication

Independent deployment

Failure isolation

Horizontal scaling

Distributed tracing

Schema evolution

Event replay

Operational complexity
```

---

# 2. V1 → V2 → V3 EVOLUTION

## V1

```text id="42055"
Modular Monolith
```

Goal:

```text id="54307"
Core business correctness
```

---

## V2

```text id="29953"
Event-Driven Modular Monolith
```

Goal:

```text id="82385"
Business completeness
+
Reliable asynchronous architecture
```

V2 đã có:

```text id="34190"
14 business modules

Transactional Outbox

Inbox

Idempotency

RabbitMQ

Search

Analytics

Observability

Failure recovery
```

---

## V3

```text id="18244"
Selective Microservices
+
RabbitMQ
+
Kafka
+
Kubernetes
```

Goal:

> Chứng minh rằng một production-grade modular monolith có thể được tiến hóa có kiểm soát thành distributed system mà không phá vỡ business invariants.

---

# 3. V3 ARCHITECTURAL PRINCIPLES

## 3.1 Do Not Extract Everything

Không áp dụng:

```text id="18978"
1 module = 1 microservice
```

Một module chỉ nên được tách khi có ít nhất một lý do:

```text id="20971"
different scaling requirement

different failure boundary

different security boundary

different deployment lifecycle

different workload profile

different data lifecycle

external integration complexity
```

---

# 4. SERVICE VS MODULE

Trong V2:

```text id="16516"
internal/payment/
```

là module.

Trong V3:

```text id="65315"
payment-service
```

chỉ thực sự là microservice nếu có:

```text id="82903"
separate process

separate deployment

separate API

separate database ownership

separate lifecycle

independent scaling

independent failure boundary
```

---

# 5. TARGET SERVICE MAP

V3 target:

```text id="44018"
1. API Gateway

2. Identity Service

3. Commerce Core Service

4. Catalog Service

5. Inventory Service

6. Order Service

7. Payment Service

8. Shipping Service

9. Search Service

10. Notification Service

11. Analytics Service
```

Khoảng:

```text id="69493"
10–11 deployable services
```

là đủ để project trở thành distributed system nghiêm túc.

---

# 6. MODULES REMAINING INSIDE COMMERCE CORE

Không cần tách ngay:

```text id="57419"
User

Seller

Cart

Voucher

Review

Return
```

Có thể nằm trong:

```text id="90157"
Commerce Core Service
```

---

# 7. WHY COMMERCE CORE EXISTS

V3 không cố tạo:

```text id="45337"
user-service
seller-service
cart-service
voucher-service
review-service
return-service
```

ngay từ đầu.

Nếu tách quá nhỏ:

```text id="26446"
Checkout
   |
   +--> User
   +--> Seller
   +--> Cart
   +--> Voucher
   +--> Review
   +--> Return
   +--> Catalog
   +--> Inventory
   +--> Payment
```

sẽ tạo network complexity cực lớn.

Vì vậy giữ một Commerce Core cho các capability còn coupling tương đối cao.

---

# 8. HIGH-LEVEL V3 ARCHITECTURE

```text id="47374"
                             CLIENT
                               |
                               v
                       +---------------+
                       |  API Gateway  |
                       +-------+-------+
                               |
          +--------------------+----------------------+
          |                    |                      |
          v                    v                      v
 Identity Service      Commerce Core            Search Service
                              |
             +----------------+----------------+
             |                                 |
             v                                 v
      Catalog Service                     Order Service
                                                |
                              +-----------------+----------------+
                              |                                  |
                              v                                  v
                     Inventory Service                   Payment Service
                              |
                              v
                      Shipping Service

```

Async infrastructure:

```text id="93132"
                    +-------------------+
                    |    RabbitMQ       |
                    +-------------------+
                      Commands / Jobs
                    Workflow Messaging

                    +-------------------+
                    |      Kafka        |
                    +-------------------+
                     Domain Event Stream
                     Analytics / Replay
```

Consumers:

```text id="64578"
Notification

Analytics

Search Indexer

Recommendation future

Fraud future
```

---

# 9. RABBITMQ VS KAFKA

V3 sử dụng cả hai.

Không phải:

```text id="52551"
RabbitMQ OR Kafka
```

mà là:

```text id="68547"
RabbitMQ AND Kafka
```

khi mỗi hệ thống phục vụ đúng workload.

---

# 10. RABBITMQ RESPONSIBILITIES

RabbitMQ dùng cho:

```text id="71527"
Commands

Work queues

Saga commands

Retry jobs

Notifications

Task distribution

Short-lived operational messages
```

Examples:

```text id="93742"
ReserveInventory

ReleaseInventory

CreateShipment

SendNotification

ProcessRefund
```

---

# 11. KAFKA RESPONSIBILITIES

Kafka dùng cho:

```text id="96411"
Domain event stream

Analytics

Search indexing

Clickstream

Long retention

Event replay

Multiple independent consumers
```

Examples:

```text id="23629"
OrderCreated

OrderPaid

ProductUpdated

ShipmentDelivered

ReviewCreated

RefundSucceeded
```

---

# 12. MESSAGE FLOW EXAMPLE

```text id="28637"
Order Service
      |
      | DB Transaction
      v
Order Outbox
      |
      v
Event Relay
      |
      +------> RabbitMQ
      |
      +------> Kafka
```

Không phải mọi event phải publish cả hai.

Routing được quyết định theo use case.

---

# 13. HYBRID MESSAGE MODEL

Example:

```text id="67136"
Checkout Saga
     |
     | command
     v
RabbitMQ
     |
Inventory
```

Sau khi inventory reserve:

```text id="59495"
Inventory Service
      |
InventoryReserved
      |
Kafka
      |
      +--> Analytics
      +--> Audit Projection
```

---

# 14. API GATEWAY

Gateway là entry point public chính.

Responsibilities:

```text id="23890"
routing

authentication entry point

authorization context propagation

rate limiting

request ID

trace propagation

API versioning

TLS termination

request size limits

CORS

basic security headers
```

---

# 15. GATEWAY MUST NOT OWN BUSINESS LOGIC

Không:

```text id="80713"
Gateway
  |
  +--> calculate voucher
  +--> reserve stock
  +--> change order state
```

Gateway chỉ chịu trách nhiệm edge concerns.

---

# 16. GATEWAY ROUTING

Example:

```text id="56297"
/api/v1/auth/*         → Identity

/api/v1/users/*        → Commerce Core

/api/v1/sellers/*      → Commerce Core

/api/v1/products/*     → Catalog

/api/v1/search/*       → Search

/api/v1/cart/*         → Commerce Core

/api/v1/orders/*       → Order

/api/v1/payments/*     → Payment
```

---

# 17. IDENTITY SERVICE

Identity Service được tách từ Auth module.

Owns:

```text id="14180"
credentials

password_hashes

sessions

refresh_tokens

verification_tokens
```

Responsibilities:

```text id="35324"
login

logout

token issuance

refresh rotation

revocation

password reset

email verification

authentication
```

---

# 18. IDENTITY DATABASE

```text id="27111"
identity_db
```

Other services không được query:

```text id="57982"
credentials
sessions
refresh_tokens
```

trực tiếp.

---

# 19. USER IDENTITY PROPAGATION

Gateway validates external token.

Then propagates authenticated identity:

```text id="51463"
user_id

roles

permissions

session_id

trace_id
```

to internal services.

---

# 20. ZERO-TRUST INTERNAL PRINCIPLE

Internal network không tự động trusted.

Không giả định:

```text id="86990"
inside Kubernetes = trusted
```

Internal service calls phải có identity.

---

# 21. SERVICE-TO-SERVICE AUTHENTICATION

V3 initial:

```text id="24730"
signed internal JWT
```

Advanced:

```text id="24790"
mTLS
```

Example:

```text id="46718"
Order Service
      |
      | authenticated
      v
Inventory Service
```

---

# 22. SERVICE AUTHORIZATION

Inventory Service có thể quy định:

```text id="51731"
Order Service:
  reserve
  commit
  release

Commerce Core:
  read availability

Admin:
  adjustment
```

Unknown service:

```text id="57636"
deny
```

---

# 23. COMMERCE CORE SERVICE

Contains:

```text id="76357"
User

Seller

Cart

Voucher

Review

Return
```

Own database:

```text id="87914"
commerce_db
```

---

# 24. COMMERCE CORE OWNERSHIP

Example tables:

```text id="23790"
users

profiles

addresses

seller_accounts

shops

carts

cart_items

vouchers

voucher_usages

reviews

return_requests
```

---

# 25. CATALOG SERVICE

Owns:

```text id="38877"
products

skus

categories

brands

attributes

product_metadata
```

Database:

```text id="43078"
catalog_db
```

---

# 26. CATALOG RESPONSIBILITIES

```text id="74354"
product CRUD

SKU management

price management

product lifecycle

category hierarchy

brand

attributes

moderation state
```

---

# 27. CATALOG EVENTS

Kafka:

```text id="27106"
ProductCreated

ProductUpdated

ProductPriceChanged

ProductActivated

ProductSuspended

SKUUpdated
```

Consumers:

```text id="54977"
Search

Analytics

Recommendation future
```

---

# 28. SEARCH SERVICE

Search owns:

```text id="28500"
Elasticsearch read model
```

Not source of truth.

---

# 29. SEARCH ARCHITECTURE

```text id="52697"
Catalog
   |
ProductUpdated
   |
Kafka
   |
Search Indexer
   |
Elasticsearch
```

Query:

```text id="12730"
Client
  |
Gateway
  |
Search Service
  |
Elasticsearch
```

---

# 30. SEARCH EVENTUAL CONSISTENCY

Product update:

```text id="19394"
Catalog DB = latest

Search Index = maybe slightly stale
```

This is acceptable.

Checkout never trusts Elasticsearch price.

---

# 31. SEARCH REINDEX

Must support:

```text id="79865"
full reindex

partial reindex

repair missing documents
```

---

# 32. INVENTORY SERVICE

Owns:

```text id="64790"
inventory_stocks

inventory_reservations

stock_movements

warehouses
```

Database:

```text id="25486"
inventory_db
```

---

# 33. INVENTORY API

Example:

```text id="42271"
POST /internal/v1/reservations

POST /internal/v1/reservations/{id}/commit

POST /internal/v1/reservations/{id}/release

GET /internal/v1/skus/{id}/availability

POST /internal/v1/stock-adjustments
```

---

# 34. INVENTORY INVARIANT

Always:

```text id="23011"
available_quantity >= 0

reserved_quantity >= 0
```

---

# 35. INVENTORY CONCURRENCY

Scenario:

```text id="84113"
stock = 50

1000 concurrent reserve requests
```

Expected:

```text id="40632"
<= 50 success

stock never negative
```

Must work with:

```text id="60002"
multiple Inventory replicas
```

---

# 36. INVENTORY DATABASE CONTROL

No reliance on:

```text id="61077"
sync.Mutex
```

for cross-instance correctness.

Use:

```text id="62241"
atomic SQL

row locking

constraints

transactions
```

---

# 37. INVENTORY EVENTS

Kafka:

```text id="37261"
InventoryReserved

InventoryCommitted

InventoryReleased

InventoryAdjusted
```

RabbitMQ may be used for commands:

```text id="52622"
ReserveInventory

ReleaseInventory
```

---

# 38. ORDER SERVICE

Order Service becomes central transactional coordinator.

Owns:

```text id="87312"
orders

order_items

order_status_histories

checkout_sagas

order_outbox

order_inbox
```

Database:

```text id="45326"
order_db
```

---

# 39. ORDER STATE OWNERSHIP

Only Order Service changes order state.

Payment cannot:

```text id="48837"
UPDATE order_db.orders
```

Shipping cannot either.

---

# 40. ORDER STATE MACHINE

```text id="81412"
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

Failure paths:

```text id="33922"
CANCELLED

PAYMENT_FAILED

RETURN_REQUESTED

RETURNED

REFUND_PENDING

REFUNDED
```

---

# 41. ORDER SNAPSHOT

Order keeps:

```text id="21566"
product_name

sku_name

unit_price

seller snapshot

attributes

shipping address

shipping fee

discount

total
```

Therefore historical order does not depend on Catalog.

---

# 42. ORDER API

Public:

```text id="66919"
POST /api/v1/orders/checkout

GET /api/v1/orders

GET /api/v1/orders/{id}

POST /api/v1/orders/{id}/cancel
```

Internal:

```text id="55345"
POST /internal/v1/orders/{id}/payment-success

POST /internal/v1/orders/{id}/shipment-event
```

where appropriate.

Prefer events for asynchronous facts.

---

# 43. DISTRIBUTED CHECKOUT

V2 checkout could run inside one application process.

V3 checkout crosses network boundaries.

```text id="75416"
Order Service
     |
     +--> Commerce Core
     |
     +--> Catalog
     |
     +--> Inventory
     |
     +--> Payment
```

No single PostgreSQL transaction can cover all databases.

---

# 44. DISTRIBUTED TRANSACTION PROBLEM

Cannot:

```text id="45992"
BEGIN

inventory_db

order_db

payment_db

commerce_db

COMMIT
```

Therefore V3 introduces:

```text id="14944"
Saga
```

---

# 45. CHECKOUT SAGA

Initial orchestration:

```text id="77558"
START
  |
  v
Load Cart
  |
  v
Validate Catalog
  |
  v
Reserve Voucher
  |
  v
Reserve Inventory
  |
  v
Create Order
  |
  v
Create Payment
  |
  v
WAIT PAYMENT
```

---

# 46. WHY ORCHESTRATION FIRST

V3 checkout should use:

```text id="88471"
Saga Orchestration
```

rather than pure choreography.

Reason:

```text id="26053"
critical workflow

many compensation steps

easy debugging

explicit state machine

better observability
```

---

# 47. CHECKOUT SAGA STATE

```text id="83228"
checkout_sagas

id

user_id

order_id

cart_id

inventory_reservation_id

voucher_reservation_id

payment_id

state

failure_reason

created_at

updated_at
```

---

# 48. SAGA STATES

```text id="24596"
STARTED

CART_VALIDATED

CATALOG_VALIDATED

VOUCHER_RESERVED

INVENTORY_RESERVED

ORDER_CREATED

PAYMENT_CREATED

AWAITING_PAYMENT

COMPLETED

COMPENSATING

COMPENSATED

FAILED
```

---

# 49. SAGA STATE MUST BE PERSISTED

Never keep Saga only in:

```text id="61691"
Go memory
```

Because Order Service can crash.

Saga state must survive restart.

---

# 50. SAGA COMPENSATION

If payment setup fails:

```text id="95389"
Payment Failed
      |
      v
Cancel Order
      |
      v
Release Inventory
      |
      v
Release Voucher
```

---

# 51. COMPENSATION MUST ALSO BE IDEMPOTENT

If:

```text id="67097"
ReleaseInventory
```

is executed twice:

Expected:

```text id="60653"
stock restored once
```

---

# 52. SAGA CRASH RECOVERY

If Order Service crashes after:

```text id="67974"
InventoryReserved
```

on restart:

```text id="82471"
Saga Recovery Worker
        |
        v
load incomplete sagas
        |
        v
continue / compensate
```

---

# 53. PAYMENT SERVICE

Owns:

```text id="77696"
payment_transactions

payment_attempts

payment_webhooks

refund_transactions

payment_outbox

payment_inbox
```

Database:

```text id="12601"
payment_db
```

---

# 54. PAYMENT PROVIDERS

Adapter abstraction:

```go id="61221"
type Provider interface {
    CreatePayment(...)
    QueryPayment(...)
    Refund(...)
    VerifyWebhook(...)
}
```

Implementations:

```text id="34984"
Mock

VNPay

MoMo
```

Optional:

```text id="41678"
Stripe
```

---

# 55. PAYMENT IDEMPOTENCY

Creating Payment must support:

```text id="16606"
Idempotency-Key
```

Same business operation cannot generate multiple independent charges.

---

# 56. PAYMENT TIMEOUT

Critical:

```text id="68125"
TIMEOUT != FAILED
```

Provider may have completed payment.

Therefore:

```text id="96570"
PENDING
```

until provider state is known.

---

# 57. PAYMENT WEBHOOK

```text id="78276"
Provider
   |
   v
Payment Service
```

Verify:

```text id="93106"
signature

timestamp

provider transaction id

duplicate

replay
```

---

# 58. PAYMENT RECONCILIATION

Worker:

```text id="53179"
stale PENDING
     |
     v
Query Provider
     |
 +---+---+
 |       |
SUCCESS FAILED
```

---

# 59. PAYMENT EVENTS

Kafka:

```text id="34939"
PaymentCreated

PaymentSucceeded

PaymentFailed

RefundSucceeded
```

RabbitMQ:

```text id="55660"
ProcessRefund

QueryPaymentStatus
```

where task semantics fit better.

---

# 60. SHIPPING SERVICE

Shipping becomes independent because:

```text id="95245"
external carrier integration

different failure profile

different lifecycle

different data
```

---

# 61. SHIPPING DATABASE

```text id="32193"
shipping_db
```

Owns:

```text id="22803"
shipments

shipment_items

tracking_events

provider_transactions

shipping_rates
```

---

# 62. SHIPPING STATE

```text id="44621"
PENDING

READY_FOR_PICKUP

PICKED_UP

IN_TRANSIT

OUT_FOR_DELIVERY

DELIVERED

DELIVERY_FAILED

RETURN_TO_SENDER
```

---

# 63. ORDER AND SHIPPING COMMUNICATION

Shipping emits:

```text id="88813"
ShipmentCreated

ShipmentPickedUp

ShipmentDelivered

ShipmentFailed
```

Order consumes these facts.

Shipping never updates Order DB.

---

# 64. MULTI-SELLER SHIPMENT

One Order:

```text id="13374"
Order
 ├── Shipment Seller A
 ├── Shipment Seller B
 └── Shipment Seller C
```

Shipping boundary must support this.

---

# 65. NOTIFICATION SERVICE

Notification is one of the first services to extract.

Owns:

```text id="75517"
notifications

templates

delivery_attempts

preferences
```

---

# 66. NOTIFICATION MESSAGING

RabbitMQ:

```text id="57713"
SendEmail

SendPush

SendSMS
```

Kafka consumption:

```text id="91221"
OrderPaid

ShipmentDelivered

RefundSucceeded
```

Notification converts events to delivery jobs.

---

# 67. NOTIFICATION FAILURE

Email failure must not fail Order.

Use:

```text id="60993"
retry

backoff

DLQ

delivery history
```

---

# 68. ANALYTICS SERVICE

Analytics is a strong Kafka consumer.

Architecture:

```text id="54291"
Kafka
  |
  +--> Order Consumer
  |
  +--> Payment Consumer
  |
  +--> Catalog Consumer
  |
  +--> Shipping Consumer
```

---

# 69. ANALYTICS DATABASE

Initial:

```text id="26764"
analytics_db
```

Store projections:

```text id="60849"
daily_sales

seller_metrics

product_metrics

conversion_funnel

refund_metrics
```

---

# 70. ANALYTICS DOES NOT QUERY OPERATIONAL DATABASES

Avoid:

```text id="27120"
Analytics
    |
    JOIN
    |
order_db + catalog_db + payment_db
```

Instead:

```text id="91493"
Kafka events
      |
Analytics projections
```

---

# 71. EVENT STREAMING PLATFORM

Kafka topics organized by domain.

Example:

```text id="27137"
commerce.order.events

commerce.payment.events

commerce.catalog.events

commerce.inventory.events

commerce.shipping.events

commerce.review.events
```

---

# 72. TOPIC DESIGN

Prefer domain-oriented topics over:

```text id="42120"
one topic for every tiny event
```

Example:

```text id="35056"
commerce.order.events
```

contains:

```text id="43455"
OrderCreated

OrderPaid

OrderCancelled

OrderDelivered
```

---

# 73. KAFKA PARTITION KEY

Order events:

```text id="58180"
key = order_id
```

This preserves ordering for one Order aggregate within a partition.

---

# 74. PAYMENT PARTITION KEY

```text id="81525"
key = payment_id
```

---

# 75. CATALOG PARTITION KEY

```text id="58737"
key = product_id
```

---

# 76. EVENT ORDERING

Kafka guarantees ordering only:

```text id="39260"
within a partition
```

Not globally.

Consumers cannot assume global event order.

---

# 77. EVENT ENVELOPE V3

```json id="70135"
{
  "event_id": "uuid",
  "event_type": "OrderPaid",
  "event_version": 1,
  "aggregate_type": "order",
  "aggregate_id": "uuid",
  "producer": "order-service",
  "correlation_id": "uuid",
  "causation_id": "uuid",
  "occurred_at": "...",
  "payload": {}
}
```

---

# 78. CORRELATION ID

Tracks business workflow.

Example:

```text id="38294"
Checkout correlation_id
        |
        +--> Order
        +--> Inventory
        +--> Payment
        +--> Shipping
        +--> Kafka
        +--> Notification
```

---

# 79. CAUSATION ID

Suppose:

```text id="59226"
PaymentSucceeded
```

causes:

```text id="38578"
OrderPaid
```

Then:

```text id="87535"
OrderPaid.causation_id
=
PaymentSucceeded.event_id
```

This helps reconstruct distributed workflows.

---

# 80. EVENT VERSIONING

Events are contracts.

Do not silently modify.

Example:

```text id="41007"
OrderPaid v1

OrderPaid v2
```

---

# 81. SCHEMA GOVERNANCE

Repository:

```text id="97859"
contracts/
│
├── events/
│   ├── order/
│   ├── payment/
│   ├── catalog/
│   └── shipping/
│
├── openapi/
└── proto/
```

---

# 82. EVENT SCHEMA FORMAT

Initial:

```text id="88503"
JSON Schema
```

Advanced:

```text id="33803"
Protobuf

Avro
```

---

# 83. KAFKA SCHEMA REGISTRY — OPTIONAL ADVANCED

If project evolves further:

```text id="29584"
Schema Registry
```

can enforce compatibility.

Not mandatory for first V3 implementation.

---

# 84. TRANSACTIONAL OUTBOX PER SERVICE

Every transactional service maintains own outbox.

Example:

```text id="71243"
order_db.outbox_events

payment_db.outbox_events

catalog_db.outbox_events

inventory_db.outbox_events
```

---

# 85. NO GLOBAL OUTBOX

Avoid:

```text id="21531"
central_event_database
```

shared by all services.

That would break service autonomy.

---

# 86. OUTBOX FLOW

```text id="97456"
Business Transaction
      |
      +--> UPDATE Domain State
      |
      +--> INSERT Outbox Event
      |
      v
COMMIT
```

Then:

```text id="77663"
Outbox Publisher
      |
      +--> RabbitMQ
      |
      +--> Kafka
```

---

# 87. OUTBOX DESTINATION

Add metadata:

```text id="52749"
destination

topic

routing_key
```

Example:

```text id="32780"
destination = KAFKA
topic = commerce.order.events
```

---

# 88. INBOX PER SERVICE

For RabbitMQ consumers:

```text id="22353"
inbox_messages
```

For Kafka consumers, idempotency may use:

```text id="19032"
event_id deduplication
```

when business side effects require it.

---

# 89. KAFKA OFFSET IS NOT BUSINESS IDEMPOTENCY

Committing Kafka offset does not guarantee:

```text id="93961"
business exactly once
```

Consumer can:

```text id="67254"
update DB
crash before offset commit
```

then Kafka redelivers.

Business operation still must tolerate duplication.

---

# 90. AT-LEAST-ONCE DELIVERY

Base assumption:

```text id="32196"
messages may be delivered more than once
```

Therefore:

```text id="31107"
consumers must be idempotent
```

---

# 91. DEAD LETTER QUEUE

RabbitMQ:

```text id="90510"
main queue

retry queue

DLQ
```

Kafka:

Could use:

```text id="48882"
*.retry

*.dlq
```

topics.

---

# 92. RETRY POLICY

Retry only:

```text id="86968"
transient failures
```

Not:

```text id="29358"
invalid state

invalid voucher

permission denied
```

---

# 93. EXPONENTIAL BACKOFF

Example:

```text id="25774"
100ms

200ms

400ms

800ms
```

plus:

```text id="79075"
jitter
```

---

# 94. RETRY BUDGET

Avoid:

```text id="45913"
Gateway retries 3

Order retries 3

Inventory retries 3
```

Potential amplification.

Each request must have:

```text id="35084"
retry owner
```

---

# 95. TIMEOUT POLICY

Every synchronous dependency must have timeout.

Example starting point:

```text id="56413"
Catalog: 300ms

Inventory: 500ms

Commerce Core: 500ms

Payment: 2s
```

Actual values must be benchmarked.

---

# 96. REQUEST DEADLINE PROPAGATION

Go:

```text id="20818"
Gateway context
      |
      v
Order
      |
      v
Inventory
```

Deadline must propagate through:

```text id="25525"
context.Context
```

---

# 97. CIRCUIT BREAKER

For external dependencies:

```text id="87651"
CLOSED
  |
failures
  |
  v
OPEN
  |
cooldown
  |
  v
HALF_OPEN
```

Useful for:

```text id="15633"
payment providers

shipping providers

email providers
```

---

# 98. BULKHEAD

Limit dependency-specific concurrent work.

Example:

```text id="50343"
Payment provider concurrency <= 100
```

so provider outage doesn't consume all service resources.

---

# 99. BACKPRESSURE

Do not spawn unlimited goroutines for events.

Use:

```text id="97379"
worker pool

bounded queue

semaphore

consumer concurrency
```

---

# 100. DATABASE-PER-SERVICE

Target:

```text id="36006"
Identity → identity_db

Commerce → commerce_db

Catalog → catalog_db

Inventory → inventory_db

Order → order_db

Payment → payment_db

Shipping → shipping_db

Analytics → analytics_db
```

---

# 101. SAME CLUSTER IS ACCEPTABLE INITIALLY

For local development:

```text id="21856"
one PostgreSQL instance
```

can host:

```text id="36994"
multiple databases
```

But credentials must enforce ownership.

---

# 102. NO CROSS-SERVICE SQL

Forbidden:

```text id="48429"
Order Service
   |
SELECT *
FROM payment_db.payment_transactions
```

Instead:

```text id="56312"
API

Event

Local projection
```

---

# 103. LOCAL PROJECTION

Sometimes Order needs external data frequently.

Instead of synchronous call every time:

```text id="61048"
Catalog events
    |
    v
Order local projection
```

But use only when justified.

---

# 104. CQRS — SELECTIVE

Use CQRS where read and write workloads differ significantly.

Example Search:

```text id="25336"
WRITE
Catalog PostgreSQL

READ
Elasticsearch
```

Analytics:

```text id="47663"
WRITE
Domain Services

READ
Analytics projections
```

---

# 105. DO NOT CQRS EVERYTHING

Avoid unnecessary:

```text id="53297"
CommandHandler

QueryHandler

EventSourcing

Projection
```

for simple CRUD.

---

# 106. EVENTUAL CONSISTENCY

Example:

```text id="81443"
Catalog price update
      |
      v
catalog_db
      |
      v
Kafka
      |
      v
Search
```

Search may temporarily show old price.

Checkout always validates latest price via Catalog.

---

# 107. SERVICE DISCOVERY

Kubernetes DNS:

```text id="29649"
http://inventory-service

http://payment-service

http://catalog-service
```

No hard-coded Pod IP.

---

# 108. INTERNAL HTTP VS gRPC

V3 initial recommendation:

```text id="79718"
HTTP/REST
```

for simplicity.

Later:

```text id="63234"
gRPC
```

for high-throughput internal service communication.

---

# 109. REST FIRST

Benefits:

```text id="39785"
easy debugging

curl friendly

OpenAPI

simple integration
```

---

# 110. gRPC RESEARCH PHASE

Can compare:

```text id="48337"
REST

vs

gRPC
```

on:

```text id="15720"
latency

throughput

payload size

developer complexity
```

---

# 111. INTERNAL API VERSIONING

Example:

```text id="28151"
/internal/v1/reservations
```

Breaking changes require:

```text id="42617"
/internal/v2
```

or backward-compatible evolution.

---

# 112. CONTRACT TESTING

Microservices create contract risk.

Need:

```text id="16745"
HTTP contract tests

event contract tests
```

---

# 113. CONSUMER-DRIVEN CONTRACTS — OPTIONAL

Advanced:

```text id="95230"
Pact
```

or custom OpenAPI contract validation.

---

# 114. DISTRIBUTED RATE LIMITING

Gateway:

```text id="44552"
global public limits
```

Services:

```text id="85738"
domain-specific limits
```

Example:

```text id="72613"
Login → Identity

Checkout → Order

Search → Search
```

---

# 115. REDIS

Redis may provide:

```text id="32502"
rate limiting

cache

distributed coordination

short-lived state
```

But not critical source of truth.

---

# 116. CACHE OWNERSHIP

Example:

```text id="39613"
catalog:product:123
```

belongs to Catalog.

Order Service must not manipulate it directly.

---

# 117. REDIS FAILURE

If Redis dies:

```text id="85578"
transactional correctness
```

must generally remain intact.

Possible degradation:

```text id="20420"
cache miss

rate limiting degraded

higher DB latency
```

Policy must be documented.

---

# 118. DISTRIBUTED OBSERVABILITY

Stack:

```text id="14771"
OpenTelemetry

Prometheus

Grafana

Loki

Tempo / Jaeger
```

---

# 119. DISTRIBUTED TRACE

Checkout trace:

```text id="88646"
Gateway
   |
   v
Order Service
   |
   +--> Commerce Core
   |
   +--> Catalog
   |
   +--> Inventory
   |
   +--> Payment
```

Async continuation:

```text id="39005"
PaymentSucceeded
       |
       v
Kafka / RabbitMQ
       |
       v
Order Event Handler
```

Trace context should propagate where practical.

---

# 120. TRACE FIELDS

Spans should include:

```text id="55125"
service.name

operation

trace_id

span_id

correlation_id

latency

status

error
```

---

# 121. CENTRALIZED LOGGING

Every service logs:

```text id="39688"
timestamp

level

service

instance

trace_id

span_id

request_id

correlation_id

user_id

operation

resource

status

latency

error
```

---

# 122. METRICS

HTTP:

```text id="71200"
request rate

error rate

p50

p95

p99
```

---

# 123. SERVICE METRICS

Inventory:

```text id="40719"
reservation success

reservation conflicts

reservation latency
```

Payment:

```text id="97513"
payment success rate

pending payments

provider latency

reconciliation count
```

---

# 124. KAFKA METRICS

Track:

```text id="31267"
consumer lag

produce latency

failed produce

partition count

consumer throughput
```

---

# 125. RABBITMQ METRICS

Track:

```text id="90323"
queue depth

unacked messages

redelivery

DLQ

publish failures

consumer rate
```

---

# 126. SLI

Service Level Indicators:

```text id="17101"
availability

latency

error rate

throughput
```

---

# 127. SLO

Example experimental targets:

```text id="99485"
Order API availability >= 99.9%

Catalog read p95 < 300ms

Search p95 < 300ms

Inventory reserve p95 < 200ms
```

Targets must be validated through benchmark.

---

# 128. ERROR BUDGET

Study:

```text id="30177"
SLO

error budget

reliability vs release speed
```

Useful SRE concept for V3.

---

# 129. KUBERNETES ARCHITECTURE

```text id="99032"
Ingress
   |
Gateway
   |
+---------+----------+----------+
|         |          |          |
Identity Commerce  Catalog    Order
                             /     \
                      Inventory   Payment
                                  |
                               Shipping
```

---

# 130. EACH SERVICE DEPLOYMENT

Each service gets:

```text id="29348"
Deployment

Service

ConfigMap

Secret

HPA

PodDisruptionBudget

readinessProbe

livenessProbe

resources
```

---

# 131. HEALTH

```text id="63922"
/health
```

Checks process alive.

---

# 132. READINESS

```text id="90552"
/ready
```

Determines if Pod can receive traffic.

---

# 133. GRACEFUL SHUTDOWN

SIGTERM:

```text id="37127"
ready = false
      |
drain traffic
      |
finish requests
      |
stop consumers
      |
finish current jobs
      |
close broker
      |
close DB
      |
exit
```

---

# 134. HORIZONTAL SCALING

Example:

```text id="97621"
Order Service

Pod A

Pod B

Pod C
```

No correctness logic may depend on one instance.

---

# 135. HPA

Scale HTTP services based on:

```text id="89015"
CPU

memory

custom request metrics
```

---

# 136. KEDA — OPTIONAL

Kafka/RabbitMQ workers can scale based on:

```text id="31769"
queue depth

consumer lag
```

using KEDA.

---

# 137. POD DISRUPTION BUDGET

Protect minimum replicas during:

```text id="36770"
node maintenance

rolling updates
```

---

# 138. ROLLING UPDATE

Deployment should support:

```text id="14557"
v1 + v2 coexist temporarily
```

Requires backward-compatible contracts.

---

# 139. ZERO-DOWNTIME DATABASE MIGRATION

Use:

```text id="80241"
Expand

Migrate

Contract
```

---

# 140. EXAMPLE MIGRATION

Step 1:

```text id="24025"
add new nullable column
```

Step 2:

```text id="55584"
deploy code supporting old + new
```

Step 3:

```text id="83450"
backfill
```

Step 4:

```text id="20798"
switch reads
```

Step 5:

```text id="89105"
remove old column later
```

---

# 141. HELM

V3 deployments should eventually use:

```text id="98052"
Helm
```

Structure:

```text id="76929"
deployments/helm/
├── gateway/
├── identity/
├── commerce/
├── catalog/
├── inventory/
├── order/
├── payment/
└── shipping/
```

---

# 142. TERRAFORM

Infrastructure:

```text id="20743"
network

Kubernetes cluster

databases

object storage

monitoring dependencies
```

represented as code.

---

# 143. ENVIRONMENTS

```text id="25642"
local

test

staging

production
```

---

# 144. CONFIGURATION

Each service validates configuration at startup.

Missing critical variable:

```text id="68494"
fail fast
```

---

# 145. SECRET MANAGEMENT

Development:

```text id="88930"
local .env
```

Production:

```text id="44707"
Kubernetes Secret
```

Advanced:

```text id="21113"
Vault

AWS Secrets Manager

GCP Secret Manager

External Secrets Operator
```

---

# 146. NETWORK POLICY

Example:

```text id="24334"
Order
  |
  +--> Inventory
  |
  +--> Payment
```

Order should not automatically have network access to every service.

---

# 147. CONTAINER SECURITY

Run:

```text id="84354"
non-root

minimal image

read-only filesystem when possible
```

Avoid unnecessary binaries.

---

# 148. SUPPLY CHAIN SECURITY

CI can add:

```text id="75619"
dependency scan

container scan

secret scan

SBOM

IaC scan
```

Advanced:

```text id="74497"
image signing

provenance
```

---

# 149. SECURITY THREAT MODEL V3

New threats:

```text id="75690"
service impersonation

internal API abuse

lateral movement

SSRF

message forgery

event poisoning

secret leakage

Kubernetes misconfiguration

supply-chain compromise
```

---

# 150. MULTI-TENANT SELLER SECURITY

Invariant:

```text id="43780"
Seller A
cannot modify
Seller B resources
```

Must survive across service boundaries.

---

# 151. AUTHORIZATION CONTEXT

Example request:

```text id="39794"
Seller → Gateway → Catalog
```

Catalog validates:

```text id="65564"
user identity

seller relationship

shop ownership

permission
```

Never trust arbitrary:

```text id="80148"
shop_id
```

from client.

---

# 152. FILE / MEDIA ARCHITECTURE

Media can remain supporting infrastructure or later become service.

Flow:

```text id="67863"
Client
   |
presigned upload
   |
S3 / MinIO
```

Backend controls:

```text id="90581"
authorization

metadata

size/type policy

object ownership
```

---

# 153. OBJECT CLEANUP

Handle orphan objects:

```text id="24490"
upload succeeds

DB transaction fails
```

Periodic cleanup job reconciles storage with metadata.

---

# 154. EVENT REPLAY

Kafka enables:

```text id="44391"
rebuild Search

rebuild Analytics

recompute projections
```

from retained event history.

---

# 155. SEARCH REBUILD EXAMPLE

```text id="35563"
delete Elasticsearch index
      |
      v
consume Catalog event history
      |
      v
rebuild index
```

This becomes possible with retained event stream.

---

# 156. ANALYTICS REBUILD

Similarly:

```text id="93164"
Kafka events
     |
     v
rebuild analytics projection
```

---

# 157. CLICKSTREAM

V3 can introduce events:

```text id="92838"
ProductViewed

SearchPerformed

ProductAddedToCart

CheckoutStarted
```

Kafka handles high-volume stream better than RabbitMQ.

---

# 158. FUTURE RECOMMENDATION SYSTEM

Kafka event consumers can feed:

```text id="75482"
Recommendation Service
```

using:

```text id="24389"
views

cart actions

orders

reviews
```

Not required for V3 core.

---

# 159. FUTURE FRAUD SERVICE

Kafka events:

```text id="68815"
Login

Checkout

Payment

Refund

Voucher Usage
```

could later feed:

```text id="17187"
Fraud / Risk Service
```

---

# 160. DATA RECONCILIATION

Distributed systems drift.

Need periodic jobs comparing:

```text id="28726"
Payment DB
vs
payment provider
```

```text id="70205"
Catalog DB
vs
Elasticsearch
```

```text id="83200"
Order
vs
Shipment state
```

---

# 161. RECONCILIATION PRINCIPLE

Events provide eventual updates.

Reconciliation provides:

```text id="78695"
eventual repair
```

Both are needed in reliable systems.

---

# 162. DISASTER RECOVERY

V3 must document:

```text id="89189"
database backup

restore procedures

object storage backup

Kafka retention considerations

configuration recovery

runbooks
```

---

# 163. BACKUP IS NOT ENOUGH

Need actual:

```text id="43119"
restore test
```

---

# 164. RPO / RTO

Document target assumptions:

```text id="94728"
RPO

Recovery Point Objective

RTO

Recovery Time Objective
```

No need enterprise target, but must understand them.

---

# 165. CHAOS ENGINEERING

Failure scenarios:

```text id="78597"
kill Order pod

kill Inventory pod

kill Payment pod

restart RabbitMQ

restart Kafka broker

Redis unavailable

Elasticsearch unavailable

network latency

duplicate messages

out-of-order events

consumer crash

database connection exhaustion
```

---

# 166. CHAOS INVARIANTS

After failures:

```text id="62377"
inventory >= 0

no duplicate payment

no duplicate refund

no lost paid order

no invalid order state

no duplicate business side effect
```

---

# 167. INVENTORY CHAOS TEST

During 1000 checkout attempts:

```text id="58140"
kill Inventory replica
```

Expected:

```text id="13022"
system may return failures

but stock remains correct
```

---

# 168. PAYMENT CRASH TEST

Flow:

```text id="15093"
provider payment succeeds
      |
Payment Service crashes
      |
webhook redelivered / reconciliation
```

Expected:

```text id="66905"
eventually consistent payment state
```

---

# 169. KAFKA CONSUMER CRASH

```text id="19368"
consumer updates DB
      |
crash before offset commit
```

Event reprocessed.

Expected:

```text id="13075"
no duplicate business effect
```

---

# 170. RABBITMQ CONSUMER CRASH

```text id="94976"
business operation succeeds
      |
crash before ACK
```

Message redelivered.

Expected:

```text id="13283"
Inbox prevents duplicate operation
```

---

# 171. LOAD TESTING

Use:

```text id="18317"
k6
```

Test scenarios:

```text id="64371"
browse

search

login

cart

checkout

payment webhook

order history
```

---

# 172. SERVICE BENCHMARKING

Measure independently:

```text id="15591"
Catalog throughput

Search throughput

Inventory reservation throughput

Order throughput

Payment webhook throughput
```

---

# 173. GO PROFILING

Use:

```text id="44871"
pprof
```

Measure:

```text id="22503"
CPU

heap

goroutines

allocations

mutex contention

blocking
```

---

# 174. DATABASE PERFORMANCE

Each service monitors:

```text id="42725"
slow queries

locks

deadlocks

connection pool

index usage
```

Use:

```text id="34326"
EXPLAIN ANALYZE

pg_stat_statements
```

---

# 175. CAPACITY PLANNING

Estimate:

```text id="42950"
requests/sec

orders/sec

payments/sec

events/sec

Kafka throughput

RabbitMQ workload

DB writes/sec

storage growth
```

---

# 176. MONOREPO STRATEGY

V3 should initially remain:

```text id="39037"
Monorepo
```

Structure:

```text id="40497"
nexus-commerce/
│
├── services/
│   ├── gateway/
│   ├── identity/
│   ├── commerce/
│   ├── catalog/
│   ├── inventory/
│   ├── order/
│   ├── payment/
│   ├── shipping/
│   ├── search/
│   ├── notification/
│   └── analytics/
│
├── contracts/
│   ├── events/
│   ├── openapi/
│   └── proto/
│
├── libs/
│   └── platform/
│
├── deployments/
│   ├── docker/
│   ├── helm/
│   └── kubernetes/
│
├── infrastructure/
│   └── terraform/
│
├── scripts/
│
└── docs/
```

---

# 177. SHARED PLATFORM LIBRARY

Allowed shared code:

```text id="19541"
logging

telemetry

event envelope

HTTP middleware

test utilities

config
```

---

# 178. DO NOT SHARE DOMAIN LOGIC

Avoid:

```text id="82264"
libs/domain/
```

containing:

```text id="99889"
Order

Payment

Inventory business logic
```

because services become tightly coupled.

---

# 179. DISTRIBUTED MONOLITH WARNING

If deploying Order requires simultaneously deploying:

```text id="55308"
Inventory

Payment

Catalog

Commerce
```

every time, architecture may become:

```text id="16551"
Distributed Monolith
```

---

# 180. INDEPENDENT DEPLOYMENT

Goal:

```text id="67812"
Payment v2
```

can deploy while:

```text id="16248"
Order v1

Inventory v1

Catalog v1
```

continue running.

Requires backward-compatible contracts.

---

# 181. MIGRATION STRATEGY

Use:

```text id="14538"
Strangler Fig Pattern
```

Do not rewrite V2.

---

# 182. EXTRACTION PROCESS

For each service:

```text id="16253"
1. Identify stable V2 module

2. Formalize interface

3. Remove direct DB dependencies

4. Introduce API/event boundary

5. Separate database

6. Create service process

7. Route traffic

8. Observe

9. Remove old module implementation
```

---

# 183. EXTRACTION ORDER

Recommended:

```text id="13985"
1. Notification

2. Search

3. Payment

4. API Gateway

5. Inventory

6. Order

7. Shipping

8. Analytics
```

---

# 184. WHY NOT ORDER FIRST

Order has high coupling:

```text id="70807"
Cart

Catalog

Voucher

Inventory

Payment

Shipping
```

Extracting it first maximizes risk.

Notification and Search are safer practice.

---

# 185. PHASE V3.1 — MICROSERVICE READINESS

Tickets:

```text id="30707"
MS-ARCH-001 V2 Extraction Assessment

MS-ARCH-002 Service Boundary Map

MS-ARCH-003 Communication Matrix

MS-ARCH-004 Data Ownership Matrix

MS-ARCH-005 Failure Boundary Analysis

MS-ARCH-006 Service API Standards

MS-ARCH-007 Event Standards

MS-ARCH-008 Security Model

MS-ARCH-009 Observability Model

MS-ARCH-010 Migration Strategy
```

---

# 186. PHASE V3.2 — RABBITMQ & KAFKA PLATFORM

```text id="54830"
MSG-001 RabbitMQ Production Configuration

MSG-002 Kafka Cluster Bootstrap

MSG-003 Kafka Topic Convention

MSG-004 Event Envelope

MSG-005 Event Serializer

MSG-006 Producer Library

MSG-007 Consumer Library

MSG-008 Retry Strategy

MSG-009 RabbitMQ DLQ

MSG-010 Kafka Retry/DLQ Topics

MSG-011 Correlation Propagation

MSG-012 Event Contract Tests
```

---

# 187. PHASE V3.3 — NOTIFICATION EXTRACTION

```text id="75598"
MS-NOTIF-001 Service Bootstrap

MS-NOTIF-002 Notification Database

MS-NOTIF-003 RabbitMQ Consumers

MS-NOTIF-004 Kafka Event Consumers

MS-NOTIF-005 Email Adapter

MS-NOTIF-006 Retry

MS-NOTIF-007 DLQ

MS-NOTIF-008 Observability

MS-NOTIF-009 Independent Deployment

MS-NOTIF-010 Remove V2 Notification Module
```

---

# 188. PHASE V3.4 — SEARCH EXTRACTION

```text id="90391"
MS-SEARCH-001 Search Service Bootstrap

MS-SEARCH-002 Kafka Catalog Consumer

MS-SEARCH-003 Elasticsearch Indexing

MS-SEARCH-004 Query API

MS-SEARCH-005 Filtering

MS-SEARCH-006 Full Reindex

MS-SEARCH-007 Replay From Kafka

MS-SEARCH-008 Reconciliation

MS-SEARCH-009 Search Failure Testing
```

---

# 189. PHASE V3.5 — PAYMENT EXTRACTION

```text id="49994"
MS-PAY-001 Payment Service Bootstrap

MS-PAY-002 payment_db

MS-PAY-003 Provider Interface

MS-PAY-004 Mock Provider

MS-PAY-005 Create Payment API

MS-PAY-006 Webhook

MS-PAY-007 Idempotency

MS-PAY-008 Reconciliation

MS-PAY-009 Refund

MS-PAY-010 Outbox

MS-PAY-011 Inbox

MS-PAY-012 Kafka Events

MS-PAY-013 Service Authentication

MS-PAY-014 Failure Tests

MS-PAY-015 Cutover
```

---

# 190. PHASE V3.6 — API GATEWAY

```text id="76990"
MS-GW-001 Gateway Bootstrap

MS-GW-002 Routing

MS-GW-003 Authentication

MS-GW-004 Identity Propagation

MS-GW-005 Rate Limiting

MS-GW-006 Request ID

MS-GW-007 Trace Propagation

MS-GW-008 Timeout Policy

MS-GW-009 Security Headers

MS-GW-010 Gateway Observability
```

---

# 191. PHASE V3.7 — INVENTORY EXTRACTION

```text id="36242"
MS-INV-001 Inventory Service

MS-INV-002 inventory_db

MS-INV-003 Reservation API

MS-INV-004 Commit API

MS-INV-005 Release API

MS-INV-006 Stock Adjustment

MS-INV-007 Authentication

MS-INV-008 Outbox

MS-INV-009 Kafka Events

MS-INV-010 Concurrency Tests

MS-INV-011 Failure Tests

MS-INV-012 Cutover
```

---

# 192. PHASE V3.8 — ORDER EXTRACTION

```text id="15231"
MS-ORDER-001 Order Service

MS-ORDER-002 order_db

MS-ORDER-003 Order State Machine

MS-ORDER-004 Snapshot Migration

MS-ORDER-005 API

MS-ORDER-006 Outbox

MS-ORDER-007 Inbox

MS-ORDER-008 Payment Events

MS-ORDER-009 Inventory Integration

MS-ORDER-010 Shipping Events

MS-ORDER-011 Failure Tests

MS-ORDER-012 Cutover
```

---

# 193. PHASE V3.9 — CHECKOUT SAGA

```text id="38447"
SAGA-001 Saga Architecture

SAGA-002 Saga Persistence

SAGA-003 Cart Validation

SAGA-004 Catalog Validation

SAGA-005 Voucher Reservation

SAGA-006 Inventory Reservation

SAGA-007 Order Creation

SAGA-008 Payment Creation

SAGA-009 Compensation

SAGA-010 Idempotent Commands

SAGA-011 Crash Recovery

SAGA-012 Retry

SAGA-013 Saga Timeout

SAGA-014 Saga Observability

SAGA-015 Chaos Tests
```

---

# 194. PHASE V3.10 — SHIPPING EXTRACTION

```text id="68135"
MS-SHIP-001 Shipping Service

MS-SHIP-002 shipping_db

MS-SHIP-003 Shipping Provider Adapter

MS-SHIP-004 Rate API

MS-SHIP-005 Shipment Lifecycle

MS-SHIP-006 Tracking

MS-SHIP-007 Kafka Events

MS-SHIP-008 Order Integration

MS-SHIP-009 Reconciliation

MS-SHIP-010 Failure Tests
```

---

# 195. PHASE V3.11 — ANALYTICS EXTRACTION

```text id="94350"
MS-ANALYTICS-001 Analytics Service

MS-ANALYTICS-002 Kafka Consumers

MS-ANALYTICS-003 analytics_db

MS-ANALYTICS-004 Sales Projection

MS-ANALYTICS-005 Seller Projection

MS-ANALYTICS-006 Funnel Projection

MS-ANALYTICS-007 Event Replay

MS-ANALYTICS-008 Dashboard API
```

---

# 196. PHASE V3.12 — DISTRIBUTED SECURITY

```text id="94308"
SEC3-001 Internal JWT

SEC3-002 Service Identity

SEC3-003 Service Authorization

SEC3-004 mTLS Research

SEC3-005 Kubernetes RBAC

SEC3-006 Network Policies

SEC3-007 Secret Management

SEC3-008 Container Hardening

SEC3-009 Supply Chain Scanning

SEC3-010 Security Tests
```

---

# 197. PHASE V3.13 — OBSERVABILITY

```text id="28306"
OBS3-001 OpenTelemetry Collector

OBS3-002 Distributed Tracing

OBS3-003 Trace Propagation

OBS3-004 Prometheus Metrics

OBS3-005 Kafka Metrics

OBS3-006 RabbitMQ Metrics

OBS3-007 PostgreSQL Metrics

OBS3-008 Grafana Dashboards

OBS3-009 Loki

OBS3-010 Tempo / Jaeger

OBS3-011 SLI

OBS3-012 SLO

OBS3-013 Alerts
```

---

# 198. PHASE V3.14 — KUBERNETES

```text id="85866"
K8S-001 Namespaces

K8S-002 Deployments

K8S-003 Services

K8S-004 Ingress

K8S-005 ConfigMaps

K8S-006 Secrets

K8S-007 Liveness

K8S-008 Readiness

K8S-009 Resources

K8S-010 HPA

K8S-011 PDB

K8S-012 Rolling Updates

K8S-013 Graceful Shutdown

K8S-014 Helm
```

---

# 199. PHASE V3.15 — RELIABILITY ENGINEERING

```text id="95383"
REL-001 Timeout Standards

REL-002 Retry Standards

REL-003 Retry Budget

REL-004 Circuit Breaker

REL-005 Bulkhead

REL-006 Backpressure

REL-007 RabbitMQ DLQ

REL-008 Kafka Retry

REL-009 Payment Reconciliation

REL-010 Search Reconciliation

REL-011 Shipping Reconciliation

REL-012 Saga Recovery
```

---

# 200. PHASE V3.16 — CHAOS & PERFORMANCE

```text id="59008"
TEST3-001 Service Integration Tests

TEST3-002 Contract Tests

TEST3-003 Inventory Concurrency

TEST3-004 Saga Failure

TEST3-005 Payment Failure

TEST3-006 RabbitMQ Failure

TEST3-007 Kafka Failure

TEST3-008 Redis Failure

TEST3-009 Service Crash

TEST3-010 Network Delay

TEST3-011 Event Duplication

TEST3-012 Event Ordering

TEST3-013 k6 Load Test

TEST3-014 pprof

TEST3-015 Capacity Report
```

---

# 201. PHASE V3.17 — INFRASTRUCTURE AS CODE

```text id="56154"
IAC-001 Terraform Structure

IAC-002 Network

IAC-003 Kubernetes Cluster

IAC-004 Databases

IAC-005 Redis

IAC-006 Kafka

IAC-007 RabbitMQ

IAC-008 Object Storage

IAC-009 Monitoring

IAC-010 Environment Separation
```

---

# 202. REQUIRED V3 DEMOS

## Demo 1 — Independent Deployment

Deploy:

```text id="12255"
Payment v2
```

without redeploying:

```text id="46299"
Order

Inventory

Catalog
```

---

## Demo 2 — Independent Scaling

```text id="33950"
Search = 5 pods

Order = 3 pods

Payment = 2 pods

Notification = 1 pod
```

---

## Demo 3 — Inventory Concurrency

```text id="75013"
Stock = 50

1000 concurrent checkout attempts
```

Expected:

```text id="67870"
<= 50 succeed

stock >= 0
```

---

## Demo 4 — Payment Duplicate Event

Send:

```text id="89228"
PaymentSucceeded
```

10 times.

Expected:

```text id="46692"
Order marked PAID once
```

---

## Demo 5 — RabbitMQ Failure

Stop RabbitMQ.

Create business transaction.

Expected:

```text id="72550"
business DB commits

outbox retains event
```

Restart RabbitMQ.

Expected:

```text id="77670"
event eventually delivered
```

---

## Demo 6 — Kafka Failure

Temporarily stop Kafka.

Catalog update continues.

Expected:

```text id="45357"
Catalog remains source of truth

outbox retains event
```

Kafka returns.

Expected:

```text id="82806"
Search and Analytics catch up
```

---

## Demo 7 — Kafka Replay

Delete search index.

Replay Catalog events.

Expected:

```text id="87030"
Elasticsearch rebuilt
```

---

## Demo 8 — Consumer Crash

Consumer:

```text id="74557"
updates DB
crashes before ACK/offset commit
```

Expected:

```text id="94262"
message redelivered

no duplicate business effect
```

---

## Demo 9 — Saga Compensation

```text id="27516"
Voucher reserved

Inventory reserved

Order created

Payment creation fails
```

Expected:

```text id="51770"
Order cancelled

Inventory released

Voucher released
```

---

## Demo 10 — Saga Crash Recovery

Kill Order Service after:

```text id="19396"
Inventory reserved
```

Restart it.

Expected:

```text id="85780"
Saga resumes or compensates
```

---

## Demo 11 — Payment Uncertainty

Provider succeeds.

Network response lost.

Webhook delayed.

Expected:

```text id="59048"
Payment stays PENDING

Reconciliation later resolves SUCCESS
```

---

## Demo 12 — Shipping Failure

Carrier API unavailable.

Expected:

```text id="73534"
Order remains correct

shipping operation retries

payment unaffected
```

---

## Demo 13 — Distributed Trace

Show one checkout trace:

```text id="72729"
Gateway
  |
Order
  |
Commerce
  |
Catalog
  |
Inventory
  |
Payment
```

plus async events.

---

## Demo 14 — Pod Failure

Kill one Order Pod during traffic.

Expected:

```text id="24380"
remaining replicas continue

no corrupted state
```

---

## Demo 15 — Redis Failure

Kill Redis.

Expected:

```text id="38857"
cache/rate limit may degrade

transactional correctness remains
```

---

# 203. CORE V3 INVARIANTS

System must guarantee:

```text id="37687"
Inventory never negative.

Voucher usage never exceeds limit.

Order history remains immutable.

Only Order Service changes Order lifecycle.

Payment charges do not duplicate.

Refunds do not duplicate.

Duplicate messages do not duplicate business effects.

Outbox prevents lost committed events.

Saga state survives process crash.

Compensation is idempotent.

Seller boundaries remain enforced.

Cross-service DB access is forbidden.

Search may be stale but Checkout never trusts stale price.

Payment timeout is not payment failure.
```

---

# 204. V3 DEFINITION OF DONE

V3 is not complete because:

```text id="28447"
docker compose has many containers
```

V3 is complete when the following are proven.

---

## Architecture

```text id="47725"
Service boundaries are explicit.

Databases have clear ownership.

Cross-service SQL is forbidden.

Contracts are documented.
```

---

## Microservices

```text id="16655"
Multiple independently deployed services.

Independent scaling demonstrated.

Independent service failure demonstrated.
```

---

## Messaging

```text id="83674"
RabbitMQ handles commands/jobs.

Kafka handles retained domain streams.

Outbox works.

Inbox/idempotency works.

DLQ/retry works.
```

---

## Distributed Transactions

```text id="42183"
Checkout Saga exists.

Saga state is persisted.

Compensation works.

Crash recovery works.
```

---

## Reliability

```text id="53160"
Timeouts defined.

Retries bounded.

Circuit breakers applied where justified.

Reconciliation works.

Partial failures tested.
```

---

## Security

```text id="69990"
External auth secure.

Internal service identity exists.

Service authorization exists.

Secrets protected.

Network policies exist.

Containers hardened.
```

---

## Observability

```text id="19170"
Distributed traces available.

Centralized logs available.

Service metrics available.

Kafka/RabbitMQ observable.

Dashboards available.

Alerts available.
```

---

## Kubernetes

```text id="25896"
Services deploy successfully.

Health/readiness configured.

Graceful shutdown works.

HPA works.

Rolling update works.
```

---

## Testing

```text id="99622"
Unit tests

Integration tests

Contract tests

Concurrency tests

Failure tests

Chaos tests

Load tests
```

all exist.

---

# 205. V3 ARCHITECTURE DECISION RECORDS

Recommended ADRs:

```text id="36492"
ADR-013 Why Microservices Now?

ADR-014 Why Selective Microservices?

ADR-015 Service Extraction Order

ADR-016 Database-per-Service

ADR-017 RabbitMQ vs Kafka

ADR-018 Hybrid Messaging Architecture

ADR-019 Saga Orchestration

ADR-020 REST vs gRPC

ADR-021 Service Authentication

ADR-022 Event Schema Versioning

ADR-023 Kafka Topic Design

ADR-024 Retry Ownership

ADR-025 Kubernetes

ADR-026 CQRS Boundaries

ADR-027 Distributed Tracing
```

---

# 206. ENGINEERING TICKET TEMPLATE V3

Every ticket should define:

```text id="56046"
Ticket ID

Problem

Context

Goal

Scope

Out of Scope

Service Ownership

Data Ownership

API Contract

Event Contract

Security

Transaction Boundary

Consistency Model

Failure Cases

Retry Policy

Idempotency

Timeout

Observability

Deployment Impact

Rollback Strategy

Unit Tests

Integration Tests

Contract Tests

Failure Tests

Acceptance Criteria

Definition of Done
```

---

# 207. V3 REVIEW QUESTIONS

Every distributed feature must answer:

```text id="69251"
Why is this a separate service?

Who owns the data?

What happens if the network call times out?

Can this operation execute twice?

Can this message arrive twice?

Can events arrive out of order?

What happens if the consumer crashes?

What happens if RabbitMQ fails?

What happens if Kafka fails?

What happens if the database commits but event publication fails?

How is the system repaired?

What is the timeout?

Who owns retry?

How is the request traced?

Can this service deploy independently?

How do we roll it back?
```

---

# 208. V3 FINAL ARCHITECTURE

```text id="68627"
                                  CLIENT
                                    |
                                    v
                           +----------------+
                           |  API GATEWAY   |
                           +--------+-------+
                                    |
             +----------------------+-----------------------+
             |                      |                       |
             v                      v                       v
     Identity Service        Commerce Core           Search Service
                                    |
                      +-------------+--------------+
                      |                            |
                      v                            v
                Catalog Service              Order Service
                                                  |
                                    +-------------+-------------+
                                    |                           |
                                    v                           v
                             Inventory Service           Payment Service
                                    |
                                    v
                             Shipping Service


                        SYNCHRONOUS COMMUNICATION
                             HTTP / gRPC


              +-----------------------------------------+
              |                                         |
              v                                         v
        +------------+                           +-------------+
        | RabbitMQ   |                           |    Kafka    |
        +------------+                           +-------------+
        Commands / Jobs                         Event Streams
        Saga Messages                           Retention
        Retry Queues                            Replay
        DLQ                                     Analytics
                                                Search
                                                Future ML
              |                                         |
              v                                         v
       Notification                               Analytics
                                                  Search Indexer
```

---

# 209. DATA ARCHITECTURE

```text id="95120"
identity_db

commerce_db

catalog_db

inventory_db

order_db

payment_db

shipping_db

analytics_db

Redis

RabbitMQ

Kafka

Elasticsearch

MinIO / S3
```

---

# 210. V3 → V3+ FUTURE

After V3 is stable, optional evolution:

```text id="22219"
Recommendation Service

Fraud / Risk Service

Seller Settlement

Customer Support / Dispute

Data Warehouse

CDC

Stream Processing

Real-time Analytics
```

Kafka becomes foundation for many of these capabilities.

---

# 211. POSSIBLE V3+ DATA PLATFORM

```text id="57013"
             Domain Services
                    |
                    v
                  Kafka
                    |
        +-----------+-----------+
        |           |           |
        v           v           v
   Analytics   Recommendation   Fraud
        |
        v
 Data Warehouse
```

---

# 212. WHAT V3 SHOULD TEACH

After completing V3, the developer should be able to explain:

```text id="19470"
Why microservices?

Why not every module should become a service?

How service boundaries are chosen?

How database-per-service works?

Why cross-service SQL is dangerous?

How distributed transactions differ from local transactions?

How Saga works?

How compensation works?

Why compensation must be idempotent?

Why RabbitMQ and Kafka solve different problems?

Why Kafka is useful for replay?

How Kafka partition ordering works?

Why Kafka offset does not guarantee exactly-once business effects?

How Outbox prevents lost events?

How Inbox prevents duplicate effects?

How retries create retry storms?

How timeouts propagate?

How circuit breakers work?

How service discovery works?

How internal authentication works?

How Kubernetes scales services?

How rolling upgrades stay compatible?

How distributed tracing reconstructs workflows?

How to recover from partial failure?

How to test distributed correctness?
```

---

# 213. FINAL V3 GOAL

Nexus-Commerce V3 should ultimately be describable as:

> **A distributed, event-driven, multi-vendor e-commerce platform built with Go, combining transactional messaging with RabbitMQ, retained event streaming with Apache Kafka, database-per-service ownership, Saga-based distributed transactions, Kubernetes orchestration, distributed observability, fault isolation and production-grade reliability engineering.**

The architectural philosophy is:

```text id="55856"
Do not distribute before boundaries are stable.

Use RabbitMQ for work.

Use Kafka for streams.

Persist critical state.

Assume messages duplicate.

Assume networks fail.

Never confuse timeout with failure.

Make commands idempotent.

Make compensation idempotent.

Own your database.

Avoid distributed monoliths.

Measure before scaling.

Recover before retrying.

Test failure, not only success.
```

---

# 214. NEXUS-COMMERCE EVOLUTION SUMMARY

```text id="13292"
V1
│
│  Modular Monolith
│  Core Commerce
│
v
V2
│
│  Event-Driven Modular Monolith
│  14 Business Modules
│  RabbitMQ
│  Outbox / Inbox
│  Business Complete
│
v
V3
│
│  Selective Microservices
│  Database-per-Service
│  RabbitMQ + Kafka
│  Checkout Saga
│  API Gateway
│  Kubernetes
│  Distributed Tracing
│
v
V3+
   Intelligent Commerce Platform

   Recommendation
   Fraud Detection
   Streaming Analytics
   Data Warehouse
   ML Pipelines
```
