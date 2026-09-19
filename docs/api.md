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
