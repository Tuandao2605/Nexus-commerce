# NEXUS-COMMERCE V1 — CANONICAL EXECUTION PLAN

## Production-Grade Multi-Vendor E-Commerce Backend

**Status:** Active Development  
**Current Stage:** V1 — Production-Grade Core Commerce  
**Current Active Ticket:** `ECOM-DB-004D — Inventory Schema`
**Next Stage:** V2 — Business-Complete Event-Driven Modular Monolith  
**Future Stage:** V3 — Selective Microservices & Event Streaming

---

# 1. PURPOSE

Tài liệu này là canonical execution plan của Nexus-Commerce V1.

Nếu yêu cầu cũ mâu thuẫn với các quyết định đã PASS trong:

```text
ECOM-ARCH-002
ECOM-DATA-003A
ECOM-DATA-003B
ECOM-DATA-003C
ECOM-DATA-003D
ECOM-DATA-003E
ECOM-DATA-003F
ECOM-DATA-003G
```

thì quyết định mới hơn được ưu tiên.

---

# 2. V1 VISION

Nexus-Commerce V1 là một:

```text
Production-Grade Modular Monolith
```

phải chứng minh được:

```text
Authentication
Authorization
Seller ownership
Product / Variant / SKU modeling
Inventory concurrency correctness
Global multi-shop Cart
Voucher concurrency correctness
Parent / Seller Order modeling
Checkout orchestration
Payment reliability
Idempotency
Webhook deduplication
Refund concurrency
Rate limiting
Audit logging
Observability
Integration testing
Concurrency testing
Load testing
Production-oriented deployment
```

---

# 3. OFFICIAL STACK

```text
Language                 Go
HTTP                     Chi
Database                 PostgreSQL
Database Driver          pgx/v5 + pgxpool
SQL Generation           sqlc
Migration                golang-migrate
Cache / Coordination     Redis
Object Storage           MinIO / S3-compatible
Logging                  log/slog
Metrics                  Prometheus
Dashboard                Grafana
Testing                  testing + Testify + Testcontainers
Load Testing             k6
Deployment               Docker + Docker Compose
```

V1 chưa cần:

```text
Kafka
RabbitMQ as mandatory runtime infrastructure
Distributed Saga
Database-per-Service
Service Mesh
Kubernetes
Distributed Tracing
Independent microservice deployment
```

---

# 4. ARCHITECTURE EVOLUTION

```text
V1
Production-Grade Modular Monolith
        │
        ▼
V2
Event-Driven Modular Monolith
Transactional Outbox
RabbitMQ
Inbox / Idempotent Consumers
DLQ / Retry / Event Versioning
        │
        ▼
V3
Selective Microservices
Kafka where justified
Independent scaling/deployment
Distributed observability
```

---

# 5. CORE MODULES

```text
1. Auth
2. User
3. Seller
4. Catalog
5. Inventory
6. Cart
7. Order
8. Voucher
9. Payment
10. Notification
```

Checkout là application orchestration, không phải module sở hữu business data riêng.

---

# 6. CANONICAL DATA OWNERSHIP

```text
Auth          → credentials, sessions, refresh/security credentials
User          → users, user_addresses
Seller        → seller_accounts, shops, shop_memberships
Catalog       → categories, brands, products, product_variants, skus
Inventory     → warehouses, inventory_stocks, inventory_reservations,
                inventory_reservation_items, stock_movements
Cart          → carts, cart_items
Order         → orders, order_items, order_status_histories
Voucher       → vouchers, voucher_usages
Payment       → payment_transactions, payment_refunds,
                payment_webhook_events
Notification  → notification records / delivery state
```

Rule:

```text
one mutable source of truth
→ one owning module
```

Payment không trực tiếp UPDATE Order.status.
Order không trực tiếp mutate Voucher quota.
Cart không trực tiếp reserve Inventory.

---

# 7. CANONICAL DOMAIN DECISIONS

## Identity

```text
users
credentials
sessions
user_addresses
```

