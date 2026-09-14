// File này kiểm thử migration Order bằng PostgreSQL thật, gồm hierarchy, tenant, snapshot, lifecycle, idempotency và status history.
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

// orderTestHierarchy giữ một Parent Order, Seller Order, OrderItem và Catalog graph hợp lệ dùng trong test.
type orderTestHierarchy struct {
	catalog       sellerCatalogTestFixture
	parentID      string
	sellerOrderID string
	itemID        string
}

// orderConcurrentResult giữ SQLSTATE hoặc lỗi ngoài PostgreSQL của một lần tạo Parent Order đồng thời.
type orderConcurrentResult struct {
	sqlState string
	err      error
}

// TestOrderMigrationCreatesExpectedObjects xác nhận ba bảng cùng các index và supporting key quan trọng của Order tồn tại.
func TestOrderMigrationCreatesExpectedObjects(t *testing.T) {
	ctx, tx := beginMigrationTest(t)

	objects := []string{
		"orders",
		"order_items",
		"order_status_histories",
		"uq_orders_order_number",
		"uq_orders_id_shop_id",
		"uq_orders_id_order_type",
		"uq_orders_id_user_currency_type",
		"uq_orders_id_user_checkout_currency_type",
		"uq_orders_id_currency_type",
		"uq_parent_order_checkout_reference",
		"uq_orders_parent_shop",
		"idx_orders_parent",
		"idx_orders_user_created",
		"idx_orders_shop_status_created",
		"uq_order_items_order_sku",
		"idx_order_items_order",
		"idx_order_items_sku",
		"idx_order_status_histories_order_created",
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

// TestOrderMigrationUsesCanonicalTypesAndDeleteRules xác nhận UUIDv7-by-Go, BIGINT money, TIMESTAMPTZ và RESTRICT đúng convention.
func TestOrderMigrationUsesCanonicalTypesAndDeleteRules(t *testing.T) {
	ctx, tx := beginMigrationTest(t)

	for _, table := range []string{"orders", "order_items", "order_status_histories"} {
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
		"fk_orders_user",
		"fk_orders_shop_currency",
		"fk_orders_parent_identity",
		"fk_order_items_order_shop",
		"fk_order_items_variant_product_shop",
		"fk_order_items_sku_variant_shop",
		"fk_order_status_histories_order",
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
	`, []string{"orders", "order_items", "order_status_histories"}).Scan(&invalidTimestampColumns); err != nil {
		t.Fatalf("inspect Order timestamps: %v", err)
	}
	if invalidTimestampColumns != 0 {
		t.Errorf("Order has %d non-TIMESTAMPTZ absolute-time columns", invalidTimestampColumns)
	}

	moneyColumns := []string{
		"subtotal_amount", "discount_amount", "tax_amount", "shipping_amount", "total_amount",
		"unit_price_amount", "line_subtotal_amount", "line_total_amount",
	}
	var moneyColumnCount int
	var nonBigintMoneyColumns int
	if err := tx.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE data_type <> 'bigint')
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name = ANY($1::text[])
		  AND column_name = ANY($2::text[])
	`, []string{"orders", "order_items"}, moneyColumns).Scan(&moneyColumnCount, &nonBigintMoneyColumns); err != nil {
		t.Fatalf("inspect Order money types: %v", err)
	}
	if moneyColumnCount != 10 {
		t.Errorf("Order money column count = %d, want 10", moneyColumnCount)
	}
	if nonBigintMoneyColumns != 0 {
		t.Errorf("Order has %d non-BIGINT money columns", nonBigintMoneyColumns)
	}

	var floatingMoneyColumns int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name = ANY($1::text[])
		  AND data_type IN ('real', 'double precision', 'numeric')
	`, []string{"orders", "order_items"}).Scan(&floatingMoneyColumns); err != nil {
		t.Fatalf("inspect floating Order columns: %v", err)
	}
	if floatingMoneyColumns != 0 {
		t.Errorf("Order has %d floating/numeric columns, want integer minor units only", floatingMoneyColumns)
	}
}

// TestOrderMigrationAcceptsValidMultiShopHierarchy xác nhận một Checkout tạo một Parent, nhiều Seller Orders, items và initial histories.
func TestOrderMigrationAcceptsValidMultiShopHierarchy(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	buyerID := insertSellerCatalogTestUser(t, ctx, tx)
	first := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
	second := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
	parentID := insertOrderTestParent(t, ctx, tx, buyerID, "VND", 200000)
	firstSellerID := insertOrderTestSeller(t, ctx, tx, parentID, buyerID, first.shopID, "VND", 100000)
	secondSellerID := insertOrderTestSeller(t, ctx, tx, parentID, buyerID, second.shopID, "VND", 100000)
	insertOrderTestItem(t, ctx, tx, firstSellerID, first, 1)
	insertOrderTestItem(t, ctx, tx, secondSellerID, second, 1)
	insertOrderTestHistory(t, ctx, tx, parentID, nil, "awaiting_payment", "system")
	insertOrderTestHistory(t, ctx, tx, firstSellerID, nil, "awaiting_payment", "system")
	insertOrderTestHistory(t, ctx, tx, secondSellerID, nil, "awaiting_payment", "system")

	var parentCount, sellerCount, itemCount, historyCount int
	if err := tx.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE order_type = 'parent'),
			count(*) FILTER (WHERE order_type = 'seller')
		FROM orders
		WHERE id = $1 OR parent_order_id = $1
	`, parentID).Scan(&parentCount, &sellerCount); err != nil {
		t.Fatalf("count multi-Shop Order hierarchy: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM order_items WHERE order_id = ANY($1::uuid[])
	`, []string{firstSellerID, secondSellerID}).Scan(&itemCount); err != nil {
		t.Fatalf("count multi-Shop OrderItems: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM order_status_histories WHERE order_id = ANY($1::uuid[])
	`, []string{parentID, firstSellerID, secondSellerID}).Scan(&historyCount); err != nil {
		t.Fatalf("count initial Order histories: %v", err)
	}

	if parentCount != 1 || sellerCount != 2 || itemCount != 2 || historyCount != 3 {
		t.Fatalf(
			"hierarchy counts parent=%d seller=%d item=%d history=%d, want 1/2/2/3",
			parentCount, sellerCount, itemCount, historyCount,
		)
	}
}

// TestOrderMigrationRejectsTypeInvalidStatuses xác nhận Parent và Seller chỉ dùng state space dành cho đúng order_type.
func TestOrderMigrationRejectsTypeInvalidStatuses(t *testing.T) {
	t.Run("parent cannot use seller status", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		userID := insertSellerCatalogTestUser(t, ctx, tx)
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (
				id, order_number, order_type, checkout_reference_id, user_id,
				status, currency, customer_name_snapshot, confirmed_at
			)
			VALUES ($1, $2, 'parent', $3, $4, 'shipped', 'VND', 'Buyer Snapshot', now())
		`, newMigrationTestUUID(t), orderTestNumber(t), newMigrationTestUUID(t), userID)
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("seller cannot use parent aggregate status", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		parentID := insertOrderTestParent(t, ctx, tx, catalog.userID, "VND", 0)
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (
				id, order_number, order_type, parent_order_id, parent_order_type,
				user_id, shop_id, status, currency, customer_name_snapshot,
				shop_name_snapshot, confirmed_at
			)
			VALUES (
				$1, $2, 'seller', $3, 'parent', $4, $5,
				'partially_completed', 'VND', 'Buyer Snapshot', 'Shop Snapshot', now()
			)
		`, newMigrationTestUUID(t), orderTestNumber(t), parentID, catalog.userID, catalog.shopID)
		assertMigrationSQLState(t, err, "23514")
	})
}

