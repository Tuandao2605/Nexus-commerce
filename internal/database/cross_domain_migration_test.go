// File này kiểm thử các invariant xuyên domain của DB-004I trên PostgreSQL thật: tenant, checkout, parent safety, deadline và idempotency.
package database

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestCrossDomainTenantConsistency chứng minh một graph hợp lệ dùng cùng Shop và database từ chối mọi attachment chéo Shop.
func TestCrossDomainTenantConsistency(t *testing.T) {
	t.Run("accept consistent Catalog Inventory Cart and Order graph", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		warehouseID := insertInventoryTestWarehouse(t, ctx, tx, catalog.shopID, "CROSS-DOMAIN-WH")
		stockID := insertInventoryTestStock(t, ctx, tx, catalog.shopID, warehouseID, catalog.skuID, 10, 0)
		cartID := insertCartTestCart(t, ctx, tx, catalog.userID)
		cartItemID := insertCartTestItem(t, ctx, tx, cartID, catalog.shopID, catalog.skuID, 1)
		parentID := insertOrderTestParent(t, ctx, tx, catalog.userID, "VND", 100000)
		sellerOrderID := insertOrderTestSeller(t, ctx, tx, parentID, catalog.userID, catalog.shopID, "VND", 100000)
		orderItemID := insertOrderTestItem(t, ctx, tx, sellerOrderID, catalog, 1)

		var matchingRows int
		if err := tx.QueryRow(ctx, `
			SELECT count(*)
			FROM skus AS sku
			JOIN inventory_stocks AS stock
			  ON stock.sku_id = sku.id AND stock.shop_id = sku.shop_id
			JOIN cart_items AS cart_item
			  ON cart_item.sku_id = sku.id AND cart_item.shop_id = sku.shop_id
			JOIN order_items AS order_item
			  ON order_item.sku_id = sku.id AND order_item.shop_id = sku.shop_id
			JOIN orders AS seller_order
			  ON seller_order.id = order_item.order_id AND seller_order.shop_id = order_item.shop_id
			WHERE sku.id = $1
			  AND stock.id = $2
			  AND cart_item.id = $3
			  AND order_item.id = $4
		`, catalog.skuID, stockID, cartItemID, orderItemID).Scan(&matchingRows); err != nil {
			t.Fatalf("verify cross-domain tenant graph: %v", err)
		}
		if matchingRows != 1 {
			t.Fatalf("matching cross-domain tenant rows = %d, want 1", matchingRows)
		}
	})

	t.Run("reject cross-Shop InventoryStock", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		catalogA := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		catalogB := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		warehouseID := insertInventoryTestWarehouse(t, ctx, tx, catalogA.shopID, "SHOP-A-WH")

		_, err := tx.Exec(ctx, `
			INSERT INTO inventory_stocks (
				id, shop_id, warehouse_id, sku_id, on_hand_quantity, reserved_quantity
			)
			VALUES ($1, $2, $3, $4, 10, 0)
		`, newMigrationTestUUID(t), catalogA.shopID, warehouseID, catalogB.skuID)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("reject cross-Shop CartItem", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		catalogA := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		catalogB := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		cartID := insertCartTestCart(t, ctx, tx, catalogA.userID)

		_, err := tx.Exec(ctx, `
			INSERT INTO cart_items (id, cart_id, shop_id, sku_id, quantity)
			VALUES ($1, $2, $3, $4, 1)
		`, newMigrationTestUUID(t), cartID, catalogA.shopID, catalogB.skuID)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("reject cross-Shop OrderItem Catalog chain", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		catalogA := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		catalogB := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		parentID := insertOrderTestParent(t, ctx, tx, catalogA.userID, "VND", 100000)
		sellerOrderID := insertOrderTestSeller(t, ctx, tx, parentID, catalogA.userID, catalogA.shopID, "VND", 100000)
		catalogB.shopID = catalogA.shopID

		_, err := insertOrderTestItemSQL(
			ctx, tx, newMigrationTestUUID(t), sellerOrderID, catalogB,
			1, 100000, 100000, 100000, `{"size":"cross-shop"}`,
		)
		assertMigrationSQLState(t, err, "23503")
	})
}

