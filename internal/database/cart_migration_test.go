// File này kiểm thử migration Cart bằng PostgreSQL thật, gồm lifecycle, tenant, quantity, partial checkout và concurrency invariants.
package database

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// cartTestFixture giữ một Catalog graph cùng active Cart hợp lệ dùng trong integration tests.
type cartTestFixture struct {
	catalog sellerCatalogTestFixture
	cartID  string
}

// cartConcurrentResult giữ kết quả của một Cart mutation chạy đồng thời.
type cartConcurrentResult struct {
	sqlState string
	err      error
}

// TestCartMigrationCreatesExpectedObjects xác nhận hai bảng và các index quan trọng của Cart tồn tại.
func TestCartMigrationCreatesExpectedObjects(t *testing.T) {
	ctx, tx := beginMigrationTest(t)

	objects := []string{
		"carts",
		"cart_items",
		"uq_carts_user_active",
		"idx_carts_user_created",
		"uq_cart_items_cart_sku",
		"idx_cart_items_cart_shop",
		"idx_cart_items_sku",
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

// TestCartMigrationUsesCanonicalTypesAndDeleteRules xác nhận UUID, BIGINT, TIMESTAMPTZ và RESTRICT đúng convention dự án.
func TestCartMigrationUsesCanonicalTypesAndDeleteRules(t *testing.T) {
	ctx, tx := beginMigrationTest(t)

	for _, table := range []string{"carts", "cart_items"} {
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

	for _, foreignKey := range []string{"fk_carts_user", "fk_cart_items_cart", "fk_cart_items_sku_shop"} {
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
	`, []string{"carts", "cart_items"}).Scan(&invalidTimestampColumns); err != nil {
		t.Fatalf("inspect Cart timestamps: %v", err)
	}
	if invalidTimestampColumns != 0 {
		t.Errorf("Cart has %d non-TIMESTAMPTZ absolute-time columns", invalidTimestampColumns)
	}

	var quantityType string
	if err := tx.QueryRow(ctx, `
		SELECT data_type
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'cart_items' AND column_name = 'quantity'
	`).Scan(&quantityType); err != nil {
		t.Fatalf("inspect cart_items.quantity: %v", err)
	}
	if quantityType != "bigint" {
		t.Errorf("cart_items.quantity type = %s, want bigint", quantityType)
	}
}

// TestCartMigrationKeepsCartAsPurchaseIntentOnly xác nhận Cart không sở hữu Shop, price, total, stock hay reservation data.
func TestCartMigrationKeepsCartAsPurchaseIntentOnly(t *testing.T) {
	ctx, tx := beginMigrationTest(t)

	var forbiddenColumnCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND (
		      (table_name = 'carts' AND column_name IN (
		          'shop_id', 'subtotal_amount_minor', 'total_amount_minor',
		          'discount_amount_minor', 'shipping_amount_minor', 'currency_code', 'stock_reserved'
		      ))
		      OR
		      (table_name = 'cart_items' AND column_name IN (
		          'unit_price', 'unit_price_amount_minor', 'price_snapshot',
		          'subtotal_amount_minor', 'available_quantity', 'reserved_quantity'
		      ))
		  )
	`).Scan(&forbiddenColumnCount); err != nil {
		t.Fatalf("inspect forbidden Cart columns: %v", err)
	}
	if forbiddenColumnCount != 0 {
		t.Fatalf("Cart schema contains %d forbidden cross-domain columns, want none", forbiddenColumnCount)
	}
}

// TestCartMigrationAcceptsGlobalMultiShopCart xác nhận một Cart global có thể chứa SKU hợp lệ từ nhiều Shop.
func TestCartMigrationAcceptsGlobalMultiShopCart(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	first := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
	second := insertSellerCatalogTestFixture(t, ctx, tx, "USD")
	cartID := insertCartTestCart(t, ctx, tx, first.userID)
	insertCartTestItem(t, ctx, tx, cartID, first.shopID, first.skuID, 2)
	insertCartTestItem(t, ctx, tx, cartID, second.shopID, second.skuID, 3)

	var itemCount int
	var shopCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*), count(DISTINCT shop_id)
		FROM cart_items
		WHERE cart_id = $1
	`, cartID).Scan(&itemCount, &shopCount); err != nil {
		t.Fatalf("inspect multi-Shop Cart: %v", err)
	}
	if itemCount != 2 || shopCount != 2 {
		t.Fatalf("multi-Shop Cart items = %d, shops = %d; want 2 and 2", itemCount, shopCount)
	}
}

// TestCartMigrationEnforcesOneActiveCartPerUser xác nhận partial unique index chỉ cho một active Cart nhưng vẫn giữ lịch sử.
func TestCartMigrationEnforcesOneActiveCartPerUser(t *testing.T) {
	t.Run("duplicate active cart rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		userID := insertSellerCatalogTestUser(t, ctx, tx)
		insertCartTestCart(t, ctx, tx, userID)

		_, err := tx.Exec(ctx, `
			INSERT INTO carts (id, user_id)
			VALUES ($1, $2)
		`, newMigrationTestUUID(t), userID)
		assertMigrationSQLState(t, err, "23505")
	})

	t.Run("terminal history and new active cart accepted", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		userID := insertSellerCatalogTestUser(t, ctx, tx)
		firstCartID := insertCartTestCart(t, ctx, tx, userID)
		if _, err := tx.Exec(ctx, `
			UPDATE carts
			SET status = 'checked_out', checked_out_at = now(), updated_at = now()
			WHERE id = $1
		`, firstCartID); err != nil {
			t.Fatalf("complete first Cart: %v", err)
		}
		insertCartTestCart(t, ctx, tx, userID)

		var cartCount int
		var activeCount int
		if err := tx.QueryRow(ctx, `
			SELECT count(*), count(*) FILTER (WHERE status = 'active')
			FROM carts
			WHERE user_id = $1
		`, userID).Scan(&cartCount, &activeCount); err != nil {
			t.Fatalf("inspect Cart history: %v", err)
		}
		if cartCount != 2 || activeCount != 1 {
			t.Fatalf("Cart history count = %d, active = %d; want 2 and 1", cartCount, activeCount)
		}
	})
}

// TestCartMigrationRejectsInvalidLifecycle xác nhận status, terminal timestamp và chronology luôn nhất quán.
func TestCartMigrationRejectsInvalidLifecycle(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{
			name: "unknown status",
			query: `INSERT INTO carts (id, user_id, status)
			        VALUES ($1, $2, 'unknown')`,
		},
		{
			name: "checked out requires timestamp",
			query: `INSERT INTO carts (id, user_id, status)
			        VALUES ($1, $2, 'checked_out')`,
		},
		{
			name: "abandoned requires timestamp",
			query: `INSERT INTO carts (id, user_id, status)
			        VALUES ($1, $2, 'abandoned')`,
		},
		{
			name: "active forbids terminal timestamp",
			query: `INSERT INTO carts (id, user_id, status, checked_out_at)
			        VALUES ($1, $2, 'active', now())`,
		},
		{
			name: "terminal timestamp cannot predate creation",
			query: `INSERT INTO carts (id, user_id, status, created_at, updated_at, checked_out_at)
			        VALUES ($1, $2, 'checked_out', now(), now(), now() - interval '1 minute')`,
		},
		{
			name: "updated timestamp cannot predate creation",
			query: `INSERT INTO carts (id, user_id, created_at, updated_at)
			        VALUES ($1, $2, now(), now() - interval '1 minute')`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			_, err := tx.Exec(ctx, test.query, newMigrationTestUUID(t), userID)
			assertMigrationSQLState(t, err, "23514")
		})
	}
}

// TestCartMigrationEnforcesCartItemInvariants xác nhận parent, tenant, uniqueness, quantity và timestamp của CartItem.
func TestCartMigrationEnforcesCartItemInvariants(t *testing.T) {
	t.Run("cart must exist", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")

		_, err := tx.Exec(ctx, `
			INSERT INTO cart_items (id, cart_id, shop_id, sku_id, quantity)
			VALUES ($1, $2, $3, $4, 1)
		`, newMigrationTestUUID(t), newMigrationTestUUID(t), catalog.shopID, catalog.skuID)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("shop must match sku", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		first := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		second := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		cartID := insertCartTestCart(t, ctx, tx, first.userID)

		_, err := tx.Exec(ctx, `
			INSERT INTO cart_items (id, cart_id, shop_id, sku_id, quantity)
			VALUES ($1, $2, $3, $4, 1)
		`, newMigrationTestUUID(t), cartID, first.shopID, second.skuID)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("same sku appears once per cart", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		fixture := insertCartTestFixture(t, ctx, tx)
		insertCartTestItem(t, ctx, tx, fixture.cartID, fixture.catalog.shopID, fixture.catalog.skuID, 1)

		_, err := tx.Exec(ctx, `
			INSERT INTO cart_items (id, cart_id, shop_id, sku_id, quantity)
			VALUES ($1, $2, $3, $4, 2)
		`, newMigrationTestUUID(t), fixture.cartID, fixture.catalog.shopID, fixture.catalog.skuID)
		assertMigrationSQLState(t, err, "23505")
	})

	for _, quantity := range []int64{0, 100} {
		quantity := quantity
		t.Run("quantity outside one to ninety-nine rejected", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			fixture := insertCartTestFixture(t, ctx, tx)

			_, err := tx.Exec(ctx, `
				INSERT INTO cart_items (id, cart_id, shop_id, sku_id, quantity)
				VALUES ($1, $2, $3, $4, $5)
			`, newMigrationTestUUID(t), fixture.cartID, fixture.catalog.shopID, fixture.catalog.skuID, quantity)
			assertMigrationSQLState(t, err, "23514")
		})
	}

	t.Run("updated timestamp cannot predate creation", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		fixture := insertCartTestFixture(t, ctx, tx)

		_, err := tx.Exec(ctx, `
			INSERT INTO cart_items (
				id, cart_id, shop_id, sku_id, quantity, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, 1, now(), now() - interval '1 minute')
		`, newMigrationTestUUID(t), fixture.cartID, fixture.catalog.shopID, fixture.catalog.skuID)
		assertMigrationSQLState(t, err, "23514")
	})
}

// TestCartMigrationPreservesUnavailableSKUIntent xác nhận SKU inactive/archive không tự xóa CartItem và physical delete bị chặn.
func TestCartMigrationPreservesUnavailableSKUIntent(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	fixture := insertCartTestFixture(t, ctx, tx)
	insertCartTestItem(t, ctx, tx, fixture.cartID, fixture.catalog.shopID, fixture.catalog.skuID, 2)

	if _, err := tx.Exec(ctx, `
		UPDATE skus SET status = 'inactive', updated_at = now() WHERE id = $1
	`, fixture.catalog.skuID); err != nil {
		t.Fatalf("mark Cart SKU inactive: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE skus SET status = 'archived', archived_at = now(), updated_at = now() WHERE id = $1
	`, fixture.catalog.skuID); err != nil {
		t.Fatalf("archive Cart SKU: %v", err)
	}

	var itemCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM cart_items WHERE cart_id = $1 AND sku_id = $2
	`, fixture.cartID, fixture.catalog.skuID).Scan(&itemCount); err != nil {
		t.Fatalf("count CartItem after SKU lifecycle changes: %v", err)
	}
	if itemCount != 1 {
		t.Fatalf("CartItem count after SKU lifecycle changes = %d, want 1", itemCount)
	}

	_, err := tx.Exec(ctx, "DELETE FROM skus WHERE id = $1", fixture.catalog.skuID)
	assertMigrationSQLState(t, err, "23503")
}

// TestCartMigrationSupportsPartialCheckoutTransaction xác nhận transaction chỉ xóa item được chọn và chỉ terminal Cart khi đã rỗng.
func TestCartMigrationSupportsPartialCheckoutTransaction(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	first := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
	second := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
	third := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
	cartID := insertCartTestCart(t, ctx, tx, first.userID)
	firstItemID := insertCartTestItem(t, ctx, tx, cartID, first.shopID, first.skuID, 1)
	secondItemID := insertCartTestItem(t, ctx, tx, cartID, second.shopID, second.skuID, 1)
	thirdItemID := insertCartTestItem(t, ctx, tx, cartID, third.shopID, third.skuID, 1)

	if _, err := tx.Exec(ctx, `
		DELETE FROM cart_items
		WHERE cart_id = $1 AND id IN ($2, $3)
	`, cartID, firstItemID, secondItemID); err != nil {
		t.Fatalf("remove selected CartItems: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE carts
		SET status = 'checked_out', checked_out_at = now(), updated_at = now()
		WHERE id = $1
		  AND status = 'active'
		  AND NOT EXISTS (SELECT 1 FROM cart_items WHERE cart_id = $1)
	`, cartID); err != nil {
		t.Fatalf("conditionally complete non-empty Cart: %v", err)
	}

	var status string
	var remainingItemID string
	if err := tx.QueryRow(ctx, `
		SELECT c.status, ci.id
		FROM carts c
		JOIN cart_items ci ON ci.cart_id = c.id
		WHERE c.id = $1
	`, cartID).Scan(&status, &remainingItemID); err != nil {
		t.Fatalf("inspect partial checkout Cart: %v", err)
	}
	if status != "active" || remainingItemID != thirdItemID {
		t.Fatalf("partial checkout status = %s, remaining item = %s; want active and %s", status, remainingItemID, thirdItemID)
	}

	if _, err := tx.Exec(ctx, "DELETE FROM cart_items WHERE cart_id = $1 AND id = $2", cartID, thirdItemID); err != nil {
		t.Fatalf("remove final CartItem: %v", err)
	}
	commandTag, err := tx.Exec(ctx, `
		UPDATE carts
		SET status = 'checked_out', checked_out_at = now(), updated_at = now()
		WHERE id = $1
		  AND status = 'active'
		  AND NOT EXISTS (SELECT 1 FROM cart_items WHERE cart_id = $1)
	`, cartID)
	if err != nil {
		t.Fatalf("complete empty Cart: %v", err)
	}
	if commandTag.RowsAffected() != 1 {
		t.Fatalf("complete empty Cart affected %d rows, want 1", commandTag.RowsAffected())
	}
}

