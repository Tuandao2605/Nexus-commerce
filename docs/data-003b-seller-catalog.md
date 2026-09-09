# ECOM-DATA-003B — Seller & Catalog Domain Model

> Status: FINAL DESIGN — reconciled with DATA-003G remediation

## 1. Scope

DATA-003B thiết kế tám bảng:

```text
SELLER MODULE
├── seller_accounts
├── shops
└── shop_memberships

CATALOG MODULE
├── categories
├── brands
├── products
├── product_variants
└── skus
```

Mục tiêu:

```text
User
  │
  ▼
SellerAccount
  │
  │ many-to-many
  ▼
Shop
  │
  ▼
Product
  │
  ▼
ProductVariant
  │
  ▼
SKU
```

Thiết kế phải hỗ trợ:

- một seller quản lý nhiều shop;
- một shop có nhiều seller/member;
- shop-scoped authorization;
- product moderation;
- category hierarchy;
- Product → Variant → SKU;
- SKU-level price;
- SKU code unique trong phạm vi shop;
- Catalog không chứa inventory quantity;
- archive Product/SKU thay vì destructive delete;
- shipping metadata;
- object-storage references cho media;
- tương thích với checkout, inventory và order ở các ticket sau.

---

# 2. Global Database Conventions

## 2.1 ID

Primary key sử dụng:

```text
PostgreSQL type: UUID
Generation strategy: UUIDv7
```

UUIDv7 được generate ở Go application.

Lý do:

- không phụ thuộc sequence của một PostgreSQL instance;
- phù hợp với roadmap selective microservices;
- khó đoán hơn sequential BIGINT;
- locality tốt hơn UUIDv4.

---

## 2.2 Timestamp

Tất cả thời gian tuyệt đối sử dụng:

```sql
TIMESTAMPTZ
```

Convention:

```text
created_at
updated_at
published_at
archived_at
suspended_at
closed_at
invited_at
accepted_at
revoked_at
```

`created_at` và `updated_at` mặc định:

```sql
now()
```

`updated_at` được cập nhật explicit trong SQL UPDATE.

Không sử dụng trigger tự động ở V1.

---

## 2.3 Status

V1 sử dụng:

```text
VARCHAR + CHECK
```

thay vì PostgreSQL ENUM.

Lý do:

- lifecycle còn có thể thay đổi;
- migration đơn giản hơn;
- giảm coupling giữa database enum và application state machine.

---

## 2.4 Media/Object Storage

Các bảng không lưu permanent public URL trực tiếp.

Thay vì:

```text
logo_url
image_url
banner_url
```

sử dụng:

```text
logo_object_key
image_object_key
banner_object_key
```

Ví dụ:

```text
shops/{shop_id}/logo.webp
products/{product_id}/main.webp
```

Application/CDN chịu trách nhiệm resolve object key thành URL.

Điều này tránh coupling database với:

```text
MinIO
S3
CloudFront/CDN
bucket domain
signed URL
```

---

# 3. Multi-Vendor Ownership Model

## 3.1 SellerAccount → Shop

Một SellerAccount có thể tham gia nhiều Shop.

```text
Seller A
├── Electronics Shop
├── Gaming Shop
└── Accessories Shop
```

Cardinality:

```text
SellerAccount 1 ───── *
ShopMembership
```

---

## 3.2 Shop → SellerAccount

Một Shop có thể có nhiều seller/member.

```text
Shop
├── Owner
├── Admin
├── Catalog Manager
└── Order Manager
```

Do đó:

```text
SellerAccount * ───── * Shop
```

được biểu diễn thông qua:

```text
shop_memberships
```

Không đặt:

```text
shops.seller_account_id
```

vì field đó sẽ ép quan hệ thành one-to-many.

---

# 4. TABLE: seller_accounts

**Owner:** Seller Module

## Purpose

Đại diện cho business identity của một user khi hoạt động với vai trò Seller.

```text
users
    → identity/profile

seller_accounts
    → seller capability/lifecycle
```

Một user có tối đa một SellerAccount trong V1.

---

## Columns

| Column         | Type          | Null | Default     | Constraint | FK          | Reason                 |
| -------------- | ------------- | ---: | ----------- | ---------- | ----------- | ---------------------- |
| `id`           | `UUID`        |   NO | —           | PK         | —           | Seller identity        |
| `user_id`      | `UUID`        |   NO | —           | UNIQUE     | `users(id)` | Seller belongs to user |
| `status`       | `VARCHAR(30)` |   NO | `'pending'` | CHECK      | —           | Seller lifecycle       |
| `created_at`   | `TIMESTAMPTZ` |   NO | `now()`     | —          | —           | Creation               |
| `updated_at`   | `TIMESTAMPTZ` |   NO | `now()`     | CHECK      | —           | Last mutation          |
| `suspended_at` | `TIMESTAMPTZ` |  YES | NULL        | CHECK      | —           | Current suspension     |
| `closed_at`    | `TIMESTAMPTZ` |  YES | NULL        | CHECK      | —           | Account closure        |

---

## Primary Key

```sql
PRIMARY KEY (id)
```

---

## Foreign Keys

```sql
FOREIGN KEY (user_id)
REFERENCES users(id)
ON DELETE RESTRICT
```

---

## Unique Constraints

```sql
UNIQUE (user_id)
```

Invariant:

```text
User 1 ───── 0..1 SellerAccount
```

---

## CHECK Constraints

```sql
CHECK (
    status IN (
        'pending',
        'active',
        'suspended',
        'closed'
    )
)
```

```sql
CHECK (updated_at >= created_at)
```

```sql
CHECK (
    (status = 'suspended' AND suspended_at IS NOT NULL)
    OR
    (status <> 'suspended' AND suspended_at IS NULL)
)
```

```sql
CHECK (
    (status = 'closed' AND closed_at IS NOT NULL)
    OR
    (status <> 'closed' AND closed_at IS NULL)
)
```

---

## Indexes

```sql
CREATE INDEX idx_seller_accounts_status
ON seller_accounts(status);
```

`UNIQUE(user_id)` đã tạo index phục vụ lookup SellerAccount từ User.

---

## ON DELETE

```text
users → seller_accounts
ON DELETE RESTRICT
```

Không physical delete SellerAccount sau khi đã tham gia business operation.

Closure:

```text
status = 'closed'
closed_at = now()
```

---

## Reasoning

Không sử dụng:

```text
users.is_seller
```

