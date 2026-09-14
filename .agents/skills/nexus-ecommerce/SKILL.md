---
name: nexus-ecommerce
description: Apply Nexus-Commerce-specific domain rules, module boundaries, persistence conventions, and reliability invariants when implementing or reviewing this repository. Use for Catalog, Inventory, Cart, Order, Payment, Voucher, Auth, Seller, database, API, migration, security, or test work; do not use for unrelated generic Go or React tasks.
---

# Nexus Ecommerce

Build against the repository's implemented state while preserving its target architecture. The current code is a Go/Chi service with pgxpool, golang-migrate, and implemented Identity through Cart schemas. sqlc, Redis, messaging, and later domain modules remain planned. Never describe a planned component as already present.

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

For ticket order, verification, command safety, and diff review, follow the repository `AGENTS.md` and the matching short workflow. Keep this skill focused on Nexus-specific decisions.