// TestCrossDomainCheckoutCorrelationAndRetrySafety chứng minh các resource cùng checkout spine và retry không tạo allocation hay Parent Order thứ hai.
func TestCrossDomainCheckoutCorrelationAndRetrySafety(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
	checkoutReferenceID := newMigrationTestUUID(t)
	idempotencyKey := inventoryTestIdempotencyKey(t)
	reservationID := insertCrossDomainInventoryReservationTest(
		t, ctx, tx, checkoutReferenceID, idempotencyKey, "15 minutes",
	)
	warehouseID := insertInventoryTestWarehouse(t, ctx, tx, catalog.shopID, "CHECKOUT-WH")
	stockID := insertInventoryTestStock(t, ctx, tx, catalog.shopID, warehouseID, catalog.skuID, 10, 1)
	insertInventoryTestReservationItem(t, ctx, tx, reservationID, stockID, 1)

	voucherID := newMigrationTestUUID(t)
	if _, err := insertVoucherTestRow(
		ctx, tx, voucherID, "CHECKOUT-"+strings.ToUpper(sellerCatalogCompactID(voucherID)[:12]),
		"platform", nil, "VND", "fixed_amount", 10000, nil, 10,
	); err != nil {
		t.Fatalf("insert cross-domain Voucher: %v", err)
	}
	if err := activateVoucherTestRow(ctx, tx, voucherID); err != nil {
		t.Fatalf("activate cross-domain Voucher: %v", err)
	}
	voucherUsageID, created, err := reserveVoucherUsageTest(
		ctx, tx, voucherID, catalog.userID, checkoutReferenceID, newMigrationTestUUID(t), 10000,
	)
	if err != nil {
		t.Fatalf("reserve cross-domain Voucher: %v", err)
	}
	if !created {
		t.Fatal("first Voucher reservation created = false, want true")
	}

	parentID := insertVoucherTestParentOrder(t, ctx, tx, catalog.userID, checkoutReferenceID, "VND")
	sellerOrderID := insertVoucherTestSellerOrder(t, ctx, tx, parentID, catalog.userID, catalog.shopID, "VND")
	insertOrderTestItem(t, ctx, tx, sellerOrderID, catalog, 1)
	committed, err := commitVoucherUsageTest(ctx, tx, voucherUsageID, parentID)
	if err != nil {
		t.Fatalf("commit cross-domain Voucher usage: %v", err)
	}
	if !committed {
		t.Fatal("Voucher usage committed = false, want true")
	}
	paymentID := insertCrossDomainPaymentTest(t, ctx, tx, parentID, "VND", "pending")

	var correlatedRows int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM inventory_reservations AS inventory
		JOIN voucher_usages AS voucher
		  ON voucher.checkout_reference_id = inventory.reference_id
		JOIN orders AS parent_order
		  ON parent_order.checkout_reference_id = inventory.reference_id
		 AND parent_order.id = voucher.order_id
		 AND parent_order.user_id = voucher.user_id
		 AND parent_order.currency = voucher.currency_code
		JOIN payment_transactions AS payment
		  ON payment.parent_order_id = parent_order.id
		 AND payment.currency_code = parent_order.currency
		WHERE inventory.id = $1
		  AND inventory.reference_type = 'checkout'
		  AND payment.id = $2
	`, reservationID, paymentID).Scan(&correlatedRows); err != nil {
		t.Fatalf("verify checkout correlation spine: %v", err)
	}
	if correlatedRows != 1 {
		t.Fatalf("correlated checkout rows = %d, want 1", correlatedRows)
	}

	assertCrossDomainTransactionSQLState(t, ctx, tx, "23505", func() error {
		_, err := tx.Exec(ctx, `
			INSERT INTO inventory_reservations (
				id, reference_type, reference_id, idempotency_key, request_hash, expires_at
			)
			VALUES ($1, 'checkout', $2, $3, repeat('b', 64), clock_timestamp() + interval '15 minutes')
		`, newMigrationTestUUID(t), checkoutReferenceID, idempotencyKey)
		return err
	})

	retriedUsageID, retriedCreated, err := reserveVoucherUsageTest(
		ctx, tx, voucherID, catalog.userID, checkoutReferenceID, newMigrationTestUUID(t), 10000,
	)
	if err != nil {
		t.Fatalf("retry cross-domain Voucher reservation: %v", err)
	}
	if retriedCreated || retriedUsageID != voucherUsageID {
		t.Fatalf("Voucher retry = (%s, %t), want existing (%s, false)", retriedUsageID, retriedCreated, voucherUsageID)
	}

	assertCrossDomainTransactionSQLState(t, ctx, tx, "23505", func() error {
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (
				id, order_number, order_type, checkout_reference_id, user_id, currency,
				subtotal_amount, total_amount, customer_name_snapshot
			)
			VALUES ($1, $2, 'parent', $3, $4, 'VND', 100000, 100000, 'Retry Buyer')
		`, newMigrationTestUUID(t), orderTestNumber(t), checkoutReferenceID, catalog.userID)
		return err
	})

	var reservationCount, usageCount, parentCount, allocatedUsageCount int
	if err := tx.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM inventory_reservations WHERE idempotency_key = $1),
			(SELECT count(*) FROM voucher_usages WHERE voucher_id = $2 AND checkout_reference_id = $3),
			(SELECT count(*) FROM orders WHERE order_type = 'parent' AND checkout_reference_id = $3),
			(SELECT allocated_usage_count FROM vouchers WHERE id = $2)
	`, idempotencyKey, voucherID, checkoutReferenceID).Scan(
		&reservationCount, &usageCount, &parentCount, &allocatedUsageCount,
	); err != nil {
		t.Fatalf("count checkout retry effects: %v", err)
	}
	if reservationCount != 1 || usageCount != 1 || parentCount != 1 || allocatedUsageCount != 1 {
		t.Fatalf(
			"retry effects inventory=%d voucher=%d parent=%d quota=%d, want all 1",
			reservationCount, usageCount, parentCount, allocatedUsageCount,
		)
	}
}

// TestCrossDomainParentSafeReferences chứng minh Payment và Voucher chỉ gắn đúng Parent Order cùng buyer, checkout và currency.
func TestCrossDomainParentSafeReferences(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
	checkoutReferenceID := newMigrationTestUUID(t)
	parentID := insertVoucherTestParentOrder(t, ctx, tx, catalog.userID, checkoutReferenceID, "VND")
	sellerOrderID := insertVoucherTestSellerOrder(t, ctx, tx, parentID, catalog.userID, catalog.shopID, "VND")
	otherUserID := insertSellerCatalogTestUser(t, ctx, tx)
	voucherID := newMigrationTestUUID(t)
	if _, err := insertVoucherTestRow(
		ctx, tx, voucherID, "PARENT-"+strings.ToUpper(sellerCatalogCompactID(voucherID)[:12]),
		"platform", nil, "VND", "fixed_amount", 10000, nil, 10,
	); err != nil {
		t.Fatalf("insert parent-safety Voucher: %v", err)
	}
	parentType := "parent"

	t.Run("reject Payment pointing to Seller Order", func(t *testing.T) {
		assertCrossDomainTransactionSQLState(t, ctx, tx, "23503", func() error {
			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, idempotency_key,
					request_hash, amount, currency_code, expires_at
				)
				VALUES ($1, $2, 'parent', 'STRIPE', $3, repeat('p', 64), 100000, 'VND', clock_timestamp() + interval '1 hour')
			`, newMigrationTestUUID(t), sellerOrderID, "seller-order-"+newMigrationTestUUID(t))
			return err
		})
	})

	t.Run("reject Payment with Parent currency mismatch", func(t *testing.T) {
		assertCrossDomainTransactionSQLState(t, ctx, tx, "23503", func() error {
			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, idempotency_key,
					request_hash, amount, currency_code, expires_at
				)
				VALUES ($1, $2, 'parent', 'STRIPE', $3, repeat('p', 64), 100000, 'USD', clock_timestamp() + interval '1 hour')
			`, newMigrationTestUUID(t), parentID, "wrong-currency-"+newMigrationTestUUID(t))
			return err
		})
	})

	t.Run("reject Voucher usage pointing to Seller Order", func(t *testing.T) {
		assertCrossDomainTransactionSQLState(t, ctx, tx, "23503", func() error {
			_, err := insertVoucherUsageTestRow(
				ctx, tx, newMigrationTestUUID(t), voucherID, catalog.userID, checkoutReferenceID,
				10000, "VND", "committed", &sellerOrderID, &parentType,
			)
			return err
		})
	})

	t.Run("reject Voucher user mismatch", func(t *testing.T) {
		assertCrossDomainTransactionSQLState(t, ctx, tx, "23503", func() error {
			_, err := insertVoucherUsageTestRow(
				ctx, tx, newMigrationTestUUID(t), voucherID, otherUserID, checkoutReferenceID,
				10000, "VND", "committed", &parentID, &parentType,
			)
			return err
		})
	})

	t.Run("reject Voucher checkout mismatch", func(t *testing.T) {
		assertCrossDomainTransactionSQLState(t, ctx, tx, "23503", func() error {
			_, err := insertVoucherUsageTestRow(
				ctx, tx, newMigrationTestUUID(t), voucherID, catalog.userID, newMigrationTestUUID(t),
				10000, "VND", "committed", &parentID, &parentType,
			)
			return err
		})
	})
}

// TestCrossDomainMixedCurrencyCheckoutPreflight chứng minh Cart được phép đa tiền tệ nhưng predicate checkout V1 chỉ chấp nhận một currency.
func TestCrossDomainMixedCurrencyCheckoutPreflight(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	vndCatalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
	usdCatalog := insertSellerCatalogTestFixture(t, ctx, tx, "USD")
	cartID := insertCartTestCart(t, ctx, tx, vndCatalog.userID)
	insertCartTestItem(t, ctx, tx, cartID, vndCatalog.shopID, vndCatalog.skuID, 1)
	usdItemID := insertCartTestItem(t, ctx, tx, cartID, usdCatalog.shopID, usdCatalog.skuID, 1)

	var currencyCount int
	var singleCurrency bool
	if err := tx.QueryRow(ctx, `
		SELECT count(DISTINCT btrim(sku.currency_code)), count(DISTINCT btrim(sku.currency_code)) = 1
		FROM cart_items AS item
		JOIN skus AS sku ON sku.id = item.sku_id AND sku.shop_id = item.shop_id
		WHERE item.cart_id = $1
	`, cartID).Scan(&currencyCount, &singleCurrency); err != nil {
		t.Fatalf("run mixed-currency checkout preflight: %v", err)
	}
	if currencyCount != 2 || singleCurrency {
		t.Fatalf("mixed-currency preflight = (count %d, allowed %t), want (2, false)", currencyCount, singleCurrency)
	}

	if _, err := tx.Exec(ctx, "DELETE FROM cart_items WHERE id = $1", usdItemID); err != nil {
		t.Fatalf("remove USD item from checkout selection: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(DISTINCT btrim(sku.currency_code)) = 1
		FROM cart_items AS item
		JOIN skus AS sku ON sku.id = item.sku_id AND sku.shop_id = item.shop_id
		WHERE item.cart_id = $1
	`, cartID).Scan(&singleCurrency); err != nil {
		t.Fatalf("run single-currency checkout preflight: %v", err)
	}
	if !singleCurrency {
		t.Fatal("single-currency checkout preflight = false, want true")
	}
}

// TestCrossDomainExpiredHoldRollsBackFinalization chứng minh một hold quá hạn làm toàn bộ commerce finalization transaction rollback.
func TestCrossDomainExpiredHoldRollsBackFinalization(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
	checkoutReferenceID := newMigrationTestUUID(t)
	reservationID := insertCrossDomainInventoryReservationTest(
		t, ctx, tx, checkoutReferenceID, inventoryTestIdempotencyKey(t), "15 minutes",
	)

	voucherID := newMigrationTestUUID(t)
	if _, err := insertVoucherTestRow(
		ctx, tx, voucherID, "EXPIRED-"+strings.ToUpper(sellerCatalogCompactID(voucherID)[:12]),
		"platform", nil, "VND", "fixed_amount", 10000, nil, 10,
	); err != nil {
		t.Fatalf("insert expired-hold Voucher: %v", err)
	}
	voucherUsageID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO voucher_usages (
			id, voucher_id, user_id, checkout_reference_id, discount_amount,
			currency_code, status, created_at, updated_at, expires_at
		)
		VALUES (
			$1, $2, $3, $4, 10000,
			'VND', 'reserved', clock_timestamp() - interval '2 minutes',
			clock_timestamp(), clock_timestamp() - interval '1 minute'
		)
	`, voucherUsageID, voucherID, catalog.userID, checkoutReferenceID); err != nil {
		t.Fatalf("insert expired Voucher hold: %v", err)
	}
	parentID := insertVoucherTestParentOrder(t, ctx, tx, catalog.userID, checkoutReferenceID, "VND")
	sellerOrderID := insertVoucherTestSellerOrder(t, ctx, tx, parentID, catalog.userID, catalog.shopID, "VND")
	insertCrossDomainPaymentTest(t, ctx, tx, parentID, "VND", "succeeded")

	if _, err := tx.Exec(ctx, "SAVEPOINT cross_domain_finalization"); err != nil {
		t.Fatalf("create finalization savepoint: %v", err)
	}
	inventoryTag, err := tx.Exec(ctx, `
		UPDATE inventory_reservations
		SET status = 'committed', committed_at = clock_timestamp(), updated_at = clock_timestamp()
		WHERE id = $1 AND status = 'active' AND expires_at > clock_timestamp()
	`, reservationID)
	if err != nil {
		t.Fatalf("commit valid Inventory hold: %v", err)
	}
	if inventoryTag.RowsAffected() != 1 {
		t.Fatalf("committed Inventory holds = %d, want 1", inventoryTag.RowsAffected())
	}
	voucherTag, err := tx.Exec(ctx, `
		UPDATE voucher_usages
		SET status = 'committed', order_id = $2, parent_order_type = 'parent',
			committed_at = clock_timestamp(), updated_at = clock_timestamp()
		WHERE id = $1 AND status = 'reserved' AND expires_at > clock_timestamp()
	`, voucherUsageID, parentID)
	if err != nil {
		t.Fatalf("attempt expired Voucher hold commit: %v", err)
	}
	if voucherTag.RowsAffected() != 0 {
		t.Fatalf("committed expired Voucher holds = %d, want 0", voucherTag.RowsAffected())
	}
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT cross_domain_finalization"); err != nil {
		t.Fatalf("rollback failed finalization: %v", err)
	}
	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT cross_domain_finalization"); err != nil {
		t.Fatalf("release finalization savepoint: %v", err)
	}

	var inventoryStatus, voucherStatus, parentStatus, sellerStatus string
	if err := tx.QueryRow(ctx, `
		SELECT
			(SELECT status FROM inventory_reservations WHERE id = $1),
			(SELECT status FROM voucher_usages WHERE id = $2),
			(SELECT status FROM orders WHERE id = $3),
			(SELECT status FROM orders WHERE id = $4)
	`, reservationID, voucherUsageID, parentID, sellerOrderID).Scan(
		&inventoryStatus, &voucherStatus, &parentStatus, &sellerStatus,
	); err != nil {
		t.Fatalf("read state after failed finalization: %v", err)
	}
	if inventoryStatus != "active" || voucherStatus != "reserved" ||
		parentStatus != "awaiting_payment" || sellerStatus != "awaiting_payment" {
		t.Fatalf(
			"state after rollback inventory=%s voucher=%s parent=%s seller=%s",
			inventoryStatus, voucherStatus, parentStatus, sellerStatus,
		)
	}
}

// TestCrossDomainDuplicateWebhookHasOneLogicalEffect chứng minh inbox dedupe chặn delivery lặp làm Payment và Order chuyển trạng thái lần hai.
func TestCrossDomainDuplicateWebhookHasOneLogicalEffect(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	catalog := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
	checkoutReferenceID := newMigrationTestUUID(t)
	parentID := insertVoucherTestParentOrder(t, ctx, tx, catalog.userID, checkoutReferenceID, "VND")
	sellerOrderID := insertVoucherTestSellerOrder(t, ctx, tx, parentID, catalog.userID, catalog.shopID, "VND")
	insertOrderTestHistory(t, ctx, tx, parentID, nil, "awaiting_payment", "system")
	insertOrderTestHistory(t, ctx, tx, sellerOrderID, nil, "awaiting_payment", "system")
	paymentID := insertCrossDomainPaymentTest(t, ctx, tx, parentID, "VND", "processing")
	eventID := "evt_" + newMigrationTestUUID(t)

	firstApplied := applyCrossDomainPaymentSuccessTest(t, ctx, tx, eventID, paymentID, parentID, sellerOrderID)
	secondApplied := applyCrossDomainPaymentSuccessTest(t, ctx, tx, eventID, paymentID, parentID, sellerOrderID)
	if !firstApplied || secondApplied {
		t.Fatalf("webhook applications = (first %t, second %t), want (true, false)", firstApplied, secondApplied)
	}

	var webhookCount, confirmedHistoryCount int
	var paymentStatus, parentStatus, sellerStatus string
	if err := tx.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM payment_webhook_events WHERE provider = 'STRIPE' AND provider_event_id = $1),
			(SELECT status FROM payment_transactions WHERE id = $2),
			(SELECT status FROM orders WHERE id = $3),
			(SELECT status FROM orders WHERE id = $4),
			(SELECT count(*) FROM order_status_histories WHERE order_id IN ($3, $4) AND to_status = 'confirmed')
	`, eventID, paymentID, parentID, sellerOrderID).Scan(
		&webhookCount, &paymentStatus, &parentStatus, &sellerStatus, &confirmedHistoryCount,
	); err != nil {
		t.Fatalf("read duplicate webhook effects: %v", err)
	}
	if webhookCount != 1 || confirmedHistoryCount != 2 || paymentStatus != "succeeded" ||
		parentStatus != "confirmed" || sellerStatus != "confirmed" {
		t.Fatalf(
			"webhook effects rows=%d histories=%d payment=%s parent=%s seller=%s",
			webhookCount, confirmedHistoryCount, paymentStatus, parentStatus, sellerStatus,
		)
	}
}

