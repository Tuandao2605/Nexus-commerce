# ECOM-AUTH-003 — Login

Status: DONE

## Source Design

- ECOM-AUTH-001 — Registration
- ECOM-AUTH-002 — Password Hashing
- `docs/data-003a-identity.md`
- `migrations/000001_identity.up.sql`
- `docs/NEXUS-COMMERCE_V1_CANONICAL_ROADMAP.md`
- [OWASP Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)
- [OWASP Password Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html)

## Goal

Triển khai email/password authentication core để xác minh Credential, kiểm tra
User còn active và trả identity nội bộ cho AUTH-004 phát access token. Login phải
chống account enumeration bằng generic error và dummy Argon2id work, đồng thời
nâng legacy password hash sau khi xác thực thành công mà không ghi đè một password
change chạy đồng thời.

AUTH-003 không phát token/session và chưa mở public HTTP login endpoint. AUTH-004
sẽ compose `LoginService` với access-token issuer rồi mới định nghĩa response API
hoàn chỉnh; tránh tạo một endpoint xác minh password nhưng không cấp authenticated
artifact.

## Important Invariants

- Auth sở hữu `credentials`; User sở hữu account lifecycle trong `users`
- Email được trim/lowercase trước lookup và query luôn parameterized qua sqlc
- Unknown email, wrong password, malformed stored hash, missing User, suspended User và deleted User cùng trả `ErrInvalidCredentials`
- Unknown/invalid email chạy verify với dummy hash dùng current Argon2id work factor
- Database/infrastructure error không bị che thành invalid credential
- Chỉ User có status `active` mới xác thực thành công
- Auth đọc User status qua User-owned interface, không tự đặt User query trong Auth repository
- Rehash chỉ chạy sau password verify thành công
- Rehash dùng `WHERE user_id = ? AND password_hash = old_hash` như compare-and-swap
- Concurrent password change thắng; stale login không được ghi đè hash mới
- Work-factor upgrade giữ nguyên `password_changed_at` vì plaintext password không đổi
- Rehash maintenance là best-effort và không làm hỏng login đã xác thực thành công
- Password, encoded hash và token không xuất hiện trong result hoặc log
- Request context truyền xuyên service, repository và PostgreSQL

## Authentication Flow

```text
LoginInput
    ↓ canonicalize email
Credential lookup ── not found ──► verify(password, dummy hash) ──► generic error
    ↓ found
verify(password, stored hash) ── failure ──► generic error
    ↓ success
User-owned status lookup ── not active ──► generic error
    ↓ active
NeedsRehash?
    ├── no  ──► AuthenticatedIdentity
    └── yes ──► hash again ──► compare-and-swap update ──► AuthenticatedIdentity
```

## Implementation

Files:

- `internal/auth/login.go` định nghĩa login ports, application service, generic error, dummy-hash work và opportunistic rehash
- `internal/auth/postgres_login_repository.go` lookup Credential và compare-and-swap hash bằng pgx/sqlc
- `internal/user/postgres_login_reader.go` cung cấp User-owned account-status reader cho Auth
- `sql/queries/auth_login.sql` chứa Auth-owned Credential lookup/update parameterized queries
- `sql/queries/user_login.sql` chứa User-owned status lookup query
- `internal/database/sqlc/auth_login.sql.go` là generated Auth login query code
- `internal/database/sqlc/user_login.sql.go` là generated User login query code
- `internal/auth/login_test.go` kiểm thử service failures, dummy work, lifecycle boundary, cancellation và best-effort rehash
- `internal/auth/postgres_login_repository_test.go` chứng minh authentication, inactive rejection, real legacy rehash và compare-and-swap với PostgreSQL thật
- `Makefile` mở rộng focused Auth integration gate để luôn chạy login tests
- `.agents/skills/nexus-ecommerce/` đồng bộ implemented-state và Auth/User boundary cho ticket sau
- `ticket/ECOM-AUTH-003-Login.md` ghi scope, quyết định và evidence của ticket
- `docs/NEXUS-COMMERCE_V1_CANONICAL_ROADMAP.md` chuyển current ticket sang AUTH-004 sau khi gate pass

Không có migration `000009`; schema `users` và `credentials` hiện tại đã đủ cho
password authentication và safe hash upgrade.

## Acceptance Criteria

- [x] Valid active account trả đúng internal identity
- [x] Email login được trim và lowercase trước query
- [x] Wrong password trả `ErrInvalidCredentials`
- [x] Unknown email trả cùng `ErrInvalidCredentials`
- [x] Malformed stored hash không bị lộ ra client
- [x] Suspended/deleted/missing User trả cùng generic error
- [x] Unknown/invalid email đi qua dummy Argon2id verify path
- [x] Operational persistence error vẫn phân biệt được ở internal boundary
- [x] User status được đọc qua User-owned interface
- [x] Legacy valid hash được nâng lên current Argon2id policy sau successful login
- [x] Rehash dùng compare-and-swap và không ghi đè concurrent password change
- [x] Rehash không thay đổi `password_changed_at`
- [x] Rehash failure không làm login hợp lệ thất bại
- [x] Cancelled context dừng trước login work khi request đã bị hủy
- [x] SQL business queries dùng sqlc/pgx v5
- [x] Không thêm/sửa migration
- [x] Migration database vẫn version `8`, clean
- [x] Public login endpoint và token issuance không bị làm sớm hơn AUTH-004

## Commands

```bash
make sqlc-generate
make sqlc-check
make test-target TEST_PACKAGE='./internal/auth ./internal/user' TEST_NAME='^TestLoginService|^TestNewLoginService'
make test-auth-integration TEST_DATABASE_URL='...'
REQUIRE_DATABASE_INTEGRATION=1 TEST_DATABASE_URL='...' go test -race -timeout 2m ./internal/auth -count=1
make verify-fast
make verify-full TEST_DATABASE_URL='...'
make db-migration-status TEST_DATABASE_URL='...'
git diff --check
```

## Verification

- Focused login unit tests: PASS
- Auth PostgreSQL integration tests: PASS
- Full Auth race test: PASS
- `make sqlc-check`: PASS
- `make verify-fast`: PASS
- `make verify-full`: PASS
- Full migration cycle: PASS — `008 → 0 → 008`
- `schema_migrations`: version `8`, clean
- Function/file comment audit: PASS
- `git diff --check`: PASS

## Decisions

- Dùng generic `invalid email or password` cho mọi authentication failure có thể tiết lộ account hoặc lifecycle state.
- Sinh dummy hash một lần khi tạo service, bằng chính current Argon2id policy; không hash dummy mới trên từng failed login.
- Verify password trước khi hỏi User status để không trả lifecycle state cho người chưa chứng minh biết password.
- Giữ User status query trong User module rồi inject interface vào Auth, thay vì cho Auth repository đọc bảng owner khác trực tiếp.
- Rehash ngay sau successful authentication là cách nâng work factor dần cho active accounts; account chưa login vẫn giữ old hash tới lần login sau.
- Rehash là compare-and-swap theo old hash để password reset/change đồng thời luôn thắng stale login.
- Không đổi `password_changed_at` khi chỉ đổi representation/work factor của cùng password.
- Không mở `POST /auth/login` trong ticket này vì chưa có access token; AUTH-004 sẽ định nghĩa atomic application response và public route.
- Brute-force/distributed login rate limiting thuộc security ticket riêng; public route không được coi là production-ready trước lớp bảo vệ đó.

## Result

PASS

## Next

ECOM-AUTH-004 — Access Token
