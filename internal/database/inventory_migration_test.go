// File này kiểm thử migration Inventory bằng PostgreSQL thật, gồm tenant, quantity, reservation, ledger và concurrency invariants.
package database

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// inventoryTestFixture giữ graph Catalog cùng Warehouse và InventoryStock hợp lệ dùng trong integration tests.
type inventoryTestFixture struct {
	catalog     sellerCatalogTestFixture
	warehouseID string
	stockID     string
}

// inventoryConcurrentResult lưu kết quả một conditional stock reservation chạy đồng thời.
type inventoryConcurrentResult struct {
	success bool
	err     error
}

// TestInventoryMigrationCreatesExpectedObjects xác nhận đủ năm bảng và các index quan trọng của Inventory tồn tại.
func TestInventoryMigrationCreatesExpectedObjects(t *testing.T) {
	ctx, tx := beginMigrationTest(t)

	objects := []string{
		"warehouses",
		"inventory_stocks",
		"inventory_reservations",
		"inventory_reservation_items",
		"stock_movements",
		"idx_warehouses_shop_status",
		"idx_inventory_stocks_shop_sku",
		"idx_inventory_reservations_active_expires",
		"idx_inventory_reservations_reference",
		"idx_inventory_reservation_items_stock",
		"idx_stock_movements_stock_created",
		"idx_stock_movements_reference",
	}

	for _, object := range objects {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+object).Scan(&exists); err != nil {
			t.Fatalf("check database object %s: %v", object, err)
		}
		if !exists {
			t.Errorf("database object %s does not exist", object)
		}
	}
}

// TestInventoryMigrationUsesCanonicalTypesAndDeleteRules xác nhận UUID, BIGINT, TIMESTAMPTZ và RESTRICT đúng convention dự án.
func TestInventoryMigrationUsesCanonicalTypesAndDeleteRules(t *testing.T) {
	ctx, tx := beginMigrationTest(t)

	tables := []string{
		"warehouses",
		"inventory_stocks",
		"inventory_reservations",
		"inventory_reservation_items",
		"stock_movements",
	}
	for _, table := range tables {
		var dataType string
		var defaultValue string
		if err := tx.QueryRow(ctx, `
			SELECT data_type, COALESCE(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = 'id'
		`, table).Scan(&dataType, &defaultValue); err != nil {
			t.Fatalf("inspect %s.id: %v", table, err)
		}
		if dataType != "uuid" {
			t.Errorf("%s.id type = %s, want uuid", table, dataType)
		}
		if defaultValue != "" {
			t.Errorf("%s.id default = %q, want no database-generated UUID", table, defaultValue)
		}
	}

	foreignKeys := []string{
		"fk_warehouses_shop",
		"fk_inventory_stocks_warehouse_shop",
		"fk_inventory_stocks_sku_shop",
		"fk_inventory_reservation_items_reservation",
		"fk_inventory_reservation_items_stock",
		"fk_stock_movements_stock",
	}
	for _, foreignKey := range foreignKeys {
		var deleteAction string
		if err := tx.QueryRow(ctx, `
			SELECT confdeltype::text
			FROM pg_constraint
			WHERE conname = $1
		`, foreignKey).Scan(&deleteAction); err != nil {
			t.Fatalf("inspect foreign key %s: %v", foreignKey, err)
		}
		if deleteAction != "r" {
			t.Errorf("foreign key %s delete action = %q, want RESTRICT", foreignKey, deleteAction)
		}
	}

	var invalidTimestampColumns int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name = ANY($1::text[])
		  AND right(column_name, 3) = '_at'
		  AND data_type <> 'timestamp with time zone'
	`, tables).Scan(&invalidTimestampColumns); err != nil {
		t.Fatalf("inspect Inventory timestamps: %v", err)
	}
	if invalidTimestampColumns != 0 {
		t.Errorf("Inventory has %d non-TIMESTAMPTZ absolute-time columns", invalidTimestampColumns)
	}

	var invalidQuantityColumns int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND (
		      (table_name = 'inventory_stocks' AND column_name IN ('on_hand_quantity', 'reserved_quantity'))
		      OR (table_name = 'inventory_reservation_items' AND column_name = 'quantity')
		      OR (table_name = 'stock_movements' AND column_name = 'quantity_delta')
		  )
		  AND data_type <> 'bigint'
	`).Scan(&invalidQuantityColumns); err != nil {
		t.Fatalf("inspect Inventory quantity types: %v", err)
	}
	if invalidQuantityColumns != 0 {
		t.Errorf("Inventory has %d non-BIGINT quantity columns", invalidQuantityColumns)
	}
}