// TestCartMigrationConcurrentCreationAllowsOneActiveCart xác nhận nhiều process cạnh tranh chỉ tạo được một active Cart cho User.
func TestCartMigrationConcurrentCreationAllowsOneActiveCart(t *testing.T) {
	ctx, pool := openMigrationTestPool(t)
	setupTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin concurrent Cart creation setup: %v", err)
	}
	userID := insertSellerCatalogTestUser(t, ctx, setupTx)
	if err := setupTx.Commit(ctx); err != nil {
		t.Fatalf("commit concurrent Cart creation setup: %v", err)
	}
	registerCartUserCleanup(t, pool, userID)

	const attempts = 32
	start := make(chan struct{})
	results := make(chan cartConcurrentResult, attempts)
	var waitGroup sync.WaitGroup

	for index := 0; index < attempts; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start

			_, insertErr := pool.Exec(ctx, `
				INSERT INTO carts (id, user_id)
				VALUES ($1, $2)
			`, newMigrationTestUUID(t), userID)
			if insertErr == nil {
				results <- cartConcurrentResult{}
				return
			}
			if sqlState := cartTestSQLState(insertErr); sqlState != "" {
				results <- cartConcurrentResult{sqlState: sqlState}
				return
			}
			results <- cartConcurrentResult{err: insertErr}
		}()
	}

	close(start)
	waitGroup.Wait()
	close(results)

	successCount := 0
	conflictCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent active Cart creation: %v", result.err)
		}
		switch result.sqlState {
		case "":
			successCount++
		case "23505":
			conflictCount++
		default:
			t.Fatalf("concurrent active Cart SQLSTATE = %s, want 23505", result.sqlState)
		}
	}
	if successCount != 1 || conflictCount != attempts-1 {
		t.Fatalf("concurrent active Cart successes = %d, conflicts = %d; want 1 and %d", successCount, conflictCount, attempts-1)
	}
}