vì Seller có lifecycle riêng và tương lai có thể mở rộng:

```text
KYC
seller verification
risk status
payout configuration
business information
```

---

# 5. TABLE: shops

**Owner:** Seller Module

## Purpose

Đại diện cho storefront và tenant boundary của Catalog.

Product, Variant và SKU cuối cùng đều thuộc về một Shop.

---

## Columns

| Column              | Type           | Null | Default   | Constraint | FK  | Reason                          |
| ------------------- | -------------- | ---: | --------- | ---------- | --- | ------------------------------- |
| `id`                | `UUID`         |   NO | —         | PK         | —   | Shop identity                   |
| `name`              | `VARCHAR(160)` |   NO | —         | CHECK      | —   | Shop display name               |
| `slug`              | `VARCHAR(160)` |   NO | —         | UNIQUE     | —   | Public identifier               |
| `description`       | `TEXT`         |  YES | NULL      | —          | —   | Storefront description          |
| `logo_object_key`   | `VARCHAR(512)` |  YES | NULL      | —          | —   | Shop logo                       |
| `banner_object_key` | `VARCHAR(512)` |  YES | NULL      | —          | —   | Shop banner                     |
| `phone`             | `VARCHAR(32)`  |  YES | NULL      | CHECK      | —   | Operational contact             |
| `email`             | `VARCHAR(254)` |  YES | NULL      | CHECK      | —   | Support/operation email         |
| `is_official`       | `BOOLEAN`      |   NO | `false`   | —          | —   | Platform verified/official flag |
| `currency_code`     | `CHAR(3)`      |   NO | `'VND'`   | CHECK      | —   | Shop pricing currency           |
| `status`            | `VARCHAR(20)`  |   NO | `'draft'` | CHECK      | —   | Shop lifecycle                  |
| `created_at`        | `TIMESTAMPTZ`  |   NO | `now()`   | —          | —   | Creation                        |
| `updated_at`        | `TIMESTAMPTZ`  |   NO | `now()`   | CHECK      | —   | Last mutation                   |
| `closed_at`         | `TIMESTAMPTZ`  |  YES | NULL      | CHECK      | —   | Closure                         |

---

## Primary Key

```sql
PRIMARY KEY (id)
```

---

## Unique Constraints

```sql
UNIQUE (slug)
```

Shop URL phải resolve tới duy nhất một shop.

Ngoài ra:

```sql
UNIQUE (id, currency_code)
```

Composite unique này cho phép SKU dùng foreign key để enforce:

```text
SKU currency == Shop currency
```

---

## CHECK Constraints

```sql
CHECK (
    char_length(btrim(name)) BETWEEN 1 AND 160
)
```

```sql
CHECK (
    slug = lower(btrim(slug))
    AND slug <> ''
)
```

```sql
CHECK ( email IS NULL
        OR (
             email = lower(btrim(email))
            AND char_length(email) BETWEEN 3 AND 254
            )
     )
```

```sql
CHECK ( phone IS NULL OR char_length(btrim(phone)) > 0 )
```

```sql
CHECK (
    currency_code = upper(currency_code)
    AND char_length(currency_code) = 3
)
```

```sql
CHECK (
    status IN (
        'draft',
        'active',
        'suspended',
        'closed'
    )
)
```

```sql
CHECK (updated_at >= created_at)
```

```sql
CHECK (
    (status = 'closed' AND closed_at IS NOT NULL)
    OR
    (status <> 'closed' AND closed_at IS NULL)
)
```

---

## Indexes

```sql
CREATE INDEX idx_shops_status
ON shops(status);
```

`UNIQUE(slug)` đã tạo index cho public lookup.

---

## `is_official`

`is_official` là platform-controlled field.

Seller không được trực tiếp:

```text
PATCH shop
is_official = true
```

Chỉ Admin/Moderation workflow được thay đổi field này.

---

## Không thêm rating vào V1

Không thêm:

```text
rating_avg
rating_count
```

ở source-of-truth `shops`.

Rating là derived data từ Review Module.

Sau này có thể materialize thành:

```text
shop_metrics
```

hoặc search/read projection.

---

## ON DELETE

Shop đã hoạt động không physical delete.

Lifecycle:

```text
draft
  ↓
active
  ↓
suspended

active
  ↓
closed
```

Business history phải tiếp tục tồn tại.

---

# 6. TABLE: shop_memberships

**Owner:** Seller Module

## Purpose

Mapping SellerAccount vào Shop và lưu shop-scoped authorization.

Đây là source of truth cho quyền Seller trên Shop.

---

## Columns

| Column                         | Type          | Null | Default     | Constraint  | FK                    | Reason               |
| ------------------------------ | ------------- | ---: | ----------- | ----------- | --------------------- | -------------------- |
| `id`                           | `UUID`        |   NO | —           | PK          | —                     | Membership identity  |
| `shop_id`                      | `UUID`        |   NO | —           | UNIQUE pair | `shops(id)`           | Shop                 |
| `seller_account_id`            | `UUID`        |   NO | —           | UNIQUE pair | `seller_accounts(id)` | Member               |
| `created_by_seller_account_id` | `UUID`        |  YES | NULL        | —           | `seller_accounts(id)` | Invite/audit actor   |
| `role`                         | `VARCHAR(30)` |   NO | —           | CHECK       | —                     | Shop-scoped role     |
| `status`                       | `VARCHAR(20)` |   NO | `'pending'` | CHECK       | —                     | Membership lifecycle |
| `invited_at`                   | `TIMESTAMPTZ` |  YES | NULL        | CHECK       | —                     | Invitation time      |
| `accepted_at`                  | `TIMESTAMPTZ` |  YES | NULL        | CHECK       | —                     | Acceptance time      |
| `revoked_at`                   | `TIMESTAMPTZ` |  YES | NULL        | CHECK       | —                     | Revocation           |
| `created_at`                   | `TIMESTAMPTZ` |   NO | `now()`     | —           | —                     | Record creation      |
| `updated_at`                   | `TIMESTAMPTZ` |   NO | `now()`     | CHECK       | —                     | Mutation             |

---

## Primary Key

```sql
PRIMARY KEY (id)
```

---

## Foreign Keys

```sql
FOREIGN KEY (shop_id)
REFERENCES shops(id)
ON DELETE RESTRICT
```

```sql
FOREIGN KEY (seller_account_id)
REFERENCES seller_accounts(id)
ON DELETE RESTRICT
```