Các bảng refresh token / verification / reset token được thêm khi implementation Auth yêu cầu.

## Seller + Catalog

```text
seller_accounts
shops
shop_memberships

categories
brands
products
product_variants
skus
```

Sellable chain:

```text
Product
└── ProductVariant
    └── SKU
```

## Inventory

```text
warehouses
inventory_stocks
inventory_reservations
inventory_reservation_items
stock_movements
```

Persist:

```text
on_hand_quantity
reserved_quantity
```

Derived:

```text
available_quantity = on_hand_quantity - reserved_quantity
```

Không persist `available_quantity` như mutable source of truth.

Reservation/release/expire không tạo physical stock movement.
Commit mới làm:

```text
on_hand -= qty
reserved -= qty
SALE movement = -qty
```

## Cart

```text
carts
cart_items
```

Cart là purchase intent, không là price source, inventory reservation hay historical order.

## Order

```text
Parent Order
├── Seller Order A
│   └── OrderItems
├── Seller Order B
│   └── OrderItems
└── ...
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

Seller Order states:

```text
pending
confirmed
processing
shipped
delivered
cancelled
```

Order giữ historical snapshots.

## Voucher

```text
vouchers
voucher_usages
```

Scope:

```text
platform
shop
```

Usage lifecycle:

```text
RESERVED
├── COMMITTED
├── RELEASED
└── EXPIRED
```

Reservation có TTL.
Global quota dùng atomic counter.
Per-user quota dùng transaction-scoped locking discipline.

## Payment

```text
payment_transactions
payment_refunds
payment_webhook_events
```

Parent Order có nhiều historical payment attempts nhưng V1 chỉ cho một economically live attempt:

```text
pending
processing
succeeded
```

Webhook:

```text
UNIQUE(provider, provider_event_id)
```

Provider payment:

```text
UNIQUE(provider, provider_payment_id)
```

Refund hỗ trợ partial refund và concurrency-safe allocation.

---

# 8. CROSS-DOMAIN INVARIANTS

## Tenant consistency

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

## Checkout correlation

```text
checkout_reference_id
```

là business correlation spine:

```text
Checkout X
├── InventoryReservation.reference_type = 'checkout'
├── InventoryReservation.reference_id = X
├── VoucherUsage.checkout_reference_id = X
└── ParentOrder.checkout_reference_id = X
```

## Parent-safe Order references

Migrations phải chứng minh:

```text
orders.parent_order_id
→ Parent Order only

voucher_usages.order_id
→ Parent Order only

payment_transactions.parent_order_id
→ Parent Order only
```

## User integrity

```text
SellerOrder.user_id = ParentOrder.user_id
VoucherUsage.user_id = ParentOrder.user_id
VoucherUsage.checkout_reference_id = ParentOrder.checkout_reference_id
```

## Currency

```text
one Checkout hierarchy = exactly one currency
```

No FX conversion in V1.

## Money

```text
BIGINT minor units
+
CHAR(3) currency
```

Không dùng FLOAT/REAL/DOUBLE cho money.

## TTL

```text
business deadline != worker execution time
```

Deadline đã qua thì commit reject ngay cả khi cleanup worker chưa chạy.

---

# 9. OFFICIAL WORKFLOW GIỮA CHÚNG TA

```text
1. Tôi giao ticket/spec
        ↓
2. Bạn implement
        ↓
3. Bạn gửi code/tài liệu
        ↓
4. Tôi review
        ↓
5. PASS / PASS WITH CHANGES / NEEDS REVISION
        ↓
6. Bạn sửa blocking issues
        ↓
7. Chạy acceptance tests
        ↓
8. Close ticket
        ↓
9. Sang ticket kế tiếp
```

Không triển khai nhiều module cùng lúc.

Mỗi review tập trung vào:

```text
correctness
ownership
transaction boundaries
concurrency
idempotency
failure handling
security
testability
production behavior
```

---

# 10. COMPLETED SEQUENCE

```text
ECOM-GO-001
Bootstrap Go Backend
✅ PASS

