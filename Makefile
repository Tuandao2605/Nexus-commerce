# File này gom các lệnh cài tool, vận hành PostgreSQL, chạy migration và kiểm tra các ticket thuộc chuỗi DB-004.
MIGRATE_VERSION ?= v4.18.3
TOOLS_BIN ?= $(CURDIR)/bin
MIGRATE ?= $(TOOLS_BIN)/migrate
MIGRATIONS_PATH ?= migrations
TEST_MIGRATIONS_PATH ?= testdata/migrations

.PHONY: \
	tools \
	check-migrate \
	require-database \
	require-test-database \
	require-migration-probe-database \
	db-up \
	db-ensure-test-databases \
	db-down \
	db-status \
	db-smoke \
	migrate-up \
	migrate-down \
	migrate-version \
	migrate-test-up \
	migrate-test-down \
	migrate-test-version \
	migrate-test-cycle \
	migrate-integration-up \
	migrate-integration-down \
	migrate-integration-cycle \
	fmt \
	vet \
	test \
	test-integration \
	verify

# tools cài đúng phiên bản golang-migrate vào thư mục bin cục bộ của dự án.
tools:
	mkdir -p "$(TOOLS_BIN)"
	GOBIN="$(TOOLS_BIN)" go install -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)

# check-migrate dừng sớm với hướng dẫn rõ ràng nếu binary migrate chưa được cài.
check-migrate:
	@test -x "$(MIGRATE)" || (echo "missing $(MIGRATE); run 'make tools'" && exit 1)

# require-database ngăn domain migration local chạy khi thiếu DATABASE_URL.
require-database:
	@test -n "$(DATABASE_URL)" || (echo "DATABASE_URL is required" && exit 1)

# require-test-database ngăn domain migration test và integration test chạy khi thiếu TEST_DATABASE_URL.
require-test-database:
	@test -n "$(TEST_DATABASE_URL)" || (echo "TEST_DATABASE_URL is required" && exit 1)

# require-migration-probe-database giữ migration fixture ở database riêng, không dùng chung version table với domain migrations.
require-migration-probe-database:
	@test -n "$(MIGRATION_PROBE_DATABASE_URL)" || (echo "MIGRATION_PROBE_DATABASE_URL is required" && exit 1)

# db-up khởi động PostgreSQL, chờ healthcheck rồi bảo đảm hai database test tồn tại.
db-up: db-ensure-test-databases

# db-ensure-test-databases tạo bù các database test khi named volume cũ chưa chạy init script mới.
db-ensure-test-databases:
	docker compose up -d --wait postgres
	docker compose exec -T postgres sh /docker-entrypoint-initdb.d/002-ensure-test-databases.sh

# db-down dừng và xóa container/network Compose nhưng giữ nguyên named volume dữ liệu.
db-down:
	docker compose down

# db-status hiển thị trạng thái và health của các service Compose.
db-status:
	docker compose ps

# db-smoke chạy SELECT 1 bên trong container để xác nhận PostgreSQL nhận truy vấn.
db-smoke:
	docker compose exec -T postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -c "SELECT 1;"'

# migrate-up apply domain migrations còn thiếu vào development database.
migrate-up: check-migrate require-database
	"$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(DATABASE_URL)" up

# migrate-down rollback đúng một domain migration gần nhất trong development database.
migrate-down: check-migrate require-database
	"$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(DATABASE_URL)" down 1

# migrate-version hiển thị version và dirty state domain migration của development database.
migrate-version: check-migrate require-database
	"$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(DATABASE_URL)" version

# migrate-test-up apply migration-engine probe trong database probe riêng.
migrate-test-up: check-migrate require-migration-probe-database
	"$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(MIGRATION_PROBE_DATABASE_URL)" up

# migrate-test-down rollback đúng một version migration-engine probe gần nhất.
migrate-test-down: check-migrate require-migration-probe-database
	"$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(MIGRATION_PROBE_DATABASE_URL)" down 1

# migrate-test-version hiển thị version và dirty state của database probe.
migrate-test-version: check-migrate require-migration-probe-database
	"$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(MIGRATION_PROBE_DATABASE_URL)" version

# migrate-test-cycle chạy UP, DOWN rồi UP lại để kiểm tra migration engine độc lập với domain schema.
migrate-test-cycle: check-migrate require-migration-probe-database
	"$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(MIGRATION_PROBE_DATABASE_URL)" up
	"$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(MIGRATION_PROBE_DATABASE_URL)" down 1
	"$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(MIGRATION_PROBE_DATABASE_URL)" up

# migrate-integration-up apply domain migrations vào database integration test.
migrate-integration-up: check-migrate require-test-database
	"$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" up

# migrate-integration-down rollback đúng một domain migration trong database integration test.
migrate-integration-down: check-migrate require-test-database
	"$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" down 1

# migrate-integration-cycle chạy domain migration UP, DOWN rồi UP lại trên database integration test.
migrate-integration-cycle: check-migrate require-test-database
	"$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" up
	"$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" down 1
	"$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" up

# fmt định dạng toàn bộ package Go trong module.
fmt:
	go fmt ./...

# vet chạy phân tích tĩnh chuẩn của Go để tìm lỗi sử dụng API và kiểu dữ liệu đáng ngờ.
vet:
	go vet ./...

# test chạy toàn bộ Go tests; database integration tests có thể skip nếu TEST_DATABASE_URL chưa được đặt.
test:
	go test ./...

# test-integration apply domain schema, kết nối PostgreSQL thật và không cho phép test âm thầm bị skip.
test-integration: migrate-integration-up
	REQUIRE_DATABASE_INTEGRATION=1 TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test ./internal/database -count=1

# verify chạy chuỗi kiểm tra nhanh gồm format, vet và toàn bộ Go tests không bắt buộc database.
verify: fmt vet test
