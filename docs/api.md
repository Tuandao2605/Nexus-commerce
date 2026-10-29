# Nexus-Commerce V1 API

Status: IMPLEMENTED CONTRACTS ONLY

## General JSON Rules

- Business request bodies use `application/json`.
- Unknown JSON fields are rejected to prevent accidental mass assignment.
- Each request body must contain exactly one JSON object and is size-bounded by the owning endpoint.
- Business errors use a stable machine-readable envelope:

```json
{
  "error": {
    "code": "auth.invalid_registration",
    "message": "Registration request is invalid",
    "details": [
      {"field": "email", "code": "invalid"}
    ]
  }
}
```

`request_id` will be added when request identity middleware is implemented.

## GET /health

Process liveness endpoint.

Response: `200 OK`

```json
{"status":"ok"}
```

## POST /auth/registrations

Creates one active User and one email/password Credential atomically.

Request body limit: `16 KiB`.

Request:

```json
{
  "display_name": "Tuan Nguyen",
  "email": "tuan@example.com",
  "password": "user supplied password"
}
```

Success: `201 Created`

```json
{
  "id": "0199f9d2-2e83-7000-8000-000000000001",
  "display_name": "Tuan Nguyen",
  "email": "tuan@example.com"
}
```

The response never contains the password or encoded password hash.

Errors:

| Status | Code | Meaning |
| --- | --- | --- |
| `400` | `request.invalid_json` | Malformed, trailing or unknown JSON fields |
| `400` | `auth.invalid_registration` | Invalid registration field |
| `408` | `request.cancelled` | Request context was cancelled |
| `409` | `auth.email_already_registered` | Canonical email already exists |
| `413` | `request.body_too_large` | Body exceeds 16 KiB |
| `415` | `request.unsupported_media_type` | Content-Type is not `application/json` |
| `500` | `auth.registration_failed` | Safe generic internal failure |
| `504` | `request.timeout` | Request deadline expired |

Registration rate limiting is not part of AUTH-002. `ECOM-SEC-006` must add the
distributed policy before public production exposure.

## POST /auth/login

Authenticates one active email/password account, creates a persistent session,
and returns a short-lived RS256 JWT access token plus an opaque refresh token.
The request body is limited to 16 KiB; unknown fields and
trailing JSON documents are rejected.

Request:

    {
      "email": "tuan@example.com",
      "password": "user supplied password"
    }

Success: 200 OK with Cache-Control: no-store and Pragma: no-cache

    {
      "access_token": "<signed JWT>",
      "token_type": "Bearer",
      "expires_in": 900,
      "refresh_token": "<opaque refresh token>",
      "refresh_expires_in": 604799
    }

The access token uses typ=at+jwt, alg=RS256, and iss, sub, aud, client_id, iat,
exp, and jti claims. Its default lifetime is 15 minutes and the configured
maximum is 15 minutes. It contains no email, password, or authorization roles.
Resource handlers must validate signature, issuer, audience, key ID, expiry, and
intended access-token type before trusting sub.

| Status | Code | Meaning |
| --- | --- | --- |
| 400 | request.invalid_json | Malformed, trailing or unknown JSON fields |
| 408 | request.cancelled | Request context was cancelled |
| 401 | auth.invalid_credentials | Email/password invalid or account is inactive |
| 413 | request.body_too_large | Body exceeds 16 KiB |
| 415 | request.unsupported_media_type | Content-Type is not application/json |
| 500 | auth.login_failed | Safe generic internal failure |
| 504 | request.timeout | Request deadline expired |

Login uses one generic credential error for unknown email, wrong password,
malformed stored hash, and inactive account. Distributed brute-force protection
is still required before public production exposure and belongs to the security
rate-limiting ticket. Session and initial refresh digest are persisted atomically;
no token pair is returned before commit. Refresh tokens contain 32 random bytes
encoded as unpadded Base64URL; only SHA-256 digests are stored in PostgreSQL.

## POST /auth/refresh

Exchanges one live, unused refresh token for a new access token and a replacement
refresh token. No access JWT is required. Send `Content-Type: application/json`:

```json
{
  "refresh_token": "<opaque refresh token>"
}
```

Success is `200 OK`, with the same five fields as login and `Cache-Control:
no-store`, `Pragma: no-cache`. Both access and refresh token values change.
`expires_in` is the configured access-token lifetime. `refresh_expires_in` is
the remaining refresh lifetime in whole seconds, so an initial response may show
604799 rather than 604800 because issuance takes time.

`AUTH_REFRESH_TOKEN_TTL` defaults to `168h` (7 days), accepts `1m` through `720h`,
and sets an absolute session deadline. Rotation never extends that deadline or
the predecessor's expiry. The User must still be active; the session and token
must be unrevoked and unexpired. Session identity is derived from the digest,
never from a client-supplied User/session ID.

| Status | Code | Meaning |
| --- | --- | --- |
| 400 | request.invalid_json | Malformed, null, unknown-field or trailing JSON |
| 401 | auth.invalid_refresh_token | Invalid/reused credential, inactive User or expired/revoked session |
| 408 | request.cancelled | Request context cancelled |
| 413 | request.body_too_large | Body exceeds 16 KiB |
| 415 | request.unsupported_media_type | Content-Type is not application/json |
| 500 | auth.refresh_failed | Safe infrastructure/transaction failure |
| 504 | request.timeout | Request deadline expired |

Rotation locks the session before the token. Consuming the old token, creating
its successor and updating activity commit in one transaction. Reuse of a
consumed token, including an expired predecessor, commits revocation of that
session and all of its refresh tokens before returning 401. Other sessions are
unaffected. Security-revocation persistence failures return 500 rather than
claiming that revocation succeeded.

Clients must serialize refresh requests and replace their old credential after
success. With the strict V1 policy, two concurrent uses of one token create at
most one successor; the losing request detects reuse and revokes the session,
including the winner's replacement. If a rotation commits but its response is
lost, retrying the old token requires a fresh login. There is no retry grace
window or cached credential response.

These backend endpoints transport refresh tokens explicitly in JSON and do not
set authentication cookies. Browser storage/cookie transport and CSRF policy
need a client-integration design; this API does not prescribe localStorage.
Revoking a session prevents further refreshes; already-issued stateless access
JWTs remain valid until their own expiry. Public logout/revocation endpoints
belong to AUTH-007. Distributed refresh/login rate limiting remains a separate
security ticket before public production exposure.