// TestCartMigrationAtomicAddCapsQuantityAtNinetyNine xác nhận concurrent UPSERT không lost update và không vượt quantity cap.
func TestCartMigrationAtomicAddCapsQuantityAtNinetyNine(t *testing.T) {
	ctx, pool := openMigrationTestPool(t)
	setupTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin concurrent CartItem setup: %v", err)
	}
	fixture := insertCartTestFixture(t, ctx, setupTx)
	if err := setupTx.Commit(ctx); err != nil {
		t.Fatalf("commit concurrent CartItem setup: %v", err)
	}
	registerCartCatalogCleanup(t, pool, fixture.cartID, []sellerCatalogTestFixture{fixture.catalog})

	const attempts = 99
	start := make(chan struct{})
	results := make(chan error, attempts)
	var waitGroup sync.WaitGroup

	for index := 0; index < attempts; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start

			var quantity int64
			results <- pool.QueryRow(ctx, `
				INSERT INTO cart_items (id, cart_id, shop_id, sku_id, quantity)
				VALUES ($1, $2, $3, $4, 1)
				ON CONFLICT (cart_id, sku_id)
				DO UPDATE
				SET quantity = cart_items.quantity + EXCLUDED.quantity,
				    updated_at = now()
				WHERE cart_items.quantity + EXCLUDED.quantity <= 99
				RETURNING quantity
			`, newMigrationTestUUID(t), fixture.cartID, fixture.catalog.shopID, fixture.catalog.skuID).Scan(&quantity)
		}()
	}

	close(start)
	waitGroup.Wait()
	close(results)
	for resultErr := range results {
		if resultErr != nil {
			t.Fatalf("concurrent CartItem add: %v", resultErr)
		}
	}

	var quantity int64
	if err := pool.QueryRow(ctx, `
		SELECT quantity FROM cart_items WHERE cart_id = $1 AND sku_id = $2
	`, fixture.cartID, fixture.catalog.skuID).Scan(&quantity); err != nil {
		t.Fatalf("read CartItem quantity after concurrent adds: %v", err)
	}
	if quantity != 99 {
		t.Fatalf("CartItem quantity after concurrent adds = %d, want 99", quantity)
	}

	err = pool.QueryRow(ctx, `
		INSERT INTO cart_items (id, cart_id, shop_id, sku_id, quantity)
		VALUES ($1, $2, $3, $4, 1)
		ON CONFLICT (cart_id, sku_id)
		DO UPDATE
		SET quantity = cart_items.quantity + EXCLUDED.quantity,
		    updated_at = now()
		WHERE cart_items.quantity + EXCLUDED.quantity <= 99
		RETURNING quantity
	`, newMigrationTestUUID(t), fixture.cartID, fixture.catalog.shopID, fixture.catalog.skuID).Scan(&quantity)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("CartItem increment above 99 error = %v, want pgx.ErrNoRows", err)
	}
}