```sql
FOREIGN KEY (created_by_seller_account_id)
REFERENCES seller_accounts(id)
ON DELETE RESTRICT
```

`created_by_seller_account_id` nullable vì membership đầu tiên có thể được tạo bởi:

```text
shop creation workflow
admin intervention
system migration
```

---

## Unique Constraints

```sql
UNIQUE (shop_id, seller_account_id)
```

Một SellerAccount không có nhiều membership song song trong cùng Shop.

---

## Roles

```sql
CHECK (
    role IN (
        'owner',
        'admin',
        'catalog_manager',
        'order_manager',
        'viewer'
    )
)
```

Đây là shop-scoped role.

Ví dụ:

```text
Seller A
├── Shop X → owner
└── Shop Y → catalog_manager
```

Không thay thế global RBAC.

---

## Status

```sql
CHECK (
    status IN (
        'pending',
        'active',
        'suspended',
        'revoked'
    )
)
```

Invite lifecycle:

```text
pending
   │
   ▼
active
   │
   ├──► suspended
   │
   └──► revoked
```

---

## Timestamp Constraints

```sql
CHECK (
    invited_at IS NULL
    OR invited_at >= created_at
)
```

```sql
CHECK (
    accepted_at IS NULL
    OR accepted_at >= COALESCE(invited_at, created_at)
)
```

```sql
CHECK (
    status <> 'pending'
    OR accepted_at IS NULL
)
```

```sql
CHECK (
    status NOT IN ('active', 'suspended', 'revoked')
    OR accepted_at IS NOT NULL
)
```

```sql
CHECK (
    (status = 'revoked' AND revoked_at IS NOT NULL)
    OR
    (status <> 'revoked' AND revoked_at IS NULL)
)
```

```sql
CHECK (
    revoked_at IS NULL
    OR revoked_at >= accepted_at
)
```

```sql
CHECK (updated_at >= created_at)
```

---

## One Active Owner Invariant

Một Shop có tối đa một active owner:

```sql
CREATE UNIQUE INDEX uq_shop_memberships_active_owner
ON shop_memberships(shop_id)
WHERE role = 'owner'
  AND status = 'active';
```

Hai transaction concurrent không thể cùng tạo hai active owner.

---

## Indexes

Seller → Shops:

```sql
CREATE INDEX idx_shop_memberships_seller_status
ON shop_memberships(seller_account_id, status);
```

Authorization:

```sql
CREATE INDEX idx_shop_memberships_shop_seller_active
ON shop_memberships(shop_id, seller_account_id)
WHERE status = 'active';
```

Pending invites:

```sql
CREATE INDEX idx_shop_memberships_pending
ON shop_memberships(seller_account_id)
WHERE status = 'pending';
```

---

## Authorization Example

Request:

```text
PATCH /shops/{shopId}/products/{productId}
```

Authorization:

```sql
SELECT 1
FROM shop_memberships
WHERE shop_id = $1
  AND seller_account_id = $2
  AND status = 'active'
  AND role IN ('owner', 'admin', 'catalog_manager');
```

Resource lookup vẫn phải scope theo Shop:

```sql
SELECT ...
FROM products
WHERE id = $1
  AND shop_id = $2;
```

Không chỉ kiểm tra:

```text
user has SELLER role
```

---

## Owner Creation

Partial unique index chỉ enforce:

```text
at most one active owner
```

không enforce:

```text
at least one owner
```

Shop creation phải transactionally:

```text
BEGIN

INSERT INTO shops ...

INSERT INTO shop_memberships (
    shop_id,
    seller_account_id,
    role,
    status,
    accepted_at
)
VALUES (
    ...,
    'owner',
    'active',
    now()
);

COMMIT;
```

Owner transfer/revocation cũng phải chạy trong một transaction và lock Shop cùng
membership owner hiện tại. Successor phải được activate trước khi owner cũ bị
demote/revoke; transaction rollback toàn bộ nếu không thể duy trì một active
owner. Partial unique index bảo vệ **at most one** active owner, còn transaction
workflow bảo vệ **at least one** active owner cho mọi Shop chưa đóng.

Mọi mutation membership phải authorize theo chính `shop_id` trong transaction;
role Seller toàn cục không thay thế kiểm tra membership của Shop đích.

---

# 7. TABLE: categories

**Owner:** Catalog Module

## Purpose

Global product taxonomy.

Ví dụ:

```text
Electronics
└── Phones
    ├── Android
    └── iPhone
```

V1 sử dụng:

```text
Adjacency List
```

thông qua `parent_id`.

---

## Columns

| Column             | Type           | Null | Default    | Constraint    | FK               | Reason                |
| ------------------ | -------------- | ---: | ---------- | ------------- | ---------------- | --------------------- |
| `id`               | `UUID`         |   NO | —          | PK            | —                | Category identity     |
| `parent_id`        | `UUID`         |  YES | NULL       | CHECK         | `categories(id)` | Hierarchy             |
| `name`             | `VARCHAR(160)` |   NO | —          | CHECK         | —                | Display name          |
| `slug`             | `VARCHAR(160)` |   NO | —          | Scoped UNIQUE | —                | URL/filter identifier |
| `icon_object_key`  | `VARCHAR(512)` |  YES | NULL       | —             | —                | Category icon         |
| `image_object_key` | `VARCHAR(512)` |  YES | NULL       | —             | —                | Category image        |
| `status`           | `VARCHAR(20)`  |   NO | `'active'` | CHECK         | —                | Lifecycle             |
| `sort_order`       | `INTEGER`      |   NO | `0`        | CHECK         | —                | UI ordering           |
| `created_at`       | `TIMESTAMPTZ`  |   NO | `now()`    | —             | —                | Creation              |
| `updated_at`       | `TIMESTAMPTZ`  |   NO | `now()`    | CHECK         | —                | Mutation              |

---

## Primary Key

```sql
PRIMARY KEY (id)
```

---

## Foreign Key

```sql
FOREIGN KEY (parent_id)
REFERENCES categories(id)
ON DELETE RESTRICT
```

Không xóa parent khi vẫn còn child.

---

## CHECK Constraints

```sql
CHECK (
    parent_id IS NULL
    OR parent_id <> id
)
```

```sql
CHECK (
    char_length(btrim(name)) BETWEEN 1 AND 160
)
```

```sql
CHECK (
    slug = lower(btrim(slug))
    AND slug <> ''
)
```

```sql
CHECK (
    status IN (
        'active',
        'inactive'
    )
)
```

