# NEXUS-COMMERCE V2 — CANONICAL EXECUTION PLAN

## Business-Complete Event-Driven Modular Monolith

**Previous Stage:** Nexus-Commerce V1 — Production-Grade Core Commerce
**Current Stage:** V2 — Business-Complete Event-Driven Modular Monolith
**Next Stage:** V3 — Selective Distributed Microservices

---

# 1. PURPOSE

V2 không xây lại V1.

V2 kế thừa toàn bộ:

```text
domain boundaries
database invariants
concurrency guarantees
idempotency rules
historical snapshots
security rules
payment reliability
testing foundation
```

đã hoàn thành ở V1.

Rule:

```text
V1 invariant
→ preserved in V2
```

Nếu tài liệu V2 cũ mâu thuẫn với V1 canonical:

```text
V1 canonical wins
```

---

# 2. V2 VISION

Nexus-Commerce V2 là:

```text
Production-Grade
Business-Complete
Event-Driven
Modular Monolith
```

V2 vẫn:

```text
one application boundary
shared PostgreSQL
strong module ownership
```

nhưng bổ sung:

```text
Shipping
Return
Review
Search
Analytics
Media
Transactional Outbox
RabbitMQ
Inbox
Idempotent Consumers
Retry / DLQ
Event Versioning
Failure Recovery
OpenTelemetry
```

V2 chưa phải microservices.

---

# 3. ARCHITECTURE EVOLUTION

```text
V1
Production-Grade Modular Monolith
Core Commerce Correctness
        │
        ▼
V2
Event-Driven Modular Monolith
Business Completeness
Reliable Async Processing
        │
        ▼
V3
Selective Distributed Microservices
Database-per-Service
Kafka
Saga
Kubernetes
```

---

# 4. OFFICIAL V2 STACK

```text
Go
Chi
PostgreSQL
pgx/v5
sqlc
golang-migrate

Redis

RabbitMQ

Elasticsearch

MinIO / S3

OpenTelemetry
Prometheus
Grafana
Loki

Docker
Docker Compose

Testcontainers
k6
```

V2 chưa bắt buộc:

```text
Kafka
database-per-service
distributed Saga
service mesh
independent microservice deployment
```

---

# 5. V1 FOUNDATION THAT MUST NOT CHANGE

## Inventory

Canonical:

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

Never persist mutable:

```text
available_quantity
```

Invariant:

```text
on_hand_quantity >= 0
reserved_quantity >= 0
reserved_quantity <= on_hand_quantity
```

Reservation:

```text
reserved += qty
```

Commit:

```text
on_hand -= qty
reserved -= qty
```

Release:

```text
reserved -= qty
```

Reservation/release/expiration do not create physical StockMovement.

Physical stock changes create StockMovement.

---

## Cart

Cart remains:

```text
purchase intent
```

Cart is not:

```text
price source of truth
inventory reservation
order history
```

Checkout revalidates authoritative data.

---

## Order

Canonical order structure remains:

```text
Parent Order
├── Seller Order A
│   └── Order Items
├── Seller Order B
│   └── Order Items
└── ...
```

Parent Order is:

```text
customer checkout aggregate
payment correlation boundary
cross-seller aggregate
```

Seller Order is:

```text
shop-specific commercial unit
fulfillment unit
shipping unit
seller cancellation unit
```

Parent states:

```text
pending
confirmed
partially_completed
completed
partially_cancelled
cancelled
```

Seller states:

```text
pending
confirmed
processing
shipped
delivered
cancelled
```

Shipping/Return must evolve around this model.

Do not replace it with a flat global state machine.

---

## Voucher

Canonical V2 foundation:

```text
vouchers
voucher_usages
```

Lifecycle:

```text
RESERVED
├── COMMITTED
├── RELEASED
└── EXPIRED
```

Reservation has:

```text
expires_at
```

Platform and Shop voucher remain supported.

Do not create a second voucher reservation source of truth unless a future requirement proves it necessary.