// insertCartTestFixture tạo một Catalog graph và active Cart hợp lệ cho test.
func insertCartTestFixture(t *testing.T, ctx context.Context, tx pgx.Tx) cartTestFixture {
	t.Helper()

	catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
	return cartTestFixture{
		catalog: catalog,
		cartID:  insertCartTestCart(t, ctx, tx, catalog.userID),
	}
}

// insertCartTestCart tạo active global Cart cho User được chỉ định.
func insertCartTestCart(t *testing.T, ctx context.Context, tx pgx.Tx, userID string) string {
	t.Helper()

	cartID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO carts (id, user_id)
		VALUES ($1, $2)
	`, cartID, userID); err != nil {
		t.Fatalf("insert active Cart: %v", err)
	}

	return cartID
}

// insertCartTestItem tạo một CartItem hợp lệ với SKU/Shop và quantity do caller cung cấp.
func insertCartTestItem(t *testing.T, ctx context.Context, tx pgx.Tx, cartID, shopID, skuID string, quantity int64) string {
	t.Helper()

	itemID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO cart_items (id, cart_id, shop_id, sku_id, quantity)
		VALUES ($1, $2, $3, $4, $5)
	`, itemID, cartID, shopID, skuID, quantity); err != nil {
		t.Fatalf("insert CartItem: %v", err)
	}

	return itemID
}