// assertCrossDomainTransactionSQLState cô lập một lỗi mong đợi bằng savepoint để các assertion sau vẫn dùng được transaction.
func assertCrossDomainTransactionSQLState(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	expectedCode string,
	action func() error,
) {
	t.Helper()

	if _, err := tx.Exec(ctx, "SAVEPOINT cross_domain_expected_error"); err != nil {
		t.Fatalf("create cross-domain expected-error savepoint: %v", err)
	}
	actionErr := action()
	assertMigrationSQLState(t, actionErr, expectedCode)
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT cross_domain_expected_error"); err != nil {
		t.Fatalf("rollback cross-domain expected error: %v", err)
	}
	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT cross_domain_expected_error"); err != nil {
		t.Fatalf("release cross-domain expected-error savepoint: %v", err)
	}
}

// insertCrossDomainInventoryReservationTest tạo Inventory hold với checkout reference, idempotency key và TTL do test chỉ định.
func insertCrossDomainInventoryReservationTest(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	checkoutReferenceID string,
	idempotencyKey string,
	ttl string,
) string {
	t.Helper()

	reservationID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO inventory_reservations (
			id, reference_type, reference_id, idempotency_key, request_hash, expires_at
		)
		VALUES ($1, 'checkout', $2, $3, repeat('i', 64), clock_timestamp() + $4::interval)
	`, reservationID, checkoutReferenceID, idempotencyKey, ttl); err != nil {
		t.Fatalf("insert cross-domain Inventory reservation: %v", err)
	}
	return reservationID
}

// insertCrossDomainPaymentTest tạo Payment hợp lệ ở trạng thái pending, processing hoặc succeeded cho Parent Order.
func insertCrossDomainPaymentTest(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	parentOrderID string,
	currency string,
	status string,
) string {
	t.Helper()

	paymentID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO payment_transactions (
			id, parent_order_id, parent_order_type, provider, provider_payment_id,
			idempotency_key, request_hash, amount, currency_code, status, expires_at,
			processing_at, succeeded_at
		)
		VALUES (
			$1, $2, 'parent', 'STRIPE', $3,
			$4, repeat('p', 64), 100000, $5, $6::varchar(20), clock_timestamp() + interval '1 hour',
			CASE WHEN $6::varchar(20) IN ('processing', 'succeeded') THEN clock_timestamp() END,
			CASE WHEN $6::varchar(20) = 'succeeded' THEN clock_timestamp() END
		)
	`, paymentID, parentOrderID, "pi_"+newMigrationTestUUID(t), "payment-"+newMigrationTestUUID(t), currency, status); err != nil {
		t.Fatalf("insert cross-domain Payment: %v", err)
	}
	return paymentID
}

