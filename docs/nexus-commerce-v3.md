# NEXUS-COMMERCE V3 — CANONICAL EXECUTION PLAN

## Selective Distributed Microservices & Event Streaming Platform

**Previous Stage:** Nexus-Commerce V2 — Event-Driven Modular Monolith
**Current Stage:** V3 — Selective Distributed Microservices
**Future Stage:** V3+ — Intelligent Commerce / Data Platform

---

# 1. PURPOSE

V3 evolves V2 into a distributed system.

It does not rebuild the commerce domain.

Core rule:

```text
V1 defines business correctness
↓
V2 proves modular + asynchronous correctness
↓
V3 distributes those boundaries
```

Business invariant migration principle:

```text
same invariant
different enforcement mechanism
```

Example:

```text
V1:
cross-module FK + transaction

V3:
service API
+ owned DB constraint
+ idempotency
+ event contracts
+ reconciliation
```

---

# 2. V3 VISION

V3 must demonstrate:

```text
Selective Microservices
Database-per-Service
API Gateway
RabbitMQ
Kafka
Saga Orchestration
Distributed Authentication
Eventual Consistency
Independent Deployment
Independent Scaling
Distributed Tracing
Failure Isolation
Kubernetes
Helm
Terraform
Chaos Testing
```

V3 is not:

```text
move folders into different repositories
```

---

# 3. WHY MICROservices NOW

A module may become a service when justified by:

```text
independent scaling
failure isolation
security boundary
deployment lifecycle
workload differences
external integration
data ownership
operational independence
```

Do not enforce:

```text
1 module = 1 microservice
```

---

# 4. TARGET SERVICE MAP

Canonical V3 target:

```text
1. API Gateway

2. Identity Service

3. Commerce Core Service
   ├── User
   ├── Seller
   ├── Cart
   ├── Voucher
   ├── Review
   └── Return

4. Catalog Service

5. Inventory Service

6. Order Service

7. Payment Service

8. Shipping Service

9. Search Service

10. Notification Service

11. Analytics Service
```

Total:

```text
10–11 deployable services
```

Not every V2 module needs an independent service.

---

# 5. TARGET DATA OWNERSHIP

```text
Identity Service
→ identity_db

Commerce Core
→ commerce_db

Catalog Service
→ catalog_db

Inventory Service
→ inventory_db

Order Service
→ order_db

Payment Service
→ payment_db

Shipping Service
→ shipping_db

Analytics Service
→ analytics_db

Search Service
→ Elasticsearch

Notification Service
→ notification storage if required
```

Rule:

```text
one service
→ owns its database

another service
→ never queries that database directly
```

---

# 6. BUSINESS INVARIANTS MUST SURVIVE DISTRIBUTION

V3 must still guarantee:

```text
Inventory never oversells

Voucher usage never exceeds quota

Order history remains immutable

Only Order Service changes Order lifecycle

Payment does not double charge

Refund does not over-refund

Checkout retry does not duplicate business transaction

Duplicate messages do not duplicate effects

Expired reservation cannot commit

Seller isolation remains enforced

Customer IDOR remains prevented

One checkout remains one currency

Payment amount matches commercial Order amount
```

---

# 7. INVENTORY CANONICAL MODEL

Inventory Service keeps V1 model:

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

Invariant:

```text
on_hand >= 0
reserved >= 0
reserved <= on_hand
```

Never redesign Service schema to persist mutable:

```text
available_quantity
```

simply because Inventory became a microservice.

---

# 8. INVENTORY API

Example:

```text
POST /internal/v1/reservations

POST /internal/v1/reservations/{id}/commit

POST /internal/v1/reservations/{id}/release

GET /internal/v1/skus/{id}/availability

POST /internal/v1/stock-adjustments
```

Correctness must remain DB-based:

```text
atomic SQL
transactions
constraints
row locking
```

not:

```text
sync.Mutex
```

---

# 9. ORDER CANONICAL MODEL

Order Service preserves:

```text
Parent Order
├── Seller Order
├── Seller Order
└── Seller Order
```