// TestInventoryMigrationAcceptsValidGraph xác nhận graph Shop/SKU đến stock, reservation item và movement hợp lệ được lưu.
func TestInventoryMigrationAcceptsValidGraph(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	fixture := insertInventoryTestFixture(t, ctx, tx, 10, 2)
	reservationID := insertInventoryTestReservation(t, ctx, tx)
	insertInventoryTestReservationItem(t, ctx, tx, reservationID, fixture.stockID, 2)
	insertInventoryTestMovement(t, ctx, tx, fixture.stockID, "receipt", 10)

	var available int64
	if err := tx.QueryRow(ctx, `
		SELECT on_hand_quantity - reserved_quantity
		FROM inventory_stocks
		WHERE id = $1
	`, fixture.stockID).Scan(&available); err != nil {
		t.Fatalf("calculate available quantity: %v", err)
	}
	if available != 8 {
		t.Fatalf("available quantity = %d, want 8", available)
	}
}

// TestInventoryMigrationEnforcesTenantConsistency xác nhận Warehouse, InventoryStock và SKU luôn thuộc cùng Shop.
func TestInventoryMigrationEnforcesTenantConsistency(t *testing.T) {
	t.Run("warehouse shop and stock shop must match", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		first := insertInventoryTestFixture(t, ctx, tx, 10, 0)
		secondCatalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")

		_, err := tx.Exec(ctx, `
			INSERT INTO inventory_stocks (
				id, shop_id, warehouse_id, sku_id, on_hand_quantity, reserved_quantity
			)
			VALUES ($1, $2, $3, $4, 10, 0)
		`, newMigrationTestUUID(t), secondCatalog.shopID, first.warehouseID, secondCatalog.skuID)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("sku shop and stock shop must match", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		first := insertInventoryTestFixture(t, ctx, tx, 10, 0)
		secondCatalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")

		_, err := tx.Exec(ctx, `
			INSERT INTO inventory_stocks (
				id, shop_id, warehouse_id, sku_id, on_hand_quantity, reserved_quantity
			)
			VALUES ($1, $2, $3, $4, 10, 0)
		`, newMigrationTestUUID(t), first.catalog.shopID, first.warehouseID, secondCatalog.skuID)
		assertMigrationSQLState(t, err, "23503")
	})
}