ECOM-ARCH-002
Module Boundaries & Data Ownership
✅ PASS
```

Domain design:

```text
ECOM-DATA-003A Identity — User + Auth      ✅ PASS
ECOM-DATA-003B Seller + Catalog            ✅ PASS
ECOM-DATA-003C Inventory                   ✅ PASS
ECOM-DATA-003D Cart                        ✅ PASS
ECOM-DATA-003E Order                       ✅ PASS
ECOM-DATA-003F Voucher + Payment           ✅ PASS
ECOM-DATA-003G Full ERD Review             ✅ DESIGN ACCEPTED
```

Database implementation:

```text
ECOM-DB-004A Migration Foundation          ✅ PASS (verified 2026-09-11)
ECOM-DB-004B Identity Schema               ✅ PASS (verified 2026-09-14)
ECOM-DB-004C Seller + Catalog Schema       ✅ PASS (verified 2026-09-14)
```

Các correction của DATA-003G phải được encode vào migration; không tạo thêm một design phase mới.

---

# 11. CURRENT PHASE — ECOM-DB-004

## PostgreSQL Schema & Migrations

```text
ECOM-DB-004A Migration Foundation          ✅ PASS
ECOM-DB-004B Identity Schema               ✅ PASS
ECOM-DB-004C Seller + Catalog Schema       ✅ PASS
ECOM-DB-004D Inventory Schema              ← CURRENT
ECOM-DB-004E Cart Schema
ECOM-DB-004F Order Schema
ECOM-DB-004G Voucher Schema
ECOM-DB-004H Payment Schema
ECOM-DB-004I Cross-Domain Constraint Tests
ECOM-DB-004J Full Migration Review
```

Trong DB-004:

```text
DO:
PostgreSQL
pgxpool foundation
golang-migrate
tables
PK/FK
composite FK
UNIQUE
CHECK
indexes
rollback
DB integration tests
constraint/concurrency tests

DO NOT:
business HTTP endpoints
full repositories/services
Redis business logic
RabbitMQ/Kafka
Elasticsearch
```

Migration dependency:

```text
001 Identity
002 Seller
003 Catalog
004 Inventory
005 Cart
006 Order
007 Voucher
008 Payment
```

---

# 12. DB-004A — MIGRATION FOUNDATION

**Status:** ✅ PASS — verified 2026-09-11

Required:

```text
PostgreSQL via Docker Compose
DATABASE_URL configuration
pgx/v5 + pgxpool
startup Ping with timeout
clean failure when DB unavailable
pool close on shutdown
golang-migrate CLI
Makefile migration commands
migration up/down verification
database integration test
```

Không tạo hàng loạt domain tables ở DB-004A.

Gate:

```text
DB-004A PASS
→ DB-004B
```

---

# 13. PRE-MIGRATION HARDENING

Order:

```text
Parent-safe orders.parent_order_id
Parent-safe VoucherUsage order reference
Parent-safe PaymentTransaction order reference
SellerOrder.user_id = Parent.user_id
Parent/Seller currency consistency
one Parent per checkout_reference_id
one Seller Order per Shop per Parent
```

Voucher:

```text
atomic global quota
per-user concurrency discipline
reservation expires_at
EXPIRED recovery
same voucher + checkout = one logical usage
eligible amount isolated by scope
```

Payment:

```text
one live payment attempt per Parent
provider-scoped payment identity
provider-scoped refund identity
durable webhook dedup
refund allocated capacity <= payment amount
```

Inventory:

```text
available = on_hand - reserved
reserved <= on_hand
cross-Shop stock impossible
idempotency key + request hash
reference_type='checkout'
reference_id=checkout_reference_id
deadline decides validity
```

---

# 14. AFTER DB-004 — SQLC FOUNDATION

```text
ECOM-DB-005 — SQLC Foundation
```

Scope:

```text
sqlc.yaml
schema visibility
pgx/v5 generation
generated package layout
type overrides if required
codegen verification
```

Không viết toàn bộ business queries từ trước.

Business query được thêm cùng ticket module cần nó.

---

# 15. APPLICATION IMPLEMENTATION ROADMAP

## A. Auth + User

```text
ECOM-AUTH-001 Registration
ECOM-AUTH-002 Password Hashing
ECOM-AUTH-003 Login
ECOM-AUTH-004 Access Token
ECOM-AUTH-005 Refresh Token Schema/Queries
ECOM-AUTH-006 Refresh Rotation
ECOM-AUTH-007 Logout / Revocation
ECOM-AUTH-008 Password Reset
ECOM-AUTH-009 Email Verification
ECOM-AUTH-010 Session Management

