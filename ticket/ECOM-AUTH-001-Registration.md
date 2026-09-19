# ECOM-AUTH-001 — Registration

Status: DONE

## Source Design

- `docs/NEXUS-COMMERCE_V1_CANONICAL_ROADMAP.md`
- `docs/data-003a-identity.md`
- `migrations/000001_identity.up.sql`
- `nexus_commerce_golang_requirements.txt`
- ECOM-DB-005 — SQLC Foundation

## Goal

Triển khai registration application use case và PostgreSQL persistence để tạo
User cùng email/password Credential atomically, an toàn khi duplicate request
chạy đồng thời và không truyền plaintext password xuống repository.

AUTH-001 định nghĩa `PasswordHasher` port nhưng chưa chọn hoặc wire concrete
password hashing algorithm. Việc đó thuộc ECOM-AUTH-002. Vì vậy production HTTP
registration route chỉ được mở khi AUTH-002 cung cấp hasher an toàn; AUTH-001
không lưu plaintext và không dùng placeholder hash.

## Important Invariants

- User module sở hữu `users`; Auth module sở hữu `credentials`
- Registration phối hợp hai owner qua boundary, không để Auth tự ghi bảng `users`
- User và Credential commit trong cùng một caller-owned PostgreSQL transaction
- Credential lỗi phải rollback User, không tạo orphan account
- Email được trim, lowercase và validate trước khi persistence
- Database `UNIQUE(email)` là lớp quyết định cuối khi request race
- Duplicate email được map bằng SQLSTATE/constraint name sang typed domain error
- UUIDv7 được tạo trong Go application layer
- Plaintext password chỉ tới `PasswordHasher`; repository chỉ nhận encoded hash
- Result và error không chứa password hoặc password hash
- Request context được truyền service → repository → pgx/sqlc

## Implementation

Files:

- `internal/auth/registration.go` định nghĩa input/result, validation, canonicalization, UUIDv7, hasher port và registration service
- `internal/auth/postgres_registration_repository.go` sở hữu transaction, ghi Credential và map duplicate-email conflict
- `internal/user/postgres_registration_writer.go` thực hiện User-owned write trong transaction do registration truyền vào
- `sql/queries/auth_registration.sql` chứa Auth-owned Credential insert
- `sql/queries/user_registration.sql` chứa User-owned profile insert
- `internal/database/sqlc/auth_registration.sql.go` là generated Auth query code
- `internal/database/sqlc/user_registration.sql.go` là generated User query code
- `internal/auth/registration_test.go` kiểm thử service, validation, hash boundary, cancellation và UUIDv7
- `internal/auth/postgres_registration_repository_test.go` kiểm thử PostgreSQL transaction, rollback, conflict và concurrency
- `Makefile` thêm focused Auth integration target và đưa Auth tests vào full PostgreSQL gate
- `ticket/ECOM-AUTH-001-Registration.md` ghi contract, scope, evidence và kết quả ticket
- `docs/NEXUS-COMMERCE_V1_CANONICAL_ROADMAP.md` chuyển current ticket sang AUTH-002 sau khi gate pass

Không có migration `000009` vì Identity schema hiện tại đã đủ cho registration.

## Registration Flow

```text
RegisterInput
    ↓ validate + canonicalize
PasswordHasher.Hash(plaintext)
    ↓ encoded hash only
BEGIN
    User writer  → INSERT users
    Auth writer  → INSERT credentials
COMMIT
```

Nếu Credential insert hoặc commit thất bại, transaction rollback cả hai write.
Không dùng flow `SELECT email exists` trước insert; unique constraint xử lý race.

## Acceptance Criteria

- [x] Blank/oversized display name bị reject trước hashing
- [x] Invalid/oversized email bị reject trước hashing
- [x] Empty/oversized password input bị reject trước hashing
- [x] Display name được trim; email được trim và lowercase
- [x] Plaintext password không đi xuống repository
- [x] Empty encoded hash bị reject
- [x] UUIDv7 được tạo ở Go application layer
- [x] User write đi qua User-owned boundary
- [x] Credential write thuộc Auth persistence adapter
- [x] User và Credential commit atomically
- [x] Credential failure rollback User
- [x] Duplicate email trả `ErrEmailAlreadyRegistered`
- [x] Concurrent duplicate registration chỉ commit một account
- [x] Cancelled context dừng trước hashing/persistence khi đã bị cancel
- [x] Business queries dùng sqlc/pgx v5
- [x] `sqlc compile`, `sqlc vet` và `sqlc diff` pass
- [x] Không thêm hoặc sửa migration
- [x] Migration database vẫn version `8`, clean

## Commands

```bash
make sqlc-generate
make sqlc-check
make test-target TEST_PACKAGE='./internal/auth ./internal/user' TEST_NAME='^TestRegistrationService|^TestNewUUIDv7'
make test-auth-integration TEST_DATABASE_URL='...'
REQUIRE_DATABASE_INTEGRATION=1 TEST_DATABASE_URL='...' go test -race -timeout 2m ./internal/auth -count=1 -run '^TestPostgresRegistrationRepository'
make verify-fast
make verify-full TEST_DATABASE_URL='...'
make db-migration-status TEST_DATABASE_URL='...'
```

## Verification

- Focused registration unit tests: PASS
- Auth PostgreSQL integration tests: PASS
- Auth PostgreSQL race tests: PASS
- `make sqlc-check`: PASS
- `make verify-fast`: PASS
- `make verify-full`: PASS
- Full migration cycle: PASS — `008 → 0 → 008`
- `schema_migrations`: version `8`, clean
- `git diff --check`: PASS

## Decisions

- AUTH-001 chỉ định nghĩa hasher port; không chọn algorithm sớm hơn AUTH-002 và không dùng hash giả trong production.
- Chưa đăng ký HTTP route production vì chưa có concrete password hasher; route/wiring phải dùng implementation an toàn từ AUTH-002.
- Registration orchestration được đặt trong Auth application layer nhưng User-owned insert đi qua `RegistrationUserWriter` của User module.
- Transaction do PostgreSQL registration repository sở hữu và truyền cùng `pgx.Tx` qua owner boundary để không có partial registration.
- Duplicate email dựa vào `uq_credentials_email`, không pre-check dễ race.
- Password policy chi tiết và algorithm parameters thuộc AUTH-002; AUTH-001 chỉ áp dụng input bounds tối thiểu để tránh empty/oversized work.
- Không tạo interface cho generated queries; chỉ tạo hai consumer-owned port có mục đích rõ ràng: password hashing và User write boundary.

## Result

PASS

## Next

ECOM-AUTH-002 — Password Hashing