// TestInventoryMigrationEnforcesScopedUniqueness kiểm tra code Warehouse và current stock identity không bị trùng sai phạm vi.
func TestInventoryMigrationEnforcesScopedUniqueness(t *testing.T) {
	t.Run("warehouse code unique inside shop", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		fixture := insertInventoryTestFixture(t, ctx, tx, 10, 0)

		_, err := tx.Exec(ctx, `
			INSERT INTO warehouses (id, shop_id, name, code)
			VALUES ($1, $2, 'Duplicate Warehouse', 'DEFAULT-WH')
		`, newMigrationTestUUID(t), fixture.catalog.shopID)
		assertMigrationSQLState(t, err, "23505")
	})

	t.Run("same warehouse code allowed across shops", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		first := insertInventoryTestFixture(t, ctx, tx, 10, 0)
		second := insertInventoryTestFixture(t, ctx, tx, 10, 0)

		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*)
			FROM warehouses
			WHERE code = 'DEFAULT-WH' AND shop_id IN ($1, $2)
		`, first.catalog.shopID, second.catalog.shopID).Scan(&count); err != nil {
			t.Fatalf("count repeated warehouse codes: %v", err)
		}
		if count != 2 {
			t.Fatalf("same warehouse code across shops count = %d, want 2", count)
		}
	})

	t.Run("one stock row per warehouse and sku", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		fixture := insertInventoryTestFixture(t, ctx, tx, 10, 0)

		_, err := tx.Exec(ctx, `
			INSERT INTO inventory_stocks (
				id, shop_id, warehouse_id, sku_id, on_hand_quantity, reserved_quantity
			)
			VALUES ($1, $2, $3, $4, 20, 0)
		`, newMigrationTestUUID(t), fixture.catalog.shopID, fixture.warehouseID, fixture.catalog.skuID)
		assertMigrationSQLState(t, err, "23505")
	})
}

// TestInventoryMigrationEnforcesQuantityInvariants kiểm tra on-hand và reserved không âm, đồng thời reserved không vượt on-hand.
func TestInventoryMigrationEnforcesQuantityInvariants(t *testing.T) {
	tests := []struct {
		name     string
		onHand   int64
		reserved int64
	}{
		{name: "negative on-hand", onHand: -1, reserved: 0},
		{name: "negative reserved", onHand: 10, reserved: -1},
		{name: "reserved above on-hand", onHand: 10, reserved: 11},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
			warehouseID := insertInventoryTestWarehouse(t, ctx, tx, catalog.shopID, "QUANTITY-WH")

			_, err := tx.Exec(ctx, `
				INSERT INTO inventory_stocks (
					id, shop_id, warehouse_id, sku_id, on_hand_quantity, reserved_quantity
				)
				VALUES ($1, $2, $3, $4, $5, $6)
			`, newMigrationTestUUID(t), catalog.shopID, warehouseID, catalog.skuID, test.onHand, test.reserved)
			assertMigrationSQLState(t, err, "23514")
		})
	}
}

// TestInventoryMigrationDoesNotPersistAvailableQuantity xác nhận available luôn được derive thay vì trở thành source of truth thứ hai.
func TestInventoryMigrationDoesNotPersistAvailableQuantity(t *testing.T) {
	ctx, tx := beginMigrationTest(t)

	var columnCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name = 'inventory_stocks'
		  AND column_name = 'available_quantity'
	`).Scan(&columnCount); err != nil {
		t.Fatalf("inspect available_quantity column: %v", err)
	}
	if columnCount != 0 {
		t.Fatalf("inventory_stocks has available_quantity columns = %d, want 0", columnCount)
	}
}

