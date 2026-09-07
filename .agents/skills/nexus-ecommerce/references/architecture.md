# Nexus-Commerce Architecture

Use this reference for module placement, cross-module calls, checkout orchestration, events, and architecture decisions.

## Implemented State and Target

The repository currently contains a small Go 1.23 service using Chi, JSON `slog`, graceful shutdown, and `GET /health`. The domain modules and infrastructure below are the approved target, not evidence of implemented code.

Evolution path:

1. Modular monolith.
2. Event-driven modular monolith.
3. Selective microservices only when operational or scaling evidence justifies extraction.

Keep high cohesion, loose coupling, an acyclic dependency graph, and single data ownership throughout all stages.

## Modules and Ownership

| Module | Owns / is responsible for |
| --- | --- |
| Auth | credentials, password hashes, sessions, refresh tokens, authentication lifecycle |
| User | user profiles, addresses, preferences |
| Seller | seller accounts, shops, shop memberships, shop-scoped roles |
| Catalog | categories, brands, products, product variants, SKUs, current SKU price |
| Inventory | stock, reservations with expiry, stock movements |
| Cart | carts and cart items; any displayed price is only a cache |
| Order | orders, order items, status history, purchase snapshots, CheckoutService |
| Payment | payment transactions, provider/webhook handling, refunds |
| Voucher | vouchers, conditions, usage and usage limits |
| Notification | templates, delivery records/logs and asynchronous notifications |

Physical co-location in one PostgreSQL database does not relax logical ownership. A module may read or mutate another module only through an application interface or event contract approved for that boundary.

## Core Commerce Chain

```text
Shop
  -> Product
    -> ProductVariant
      -> SKU
        -> Inventory reservation
        -> Cart item
        -> Order item snapshot
        -> Payment transaction
```

`SKU` is the final sellable identity. Inventory, Cart, and Order integrations use `sku_id`. Product and Variant organize catalog information; neither owns stock.

## Allowed Collaboration

- Auth resolves the authenticated user identity.
- Seller relates a seller account to a User and owns shop membership.
- Catalog validates a Shop through the Seller boundary.
- Cart reads SKU display data through Catalog.
- Order's `CheckoutService` orchestrates Cart, Catalog, Voucher, Inventory, Order persistence, and Payment.
- Payment emits `PaymentSucceeded` or `PaymentFailed`; Order consumes the result and performs its own state transition.
- Notification consumes events from Order, Payment, Auth, and other producers as needed.

Do not place cross-module infrastructure calls inside the `Order` entity. The entity enforces its state machine; the application-layer CheckoutService coordinates other modules.

## Checkout Sequence

1. Accept selected `CartItemIDs`, optional `VoucherID`, and `AddressID`.
2. Load selected cart items.
3. Reload SKU status and current price from Catalog. Reject changed, inactive, or unavailable SKUs according to the API contract.
4. Validate and calculate the voucher through Voucher.
5. Reserve all required SKU quantities through Inventory with a 15-minute TTL.
6. Persist the Order and immutable item/address snapshots.
7. Clear only the purchased cart items after Order persistence succeeds.
8. Create the Payment transaction and return its URL or QR information.

Failure rules:

- If reservation fails, create no Order.
- If reservation succeeds but Order persistence fails, request immediate release. Reservation expiry is the crash-recovery safety net.
- A payment initialization or result failure must not leave stock reserved forever; handle release/expiry according to the finalized Order/Payment transition.
- Do not pretend that cross-module calls form one ACID transaction. Use local transactions, idempotency, compensation, and durable events.

## Events and Reliability

Start with RabbitMQ when asynchronous messaging is implemented. State changes that publish events use the transactional outbox:

```text
BEGIN
  mutate owner-module state
  insert outbox event
COMMIT
```

The outbox worker retries publishing. Consumers tolerate duplicate, delayed, retried, and out-of-order delivery and record processed messages with an inbox or equivalent idempotency mechanism. Never use `UPDATE database` followed by an assumed-infallible `broker.Publish()`.

## Historical Immutability

An Order must be reconstructable from its own snapshots without live Catalog, User, or Seller data. Keep source IDs for traceability, but snapshot at least product/variant/SKU labels and attributes, purchase price and currency, quantity, seller/shop identity needed for history, and shipping address.

## Decision Checklist

For a significant change, record:

- owning module and affected boundary;
- synchronous interface or asynchronous event contract;
- source of truth;
- transaction boundary;
- duplicate, retry, timeout, and crash behavior;
- extraction implications without prematurely extracting a service;
- invariant and the test that proves it.

## Repository Sources

- `notes/architect.md` is the detailed V2 module/checkout decision record.
- `nexus_commerce_golang_requirements.txt` defines the staged target and engineering roadmap.
- `cmd/api/main.go` and `internal/server/` show the currently implemented bootstrap.
