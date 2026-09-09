# ECOM-DB-004A — Migration Foundation

## 1. Mục tiêu

DB-004A chỉ thiết lập **migration foundation** cho Nexus Commerce.

Không tạo domain schema ở ticket này.

### Phạm vi

```text
PostgreSQL
+
Docker Compose
+
pgx/v5 + pgxpool
+
golang-migrate
+
config validation
+
startup / shutdown lifecycle
+
database integration test
+
isolated test database
```

Không làm:

```text
users
credentials
products
orders
sqlc
repository
service
REST API
Redis
RabbitMQ
Elasticsearch
```

---

# 2. Quyết định về migration `000001`

Không tạo:

```text
000001_init
```

nếu migration đó rỗng hoặc chỉ để giữ số version.

Thay vào đó:

```text
migrations/
└── .gitkeep
```

DB-004A chỉ setup tooling.

Đến DB-004B mới tạo migration domain đầu tiên:

```text
migrations/
├── 000001_identity.up.sql
└── 000001_identity.down.sql
```

Để kiểm tra migration engine ở DB-004A, dùng migration fixture riêng:

```text
testdata/migrations/
├── 000001_probe.up.sql
└── 000001_probe.down.sql
```

Như vậy:

```text
production/domain migrations
!=
migration tooling test fixture
```

---

# 3. Project Structure

```text
nexus-commerce/
├── cmd/
│   └── api/
│       └── main.go
│
├── internal/
│   ├── config/
│   │   ├── config.go
│   │   └── config_test.go
│   │
│   ├── database/
│   │   ├── postgres.go
│   │   └── postgres_test.go
│   │
│   └── server/
│
├── migrations/
│   └── .gitkeep
│
├── testdata/
│   └── migrations/
│       ├── 000001_probe.up.sql
│       └── 000001_probe.down.sql
│
├── docker/
│   └── postgres/
│       └── init/
│           └── 001-create-test-database.sql
│
├── docker-compose.yml
├── Makefile
├── .env.example
├── go.mod
└── go.sum
```

---

# 4. Test Migration Fixture

## `testdata/migrations/000001_probe.up.sql`

```sql
CREATE TABLE migration_probe (
    id BIGINT PRIMARY KEY
);
```

## `testdata/migrations/000001_probe.down.sql`

```sql
DROP TABLE migration_probe;
```

Mục đích:

```text
migrate up
→ migration_probe exists

migrate down
→ migration_probe disappears

migrate up again
→ succeeds
```

Đây không phải domain table.

---

# 5. `.env.example`

```env
PORT=8080

DATABASE_URL=postgres://nexus:nexus_dev_password@localhost:5432/nexus_commerce?sslmode=disable

# Database riêng cho migration fixture và integration test.
TEST_DATABASE_URL=postgres://nexus:nexus_dev_password@localhost:5432/nexus_commerce_test?sslmode=disable

DATABASE_MAX_CONNS=20
DATABASE_MIN_CONNS=2
DATABASE_MAX_CONN_LIFETIME=1h
DATABASE_MAX_CONN_IDLE_TIME=30m
DATABASE_HEALTH_CHECK_PERIOD=1m
DATABASE_STARTUP_TIMEOUT=5s
```

Rule:

```text
DATABASE_URL
→ required
```

`TEST_DATABASE_URL` phải trỏ tới database riêng. Không chạy probe migration
trên `DATABASE_URL`, nếu không version `000001` của fixture có thể làm domain
migration `000001_identity` ở DB-004B bị bỏ qua.

Không silently fallback nếu URL thiếu hoặc invalid.

Các pool setting cũng phải được validate.

---

# 6. Docker PostgreSQL

## `docker-compose.yml`

```yaml
services:
  postgres:
    image: postgres:17-alpine

    environment:
      POSTGRES_DB: nexus_commerce
      POSTGRES_USER: nexus
      POSTGRES_PASSWORD: nexus_dev_password

    ports:
      - "127.0.0.1:${POSTGRES_PORT:-5432}:5432"

    volumes:
      - postgres_data:/var/lib/postgresql/data
      - ./docker/postgres/init:/docker-entrypoint-initdb.d:ro

    healthcheck:
      test:
        [
          "CMD-SHELL",
          "pg_isready -U $${POSTGRES_USER} -d $${POSTGRES_DB}"
        ]
      interval: 5s
      timeout: 5s
      retries: 10
      start_period: 5s

volumes:
  postgres_data:
```

Credential trên chỉ dùng cho local development.