// TestInventoryMigrationEnforcesReservationInvariants kiểm tra correlation, idempotency, TTL, lifecycle và item uniqueness.
func TestInventoryMigrationEnforcesReservationInvariants(t *testing.T) {
	t.Run("reference type must be checkout", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)

		_, err := tx.Exec(ctx, `
			INSERT INTO inventory_reservations (
				id, reference_type, reference_id, idempotency_key, request_hash, expires_at
			)
			VALUES ($1, 'order', $2, $3, $4, now() + interval '15 minutes')
		`, newMigrationTestUUID(t), newMigrationTestUUID(t), inventoryTestIdempotencyKey(t), inventoryTestRequestHash())
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("request hash must have sha256 length", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)

		_, err := tx.Exec(ctx, `
			INSERT INTO inventory_reservations (
				id, reference_type, reference_id, idempotency_key, request_hash, expires_at
			)
			VALUES ($1, 'checkout', $2, $3, 'too-short', now() + interval '15 minutes')
		`, newMigrationTestUUID(t), newMigrationTestUUID(t), inventoryTestIdempotencyKey(t))
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("expiration must be after creation", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)

		_, err := tx.Exec(ctx, `
			INSERT INTO inventory_reservations (
				id, reference_type, reference_id, idempotency_key, request_hash, created_at, expires_at
			)
			VALUES ($1, 'checkout', $2, $3, $4, now(), now())
		`, newMigrationTestUUID(t), newMigrationTestUUID(t), inventoryTestIdempotencyKey(t), inventoryTestRequestHash())
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("terminal status requires matching timestamp", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)

		_, err := tx.Exec(ctx, `
			INSERT INTO inventory_reservations (
				id, reference_type, reference_id, idempotency_key, request_hash, status, expires_at
			)
			VALUES ($1, 'checkout', $2, $3, $4, 'committed', now() + interval '15 minutes')
		`, newMigrationTestUUID(t), newMigrationTestUUID(t), inventoryTestIdempotencyKey(t), inventoryTestRequestHash())
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("idempotency key is unique", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		key := inventoryTestIdempotencyKey(t)
		firstID := newMigrationTestUUID(t)
		if _, err := tx.Exec(ctx, `
			INSERT INTO inventory_reservations (
				id, reference_type, reference_id, idempotency_key, request_hash, expires_at
			)
			VALUES ($1, 'checkout', $2, $3, $4, now() + interval '15 minutes')
		`, firstID, newMigrationTestUUID(t), key, inventoryTestRequestHash()); err != nil {
			t.Fatalf("insert first idempotent reservation: %v", err)
		}

		_, err := tx.Exec(ctx, `
			INSERT INTO inventory_reservations (
				id, reference_type, reference_id, idempotency_key, request_hash, expires_at
			)
			VALUES ($1, 'checkout', $2, $3, $4, now() + interval '15 minutes')
		`, newMigrationTestUUID(t), newMigrationTestUUID(t), key, strings.Repeat("b", 64))
		assertMigrationSQLState(t, err, "23505")
	})

	t.Run("reservation stock item is unique", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		fixture := insertInventoryTestFixture(t, ctx, tx, 10, 2)
		reservationID := insertInventoryTestReservation(t, ctx, tx)
		insertInventoryTestReservationItem(t, ctx, tx, reservationID, fixture.stockID, 1)

		_, err := tx.Exec(ctx, `
			INSERT INTO inventory_reservation_items (
				id, reservation_id, inventory_stock_id, quantity
			)
			VALUES ($1, $2, $3, 1)
		`, newMigrationTestUUID(t), reservationID, fixture.stockID)
		assertMigrationSQLState(t, err, "23505")
	})

	t.Run("reservation item quantity must be positive", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		fixture := insertInventoryTestFixture(t, ctx, tx, 10, 0)
		reservationID := insertInventoryTestReservation(t, ctx, tx)

		_, err := tx.Exec(ctx, `
			INSERT INTO inventory_reservation_items (
				id, reservation_id, inventory_stock_id, quantity
			)
			VALUES ($1, $2, $3, 0)
		`, newMigrationTestUUID(t), reservationID, fixture.stockID)
		assertMigrationSQLState(t, err, "23514")
	})
}

// TestInventoryMigrationEnforcesMovementInvariants kiểm tra type và dấu của physical stock movement luôn tương thích.
func TestInventoryMigrationEnforcesMovementInvariants(t *testing.T) {
	tests := []struct {
		name         string
		movementType string
		delta        int64
	}{
		{name: "unknown movement type", movementType: "reserve", delta: -1},
		{name: "zero movement", movementType: "receipt", delta: 0},
		{name: "incoming movement requires positive delta", movementType: "receipt", delta: -1},
		{name: "outgoing movement requires negative delta", movementType: "sale", delta: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			fixture := insertInventoryTestFixture(t, ctx, tx, 10, 0)

			_, err := tx.Exec(ctx, `
				INSERT INTO stock_movements (
					id, inventory_stock_id, movement_type, quantity_delta
				)
				VALUES ($1, $2, $3, $4)
			`, newMigrationTestUUID(t), fixture.stockID, test.movementType, test.delta)
			assertMigrationSQLState(t, err, "23514")
		})
	}
}