```sql
CHECK (sort_order >= 0)
```

```sql
CHECK (updated_at >= created_at)
```

---

## Unique Constraints

Root categories:

```sql
CREATE UNIQUE INDEX uq_categories_root_slug
ON categories(slug)
WHERE parent_id IS NULL;
```

Sibling categories:

```sql
CREATE UNIQUE INDEX uq_categories_parent_slug
ON categories(parent_id, slug)
WHERE parent_id IS NOT NULL;
```

Do đó:

```text
Electronics
└── Accessories

Fashion
└── Accessories
```

được phép.

---

## Indexes

```sql
CREATE INDEX idx_categories_parent
ON categories(parent_id);
```

```sql
CREATE INDEX idx_categories_status
ON categories(status);
```

---

## Không lưu `level`

Không thêm:

```text
level SMALLINT
```

vì level có thể derive từ hierarchy.

Nếu move một subtree:

```text
A
└── B
    └── C
```

thì `level` của B, C và toàn descendants phải update.

Điều này tạo duplicated source of truth:

```text
parent relationship
+
stored depth
```

V1 ưu tiên consistency.

Recursive CTE được sử dụng khi cần.

---

## Không lưu `is_leaf`

`is_leaf` được suy ra từ:

```sql
NOT EXISTS (
    SELECT 1
    FROM categories child
    WHERE child.parent_id = categories.id
)
```

Không tạo boolean dễ stale.

Nếu tương lai cần business rule:

```text
category có cho phép gắn product trực tiếp hay không
```

nên tạo field có semantic riêng như:

```text
allow_products
```

chứ không overload `is_leaf`.

---

## Cycle Limitation

Constraint:

```text
parent_id <> id
```

chỉ chặn:

```text
A → A
```

Không tự chặn:

```text
A → B → C → A
```

Application phải kiểm tra ancestor graph khi move category. Việc kiểm tra và
`UPDATE parent_id` phải nằm trong cùng transaction, đồng thời serialize các move
có thể giao nhau bằng row/advisory lock theo category root. Không dùng flow
`read ancestors → commit → update` vì hai move concurrent có thể cùng vượt qua
validation rồi tạo cycle.

---

## ON DELETE

Category đang được sử dụng không physical delete.

Dùng:

```text
status = inactive
```

Products reference Category bằng:

```text
ON DELETE RESTRICT
```

---

# 8. TABLE: brands

**Owner:** Catalog Module

## Purpose

Global normalized Brand entity.

Ví dụ:

```text
Nike
Apple
Samsung
Logitech
```

---

## Columns

| Column            | Type           | Null | Default    | Constraint                | FK  | Reason                |
| ----------------- | -------------- | ---: | ---------- | ------------------------- | --- | --------------------- |
| `id`              | `UUID`         |   NO | —          | PK                        | —   | Brand identity        |
| `name`            | `VARCHAR(160)` |   NO | —          | CHECK + unique normalized | —   | Brand name            |
| `slug`            | `VARCHAR(160)` |   NO | —          | UNIQUE                    | —   | URL/filter identifier |
| `description`     | `TEXT`         |  YES | NULL       | —                         | —   | Brand description     |
| `logo_object_key` | `VARCHAR(512)` |  YES | NULL       | —                         | —   | Brand logo            |
| `status`          | `VARCHAR(20)`  |   NO | `'active'` | CHECK                     | —   | Lifecycle             |
| `created_at`      | `TIMESTAMPTZ`  |   NO | `now()`    | —                         | —   | Creation              |
| `updated_at`      | `TIMESTAMPTZ`  |   NO | `now()`    | CHECK                     | —   | Mutation              |

---

## Primary Key

```sql
PRIMARY KEY (id)
```

---

## Unique Constraints

```sql
UNIQUE (slug)
```

Normalized name uniqueness:

```sql
CREATE UNIQUE INDEX uq_brands_name_ci
ON brands(lower(btrim(name)));
```

Do đó:

```text
Nike
nike
NIKE
```

không trở thành ba Brand khác nhau.

---

## CHECK Constraints

```sql
CHECK (
    char_length(btrim(name)) BETWEEN 1 AND 160
)
```

```sql
CHECK (
    slug = lower(btrim(slug))
    AND slug <> ''
)
```

```sql
CHECK (
    status IN (
        'active',
        'inactive'
    )
)
```

```sql
CHECK (updated_at >= created_at)
```

---

## ON DELETE

Brand đang được Product tham chiếu:

```text
ON DELETE RESTRICT
```

Normal lifecycle:

```text
status = inactive
```

---

# 9. TABLE: products

**Owner:** Catalog Module

## Purpose

Đại diện cho product listing/concept thuộc một Shop.

Ví dụ:

```text
Product:
Nike Air Max 2026
```

Product không phải đơn vị inventory cuối cùng.

---

## Columns

| Column                  | Type           | Null | Default   | Constraint       | FK               | Reason                      |
| ----------------------- | -------------- | ---: | --------- | ---------------- | ---------------- | --------------------------- |
| `id`                    | `UUID`         |   NO | —         | PK               | —                | Product identity            |
| `shop_id`               | `UUID`         |   NO | —         | composite UNIQUE | `shops(id)`      | Tenant ownership            |
| `category_id`           | `UUID`         |   NO | —         | —                | `categories(id)` | Taxonomy                    |
| `brand_id`              | `UUID`         |  YES | NULL      | —                | `brands(id)`     | Brand                       |
| `name`                  | `VARCHAR(255)` |   NO | —         | CHECK            | —                | Product name                |
| `slug`                  | `VARCHAR(255)` |   NO | —         | UNIQUE per shop  | —                | Public product identifier   |
| `description`           | `TEXT`         |  YES | NULL      | —                | —                | Description                 |
| `main_image_object_key` | `VARCHAR(512)` |  YES | NULL      | —                | —                | Main/thumbnail image        |
| `weight_g`              | `INTEGER`      |  YES | NULL      | CHECK            | —                | Default shipping weight     |
| `package_length_mm`     | `INTEGER`      |  YES | NULL      | CHECK            | —                | Default package length      |
| `package_width_mm`      | `INTEGER`      |  YES | NULL      | CHECK            | —                | Default package width       |
| `package_height_mm`     | `INTEGER`      |  YES | NULL      | CHECK            | —                | Default package height      |
| `status`                | `VARCHAR(30)`  |   NO | `'draft'` | CHECK            | —                | Product lifecycle           |
| `rejection_reason`      | `VARCHAR(512)` |  YES | NULL      | CHECK            | —                | Moderation rejection reason |
| `created_at`            | `TIMESTAMPTZ`  |   NO | `now()`   | —                | —                | Creation                    |
| `updated_at`            | `TIMESTAMPTZ`  |   NO | `now()`   | CHECK            | —                | Mutation                    |
| `published_at`          | `TIMESTAMPTZ`  |  YES | NULL      | CHECK            | —                | First publication           |
| `archived_at`           | `TIMESTAMPTZ`  |  YES | NULL      | CHECK            | —                | Archival                    |

