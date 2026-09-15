# ECOM-DB-004I — Cross-Domain Constraint Tests

Status: DONE

## Source Design

- `docs/NEXUS-COMMERCE_V1_CANONICAL_ROADMAP.md` — mục Cross-Domain Invariants
- `docs/erd.md` — mục Cross-Domain Constraint Classification và Integration Tests Required Across Domains
- DATA-003A → DATA-003G — canonical data design
- ECOM-DB-004B → ECOM-DB-004H — schema đã triển khai

## Goal

Kiểm chứng trên PostgreSQL thật rằng schema Identity, Seller/Catalog, Inventory, Cart,
Order, Voucher và Payment ghép lại thành một commerce graph nhất quán. Ticket này là
test-only: không thêm bảng hoặc migration mới nếu các schema `000001` → `000008` đã
enforce đúng contract.

## Important Invariants

- Tenant spine giữ cùng `shop_id` từ SKU đến InventoryStock, CartItem, SellerOrder và OrderItem
- `checkout_reference_id` nối InventoryReservation, VoucherUsage và ParentOrder
- SellerOrder giữ cùng User và Currency với ParentOrder
- Payment chỉ được tham chiếu ParentOrder và phải cùng Currency
- VoucherUsage committed chỉ được tham chiếu ParentOrder cùng User, Checkout và Currency
- Một retry checkout không tạo thêm Inventory allocation, Voucher allocation hoặc ParentOrder
- Cart có thể chứa nhiều currency; một checkout selection V1 chỉ được có đúng một currency
- Deadline được kiểm tra tại thời điểm commit, không phụ thuộc lúc TTL worker chạy
- Một precondition finalization thất bại phải rollback mọi commerce effect đã thực hiện trước đó
- Duplicate payment webhook chỉ tạo một logical side effect
- Order snapshot không thay đổi khi dữ liệu nguồn User, Shop hoặc Catalog thay đổi

## Enforcement Boundary

### PostgreSQL enforce trực tiếp

- composite FK tenant và Catalog trace chain
- Parent/Seller User, Shop và Currency consistency
- Parent-only Voucher và Payment references
- checkout, command và provider uniqueness safety nets
- one live Payment attempt, Voucher quota ceiling và refund allocation ceiling
- row shape, status/timestamp matrix, money và quantity constraints

### Repository/service transaction enforce

- stable idempotency key được resolve về resource đã có
- mixed-currency checkout preflight
- guarded lifecycle transition kiểm tra `RowsAffected`
- Inventory/Voucher commit trước deadline
- toàn bộ local finalization chạy trong một PostgreSQL transaction

### Orchestration enforce

- thứ tự reserve Inventory, reserve Voucher, tạo Order và khởi tạo Payment
- compensation theo thứ tự ngược khi một bước thất bại
- owner module tự cập nhật state của domain mình; Payment repository không trực tiếp sở hữu Order state

## Implementation

Không có migration `000009`: schema version của ticket vẫn là `8`.

Files:

- `internal/database/cross_domain_migration_test.go` kiểm thử các contract xuyên domain mới trên PostgreSQL thật
- `ticket/ECOM-DB-004I-Cross-Domain-Constraint-Tests.md` ghi scope, enforcement boundary, acceptance mapping và bằng chứng verification của ticket
- `docs/NEXUS-COMMERCE_V1_CANONICAL_ROADMAP.md` chỉ được chuyển sang DB-004J sau khi mọi verification của DB-004I pass

Test harness tái sử dụng fixture/helper của từng domain thay vì tạo một schema hoặc
fixture framework song song. Mỗi test dùng transaction riêng và rollback khi kết thúc.

## Acceptance Criteria