Không dùng credential này trong production.

`docker/postgres/init/001-create-test-database.sql` tạo
`nexus_commerce_test` khi volume PostgreSQL được khởi tạo lần đầu.

---

# 7. Database Config

Tạo config riêng cho PostgreSQL.

Ví dụ:

```go
package config

import "time"

type DatabaseConfig struct {
    URL               string
    MaxConns          int32
    MinConns          int32
    MaxConnLifetime   time.Duration
    MaxConnIdleTime   time.Duration
    HealthCheckPeriod time.Duration
    StartupTimeout    time.Duration
}
```

## Required Validation

```text
DATABASE_URL empty
→ error

DATABASE_MAX_CONNS <= 0
→ error

DATABASE_MIN_CONNS < 0
→ error

DATABASE_MIN_CONNS > DATABASE_MAX_CONNS
→ error

invalid duration
→ error
```

Không nên silent fallback cho config database quan trọng.

---

# 8. Env Helper

Ví dụ:

```go
func requireEnv(key string) (string, error) {
    value := os.Getenv(key)

    if value == "" {
        return "", fmt.Errorf("%s is required", key)
    }

    return value, nil
}
```

Parse integer:

```go
func parseInt32Env(key string) (int32, error) {
    value, err := requireEnv(key)
    if err != nil {
        return 0, err
    }

    n, err := strconv.ParseInt(value, 10, 32)
    if err != nil {
        return 0, fmt.Errorf(
            "%s must be a valid integer: %w",
            key,
            err,
        )
    }

    return int32(n), nil
}
```

Duration:

```go
func parseDurationEnv(key string) (time.Duration, error) {
    value, err := requireEnv(key)
    if err != nil {
        return 0, err
    }

    duration, err := time.ParseDuration(value)
    if err != nil {
        return 0, fmt.Errorf(
            "%s must be a valid duration: %w",
            key,
            err,
        )
    }

    return duration, nil
}
```

---

# 9. PostgreSQL Driver

Dùng:

```text
github.com/jackc/pgx/v5
```

và:

```text
pgxpool
```

Không dùng ORM ở phase này:

```text
GORM
Ent
Bun
```

Mục tiêu là giữ rõ:

```text
SQL
transactions
locking
MVCC
constraints
query plans
```

---

# 10. Database Package

Tạo:

```text
internal/database/
├── postgres.go
└── postgres_test.go
```

Không tạo global DB variable.

Không:

```go
var DB *pgxpool.Pool
```

hoặc:

```go
var DB *sql.DB
```

Database pool phải được inject theo application lifecycle.

---

# 11. `NewPool`

## `internal/database/postgres.go`

Concept:

```go
package database

import (
    "context"
    "fmt"

    "github.com/jackc/pgx/v5/pgxpool"

    "nexus-commerce/internal/config"
)

func NewPool(
    ctx context.Context,
    cfg config.DatabaseConfig,
) (*pgxpool.Pool, error) {
    poolCfg, err := pgxpool.ParseConfig(cfg.URL)
    if err != nil {
        return nil, fmt.Errorf(
            "parse postgres config: %w",
            err,
        )
    }

    poolCfg.MaxConns = cfg.MaxConns
    poolCfg.MinConns = cfg.MinConns
    poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
    poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
    poolCfg.HealthCheckPeriod = cfg.HealthCheckPeriod

    pool, err := pgxpool.NewWithConfig(
        ctx,
        poolCfg,
    )
    if err != nil {
        return nil, fmt.Errorf(
            "create postgres pool: %w",
            err,
        )
    }

    if err := pool.Ping(ctx); err != nil {
        pool.Close()

        return nil, fmt.Errorf(
            "ping postgres: %w",
            err,
        )
    }

    return pool, nil
}
```

Important:

```text
NewWithConfig()
!=
database guaranteed reachable
```

Do đó phải:

```text
NewWithConfig()
↓
Ping()
```

Nếu Ping fail:

```text
pool.Close()
→ return error
```

---

# 12. Startup Timeout

Database initialization không được block vô hạn.

Concept:

```go
dbCtx, cancel := context.WithTimeout(
    context.Background(),
    cfg.Database.StartupTimeout,
)
defer cancel()

pool, err := database.NewPool(
    dbCtx,
    cfg.Database,
)
if err != nil {
    logger.Error(
        "database startup failed",
        "error",
        err,
    )

    os.Exit(1)
}
```

Rule:

```text
Database unavailable
→ application startup fails
```

Không:

