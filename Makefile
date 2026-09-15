# File này gom các lệnh cài tool, vận hành PostgreSQL, chạy migration và kiểm tra các ticket thuộc chuỗi DB-004.
MIGRATE_VERSION ?= v4.18.3
SQLC_VERSION ?= v1.31.1
TOOLS_BIN ?= $(CURDIR)/bin
MIGRATE ?= $(TOOLS_BIN)/migrate
SQLC ?= $(TOOLS_BIN)/sqlc
MIGRATIONS_PATH ?= migrations
TEST_MIGRATIONS_PATH ?= testdata/migrations
COMMAND_TIMEOUT ?= 120s
DB_COMMAND_TIMEOUT ?= 60s
DB_START_TIMEOUT ?= 90s
GO_TEST_TIMEOUT ?= 2m
GOCACHE ?= $(if $(TMPDIR),$(TMPDIR),/tmp)/nexus-commerce-go-build
QUIET_RUN ?= ./scripts/run-quiet.sh
TEST_PACKAGE ?= ./...
TEST_NAME ?=
DB_TEST_NAME ?=

export GOCACHE

.PHONY: \
	agent-preflight \
	tools \
	check-migrate \
	check-sqlc \
	require-database \
	require-test-database \
	require-migration-probe-database \
	db-up \
	db-ensure-test-databases \
	db-down \
	db-status \
	db-smoke \
	db-migration-status \
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
	migrate-integration-full-cycle \
	sqlc-version \
	sqlc-generate \
	sqlc-check \
	fmt \
	format-check \
	vet \
	test \
	test-target \
	test-integration \
	test-integration-target \
	diff-check \
	verify-fast \
	verify-full \
	verify

# agent-preflight kiểm tra nhanh repo root và tool nền, không yêu cầu Docker hay database phải chạy.
agent-preflight:
	@for command in git go gofmt make mktemp timeout; do \
		command -v "$$command" >/dev/null 2>&1 || { echo "FAIL: missing command $$command"; exit 1; }; \
	done
	@test -f AGENTS.md -a -f Makefile -a -f go.mod || { echo "FAIL: required repository files are missing"; exit 1; }
	@test -x "$(QUIET_RUN)" || { echo "FAIL: verification runner $(QUIET_RUN) is missing or not executable"; exit 1; }
	@repo_root="$$(git rev-parse --show-toplevel 2>/dev/null)"; \
		test "$$repo_root" = "$(CURDIR)" || { echo "FAIL: run make from repository root"; exit 1; }
	@echo "PASS: agent preflight"

# tools cài đúng phiên bản golang-migrate và sqlc vào thư mục bin cục bộ của dự án.
tools:
	mkdir -p "$(TOOLS_BIN)"
	GOBIN="$(TOOLS_BIN)" go install -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)
	GOBIN="$(TOOLS_BIN)" go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

# check-migrate dừng sớm với hướng dẫn rõ ràng nếu binary migrate chưa được cài.
check-migrate:
	@test -x "$(MIGRATE)" || (echo "missing $(MIGRATE); run 'make tools'" && exit 1)

# check-sqlc xác nhận binary sqlc local tồn tại và đúng version đã pin cho codegen tái lập.
check-sqlc:
	@test -x "$(SQLC)" || (echo "missing $(SQLC); run 'make tools'" && exit 1)
	@actual_version="$$($(SQLC) version 2>&1)"; \
		case "$$actual_version" in *"$(SQLC_VERSION)"*) ;; \
		*) echo "FAIL: sqlc version $$actual_version, want $(SQLC_VERSION)"; exit 1;; esac

# require-database ngăn domain migration local chạy khi thiếu DATABASE_URL.
require-database:
	@test -n "$(DATABASE_URL)" || (echo "DATABASE_URL is required" && exit 1)

# require-test-database ngăn domain migration test và integration test chạy khi thiếu TEST_DATABASE_URL.
require-test-database:
	@test -n "$(TEST_DATABASE_URL)" || (echo "TEST_DATABASE_URL is required" && exit 1)
	@database_name="$$(printf '%s\n' "$(TEST_DATABASE_URL)" | sed -e 's/[?].*//' -e 's#.*/##')"; \
		case "$$database_name" in *test*) ;; *) echo "FAIL: TEST_DATABASE_URL must name an isolated test database"; exit 1;; esac
	@if test -n "$(DATABASE_URL)" && test "$(TEST_DATABASE_URL)" = "$(DATABASE_URL)"; then \
		echo "FAIL: TEST_DATABASE_URL must differ from DATABASE_URL"; exit 1; \
	fi

