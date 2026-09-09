MIGRATE_VERSION ?= v4.18.3
TOOLS_BIN ?= $(CURDIR)/bin
MIGRATE ?= $(TOOLS_BIN)/migrate
TEST_MIGRATIONS_PATH ?= testdata/migrations

.PHONY: \
	tools \
	check-migrate \
	require-test-database \
	db-up \
	db-down \
	db-status \
	db-smoke \
	migrate-test-up \
	migrate-test-down \
	migrate-test-version \
	migrate-test-cycle \
	fmt \
	vet \
	test \
	test-integration \
	verify

tools:
	mkdir -p "$(TOOLS_BIN)"
	GOBIN="$(TOOLS_BIN)" go install -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)

check-migrate:
	@test -x "$(MIGRATE)" || (echo "missing $(MIGRATE); run 'make tools'" && exit 1)

require-test-database:
	@test -n "$(TEST_DATABASE_URL)" || (echo "TEST_DATABASE_URL is required" && exit 1)

db-up:
	docker compose up -d --wait postgres

db-down:
	docker compose down

db-status:
	docker compose ps

db-smoke:
	docker compose exec -T postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -c "SELECT 1;"'

migrate-test-up: check-migrate require-test-database
	"$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" up

migrate-test-down: check-migrate require-test-database
	"$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" down 1

migrate-test-version: check-migrate require-test-database
	"$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" version

migrate-test-cycle: check-migrate require-test-database
	"$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" up
	"$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" down 1
	"$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" up

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...

test-integration: require-test-database
	REQUIRE_DATABASE_INTEGRATION=1 TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test ./internal/database -run Integration -count=1

verify: fmt vet test
