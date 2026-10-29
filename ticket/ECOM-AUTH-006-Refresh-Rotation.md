# ECOM-AUTH-006 — Refresh Rotation

Status: DONE

## Source Design

- `ECOM-AUTH-004` — Access Token
- `ECOM-AUTH-005` — Refresh Token Schema/Queries
- `docs/data-003a-identity.md` — Auth owns sessions and refresh-token credentials
- `docs/api.md` — Existing JSON login contract
- [RFC 9700 §4.14.2 — Refresh Token Protection](https://www.rfc-editor.org/rfc/rfc9700.html#section-4.14.2)

## Prerequisite

AUTH-005 was reverified during this ticket: required PostgreSQL tests and a full migration rollback/apply passed on the isolated test database. Migration 000009 is clean. AUTH-005 is now DONE.

## Why This Ticket Exists

Before AUTH-006, login returned only a short-lived access token, normally valid for 15 minutes. There was no API to obtain another access token without submitting the password again.

AUTH-005 provides storage and SQL primitives. AUTH-006 connects those primitives into a complete login/refresh workflow: create a session at login, issue an initial refresh token, and exchange each valid refresh token for a new access token and a replacement refresh token.

Rotation makes each refresh token single-use. Keep its history so reuse of a consumed token can trigger revocation of the affected session and its remaining refresh tokens.

## Goal

Implement refresh-token issuance, rotation and reuse handling through Auth services, PostgreSQL repositories, HTTP handlers, configuration and application wiring.

## Scope

- Extend successful `POST /auth/login` to create a persistent session and its first refresh token.
- Add `POST /auth/refresh` to return a new access token and refresh token without requiring the password again.
- Retain the existing RS256 access-token issuer and maximum access-token lifetime.
- Check current User status through a User-owned boundary before issuing credentials.
- Persist token lineage and reject invalid, expired, revoked or previously consumed refresh tokens.
- Revoke the affected session and its tokens when a consumed token is reused.
- Update API documentation, route wiring and focused tests.

## API Contract

Implemented in the Auth HTTP handlers and documented in `docs/api.md`.

### Login Success

Extend the existing JSON response with `refresh_token` and `refresh_expires_in`, preserving `access_token`, `token_type` and `expires_in`.

### Refresh Request

```http
POST /auth/refresh
Content-Type: application/json
```

```json
{
  "refresh_token": "<opaque refresh token>"
}
```

Do not require a valid access token for this endpoint: refreshing must work after the access token has expired. Derive session and User identity from the stored refresh-token digest; do not accept client-supplied User or session IDs as authority.

### Refresh Success

```json
{
  "access_token": "<new signed JWT>",
  "token_type": "Bearer",
  "expires_in": 900,
  "refresh_token": "<new opaque refresh token>",
  "refresh_expires_in": 604799
}
```

The lifetime values above are examples. Access-token expiry follows AUTH-004 configuration. `AUTH_REFRESH_TOKEN_TTL` defaults to `168h` (7 days), accepts `1m` through `720h`, and sets the absolute session/refresh deadline. Each replacement expires no later than the original session deadline or its predecessor. Rotation does not extend that deadline. Remaining lifetime is returned in whole seconds, so issuance time can reduce the initial value by a second.

Use `Cache-Control: no-store` and `Pragma: no-cache`. Apply bounded JSON decoding, reject unknown fields and trailing JSON, and follow existing content-type and body-size errors. Return generic `401 auth.invalid_refresh_token` for credential/session/account rejection, and a safe `500 auth.refresh_failed` for infrastructure failures. Preserve request cancellation and deadline handling.

Transport for this backend ticket is explicit JSON, consistent with the existing login API. Browser cookie transport, CSRF policy and frontend token storage need a separately documented client integration decision.

## Important Invariants

- Generate opaque refresh tokens from at least 32 cryptographically random bytes and encode them using Base64URL.
- Persist only their SHA-256 digests; never log raw tokens, passwords or Authorization headers.
- Generate database IDs as UUIDv7 in Go.
- One session represents one login context; every successor stays in the predecessor's session.
- A token may produce at most one successor. Check affected-row counts from conditional queries; zero rows never means success.
- Consume predecessor, insert successor and update session activity in one PostgreSQL transaction.
- Initial session and initial token must also commit atomically.
- On RNG, signing, persistence or commit failure, return no credentials. Roll back tentative consume/insert operations when possible.
- Evaluate expiry using current database time after acquiring locks. Do not accept a token that expires while waiting for a lock because transaction-start `now()` is stale.
- A revoked/expired session or inactive/missing User cannot obtain new credentials.
- Revocation affects only the relevant session; other devices remain independent.
- Revoking a session does not automatically invalidate previously issued stateless access JWTs; their existing expiry remains in force unless a later access-token revocation mechanism is implemented.

## Implemented Workflow

1. Add a refresh-token generator and digest helper, with injected dependencies where needed to test RNG failures.
2. Add Auth-owned repository operations for session creation, transaction-scoped lookup/locking, conditional consume, successor insert, session activity and security revocation.
3. Use a consistent lock order: lock the owning session before locking/rechecking token state. Apply the same session lock convention to rotation and reuse revocation; document the contract for AUTH-007.
4. Extend login orchestration to create the initial session/token after successful authentication. Sign the access token before committing and expose the token pair only after a successful commit.
5. Implement refresh orchestration: locate digest, lock and recheck session/token, verify User status, prepare/sign new credentials, consume predecessor, insert successor, update activity, commit, then return the pair.
6. For a known consumed token, revoke the session and its refresh tokens and commit that revocation before returning the generic authentication error. Do not roll back the security action merely because the HTTP outcome is 401. Keep consumed-token history available for this check even after token expiry, while the session can still have live descendants.
7. Add the HTTP adapter, configuration, route registration and constructor wiring; document the extended login/refresh contracts.
8. Run focused unit, HTTP, PostgreSQL and concurrency tests, then repository verification gates.

## Concurrency and Retry Policy

Use strict single-use rotation for V1. Two concurrent requests with the same token can create at most one successor. After the winner commits, the loser observes reuse and revokes the session; the winner's replacement then becomes unusable. Document this behavior so clients serialize refresh calls and do not blindly retry an old token after an uncertain response.

Do not claim exactly-once delivery to the client: a committed rotation can be followed by a disconnected client or lost response. This strict policy may require a fresh login in that case. A grace window or cached retry response would require a separate security decision.

## Files and Responsibilities

- `internal/auth/refresh.go`: credential generation/digest, session issuance, rotation and reuse rules.
- `internal/auth/postgres_refresh_repository.go`: transaction adapter, session-first locks, guarded writes and committed security revocation.
- `internal/auth/refresh_http.go`: bounded JSON endpoint and safe response/error mapping.
- `sql/queries/auth_sessions.sql`: session creation, lock, activity, database clock and revocation queries.
- `sql/queries/auth_refresh_tokens.sql`: token insert/consume/revoke with real-time expiry guards; AUTH-005 schema is reused without modifying migration history.
- `sql/queries/user_auth_session.sql` and `internal/user/postgres_auth_session_reader.go`: User-owned status checks under a shared row lock in the same transaction.
- `internal/database/sqlc/auth_sessions.sql.go`, `user_auth_session.sql.go` and `auth_refresh_tokens.sql.go`: generated typed persistence operations.
- `internal/auth/login_token.go` and `login_http.go`: initial session/token-pair integration.
- `internal/config/config.go`, `.env.example`, `cmd/api/main.go` and `internal/server/routes.go`: validated lifetime policy and application wiring.
- `internal/auth/refresh_test.go`, `refresh_http_test.go`, `postgres_refresh_repository_test.go`: token generation, HTTP boundaries, rollback, isolation, lock-wait expiry and concurrency evidence.
- Existing login/access-token, config and routing tests: updated regression coverage.
- `Makefile`: refresh integration filter and reusable `test-auth-race` target.
- `docs/api.md`: public contract, token lifetimes, JSON transport and strict retry behavior.

Hand-written files include purpose notes and function comments. No new migration was required; schema remains at version 9.

## Acceptance Criteria

- [x] AUTH-005's required PostgreSQL verification passes; migration state is clean.
- [x] Successful login returns a token pair and atomically stores session plus initial digest.
- [x] Failed login creates neither a session nor refresh token.
- [x] A valid refresh rotates exactly once and preserves predecessor/session linkage.
- [x] Old-token reuse commits session-wide revocation before returning 401.
- [x] Unknown, malformed, expired and revoked tokens are rejected safely.
- [x] Suspended/deleted/missing Users and expired/revoked sessions cannot obtain credentials. Existing session owners cannot be physically missing while FK constraints hold.
- [x] Refresh expiry never exceeds the original session deadline.
- [x] Concurrent rotation has at most one successor and follows the documented strict reuse policy.
- [x] Revocation cannot race into leaving a usable successor; unrelated sessions remain usable.
- [x] Failure-injection tests prove rollback for consume/insert/activity changes and no credential response on commit/signing/RNG failure.
- [x] Tests cover expiry after lock waits, token lifetime, digest-only persistence and response/input boundaries.
- [x] Existing login error and access-token validation tests remain valid.
- [x] Configuration, API docs, comments, routes and application wiring are updated.

## Verification — 2026-10-22

- `make agent-preflight`: PASS.
- `make sqlc-generate` and `make sqlc-check`: PASS.
- Focused Auth/config/server tests: PASS.
- `make test-auth-integration`: PASS with required PostgreSQL integration enabled.
- `make test-auth-race`: PASS with required PostgreSQL integration and Go race detector.
- `make verify-full`: PASS, including all PostgreSQL integration tests.
- Full migration cycle: PASS — 9 → empty schema → 9 on the isolated test database.
- `make db-migration-status`: PASS — version 9, clean.
- `make diff-check`: PASS.

Tests used the repository's local `nexus_commerce_test` database. No application database or persistent volume was deleted. Commit-failure injection models an error before COMMIT; an actual network failure during COMMIT can have an unknown outcome. No token pair is returned on a commit error, and raw credentials are not persisted for recovery.

## Out of Scope

- Public logout/revoke endpoints: AUTH-007.
- Password reset, email verification and session-management UI/endpoints.
- Redis token storage, distributed rate limiting, and immediate access-JWT invalidation.
- Changing AUTH-004's signing algorithm or implementing a complete OAuth authorization server.

## Result

PASS

## Next

ECOM-AUTH-007 — Logout / Revocation
