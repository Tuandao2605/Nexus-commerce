---
name: nexus-ecommerce
description: Apply Nexus-Commerce-specific domain rules, module boundaries, persistence conventions, and reliability invariants when implementing or reviewing this repository. Use for Catalog, Inventory, Cart, Order, Payment, Voucher, Auth, Seller, database, API, migration, security, or test work; do not use for unrelated generic Go or React tasks.
---

# Nexus Ecommerce

Build against the repository's implemented state while preserving its target architecture. The current code is an early Go/Chi bootstrap; PostgreSQL, sqlc, migrations, Redis, messaging, and most domain modules are planned but not yet implemented. Never describe a planned component as already present.

The user's request takes precedence over this skill. For repository facts, prefer executable contracts in code, tests, and migrations over prose. When prose documents disagree or leave a contract open, call out the mismatch and resolve it in the task's design or ADR instead of silently choosing.

## Read the Relevant Context

- Read [references/architecture.md](references/architecture.md) for module ownership, allowed dependencies, checkout orchestration, events, or service extraction.
- Read [references/database.md](references/database.md) for schemas, SQL, transactions, concurrency control, sqlc, indexes, or migrations.
- Read [references/api-conventions.md](references/api-conventions.md) for handlers, JSON contracts, errors, idempotency, pagination, or webhooks.
- Read [references/security.md](references/security.md) for identity, RBAC, ownership checks, tokens, rate limits, uploads, or sensitive data.
- Read [references/business-rules.md](references/business-rules.md) for Product/Variant/SKU, inventory, cart, vouchers, checkout, orders, payments, and their tests.

Read only the references needed for the current task. Consult the linked repository source documents when exact columns, constraints, or long-form rationale matter.

## Non-Negotiable Decisions

- Keep a modular monolith until measured needs justify extracting services.
- Every table/entity has exactly one owning module. Other modules use application interfaces or domain events; they do not mutate another module's tables.
- The sellable chain is `Product -> ProductVariant -> SKU -> Inventory/Cart -> Order -> Payment`. Inventory and Cart use `sku_id`, not `product_id`.
- Catalog owns current SKU price. Checkout reloads it. Order owns the immutable purchase snapshot.
- Inventory owns stock and reservations. Database concurrency control must prevent overselling across multiple API instances.
- Only Order may change `Order.status`. Payment reports results through an application boundary/domain event and never updates orders directly.
- Critical writes and payment/event consumers are idempotent. Transactional state plus its outbox event commit atomically.
- Authorization combines role/permission checks with resource and shop ownership checks.

## Work Method

1. Identify the owning module and the invariants affected before editing.
2. Trace success, retry, duplicate, timeout, cancellation, and crash paths for any critical write.
3. Put semantic validation in the application layer and correctness/concurrency invariants in PostgreSQL constraints or transactional SQL.
4. Keep handlers thin: decode and validate, authenticate/authorize, call the application service, and encode the stable response/error contract.
5. Propagate `context.Context` from HTTP through service and repository calls; do not replace request context with `context.Background()`.
6. Implement the smallest coherent vertical slice and avoid speculative abstractions, interfaces for every struct, premature microservices, or incidental goroutines.
7. Update the relevant contract, migration/schema notes, and tests when a business decision changes.

## Verification

Run `go test ./...` for every Go change. Add the narrowest relevant tests:

- unit tests for validation, calculations, and state transitions;
- repository/integration tests with PostgreSQL, preferably `testcontainers-go`, for constraints, transactions, locks, and sqlc queries;
- HTTP tests for status, response/error envelope, authentication, authorization, and idempotency;
- concurrency tests for stock and voucher invariants;
- duplicate/retry/failure tests for payment webhooks, events, outbox workers, and compensation;
- k6 or targeted load tests only when performance or capacity is in scope.

Do not mark critical commerce work complete unless its business invariant is observable in tests, not merely covered by mocks.
