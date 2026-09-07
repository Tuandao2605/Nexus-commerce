# ECOM-DATA-003A — Identity Domain Model

## Scope

DATA-003A thiết kế bốn bảng:

- `users`
- `credentials`
- `sessions`
- `user_addresses`

Chưa bao gồm:

- `refresh_tokens`
- RBAC (`roles`, `permissions`, ...)
- password reset tokens
- email verification tokens
- audit logs

Các entity trên sẽ được bổ sung ở ticket Identity/Auth tiếp theo.

---

# 1. Ownership

```text
USER MODULE
├── users
└── user_addresses

AUTH MODULE
├── credentials
└── sessions
```

Nguyên tắc:

```text
User Module                         Auth Module

users                               credentials
  │                                     │
  └── user_addresses                    └── sessions
```

`users` đại diện cho identity/profile của một actor trong hệ thống.

`credentials` đại diện cho khả năng xác thực của user.

Password và email đăng nhập không thuộc `users`.

---

# 2. Global Database Conventions

## ID convention

Tất cả primary key mới sử dụng:

```text
PostgreSQL type: UUID
Generation strategy: UUIDv7
```

UUIDv7 được generate ở application layer trong Go.

Không sử dụng `BIGSERIAL`.

Không sử dụng random UUIDv4 làm mặc định.

---

## Timestamp convention

Tất cả thời gian tuyệt đối sử dụng:

```sql
TIMESTAMPTZ
```

Không sử dụng:

```sql
TIMESTAMP WITHOUT TIME ZONE
```

Convention:

```text
created_at
updated_at
deleted_at
expires_at
revoked_at
last_activity_at
```

Các timestamp lưu instant; application chịu trách nhiệm convert timezone khi hiển thị.

`created_at` và `updated_at` mặc định `now()`.

`updated_at` phải được cập nhật trong câu SQL UPDATE.

Không tạo trigger tự động ở V1 để mutation vẫn explicit trong SQL/sqlc.

---

# 3. TABLE: users

**Owner:** User Module

Mục đích: lưu identity/profile tối thiểu của một user.

| Column         | PostgreSQL Type | Null | Default    | Unique / Constraint | FK  | Reason                                     |
| -------------- | --------------- | ---: | ---------- | ------------------- | --- | ------------------------------------------ |
| `id`           | `UUID`          |   NO | —          | PK                  | —   | Stable global identifier, UUIDv7           |
| `display_name` | `VARCHAR(120)`  |   NO | —          | CHECK non-empty     | —   | Tên hiển thị, không dùng để authentication |
| `status`       | `VARCHAR(20)`   |   NO | `'active'` | CHECK               | —   | Account lifecycle                          |
| `created_at`   | `TIMESTAMPTZ`   |   NO | `now()`    | —                   | —   | Audit/lifecycle                            |
| `updated_at`   | `TIMESTAMPTZ`   |   NO | `now()`    | CHECK               | —   | Last profile mutation                      |
| `deleted_at`   | `TIMESTAMPTZ`   |  YES | NULL       | CHECK với status    | —   | Soft-delete / tombstone                    |

## Primary Key

```sql
PRIMARY KEY (id)
```

---

CHECK constraints

Display name

```sql
CHECK (char_length(btrim(display_name)) BETWEEN 1 AND 120)
```

Application vẫn validate format, nhưng DB không chấp nhận empty name.

### User status

```sql
CHECK (
    status IN (
        'active',
        'suspended',
        'deleted'
    )
)
```

Không dùng PostgreSQL ENUM ở V1.

Lý do: account states có khả năng thay đổi khi hệ thống phát triển; `CHECK` dễ migration hơn database enum.

### deleted_at invariant

```sql
CHECK (
    (status = 'deleted' AND deleted_at IS NOT NULL)
    OR
    (status <> 'deleted' AND deleted_at IS NULL)
)
```

### Timestamp invariant

```sql
CHECK (updated_at >= created_at)
```

---

## Indexes

Primary key đã tạo B-tree index trên:

```text
users(id)
```

Thêm:

```sql
CREATE INDEX idx_users_status
ON users(status);
```

Index này phục vụ admin/moderation query như:

```text
list suspended users
list active users
```

Không index `deleted_at` riêng ở V1.

---

## ON DELETE behavior