ECOM-USER-001 Profile
ECOM-USER-002 Address CRUD
ECOM-USER-003 Default Address
ECOM-USER-004 Ownership Tests
```

## B. Authorization & Security

```text
ECOM-SEC-001 Role Model
ECOM-SEC-002 Permission Model
ECOM-SEC-003 Authentication Middleware
ECOM-SEC-004 RBAC
ECOM-SEC-005 Ownership Authorization
ECOM-SEC-006 Sensitive Endpoint Rate Limits
ECOM-SEC-007 Audit Logging Foundation
ECOM-SEC-008 Security Tests
```

## C. Seller

```text
ECOM-SELLER-001 Seller Registration
ECOM-SELLER-002 Seller Status Workflow
ECOM-SELLER-003 Shop Creation
ECOM-SELLER-004 Shop Membership
ECOM-SELLER-005 Shop-Scoped Authorization
ECOM-SELLER-006 Seller Integration Tests
```

## D. Catalog

```text
ECOM-CAT-001 Category
ECOM-CAT-002 Brand
ECOM-CAT-003 Product
ECOM-CAT-004 Product Variant
ECOM-CAT-005 SKU
ECOM-CAT-006 Product Attributes
ECOM-CAT-007 Product Images
ECOM-CAT-008 Product/SKU Lifecycle
ECOM-CAT-009 Product Query
ECOM-CAT-010 PostgreSQL Search V1
ECOM-CAT-011 Tenant/Ownership Tests
```

Elasticsearch chuyển V2.

## E. Inventory

```text
ECOM-INV-001 Warehouse Operations
ECOM-INV-002 Stock Receipt / Adjustment
ECOM-INV-003 Stock Movement Ledger
ECOM-INV-004 Atomic Reservation
ECOM-INV-005 Multi-SKU Reservation
ECOM-INV-006 Reservation Commit
ECOM-INV-007 Reservation Release
ECOM-INV-008 Reservation Expiration
ECOM-INV-009 Expiration Worker
ECOM-INV-010 Idempotency
ECOM-INV-011 Concurrency Tests
ECOM-INV-012 Failure/Deadlock Tests
```

## F. Cart

```text
ECOM-CART-001 Active Cart Lifecycle
ECOM-CART-002 Add Item
ECOM-CART-003 Update Quantity
ECOM-CART-004 Remove Item
ECOM-CART-005 Cart Query
ECOM-CART-006 Concurrent Mutation Protection
ECOM-CART-007 Checkout Lock Integration
ECOM-CART-008 Revalidation Tests
```

## G. Voucher

```text
ECOM-VOUCHER-001 Voucher Management
ECOM-VOUCHER-002 Eligibility
ECOM-VOUCHER-003 Discount Calculation
ECOM-VOUCHER-004 Scope Isolation
ECOM-VOUCHER-005 Reserve Usage
ECOM-VOUCHER-006 Commit Usage
ECOM-VOUCHER-007 Release / Expire Usage
ECOM-VOUCHER-008 Per-User Limit
ECOM-VOUCHER-009 Idempotency
ECOM-VOUCHER-010 Concurrency Tests
```

## H. Order

```text
ECOM-ORDER-001 Parent/Seller Aggregate
ECOM-ORDER-002 Order Snapshot Creation
ECOM-ORDER-003 Order Items
ECOM-ORDER-004 Seller Order State Machine
ECOM-ORDER-005 Parent Aggregate State
ECOM-ORDER-006 Status History
ECOM-ORDER-007 Customer Query
ECOM-ORDER-008 Seller Query / Authorization
ECOM-ORDER-009 Cancellation
ECOM-ORDER-010 Transition Tests
```

## I. Checkout

```text
ECOM-CHECKOUT-001 Checkout Command
ECOM-CHECKOUT-002 Cart Lock / Selection
ECOM-CHECKOUT-003 Catalog Revalidation
ECOM-CHECKOUT-004 Currency Validation
ECOM-CHECKOUT-005 Voucher Reservation
ECOM-CHECKOUT-006 Inventory Reservation
ECOM-CHECKOUT-007 Commercial Calculation
ECOM-CHECKOUT-008 Parent/Seller Order Creation
ECOM-CHECKOUT-009 Cart Checked-Out Transition
ECOM-CHECKOUT-010 Compensation
ECOM-CHECKOUT-011 Idempotency
ECOM-CHECKOUT-012 Integration / E2E
```

## J. Payment

```text
ECOM-PAY-001 Provider Interface
ECOM-PAY-002 Mock Provider
ECOM-PAY-003 Create Payment Attempt
ECOM-PAY-004 Payment Command Idempotency
ECOM-PAY-005 Webhook Endpoint
ECOM-PAY-006 Signature Verification
ECOM-PAY-007 Durable Webhook Dedup
ECOM-PAY-008 Payment State Machine
ECOM-PAY-009 Order Fact Integration
ECOM-PAY-010 Reconciliation
ECOM-PAY-011 Partial Refund
ECOM-PAY-012 Refund Idempotency
ECOM-PAY-013 Refund Concurrency
ECOM-PAY-014 Failure Tests
```

## K. Notification

```text
ECOM-NOTIF-001 Notification Record
ECOM-NOTIF-002 Email Adapter
ECOM-NOTIF-003 Registration / Verification
ECOM-NOTIF-004 Password Reset
ECOM-NOTIF-005 Order Notification
ECOM-NOTIF-006 Payment Notification
ECOM-NOTIF-007 Retry / Failure Isolation
```

---

# 16. REDIS & PERFORMANCE

```text
ECOM-REDIS-001 Redis Client
ECOM-REDIS-002 Product Cache
ECOM-REDIS-003 Cache Invalidation
ECOM-REDIS-004 Distributed Rate Limiter
ECOM-REDIS-005 Session/Coordination Review