// TestInventoryMigrationRollsBackMultiStockReservation xác nhận một stock hết hàng làm rollback toàn bộ reservation transaction.
func TestInventoryMigrationRollsBackMultiStockReservation(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	fixture := insertInventoryTestFixture(t, ctx, tx, 10, 0)
	secondWarehouseID := insertInventoryTestWarehouse(t, ctx, tx, fixture.catalog.shopID, "SECOND-WH")
	thirdWarehouseID := insertInventoryTestWarehouse(t, ctx, tx, fixture.catalog.shopID, "EMPTY-WH")
	secondStockID := insertInventoryTestStock(t, ctx, tx, fixture.catalog.shopID, secondWarehouseID, fixture.catalog.skuID, 10, 0)
	emptyStockID := insertInventoryTestStock(t, ctx, tx, fixture.catalog.shopID, thirdWarehouseID, fixture.catalog.skuID, 0, 0)

	reservationTx, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("begin multi-stock reservation transaction: %v", err)
	}
	reservationID := insertInventoryTestReservation(t, ctx, reservationTx)

	for _, stockID := range []string{fixture.stockID, secondStockID} {
		commandTag, reserveErr := reservationTx.Exec(ctx, `
			UPDATE inventory_stocks
			SET reserved_quantity = reserved_quantity + 1, updated_at = now()
			WHERE id = $1 AND on_hand_quantity - reserved_quantity >= 1
		`, stockID)
		if reserveErr != nil {
			t.Fatalf("reserve available stock %s: %v", stockID, reserveErr)
		}
		if commandTag.RowsAffected() != 1 {
			t.Fatalf("reserve available stock %s affected %d rows, want 1", stockID, commandTag.RowsAffected())
		}
		insertInventoryTestReservationItem(t, ctx, reservationTx, reservationID, stockID, 1)
	}

	commandTag, err := reservationTx.Exec(ctx, `
		UPDATE inventory_stocks
		SET reserved_quantity = reserved_quantity + 1, updated_at = now()
		WHERE id = $1 AND on_hand_quantity - reserved_quantity >= 1
	`, emptyStockID)
	if err != nil {
		t.Fatalf("attempt reserve empty stock: %v", err)
	}
	if commandTag.RowsAffected() != 0 {
		t.Fatalf("reserve empty stock affected %d rows, want 0", commandTag.RowsAffected())
	}
	if err := reservationTx.Rollback(ctx); err != nil {
		t.Fatalf("rollback failed multi-stock reservation: %v", err)
	}

	for _, stockID := range []string{fixture.stockID, secondStockID, emptyStockID} {
		var reserved int64
		if err := tx.QueryRow(ctx, `
			SELECT reserved_quantity FROM inventory_stocks WHERE id = $1
		`, stockID).Scan(&reserved); err != nil {
			t.Fatalf("read stock %s after rollback: %v", stockID, err)
		}
		if reserved != 0 {
			t.Errorf("stock %s reserved after rollback = %d, want 0", stockID, reserved)
		}
	}

	var reservationCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM inventory_reservations WHERE id = $1
	`, reservationID).Scan(&reservationCount); err != nil {
		t.Fatalf("count rolled-back reservation: %v", err)
	}
	if reservationCount != 0 {
		t.Fatalf("rolled-back reservation count = %d, want 0", reservationCount)
	}
}

// TestInventoryMigrationRejectsCommitAfterDeadline xác nhận wall-clock deadline chặn commit dù worker chưa đổi status active.
func TestInventoryMigrationRejectsCommitAfterDeadline(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	reservationID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO inventory_reservations (
			id, reference_type, reference_id, idempotency_key, request_hash,
			status, created_at, updated_at, expires_at
		)
		VALUES (
			$1, 'checkout', $2, $3, $4,
			'active', now() - interval '2 minutes', now(), now() - interval '1 minute'
		)
	`, reservationID, newMigrationTestUUID(t), inventoryTestIdempotencyKey(t), inventoryTestRequestHash()); err != nil {
		t.Fatalf("insert effectively expired reservation: %v", err)
	}

	commandTag, err := tx.Exec(ctx, `
		UPDATE inventory_reservations
		SET status = 'committed', committed_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'active' AND expires_at > now()
	`, reservationID)
	if err != nil {
		t.Fatalf("attempt commit after deadline: %v", err)
	}
	if commandTag.RowsAffected() != 0 {
		t.Fatalf("commit after deadline affected %d rows, want 0", commandTag.RowsAffected())
	}
}