`users` là root entity.

Không thiết kế application bình thường để thực hiện:

```sql
DELETE FROM users
```

Account deletion sử dụng soft deletion:

```text
status = 'deleted'
deleted_at = now()
```

Physical deletion chỉ dành cho maintenance/data-retention workflow đặc biệt.

---

## Reasoning

`users` cố tình không có:

```text
email
password
password_hash
refresh_token
```

Những dữ liệu đó thuộc Auth Module.

Điều này giữ module boundary:

```text
User owns profile.
Auth owns authentication identity.
```

---

# 4. TABLE: credentials

**Owner:** Auth Module

Mục đích: lưu thông tin cần thiết để một user đăng nhập bằng email/password.

Quan hệ:

```text
users
  1
  │
  │
  1
credentials
```

Một user tối đa có một credential email/password trong V1.

| Column                | PostgreSQL Type | Null | Default | Unique / Constraint       | FK          | Reason                                 |
| --------------------- | --------------- | ---: | ------- | ------------------------- | ----------- | -------------------------------------- |
| `user_id`             | `UUID`          |   NO | —       | PK                        | `users(id)` | Credential belongs to exactly one user |
| `email`               | `VARCHAR(254)`  |   NO | —       | UNIQUE + CHECK normalized | —           | Authentication identifier              |
| `password_hash`       | `TEXT`          |   NO | —       | CHECK non-empty           | —           | Password hash only                     |
| `email_verified_at`   | `TIMESTAMPTZ`   |  YES | NULL    | —                         | —           | Email verification state               |
| `password_changed_at` | `TIMESTAMPTZ`   |   NO | `now()` | —                         | —           | Token/session security decisions       |
| `created_at`          | `TIMESTAMPTZ`   |   NO | `now()` | —                         | —           | Audit                                  |
| `updated_at`          | `TIMESTAMPTZ`   |   NO | `now()` | CHECK                     | —           | Credential mutation                    |

---

## Primary Key

```sql
PRIMARY KEY (user_id)
```

Dùng `user_id` làm PK thay vì thêm `credential_id` vì V1 có quan hệ:

```text
User 1 ───── 1 Credential
```

Nếu sau này hỗ trợ nhiều authentication identities:

```text
password
Google OAuth
GitHub OAuth
passkey
```

schema Auth có thể được mở rộng thành identity/provider model riêng.

---

## Foreign Key

```sql
FOREIGN KEY (user_id)
REFERENCES users(id)
ON DELETE RESTRICT
```

Không sử dụng `ON DELETE CASCADE`.

Một câu lệnh SQL vô tình delete user không được phép âm thầm xóa credential.

Account deletion phải đi qua explicit business workflow.

---

# Email normalization

Email lưu trong DB theo dạng canonical:

```text
trimmed
lower-case
```

Ví dụ:

```text
User@Example.COM
```

được application convert thành:

```text
user@example.com
```

trước khi insert.

DB enforce canonical form:

```sql
CHECK (
    email = lower(btrim(email))
    AND char_length(email) BETWEEN 3 AND 254
)
```

Application chịu trách nhiệm validation email đầy đủ.

Database chịu trách nhiệm uniqueness.

---

## UNIQUE constraint

```sql
UNIQUE (email)
```

Đây là invariant quan trọng.

Không sử dụng flow:

```text
SELECT email exists?

if false:
    INSERT
```

để đảm bảo uniqueness.

Hai registration request concurrent có thể cùng thấy email chưa tồn tại.

Database phải là lớp cuối cùng quyết định:

```text
UNIQUE violation
```

Application bắt PostgreSQL error và trả:

```text
409 Conflict
```

hoặc domain error tương ứng.

---

# Password storage

Schema chỉ chứa:

```text
password_hash
```

Không có:

```text
password
plaintext_password
password_salt
```

`password_hash` dự kiến lưu Password Hashing Competition-style encoded string, ví dụ format chứa:

```text
algorithm
parameters
salt
hash
```

Vì vậy không cần cột `salt` riêng.

Password hashing algorithm được quyết định ở Auth implementation; schema không hardcode algorithm name để cho phép nâng cấp hashing parameters sau này.

---

## CHECK

```sql
CHECK (char_length(password_hash) > 0)
```