```text
start HTTP server
→ DB unavailable
→ request mới crash/fail sau
```

---

# 13. Startup Lifecycle

Expected:

```text
main
 │
 ▼
load config
 │
 ▼
create logger
 │
 ▼
create PostgreSQL pool
 │
 ▼
Ping PostgreSQL
 │
 ▼
create HTTP server
 │
 ▼
run
```

Nếu DB là dependency bắt buộc:

```text
PostgreSQL unavailable
→ fail startup
```

---

# 14. Graceful Shutdown

Expected:

```text
SIGINT / SIGTERM
        │
        ▼
stop accepting HTTP requests
        │
        ▼
server.Shutdown(...)
        │
        ▼
pool.Close()
        │
        ▼
process exit
```

Application bootstrap nên giữ ownership của:

```text
HTTP server
PostgreSQL pool
```

Không nên có database resource bị leak khi process shutdown.

---

# 15. Health vs Readiness

V1 nên hiểu:

```text
GET /health
=
process alive
```

Không cần ping database trên mọi `/health` request.

Sau này:

```text
GET /ready
=
required dependencies usable
```

có thể kiểm tra PostgreSQL readiness.

---

# 16. Migration Tool

Dùng CLI:

```text
golang-migrate/migrate
```

Không tự viết migration runner bằng Go ở DB-004A.

Domain migration sau này sẽ chạy:

```bash
migrate \
  -path migrations \
  -database "$DATABASE_URL" \
  up
```

Rollback một version:

```bash
migrate \
  -path migrations \
  -database "$DATABASE_URL" \
  down 1
```

Ở DB-004A, dùng:

```text
testdata/migrations
```

để kiểm tra migration engine.

---

# 17. Makefile

Pin CLI version để local và CI dùng cùng một migration tool. Migration fixture
phải dùng database riêng qua `TEST_DATABASE_URL`.

```makefile
MIGRATE_VERSION ?= v4.18.3
TOOLS_BIN ?= $(CURDIR)/bin
MIGRATE ?= $(TOOLS_BIN)/migrate

.PHONY: \
	tools \
	db-up \
	db-down \
	migrate-test-up \
	migrate-test-down \
	migrate-test-version \
	migrate-test-cycle \
	fmt \
	vet \
	test \
	test-integration

tools:
	mkdir -p "$(TOOLS_BIN)"
	GOBIN="$(TOOLS_BIN)" go install -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)

db-up:
	docker compose up -d --wait postgres

db-down:
	docker compose down

migrate-test-up:
	"$(MIGRATE)" \
		-path testdata/migrations \
		-database "$(TEST_DATABASE_URL)" \
		up

migrate-test-down:
	"$(MIGRATE)" \
		-path testdata/migrations \
		-database "$(TEST_DATABASE_URL)" \
		down 1

migrate-test-version:
	"$(MIGRATE)" \
		-path testdata/migrations \
		-database "$(TEST_DATABASE_URL)" \
		version

migrate-test-cycle:
	"$(MIGRATE)" -path testdata/migrations -database "$(TEST_DATABASE_URL)" up
	"$(MIGRATE)" -path testdata/migrations -database "$(TEST_DATABASE_URL)" down 1
	"$(MIGRATE)" -path testdata/migrations -database "$(TEST_DATABASE_URL)" up

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...

test-integration:
	REQUIRE_DATABASE_INTEGRATION=1 TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test ./internal/database -run Integration -count=1
```

Đến DB-004B mới cần:

```makefile
migrate-up:
	migrate \
		-path migrations \
		-database "$(DATABASE_URL)" \
		up
```

---

# 18. Config Unit Tests

Nên dùng table-driven tests.

Concept:

```go
func TestDatabaseConfigValidation(t *testing.T) {
    tests := []struct {
        name    string
        wantErr bool
    }{
        {
            name:    "valid config",
            wantErr: false,
        },
        {
            name:    "missing database url",
            wantErr: true,
        },
        {
            name:    "max connections zero",
            wantErr: true,
        },
        {
            name:    "negative min connections",
            wantErr: true,
        },
        {
            name:    "min greater than max",
            wantErr: true,
        },
    }
}
```

Required cases:

```text
valid config
→ success

missing DATABASE_URL
→ error

DATABASE_MAX_CONNS = 0
→ error

DATABASE_MIN_CONNS < 0
→ error

MIN > MAX
→ error

invalid duration
→ error
```

---

# 19. Database Integration Test

Không mock PostgreSQL.

Không mock `pgxpool`.