# require-migration-probe-database giữ migration fixture ở database riêng, không dùng chung version table với domain migrations.
require-migration-probe-database:
	@test -n "$(MIGRATION_PROBE_DATABASE_URL)" || (echo "MIGRATION_PROBE_DATABASE_URL is required" && exit 1)

# db-up khởi động PostgreSQL, chờ healthcheck rồi bảo đảm hai database test tồn tại.
db-up: db-ensure-test-databases

# db-ensure-test-databases tạo bù các database test khi named volume cũ chưa chạy init script mới.
db-ensure-test-databases:
	@timeout "$(DB_START_TIMEOUT)" docker compose up -d --wait postgres < /dev/null
	@timeout "$(DB_COMMAND_TIMEOUT)" docker compose exec -T postgres sh /docker-entrypoint-initdb.d/002-ensure-test-databases.sh < /dev/null

# db-down dừng và xóa container/network Compose nhưng giữ nguyên named volume dữ liệu.
db-down:
	@timeout "$(DB_COMMAND_TIMEOUT)" docker compose down < /dev/null

# db-status hiển thị trạng thái và health của các service Compose.
db-status:
	@timeout "$(DB_COMMAND_TIMEOUT)" docker compose ps < /dev/null

# db-smoke chạy SELECT 1 bên trong container để xác nhận PostgreSQL nhận truy vấn.
db-smoke:
	@timeout "$(DB_COMMAND_TIMEOUT)" docker compose exec -T postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB" -c "SELECT 1;"' < /dev/null

# db-migration-status hiển thị version và fail nếu domain migrations trên database integration test bị dirty.
db-migration-status: check-migrate require-test-database
	@status="$$(timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" version 2>&1)" || { \
		exit_code=$$?; printf '%s\n' "$$status" >&2; exit "$$exit_code"; \
	}; \
	printf '%s\n' "$$status"; \
	case "$$status" in *dirty*|*Dirty*|*DIRTY*) echo "FAIL: integration migration state is dirty" >&2; exit 1;; esac

# migrate-up apply domain migrations còn thiếu vào development database.
migrate-up: check-migrate require-database
	@timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(DATABASE_URL)" up

# migrate-down rollback đúng một domain migration gần nhất trong development database.
migrate-down: check-migrate require-database
	@timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(DATABASE_URL)" down 1

# migrate-version hiển thị version và dirty state domain migration của development database.
migrate-version: check-migrate require-database
	@timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(DATABASE_URL)" version

# migrate-test-up apply migration-engine probe trong database probe riêng.
migrate-test-up: check-migrate require-migration-probe-database
	@timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(MIGRATION_PROBE_DATABASE_URL)" up

# migrate-test-down rollback đúng một version migration-engine probe gần nhất.
migrate-test-down: check-migrate require-migration-probe-database
	@timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(MIGRATION_PROBE_DATABASE_URL)" down 1

# migrate-test-version hiển thị version và dirty state của database probe.
migrate-test-version: check-migrate require-migration-probe-database
	@timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(MIGRATION_PROBE_DATABASE_URL)" version

# migrate-test-cycle chạy UP, DOWN rồi UP lại để kiểm tra migration engine độc lập với domain schema.
migrate-test-cycle: check-migrate require-migration-probe-database
	@timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(MIGRATION_PROBE_DATABASE_URL)" up
	@timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(MIGRATION_PROBE_DATABASE_URL)" down 1
	@timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(TEST_MIGRATIONS_PATH)" -database "$(MIGRATION_PROBE_DATABASE_URL)" up

# migrate-integration-up apply domain migrations vào database integration test.
migrate-integration-up: check-migrate require-test-database
	@timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" up

# migrate-integration-down rollback đúng một domain migration trong database integration test.
migrate-integration-down: check-migrate require-test-database
	@timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" down 1

# migrate-integration-cycle chạy domain migration UP, DOWN rồi UP lại trên database integration test.
migrate-integration-cycle: check-migrate require-test-database
	@timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" up
	@timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" down 1
	@timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" up

# migrate-integration-full-cycle bootstrap schema mới nhất, rollback toàn bộ rồi apply lại toàn chuỗi trên database test cô lập.
migrate-integration-full-cycle: check-migrate require-test-database
	@timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" up
	@timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" down -all
	@timeout "$(DB_COMMAND_TIMEOUT)" "$(MIGRATE)" -path "$(MIGRATIONS_PATH)" -database "$(TEST_DATABASE_URL)" up