---

## Primary Key

```sql
PRIMARY KEY (id)
```

---

## Foreign Keys

```sql
FOREIGN KEY (shop_id)
REFERENCES shops(id)
ON DELETE RESTRICT
```

```sql
FOREIGN KEY (category_id)
REFERENCES categories(id)
ON DELETE RESTRICT
```

```sql
FOREIGN KEY (brand_id)
REFERENCES brands(id)
ON DELETE RESTRICT
```

---

## Unique Constraints

Product slug unique trong Shop:

```sql
UNIQUE (shop_id, slug)
```

Để Variant enforce tenant consistency:

```sql
UNIQUE (id, shop_id)
```

---

## CHECK Constraints

```sql
CHECK (
    char_length(btrim(name)) BETWEEN 1 AND 255
)
```

```sql
CHECK (
    slug = lower(btrim(slug))
    AND slug <> ''
)
```

Shipping:

```sql
CHECK (
    weight_g IS NULL
    OR weight_g > 0
)
```

```sql
CHECK (
    package_length_mm IS NULL
    OR package_length_mm > 0
)
```

```sql
CHECK (
    package_width_mm IS NULL
    OR package_width_mm > 0
)
```

```sql
CHECK (
    package_height_mm IS NULL
    OR package_height_mm > 0
)
```

Lifecycle:

```sql
CHECK (
    status IN (
        'draft',
        'pending_review',
        'active',
        'rejected',
        'inactive',
        'archived'
    )
)
```

Moderation:

```sql
CHECK (
    (status = 'rejected' AND rejection_reason IS NOT NULL)
    OR
    (status <> 'rejected' AND rejection_reason IS NULL)
)
```

Archive:

```sql
CHECK (
    (status = 'archived' AND archived_at IS NOT NULL)
    OR
    (status <> 'archived' AND archived_at IS NULL)
)
```

Publication:

```sql
CHECK (
    status NOT IN ('active', 'inactive')
    OR published_at IS NOT NULL
)
```

Timestamp:

```sql
CHECK (updated_at >= created_at)
```

---

## Main Image

`main_image_object_key` nullable vì Draft Product có thể được tạo trước khi upload ảnh.

Application phải enforce trước:

```text
draft → pending_review
```

rằng Product có đủ required fields như main image.

---

## Không thêm `images JSONB`

Không sử dụng:

```text
products.images JSONB
```

Album media sau này nên có table:

```text
product_images
```

với metadata riêng:

```text
product_id
object_key
media_type
position
alt_text
```

---

## Không thêm rating/review/sold metrics

Không thêm V1:

```text
rating_avg
review_count
sold_count
```

Đây là derived data từ:

```text
Review Module
Order Module
Analytics/read projections
```

Không phải Catalog source of truth.

---

## Indexes

Shop listing:

```sql
CREATE INDEX idx_products_shop_status
ON products(shop_id, status);
```

Category browsing:

```sql
CREATE INDEX idx_products_category_status
ON products(category_id, status);
```

Brand filtering:

```sql
CREATE INDEX idx_products_brand_status
ON products(brand_id, status)
WHERE brand_id IS NOT NULL;
```

Moderation:

```sql
CREATE INDEX idx_products_status_created
ON products(status, created_at);
```

---

## ON DELETE

Seller không physical delete Product.

Normal action:

```text
status = archived
archived_at = now()
```

Product có thể đã xuất hiện trong historical OrderItem.

Business history không được phá hủy.

---

# 10. TABLE: product_variants

**Owner:** Catalog Module

## Purpose

Nhóm một variation của Product.

Ví dụ:

```text
Product:
Nike Air Max

Variant:
Black

SKU:
Black / Size 42
```

---

## Columns

| Column             | Type           | Null | Default       | Constraint   | FK         | Reason                   |
| ------------------ | -------------- | ---: | ------------- | ------------ | ---------- | ------------------------ |
| `id`               | `UUID`         |   NO | —             | PK           | —          | Variant identity         |
| `product_id`       | `UUID`         |   NO | —             | composite FK | `products` | Parent Product           |
| `shop_id`          | `UUID`         |   NO | —             | composite FK | `products` | Tenant ownership         |
| `name`             | `VARCHAR(160)` |   NO | —             | CHECK        | —          | Human-readable variation |
| `attributes`       | `JSONB`        |   NO | `'{}'::jsonb` | CHECK        | —          | V1 variation metadata    |
| `image_object_key` | `VARCHAR(512)` |  YES | NULL          | —            | —          | Variant-specific image   |
| `status`           | `VARCHAR(20)`  |   NO | `'active'`    | CHECK        | —          | Lifecycle                |
| `position`         | `INTEGER`      |   NO | `0`           | CHECK        | —          | Display order            |
| `created_at`       | `TIMESTAMPTZ`  |   NO | `now()`       | —            | —          | Creation                 |
| `updated_at`       | `TIMESTAMPTZ`  |   NO | `now()`       | CHECK        | —          | Mutation                 |
| `archived_at`      | `TIMESTAMPTZ`  |  YES | NULL          | CHECK        | —          | Archival                 |

---

## Primary Key

```sql
PRIMARY KEY (id)
```

---

## Foreign Key

```sql
FOREIGN KEY (product_id, shop_id)
REFERENCES products(id, shop_id)
ON DELETE RESTRICT
```

Invariant:

```text
Variant.shop_id = Product.shop_id
```

Database enforce tenant consistency.

---

## Unique Constraints

Để SKU dùng composite FK:

```sql
UNIQUE (id, shop_id)

UNIQUE (id, product_id, shop_id)
```

Key thứ hai cho phép Order snapshot reference đúng Product + Variant chain,
không chỉ cùng tenant.

Variant name case-insensitive unique trong Product:

```sql
CREATE UNIQUE INDEX uq_product_variants_name_ci
ON product_variants(
    product_id,
    lower(btrim(name))
);
```

