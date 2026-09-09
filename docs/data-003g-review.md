# ECOM-DATA-003G — Full ERD Review

> Status: **COMPLETED — PASS (DESIGN)**  
> Consolidated result: [`docs/erd.md`](./erd.md)

DATA-003G là review/consolidation ticket, không mở rộng feature scope. Review đã
ghép DATA-003A → DATA-003F và kiểm tra các invariant xuyên module:

- Shop/SKU tenant consistency;
- User ownership và Auth/User ownership boundary;
- Cart → Checkout → Order correlation, gồm partial checkout;
- Parent/Seller Order references;
- Voucher/Payment/Inventory correlation;
- single-currency checkout hierarchy;
- money representation, idempotency và TTL;
- `ON DELETE` policy và historical snapshots;
- status ownership, lifecycle timestamps và critical indexes.

Các finding ban đầu đã được sửa trực tiếp trong DATA documents và được ghi thành
resolution trong `docs/erd.md`. Kết luận này là **design pass**; repository chưa
có PostgreSQL migrations, sqlc repositories hay implementation cho các domain.

## Source-of-Truth Summary

| Concern | Owner |
|---|---|
| Authentication identity | Auth |
| User profile/address | User |
| Shop ownership/membership | Seller |
| Product/current SKU price | Catalog |
| Current stock/holds | Inventory |
| Purchase intent | Cart |
| Commercial transaction | Order |
| Voucher quota/redemption | Voucher |
| Payment/refund state | Payment |

Next implementation sequence:

```text
PostgreSQL migrations
→ sqlc schema/queries
→ integration tests
→ concurrency tests
```