Database không cố kiểm tra password hash cryptographically.

Việc verify hash thuộc Auth service.

---

## Indexes

Không cần index `user_id` riêng vì PK đã có index.

`UNIQUE(email)` tự tạo B-tree index.

Lookup phổ biến:

```sql
SELECT ...
FROM credentials
WHERE email = $1;
```

sẽ sử dụng unique index trực tiếp.

---

# 5. TABLE: sessions

**Owner:** Auth Module

Mục đích: đại diện cho một authenticated login session/device.

Một user có thể đăng nhập nhiều thiết bị:

```text
users
  1
  │
  └────────── *
           sessions
```

Refresh tokens sau này thuộc một session.

Ví dụ:

```text
Session
  │
  ├── Refresh Token #1
  ├── Refresh Token #2
  └── Refresh Token #3
```

rotation không tạo session mới.

---

| Column             | PostgreSQL Type | Null | Default | Unique / Constraint | FK          | Reason                      |
| ------------------ | --------------- | ---: | ------- | ------------------- | ----------- | --------------------------- |
| `id`               | `UUID`          |   NO | —       | PK                  | —           | Session identifier          |
| `user_id`          | `UUID`          |   NO | —       | —                   | `users(id)` | Session owner               |
| `ip_address`       | `INET`          |  YES | NULL    | —                   | —           | Security/session visibility |
| `user_agent`       | `VARCHAR(1024)` |  YES | NULL    | —                   | —           | Device/session information  |
| `created_at`       | `TIMESTAMPTZ`   |   NO | `now()` | —                   | —           | Session start               |
| `last_activity_at` | `TIMESTAMPTZ`   |   NO | `now()` | CHECK               | —           | Session activity            |
| `expires_at`       | `TIMESTAMPTZ`   |   NO | —       | CHECK               | —           | Hard session lifetime       |
| `revoked_at`       | `TIMESTAMPTZ`   |  YES | NULL    | CHECK               | —           | Explicit revocation         |

---

## Primary Key

```sql
PRIMARY KEY (id)
```

`id` sử dụng UUIDv7.

---

## Foreign Key

```sql
FOREIGN KEY (user_id)
REFERENCES users(id)
ON DELETE RESTRICT
```

Session không được âm thầm mất khi user bị hard delete.

Deletion/revocation phải explicit.

---

# Revocation model

Không dùng:

```text
is_revoked BOOLEAN
```

Mà dùng:

```text
revoked_at NULL
```

Meaning:

```text
revoked_at IS NULL
    → chưa bị revoke

revoked_at IS NOT NULL
    → session đã bị revoke
```

Timestamp cung cấp nhiều information hơn boolean và hữu ích cho:

```text
audit
incident investigation
session management
```

---

## CHECK constraints

### Expiration

```sql
CHECK (expires_at > created_at)
```

### Activity

```sql
CHECK (last_activity_at >= created_at)
```

### Revocation

```sql
CHECK (
    revoked_at IS NULL
    OR revoked_at >= created_at
)
```

---

## Indexes

### Load user sessions

```sql
CREATE INDEX idx_sessions_user_id
ON sessions(user_id);
```

### Active/revoked session queries

```sql
CREATE INDEX idx_sessions_user_revoked_expires
ON sessions(user_id, revoked_at, expires_at);
```

Useful for:

```text
GET /sessions
logout-all-devices
revoke all user sessions
cleanup expired sessions
```

### Cleanup expired sessions

```sql
CREATE INDEX idx_sessions_expires_at
ON sessions(expires_at);
```

Cho background worker:

```sql
DELETE FROM sessions
WHERE expires_at < ...
```

---

## last_activity_at write strategy

Không update `last_activity_at` sau mọi HTTP request.

Nếu làm vậy, một user active có thể gây lượng UPDATE lớn và tạo unnecessary database write amplification.

Application có thể update theo interval, ví dụ khi activity timestamp đã đủ cũ.

Exact interval sẽ được quyết định khi implement Auth.

---

# 6. TABLE: user_addresses

**Owner:** User Module

Mục đích: địa chỉ giao hàng được user lưu trong profile.

Order không được phụ thuộc trực tiếp vào địa chỉ này.

Khi checkout, Order Module phải snapshot shipping address.

Do đó user có thể sửa/xóa address mà không thay đổi lịch sử order.