Do not collapse back to a single flat Order lifecycle.

Parent Order remains:

```text
checkout/customer aggregate
payment boundary
cross-seller aggregate
```

Seller Order remains:

```text
seller commercial unit
fulfillment unit
shipping unit
```

---

# 10. ORDER OWNERSHIP

Only:

```text
Order Service
```

changes:

```text
Order lifecycle state
```

Payment publishes:

```text
PaymentSucceeded
PaymentFailed
RefundSucceeded
```

Shipping publishes:

```text
ShipmentDelivered
ShipmentFailed
```

Order Service decides what those facts mean for Order state.

---

# 11. DISTRIBUTED COMMUNICATION

Use synchronous calls for:

```text
immediate validation
commands requiring direct response
read-before-decision
```

Examples:

```text
Catalog validation
Inventory reservation request
Create Payment request
```

Use asynchronous events for:

```text
facts
projections
notifications
analytics
search indexing
workflow continuation
```

---

# 12. RABBITMQ VS KAFKA

V3 uses both intentionally.

RabbitMQ:

```text
commands
work queues
Saga commands
retries
operational jobs
notifications
short-lived workflow messages
```

Kafka:

```text
domain event stream
analytics
search indexing
long retention
event replay
multiple independent consumers
clickstream
future ML pipelines
```

Rule:

```text
RabbitMQ for work
Kafka for streams
```

Not every event must exist in both.

---

# 13. OUTBOX REMAINS REQUIRED

Each service maintains local Outbox where required.

Example:

```text
Order DB transaction
├── update orders
└── insert OrderConfirmed event into order_outbox
        ↓
commit
        ↓
relay
        ↓
Kafka / RabbitMQ
```

No service should perform:

```text
DB COMMIT
then blindly publish event
```

for critical business events.

---

# 14. INBOX REMAINS REQUIRED

Critical consumers persist message identity.

```text
consumer
+
event_id / command_id
```

must deduplicate side effects.

Assume:

```text
message duplication is normal
```

---

# 15. CHECKOUT REFERENCE AS DISTRIBUTED BUSINESS IDENTITY

V1 introduced:

```text
checkout_reference_id
```

V3 keeps it.

It must propagate through:

```text
Commerce Core
Catalog
Inventory
Voucher
Order
Payment
Saga
RabbitMQ
Kafka
logs
traces
```

Do not replace it with only `saga_id`.

Use distinct concepts:

```text
checkout_reference_id
→ business operation identity

saga_id
→ workflow execution identity

event_id
→ event identity

command_id
→ command identity

correlation_id
→ distributed correlation

causation_id
→ causal relationship

trace_id
→ observability trace
```

---

# 16. CHECKOUT SAGA

Once Checkout crosses databases, no local PostgreSQL transaction can cover:

```text
commerce_db
catalog_db
inventory_db
order_db
payment_db
```

Therefore V3 introduces:

```text
Saga Orchestration
```

Initial orchestrator:

```text
Order Service / dedicated Checkout orchestration component
```

depending final V3 implementation.

---

# 17. SAGA FLOW

```text
START
↓
Load Cart
↓
Validate Catalog
↓
Validate Currency
↓
Reserve Voucher
↓
Reserve Inventory
↓
Create Parent + Seller Orders
↓
Create Payment
↓
WAIT PAYMENT
↓
CONFIRM / COMPENSATE
```

Every command must be idempotent.

---

# 18. SAGA PERSISTENCE

Persist:

```text
checkout_sagas
```

Fields conceptually:

```text
id
checkout_reference_id

user_id
cart_id

parent_order_id

inventory_reservation_id
voucher_usage_id
payment_transaction_id

state

failure_code
failure_detail

created_at
updated_at
deadline_at
```

Do not keep Saga state only in Go memory.

---

# 19. SAGA STATES

Example:

```text
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

Persist every meaningful state transition.

---

# 20. COMPENSATION

Example payment setup failure:

```text
Order created
+
Inventory reserved
+
Voucher reserved
+
Payment creation fails
```

Compensate:

```text
Cancel appropriate Order state
↓
Release Inventory
↓
Release Voucher
```

Compensation must itself be idempotent.

---

# 21. SAGA TIMEOUT

Timeout never automatically means:

```text
operation failed
```

Especially Payment.

Example:

```text
CreatePayment request
↓
network timeout
```

Possible provider state:

```text
payment created successfully
```

Therefore:

```text
TIMEOUT
!=
FAILED
```

Use reconciliation.

---

# 22. SAGA RECOVERY

If orchestrator crashes:

```text
Inventory reserved
↓
process crash
```

after restart:

```text
Saga Recovery Worker
↓
find incomplete sagas
↓
continue
or
compensate
```

---

# 23. SERVICE-TO-SERVICE AUTHENTICATION

Initial V3:

```text
signed internal service credentials / internal JWT
```

Advanced:

```text
mTLS
```

Do not assume:

```text
inside Kubernetes
=
trusted
```

---

# 24. SERVICE AUTHORIZATION

Example Inventory policy:

```text
Order Service
→ reserve
→ commit
→ release

Commerce Core
→ availability read

Admin capability
→ stock adjustment

Unknown service
→ deny
```

---

# 25. API GATEWAY

Gateway owns edge concerns:

```text
routing
external authentication
identity propagation
rate limiting
request size limits
CORS
security headers
API versioning
request ID
trace propagation
TLS termination
```

Gateway never owns:

```text
voucher calculation
inventory reservation logic
order transitions
payment business logic
```

---

# 26. IDENTITY SERVICE

Identity extraction is a required V3 phase.

Owns:

```text
credentials
sessions
refresh tokens
verification tokens
password reset tokens
```

Responsibilities:

```text
login
logout
token issuance
refresh rotation
revocation
password reset
email verification
authentication
```

Other services cannot query `identity_db`.

---

# 27. CATALOG SERVICE

Catalog extraction is also a required V3 phase.

Owns:

```text
categories
brands
products
product_variants
skus
attributes
product metadata
```

Source of truth for:

```text
current product state
current SKU state
current price
```

Produces Kafka events:

```text
ProductCreated
ProductUpdated
ProductPriceChanged
ProductActivated
ProductArchived
SKUUpdated
```

---

# 28. SEARCH SERVICE

Owns:

```text
Elasticsearch read model
```

Consumes Catalog events.

Must support:

```text
full rebuild
partial repair
Kafka replay
reconciliation
```

Checkout never queries Search for commercial truth.

---

# 29. PAYMENT SERVICE

Preserve V1 canonical concepts:

```text
payment_transactions
payment_refunds
payment_webhook_events
```

Do not introduce separate `payment_attempts` unless requirements later prove the abstraction is useful.

`payment_transactions` already model historical attempts.

Rules:

```text
one Parent Order
→ many historical transactions

maximum one economically live attempt

provider payment identity scoped by provider

provider refund identity scoped by provider

refund allocation concurrency-safe

