# ECOM-DB-004H — Payment Schema

Status: DONE

## 1. Context & Scope

Ticket này đặc tả kỹ thuật để triển khai Schema cho Payment Domain trong Nexus-Commerce dựa trên các tài liệu canonical:

- `docs/data-003f-voucher-payment.md`
- `docs/erd.md`

**Lưu ý quan trọng**:

- KHÔNG tạo bảng `payment_status_histories` ở phiên bản V1 này (theo cấu trúc AD-003F-23).
- Chỉ định nghĩa Schema và Validation/Integration tests. Các implement liên quan đến Go logic, RabbitMQ sẽ thực hiện ở ticket khác.

Phạm vi tạo bảng:

1. `payment_transactions`: Lưu thông tin các lần thử thanh toán.
2. `payment_webhook_events`: Inbox/Deduplication cho provider webhook events.
3. `payment_refunds`: Quản lý các khoản hoàn tiền.

---

## 2. Core Domain Rules & Invariants

1. **Payment Ownership**: Payment Module là nguồn chân lý (Source of Truth) cho trạng thái thanh toán. KHÔNG trực tiếp cập nhật `orders.status` từ Payment Repository.
2. **Multi-attempt Model**: Một Parent Order có thể có nhiều `PaymentTransactions` (do user thử lại hoặc đổi phương thức), nhưng **tối đa 1 "live" attempt** (tức là trạng thái `pending`, `processing`, hoặc `succeeded`).
3. **Composite Parent FK**:
   `payment_transactions` bắt buộc tham chiếu Parent Order bằng composite key: `(parent_order_id, currency_code, parent_order_type)` tham chiếu đến `orders(id, currency, order_type)`. Điều này chặn lỗi khác currency hoặc reference nhầm vào Seller Order.
4. **Idempotency & Provider Identity**:
   Sử dụng `idempotency_key` cho deduplication command ở server, `request_hash` cho validation. Phải đảm bảo `(provider, provider_payment_id)` là Unique.
5. **Refund Allocation Concurrency**:
   Bảng `payment_transactions` phải có field `allocated_refund_amount`. Mọi thao tác cấp phát refund (trạng thái pending/processing/succeeded) phải được atomic update trên column này. Chặn cấp phát vượt quá `amount` ban đầu.
6. **State-Timestamp Matrices**: Trạng thái (status) phải khớp 100% với sự tồn tại của các timestamp. Đặc biệt `succeeded` bắt buộc phải có CẢ `processing_at IS NOT NULL AND succeeded_at IS NOT NULL`.

---

## 3. Schema Definitions

### 3.1. `payment_transactions`

- **PK**: `id` UUID
- **Order FK**: `parent_order_id`, `parent_order_type` (bắt buộc `'parent'`), `currency_code` (CHAR(3)).
- **Identifiers**:
  - `provider` (VARCHAR), `provider_payment_id` (VARCHAR), `idempotency_key` (VARCHAR), `request_hash` (CHAR(64)).
- **Finances**: `amount` (BIGINT, >0), `allocated_refund_amount` (BIGINT, NOT NULL, DEFAULT 0).
- **Status & Lifecycle**: `status` (pending, processing, succeeded, failed, cancelled), `failure_code`, `failure_message`.
- **Timestamps**: `expires_at`, `created_at`, `updated_at`, `processing_at`, `succeeded_at`, `failed_at`, `cancelled_at`.
- **Indexes & Constraints**:
  - `uq_payment_parent_live_attempt`: UNIQUE trên `parent_order_id` WHERE status IN ('pending', 'processing', 'succeeded').
  - **[BLOCKER FIX]** Supporting Keys cho Downstream Composite FKs:
    - `uq_payment_transactions_id_provider`: UNIQUE (id, provider)
    - `uq_payment_transactions_id_provider_currency`: UNIQUE (id, provider, currency_code)
  - **[MAJOR FIX]** Provider Identity Partial Unique Index:
    - `uq_payment_provider_payment`: UNIQUE (provider, provider_payment_id) WHERE provider_payment_id IS NOT NULL
  - **[MINOR FIX]** Concurrency Counter:
    - `CHECK (allocated_refund_amount >= 0 AND allocated_refund_amount <= amount)`
  - **[BLOCKER FIX]** State-Timestamp Matrix (`succeeded` = `processing_at IS NOT NULL AND succeeded_at IS NOT NULL`).
  - **[MAJOR FIX]** Timeline Integrity Checks:
    - `CHECK (updated_at >= created_at)`
    - `CHECK (expires_at > created_at)`
    - `CHECK (processing_at IS NULL OR processing_at >= created_at)`
    - `CHECK (succeeded_at IS NULL OR succeeded_at >= created_at)`
    - `CHECK (failed_at IS NULL OR failed_at >= created_at)`
    - `CHECK (cancelled_at IS NULL OR cancelled_at >= created_at)`
    - `CHECK (currency_code = UPPER(currency_code) AND CHAR_LENGTH(currency_code) = 3)`