// cartTestSQLState lấy SQLSTATE từ lỗi PostgreSQL để goroutine trả kết quả mà không gọi testing.T trực tiếp.
func cartTestSQLState(err error) string {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		return postgresError.Code
	}
	return ""
}

// registerCartUserCleanup đăng ký xóa Cart và User đã commit sau concurrency test.
func registerCartUserCleanup(t *testing.T, pool *pgxpool.Pool, userID string) {
	t.Helper()

	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()

		cleanupTx, err := pool.Begin(cleanupContext)
		if err != nil {
			t.Errorf("begin Cart user cleanup: %v", err)
			return
		}
		if _, err := cleanupTx.Exec(cleanupContext, "DELETE FROM cart_items WHERE cart_id IN (SELECT id FROM carts WHERE user_id = $1)", userID); err != nil {
			_ = cleanupTx.Rollback(cleanupContext)
			t.Errorf("clean CartItems by user: %v", err)
			return
		}
		if _, err := cleanupTx.Exec(cleanupContext, "DELETE FROM carts WHERE user_id = $1", userID); err != nil {
			_ = cleanupTx.Rollback(cleanupContext)
			t.Errorf("clean Carts by user: %v", err)
			return
		}
		if _, err := cleanupTx.Exec(cleanupContext, "DELETE FROM users WHERE id = $1", userID); err != nil {
			_ = cleanupTx.Rollback(cleanupContext)
			t.Errorf("clean Cart test User: %v", err)
			return
		}
		if err := cleanupTx.Commit(cleanupContext); err != nil {
			t.Errorf("commit Cart user cleanup: %v", err)
		}
	})
}