---

## Payment

Canonical:

```text
payment_transactions
payment_refunds
payment_webhook_events
```

A Parent Order may have:

```text
many historical PaymentTransactions
```

but:

```text
maximum one economically live attempt
```

Payment never directly mutates Order state.

---

# 6. V2 BUSINESS MODULES

Core modules inherited:

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
10. Notification
```

New business modules:

```text
11. Shipping
12. Return
13. Review
14. Analytics
```

Supporting capabilities:

```text
Search
Media
Audit
Idempotency
Outbox
Inbox
Rate Limiting
Observability
```

Checkout remains:

```text
Application Orchestrator
```

not a data-owning module.

---

# 7. V2 MODULE OWNERSHIP

```text
Auth
→ credentials
→ sessions
→ refresh/security tokens

User
→ users
→ addresses
→ preferences

Seller
→ seller_accounts
→ shops
→ shop_memberships

Catalog
→ categories
→ brands
→ products
→ product_variants
→ skus
→ product images metadata

Inventory
→ warehouses
→ stocks
→ reservations
→ reservation items
→ stock movements

Cart
→ carts
→ cart_items

Voucher
→ vouchers
→ voucher_usages

Order
→ orders
→ order_items
→ status histories

Payment
→ payment_transactions
→ payment_refunds
→ payment_webhook_events

Shipping
→ shipments
→ shipment_items
→ tracking_events

Return
→ return_requests
→ return_items
→ return histories

Review
→ reviews
→ review_images
→ review_votes

Notification
→ notifications
→ delivery attempts

Analytics
→ projections / read models
```

---

# 8. NO CROSS-MODULE DIRECT MUTATION

Forbidden:

```text
Payment
→ orderRepository.UpdateStatus()

Shipping
→ orderRepository.UpdateStatus()

Return
→ paymentRepository.Refund()

Analytics
→ business table mutation
```

Allowed:

```text
Application boundary
Domain Event
Command
Owned public interface
```

Example:

```text
Payment
↓
PaymentSucceeded
↓
Order Handler
↓
Order transition
```

---

# 9. EVENT-DRIVEN FOUNDATION

V2 introduces:

```text
Transactional Outbox
RabbitMQ
Inbox
Idempotent Consumers
Retry
DLQ
Event Versioning
```

Core guarantee:

```text
business state commit
+
required event persistence
=
same PostgreSQL transaction
```

Flow:

```text
Business Transaction
      │
      ├── mutate owned tables
      │
      └── INSERT outbox event
              │
              COMMIT
              │
              ▼
        Outbox Publisher
              │
              ▼
          RabbitMQ
              │
              ▼
           Consumer
              │
              ▼
            Inbox
              │
              ▼
       Business Handler
```

---

# 10. DELIVERY SEMANTICS

Assume:

```text
at-least-once delivery
```

Therefore:

```text
events can duplicate
consumers can crash
ACK can be lost
network can time out
```

Business effects must remain idempotent.

Never claim:

```text
exactly-once distributed execution
```

---

# 11. EVENT ENVELOPE

Every V2 event should contain:

```text
event_id
event_type
event_version
aggregate_type
aggregate_id
occurred_at

correlation_id
causation_id

producer

payload
```

For checkout-related events also propagate:

```text
checkout_reference_id
```

---

# 12. OUTBOX

Conceptual table:

```text
outbox_events
```

Fields:

```text
id
aggregate_type
aggregate_id
event_type
event_version
payload
correlation_id
causation_id
created_at
published_at
attempt_count
last_error
```

Publisher must tolerate:

```text
publish succeeds
↓
process crashes before published_at update
↓
event publishes again
```

Therefore consumer idempotency is mandatory.

---

# 13. INBOX

Consumer maintains durable dedup identity:

```text
consumer_name
event_id
```

Unique:

```text
UNIQUE(consumer_name, event_id)
```

Processing:

```text
BEGIN