- **ON DELETE**: RESTRICT tham chiếu về `orders`.

### 3.2. `payment_webhook_events`

- **PK**: `id` UUID
- **Provider Event Deduplication**: `provider` (VARCHAR), `provider_event_id` (VARCHAR) -> UNIQUE `(provider, provider_event_id)`.
- **FK**: `payment_transaction_id` (tham chiếu cùng `provider` qua Composite FK).
- **Data**: `event_type`, `payload_hash` (CHAR 64).
- **Lifecycle**: `processing_status` (received, processed, ignored, failed), `last_error`.
- **Timestamps**: `received_at`, `processed_at`.
- **Indexes (Worker Retry & Lookup)**:
  - `idx_payment_webhook_processing`: ON (processing_status, received_at) WHERE processing_status IN ('received', 'failed')
  - `idx_payment_webhook_payment`: ON (payment_transaction_id, received_at DESC) WHERE payment_transaction_id IS NOT NULL

### 3.3. `payment_refunds`

- **PK**: `id` UUID
- **Payment FK**: `payment_transaction_id`, `provider`, `currency_code` -> Tham chiếu Transaction phải khớp cả 3.
- **Identifiers**: `provider_refund_id`, `idempotency_key`, `request_hash`.
- **Finances**: `amount` (BIGINT, >0).
- **Status & Lifecycle**: `status` (pending, processing, succeeded, failed, cancelled), `reason`, `failure_code`, `failure_message`.
- **Timestamps**: `created_at`, `updated_at`, `processing_at`, `succeeded_at`, `failed_at`, `cancelled_at`.
- **Indexes & Constraints**:
  - **[MAJOR FIX]** Provider Refund Identity Partial Unique Index:
    - `uq_refund_provider_refund`: UNIQUE (provider, provider_refund_id) WHERE provider_refund_id IS NOT NULL
  - Đồng bộ State-Timestamp Matrix và Timeline Checks tương tự bảng `payment_transactions`.
    - `CHECK (updated_at >= created_at)`
    - `CHECK (processing_at IS NULL OR processing_at >= created_at)`
    - `CHECK (succeeded_at IS NULL OR succeeded_at >= created_at)`
    - `CHECK (failed_at IS NULL OR failed_at >= created_at)`
    - `CHECK (cancelled_at IS NULL OR cancelled_at >= created_at)`
    - `CHECK (currency_code = UPPER(currency_code) AND CHAR_LENGTH(currency_code) = 3)`

---

## 4. Acceptance Criteria & Integration Testing (Tối thiểu 28 Tests)

Thiết kế bộ Test sử dụng PostgreSQL thật qua `pgxpool` với Isolation Transaction an toàn.

1. `payment_transactions`: Insert thành công với đầy đủ tham chiếu đến một Parent Order hợp lệ.
2. `payment_transactions`: Bị từ chối nếu `parent_order_type` truyền vào là `'seller'` (vi phạm CHECK hoặc FK).
3. `payment_transactions`: Bị từ chối nếu `currency_code` không khớp với `orders.currency` của Parent Order.
4. `payment_transactions`: Từ chối khi `amount` <= 0.
5. `payment_transactions`: Cho phép insert Attempt 2 nếu Attempt 1 đã `failed` hoặc `cancelled`.
6. `payment_transactions`: Trả về lỗi vi phạm Unique Constraint (`uq_payment_parent_live_attempt`) nếu Attempt 1 đang `pending` và cố tình tạo Attempt 2.
7. `payment_transactions`: Trả về lỗi vi phạm Unique Constraint nếu Attempt 1 đã `succeeded` và cố tình tạo Attempt 2.
8. `payment_transactions`: Validation lỗi trạng thái `pending` nếu `processing_at` hoặc `succeeded_at` IS NOT NULL.
9. `payment_transactions`: Validation đúng chuyển trạng thái `succeeded` bắt buộc `processing_at` VÀ `succeeded_at` IS NOT NULL.
10. `payment_transactions`: Lỗi nếu `idempotency_key` bị trùng lặp.
11. `payment_transactions`: Lỗi nếu `(provider, provider_payment_id)` trùng lặp (Partial Index test).
12. `payment_transactions`: Lỗi nếu `expires_at` nhỏ hơn `created_at` hoặc `updated_at` < `created_at`.
13. `payment_transactions`: Lỗi nếu tạo thành công nhưng thiếu Supporting Keys (id, provider) làm lỗi truy xuất sau này.
14. `payment_transactions`: FK Constraint RESTRICT ngăn không cho xoá `orders` khi đang có transaction.
15. `payment_webhook_events`: Insert thành công Inbox event với `payment_transaction_id`.
16. `payment_webhook_events`: Bị từ chối (FK error) nếu webhook event mang `provider='STRIPE'` liên kết với Transaction có `provider='PAYPAL'`.
17. `payment_webhook_events`: Deduplication thành công, trả về lỗi constraint cho duplicate `(provider, provider_event_id)`.
18. `payment_webhook_events`: Validation lỗi `processing_status` nếu truyền trạng thái lạ.
19. `payment_refunds`: Insert thành công gắn với một Transaction.
20. `payment_refunds`: Bị từ chối nếu sai lệch `provider` so với `PaymentTransaction`.
21. `payment_refunds`: Bị từ chối nếu sai lệch `currency_code` so với `PaymentTransaction`.
22. `payment_refunds`: Validation số tiền refund phải > 0.
23. `payment_refunds`: State-Timestamp matrix validation giống với transactions.
24. `payment_refunds`: Deduplication thông qua Unique `idempotency_key` hoạt động.
25. `payment_refunds`: Lỗi nếu `(provider, provider_refund_id)` trùng lặp (Partial Index test).
26. **Out-of-order Webhook Latch**: Guarded repository SQL phải trả về `RowsAffected = 0` khi webhook `processing` đến sau Payment `succeeded`; Payment giữ nguyên terminal state và webhook được ghi `ignored`.
27. **Refund Allocation Release**: Chỉ transition đầu tiên từ `pending/processing` sang `failed/cancelled` mới được decrement `allocated_refund_amount`; delivery lặp lại là no-op và không giải phóng capacity lần hai.
28. **Concurrency Test**: 100 workers cùng chạy một atomic statement gồm conditional allocation và insert Refund. Chỉ một worker thành công, đồng thời counter, số Refund rows và tổng active refund amount luôn khớp nhau.