---

| Column            | PostgreSQL Type | Null | Default | Unique / Constraint | FK          | Reason                       |
| ----------------- | --------------- | ---: | ------- | ------------------- | ----------- | ---------------------------- |
| `id`              | `UUID`          |   NO | —       | PK                  | —           | Address identifier           |
| `user_id`         | `UUID`          |   NO | —       | —                   | `users(id)` | Owner                        |
| `label`           | `VARCHAR(50)`   |  YES | NULL    | —                   | —           | Home/work/etc                |
| `recipient_name`  | `VARCHAR(120)`  |   NO | —       | CHECK non-empty     | —           | Shipping recipient           |
| `recipient_phone` | `VARCHAR(32)`   |   NO | —       | CHECK non-empty     | —           | Delivery contact             |
| `address_line1`   | `VARCHAR(255)`  |   NO | —       | CHECK non-empty     | —           | Main address                 |
| `address_line2`   | `VARCHAR(255)`  |  YES | NULL    | —                   | —           | Optional additional details  |
| `ward`            | `VARCHAR(120)`  |  YES | NULL    | —                   | —           | Local subdivision            |
| `district`        | `VARCHAR(120)`  |  YES | NULL    | —                   | —           | Administrative subdivision   |
| `province`        | `VARCHAR(120)`  |   NO | —       | CHECK non-empty     | —           | Province/state               |
| `postal_code`     | `VARCHAR(20)`   |  YES | NULL    | —                   | —           | Postal code                  |
| `country_code`    | `CHAR(2)`       |   NO | `'VN'`  | CHECK               | —           | ISO-style country identifier |
| `is_default`      | `BOOLEAN`       |   NO | `false` | partial UNIQUE      | —           | Default shipping address     |
| `created_at`      | `TIMESTAMPTZ`   |   NO | `now()` | —                   | —           | Audit                        |
| `updated_at`      | `TIMESTAMPTZ`   |   NO | `now()` | CHECK               | —           | Mutation timestamp           |

---

## Primary Key

```sql
PRIMARY KEY (id)
```

---

## Foreign Key

```sql
FOREIGN KEY (user_id)
REFERENCES users(id)
ON DELETE RESTRICT
```

---

## CHECK constraints

Example:

```sql
CHECK (char_length(btrim(recipient_name)) > 0)

CHECK (char_length(btrim(recipient_phone)) > 0)

CHECK (char_length(btrim(address_line1)) > 0)

CHECK (char_length(btrim(province)) > 0)

CHECK (
    country_code = upper(country_code)
    AND char_length(country_code) = 2
)

CHECK (updated_at >= created_at)
```

Application thực hiện validation chi tiết hơn.

Ví dụ phone number format không nên encode cứng bằng regex PostgreSQL ở V1 vì format phụ thuộc country.

---

# One default address invariant

Business invariant:

```text
Một user chỉ có tối đa một default address.
```

Không implement bằng:

```text
SELECT current default

UPDATE old default

UPDATE new default
```

và tin rằng concurrency sẽ luôn đúng.

Database enforce bằng partial unique index:

```sql
CREATE UNIQUE INDEX uq_user_addresses_one_default
ON user_addresses(user_id)
WHERE is_default = true;
```

Kết quả:

```text
User A
├── Address 1 default=true
├── Address 2 default=false
└── Address 3 default=false
```

hợp lệ.

Nhưng:

```text
Address 1 default=true
Address 2 default=true
```

bị PostgreSQL reject.

Hai concurrent requests cùng cố đặt hai địa chỉ khác nhau thành default cũng không phá invariant.

---

## Other indexes

```sql
CREATE INDEX idx_user_addresses_user_id
ON user_addresses(user_id);
```

Query chính:

```sql
SELECT *
FROM user_addresses
WHERE user_id = $1;
```

---

# 7. Relationship Overview