Dùng database thật chạy bằng Docker.

Ví dụ:

```go
func TestNewPool(t *testing.T) {
    databaseURL := os.Getenv("TEST_DATABASE_URL")

    if databaseURL == "" {
        if os.Getenv("REQUIRE_DATABASE_INTEGRATION") == "1" {
            t.Fatal("TEST_DATABASE_URL is required for database integration tests")
        }
        t.Skip("TEST_DATABASE_URL not set")
    }

    cfg := config.DatabaseConfig{
        URL:               databaseURL,
        MaxConns:          5,
        MinConns:          1,
        MaxConnLifetime:   time.Hour,
        MaxConnIdleTime:   30 * time.Minute,
        HealthCheckPeriod: time.Minute,
    }

    ctx, cancel := context.WithTimeout(
        context.Background(),
        5*time.Second,
    )
    defer cancel()

    pool, err := database.NewPool(
        ctx,
        cfg,
    )
    if err != nil {
        t.Fatalf(
            "NewPool() error = %v",
            err,
        )
    }
    defer pool.Close()

    var result int

    err = pool.QueryRow(
        ctx,
        "SELECT 1",
    ).Scan(&result)
    if err != nil {
        t.Fatalf(
            "SELECT 1 failed: %v",
            err,
        )
    }

    if result != 1 {
        t.Fatalf(
            "result = %d, want 1",
            result,
        )
    }
}
```

Test này chứng minh:

```text
Go
→ pgxpool
→ PostgreSQL
→ SQL query
→ result
```

hoạt động thật.

---

# 20. Invalid DB Integration Test

Có thể thêm test với port sai:

```text
postgres://nexus:password@localhost:59999/nexus_commerce
```

Expected:

```text
NewPool()
→ error
```

Không được hang vô hạn.

Startup timeout phải kết thúc request connect.

---

# 21. Migration Verification

## Step 1 — Start PostgreSQL

```bash
docker compose up -d postgres
```

Kiểm tra:

```bash
docker compose ps
```

Expected:

```text
postgres
→ healthy
```

---

## Step 2 — Export Database URLs

```bash
export DATABASE_URL='postgres://nexus:nexus_dev_password@localhost:5432/nexus_commerce?sslmode=disable'
export TEST_DATABASE_URL='postgres://nexus:nexus_dev_password@localhost:5432/nexus_commerce_test?sslmode=disable'
```

---

## Step 3 — Verify Raw Connection

```bash
psql "$DATABASE_URL" -c "SELECT 1;"
```

Expected:

```text
1
```

---

## Step 4 — Migration Up

```bash
make migrate-test-up
```

Kiểm tra:

```bash
psql "$TEST_DATABASE_URL" \
  -c "\d migration_probe"
```

Expected:

```text
migration_probe exists
```

---

## Step 5 — Migration Down

```bash
make migrate-test-down
```

Kiểm tra:

```bash
psql "$TEST_DATABASE_URL" \
  -c "\d migration_probe"
```

Expected:

```text
migration_probe does not exist
```

---

## Step 6 — Up / Down / Up Verification

```bash
make migrate-test-up
make migrate-test-down
make migrate-test-up
```

Expected:

```text
all commands succeed
```

Mục tiêu là chứng minh:

```text
up
→ down
→ up
```

đều deterministic.

---

# 22. Migration Atomicity Note

PostgreSQL hỗ trợ transactional DDL khá tốt.

Nhưng không nên assume mọi migration operation đều transaction-safe trong mọi trường hợp.

Ví dụ:

```sql
CREATE INDEX CONCURRENTLY
```

không chạy bên trong transaction block thông thường.

DB-004A chưa cần dùng:

```text
CONCURRENTLY
```

Chỉ cần ghi nhớ rule này cho production migrations sau.

---

# 23. Down Migration Rule

Development migration phải có `.down.sql`.

Ví dụ sau này:

```text
UP
users
→ credentials
→ sessions
→ user_addresses
```

thì rollback:

```text
DOWN
user_addresses
→ sessions
→ credentials
→ users
```

Không drop parent trước child.

---

# 24. Migration Naming Convention

Domain migrations bắt đầu ở DB-004B:

```text
000001_identity.up.sql
000001_identity.down.sql

000002_seller.up.sql
000002_seller.down.sql

000003_catalog.up.sql
000003_catalog.down.sql

000004_inventory.up.sql
000004_inventory.down.sql
```

Không dùng:

```text
create_users.sql
fix_users.sql
fix_users_again.sql
final_fix.sql
```