// TestOrderMigrationEnforcesParentSellerHierarchy xác nhận cấu trúc, Parent identity, checkout retry và Seller uniqueness ở DB.
func TestOrderMigrationEnforcesParentSellerHierarchy(t *testing.T) {
	t.Run("seller cannot reference seller as parent", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		first := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		second := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		parentID := insertOrderTestParent(t, ctx, tx, first.userID, "VND", 0)
		sellerID := insertOrderTestSeller(t, ctx, tx, parentID, first.userID, first.shopID, "VND", 0)
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (
				id, order_number, order_type, parent_order_id, parent_order_type,
				user_id, shop_id, currency, customer_name_snapshot, shop_name_snapshot
			)
			VALUES ($1, $2, 'seller', $3, 'parent', $4, $5, 'VND', 'Buyer Snapshot', 'Shop Snapshot')
		`, newMigrationTestUUID(t), orderTestNumber(t), sellerID, first.userID, second.shopID)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("seller must match parent user", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		otherUserID := insertSellerCatalogTestUser(t, ctx, tx)
		parentID := insertOrderTestParent(t, ctx, tx, catalog.userID, "VND", 0)
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (
				id, order_number, order_type, parent_order_id, parent_order_type,
				user_id, shop_id, currency, customer_name_snapshot, shop_name_snapshot
			)
			VALUES ($1, $2, 'seller', $3, 'parent', $4, $5, 'VND', 'Buyer Snapshot', 'Shop Snapshot')
		`, newMigrationTestUUID(t), orderTestNumber(t), parentID, otherUserID, catalog.shopID)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("seller must match parent currency", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		catalog := insertSellerCatalogTestFixture(t, ctx, tx, "USD")
		parentID := insertOrderTestParent(t, ctx, tx, catalog.userID, "VND", 0)
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (
				id, order_number, order_type, parent_order_id, parent_order_type,
				user_id, shop_id, currency, customer_name_snapshot, shop_name_snapshot
			)
			VALUES ($1, $2, 'seller', $3, 'parent', $4, $5, 'USD', 'Buyer Snapshot', 'Shop Snapshot')
		`, newMigrationTestUUID(t), orderTestNumber(t), parentID, catalog.userID, catalog.shopID)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("seller currency must match shop", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		catalog := insertSellerCatalogTestFixture(t, ctx, tx, "USD")
		parentID := insertOrderTestParent(t, ctx, tx, catalog.userID, "VND", 0)
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (
				id, order_number, order_type, parent_order_id, parent_order_type,
				user_id, shop_id, currency, customer_name_snapshot, shop_name_snapshot
			)
			VALUES ($1, $2, 'seller', $3, 'parent', $4, $5, 'VND', 'Buyer Snapshot', 'Shop Snapshot')
		`, newMigrationTestUUID(t), orderTestNumber(t), parentID, catalog.userID, catalog.shopID)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("parent structure rejects shop ownership", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (
				id, order_number, order_type, checkout_reference_id, user_id,
				shop_id, currency, customer_name_snapshot, shop_name_snapshot
			)
			VALUES ($1, $2, 'parent', $3, $4, $5, 'VND', 'Buyer Snapshot', 'Shop Snapshot')
		`, newMigrationTestUUID(t), orderTestNumber(t), newMigrationTestUUID(t), catalog.userID, catalog.shopID)
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("checkout reference creates one parent", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		userID := insertSellerCatalogTestUser(t, ctx, tx)
		checkoutReferenceID := newMigrationTestUUID(t)
		if _, err := tx.Exec(ctx, `
			INSERT INTO orders (
				id, order_number, order_type, checkout_reference_id,
				user_id, currency, customer_name_snapshot
			)
			VALUES ($1, $2, 'parent', $3, $4, 'VND', 'Buyer Snapshot')
		`, newMigrationTestUUID(t), orderTestNumber(t), checkoutReferenceID, userID); err != nil {
			t.Fatalf("insert first Parent Order: %v", err)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (
				id, order_number, order_type, checkout_reference_id,
				user_id, currency, customer_name_snapshot
			)
			VALUES ($1, $2, 'parent', $3, $4, 'VND', 'Buyer Snapshot')
		`, newMigrationTestUUID(t), orderTestNumber(t), checkoutReferenceID, userID)
		assertMigrationSQLState(t, err, "23505")
	})

	t.Run("one seller order per parent and shop", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		parentID := insertOrderTestParent(t, ctx, tx, catalog.userID, "VND", 0)
		insertOrderTestSeller(t, ctx, tx, parentID, catalog.userID, catalog.shopID, "VND", 0)
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (
				id, order_number, order_type, parent_order_id, parent_order_type,
				user_id, shop_id, currency, customer_name_snapshot, shop_name_snapshot
			)
			VALUES ($1, $2, 'seller', $3, 'parent', $4, $5, 'VND', 'Buyer Snapshot', 'Shop Snapshot')
		`, newMigrationTestUUID(t), orderTestNumber(t), parentID, catalog.userID, catalog.shopID)
		assertMigrationSQLState(t, err, "23505")
	})
}

// TestOrderMigrationEnforcesOrderLifecycleAndMoney xác nhận timestamps và công thức money snapshot luôn nhất quán.
func TestOrderMigrationEnforcesOrderLifecycleAndMoney(t *testing.T) {
	t.Run("confirmed status requires timestamp", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		userID := insertSellerCatalogTestUser(t, ctx, tx)
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (
				id, order_number, order_type, checkout_reference_id, user_id,
				status, currency, customer_name_snapshot
			)
			VALUES ($1, $2, 'parent', $3, $4, 'confirmed', 'VND', 'Buyer Snapshot')
		`, newMigrationTestUUID(t), orderTestNumber(t), newMigrationTestUUID(t), userID)
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("lifecycle timestamp cannot precede creation", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		userID := insertSellerCatalogTestUser(t, ctx, tx)
		createdAt := time.Now().UTC()
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (
				id, order_number, order_type, checkout_reference_id, user_id,
				status, currency, customer_name_snapshot, created_at, updated_at, confirmed_at
			)
			VALUES ($1, $2, 'parent', $3, $4, 'confirmed', 'VND', 'Buyer Snapshot', $5, $5, $6)
		`, newMigrationTestUUID(t), orderTestNumber(t), newMigrationTestUUID(t), userID, createdAt, createdAt.Add(-time.Second))
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("order total must follow formula", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		userID := insertSellerCatalogTestUser(t, ctx, tx)
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (
				id, order_number, order_type, checkout_reference_id, user_id, currency,
				subtotal_amount, discount_amount, tax_amount, shipping_amount, total_amount,
				customer_name_snapshot
			)
			VALUES ($1, $2, 'parent', $3, $4, 'VND', 100, 10, 5, 5, 101, 'Buyer Snapshot')
		`, newMigrationTestUUID(t), orderTestNumber(t), newMigrationTestUUID(t), userID)
		assertMigrationSQLState(t, err, "23514")
	})
}