```text
                         USER MODULE

                     ┌───────────────┐
                     │     users     │
                     │───────────────│
                     │ id PK         │
                     │ display_name  │
                     │ status        │
                     │ deleted_at    │
                     └───────┬───────┘
                             │
                ┌────────────┴─────────────┐
                │                          │
                │ 1                        │ 1
                │                          │
                ▼ *                        ▼ 1
       ┌──────────────────┐       ┌──────────────────┐
       │ user_addresses   │       │   credentials    │
       │──────────────────│       │──────────────────│
       │ id PK            │       │ user_id PK/FK    │
       │ user_id FK       │       │ email UNIQUE     │
       │ is_default       │       │ password_hash    │
       └──────────────────┘       └──────────────────┘

                                            AUTH MODULE


                     users
                       │
                       │ 1
                       │
                       ▼ *
                ┌──────────────────┐
                │     sessions     │
                │──────────────────│
                │ id PK            │
                │ user_id FK       │
                │ expires_at       │
                │ revoked_at       │
                │ last_activity_at │
                └──────────────────┘
```

---

# 8. Six Required Design Decisions

## Decision 1 — Email belongs to Auth or User?

**Decision: Auth owns email used for authentication.**

Source of truth:

```text
credentials.email
```

Không có:

```text
users.email
```

Lý do chính là email hiện tại đóng vai trò authentication identifier.

Flow login:

```text
email
   ↓
credentials
   ↓
verify password
   ↓
user_id
   ↓
users
```

Nếu cùng lưu:

```text
users.email

và

credentials.email
```

sẽ xuất hiện hai source of truth:

```text
users.email = new@example.com
credentials.email = old@example.com
```

và cần consistency synchronization không cần thiết.

Nếu tương lai business requirement yêu cầu contact email khác login email thì thêm một profile/contact field riêng với semantic rõ ràng, ví dụ:

```text
contact_email
```

chứ không duplicate authentication email.

---

# Decision 2 — User ID dùng gì?

**Decision: UUIDv7 stored as PostgreSQL \*\***`UUID`\***\*.**

### BIGSERIAL

Ưu:

```text
8 bytes
fast index
simple
excellent locality
```

Nhược:

```text
centralized ID generation
predictable
khó hơn khi service/database được tách sau này
```

### UUIDv4

Ưu:

```text
distributed generation
opaque
easy service extraction
```

Nhược:

```text
16 bytes
random B-tree insertion
poor index locality
```

### UUIDv7

Ưu:

```text
distributed generation
opaque
time ordered
better B-tree locality than UUIDv4
good fit for future service extraction
```

Nhược:

```text
16 bytes instead of BIGINT's 8
larger FK/index footprint
generation support cần được kiểm soát
```

Nexus Commerce có roadmap từ modular monolith tới selective microservices, nên UUIDv7 là trade-off hợp lý hơn long-term.

Schema vẫn dùng PostgreSQL native:

```sql
UUID
```

UUIDv7 generation thuộc Go application.

---

# Decision 3 — Password lưu thế nào?

Password thuộc:

```text
Auth Module
```

và nằm trong:

```text
credentials.password_hash
```

Không có password column trong `users`.

Không lưu plaintext.

Không encrypt password để decrypt lại.

Password được xử lý bằng password hashing algorithm phù hợp và database chỉ lưu encoded password hash.

---

# Decision 4 — Session lưu gì?

V1 lưu:

```text
session id
user id
created time
expiration
revocation
last activity
IP
user-agent
```

Không lưu access token.

Không lưu raw refresh token trong session.

Refresh token rotation sẽ được thiết kế bằng bảng riêng:

```text
refresh_tokens
```

ở Auth ticket sau.

Session đại diện cho:

```text
one login/device context
```

Refresh token đại diện cho credential được rotate bên trong session đó.

---

# Decision 5 — Delete User

Không dùng:

```text
ON DELETE CASCADE everywhere
```

## Normal account deletion

User account được soft delete:

```text
users.status = 'deleted'
users.deleted_at = now()
```

Sau đó account-deletion workflow sẽ thực hiện explicit cleanup:

```text
revoke sessions
delete authentication credentials
delete/anonymize user addresses
remove/anonymize profile PII
```

User row trở thành tombstone identity.

Điều này quan trọng vì về sau các entity business như:

```text
orders
payments
refunds
audit logs
```

không được biến mất chỉ vì customer xóa account.

Ví dụ:

```text
User
 │
 ├── Credentials      → remove explicitly
 ├── Sessions         → revoke/remove explicitly
 ├── Addresses        → remove/anonymize explicitly
 │
 └── Orders           → KEEP
```

