# Security and Authorization

Use this reference for authentication, sessions/tokens, RBAC, ownership, rate limiting, webhooks, uploads, audit logs, and sensitive data.

## Identity Ownership

- Auth owns login email, credentials, password hashes, sessions, refresh tokens, verification/reset artifacts, and token lifecycle.
- User owns profile and addresses.
- Seller owns seller accounts, shops, memberships, and shop-scoped authorization.
- Authentication answers who the actor is. Authorization separately checks permission and ownership for the requested resource.

Do not put the authentication email/password into the User profile model merely for convenience.

## Authorization

Expected platform roles include Customer, Seller, Admin, Support, and Warehouse Manager, but role alone is insufficient.

For every protected operation:

1. authenticate the actor;
2. check required permission;
3. load or securely derive resource ownership/tenant scope;
4. verify the actor's active shop membership or resource relationship;
5. perform the mutation with ownership included in the query/transaction where practical.

A seller cannot update another seller's Product, SKU, Shop, Order view, or Inventory. Seller stock changes go through Inventory's application boundary and never direct table writes.

Avoid trusting `user_id`, `seller_id`, or `shop_id` supplied by a client as proof of ownership. Prevent mass assignment with explicit request DTOs and explicit mutation fields.

## Credentials, Tokens, and Sessions

- Hash passwords with a password-specific adaptive algorithm; store only the encoded hash.
- Never hardcode secrets or commit real secrets to environment files.
- Access tokens are short-lived and validated for signature, issuer/audience when configured, expiry, and intended token type.
- Refresh tokens use rotation, revocation, replay/reuse handling, and server-side session linkage.
- Logout/revocation invalidates the relevant session/refresh lineage.
- Authentication responses and logs never expose password hashes, raw refresh tokens, reset tokens, or verification secrets.

Exact token algorithm, lifetime, and cookie/header transport are not finalized in the current repository. Make them explicit configuration/security decisions when implemented.

## Input and Output Safety

- Use typed decoding and validation; reject unknown or dangerous fields according to the shared API policy.
- Use parameterized SQL/sqlc, never build SQL from untrusted strings.
- Treat product HTML/rich content as untrusted and sanitize/encode at the appropriate output boundary.
- Return generic client errors for internal failures.
- Do not expose whether protected resources exist when the established anti-enumeration policy requires concealment.

## Payment Webhooks and Replay

- Verify signatures using the exact raw body and constant-time comparison where applicable.
- Validate event timestamp/nonce and provider account/environment.
- Deduplicate durable provider event IDs in PostgreSQL.
- Process one successful payment into one business effect even after duplicate or delayed delivery.
- Reconciliation must use authenticated provider APIs and must not bypass the same state machine/idempotency rules.

## Rate Limiting

Use a distributed Redis-backed limiter when rate limiting is implemented so policy holds across API instances. Define distinct limits for login, registration, password reset, search, checkout, and general APIs. Key by the appropriate combination of IP, user ID, and API identity.

Design the failure mode explicitly per endpoint. Do not silently treat Redis as the source of truth for authorization, payment, order, voucher, or inventory correctness.

## File and Object Storage

- Validate authentication, shop ownership, content type, extension, size, and decoded file content.
- Generate object keys server-side; do not accept arbitrary bucket paths from clients.
- Persist object keys rather than permanent URLs.
- Use scoped signed URLs or an application/CDN resolver.
- Prevent path traversal, executable upload, oversized/decompression attacks, and unauthorized overwrite/delete.

## Logging and Audit

Structured application logs should carry request/trace ID, actor ID where safe, operation, status, latency, and typed error context.

Security-relevant mutations record WHO, WHAT, WHEN, RESOURCE, RESULT, IP, and REQUEST_ID. Never log passwords, access/refresh tokens, secrets, raw authorization headers, payment signatures, or unnecessary personal data.

## Security Tests

Cover:

- missing, expired, invalid, and revoked authentication;
- horizontal and vertical privilege escalation;
- cross-shop IDOR attempts;
- mass-assignment fields;
- duplicate/replayed/forged payment webhooks;
- rate-limit bypass across identities/instances where in scope;
- SQL injection inputs despite parameterized queries;
- token rotation and refresh-token reuse;
- unsafe upload type, size, path, and ownership.

## Repository Sources

- `docs/data-003a-identity.md`: Auth/User ownership and identity invariants.
- `docs/data-003b-seller-catalog.md`: Seller/Shop membership model.
- `nexus_commerce_golang_requirements.txt`: security threats, RBAC plus ownership, webhook security, rate limiting, audit, and logging targets.