// TestOrderMigrationEnforcesOrderItemInvariants xác nhận OrderItem chỉ thuộc đúng Seller/Shop/SKU và giữ quantity, JSON, money hợp lệ.
func TestOrderMigrationEnforcesOrderItemInvariants(t *testing.T) {
	t.Run("item cannot attach directly to parent", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		parentID := insertOrderTestParent(t, ctx, tx, catalog.userID, "VND", 100000)
		_, err := insertOrderTestItemSQL(ctx, tx, newMigrationTestUUID(t), parentID, catalog, 1, 100000, 100000, 100000, `{"size":"default"}`)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("cross shop item rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		first := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		second := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		parentID := insertOrderTestParent(t, ctx, tx, first.userID, "VND", 100000)
		sellerID := insertOrderTestSeller(t, ctx, tx, parentID, first.userID, first.shopID, "VND", 100000)
		_, err := insertOrderTestItemSQL(ctx, tx, newMigrationTestUUID(t), sellerID, second, 1, 100000, 100000, 100000, `{"size":"default"}`)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("SKU shop mismatch rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		first := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		second := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		parentID := insertOrderTestParent(t, ctx, tx, first.userID, "VND", 100000)
		sellerID := insertOrderTestSeller(t, ctx, tx, parentID, first.userID, first.shopID, "VND", 100000)
		mismatched := first
		mismatched.skuID = second.skuID
		_, err := insertOrderTestItemSQL(ctx, tx, newMigrationTestUUID(t), sellerID, mismatched, 1, 100000, 100000, 100000, `{"size":"default"}`)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("variant must match product", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		otherProductID := insertSellerCatalogTestProduct(t, ctx, tx, catalog.shopID, catalog.categoryID, catalog.brandID)
		parentID := insertOrderTestParent(t, ctx, tx, catalog.userID, "VND", 100000)
		sellerID := insertOrderTestSeller(t, ctx, tx, parentID, catalog.userID, catalog.shopID, "VND", 100000)
		catalog.productID = otherProductID
		_, err := insertOrderTestItemSQL(ctx, tx, newMigrationTestUUID(t), sellerID, catalog, 1, 100000, 100000, 100000, `{"size":"default"}`)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("same SKU appears once per seller order", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		hierarchy := insertOrderTestHierarchy(t, ctx, tx)
		_, err := insertOrderTestItemSQL(ctx, tx, newMigrationTestUUID(t), hierarchy.sellerOrderID, hierarchy.catalog, 1, 100000, 100000, 100000, `{"size":"default"}`)
		assertMigrationSQLState(t, err, "23505")
	})

	for _, quantity := range []int64{0, 100} {
		quantity := quantity
		t.Run("quantity outside range rejected", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
			parentID := insertOrderTestParent(t, ctx, tx, catalog.userID, "VND", 100000)
			sellerID := insertOrderTestSeller(t, ctx, tx, parentID, catalog.userID, catalog.shopID, "VND", 100000)
			_, err := insertOrderTestItemSQL(ctx, tx, newMigrationTestUUID(t), sellerID, catalog, quantity, 100000, 100000*quantity, 100000*quantity, `{"size":"default"}`)
			assertMigrationSQLState(t, err, "23514")
		})
	}

	t.Run("line subtotal formula enforced", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		parentID := insertOrderTestParent(t, ctx, tx, catalog.userID, "VND", 100000)
		sellerID := insertOrderTestSeller(t, ctx, tx, parentID, catalog.userID, catalog.shopID, "VND", 100000)
		_, err := insertOrderTestItemSQL(ctx, tx, newMigrationTestUUID(t), sellerID, catalog, 2, 100000, 100000, 100000, `{"size":"default"}`)
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("line total formula enforced", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		parentID := insertOrderTestParent(t, ctx, tx, catalog.userID, "VND", 100000)
		sellerID := insertOrderTestSeller(t, ctx, tx, parentID, catalog.userID, catalog.shopID, "VND", 100000)
		_, err := insertOrderTestItemSQL(ctx, tx, newMigrationTestUUID(t), sellerID, catalog, 1, 100000, 100000, 99999, `{"size":"default"}`)
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("attributes snapshot must be object", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		parentID := insertOrderTestParent(t, ctx, tx, catalog.userID, "VND", 100000)
		sellerID := insertOrderTestSeller(t, ctx, tx, parentID, catalog.userID, catalog.shopID, "VND", 100000)
		_, err := insertOrderTestItemSQL(ctx, tx, newMigrationTestUUID(t), sellerID, catalog, 1, 100000, 100000, 100000, `[]`)
		assertMigrationSQLState(t, err, "23514")
	})
}

// TestOrderMigrationPreservesHistoricalSnapshots xác nhận thay đổi User, Shop và Catalog không rewrite commercial history đã chốt.
func TestOrderMigrationPreservesHistoricalSnapshots(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	hierarchy := insertOrderTestHierarchy(t, ctx, tx)

	if _, err := tx.Exec(ctx, "UPDATE users SET display_name = 'Changed Buyer' WHERE id = $1", hierarchy.catalog.userID); err != nil {
		t.Fatalf("rename live User: %v", err)
	}
	if _, err := tx.Exec(ctx, "UPDATE shops SET name = 'Changed Shop' WHERE id = $1", hierarchy.catalog.shopID); err != nil {
		t.Fatalf("rename live Shop: %v", err)
	}
	if _, err := tx.Exec(ctx, "UPDATE products SET name = 'Changed Product' WHERE id = $1", hierarchy.catalog.productID); err != nil {
		t.Fatalf("rename live Product: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE skus
		SET sku_code = $2, price_amount_minor = 150000,
			compare_at_price_amount_minor = 160000,
			status = 'archived', archived_at = now()
		WHERE id = $1
	`, hierarchy.catalog.skuID, "ARCHIVED-"+sellerCatalogCompactID(hierarchy.catalog.skuID)[:16]); err != nil {
		t.Fatalf("archive live SKU: %v", err)
	}

	var customerSnapshot, shopSnapshot, productSnapshot, skuSnapshot string
	var unitPrice int64
	if err := tx.QueryRow(ctx, `
		SELECT
			parent.customer_name_snapshot,
			seller.shop_name_snapshot,
			item.product_name_snapshot,
			item.sku_code_snapshot,
			item.unit_price_amount
		FROM orders AS parent
		JOIN orders AS seller ON seller.parent_order_id = parent.id
		JOIN order_items AS item ON item.order_id = seller.id
		WHERE parent.id = $1
	`, hierarchy.parentID).Scan(&customerSnapshot, &shopSnapshot, &productSnapshot, &skuSnapshot, &unitPrice); err != nil {
		t.Fatalf("read historical Order snapshots: %v", err)
	}
	if customerSnapshot != "Buyer Snapshot" || shopSnapshot != "Shop Snapshot" ||
		productSnapshot != "Product Snapshot" || skuSnapshot != "SKU-SNAPSHOT" || unitPrice != 100000 {
		t.Fatalf(
			"snapshots changed to customer=%q shop=%q product=%q sku=%q price=%d",
			customerSnapshot, shopSnapshot, productSnapshot, skuSnapshot, unitPrice,
		)
	}

	if _, err := tx.Exec(ctx, "SAVEPOINT delete_archived_sku"); err != nil {
		t.Fatalf("create SKU delete savepoint: %v", err)
	}
	_, deleteErr := tx.Exec(ctx, "DELETE FROM skus WHERE id = $1", hierarchy.catalog.skuID)
	assertMigrationSQLState(t, deleteErr, "23503")
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT delete_archived_sku"); err != nil {
		t.Fatalf("rollback rejected SKU delete: %v", err)
	}
}

// TestOrderMigrationKeepsStatusAndHistoryAtomic xác nhận status hiện tại và audit history có thể commit hoặc rollback cùng transaction.
func TestOrderMigrationKeepsStatusAndHistoryAtomic(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	hierarchy := insertOrderTestHierarchy(t, ctx, tx)

	if _, err := tx.Exec(ctx, "SAVEPOINT attempted_transition"); err != nil {
		t.Fatalf("create status transition savepoint: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE orders
		SET status = 'confirmed', confirmed_at = now(), updated_at = now()
		WHERE id = $1
	`, hierarchy.parentID); err != nil {
		t.Fatalf("update Parent status inside attempted transition: %v", err)
	}
	fromStatus := "awaiting_payment"
	insertOrderTestHistory(t, ctx, tx, hierarchy.parentID, &fromStatus, "confirmed", "payment")
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT attempted_transition"); err != nil {
		t.Fatalf("rollback attempted status transition: %v", err)
	}

	var status string
	var historyCount int
	if err := tx.QueryRow(ctx, "SELECT status FROM orders WHERE id = $1", hierarchy.parentID).Scan(&status); err != nil {
		t.Fatalf("read rolled back Parent status: %v", err)
	}
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM order_status_histories WHERE order_id = $1", hierarchy.parentID).Scan(&historyCount); err != nil {
		t.Fatalf("count rolled back Parent histories: %v", err)
	}
	if status != "awaiting_payment" || historyCount != 1 {
		t.Fatalf("rolled back transition left status=%q historyCount=%d, want awaiting_payment/1", status, historyCount)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE orders
		SET status = 'confirmed', confirmed_at = now(), updated_at = now()
		WHERE id = $1
	`, hierarchy.parentID); err != nil {
		t.Fatalf("confirm Parent Order: %v", err)
	}
	insertOrderTestHistory(t, ctx, tx, hierarchy.parentID, &fromStatus, "confirmed", "payment")
	if err := tx.QueryRow(ctx, "SELECT status FROM orders WHERE id = $1", hierarchy.parentID).Scan(&status); err != nil {
		t.Fatalf("read confirmed Parent status: %v", err)
	}
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM order_status_histories WHERE order_id = $1", hierarchy.parentID).Scan(&historyCount); err != nil {
		t.Fatalf("count committed Parent histories: %v", err)
	}
	if status != "confirmed" || historyCount != 2 {
		t.Fatalf("successful transition left status=%q historyCount=%d, want confirmed/2", status, historyCount)
	}
}

// TestOrderMigrationRejectsInvalidStatusHistory xác nhận history chỉ nhận status và actor type thuộc domain vocabulary.
func TestOrderMigrationRejectsInvalidStatusHistory(t *testing.T) {
	t.Run("invalid destination status", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		hierarchy := insertOrderTestHierarchy(t, ctx, tx)
		_, err := tx.Exec(ctx, `
			INSERT INTO order_status_histories (id, order_id, to_status, actor_type)
			VALUES ($1, $2, 'unknown', 'system')
		`, newMigrationTestUUID(t), hierarchy.parentID)
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("invalid actor type", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		hierarchy := insertOrderTestHierarchy(t, ctx, tx)
		_, err := tx.Exec(ctx, `
			INSERT INTO order_status_histories (id, order_id, from_status, to_status, actor_type)
			VALUES ($1, $2, 'awaiting_payment', 'confirmed', 'warehouse')
		`, newMigrationTestUUID(t), hierarchy.parentID)
		assertMigrationSQLState(t, err, "23514")
	})
}

// TestOrderMigrationConcurrentCheckoutCreatesOneParent xác nhận unique checkout identity giữ đúng khi nhiều process insert đồng thời.
func TestOrderMigrationConcurrentCheckoutCreatesOneParent(t *testing.T) {
	ctx, pool := openMigrationTestPool(t)
	userID := newMigrationTestUUID(t)
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, display_name)
		VALUES ($1, 'Concurrent Order Buyer')
	`, userID); err != nil {
		t.Fatalf("insert concurrent Order buyer: %v", err)
	}
	registerOrderUserCleanup(t, pool, userID)

	checkoutReferenceID := newMigrationTestUUID(t)
	const attempts = 32
	type attemptInput struct {
		id          string
		orderNumber string
	}
	inputs := make([]attemptInput, attempts)
	for index := range inputs {
		inputs[index] = attemptInput{id: newMigrationTestUUID(t), orderNumber: orderTestNumber(t)}
	}

	results := make(chan orderConcurrentResult, attempts)
	var waitGroup sync.WaitGroup
	for _, input := range inputs {
		input := input
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, err := pool.Exec(ctx, `
				INSERT INTO orders (
					id, order_number, order_type, checkout_reference_id,
					user_id, currency, customer_name_snapshot
				)
				VALUES ($1, $2, 'parent', $3, $4, 'VND', 'Concurrent Buyer Snapshot')
			`, input.id, input.orderNumber, checkoutReferenceID, userID)
			results <- orderConcurrentResult{sqlState: orderTestSQLState(err), err: err}
		}()
	}
	waitGroup.Wait()
	close(results)

	successCount := 0
	uniqueViolationCount := 0
	for result := range results {
		switch {
		case result.err == nil:
			successCount++
		case result.sqlState == "23505":
			uniqueViolationCount++
		default:
			t.Fatalf("concurrent Parent insert failed unexpectedly: %v", result.err)
		}
	}
	if successCount != 1 || uniqueViolationCount != attempts-1 {
		t.Fatalf("concurrent Parent results success=%d unique=%d, want 1/%d", successCount, uniqueViolationCount, attempts-1)
	}

	var storedCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM orders WHERE checkout_reference_id = $1", checkoutReferenceID).Scan(&storedCount); err != nil {
		t.Fatalf("count concurrently inserted Parent Orders: %v", err)
	}
	if storedCount != 1 {
		t.Fatalf("stored Parent Orders = %d, want 1", storedCount)
	}
}