insert inbox
if duplicate:
    no-op

apply local state mutation

COMMIT
```

Exact transaction structure depends on handler requirements.

---

# 14. RABBITMQ ROLE

RabbitMQ V2 handles:

```text
domain events
async jobs
notification delivery
search indexing jobs
analytics projection jobs
retries
dead-letter processing
```

Kafka remains deferred to V3.

---

# 15. SHIPPING MODULE

Shipping owns:

```text
shipments
shipment_items
tracking_events
```

Shipping does not own:

```text
Order.status
Payment.status
```

Natural boundary:

```text
Seller Order
1 → N Shipments
```

because fulfillment is seller-specific.

Shipment should never contain:

```text
OrderItems from unrelated Seller Orders
```

Use cross-domain identifiers plus validation/constraints where possible.

---

# 16. SHIPPING STATE

Example:

```text
pending
ready
shipped
in_transit
delivered
failed
cancelled
```

Shipping emits facts:

```text
ShipmentCreated
ShipmentDispatched
ShipmentDelivered
ShipmentFailed
```

Order consumes those facts and decides its own state.

---

# 17. RETURN MODULE

Return is not Payment.

Return owns:

```text
return request
return eligibility workflow
returned quantity
inspection
return decision
```

Payment owns:

```text
refund financial execution
```

Flow:

```text
Return Approved
↓
Refund Requested
↓
Payment Module
↓
RefundSucceeded
↓
Return / Order react
```

---

# 18. RETURN INVARIANTS

Must guarantee:

```text
return quantity
<=
eligible purchased quantity
-
already returned quantity
```

Concurrent return requests must not oversubscribe eligible quantity.

Return eligibility must use historical OrderItem snapshot/purchase data.

---

# 19. REVIEW MODULE

Review requires:

```text
authenticated user
valid purchased item
eligible delivered/completed order
```

Invariant:

```text
unauthorized user
cannot review another user's purchase
```

Suggested unique business identity:

```text
user_id
+
order_item_id
```

depending final review policy.

---

# 20. SEARCH

PostgreSQL Catalog remains:

```text
source of truth
```

Elasticsearch becomes:

```text
read model
```

Flow:

```text
Catalog DB
↓
ProductUpdated
↓
Outbox
↓
RabbitMQ
↓
Search Indexer
↓
Elasticsearch
```

Checkout never trusts Elasticsearch for:

```text
price
status
availability
voucher eligibility
```

---

# 21. SEARCH FAILURE RECOVERY

Must support:

```text
full reindex
partial reindex
repair missing documents
```

Search can be stale.

Commercial correctness cannot.

---

# 22. MEDIA

Media handles:

```text
product images
shop logos
review evidence
return evidence
```

Storage:

```text
MinIO development
S3-compatible production
```

Validate:

```text
file size
content type
authorization
generated object key
metadata
```

Do not trust original filename.

Need orphan cleanup.

---

# 23. ANALYTICS

Analytics consumes events and maintains read projections.

Examples:

```text
sales by day
orders by seller
conversion funnel
payment success rate
return rate
product sales
```

Analytics tables are:

```text
derived read models
```

not authoritative business state.

---

# 24. OBSERVABILITY

V2 adds OpenTelemetry.

Required:

```text
structured logs
request ID
correlation ID
traces
Prometheus metrics
Grafana dashboards
Loki
business metrics
failure metrics
```

Important metrics:

```text
outbox backlog
outbox publish latency
consumer lag
consumer failures
DLQ count
checkout success rate
inventory conflict rate
voucher rejection rate
payment reconciliation count
search indexing delay
```

---

# 25. V2 ROADMAP

V2 does NOT redo V1 foundations.

## PHASE V2.1 — V1 → V2 UPGRADE REVIEW

```text
V2-ARCH-001 V1 Completion Gate
V2-ARCH-002 Module Boundary Revalidation
V2-ARCH-003 Event Candidate Map
V2-ARCH-004 Async vs Sync Communication Matrix
V2-ARCH-005 Event Contract Standard
V2-ARCH-006 Worker Architecture
V2-ARCH-007 Failure Model
V2-ARCH-008 Observability Upgrade Design
```

---

## PHASE V2.2 — EVENT-DRIVEN FOUNDATION

```text
ECOM-EVT-001 RabbitMQ Bootstrap
ECOM-EVT-002 Event Envelope
ECOM-EVT-003 Outbox Schema
ECOM-EVT-004 Outbox Repository
ECOM-EVT-005 Outbox Publisher
ECOM-EVT-006 Publisher Recovery
ECOM-EVT-007 Inbox Schema
ECOM-EVT-008 Idempotent Consumer
ECOM-EVT-009 Retry Strategy
ECOM-EVT-010 DLQ
ECOM-EVT-011 Event Versioning
ECOM-EVT-012 Event Contract Tests
ECOM-EVT-013 Broker Failure Tests
```

---

## PHASE V2.3 — SHIPPING

```text
ECOM-SHIP-001 Shipping Domain Design
ECOM-SHIP-002 Shipping Schema
ECOM-SHIP-003 Seller Order Integration
ECOM-SHIP-004 Shipment Creation
ECOM-SHIP-005 Shipment Items
ECOM-SHIP-006 Tracking
ECOM-SHIP-007 Shipment State Machine
ECOM-SHIP-008 Delivery Events
ECOM-SHIP-009 Failure / Retry
ECOM-SHIP-010 Shipping Tests
```

---

## PHASE V2.4 — RETURN

```text
ECOM-RETURN-001 Return Domain
ECOM-RETURN-002 Return Schema
ECOM-RETURN-003 Eligibility
ECOM-RETURN-004 Create Return
ECOM-RETURN-005 Seller/Admin Decision
ECOM-RETURN-006 Partial Return
ECOM-RETURN-007 Concurrent Quantity Protection
ECOM-RETURN-008 Inspection
ECOM-RETURN-009 Refund Workflow
ECOM-RETURN-010 Restock Workflow
ECOM-RETURN-011 Return Tests
```

---

## PHASE V2.5 — REVIEW

```text
ECOM-REVIEW-001 Review Schema
ECOM-REVIEW-002 Purchase Verification
ECOM-REVIEW-003 Create Review
ECOM-REVIEW-004 Update/Delete
ECOM-REVIEW-005 Review Images
ECOM-REVIEW-006 Rating Projection
ECOM-REVIEW-007 Moderation
ECOM-REVIEW-008 Security Tests
```

---

## PHASE V2.6 — ASYNC NOTIFICATION

```text
ECOM-NOTIF2-001 Notification Event Contracts
ECOM-NOTIF2-002 Notification Consumer
ECOM-NOTIF2-003 Delivery Worker
ECOM-NOTIF2-004 Retry
ECOM-NOTIF2-005 DLQ
ECOM-NOTIF2-006 Delivery History
ECOM-NOTIF2-007 Failure Isolation
```

---

## PHASE V2.7 — SEARCH

```text
ECOM-SEARCH-001 Elasticsearch Bootstrap
ECOM-SEARCH-002 Catalog Index Model
ECOM-SEARCH-003 Catalog Event Consumer
ECOM-SEARCH-004 Search API
ECOM-SEARCH-005 Filtering
ECOM-SEARCH-006 Sorting
ECOM-SEARCH-007 Pagination
ECOM-SEARCH-008 Full Reindex
ECOM-SEARCH-009 Repair/Reconciliation
ECOM-SEARCH-010 Search Failure Tests
```

---

## PHASE V2.8 — MEDIA

```text
ECOM-MEDIA-001 MinIO Bootstrap
ECOM-MEDIA-002 Upload API
ECOM-MEDIA-003 Validation
ECOM-MEDIA-004 Authorization
ECOM-MEDIA-005 Product Images
ECOM-MEDIA-006 Review Images
ECOM-MEDIA-007 Return Evidence
ECOM-MEDIA-008 Orphan Cleanup
ECOM-MEDIA-009 Security Tests
```

---

## PHASE V2.9 — ANALYTICS

```text
ECOM-ANALYTICS-001 Analytics Events
ECOM-ANALYTICS-002 Consumer
ECOM-ANALYTICS-003 Sales Projection
ECOM-ANALYTICS-004 Seller Projection
ECOM-ANALYTICS-005 Funnel Projection
ECOM-ANALYTICS-006 Payment Projection
ECOM-ANALYTICS-007 Analytics API
ECOM-ANALYTICS-008 Rebuild Projection
```

---

## PHASE V2.10 — OBSERVABILITY UPGRADE

```text
ECOM-OBS2-001 OpenTelemetry
ECOM-OBS2-002 Trace Propagation
ECOM-OBS2-003 RabbitMQ Metrics
ECOM-OBS2-004 Worker Metrics
ECOM-OBS2-005 Outbox Metrics
ECOM-OBS2-006 Business Metrics
ECOM-OBS2-007 Loki
ECOM-OBS2-008 Grafana
ECOM-OBS2-009 Alerts
```

---

## PHASE V2.11 — PRODUCTION FAILURE TESTING

```text
ECOM-TEST2-001 Broker Failure
ECOM-TEST2-002 Consumer Crash
ECOM-TEST2-003 Duplicate Events
ECOM-TEST2-004 Event Redelivery
ECOM-TEST2-005 DLQ Recovery
ECOM-TEST2-006 Search Failure
ECOM-TEST2-007 Notification Failure
ECOM-TEST2-008 Return Concurrency
ECOM-TEST2-009 Full E2E
ECOM-TEST2-010 Load Test
```

---

# 26. REQUIRED V2 DEMOS

```text
1. RabbitMQ unavailable after business commit
   → outbox preserves event

