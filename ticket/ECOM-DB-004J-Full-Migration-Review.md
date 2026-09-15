# ECOM-DB-004J — Full Migration Review

Status: DONE

## Source Design

- `docs/NEXUS-COMMERCE_V1_CANONICAL_ROADMAP.md`
- `docs/erd.md`
- DATA-003A → DATA-003G
- ECOM-DB-004A → ECOM-DB-004I

## Goal

Thực hiện final gate cho toàn bộ database phase DB-004 trước khi bắt đầu
ECOM-DB-005 SQLC Foundation. Review phải chứng minh cả chuỗi migration từ Identity
đến Payment có thể bootstrap, rollback toàn bộ, apply lại và tạo đúng final schema
trên PostgreSQL thật.

Ticket này là review/test/tooling gate. Không tạo migration `000009` nếu không phát
hiện schema defect cần sửa bằng migration mới.

## Review Scope

- migration manifest liên tục từ `000001` đến `000008`
- mỗi version có đúng một file UP và một file DOWN cùng tên
- mỗi migration được bọc trong một transaction
- DOWN migration fail-fast, không dùng `IF EXISTS` hoặc `CASCADE`
- dependency order: Identity → Seller → Catalog → Inventory → Cart → Order → Voucher → Payment
- final V1 table ownership từ Identity đến Payment
- UUID do Go tạo, `TIMESTAMPTZ`, `BIGINT` minor units và `CHAR(3)` currency
- lifecycle dùng `VARCHAR` + `CHECK`, không dùng PostgreSQL ENUM
- mọi foreign key dùng `ON DELETE RESTRICT`
- tenant, checkout, Parent/Seller, Voucher và Payment critical keys tồn tại
- migration full cycle và toàn bộ PostgreSQL integration/concurrency tests pass
- `schema_migrations` ở latest version và không dirty

## Implementation

Files:

- `internal/database/full_migration_review_test.go` tạo regression gate cho migration manifest, final table set, global type/delete policies và critical cross-domain keys
- `Makefile` thêm `migrate-integration-full-cycle`; `verify-full` dùng full rollback/apply thay vì chỉ rollback migration cuối
- `ticket/ECOM-DB-004J-Full-Migration-Review.md` ghi scope, evidence, quyết định và kết quả final review
- `docs/NEXUS-COMMERCE_V1_CANONICAL_ROADMAP.md` đồng bộ trạng thái DB-004 và chuyển current action sang ECOM-DB-005 sau khi gate pass
- `.agents/skills/nexus-ecommerce/SKILL.md` và `references/database.md` cập nhật implemented-state từ Order lên Payment cùng các final database gates

Không sửa các migration `000001` → `000008` đã được áp dụng. Nếu review phát hiện
lỗi schema, correction phải được tạo bằng migration mới; không rewrite shared history.

## Acceptance Criteria

- [x] Migration filenames và version sequence được audit tự động
- [x] Mỗi migration có đủ UP/DOWN cùng domain name
- [x] Mọi migration có file-purpose note và đúng một `BEGIN`/`COMMIT`
- [x] DOWN migrations không dùng `IF EXISTS` hoặc `CASCADE`
- [x] Final schema có đúng 27 domain tables, không có bảng ngoài scope V1
- [x] Application entity IDs dùng UUID và không có database-generated default
- [x] Absolute timestamps dùng `TIMESTAMPTZ`
- [x] Currency columns dùng `CHAR(3)`
- [x] Money columns dùng `BIGINT`; không có FLOAT/REAL/DOUBLE/NUMERIC
- [x] Không có PostgreSQL ENUM
- [x] Mọi public foreign key dùng `ON DELETE RESTRICT`
- [x] Critical tenant, checkout, Voucher và Payment key/index tồn tại
- [x] Full migration `008 → 0 → 008` pass
- [x] Toàn bộ PostgreSQL integration tests pass trên fresh final schema
- [x] Database race/concurrency tests pass
- [x] `schema_migrations` latest và clean
- [x] `make verify-full` pass

## Commands

```bash
make agent-preflight
make test-target TEST_PACKAGE=./internal/database TEST_NAME='^TestFullMigrationReview'
make test-integration-target DB_TEST_NAME='^TestFullMigrationReview'
make migrate-integration-full-cycle
make test-integration
go test -race ./internal/database -count=1
make db-migration-status
make verify-full
git diff --check
```

## Verification

- `make test-target ... '^TestFullMigrationReview'`: PASS
- `make test-integration-target ... '^TestFullMigrationReview'`: PASS
- `make migrate-integration-full-cycle`: PASS (`008 → 0 → 008`)
- `go test -race ./internal/database -count=1`: PASS
- `make verify-fast`: PASS
- `make verify-full`: PASS
- `make db-migration-status`: PASS — version `8`, clean
- `git diff --check`: PASS

## Decisions

- DB-004J không tạo một empty migration chỉ để tăng version.
- Full-cycle chỉ được chạy với `TEST_DATABASE_URL`; Makefile từ chối database không có `test` trong tên và từ chối URL trùng `DATABASE_URL`.
- `migrate-integration-cycle` vẫn giữ cho focused review của migration cuối; `verify-full` dùng `migrate-integration-full-cycle` cho release gate.
- Final schema contract được kiểm tra bằng PostgreSQL catalogs thay vì copy toàn bộ DDL vào test.
- Existing domain và DB-004I tests tiếp tục là executable evidence cho constraint, transaction, idempotency và concurrency behavior.
- DB-004 dùng PostgreSQL + pgx và chuẩn bị cho sqlc; Go module hiện tại không cài hoặc dùng GORM. Nếu GORM được dùng cho flow phụ ở ticket tương lai thì phải tuân theo exception boundary trong requirements.

## Result

PASS

## Next

ECOM-DB-005 — SQLC Foundation
