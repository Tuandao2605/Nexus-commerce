# ECOM-AUTH-002 — Password Hashing

Status: DONE

## Source Design

- ECOM-AUTH-001 — Registration
- `docs/data-003a-identity.md`
- `nexus_commerce_golang_requirements.txt`
- `docs/NEXUS-COMMERCE_V1_CANONICAL_ROADMAP.md`
- [OWASP Password Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html)
- [RFC 9106 — Argon2](https://www.rfc-editor.org/rfc/rfc9106.html)
- [`golang.org/x/crypto/argon2`](https://pkg.go.dev/golang.org/x/crypto/argon2)

## Goal

Triển khai concrete password hasher an toàn cho Registration bằng Argon2id,
encoded hash tự mô tả parameters/salt, constant-time verification và rehash
detection cho Login ticket kế tiếp. Wire registration use case vào public HTTP
route sau khi không còn cần placeholder hasher.

## Password Hashing Policy

```text
algorithm   = Argon2id
version     = 19
memory      = 19 MiB (19456 KiB)
iterations  = 2
parallelism = 1
salt        = 16 random bytes
key         = 32 bytes
encoding    = PHC string + unpadded Base64
```

Baseline trên đáp ứng minimum Argon2id configuration hiện hành của OWASP.
Parameters nằm trong mỗi hash để verify record cũ và phát hiện nhu cầu rehash khi
policy được nâng cấp.

Ví dụ format, không phải hash cố định:

```text
$argon2id$v=19$m=19456,t=2,p=1$<salt>$<derived-key>
```

## Important Invariants

- Password không bao giờ được lưu hoặc phản hồi ở dạng plaintext
- Mỗi hash dùng salt riêng từ `crypto/rand`
- Verification dùng `subtle.ConstantTimeCompare`
- Algorithm, version, parameters, salt và key được parse strict
- Parser giới hạn encoded length, memory, iterations, parallelism, salt và key trước khi chạy KDF
- Hash mới không được cấu hình yếu hơn reviewed baseline
- Sai password và malformed stored hash là hai typed errors khác nhau
- `NeedsRehash` so sánh đầy đủ work factor cùng salt/key length
- HTTP DTO chỉ nhận `display_name`, `email`, `password`; unknown fields bị reject
- HTTP body bị giới hạn 16 KiB và yêu cầu `application/json`
- Client error không chứa SQL, stack, plaintext password hoặc encoded hash
- Request context tiếp tục truyền HTTP → service → repository → PostgreSQL

## Implementation

Files:

- `internal/auth/password_hasher.go` triển khai Argon2id hash/verify/parse/rehash contract
- `internal/auth/password_hasher_test.go` kiểm thử correct/wrong password, random salt, unsafe parameters và malformed PHC records
- `internal/auth/registration_http.go` triển khai bounded JSON handler cùng stable error envelope
- `internal/auth/registration_http_test.go` kiểm thử success, content type, size, unknown fields, trailing JSON và error mapping
- `internal/auth/postgres_registration_repository_test.go` thêm HTTP → Argon2id → PostgreSQL integration proof
- `cmd/api/main.go` wiring hasher, User writer, Auth repository, service và HTTP handler
- `internal/server/routes.go` inject và đăng ký `POST /auth/registrations`
- `internal/server/server.go` nhận typed route dependencies từ composition root
- `internal/server/server_test.go` kiểm thử registration route delegation
- `docs/api.md` ghi public registration request/response/error contract
- `go.mod`, `go.sum` chuyển `golang.org/x/crypto` thành direct dependency và ghi dependency `x/sys`
- `.agents/skills/nexus-ecommerce/` đồng bộ implemented-state/API facts cho các ticket sau
- `ticket/ECOM-AUTH-002-Password-Hashing.md` ghi scope, security decisions và evidence
- `docs/NEXUS-COMMERCE_V1_CANONICAL_ROADMAP.md` chuyển current ticket sang AUTH-003 sau khi gate pass

Không có migration `000009`; `credentials.password_hash TEXT` đã hỗ trợ PHC encoded string.

## HTTP Contract

```http
POST /auth/registrations
Content-Type: application/json
```

```json
{
  "display_name": "Tuan Nguyen",
  "email": "tuan@example.com",
  "password": "user supplied password"
}
```

Success trả `201 Created` với `id`, canonical `email` và `display_name`; không có
password/hash. Chi tiết status/error codes nằm trong `docs/api.md`.

## Acceptance Criteria

- [x] Argon2id version 19 được dùng qua `golang.org/x/crypto/argon2`
- [x] Default work factor đạt OWASP minimum hiện hành
- [x] Salt 16 byte được tạo bằng `crypto/rand` cho từng hash
- [x] Cùng password tạo hash khác nhau
- [x] Correct password verify thành công
- [x] Wrong password trả `ErrPasswordMismatch`
- [x] Malformed/unsupported hash trả `ErrInvalidPasswordHash`
- [x] Verification dùng constant-time comparison
- [x] Parser chặn excessive resource parameters trước KDF
- [x] Weak/excessive new-hash policy bị constructor từ chối
- [x] Current hash không cần rehash; legacy policy được phát hiện
- [x] Registration service dùng concrete hasher ở application bootstrap
- [x] Registration HTTP route được wiring vào Chi
- [x] Unknown JSON fields và multiple documents bị reject
- [x] Oversized body trả `413`; wrong media type trả `415`
- [x] Duplicate email trả typed `409` contract
- [x] HTTP integration test chứng minh PostgreSQL lưu Argon2id hash verify được
- [x] Không log hoặc phản hồi password/hash
- [x] Không thay đổi schema/migration
- [x] Migration database vẫn version `8`, clean

## Commands

```bash
make test-target TEST_PACKAGE='./internal/auth ./internal/server ./cmd/api' TEST_NAME='^TestArgon2id|^TestRegistrationHandler|^TestHealth|^TestRegistrationRoute'
make test-auth-integration TEST_DATABASE_URL='...'
REQUIRE_DATABASE_INTEGRATION=1 TEST_DATABASE_URL='...' go test -race -timeout 2m ./internal/auth -count=1
make verify-fast
make verify-full TEST_DATABASE_URL='...'
make db-migration-status TEST_DATABASE_URL='...'
git diff --check
```

## Verification

- Focused Argon2id tests: PASS
- Focused registration HTTP tests: PASS
- Registration route wiring test: PASS
- HTTP → Argon2id → PostgreSQL integration test: PASS
- Full Auth race test: PASS
- `make sqlc-check`: PASS
- `make verify-fast`: PASS
- `make verify-full`: PASS
- Full migration cycle: PASS — `008 → 0 → 008`
- `schema_migrations`: version `8`, clean
- `git diff --check`: PASS

## Decisions

- Chọn Argon2id thay cho fast general-purpose hash; parameters được version-control trong source, không lấy từ secret environment variables.
- Dùng OWASP 19 MiB / 2 iterations / p=1 baseline để giữ memory cost có thể vận hành trong API process; cần benchmark/tune trên production hardware trước launch.
- Giới hạn verification tối đa 256 MiB, 10 iterations và 16 lanes để corrupted/malicious stored hash không gây resource exhaustion không giới hạn.
- Không dùng pepper trong V1 vì chưa có secret-manager/HSM lifecycle; không hardcode pepper trong source.
- `NeedsRehash` được chuẩn bị cho AUTH-003 Login, nhưng AUTH-002 chưa tự mutation hash khi chưa có authenticated login flow.
- Registration endpoint dùng noun resource `/auth/registrations` và không tạo global `/api/v1` prefix.
- Registration không dùng application idempotency table; database unique email là concurrency authority. Retry sau success nhận deterministic `409` thay vì tạo account thứ hai.
- Distributed registration rate limiting thuộc ECOM-SEC-006. Route phải được đặt sau edge protection hoặc không public production trước ticket đó.

## Result

PASS

## Next

ECOM-AUTH-003 — Login