webhooks deduplicated
```

---

# 30. PAYMENT EVENTS

Kafka facts:

```text
PaymentCreated
PaymentProcessing
PaymentSucceeded
PaymentFailed
RefundSucceeded
RefundFailed
```

Sensitive provider command workflows may use RabbitMQ where appropriate.

---

# 31. PAYMENT RECONCILIATION

Needed for:

```text
request timeout
missing webhook
provider/local state drift
consumer failure
```

Same state machine and idempotency rules must be used by:

```text
API response handling
webhook
reconciliation
```

---

# 32. SHIPPING SERVICE

Owns:

```text
shipping_db
shipments
shipment_items
tracking_events
```

Shipping primarily relates to:

```text
Seller Orders
```

not arbitrary Parent Order items.

Must support reconciliation with carrier APIs.

---

# 33. NOTIFICATION SERVICE

Good early extraction candidate because:

```text
low business coupling
natural async boundary
independent failure
independent scaling
```

Consumes RabbitMQ/Kafka events.

Notification failure does not affect transactional correctness.

---

# 34. ANALYTICS SERVICE

Consumes Kafka.

Own:

```text
analytics_db
```

Supports:

```text
sales projection
seller projection
funnel projection
payment metrics
event replay
```

---

# 35. EXTRACTION STRATEGY

Use:

```text
Strangler Fig Pattern
```

For every service:

```text
1. Identify stable V2 module
2. Formalize boundary
3. Remove cross-module DB access
4. Introduce API/event contract
5. Introduce separate database
6. Create deployable process
7. Route traffic
8. Observe
9. Cut over
10. Remove old implementation
```

---

# 36. CANONICAL EXTRACTION ORDER

Recommended:

```text
1. Notification
2. Search
3. Identity
4. Catalog
5. Payment
6. API Gateway
7. Inventory
8. Order
9. Checkout Saga
10. Shipping
11. Analytics
```

This fixes the mismatch where Identity/Catalog existed in the target service map but previously had no explicit extraction phase.

Order is intentionally late because of high coupling.

---

# 37. V3 PHASE ROADMAP

## V3.1 — MICROSERVICE READINESS

```text
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

## V3.2 — MESSAGING PLATFORM

```text
MSG-001 RabbitMQ Production Config
MSG-002 Kafka Bootstrap
MSG-003 Topic Convention
MSG-004 Event Envelope
MSG-005 Serialization
MSG-006 Producer Library
MSG-007 Consumer Library
MSG-008 Retry Strategy
MSG-009 RabbitMQ DLQ
MSG-010 Kafka Retry/DLQ
MSG-011 Correlation Propagation
MSG-012 Contract Tests
```

---

## V3.3 — NOTIFICATION EXTRACTION

```text
MS-NOTIF-001 Service Bootstrap
MS-NOTIF-002 Notification DB
MS-NOTIF-003 Consumers
MS-NOTIF-004 Email Adapter
MS-NOTIF-005 Retry
MS-NOTIF-006 DLQ
MS-NOTIF-007 Observability
MS-NOTIF-008 Independent Deployment
MS-NOTIF-009 Cutover
```

---

## V3.4 — SEARCH EXTRACTION

```text
MS-SEARCH-001 Bootstrap
MS-SEARCH-002 Kafka Consumer
MS-SEARCH-003 Elasticsearch
MS-SEARCH-004 Query API
MS-SEARCH-005 Filtering
MS-SEARCH-006 Full Reindex
MS-SEARCH-007 Kafka Replay
MS-SEARCH-008 Reconciliation
MS-SEARCH-009 Failure Tests
MS-SEARCH-010 Cutover
```

---

## V3.5 — IDENTITY EXTRACTION

```text
MS-ID-001 Identity Service
MS-ID-002 identity_db
MS-ID-003 Credential Migration
MS-ID-004 Session Migration
MS-ID-005 Token APIs
MS-ID-006 Authentication API
MS-ID-007 Internal Identity Propagation
MS-ID-008 Service Authentication
MS-ID-009 Security Tests
MS-ID-010 Cutover
```

---

## V3.6 — CATALOG EXTRACTION

```text
MS-CAT-001 Catalog Service
MS-CAT-002 catalog_db
MS-CAT-003 Product/SKU Migration
MS-CAT-004 Catalog API
MS-CAT-005 Outbox
MS-CAT-006 Kafka Events
MS-CAT-007 Search Integration
MS-CAT-008 Contract Tests
MS-CAT-009 Failure Tests
MS-CAT-010 Cutover
```

---

## V3.7 — PAYMENT EXTRACTION

```text
MS-PAY-001 Payment Service
MS-PAY-002 payment_db
MS-PAY-003 Provider API
MS-PAY-004 Create Payment
MS-PAY-005 Webhook
MS-PAY-006 Idempotency
MS-PAY-007 Reconciliation
MS-PAY-008 Partial Refund
MS-PAY-009 Refund Concurrency
MS-PAY-010 Outbox
MS-PAY-011 Inbox
MS-PAY-012 Kafka Events
MS-PAY-013 Authentication
MS-PAY-014 Failure Tests
MS-PAY-015 Cutover
```