// insertOrderTestHierarchy tạo một graph Order hợp lệ với snapshots và initial histories cho các test hành vi.
func insertOrderTestHierarchy(t *testing.T, ctx context.Context, tx pgx.Tx) orderTestHierarchy {
	t.Helper()

	catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
	parentID := insertOrderTestParent(t, ctx, tx, catalog.userID, "VND", 100000)
	sellerOrderID := insertOrderTestSeller(t, ctx, tx, parentID, catalog.userID, catalog.shopID, "VND", 100000)
	itemID := insertOrderTestItem(t, ctx, tx, sellerOrderID, catalog, 1)
	insertOrderTestHistory(t, ctx, tx, parentID, nil, "awaiting_payment", "system")
	insertOrderTestHistory(t, ctx, tx, sellerOrderID, nil, "awaiting_payment", "system")

	return orderTestHierarchy{
		catalog:       catalog,
		parentID:      parentID,
		sellerOrderID: sellerOrderID,
		itemID:        itemID,
	}
}

// insertOrderTestParent tạo Parent Order hợp lệ, sở hữu checkout correlation và không gắn trực tiếp với Shop.
func insertOrderTestParent(t *testing.T, ctx context.Context, tx pgx.Tx, userID, currency string, total int64) string {
	t.Helper()

	orderID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO orders (
			id, order_number, order_type, checkout_reference_id, user_id, currency,
			subtotal_amount, total_amount, customer_name_snapshot,
			customer_email_snapshot, customer_phone_snapshot
		)
		VALUES ($1, $2, 'parent', $3, $4, $5, $6, $6, 'Buyer Snapshot', 'buyer@example.com', '0900000000')
	`, orderID, orderTestNumber(t), newMigrationTestUUID(t), userID, currency, total); err != nil {
		t.Fatalf("insert Parent Order: %v", err)
	}

	return orderID
}

// insertOrderTestSeller tạo Seller Order hợp lệ và để composite FK chứng minh Parent, buyer, currency cùng Shop currency.
func insertOrderTestSeller(t *testing.T, ctx context.Context, tx pgx.Tx, parentID, userID, shopID, currency string, total int64) string {
	t.Helper()

	orderID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO orders (
			id, order_number, order_type, parent_order_id, parent_order_type,
			user_id, shop_id, currency, subtotal_amount, total_amount,
			customer_name_snapshot, customer_email_snapshot, customer_phone_snapshot,
			shipping_recipient_name, shipping_phone, shipping_address_line1,
			shipping_ward, shipping_district, shipping_city, shipping_country_code,
			shop_name_snapshot
		)
		VALUES (
			$1, $2, 'seller', $3, 'parent', $4, $5, $6, $7, $7,
			'Buyer Snapshot', 'buyer@example.com', '0900000000',
			'Buyer Snapshot', '0900000000', '123 Test Street',
			'Test Ward', 'Test District', 'Ho Chi Minh City', 'VN',
			'Shop Snapshot'
		)
	`, orderID, orderTestNumber(t), parentID, userID, shopID, currency, total); err != nil {
		t.Fatalf("insert Seller Order: %v", err)
	}

	return orderID
}

