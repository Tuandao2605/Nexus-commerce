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

Authenticates one active email/password account and returns a short-lived RS256
JWT access token. The request body is limited to 16 KiB; unknown fields and
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
      "expires_in": 900
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
rate-limiting ticket. Refresh-token/session issuance and revocation are separate
Auth tickets; this response only issues an access token.
