// File này là regression gate của DB-004J, kiểm tra manifest migration và các convention toàn schema PostgreSQL sau khi ghép mọi domain.
package database

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// fullMigrationReviewPair giữ đường dẫn UP/DOWN và tên domain của cùng một migration version.
type fullMigrationReviewPair struct {
	name     string
	upPath   string
	downPath string
}

// TestFullMigrationReviewHasContinuousSafePairs xác nhận migration liên tục, đủ UP/DOWN, bọc transaction và rollback fail-fast.
func TestFullMigrationReviewHasContinuousSafePairs(t *testing.T) {
	pairs := readFullMigrationReviewManifest(t)
	if len(pairs) == 0 {
		t.Fatal("migration pair count = 0, want at least 1")
	}

	for version := 1; version <= len(pairs); version++ {
		pair, exists := pairs[version]
		if !exists {
			t.Fatalf("migration version %06d is missing", version)
		}
		if pair.upPath == "" || pair.downPath == "" {
			t.Fatalf("migration %06d_%s paths = (up %q, down %q), want both", version, pair.name, pair.upPath, pair.downPath)
		}

		for direction, path := range map[string]string{"UP": pair.upPath, "DOWN": pair.downPath} {
			contentBytes, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s migration %06d_%s: %v", direction, version, pair.name, err)
			}
			content := string(contentBytes)
			if !strings.HasPrefix(content, "-- File "+direction+" này") {
				t.Errorf("%s migration %06d_%s is missing its file-purpose note", direction, version, pair.name)
			}
			if strings.Count(content, "BEGIN;") != 1 || strings.Count(content, "COMMIT;") != 1 ||
				!strings.HasSuffix(strings.TrimSpace(content), "COMMIT;") {
				t.Errorf("%s migration %06d_%s must contain one BEGIN/COMMIT transaction", direction, version, pair.name)
			}
			if direction == "DOWN" {
				upperContent := strings.ToUpper(content)
				if strings.Contains(upperContent, "IF EXISTS") || strings.Contains(upperContent, "CASCADE") {
					t.Errorf("DOWN migration %06d_%s must fail fast without IF EXISTS or CASCADE", version, pair.name)
				}
			}
		}
	}
}

// TestFullMigrationReviewMatchesLatestCleanVersion xác nhận database integration ở migration mới nhất và không có dirty state.
func TestFullMigrationReviewMatchesLatestCleanVersion(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	pairs := readFullMigrationReviewManifest(t)

	var version int
	var dirty bool
	if err := tx.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if version != len(pairs) || dirty {
		t.Fatalf("schema_migrations = (version %d, dirty %t), want (%d, false)", version, dirty, len(pairs))
	}
}

// TestFullMigrationReviewOwnsExpectedTables xác nhận final schema có đúng 27 bảng V1 thuộc Identity đến Payment và không có bảng ngoài scope.
func TestFullMigrationReviewOwnsExpectedTables(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	expectedTables := []string{
		"brands",
		"cart_items",
		"carts",
		"categories",
		"credentials",
		"inventory_reservation_items",
		"inventory_reservations",
		"inventory_stocks",
		"order_items",
		"order_status_histories",
		"orders",
		"payment_refunds",
		"payment_transactions",
		"payment_webhook_events",
		"product_variants",
		"products",
		"seller_accounts",
		"sessions",
		"shop_memberships",
		"shops",
		"skus",
		"stock_movements",
		"user_addresses",
		"users",
		"voucher_usages",
		"vouchers",
		"warehouses",
	}

	rows, err := tx.Query(ctx, `
		SELECT tablename
		FROM pg_tables
		WHERE schemaname = 'public' AND tablename <> 'schema_migrations'
		ORDER BY tablename
	`)
	if err != nil {
		t.Fatalf("list V1 public tables: %v", err)
	}
	defer rows.Close()

	actualTables := make([]string, 0, len(expectedTables))
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan V1 public table: %v", err)
		}
		actualTables = append(actualTables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate V1 public tables: %v", err)
	}
	sort.Strings(expectedTables)
	if !reflect.DeepEqual(actualTables, expectedTables) {
		t.Fatalf("V1 public tables = %v, want %v", actualTables, expectedTables)
	}
}