---

## CHECK Constraints

```sql
CHECK (
    char_length(btrim(name)) BETWEEN 1 AND 160
)
```

```sql
CHECK (
    jsonb_typeof(attributes) = 'object'
)
```

```sql
CHECK (
    status IN (
        'active',
        'inactive',
        'archived'
    )
)
```

```sql
CHECK (position >= 0)
```

```sql
CHECK (
    (status = 'archived' AND archived_at IS NOT NULL)
    OR
    (status <> 'archived' AND archived_at IS NULL)
)
```

```sql
CHECK (updated_at >= created_at)
```

---

## Example

```json
{
  "color": "black"
}
```

`attributes JSONB` chỉ là V1 representation.

Nếu hệ thống cần faceted filtering phức tạp, Catalog có thể bổ sung:

```text
attributes
attribute_values
variant_attribute_values
```

ở ticket riêng.

---

## Indexes

```sql
CREATE INDEX idx_product_variants_product_status
ON product_variants(product_id, status);
```

```sql
CREATE INDEX idx_product_variants_shop
ON product_variants(shop_id);
```

---

## ON DELETE

Product → Variant:

```text
ON DELETE RESTRICT
```

Variant lifecycle dùng:

```text
archived
```

không physical delete trong normal workflow.

---

# 11. TABLE: skus

**Owner:** Catalog Module

## Purpose

SKU là sellable identity cuối cùng.

```text
Product
  ↓
Variant
  ↓
SKU
```

Các module sau sẽ làm việc với:

```text
sku_id
```

bao gồm:

```text
Cart
Inventory
Order
```

---

## Columns

| Column                          | Type           | Null | Default       | Constraint        | FK                 | Reason                  |
| ------------------------------- | -------------- | ---: | ------------- | ----------------- | ------------------ | ----------------------- |
| `id`                            | `UUID`         |   NO | —             | PK                | —                  | SKU identity            |
| `variant_id`                    | `UUID`         |   NO | —             | composite FK      | `product_variants` | Parent Variant          |
| `shop_id`                       | `UUID`         |   NO | —             | scoped uniqueness | composite FK       | Tenant                  |
| `sku_code`                      | `VARCHAR(80)`  |   NO | —             | UNIQUE per shop   | —                  | Seller SKU identifier   |
| `barcode`                       | `VARCHAR(100)` |  YES | NULL          | —                 | —                  | GTIN/EAN/UPC/etc        |
| `name`                          | `VARCHAR(160)` |   NO | —             | CHECK             | —                  | Sellable option name    |
| `attributes`                    | `JSONB`        |   NO | `'{}'::jsonb` | CHECK             | —                  | SKU-specific attributes |
| `image_object_key`              | `VARCHAR(512)` |  YES | NULL          | —                 | —                  | SKU-specific image      |
| `price_amount_minor`            | `BIGINT`       |   NO | —             | CHECK             | —                  | Current sell price      |
| `compare_at_price_amount_minor` | `BIGINT`       |  YES | NULL          | CHECK             | —                  | Display/list price      |
| `currency_code`                 | `CHAR(3)`      |   NO | —             | CHECK + FK        | `shops`            | Currency                |
| `weight_g`                      | `INTEGER`      |  YES | NULL          | CHECK             | —                  | Product weight override |
| `package_length_mm`             | `INTEGER`      |  YES | NULL          | CHECK             | —                  | Package length override |
| `package_width_mm`              | `INTEGER`      |  YES | NULL          | CHECK             | —                  | Package width override  |
| `package_height_mm`             | `INTEGER`      |  YES | NULL          | CHECK             | —                  | Package height override |
| `status`                        | `VARCHAR(20)`  |   NO | `'active'`    | CHECK             | —                  | Sellability             |
| `created_at`                    | `TIMESTAMPTZ`  |   NO | `now()`       | —                 | —                  | Creation                |
| `updated_at`                    | `TIMESTAMPTZ`  |   NO | `now()`       | CHECK             | —                  | Mutation                |
| `archived_at`                   | `TIMESTAMPTZ`  |  YES | NULL          | CHECK             | —                  | Archival                |

---

## Explicitly Not Included

SKU không có:

```text
stock_quantity
available_quantity
reserved_quantity
```

Stock thuộc Inventory Module.

---

## Primary Key

```sql
PRIMARY KEY (id)
```

---

## Foreign Keys

### Variant Tenant Integrity

```sql
FOREIGN KEY (variant_id, shop_id)
REFERENCES product_variants(id, shop_id)
ON DELETE RESTRICT
```

Invariant:

```text
SKU.shop_id = Variant.shop_id
```

---

### Shop Currency Integrity

```sql
FOREIGN KEY (shop_id, currency_code)
REFERENCES shops(id, currency_code)
ON DELETE RESTRICT
```

Invariant V1:

```text
SKU.currency_code = Shop.currency_code
```

---

## SKU Code Unique Scope

Để Cart/Inventory/Order dùng tenant-safe composite FK và Order giữ đúng Variant
chain:

```sql
UNIQUE (id, shop_id)

UNIQUE (id, variant_id, shop_id)
```

Quyết định:

```text
sku_code unique per Shop
```

Constraint:

```sql
UNIQUE (shop_id, sku_code)
```

Ví dụ hợp lệ:

```text
Shop A → TSHIRT-BLACK-M
Shop B → TSHIRT-BLACK-M
```

Không hợp lệ:

```text
Shop A
├── Product X → TSHIRT-BLACK-M
└── Product Y → TSHIRT-BLACK-M
```

---

## Why Per-Shop?

Global uniqueness tạo coupling không cần thiết giữa các seller.

Per-product uniqueness quá yếu vì seller thường sử dụng SKU code như merchant-wide inventory identifier.

Shop là scope hợp lý.

---

## CHECK Constraints

```sql
CHECK (
    char_length(btrim(sku_code)) BETWEEN 1 AND 80
)
```

```sql
CHECK (
    char_length(btrim(name)) BETWEEN 1 AND 160
)
```

```sql
CHECK (
    jsonb_typeof(attributes) = 'object'
)
```

Price:

```sql
CHECK (price_amount_minor >= 0)
```

```sql
CHECK (
    compare_at_price_amount_minor IS NULL
    OR compare_at_price_amount_minor >= price_amount_minor
)
```

Currency:

```sql
CHECK (
    currency_code = upper(currency_code)
    AND char_length(currency_code) = 3
)
```