---

## V3.8 — API GATEWAY

```text
MS-GW-001 Bootstrap
MS-GW-002 Routing
MS-GW-003 Authentication
MS-GW-004 Identity Propagation
MS-GW-005 Rate Limiting
MS-GW-006 Request ID
MS-GW-007 Trace Propagation
MS-GW-008 Timeout Policy
MS-GW-009 Security Headers
MS-GW-010 Observability
```

---

## V3.9 — INVENTORY EXTRACTION

```text
MS-INV-001 Inventory Service
MS-INV-002 inventory_db
MS-INV-003 Stock Migration
MS-INV-004 Reservation API
MS-INV-005 Commit API
MS-INV-006 Release API
MS-INV-007 Expiry Worker
MS-INV-008 Stock Adjustment
MS-INV-009 Outbox
MS-INV-010 Kafka Events
MS-INV-011 Concurrency Tests
MS-INV-012 Failure Tests
MS-INV-013 Cutover
```

Canonical Inventory invariant must remain unchanged.

---

## V3.10 — ORDER EXTRACTION

```text
MS-ORDER-001 Order Service
MS-ORDER-002 order_db
MS-ORDER-003 Parent/Seller Model Migration
MS-ORDER-004 Order State Machines
MS-ORDER-005 Snapshots
MS-ORDER-006 API
MS-ORDER-007 Outbox
MS-ORDER-008 Inbox
MS-ORDER-009 Payment Events
MS-ORDER-010 Inventory Integration
MS-ORDER-011 Shipping Events
MS-ORDER-012 Failure Tests
MS-ORDER-013 Cutover
```

---

## V3.11 — CHECKOUT SAGA

```text
SAGA-001 Saga Architecture
SAGA-002 Saga Persistence
SAGA-003 checkout_reference_id Propagation
SAGA-004 Cart Validation
SAGA-005 Catalog Validation
SAGA-006 Voucher Reservation
SAGA-007 Inventory Reservation
SAGA-008 Order Creation
SAGA-009 Payment Creation
SAGA-010 Compensation
SAGA-011 Idempotent Commands
SAGA-012 Crash Recovery
SAGA-013 Retry Policy
SAGA-014 Timeout Policy
SAGA-015 Observability
SAGA-016 Chaos Tests
```

---

## V3.12 — SHIPPING EXTRACTION

```text
MS-SHIP-001 Shipping Service
MS-SHIP-002 shipping_db
MS-SHIP-003 Provider Adapter
MS-SHIP-004 Shipment API
MS-SHIP-005 Seller Order Integration
MS-SHIP-006 Tracking
MS-SHIP-007 Kafka Events
MS-SHIP-008 Reconciliation
MS-SHIP-009 Failure Tests
MS-SHIP-010 Cutover
```

---

## V3.13 — ANALYTICS EXTRACTION

```text
MS-AN-001 Analytics Service
MS-AN-002 analytics_db
MS-AN-003 Kafka Consumers
MS-AN-004 Sales Projection
MS-AN-005 Seller Projection
MS-AN-006 Funnel Projection
MS-AN-007 Replay
MS-AN-008 Dashboard API
```

---

## V3.14 — DISTRIBUTED SECURITY

```text
SEC3-001 Internal JWT
SEC3-002 Service Identity
SEC3-003 Service Authorization
SEC3-004 mTLS Evaluation
SEC3-005 Kubernetes RBAC
SEC3-006 Network Policies
SEC3-007 Secret Management
SEC3-008 Container Hardening
SEC3-009 Supply Chain Scanning
SEC3-010 Security Tests
```

---

## V3.15 — OBSERVABILITY

```text
OBS3-001 OpenTelemetry Collector
OBS3-002 Distributed Tracing
OBS3-003 Trace Propagation
OBS3-004 Prometheus
OBS3-005 Kafka Metrics
OBS3-006 RabbitMQ Metrics
OBS3-007 PostgreSQL Metrics
OBS3-008 Grafana
OBS3-009 Loki
OBS3-010 Tempo/Jaeger
OBS3-011 SLI
OBS3-012 SLO
OBS3-013 Alerts
```

