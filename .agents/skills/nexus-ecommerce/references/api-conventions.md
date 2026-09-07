# API and Error Conventions

Use this reference for HTTP handlers, JSON contracts, validation, idempotency, pagination, and payment webhooks.

## Implemented and Provisional Contracts

The only implemented route is:

```http
GET /health
200 Content-Type: application/json
{"status":"ok"}
```

The repository has not finalized a global API prefix, success envelope, pagination response, or error envelope. Preserve an existing contract when one is present. When adding the first shared contract, update this reference/API documentation and cover it with handler tests instead of creating endpoint-specific formats.

The project default error shape for new JSON APIs is:

```json
{
  "error": {
    "code": "inventory.insufficient_stock",
    "message": "Requested quantity is unavailable",
    "details": [],
    "request_id": "..."
  }
}
```

Rules:

- `code` is stable and machine-readable; namespace domain errors.
- `message` is safe for clients and must not expose SQL, stack, secret, provider-signature, or internal topology details.
- `details` contains field errors or structured context and may be omitted when empty.
- `request_id` matches structured logs/traces and may be omitted only before request middleware exists.
- Encode JSON once and set status/header before writing the body.

## Status Mapping

Use the most specific stable mapping:

- `400 Bad Request`: malformed JSON, invalid fields, or unsupported query parameters.
- `401 Unauthorized`: missing, invalid, expired, or revoked authentication.
- `403 Forbidden`: authenticated actor lacks permission or resource/shop ownership.
- `404 Not Found`: resource is absent or intentionally hidden by an established anti-enumeration policy.
- `409 Conflict`: invalid state transition, idempotency-key payload mismatch, duplicate unique resource, insufficient stock, exhausted voucher, or other current-state conflict.
- `422 Unprocessable Entity`: use only if the project formally distinguishes syntactically valid semantic validation from `400`; do not mix both arbitrarily.
- `429 Too Many Requests`: rate limit exceeded, with retry metadata when known.
- `500 Internal Server Error`: unexpected failure with a generic client message.
- `502`/`503`/`504`: upstream failure/unavailability/timeout when that distinction is useful to a caller.

Translate PostgreSQL constraint errors and typed domain errors at one boundary. Do not string-match arbitrary error text in handlers.

## Handler Boundary

A handler should:

1. propagate request context and request/trace identity;
2. authenticate and load the actor;
3. decode with bounded body size and reject malformed input;
4. validate request syntax and semantic prerequisites;
5. authorize permission plus ownership;
6. call one application use case;
7. map typed results/errors to the stable HTTP contract.

Never trust client-provided user/shop ownership, price, discount, order total, payment state, or inventory availability. Derive identity from authentication and reload commerce facts from their owners.

## Resource and Query Style

- Use nouns for resource paths and HTTP methods for actions where normal resource semantics fit.
- Explicit state-changing commands such as checkout or payment webhooks may use action endpoints when they represent a use case rather than CRUD.
- Keep `GET /health` for process liveness; add `GET /ready` for dependency readiness.
- Catalog browsing supports pagination, filtering, sorting, search, category, brand, shop/seller, price range, and lifecycle status.
- Bound page/limit values and use a deterministic tie-breaker in sorting. If cursor pagination is introduced, make the cursor opaque and tied to the sort.
- Existing requirement examples use camelCase query names such as `minPrice` and `maxPrice`; keep naming consistent within the public contract.

Do not introduce `/api/v1`, a different prefix, or a success envelope without making it an explicit project-wide contract decision.

## Critical-Write Idempotency

`POST /checkout` and similar critical writes require `Idempotency-Key`.

- Scope the key to the authenticated actor and operation.
- Hash a canonical request representation.
- Same key plus same hash returns the stored in-progress/completed outcome as defined by the endpoint.
- Same key plus a different hash returns `409` with a stable idempotency conflict code.
- Concurrent claims of the same key are serialized with a database uniqueness/transaction mechanism.
- Persist the business result and idempotency outcome consistently; do not rely on in-memory maps.

## Payment Webhooks

- Verify the provider signature against the raw request bytes before trusting parsed fields.
- Enforce timestamp/nonce or provider-event replay protection.
- Deduplicate by a provider event/transaction identity protected by a database unique constraint.
- Acknowledge duplicates without repeating the business effect.
- Payment changes only Payment-owned state, then emits a durable result event/application notification. It never updates Order tables directly.
- Log provider identifiers and outcome, not secrets, tokens, full sensitive payloads, or signatures.

## Cancellation and Timeouts

Propagate `request.Context()` through handler, service, repository, PostgreSQL, Redis, and synchronous provider calls. Use explicit downstream timeouts. Retry only classified transient failures, with bounded attempts and jitter; never retry validation, authorization, state conflicts, or non-idempotent calls blindly.

## Tests

For each endpoint, cover success, malformed input, validation, unauthenticated, forbidden ownership, not found, domain conflict, internal mapping, cancellation where meaningful, and stable JSON/content type. Critical writes also cover same-key replay, different-payload conflict, and concurrent duplicate requests.

## Repository Sources

- `internal/server/routes.go` and `internal/server/server_test.go`: current health contract.
- `nexus_commerce_golang_requirements.txt`: endpoint examples, idempotency, context propagation, rate limiting, logging, and health/readiness requirements.