---

## 5. Deployment / Migration Strategy

- **Thứ tự thực thi**: Migration số `000008` (sau Voucher).
- **UP**: Tạo bảng theo thứ tự: `payment_transactions` -> `payment_webhook_events` -> `payment_refunds`. Tạo các Partial Unique Indexes, Composite Constraints tương ứng.
- **DOWN**: Drop Tables chính xác theo thứ tự ngược lại `payment_refunds` -> `payment_webhook_events` -> `payment_transactions`; rollback fail-fast nếu xuất hiện dependency ngoài dự kiến.

## 6. Database and Repository Boundary

- PostgreSQL DDL enforce row shape, FK/UNIQUE identities, live-attempt uniqueness, monetary ceilings và timestamp compatibility.
- Historical state transitions không được encode bằng trigger. Payment repository phải dùng guarded atomic SQL (`WHERE status = <expected-current-state>`) và kiểm tra `RowsAffected`.
- Refund allocation counter và Refund row được ghi trong cùng transaction/statement; lỗi insert rollback cả allocation.
- Refund release dùng guarded transition. Chỉ khi transition active -> failed/cancelled affect một row thì cùng transaction mới decrement counter.
- So khớp `payment.amount` với Parent Order total, đồng bộ payment expiry với Inventory/Voucher hold, provider calls, signature verification và event/outbox dispatch thuộc application/repository tickets sau.

## Tests

- [x] Parent-only Payment FK và currency consistency được enforce
- [x] Tối đa một live Payment attempt trên mỗi Parent Order
- [x] Payment/provider idempotency và provider-scoped identity được enforce
- [x] Payment và Refund state-timestamp matrices được enforce
- [x] Webhook provider identity, deduplication và processing status được enforce
- [x] Guarded out-of-order transition không làm terminal Payment hồi quy
- [x] Refund provider/currency composite FK được enforce
- [x] Partial Refund rows và `allocated_refund_amount` luôn khớp
- [x] Duplicate refund insert rollback allocation, không double-allocate
- [x] Failed/cancelled Refund release capacity đúng một lần
- [x] Refund insert failure rollback cả row và allocation
- [x] 100 concurrent full-refund attempts tạo đúng một Refund row và một allocation
- [x] Migration UP/DOWN/UP hoạt động và `schema_migrations` không dirty
- [x] `go test ./...`, `go vet ./...` và race test pass

## Commands

```bash
make verify-fast
make migrate-integration-cycle
make test-integration-target DB_TEST_NAME='^TestPaymentMigration$'
go test -race ./internal/database -run '^TestPaymentMigration$' -count=1
make db-migration-status
make verify-full
```

## Decisions

- UUIDv7 generated by Go; PostgreSQL không tự tạo UUID.
- Tiền được lưu bằng `BIGINT` minor units; không dùng floating point.
- Không dùng PostgreSQL ENUM; lifecycle dùng `VARCHAR` + `CHECK`.
- V1 dùng simple capture và tối đa một economically live Payment attempt.
- Provider payment, webhook và refund identities đều scoped theo provider.
- Không tạo `payment_status_histories` trong V1.
- Terminal transition và refund lifecycle dùng guarded repository SQL thay vì database trigger.
- Không thêm RabbitMQ, Elasticsearch hay outbox schema trong ticket này.

## Result

PASS

## Next

ECOM-DB-004I — Cross-Domain Constraint Tests