// TestInventoryMigrationAtomicReservationConcurrency xác nhận 1000 attempts chỉ reserve đúng 50 units hiện có.
func TestInventoryMigrationAtomicReservationConcurrency(t *testing.T) {
	ctx, pool := openMigrationTestPool(t)
	setupTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin concurrent inventory setup: %v", err)
	}
	fixture := insertInventoryTestFixture(t, ctx, setupTx, 50, 0)
	if err := setupTx.Commit(ctx); err != nil {
		t.Fatalf("commit concurrent inventory setup: %v", err)
	}
	registerInventoryTestFixtureCleanup(t, pool, fixture)

	const attempts = 1000
	start := make(chan struct{})
	results := make(chan inventoryConcurrentResult, attempts)
	var waitGroup sync.WaitGroup

	for index := 0; index < attempts; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start

			commandTag, updateErr := pool.Exec(ctx, `
				UPDATE inventory_stocks
				SET reserved_quantity = reserved_quantity + 1, updated_at = now()
				WHERE id = $1 AND on_hand_quantity - reserved_quantity >= 1
			`, fixture.stockID)
			if updateErr != nil {
				results <- inventoryConcurrentResult{err: updateErr}
				return
			}
			results <- inventoryConcurrentResult{success: commandTag.RowsAffected() == 1}
		}()
	}

	close(start)
	waitGroup.Wait()
	close(results)

	successCount := 0
	failureCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent stock reservation: %v", result.err)
		}
		if result.success {
			successCount++
		} else {
			failureCount++
		}
	}
	if successCount != 50 || failureCount != 950 {
		t.Fatalf("concurrent reservations successes = %d, failures = %d; want 50 and 950", successCount, failureCount)
	}

	var onHand int64
	var reserved int64
	if err := pool.QueryRow(ctx, `
		SELECT on_hand_quantity, reserved_quantity
		FROM inventory_stocks
		WHERE id = $1
	`, fixture.stockID).Scan(&onHand, &reserved); err != nil {
		t.Fatalf("read stock after concurrent reservations: %v", err)
	}
	if onHand != 50 || reserved != 50 {
		t.Fatalf("stock after concurrency on-hand = %d, reserved = %d; want 50 and 50", onHand, reserved)
	}
}

// insertInventoryTestFixture tạo Catalog graph, Warehouse và current stock row hợp lệ cho Inventory tests.
func insertInventoryTestFixture(t *testing.T, ctx context.Context, tx pgx.Tx, onHand, reserved int64) inventoryTestFixture {
	t.Helper()

	fixture := inventoryTestFixture{}
	fixture.catalog = insertSellerCatalogTestFixture(t, ctx, tx, "VND")
	fixture.warehouseID = insertInventoryTestWarehouse(t, ctx, tx, fixture.catalog.shopID, "DEFAULT-WH")
	fixture.stockID = insertInventoryTestStock(
		t,
		ctx,
		tx,
		fixture.catalog.shopID,
		fixture.warehouseID,
		fixture.catalog.skuID,
		onHand,
		reserved,
	)

	return fixture
}

// insertInventoryTestWarehouse tạo Warehouse active với code được chỉ định trong một Shop.
func insertInventoryTestWarehouse(t *testing.T, ctx context.Context, tx pgx.Tx, shopID, code string) string {
	t.Helper()

	warehouseID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO warehouses (id, shop_id, name, code)
		VALUES ($1, $2, 'Inventory Test Warehouse', $3)
	`, warehouseID, shopID, code); err != nil {
		t.Fatalf("insert Warehouse: %v", err)
	}

	return warehouseID
}

// insertInventoryTestStock tạo current stock row với tenant và quantities hợp lệ do caller cung cấp.
func insertInventoryTestStock(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	shopID string,
	warehouseID string,
	skuID string,
	onHand int64,
	reserved int64,
) string {
	t.Helper()

	stockID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO inventory_stocks (
			id, shop_id, warehouse_id, sku_id, on_hand_quantity, reserved_quantity
		)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, stockID, shopID, warehouseID, skuID, onHand, reserved); err != nil {
		t.Fatalf("insert InventoryStock: %v", err)
	}

	return stockID
}

// insertInventoryTestReservation tạo active checkout reservation có TTL 15 phút và idempotency metadata hợp lệ.
func insertInventoryTestReservation(t *testing.T, ctx context.Context, tx pgx.Tx) string {
	t.Helper()

	reservationID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO inventory_reservations (
			id, reference_type, reference_id, idempotency_key, request_hash, expires_at
		)
		VALUES ($1, 'checkout', $2, $3, $4, now() + interval '15 minutes')
	`, reservationID, newMigrationTestUUID(t), inventoryTestIdempotencyKey(t), inventoryTestRequestHash()); err != nil {
		t.Fatalf("insert InventoryReservation: %v", err)
	}

	return reservationID
}