Shipping overrides:

```sql
CHECK (
    weight_g IS NULL
    OR weight_g > 0
)
```

```sql
CHECK (
    package_length_mm IS NULL
    OR package_length_mm > 0
)
```

```sql
CHECK (
    package_width_mm IS NULL
    OR package_width_mm > 0
)
```

```sql
CHECK (
    package_height_mm IS NULL
    OR package_height_mm > 0
)
```

Lifecycle:

```sql
CHECK (
    status IN (
        'active',
        'inactive',
        'archived'
    )
)
```

```sql
CHECK (
    (status = 'archived' AND archived_at IS NOT NULL)
    OR
    (status <> 'archived' AND archived_at IS NULL)
)
```

```sql
CHECK (updated_at >= created_at)
```

---

## Barcode

`barcode` nullable.

V1 không đặt global `UNIQUE(barcode)`.

Lý do:

- merchant data có thể không sạch;
- có nhiều barcode standard;
- có thể xuất hiện duplicate data khi import;
- global barcode verification nên là business feature riêng.

Nếu sau này warehouse scan cần lookup:

```sql
CREATE INDEX idx_skus_barcode
ON skus(barcode)
WHERE barcode IS NOT NULL;
```

Có thể thêm khi use case thực sự xuất hiện.

---

## Shipping Metadata Override

Product chứa shipping defaults:

```text
products.weight_g
products.package_*_mm
```

SKU có thể override.

Application resolve:

```text
effective_weight =
    COALESCE(
        sku.weight_g,
        product.weight_g
    )
```

Tương tự dimensions.

NULL ở SKU nghĩa:

```text
inherit Product value
```

Không copy default values xuống từng SKU.

---

## Indexes

Variant lookup:

```sql
CREATE INDEX idx_skus_variant_status
ON skus(variant_id, status);
```

Shop listing:

```sql
CREATE INDEX idx_skus_shop_status
ON skus(shop_id, status);
```

Price filtering:

```sql
CREATE INDEX idx_skus_shop_price_active
ON skus(shop_id, price_amount_minor)
WHERE status = 'active';
```

`UNIQUE(shop_id, sku_code)` đã phục vụ seller SKU lookup.

---

## ON DELETE

SKU không physical delete trong normal workflow.

Lifecycle:

```text
status = archived
archived_at = now()
```

Inventory và Order sau này có thể đã tham chiếu SKU.

---

# 12. Product vs Variant vs SKU

## Product

Product là listing/concept.

```text
Nike Air Max
```

Chứa:

```text
shop
category
brand
name
description
moderation status
default shipping metadata
main image
```

Không chứa:

```text
stock
final SKU price
```

---

## Variant

Variant nhóm một lựa chọn chung.

```text
Nike Air Max / Black
```

Ví dụ:

```json
{
  "color": "black"
}
```

Variant có thể có image riêng.

---

## SKU

SKU là đơn vị cuối cùng mà customer mua.

```text
Nike Air Max / Black / Size 42
```

Ví dụ:

```json
{
  "size": "42"
}
```

SKU chứa:

```text
sku_code
barcode
price
compare-at price
sellability
shipping overrides
```

Inventory sau này quản lý quantity bằng:

```text
sku_id
```

---

# 13. Product Không Có Variation

Ngay cả sản phẩm không có lựa chọn, vẫn tạo:

```text
Product
└── Default Variant
    └── Default SKU
```

Không tạo hai application flows:

```text
product with variants
product without variants
```

Cart, Inventory và Order luôn làm việc với:

```text
sku_id
```

---

# 14. Price Ownership

Price thuộc Catalog Module và được lưu tại:

```text
skus.price_amount_minor
skus.currency_code
```

Không lưu:

```text
products.price
product_variants.price
```

Ví dụ:

```text
T-Shirt
└── Red
    ├── S → 200,000 VND
    ├── M → 200,000 VND
    └── L Limited → 250,000 VND
```

Mỗi SKU có giá riêng.

---

## V1 Currency Rule

Một Shop sử dụng một currency:

```text
shops.currency_code
```

SKU bắt buộc match Shop currency thông qua composite FK.

Không hỗ trợ:

```text
Shop A
├── SKU VND
└── SKU USD
```

ở V1.

Multi-currency storefront phải là architecture feature riêng nếu cần sau này.

---

# 18. Product Lifecycle & Moderation

Lifecycle:

```text
draft
  │
  ▼
pending_review
  │
  ├────────► rejected
  │
  ▼
active
  │
  ▼
inactive
  │
  └────────► active

active / inactive / rejected / draft
               │
               ▼
            archived
```

---

## Seller Actions

Seller có thể:

```text
create → draft

draft → pending_review

rejected → edit

rejected → pending_review

active → inactive

inactive → active
    nếu đã approved trước đó

draft/rejected/active/inactive → archived
```

Seller không được:

```text
draft → active
```

---

## Admin Actions

Admin/Moderation:

```text
pending_review → active

pending_review → rejected
```

Nếu rejected:

```text
rejection_reason NOT NULL
```

---

## State Machine Enforcement

Database enforce:

```text
allowed status values
rejection_reason invariant
archive timestamp invariant
```

Application enforce:

```text
who may perform transition
which transition is legal
moderation authorization
event publication
side effects
```

Không dùng database trigger cho actor-specific state machine ở V1.

---

# 19. Product Deletion Strategy

Không destructive delete Product.

Normal user-facing action:

```text
Archive Product
```

Database:

```text
status = 'archived'
archived_at = now()
```

Lý do:

```text
Product
  ↓
Variant
  ↓
SKU
  ↓
OrderItem
```

SKU có thể đã xuất hiện trong historical order.

Seller xóa Product không được làm mất business history.

## Order Snapshot

Order Module sau này phải snapshot:

```text
product_name
variant_name
sku_name
sku_code
attributes
price
shop/seller identity
shipping information
```

Do đó Product đổi tên hoặc archive không làm thay đổi historical Order.

---

---

# 27. Critical Indexes