- [x] Valid Catalog → Inventory → Cart → Order tenant graph được chấp nhận
- [x] Cross-Shop InventoryStock bị composite FK từ chối
- [x] Cross-Shop CartItem bị composite FK từ chối
- [x] Cross-Shop OrderItem/Catalog trace chain bị composite FK từ chối
- [x] Inventory, Voucher, ParentOrder và Payment cùng checkout correlation được chứng minh
- [x] Retry cùng Inventory idempotency key chỉ giữ một reservation
- [x] Retry cùng Voucher/checkout trả usage cũ và quota chỉ tăng một lần
- [x] Retry cùng checkout chỉ giữ một ParentOrder
- [x] Payment trỏ SellerOrder bị từ chối
- [x] Payment sai Parent currency bị từ chối
- [x] VoucherUsage trỏ SellerOrder bị từ chối
- [x] VoucherUsage sai User hoặc Checkout correlation bị từ chối
- [x] Mixed-currency Cart selection bị checkout preflight từ chối
- [x] Single-currency selection được checkout preflight chấp nhận
- [x] Voucher hold hết hạn làm finalization rollback cả Inventory commit đã chạy trước
- [x] Duplicate webhook tạo một inbox row và một lần Payment/Order transition
- [x] Concurrent ParentOrder creation cho cùng checkout tạo tối đa một ParentOrder
- [x] Inventory hold hết hạn không thể commit
- [x] Voucher expiry-vs-commit dùng CAS và chỉ có một terminal result
- [x] Concurrent Payment attempts có tối đa một live attempt
- [x] Concurrent refunds không over-refund và row/counter luôn đồng bộ
- [x] Historical Order snapshot không đổi sau khi User/Shop/SKU thay đổi
- [x] Toàn bộ Go và PostgreSQL integration verification pass
- [x] Migration UP/DOWN/UP pass và `schema_migrations` ở version `8`, clean

## Acceptance Mapping

| Contract | Executable evidence |
|---|---|
| Tenant graph và cross-Shop rejection | `TestCrossDomainTenantConsistency` |
| Checkout correlation và retry | `TestCrossDomainCheckoutCorrelationAndRetrySafety` |
| Parent-safe Payment/Voucher refs | `TestCrossDomainParentSafeReferences` |
| Mixed-currency checkout rule | `TestCrossDomainMixedCurrencyCheckoutPreflight` |
| Deadline + all-or-nothing finalization | `TestCrossDomainExpiredHoldRollsBackFinalization` |
| Duplicate webhook logical effect | `TestCrossDomainDuplicateWebhookHasOneLogicalEffect` |
| Concurrent checkout identity | `TestOrderMigrationConcurrentCheckoutCreatesOneParent` |
| Inventory deadline/concurrency | `TestInventoryMigrationRejectsCommitAfterDeadline`, `TestInventoryMigrationAtomicReservationConcurrency` |
| Voucher retry/deadline/concurrency | `TestVoucherMigrationEnforcesIdempotentLifecycle`, `TestVoucherMigrationEnforcesExpireVsCommitCASRace`, `TestVoucherMigrationEnforcesGlobalQuotaConcurrency` |
| Payment live attempt/refund concurrency | `TestPaymentMigration` Group 2, Group 5 và Group 6 |
| Historical snapshots | `TestOrderMigrationPreservesHistoricalSnapshots` |

## Commands

```bash
make agent-preflight
make test-target TEST_PACKAGE=./internal/database TEST_NAME='^TestCrossDomain'
make test-integration-target DB_TEST_NAME='^TestCrossDomain'
go test -race ./internal/database -run '^TestCrossDomain' -count=1
make migrate-integration-cycle
make verify-fast
make verify-full
make db-migration-status
git diff --check
```

## Decisions

- Không tạo migration chỉ để đánh số ticket; DB-004I là integration/constraint test gate.
- Không thêm trigger xuyên domain; DDL chỉ enforce dữ liệu có thể chứng minh từ một row/FK/unique constraint.
- Mixed-currency rejection là preflight query của Checkout service vì Cart V1 chủ đích là global multi-Shop.
- Stable idempotency key là contract của caller/repository; database unique key là safety net cuối.
- Duplicate webhook test dùng một transaction để mô phỏng orchestrator gọi Payment và Order owner operations; đây không phải quyền cho Payment repository tự sửa Order.
- Existing domain concurrency tests là một phần acceptance evidence và không bị copy sang file mới.
- PostgreSQL vẫn là source of truth; truy cập dữ liệu tiếp tục ưu tiên pgx + sqlc, không dùng GORM.

## Result

PASS

## Next

ECOM-DB-004J — Full Migration Review