// TestFullMigrationReviewUsesCanonicalGlobalPolicies xác nhận kiểu dữ liệu, UUID ownership, FK rollback safety và PostgreSQL ENUM đúng convention V1.
func TestFullMigrationReviewUsesCanonicalGlobalPolicies(t *testing.T) {
	ctx, tx := beginMigrationTest(t)

	var foreignKeyCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_constraint AS constraint_row
		JOIN pg_namespace AS namespace ON namespace.oid = constraint_row.connamespace
		WHERE namespace.nspname = 'public' AND constraint_row.contype = 'f'
	`).Scan(&foreignKeyCount); err != nil {
		t.Fatalf("count public foreign keys: %v", err)
	}
	if foreignKeyCount == 0 {
		t.Fatal("public foreign key count = 0, want at least 1")
	}

	checks := []struct {
		name  string
		query string
	}{
		{
			name: "foreign keys without ON DELETE RESTRICT",
			query: `
				SELECT constraint_row.conname
				FROM pg_constraint AS constraint_row
				JOIN pg_namespace AS namespace ON namespace.oid = constraint_row.connamespace
				WHERE namespace.nspname = 'public'
				  AND constraint_row.contype = 'f'
				  AND constraint_row.confdeltype <> 'r'
			`,
		},
		{
			name: "application IDs not UUIDv7-by-Go compatible",
			query: `
				SELECT table_name || '.' || column_name
				FROM information_schema.columns
				WHERE table_schema = 'public'
				  AND column_name = 'id'
				  AND (data_type <> 'uuid' OR column_default IS NOT NULL)
			`,
		},
		{
			name: "absolute timestamps not using TIMESTAMPTZ",
			query: `
				SELECT table_name || '.' || column_name
				FROM information_schema.columns
				WHERE table_schema = 'public'
				  AND right(column_name, 3) = '_at'
				  AND data_type <> 'timestamp with time zone'
			`,
		},
		{
			name: "currency columns not using CHAR(3)",
			query: `
				SELECT table_name || '.' || column_name
				FROM information_schema.columns
				WHERE table_schema = 'public'
				  AND column_name IN ('currency', 'currency_code')
				  AND (data_type <> 'character' OR character_maximum_length <> 3)
			`,
		},
		{
			name: "money columns not using BIGINT minor units",
			query: `
				SELECT table_name || '.' || column_name
				FROM information_schema.columns
				WHERE table_schema = 'public'
				  AND (
					right(column_name, 7) = '_amount'
					OR column_name IN ('price_amount_minor', 'compare_at_price_amount_minor', 'discount_value')
				  )
				  AND data_type <> 'bigint'
			`,
		},
		{
			name: "floating point or numeric domain columns",
			query: `
				SELECT table_name || '.' || column_name
				FROM information_schema.columns
				WHERE table_schema = 'public'
				  AND data_type IN ('real', 'double precision', 'numeric', 'decimal')
			`,
		},
		{
			name: "PostgreSQL ENUM types",
			query: `
				SELECT type_row.typname
				FROM pg_type AS type_row
				JOIN pg_namespace AS namespace ON namespace.oid = type_row.typnamespace
				WHERE namespace.nspname = 'public' AND type_row.typtype = 'e'
			`,
		},
	}

	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			assertFullMigrationReviewQueryReturnsNoRows(t, ctx, tx, check.query)
		})
	}
}

// TestFullMigrationReviewHasCriticalCrossDomainKeys xác nhận các key/index làm safety net cho tenant, checkout, Voucher và Payment tồn tại.
func TestFullMigrationReviewHasCriticalCrossDomainKeys(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	objects := []string{
		"uq_shop_memberships_active_owner",
		"uq_product_variants_id_product_shop",
		"uq_skus_id_variant_shop",
		"idx_inventory_reservations_reference",
		"uq_carts_user_active",
		"uq_parent_order_checkout_reference",
		"uq_orders_id_user_checkout_currency_type",
		"uq_voucher_usages_voucher_checkout",
		"uq_payment_parent_live_attempt",
		"uq_payment_webhook_provider_event",
		"uq_refund_provider_refund",
	}

	for _, object := range objects {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+object).Scan(&exists); err != nil {
			t.Fatalf("inspect critical database object %s: %v", object, err)
		}
		if !exists {
			t.Errorf("critical database object %s does not exist", object)
		}
	}
}

// readFullMigrationReviewManifest đọc các file migration và trả manifest đã nhóm theo version để nhiều review test dùng chung.
func readFullMigrationReviewManifest(t *testing.T) map[int]fullMigrationReviewPair {
	t.Helper()

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve full migration review source path")
	}
	migrationsDirectory := filepath.Join(filepath.Dir(sourceFile), "..", "..", "migrations")
	entries, err := os.ReadDir(migrationsDirectory)
	if err != nil {
		t.Fatalf("read migrations directory: %v", err)
	}

	filenamePattern := regexp.MustCompile(`^(\d{6})_([a-z][a-z0-9_]*)\.(up|down)\.sql$`)
	pairs := make(map[int]fullMigrationReviewPair)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		matches := filenamePattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			t.Fatalf("migration filename %q does not follow NNNNNN_name.(up|down).sql", entry.Name())
		}
		version, err := strconv.Atoi(matches[1])
		if err != nil {
			t.Fatalf("parse migration version from %q: %v", entry.Name(), err)
		}

		pair := pairs[version]
		if pair.name != "" && pair.name != matches[2] {
			t.Fatalf("migration version %06d uses names %q and %q", version, pair.name, matches[2])
		}
		pair.name = matches[2]
		path := filepath.Join(migrationsDirectory, entry.Name())
		switch matches[3] {
		case "up":
			if pair.upPath != "" {
				t.Fatalf("migration version %06d has duplicate UP files", version)
			}
			pair.upPath = path
		case "down":
			if pair.downPath != "" {
				t.Fatalf("migration version %06d has duplicate DOWN files", version)
			}
			pair.downPath = path
		default:
			t.Fatalf("migration %q has unsupported direction %q", entry.Name(), matches[3])
		}
		pairs[version] = pair
	}
	return pairs
}

// assertFullMigrationReviewQueryReturnsNoRows fail test và liệt kê object khi một global schema policy bị vi phạm.
func assertFullMigrationReviewQueryReturnsNoRows(t *testing.T, ctx context.Context, tx pgx.Tx, query string) {
	t.Helper()

	rows, err := tx.Query(ctx, query)
	if err != nil {
		t.Fatalf("run full migration review query: %v", err)
	}
	defer rows.Close()

	violations := make([]string, 0)
	for rows.Next() {
		var violation string
		if err := rows.Scan(&violation); err != nil {
			t.Fatalf("scan full migration review violation: %v", err)
		}
		violations = append(violations, violation)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate full migration review violations: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("global schema policy violations: %s", strings.Join(violations, ", "))
	}
}
