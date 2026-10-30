# ECOM-AUTH-007 — Logout / Revocation

Status: READY

## Source Design

- ECOM-AUTH-004 — RS256 access tokens are stateless and short-lived.
- ECOM-AUTH-005 — Refresh-token schema and session ownership.
- ECOM-AUTH-006 — Session-first locking, rotation and session-wide revocation.
- `docs/data-003a-identity.md` and `docs/api.md` — Identity ownership and existing JSON API contracts.
- [RFC 7009 §2.2 — Revocation Response](https://www.rfc-editor.org/rfc/rfc7009.html#section-2.2): reference for treating an unknown token as an already-achieved revocation outcome. This ticket defines a custom JSON API with HTTP 204; it does not implement the RFC's form-encoded OAuth revocation endpoint or claim protocol compliance.

## Goal

Cho người dùng đăng xuất session hiện tại bằng `POST /auth/logout`. Backend phải thu hồi session và toàn bộ refresh token thuộc session đó trong một transaction, phối hợp an toàn với rotation của AUTH-006.

Ticket này là đặc tả và hướng dẫn triển khai; endpoint chưa được viết.

## User Story and Business Outcome

Người dùng bấm “Đăng xuất” trên một thiết bị. Client gửi refresh token đang giữ tới backend. Backend xác định session từ digest của token, khóa session, ghi revocation và commit. Sau response thành công, client bỏ token và chuyển về trạng thái chưa đăng nhập.

Ví dụ: cùng User có session S1 trên laptop và S2 trên điện thoại. Logout bằng một token thuộc S1 chỉ thu hồi S1 và các refresh token của S1. S2 tiếp tục sử dụng bình thường.

Chỉ xóa token ở client không thu hồi bản token có thể đã được sao chép. Ghi `revoked_at` ở server ngăn session đó tiếp tục nhận access token qua refresh.

## Scope and Policy

- Logout current session bằng refresh-token possession; không yêu cầu access JWT còn hạn.
- Session được suy ra từ token trong DB. Không nhận `user_id` hoặc `session_id` từ client làm bằng chứng quyền sở hữu.
- Thu hồi cả session và toàn bộ token history thuộc session; giữ lại records, không DELETE.
- Known consumed/expired/revoked token vẫn dùng để xác định và thu hồi session. Một predecessor cũ không được bỏ sót successor còn sống.
- User bị suspend/deleted vẫn có thể logout; không thêm active-User gate vào thao tác thu hồi.
- Request lặp lại có cùng kết quả quan sát được; không ghi đè revocation timestamp đã có.
- Token không tìm thấy hoặc chuỗi token sai định dạng trả success no-op, không tiết lộ token tồn tại hay không.
- Lỗi lookup, transaction, revoke hoặc commit thực sự phải được propagate; không biến mọi error thành success.
- Stateless access JWT đã cấp không bị vô hiệu hóa tức thời. Chữ ký và expiry của chúng vẫn có hiệu lực theo AUTH-004. Logout ngăn refresh tiếp theo.

## API Contract to Implement

```http
POST /auth/logout
Content-Type: application/json
```

```json
{
  "refresh_token": "<opaque refresh token>"
}
```

- Thành công: `204 No Content`, body rỗng. Không gọi JSON encoder để ghi `null` hoặc `{}` cho status 204.
- Set `Cache-Control: no-store` và `Pragma: no-cache`, cả success và error paths.
- Body tối đa 16 KiB; yêu cầu application/json; từ chối unknown fields và trailing JSON.
- Body phải là một object có field `refresh_token` kiểu string. Missing/null/non-string field là `400 request.invalid_json`; một string rỗng hoặc không đúng token format là no-op `204`.
- Dùng pointer/kiểm tra presence khi decode để phân biệt missing/null field với string rỗng.
- Không yêu cầu Authorization header để thao tác vẫn dùng được khi access token đã hết hạn. Quyền thu hồi một session được chứng minh bằng việc biết refresh credential của chính session đó.

| HTTP | Code | Khi nào |
| --- | --- | --- |
| 204 | — | Thu hồi thành công; token unknown/malformed string; logout lặp lại |
| 400 | request.invalid_json | JSON sai, thiếu/null field, sai kiểu, unknown field hoặc trailing document |
| 408 | request.cancelled | Request context bị hủy |
| 413 | request.body_too_large | Body vượt 16 KiB |
| 415 | request.unsupported_media_type | Không phải application/json |
| 500 | auth.logout_failed | Lỗi persistence/revocation/commit, thông báo an toàn |
| 504 | request.timeout | Deadline của request hết hạn |

Giữ error envelope đang dùng ở Auth. Không trả hoặc log password, raw refresh/access token hay Authorization header. Dùng request context xuyên suốt. Production transport phải bảo vệ credential bằng HTTPS.

## Analysis by Layer

### 1. Client — User Interaction

Khi bấm logout, client dừng/serialize refresh đang chạy, gửi refresh token trước khi bỏ bản lưu local và không lưu lại response của một refresh cũ đến muộn sau logout. Sau 204, bỏ cả access và refresh token và chuyển UI về trạng thái chưa đăng nhập.

Frontend chưa thuộc scope triển khai backend của ticket. Nếu request lỗi hoặc response mất, client vẫn có thể clear token local để kết thúc UI session, nhưng không được coi server-side revocation là đã được xác nhận. Gửi lại logout với token đã có là an toàn; request không tạo credential mới.

### 2. HTTP Adapter — `internal/auth/logout_http.go`

Tạo `LogoutUseCase` có method `Logout(ctx context.Context, raw string) error`; `LogoutHandler` chỉ cần interface này và logger.

`ServeHTTP` chịu trách nhiệm headers, content type, bounded decoding, field presence và map errors. Handler gọi use case đúng một lần rồi `WriteHeader(http.StatusNoContent)` khi không có lỗi. Handler không mở transaction, đọc SQL, kiểm tra User status hay ký token.

Tham khảo `refresh_http.go` để giữ contract thống nhất. Không gọi `RefreshService.Refresh()` để logout: hàm đó thực hiện rotate/phát credential và có nghiệp vụ khác.

### 3. Application Service — `internal/auth/logout.go`

Tạo `LogoutService` chỉ giữ repository và constructor kiểm tra dependency. Không cần access-token issuer, RNG, TTL hoặc User status reader ở lớp service.

Luồng `Logout(ctx, raw)`:

1. Kiểm tra context; nếu đã cancel/deadline thì trả đúng lỗi context.
2. Parse canonical 43-character Base64URL/32-byte refresh token và hash bằng SHA-256 trên chuỗi token, giống AUTH-006. Sai format trả nil, không cần DB access.
3. Gọi `repository.Transact(ctx, callback)`.
4. Trong callback, gọi `tx.LockToken(ctx, digest)` để resolve session và lấy lock theo convention hiện tại.
5. Nếu lỗi là `ErrInvalidRefreshToken` do không còn record/session thì trả nil như no-op. Dùng `errors.Is`, không so text và không bỏ qua error khác.
6. Nếu có session, gọi `tx.Revoke(ctx, session.ID)` dù token đã consumed/expired/revoked hoặc User không active.
7. Return lỗi revoke nếu có; nếu không return nil để `Transact` commit.
8. Chỉ trả nil ra handler khi transaction/no-op hoàn tất. Lỗi commit không được map thành 204.

Có thể extract một helper nhỏ dùng chung để parse/hash refresh credential giữa `Refresh()` và `Logout()`. Helper chỉ kiểm tra format/hash; caller quyết định malformed là 401 khi refresh hay success no-op khi logout. Tránh sửa logic rotation/reuse ngoài việc chia sẻ helper nếu cần.

### 4. Repository — Reuse AUTH-006

Tái sử dụng `RefreshRepository.Transact` và các operation `RefreshTransaction.LockToken`, `RefreshTransaction.Revoke`. Có thể inject cùng `PostgresRefreshRepository` hiện có vào `NewLogoutService`; không cần tạo repository implementation mới chỉ để đổi tên.

`LockToken` hiện thực hiện lookup digest không khóa để biết session, sau đó `LockAuthSession FOR UPDATE`, rồi `LockAuthRefreshToken FOR UPDATE` để đọc lại trạng thái. Giữ thứ tự session trước token.

`Revoke` hiện gọi `RevokeAuthSession` và `RevokeSessionRefreshTokens` trên cùng pgx transaction. `Transact` rollback callback error và commit callback success. SQL error hoặc commit error phải ngăn handler trả 204.

### 5. SQL / Persistence — Reuse Existing Queries

Các query đã có:

- `GetRefreshTokenByHash` — tìm token, kể cả expired/consumed.
- `LockAuthSession` — khóa session.
- `LockAuthRefreshToken` — khóa/đọc lại token.
- `RevokeAuthSession` — set revoked_at bằng DB clock khi chưa revoke.
- `RevokeSessionRefreshTokens` — set revoked_at cho mọi token chưa revoke thuộc session.

Không thêm expiry/consumed filter vào lookup phục vụ logout. Không chỉ revoke một token row, vì có thể bỏ sót successor. Zero rows ở một idempotent revoke không tự động là error: session/token có thể đã được revoke trước đó. Phân biệt zero rows này với conditional consume của rotation vốn phải tác động đúng một row.

Schema hiện tại đủ cho thiết kế này; dự kiến không cần migration hay query mới. Nếu thực tế cần đổi schema thì thêm migration mới theo DB workflow, không sửa history đã apply.

### 6. Wiring — Router and Composition Root

- `cmd/api/main.go`: tạo `logoutService` từ refresh repository đã có, tạo `logoutHandler` với logger.
- `internal/server/routes.go`: thêm `Logout http.Handler` vào `RouteDependencies` và đăng ký `POST /auth/logout`.
- Không gắn access-JWT-required middleware riêng cho endpoint này.
- `docs/api.md`: mô tả API, idempotency, current-session scope, fixed access-JWT expiry và client retry behavior.
- Roadmap chỉ chuyển sang AUTH-008 sau implementation và required verification thực sự PASS.

## Transaction and Concurrency

```text
BEGIN
  lookup token digest → session ID
  lock session FOR UPDATE
  lock/re-read token FOR UPDATE
  update sessions.revoked_at if null
  update all refresh_tokens.revoked_at in that session if null
COMMIT
→ 204
```

Nếu refresh commit trước, logout lấy được session lock sau đó và revoke luôn successor vừa tạo. Nếu logout commit trước, refresh chờ lock rồi phải thấy session đã revoke và từ chối. Không được còn usable refresh descendant sau khi logout đã commit.

Hai logout đồng thời đều có thể trả 204 sau khi serialize qua session lock. Không đổi deadline, không tạo token, không gia hạn session. Session khác của cùng User không bị ảnh hưởng.

## Suggested Coding Order

1. Đọc `refresh.go`, `postgres_refresh_repository.go` và các revoke queries để hiểu callback commit/rollback.
2. Viết `logout.go` và unit tests cho known/unknown/malformed token, repo errors và context.
3. Viết PostgreSQL tests chứng minh cùng transaction, timestamps giữ nguyên khi retry, device isolation và race với refresh.
4. Viết `logout_http.go` cùng HTTP tests cho contract 204/error/input boundaries.
5. Wiring vào `main.go`, router và route test.
6. Cập nhật API docs/Makefile test filters, chạy verification rồi mới đánh dấu DONE.

File mới cần note mục đích ở đầu file và comment trước các hàm. Giữ tests ở package hiện có theo convention repo.

## Acceptance Criteria

- [ ] POST /auth/logout dùng JSON contract, không yêu cầu access JWT.
- [ ] Known token thu hồi đúng một session và mọi refresh token trong session đó.
- [ ] Consumed/expired predecessor vẫn thu hồi được live successor.
- [ ] User không active vẫn logout được.
- [ ] Unknown/malformed token string là 204 no-op; malformed request là 4xx theo contract.
- [ ] Retry/concurrent logout không đổi revoked_at đã ghi và không tạo credential.
- [ ] Logout và rotation đồng thời không để lại usable successor sau revocation commit.
- [ ] Session khác của cùng User vẫn refresh được.
- [ ] Lỗi lookup/revoke/commit không trả 204; injected failure sau write rollback toàn bộ mutations.
- [ ] HTTP 204 body rỗng; error response, headers, size, content type và field presence có tests.
- [ ] Context propagation và không lộ token trong logs/errors được kiểm tra.
- [ ] API docs nêu rõ access JWT còn hiệu lực đến expiry; không tuyên bố logout vô hiệu hóa JWT tức thời.
- [ ] Required PostgreSQL/race tests PASS, migration version 9 clean nếu không có schema change.

## Verification Plan

- `make agent-preflight` trước implementation.
- `make test-target TEST_PACKAGE=./internal/auth TEST_NAME=Logout` cho focused iteration.
- Bổ sung `TestPostgresLogout` vào filter của `make test-auth-integration` và `make test-auth-race`; filters hiện tại chưa chạy các test tên Logout.
- `make sqlc-generate` / `make sqlc-check` nếu có sửa SQL; không chỉnh generated files bằng tay.
- `make test-auth-integration` và `make test-auth-race` với test DB bắt buộc, không chấp nhận skip làm PASS.
- `make verify-full`, `make db-migration-status` và `make diff-check`.

## Out of Scope

- Logout mọi thiết bị, listing/revoking session theo ID hoặc admin session management: cần scope/authorization riêng, gắn với AUTH-010.
- Thêm cookie transport, frontend implementation hoặc thay đổi CSRF policy.
- Access-token denylist/introspection, sửa JWT claims/signature hoặc immediate access-JWT invalidation.
- Redis, rate-limit implementation, password reset và email verification.

## Result

PLANNED — ticket and layered implementation guidance created; application implementation and runtime tests are NOT RUN in this task.

## Next

ECOM-AUTH-008 — Password Reset