Orders phải giữ historical business data.

Order cũng phải snapshot:

```text
customer/shipping/product/price information
```

thay vì phụ thuộc vào mutable user profile.

## FK policy trong DATA-003A

```text
credentials.user_id
    ON DELETE RESTRICT

sessions.user_id
    ON DELETE RESTRICT

user_addresses.user_id
    ON DELETE RESTRICT
```

Điều này cố tình làm accidental physical user deletion thất bại.

Deletion là business workflow, không phải một cascade SQL statement.

---

# Decision 6 — Timestamp convention

Dùng:

```sql
TIMESTAMPTZ
```

cho mọi timestamp tuyệt đối.

Convention:

```text
created_at
    thời điểm entity được tạo

updated_at
    thời điểm persistent state thay đổi

deleted_at
    soft deletion nếu entity cần tombstone lifecycle

expires_at
    expiration

revoked_at
    security revocation

last_activity_at
    session activity
```

Không phải table nào cũng có `deleted_at`.

Trong DATA-003A chỉ `users` cần soft deletion.

`user_addresses` có thể physical delete vì Order sẽ giữ shipping snapshot riêng.

Sessions là ephemeral/security data và được revoke/cleanup.

Credentials có thể được xóa trong account-deletion/anonymization workflow.

---

# 9. Main Database Invariants

Sau DATA-003A, PostgreSQL phải tự bảo vệ được tối thiểu các invariant:

```text
1. User ID luôn unique.

2. Một authentication email chỉ thuộc tối đa một credential.

3. Email luôn được lưu canonical lowercase/trimmed form.

4. Credential luôn thuộc một existing user.

5. Một user tối đa có một email/password credential.

6. Session luôn thuộc một existing user.

7. Session expiration luôn sau creation.

8. Session revocation không thể xảy ra trước creation.

9. Address luôn thuộc một existing user.

10. Một user có tối đa một default address.

11. deleted user bắt buộc có deleted_at.

12. non-deleted user không được có deleted_at.
```

Application layer vẫn validate input.

Nhưng các invariant liên quan correctness và concurrency không chỉ dựa vào Go:

```text
Application validates.
Database enforces.
```

---

# 10. DATA-003A Final Schema Summary

```text
users
────────────────────────────────
id                  UUID PK
display_name        VARCHAR(120)
status              VARCHAR(20)
created_at          TIMESTAMPTZ
updated_at          TIMESTAMPTZ
deleted_at          TIMESTAMPTZ NULL


credentials
────────────────────────────────
user_id             UUID PK FK
email               VARCHAR(254) UNIQUE
password_hash       TEXT
email_verified_at   TIMESTAMPTZ NULL
password_changed_at TIMESTAMPTZ
created_at          TIMESTAMPTZ
updated_at          TIMESTAMPTZ


sessions
────────────────────────────────
id                  UUID PK
user_id             UUID FK
ip_address          INET NULL
user_agent          VARCHAR(1024) NULL
created_at          TIMESTAMPTZ
last_activity_at    TIMESTAMPTZ
expires_at          TIMESTAMPTZ
revoked_at          TIMESTAMPTZ NULL


user_addresses
────────────────────────────────
id                  UUID PK
user_id             UUID FK
label               VARCHAR(50) NULL
recipient_name      VARCHAR(120)
recipient_phone     VARCHAR(32)
address_line1       VARCHAR(255)
address_line2       VARCHAR(255) NULL
ward                VARCHAR(120) NULL
district            VARCHAR(120) NULL
province            VARCHAR(120)
postal_code         VARCHAR(20) NULL
country_code        CHAR(2)
is_default          BOOLEAN
created_at          TIMESTAMPTZ
updated_at          TIMESTAMPTZ
```

## Critical indexes

```text
credentials.email
    UNIQUE

sessions.user_id

sessions(user_id, revoked_at, expires_at)

sessions.expires_at

user_addresses.user_id

user_addresses(user_id)
    UNIQUE WHERE is_default = true
```

---

# DATA-003A Status

Schema hiện tại cố tình ưu tiên:

```text
correct ownership
database invariants
safe deletion
concurrency correctness
security
future service extraction
queryability
```

thay vì chỉ thiết kế bốn bảng CRUD.

Đây là ERD V1; chưa phải migration implementation.