// insertOrderTestItem tạo immutable OrderItem snapshot hợp lệ cho SKU cuối cùng của Catalog fixture.
func insertOrderTestItem(t *testing.T, ctx context.Context, tx pgx.Tx, orderID string, catalog sellerCatalogTestFixture, quantity int64) string {
	t.Helper()

	itemID := newMigrationTestUUID(t)
	lineSubtotal := int64(100000) * quantity
	if _, err := insertOrderTestItemSQL(
		ctx, tx, itemID, orderID, catalog, quantity,
		100000, lineSubtotal, lineSubtotal, `{"size":"default"}`,
	); err != nil {
		t.Fatalf("insert OrderItem: %v", err)
	}

	return itemID
}

// insertOrderTestItemSQL thực thi INSERT OrderItem tùy biến để cả test valid và invalid dùng cùng shape dữ liệu.
func insertOrderTestItemSQL(
	ctx context.Context,
	tx pgx.Tx,
	itemID string,
	orderID string,
	catalog sellerCatalogTestFixture,
	quantity int64,
	unitPrice int64,
	lineSubtotal int64,
	lineTotal int64,
	attributesJSON string,
) (pgconn.CommandTag, error) {
	return tx.Exec(ctx, `
		INSERT INTO order_items (
			id, order_id, shop_id, product_id, variant_id, sku_id,
			sku_code_snapshot, product_name_snapshot, variant_name_snapshot,
			sku_name_snapshot, attributes_snapshot, quantity, unit_price_amount,
			discount_amount, tax_amount, line_subtotal_amount, line_total_amount
		)
		VALUES (
			$1, $2, $3, $4, $5, $6,
			'SKU-SNAPSHOT', 'Product Snapshot', 'Variant Snapshot',
			'SKU Snapshot', $7::jsonb, $8, $9,
			0, 0, $10, $11
		)
	`, itemID, orderID, catalog.shopID, catalog.productID, catalog.variantID, catalog.skuID,
		attributesJSON, quantity, unitPrice, lineSubtotal, lineTotal)
}