// insertInventoryTestReservationItem gắn quantity dương của một stock row vào reservation.
func insertInventoryTestReservationItem(t *testing.T, ctx context.Context, tx pgx.Tx, reservationID, stockID string, quantity int64) string {
	t.Helper()

	itemID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO inventory_reservation_items (
			id, reservation_id, inventory_stock_id, quantity
		)
		VALUES ($1, $2, $3, $4)
	`, itemID, reservationID, stockID, quantity); err != nil {
		t.Fatalf("insert InventoryReservationItem: %v", err)
	}

	return itemID
}

// insertInventoryTestMovement tạo một physical stock movement hợp lệ phục vụ happy-path test.
func insertInventoryTestMovement(t *testing.T, ctx context.Context, tx pgx.Tx, stockID, movementType string, delta int64) string {
	t.Helper()

	movementID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO stock_movements (
			id, inventory_stock_id, movement_type, quantity_delta
		)
		VALUES ($1, $2, $3, $4)
	`, movementID, stockID, movementType, delta); err != nil {
		t.Fatalf("insert StockMovement: %v", err)
	}

	return movementID
}

// inventoryTestIdempotencyKey tạo internal command key duy nhất cho mỗi reservation test.
func inventoryTestIdempotencyKey(t *testing.T) string {
	t.Helper()

	return "inventory:reserve:" + newMigrationTestUUID(t)
}

// inventoryTestRequestHash trả SHA-256 hexadecimal fixture dài đúng 64 ký tự.
func inventoryTestRequestHash() string {
	return strings.Repeat("a", 64)
}

// registerInventoryTestFixtureCleanup đăng ký xóa graph đã commit theo thứ tự dependency-safe sau concurrency test.
func registerInventoryTestFixtureCleanup(t *testing.T, pool *pgxpool.Pool, fixture inventoryTestFixture) {
	t.Helper()

	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()

		cleanupTx, err := pool.Begin(cleanupContext)
		if err != nil {
			t.Errorf("begin Inventory fixture cleanup: %v", err)
			return
		}

		deletes := []struct {
			query string
			id    string
		}{
			{query: "DELETE FROM inventory_stocks WHERE id = $1", id: fixture.stockID},
			{query: "DELETE FROM warehouses WHERE id = $1", id: fixture.warehouseID},
			{query: "DELETE FROM skus WHERE id = $1", id: fixture.catalog.skuID},
			{query: "DELETE FROM product_variants WHERE id = $1", id: fixture.catalog.variantID},
			{query: "DELETE FROM products WHERE id = $1", id: fixture.catalog.productID},
			{query: "DELETE FROM brands WHERE id = $1", id: fixture.catalog.brandID},
			{query: "DELETE FROM categories WHERE id = $1", id: fixture.catalog.categoryID},
			{query: "DELETE FROM shop_memberships WHERE shop_id = $1", id: fixture.catalog.shopID},
			{query: "DELETE FROM shops WHERE id = $1", id: fixture.catalog.shopID},
			{query: "DELETE FROM seller_accounts WHERE id = $1", id: fixture.catalog.sellerAccountID},
			{query: "DELETE FROM users WHERE id = $1", id: fixture.catalog.userID},
		}
		for _, deletion := range deletes {
			if _, err := cleanupTx.Exec(cleanupContext, deletion.query, deletion.id); err != nil {
				_ = cleanupTx.Rollback(cleanupContext)
				t.Errorf("clean committed Inventory fixture with %q: %v", deletion.query, err)
				return
			}
		}

		if err := cleanupTx.Commit(cleanupContext); err != nil {
			t.Errorf("commit Inventory fixture cleanup: %v", err)
		}
	})
}