Sau khi project đã live:

```text
one migration
=
one logical schema change
```

Ví dụ:

```text
000021_add_product_images
000022_add_order_cancellation_reason
000023_add_payment_reconciliation_index
```

---

# 25. Implementation Order

Làm DB-004A theo thứ tự:

```text
DB-004A.1
Docker Compose PostgreSQL

↓

DB-004A.2
Database config

↓

DB-004A.3
Config validation

↓

DB-004A.4
pgxpool ParseConfig

↓

DB-004A.5
NewPool + Ping

↓

DB-004A.6
Startup timeout

↓

DB-004A.7
Application startup wiring

↓

DB-004A.8
Graceful shutdown

↓

DB-004A.9
golang-migrate CLI

↓

DB-004A.10
testdata migration probe

↓

DB-004A.11
config unit tests

↓

DB-004A.12
PostgreSQL integration tests

↓

DB-004A.13
migration up/down/up verification

↓

DB-004A.14
gofmt + vet + test
```

---

# 26. Required Commands Before Review

```bash
make fmt
```

```bash
go vet ./...
```

```bash
go test ./...
```

Sau đó:

```bash
docker compose up -d postgres
```

```bash
docker compose ps
```

```bash
make db-smoke
```

Migration verification:

```bash
make tools
make migrate-test-cycle
make test-integration
```

---

# 27. Acceptance Criteria

DB-004A PASS khi:

```text
[x] PostgreSQL runs via Docker Compose

[x] PostgreSQL healthcheck reports healthy

[x] DATABASE_URL is configurable

[x] DATABASE_URL is required

[x] TEST_DATABASE_URL uses an isolated test database

[x] database pool settings are configurable

[x] invalid database config is rejected

[x] pgxpool ParseConfig is used

[x] pgxpool NewWithConfig is used

[x] Ping verifies real PostgreSQL connectivity

[x] database startup timeout exists

[x] application fails cleanly if database is unavailable

[x] no global database variable exists

[x] pool closes during graceful shutdown

[x] golang-migrate CLI works

[x] golang-migrate CLI version is pinned

[x] test migration UP works

[x] test migration DOWN works

[x] UP → DOWN → UP works

[x] probe migration never changes the development/domain database

[x] domain migrations directory is still clean

[x] DB-004B can start at 000001_identity

[x] config unit tests pass

[x] database integration test passes

[x] invalid connection integration test passes

[x] gofmt passes

[x] go vet ./... passes

[x] go test ./... passes
```

---

# 28. Không Làm Trong DB-004A

Không bắt đầu:

```text
users migration
credentials
sessions
user_addresses
shops
products
SKUs
inventory
cart
orders
voucher
payment
sqlc.yaml
queries.sql
repositories
services
handlers
```

DB-004A chỉ trả lời:

```text
"Database foundation của project đã đáng tin chưa?"
```

chưa trả lời:

```text
"Domain schema hoàn chỉnh chưa?"
```

---

# 29. Sau Khi DB-004A PASS

Flow:

```text
DB-004A
Migration Foundation
        ↓
DB-004B
Identity Schema
        ↓
DB-004C
Seller + Catalog
        ↓
DB-004D
Inventory
        ↓
DB-004E
Cart
        ↓
DB-004F
Order
        ↓
DB-004G
Voucher
        ↓
DB-004H
Payment
        ↓
DB-004I
Cross-Domain DB Tests
        ↓
DB-004J
Full Migration Review
```

Các correction DATA-003G sẽ được encode trực tiếp vào các migration tương ứng, ví dụ:

```text
Parent-safe self FK

VoucherUsage ↔ Parent ownership

single-currency hierarchy

Inventory checkout correlation

one-live-payment constraint

provider-scoped refund identity
```

---

# 30. Deliverables Khi Gửi Review

Sau khi hoàn thành DB-004A, gửi các file:

```text
docker-compose.yml
docker/postgres/init/001-create-test-database.sql
.env.example
Makefile

internal/config/config.go
internal/config/config_test.go

internal/database/postgres.go
internal/database/postgres_test.go

cmd/api/main.go

testdata/migrations/000001_probe.up.sql
testdata/migrations/000001_probe.down.sql
```

Kèm output:

```bash
docker compose ps
```

```bash
go vet ./...
```

```bash
go test ./...
```

```bash
make migrate-test-cycle
make test-integration
```

Nếu tất cả PASS thì chuyển sang:

```text
ECOM-DB-004B — Identity Schema
```