---

## V3.16 — KUBERNETES & HELM

```text
K8S-001 Namespaces
K8S-002 Deployments
K8S-003 Services
K8S-004 Ingress
K8S-005 ConfigMaps
K8S-006 Secrets
K8S-007 Liveness
K8S-008 Readiness
K8S-009 Resource Requests/Limits
K8S-010 HPA
K8S-011 PDB
K8S-012 Rolling Updates
K8S-013 Graceful Shutdown
K8S-014 Helm
```

---

## V3.17 — RELIABILITY ENGINEERING

```text
REL-001 Timeout Standards
REL-002 Retry Standards
REL-003 Retry Budget
REL-004 Circuit Breaker
REL-005 Bulkhead
REL-006 Backpressure
REL-007 RabbitMQ DLQ
REL-008 Kafka Retry Strategy
REL-009 Payment Reconciliation
REL-010 Search Reconciliation
REL-011 Shipping Reconciliation
REL-012 Saga Recovery
```

---

## V3.18 — CHAOS & PERFORMANCE

```text
TEST3-001 Service Integration
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
TEST3-013 k6
TEST3-014 pprof
TEST3-015 Capacity Report
```

---

## V3.19 — INFRASTRUCTURE AS CODE

```text
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

# 38. REQUIRED V3 DEMOS

```text
1. Independent deployment

2. Independent scaling

3. Inventory concurrency across replicas

4. Duplicate PaymentSucceeded event

5. RabbitMQ outage + recovery

6. Kafka outage + recovery

7. Kafka replay rebuilds Search

8. Consumer crash before ACK/offset commit

9. Saga compensation

10. Saga crash recovery

11. Payment timeout uncertainty

12. Shipping provider failure

13. Distributed trace

14. Pod failure during traffic

15. Redis degradation

16. Catalog service independently deployed

17. Identity service independently deployed
```

---

# 39. V3 DEFINITION OF DONE

Architecture:

```text
multiple independently deployable services exist
database-per-service enforced
no cross-service SQL
service APIs/contracts documented
```

Messaging:

```text
RabbitMQ operational messaging works
Kafka event stream works
Outbox exists where required
Inbox/idempotency exists
replay works
duplicate messages safe
```

Saga:

```text
state persisted
commands idempotent
compensation idempotent
crash recovery works
timeouts handled explicitly
```

Security:

```text
external authentication
service identity
service authorization
network policies
secret management
supply-chain scanning
```

Operations:

```text
Kubernetes
Helm
Terraform
HPA
PDB
rolling deployment
health/readiness
graceful shutdown
```

Observability:

```text
distributed traces
logs
metrics
SLIs
SLOs
alerts
```

Testing:

```text
unit
integration
contract
concurrency
failure
chaos
load
```

---

# 40. FINAL EVOLUTION

```text
V1
Correctness
Transactions
Concurrency
Security
Production Core
        │
        ▼
V2
Business Completeness
Event-Driven Architecture
Outbox / Inbox
RabbitMQ
Search
Failure Recovery
        │
        ▼
V3
Selective Microservices
Database-per-Service
RabbitMQ + Kafka
Saga
Kubernetes
Distributed Tracing
Infrastructure as Code
```

---

# 41. FINAL V3 PHILOSOPHY

```text
Do not distribute unstable boundaries.

Preserve business invariants.

Own your database.

Assume networks fail.

Assume messages duplicate.

Never confuse timeout with failure.

Make commands idempotent.

Make compensation idempotent.

Use RabbitMQ for work.

Use Kafka for streams.

Persist workflow state.

Reconcile uncertain external state.

Observe before scaling.

Extract incrementally.

Test failures, not only happy paths.

Avoid distributed monoliths.
```

Nexus-Commerce V3 should demonstrate that a correctly designed modular monolith can evolve into a distributed system **without sacrificing the business correctness established in V1**.