# sqlc-version in version binary sqlc đang được project sử dụng.
sqlc-version: check-sqlc
	@$(SQLC) version

# sqlc-generate sinh package Go type-safe từ migration schema và các query đã khai báo.
sqlc-generate: check-sqlc
	@$(QUIET_RUN) "sqlc generate" timeout "$(COMMAND_TIMEOUT)" "$(SQLC)" generate

# sqlc-check compile/vet SQL và fail nếu generated code không khớp schema/query hiện tại.
sqlc-check: check-sqlc
	@$(QUIET_RUN) "sqlc compile" timeout "$(COMMAND_TIMEOUT)" "$(SQLC)" compile
	@$(QUIET_RUN) "sqlc vet" timeout "$(COMMAND_TIMEOUT)" "$(SQLC)" vet
	@$(QUIET_RUN) "sqlc diff" timeout "$(COMMAND_TIMEOUT)" "$(SQLC)" diff
	@echo "PASS: sqlc-check"

# fmt định dạng toàn bộ package Go trong module.
fmt:
	@go fmt ./...

# format-check chỉ báo các Go file chưa gofmt và không tự sửa worktree.
format-check:
	@unformatted="$$(find cmd internal -type f -name '*.go' -exec gofmt -l {} +)"; \
		if test -n "$$unformatted"; then printf 'FAIL: gofmt\n%s\n' "$$unformatted"; exit 1; fi
	@echo "PASS: gofmt"

# vet chạy phân tích tĩnh chuẩn của Go để tìm lỗi sử dụng API và kiểu dữ liệu đáng ngờ.
vet:
	@$(QUIET_RUN) "go vet ./..." timeout "$(COMMAND_TIMEOUT)" go vet ./...

# test chạy toàn bộ Go tests; database integration tests có thể skip nếu TEST_DATABASE_URL chưa được đặt.
test:
	@$(QUIET_RUN) "go test ./..." timeout "$(COMMAND_TIMEOUT)" go test -timeout "$(GO_TEST_TIMEOUT)" ./...

# test-target chạy package/test filter do ticket chọn thay vì luôn quét toàn repo.
test-target:
	@$(QUIET_RUN) "targeted Go test" timeout "$(COMMAND_TIMEOUT)" \
		go test -timeout "$(GO_TEST_TIMEOUT)" $(TEST_PACKAGE) $(if $(TEST_NAME),-run "$(TEST_NAME)",)

# test-integration apply domain schema, kết nối PostgreSQL thật và không cho phép test âm thầm bị skip.
test-integration: migrate-integration-up
	@REQUIRE_DATABASE_INTEGRATION=1 TEST_DATABASE_URL="$(TEST_DATABASE_URL)" \
		$(QUIET_RUN) "PostgreSQL integration tests" timeout "$(COMMAND_TIMEOUT)" \
		go test -timeout "$(GO_TEST_TIMEOUT)" ./internal/database -count=1

# test-integration-target chạy focused PostgreSQL test nhưng vẫn cấm integration test bị skip âm thầm.
test-integration-target: migrate-integration-up
	@REQUIRE_DATABASE_INTEGRATION=1 TEST_DATABASE_URL="$(TEST_DATABASE_URL)" \
		$(QUIET_RUN) "targeted PostgreSQL integration test" timeout "$(COMMAND_TIMEOUT)" \
		go test -timeout "$(GO_TEST_TIMEOUT)" ./internal/database -count=1 \
		$(if $(DB_TEST_NAME),-run "$(DB_TEST_NAME)",)

# diff-check kiểm tra whitespace rồi in scope ngắn, gồm cả untracked files, để agent review trước completion.
diff-check:
	@$(QUIET_RUN) "git diff --check" git diff --check
	@git status --short
	@git diff --stat

# verify-fast là gate read-only, nhanh cho vòng lặp thường xuyên.
verify-fast: agent-preflight sqlc-check format-check vet test
	@echo "PASS: verify-fast"

# verify-full chạy tuần tự gate nhanh, full migration rollback/apply, PostgreSQL tests, status và diff gate.
verify-full:
	@$(MAKE) --no-print-directory verify-fast
	@$(MAKE) --no-print-directory migrate-integration-full-cycle
	@$(MAKE) --no-print-directory test-integration
	@$(MAKE) --no-print-directory db-migration-status
	@$(MAKE) --no-print-directory diff-check
	@echo "PASS: verify-full"

# verify giữ backward compatibility và trỏ tới gate nhanh read-only.
verify: verify-fast