```text
seller_accounts
──────────────────────────────
UNIQUE(user_id)
INDEX(status)


shops
──────────────────────────────
UNIQUE(slug)
INDEX(status)


shop_memberships
──────────────────────────────
UNIQUE(shop_id, seller_account_id)

UNIQUE(shop_id)
WHERE role = owner
AND status = active

INDEX(seller_account_id, status)

INDEX(shop_id, seller_account_id)
WHERE status = active

INDEX(seller_account_id)
WHERE status = pending


categories
──────────────────────────────
UNIQUE(slug)
WHERE parent_id IS NULL

UNIQUE(parent_id, slug)
WHERE parent_id IS NOT NULL

INDEX(parent_id)
INDEX(status)


brands
──────────────────────────────
UNIQUE(slug)
UNIQUE(lower(trim(name)))


products
──────────────────────────────
UNIQUE(shop_id, slug)
UNIQUE(id, shop_id)

INDEX(shop_id, status)
INDEX(category_id, status)
INDEX(brand_id, status)
INDEX(status, created_at)


product_variants
──────────────────────────────
UNIQUE(id, shop_id)
UNIQUE(id, product_id, shop_id)
UNIQUE(product_id, lower(trim(name)))

INDEX(product_id, status)
INDEX(shop_id)


skus
──────────────────────────────
UNIQUE(shop_id, sku_code)
UNIQUE(id, shop_id)
UNIQUE(id, variant_id, shop_id)

INDEX(variant_id, status)
INDEX(shop_id, status)

INDEX(shop_id, price_amount_minor)
WHERE status = active
```

---

# 29. Final Schema Summary

```text
seller_accounts
────────────────────────────────────
id                              UUID PK
user_id                         UUID UNIQUE FK
status                          VARCHAR(30)
created_at                      TIMESTAMPTZ
updated_at                      TIMESTAMPTZ
suspended_at                    TIMESTAMPTZ NULL
closed_at                       TIMESTAMPTZ NULL


shops
────────────────────────────────────
id                              UUID PK
name                            VARCHAR(160)
slug                            VARCHAR(160) UNIQUE
description                     TEXT NULL
logo_object_key                 VARCHAR(512) NULL
banner_object_key               VARCHAR(512) NULL
phone                           VARCHAR(32) NULL
email                           VARCHAR(254) NULL
is_official                     BOOLEAN
currency_code                   CHAR(3)
status                          VARCHAR(20)
created_at                      TIMESTAMPTZ
updated_at                      TIMESTAMPTZ
closed_at                       TIMESTAMPTZ NULL


shop_memberships
────────────────────────────────────
id                              UUID PK
shop_id                         UUID FK
seller_account_id               UUID FK
created_by_seller_account_id    UUID NULL FK
role                            VARCHAR(30)
status                          VARCHAR(20)
invited_at                      TIMESTAMPTZ NULL
accepted_at                     TIMESTAMPTZ NULL
revoked_at                      TIMESTAMPTZ NULL
created_at                      TIMESTAMPTZ
updated_at                      TIMESTAMPTZ


categories
────────────────────────────────────
id                              UUID PK
parent_id                       UUID NULL FK → categories
name                            VARCHAR(160)
slug                            VARCHAR(160)
icon_object_key                 VARCHAR(512) NULL
image_object_key                VARCHAR(512) NULL
status                          VARCHAR(20)
sort_order                      INTEGER
created_at                      TIMESTAMPTZ
updated_at                      TIMESTAMPTZ


brands
────────────────────────────────────
id                              UUID PK
name                            VARCHAR(160)
slug                            VARCHAR(160) UNIQUE
description                     TEXT NULL
logo_object_key                 VARCHAR(512) NULL
status                          VARCHAR(20)
created_at                      TIMESTAMPTZ
updated_at                      TIMESTAMPTZ


products
────────────────────────────────────
id                              UUID PK
shop_id                         UUID FK
category_id                     UUID FK
brand_id                        UUID NULL FK
name                            VARCHAR(255)
slug                            VARCHAR(255)
description                     TEXT NULL
main_image_object_key           VARCHAR(512) NULL
weight_g                        INTEGER NULL
package_length_mm               INTEGER NULL
package_width_mm                INTEGER NULL
package_height_mm               INTEGER NULL
status                          VARCHAR(30)
rejection_reason                VARCHAR(512) NULL
created_at                      TIMESTAMPTZ
updated_at                      TIMESTAMPTZ
published_at                    TIMESTAMPTZ NULL
archived_at                     TIMESTAMPTZ NULL


product_variants
────────────────────────────────────
id                              UUID PK
product_id                      UUID FK
shop_id                         UUID
name                            VARCHAR(160)
attributes                      JSONB
image_object_key                VARCHAR(512) NULL
status                          VARCHAR(20)
position                        INTEGER
created_at                      TIMESTAMPTZ
updated_at                      TIMESTAMPTZ
archived_at                     TIMESTAMPTZ NULL


skus
────────────────────────────────────
id                              UUID PK
variant_id                      UUID FK
shop_id                         UUID
sku_code                        VARCHAR(80)
barcode                         VARCHAR(100) NULL
name                            VARCHAR(160)
attributes                      JSONB
image_object_key                VARCHAR(512) NULL
price_amount_minor              BIGINT
compare_at_price_amount_minor   BIGINT NULL
currency_code                   CHAR(3)
weight_g                        INTEGER NULL
package_length_mm               INTEGER NULL
package_width_mm                INTEGER NULL
package_height_mm               INTEGER NULL
status                          VARCHAR(20)
created_at                      TIMESTAMPTZ
updated_at                      TIMESTAMPTZ
archived_at                     TIMESTAMPTZ NULL
```

---

# 30. DATA-003B Final Relationship

```text
User
 │
 ▼
SellerAccount
 │
 │
 ▼
ShopMembership ◄──────────── Shop
                                 │
                                 ▼
                              Product
                                 │
                                 ▼
                              Variant
                                 │
                                 ▼
                                SKU
                                 │
                     ┌───────────┴───────────┐
                     ▼                       ▼
              Future Inventory          Future Cart
                     │
                     ▼
                Future Order
```

Category:

```text
Category
   │
   └── Category
          │
          └── Category

Product ─────────► Category
Product ─────────► Brand
```

---

# DATA-003B Status

DATA-003B V1 ưu tiên:

```text
multi-vendor correctness
shop-scoped ownership
database-enforced tenant integrity
moderation lifecycle
historical safety
SKU-level pricing
exact money representation
shipping readiness
object-storage independence
future Inventory/Cart/Order compatibility
```

Tư duy xuyên suốt:

```text
Application validates.

Database enforces invariants.

Module owns its data.

Derived metrics are not automatically source of truth.

Historical business records are not destroyed.
```

DATA-003B hoàn thành thiết kế Seller + Catalog ở mức ERD.

Chưa viết migration.

Chưa implement Go repository/service.
