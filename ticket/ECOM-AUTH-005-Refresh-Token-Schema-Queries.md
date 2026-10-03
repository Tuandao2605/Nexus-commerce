# ECOM-AUTH-005 — Refresh Token Schema/Queries

Status: IN_PROGRESS — implementation is present; PostgreSQL migration/integration verification is pending because `TEST_DATABASE_URL` is not configured in this environment.

## Source Design

- `docs/data-003a-identity.md` — Identity ownership and session model
- `ECOM-AUTH-004` — Access-token boundary and deferred refresh lifecycle
- RFC 9700 §4.14 — Refresh-token replay and rotation security

## Goal

Add persistent storage and type-safe SQL operations for opaque refresh-token credentials. This ticket does not implement the HTTP refresh endpoint or rotation orchestration; those belong to AUTH-006.

## Decisions and Invariants

- A refresh token belongs to exactly one existing `sessions` row; session deletion is restricted while token history exists.
- Persist only the 32-byte SHA-256 digest of a cryptographically random opaque token; never persist the raw token.
- `previous_token_id` records rotation lineage and must point to a token in the same session.
- One token can have at most one successor; this retains the relationship needed for later replay handling.
- Expiry must be after creation; consumed/revoked timestamps cannot precede creation.
- Conditional consume succeeds only once, only before expiry, and only while the owning session remains live. Callers must use returned affected-row count as the compare-and-swap result.
- Token lifecycle remains separate from User/Session lifecycle; token queries do not independently decide account status or issue access tokens.

## Implementation

- `migrations/000009_auth_refresh_tokens.up.sql` creates the table, constraints, and lookup/cleanup indexes.
- `migrations/000009_auth_refresh_tokens.down.sql` drops only the table introduced by this migration.
- `sql/queries/auth_refresh_tokens.sql` defines digest lookup, insert, conditional consume, and session-wide revocation operations for sqlc.
- `internal/database/auth_refresh_token_migration_test.go` exercises valid lineage, session isolation, single-successor, digest uniqueness, and digest length.
- `internal/auth/postgres_refresh_token_queries_test.go` verifies the generated queries and concurrent one-winner consume against PostgreSQL.
- `internal/database/full_migration_review_test.go` includes the new table in the canonical V1 schema inventory.
- `Makefile` includes the refresh-query integration test in `test-auth-integration`.
- `docs/data-003a-identity.md` records the implemented refresh-token persistence contract.
- `docs/NEXUS-COMMERCE_V1_CANONICAL_ROADMAP.md` remains on AUTH-005 until required PostgreSQL verification passes.

## Acceptance Criteria

- [x] New sequential UP/DOWN migration follows repository transaction and rollback conventions.
- [x] Database rejects malformed/duplicate digests, cross-session lineage, and multiple successors.
- [x] sqlc generates typed operations and checks the SQL against the complete schema.
- [ ] PostgreSQL tests verify insertion, lookup, conditional consume, and session-wide revocation.
- [x] Canonical migration/schema inventory reflects the new table.
- [x] AUTH-006 remains responsible for rotation endpoint, transaction orchestration, replay response, and token issuance.

## Verification

- `make agent-preflight`: PASS
- `make sqlc-generate`: PASS
- `make sqlc-check`: PASS
- Focused Go test compile/run: PASS (integration cases may skip without `TEST_DATABASE_URL`)
- `make db-migration-status`, `make migrate-integration-cycle`, focused required-DB integration test: NOT RUN — `TEST_DATABASE_URL` is not configured; the first command exits with `TEST_DATABASE_URL is required`.
- `make verify-fast`: PASS
- `make verify-full`: NOT RUN — its required PostgreSQL integration phase cannot run without `TEST_DATABASE_URL`.
- `make diff-check`: PASS

## Result

IN_PROGRESS until the migration cycle and required PostgreSQL integration tests pass against the isolated test database.

## Next

ECOM-AUTH-006 — Refresh Rotation