// insertOrderTestHistory thêm một immutable status transition record cho Order được chỉ định.
func insertOrderTestHistory(t *testing.T, ctx context.Context, tx pgx.Tx, orderID string, fromStatus *string, toStatus, actorType string) string {
	t.Helper()

	historyID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_status_histories (
			id, order_id, from_status, to_status, actor_type
		)
		VALUES ($1, $2, $3, $4, $5)
	`, historyID, orderID, fromStatus, toStatus, actorType); err != nil {
		t.Fatalf("insert Order status history: %v", err)
	}

	return historyID
}

// orderTestNumber tạo human-facing order number duy nhất, ngắn hơn giới hạn VARCHAR(64).
func orderTestNumber(t *testing.T) string {
	t.Helper()

	return "ORDER-" + sellerCatalogCompactID(newMigrationTestUUID(t))
}

// orderTestSQLState lấy SQLSTATE từ lỗi PostgreSQL để goroutine trả kết quả mà không gọi testing.T.
func orderTestSQLState(err error) string {
	if err == nil {
		return ""
	}

	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return ""
	}

	return postgresError.Code
}

// registerOrderUserCleanup xóa dữ liệu concurrency test đã commit theo đúng thứ tự rồi mới đóng pool.
func registerOrderUserCleanup(t *testing.T, pool *pgxpool.Pool, userID string) {
	t.Helper()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, err := pool.Exec(ctx, "DELETE FROM orders WHERE user_id = $1", userID); err != nil {
			t.Errorf("cleanup concurrent Parent Orders: %v", err)
			return
		}
		if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID); err != nil {
			t.Errorf("cleanup concurrent Order buyer: %v", err)
		}
	})
}