ECOM-PERF-001 Query Profiling
ECOM-PERF-002 EXPLAIN ANALYZE Review
ECOM-PERF-003 Index Review
ECOM-PERF-004 Connection Pool Tuning
```

Redis chỉ thêm sau khi core PostgreSQL correctness đã ổn.

---

# 17. OBSERVABILITY

```text
ECOM-OBS-001 Request ID
ECOM-OBS-002 Structured Request Logging
ECOM-OBS-003 Prometheus
ECOM-OBS-004 HTTP Metrics
ECOM-OBS-005 DB Metrics
ECOM-OBS-006 Redis Metrics
ECOM-OBS-007 Business Metrics
ECOM-OBS-008 Grafana
```

---

# 18. TESTING

Testing diễn ra trong từng ticket, không dồn hết về cuối.

Final hardening:

```text
ECOM-TEST-001 Unit Suite
ECOM-TEST-002 Repository Integration Suite
ECOM-TEST-003 API Integration Suite
ECOM-TEST-004 Inventory Concurrency
ECOM-TEST-005 Voucher Concurrency
ECOM-TEST-006 Checkout Idempotency
ECOM-TEST-007 Payment Duplicate Webhook
ECOM-TEST-008 Payment Timeout/Reconciliation
ECOM-TEST-009 Refund Concurrency
ECOM-TEST-010 Security / IDOR
ECOM-TEST-011 Full E2E
ECOM-TEST-012 Failure Scenarios
```

---

# 19. LOAD TESTING

```text
ECOM-LOAD-001 Synthetic Data
ECOM-LOAD-002 Browse
ECOM-LOAD-003 Search
ECOM-LOAD-004 Login
ECOM-LOAD-005 Cart
ECOM-LOAD-006 Checkout
ECOM-LOAD-007 Order History
ECOM-LOAD-008 Report
ECOM-LOAD-009 Bottleneck Analysis
```

---

# 20. DEPLOYMENT & OPERATIONS

```text
ECOM-OPS-001 Production Dockerfile
ECOM-OPS-002 Docker Compose
ECOM-OPS-003 Config Validation
ECOM-OPS-004 Graceful Shutdown Review
ECOM-OPS-005 Health / Readiness
ECOM-OPS-006 CI
ECOM-OPS-007 Security Scanning
ECOM-OPS-008 PostgreSQL Backup
ECOM-OPS-009 Restore Test
ECOM-OPS-010 Deployment Documentation
```

---

# 21. REQUIRED V1 DEMOS

```text
1. Complete purchase
2. Inventory race
3. Voucher race
4. Checkout retry/idempotency
5. Duplicate payment webhook
6. Payment timeout + reconciliation
7. Concurrent partial refund
8. Seller isolation
9. Customer IDOR
10. Graceful shutdown
```

---

# 22. V1 DEFINITION OF DONE

Business:

```text
Auth/User
Seller/Shop
Catalog
Inventory
Cart
Voucher
Order
Checkout
Payment/Refund
Notification
```

Correctness:

```text
stock never oversells
voucher quota never oversubscribes
expired holds cannot commit
mixed-currency checkout cannot create invalid hierarchy
cross-Shop attachment is rejected
Parent/Seller ownership is consistent
frontend cannot control final price
invalid order transitions fail
checkout retry does not duplicate order hierarchy
payment retry does not double charge
duplicate webhook does not duplicate effect
concurrent refunds do not over-refund
```

Database:

```text
migrations up/down verified
constraints tested
composite FKs tested
critical indexes reviewed
transactions tested
pgxpool configured
sqlc stable
```

Testing:

```text
unit
integration
concurrency
failure
security
E2E
load report
```

Operations:

```text
Docker
Docker Compose
health/readiness
graceful shutdown
CI
security scan
backup
restore
metrics
```

---

# 23. V1 → V2 GATE

Không sang V2 cho tới khi:

```text
module boundaries stable
database invariants tested
inventory concurrency proven
voucher concurrency proven
checkout idempotency proven
payment idempotency proven
reconciliation works
refund concurrency works
ownership authorization works
integration/E2E tests exist
Docker environment stable
logs/metrics usable
```

V2 mới thêm:

```text
Shipping
Returns
Reviews
Analytics
Elasticsearch
RabbitMQ
Transactional Outbox
Inbox
Idempotent Consumers
Retry / DLQ
Event Versioning
Async Notification
Failure Recovery
```

---

# 24. CURRENT NEXT ACTION

```text
ECOM-DB-004A Migration Foundation          ✅ PASS
        ↓
ECOM-DB-004B Identity Schema               ✅ PASS
        ↓
ECOM-DB-004C Seller + Catalog Schema       ✅ PASS
        ↓
ECOM-DB-004D Inventory Schema              ← CURRENT
        ↓ PASS
ECOM-DB-004E Cart Schema
        ↓ PASS
ECOM-DB-004F Order Schema
        ↓ PASS
ECOM-DB-004G Voucher Schema
        ↓ PASS
ECOM-DB-004H Payment Schema
        ↓ PASS
ECOM-DB-004I Cross-Domain Constraint Tests
        ↓ PASS
ECOM-DB-004J Full Migration Review
        ↓
ECOM-DB-005 SQLC Foundation
```

Sau đó mới bước vào business implementation.

---

# 25. FINAL RULE

```text
Design approved
↓
Implement smallest scoped ticket
↓
Test valid path
↓
Test invalid path
↓
Test retry/concurrency where relevant
↓
Review
↓
PASS
↓
Next ticket
```

Đây là quy trình chính thức của Nexus-Commerce V1.