// registerCartCatalogCleanup đăng ký xóa Cart và Catalog graph đã commit theo thứ tự dependency-safe.
func registerCartCatalogCleanup(t *testing.T, pool *pgxpool.Pool, cartID string, catalogs []sellerCatalogTestFixture) {
	t.Helper()

	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()

		cleanupTx, err := pool.Begin(cleanupContext)
		if err != nil {
			t.Errorf("begin Cart Catalog cleanup: %v", err)
			return
		}
		if _, err := cleanupTx.Exec(cleanupContext, "DELETE FROM cart_items WHERE cart_id = $1", cartID); err != nil {
			_ = cleanupTx.Rollback(cleanupContext)
			t.Errorf("clean committed CartItems: %v", err)
			return
		}
		if _, err := cleanupTx.Exec(cleanupContext, "DELETE FROM carts WHERE id = $1", cartID); err != nil {
			_ = cleanupTx.Rollback(cleanupContext)
			t.Errorf("clean committed Cart: %v", err)
			return
		}

		for _, catalog := range catalogs {
			deletions := []struct {
				query string
				id    string
			}{
				{query: "DELETE FROM skus WHERE id = $1", id: catalog.skuID},
				{query: "DELETE FROM product_variants WHERE id = $1", id: catalog.variantID},
				{query: "DELETE FROM products WHERE id = $1", id: catalog.productID},
				{query: "DELETE FROM brands WHERE id = $1", id: catalog.brandID},
				{query: "DELETE FROM categories WHERE id = $1", id: catalog.categoryID},
				{query: "DELETE FROM shop_memberships WHERE shop_id = $1", id: catalog.shopID},
				{query: "DELETE FROM shops WHERE id = $1", id: catalog.shopID},
				{query: "DELETE FROM seller_accounts WHERE id = $1", id: catalog.sellerAccountID},
				{query: "DELETE FROM users WHERE id = $1", id: catalog.userID},
			}
			for _, deletion := range deletions {
				if _, err := cleanupTx.Exec(cleanupContext, deletion.query, deletion.id); err != nil {
					_ = cleanupTx.Rollback(cleanupContext)
					t.Errorf("clean committed Cart fixture with %q: %v", deletion.query, err)
					return
				}
			}
		}

		if err := cleanupTx.Commit(cleanupContext); err != nil {
			t.Errorf("commit Cart Catalog cleanup: %v", err)
		}
	})
}
