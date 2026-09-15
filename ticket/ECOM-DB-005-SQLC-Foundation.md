# ECOM-DB-005 — SQLC Foundation

Status: DONE

## Source Design

- `docs/NEXUS-COMMERCE_V1_CANONICAL_ROADMAP.md`
- `nexus_commerce_golang_requirements.txt`
- ECOM-DB-004A → ECOM-DB-004J

## Goal

Thiết lập nền tảng sqlc type-safe trên final PostgreSQL schema version `8` trước
khi bắt đầu ECOM-AUTH-001. Foundation phải dùng `pgx/v5`, hỗ trợ cả `pgxpool` và
`pgx.Tx`, truyền `context.Context`, phát hiện generated-code drift và không viết
trước toàn bộ business queries của các module.

## Important Invariants

- `golang-migrate` tiếp tục là source of truth cho schema
- sqlc đọc trực tiếp thư mục migration và bỏ qua DOWN migrations theo golang-migrate convention
- migration filenames zero-padded giữ đúng lexicographic/version order
- generated code dùng `github.com/jackc/pgx/v5`, không dùng `database/sql`
- caller sở hữu transaction và truyền nó qua generated `Queries.WithTx`
- mọi generated query nhận `context.Context`
- generated persistence models không có JSON tags và không được dùng trực tiếp làm API DTO
- codegen phải deterministic; schema/query đổi mà chưa regenerate sẽ làm `sqlc diff` fail
- DB-005 chỉ có query kỹ thuật để chứng minh wiring; query nghiệp vụ được thêm cùng ticket sở hữu nó

## Implementation

Files:

- `sqlc.yaml` cấu hình PostgreSQL schema, query directory và Go generator `pgx/v5`
- `sql/queries/database.sql` chứa `DatabasePing`, query kỹ thuật duy nhất của foundation
- `internal/database/sqlc/` chứa code do sqlc sinh, gồm models, `DBTX`, `Queries` và `WithTx`
- `internal/database/sqlc_integration_test.go` kiểm thử generated query bằng pool, transaction và cancelled context
- `Makefile` pin sqlc, cung cấp install/version/generate/check targets và đưa drift check vào `verify-fast`
- `ticket/ECOM-DB-005-SQLC-Foundation.md` ghi contract, acceptance evidence và kết quả ticket
- `docs/NEXUS-COMMERCE_V1_CANONICAL_ROADMAP.md` chuyển current ticket đến ECOM-AUTH-001 sau khi gate pass

Không có migration `000009` vì ticket không thay đổi database schema.

## Generated Package Contract

```text
pgxpool.Pool ──► dbsqlc.New(pool)
                       │
                       ├──► DatabasePing(ctx)
                       │
pgx.Tx ───────► Queries.WithTx(tx)
                       │
                       └──► generated query trong cùng caller-owned transaction
```

Generated models là persistence representation. Handler/service không được dùng
chúng như public request/response contract và không được đặt business behavior vào
generated package.

## Acceptance Criteria

- [x] sqlc version được pin trong Makefile
- [x] Config version 2 đọc migration schema và query directory
- [x] Go generator dùng `pgx/v5`
- [x] Generated package nằm trong `internal/database/sqlc`
- [x] `DBTX`, `New` và `WithTx` được sinh tự động
- [x] Foundation chỉ tạo một technical smoke query
- [x] Query nhận `context.Context` và trả `int64`
- [x] Integration test chạy generated query bằng `pgxpool`
- [x] Integration test chạy generated query bằng `pgx.Tx`
- [x] Cancelled context được propagate xuống pgx
- [x] `sqlc compile`, `sqlc vet` và `sqlc diff` pass
- [x] Generated code compile và không drift
- [x] `make verify-fast` pass
- [x] `make verify-full` pass
- [x] `schema_migrations` vẫn ở version `8`, clean

## Commands

```bash
make tools
make sqlc-version
make sqlc-generate
make sqlc-check
make test-target TEST_PACKAGE=./internal/database TEST_NAME='^TestSQLC'
make test-integration-target DB_TEST_NAME='^TestSQLC'
make verify-fast
make verify-full
make db-migration-status
git diff --check
```

## Verification

- `make sqlc-version`: PASS — `v1.31.1`
- `make sqlc-generate`: PASS
- `make sqlc-check`: PASS — compile, vet và diff
- Focused Go test: PASS
- Focused PostgreSQL integration test: PASS
- Focused race test: PASS
- `make verify-fast`: PASS
- `make verify-full`: PASS
- Full migration cycle: PASS — `008 → 0 → 008`
- `schema_migrations`: version `8`, clean
- `git diff --check`: PASS

## Decisions

- Pin `sqlc v1.31.1`; không dùng floating `latest` trong project tooling.
- Đặt `sqlc.yaml` ở repository root để CLI và Makefile chạy từ một canonical working directory.
- Dùng package name `dbsqlc` trong `internal/database/sqlc` để tránh nhầm generated persistence code với database lifecycle package.
- Không bật `emit_interface`; repository interface chỉ được tạo khi consumer cần, tránh abstraction suy đoán.
- Không bật JSON tags vì database model không phải transport DTO.
- Không cấu hình database-backed/cloud analysis; foundation codegen hoạt động offline từ migration files.
- GORM không được cài hoặc dùng trong DB-005; requirements vẫn cho phép exception có kiểm soát ở flow phụ tương lai.
- Không chỉnh tay generated files để chèn comment; file header do sqlc sinh là ownership note, còn mô tả `DatabasePing` được lấy từ query source. `New` và `WithTx` giữ nguyên standard generated API để `sqlc diff` luôn đáng tin cậy.

## Result

PASS

## Next

ECOM-AUTH-001 — Registration
