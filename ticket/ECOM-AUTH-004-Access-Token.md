# ECOM-AUTH-004 — Access Token

Status: DONE

## Source Design

- ECOM-AUTH-001 — Registration
- ECOM-AUTH-002 — Password Hashing
- ECOM-AUTH-003 — Login
- docs/data-003a-identity.md
- nexus_commerce_golang_requirements.txt
- RFC 7519 — JSON Web Token
- RFC 9068 — JWT Profile for OAuth 2.0 Access Tokens
- OWASP JSON Web Token Cheat Sheet

## Goal

Compose AUTH-003 email/password authentication with a public login HTTP route
that issues a short-lived signed access token. Define signing-key configuration,
token claims, validation, error behavior, and the API contract.

## Security Decisions

- JWT signed with RS256 and RSA private key of at least 2048 bits
- JWT type is at+jwt; algorithm is pinned to RS256 by the verifier
- Required claims: iss, sub, aud, client_id, iat, exp, jti
- Token lifetime defaults to and is capped at 15 minutes
- Issuer, audience, client ID, key ID, lifetime, and private-key file are configurable
- The private key is loaded from a PEM file and never checked into the repository
- PKCS#1 and PKCS#8 unencrypted RSA private keys are accepted
- Resource-side validation can be built from a public RSA key only
- Validate signature, type, algorithm, key ID, issuer, audience, client ID, issue time, expiry, and maximum lifetime
- Token carries no email, password, or mutable role/permission claims
- Login returns one generic 401 error for invalid credentials or inactive accounts
- Token responses set Cache-Control: no-store and Pragma: no-cache
- Request body is bounded, requires JSON, rejects unknown fields and trailing documents
- No refresh token, persistent session, or revocation list is created by this ticket
- Do not expose the public login route to production traffic before distributed login rate limiting is implemented

## API Contract

POST /auth/login
Content-Type: application/json

Request fields:

- email
- password

Success is 200 OK:

    {
      "access_token": "<signed JWT>",
      "token_type": "Bearer",
      "expires_in": 900
    }

See docs/api.md for the stable error codes and complete contract.

## Implementation

Files:

- internal/auth/access_token.go implements RS256 signing, public-key validation, claim checks, and lifetime limits
- internal/auth/login_token.go composes AUTH-003 authentication with token issuance
- internal/auth/login_http.go implements bounded JSON login and safe bearer-token/error responses
- internal/config/config.go loads and validates token settings
- internal/config/config_test.go tests required signing-key path, defaults, and duration bounds
- cmd/api/main.go wires login repository, User status reader, token issuer, and HTTP handler
- internal/server/routes.go registers POST /auth/login
- internal/server/server_test.go tests route delegation
- internal/auth/access_token_test.go tests signature, claims, malformed tokens, policy limits, and public-key-only validation
- internal/auth/login_http_test.go tests HTTP success, safe errors, and request constraints
- .env.example documents token settings and local RSA key generation without committing key material
- docs/api.md documents the login request/response/error contract
- .agents/skills/nexus-ecommerce/ documents implemented Auth/token behavior for later tickets
- ticket/ECOM-AUTH-004-Access-Token.md records the decision and verification evidence
- docs/NEXUS-COMMERCE_V1_CANONICAL_ROADMAP.md moves the current ticket to AUTH-005 after verification

No migration is added. AUTH-004 does not persist access tokens.

## Acceptance Criteria

- [x] A valid active account receives a signed access token
- [x] Login response uses Bearer token type and reports expiry in seconds
- [x] Invalid credentials return a generic 401 without internal details
- [x] Malformed, oversized, unknown-field, and trailing JSON requests are rejected
- [x] Signing algorithm is fixed to RS256; none and other algorithms are rejected
- [x] Token type, key ID, issuer, audience, client ID, subject, JTI, issue time, and expiry are checked
- [x] Token lifetime is bounded to 1–15 minutes
- [x] RSA keys smaller than 2048 bits are rejected
- [x] Public-key verifier works without private signing key
- [x] Token does not disclose email, password, roles, or permissions
- [x] Signing key path is required and no private key is committed
- [x] No database migration or session/access-token persistence is introduced
- [x] Login rate limiting and refresh/session lifecycle are documented as future scope

## Verification

- Focused Auth/config/server/API tests: PASS
- Access-token signature and claims tests: PASS
- Login HTTP error and body-bound tests: PASS
- Auth PostgreSQL integration tests: PASS
- Full Auth race tests: PASS
- make verify-fast: PASS
- make verify-full: PASS
- Migration cycle: PASS — 008 → 0 → 008
- schema_migrations: version 8, clean
- Function/file comment audit: PASS
- git diff --check: PASS

## Result

PASS

## Next

ECOM-AUTH-005 — Refresh Token Schema/Queries