2. Consumer commits DB but crashes before ACK
   → redelivery
   → no duplicate effect

3. Elasticsearch deleted
   → reindex restores search

4. Seller Orders ship independently
   → Parent Order aggregates correctly

5. Partial Return
   → returned quantity protected under concurrency

6. Refund workflow
   → Return and Payment ownership remain separate

7. Notification provider fails
   → Order/Payment remain correct

8. Duplicate domain events
   → one logical business effect

9. Outbox backlog recovery

10. Full purchase → shipment → return → refund → review
```

---

# 27. V2 DEFINITION OF DONE

Business:

```text
V1 commerce still works
Shipping works
Return works
Review works
Search works
Media works
Analytics works
Async Notification works
```

Reliability:

```text
Outbox prevents event loss
Inbox prevents duplicate business effects
Consumers recover after crash
Retries are bounded
DLQ works
Search can be rebuilt
Analytics projections can be rebuilt
```

Correctness:

```text
V1 invariants remain valid
Inventory cannot oversell
Voucher cannot oversubscribe
Order ownership unchanged
Payment cannot mutate Order directly
Return cannot exceed purchased quantity
Shipping cannot mix unrelated Seller Orders
```

---

# 28. V2 → V3 GATE

Do not start V3 until:

```text
V1 invariants still pass

module boundaries stable

no cross-module repository mutation

Outbox stable

Inbox stable

consumers idempotent

event contracts versioned

failure recovery tested

Search rebuild tested

business workflows complete

OpenTelemetry works

RabbitMQ behavior understood

Docker environment stable
```

Then:

```text
Measure
↓
Identify extraction candidate
↓
Extract one service
↓
Stabilize
↓
Extract next
```

---

# 29. FINAL V2 PRINCIPLE

```text
Correctness before async
Idempotency before retries
Outbox before reliable events
Boundaries before services
Observability before distribution
Recovery before scale
```

V2 must enhance V1.

It must never redesign away the guarantees already proven in V1.