// applyCrossDomainPaymentSuccessTest mô phỏng orchestrator gọi các owner module trong một transaction và bỏ qua webhook đã xử lý.
func applyCrossDomainPaymentSuccessTest(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	providerEventID string,
	paymentID string,
	parentOrderID string,
	sellerOrderID string,
) bool {
	t.Helper()

	webhookTag, err := tx.Exec(ctx, `
		INSERT INTO payment_webhook_events (
			id, provider, provider_event_id, payment_transaction_id, event_type,
			payload_hash, processing_status, processed_at
		)
		VALUES ($1, 'STRIPE', $2, $3, 'payment.succeeded', repeat('w', 64), 'processed', clock_timestamp())
		ON CONFLICT (provider, provider_event_id) DO NOTHING
	`, newMigrationTestUUID(t), providerEventID, paymentID)
	if err != nil {
		t.Fatalf("deduplicate Payment webhook: %v", err)
	}
	if webhookTag.RowsAffected() == 0 {
		return false
	}

	paymentTag, err := tx.Exec(ctx, `
		UPDATE payment_transactions
		SET status = 'succeeded', succeeded_at = clock_timestamp(), updated_at = clock_timestamp()
		WHERE id = $1 AND status = 'processing'
	`, paymentID)
	if err != nil {
		t.Fatalf("apply Payment success transition: %v", err)
	}
	if paymentTag.RowsAffected() != 1 {
		t.Fatalf("Payment success transitions = %d, want 1", paymentTag.RowsAffected())
	}

	for _, orderID := range []string{parentOrderID, sellerOrderID} {
		orderTag, err := tx.Exec(ctx, `
			UPDATE orders
			SET status = 'confirmed', confirmed_at = clock_timestamp(), updated_at = clock_timestamp()
			WHERE id = $1 AND status = 'awaiting_payment'
		`, orderID)
		if err != nil {
			t.Fatalf("apply Order confirmation: %v", err)
		}
		if orderTag.RowsAffected() != 1 {
			t.Fatalf("Order confirmations for %s = %d, want 1", orderID, orderTag.RowsAffected())
		}
		fromStatus := "awaiting_payment"
		insertOrderTestHistory(t, ctx, tx, orderID, &fromStatus, "confirmed", "payment")
	}

	return true
}
