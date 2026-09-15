// File này kiểm thử migration Voucher bằng PostgreSQL thật, gồm scope, currency isolation,
// discount constraints, Parent Order commitment, idempotency và concurrency invariants.
package database

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// voucherConcurrentResult lưu kết quả của một thao tác voucher chạy đồng thời trong integration tests.
type voucherConcurrentResult struct {
	success bool
	err     error
}

// TestVoucherMigrationCreatesExpectedObjects xác nhận các bảng và index của Voucher tồn tại đầy đủ theo DATA-003F.
func TestVoucherMigrationCreatesExpectedObjects(t *testing.T) {
	ctx, tx := beginMigrationTest(t)

	objects := []string{
		"vouchers",
		"voucher_usages",
		"uq_vouchers_code",
		"uq_vouchers_id_currency",
		"idx_vouchers_shop_status",
		"idx_vouchers_status_time",
		"uq_voucher_usages_voucher_checkout",
		"idx_voucher_usages_voucher_status",
		"idx_voucher_usages_user_voucher",
		"idx_voucher_usages_order",
		"idx_voucher_usages_checkout",
		"idx_voucher_usages_reserved_expires",
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

// TestVoucherMigrationUsesCanonicalTypesAndDeleteRules xác nhận UUID PK, TIMESTAMPTZ, BIGINT và RESTRICT FKs.
func TestVoucherMigrationUsesCanonicalTypesAndDeleteRules(t *testing.T) {
	ctx, tx := beginMigrationTest(t)

	for _, table := range []string{"vouchers", "voucher_usages"} {
		var dataType, defaultValue string
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
		"fk_vouchers_shop_currency",
		"fk_voucher_usages_voucher_currency",
		"fk_voucher_usages_user",
		"fk_voucher_usages_order_parent",
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
	`, []string{"vouchers", "voucher_usages"}).Scan(&invalidTimestampColumns); err != nil {
		t.Fatalf("inspect Voucher timestamps: %v", err)
	}
	if invalidTimestampColumns != 0 {
		t.Errorf("Voucher has %d non-TIMESTAMPTZ absolute-time columns", invalidTimestampColumns)
	}

	bigintColumns := map[string][]string{
		"vouchers": {
			"discount_value",
			"max_discount_amount",
			"minimum_order_amount",
			"usage_limit",
			"usage_limit_per_user",
			"allocated_usage_count",
		},
		"voucher_usages": {"discount_amount"},
	}
	for table, columns := range bigintColumns {
		for _, column := range columns {
			var dataType string
			if err := tx.QueryRow(ctx, `
				SELECT data_type
				FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
			`, table, column).Scan(&dataType); err != nil {
				t.Fatalf("inspect %s.%s: %v", table, column, err)
			}
			if dataType != "bigint" {
				t.Errorf("%s.%s type = %s, want bigint", table, column, dataType)
			}
		}
	}

	voucherID := newMigrationTestUUID(t)
	if voucherID[14] != '7' {
		t.Fatalf("generated Voucher UUID version nibble = %q, want 7", voucherID[14])
	}
	if _, err := insertVoucherTestRow(ctx, tx, voucherID, "UUID-V7-CHECK", "platform", nil, "VND", "fixed_amount", 10000, nil, 10); err != nil {
		t.Fatalf("insert Voucher with Go-generated UUIDv7: %v", err)
	}
}

// TestVoucherMigrationEnforcesScopeAndCurrencyInvariants kiểm tra tính toàn vẹn của Scope và Currency Isolation.
func TestVoucherMigrationEnforcesScopeAndCurrencyInvariants(t *testing.T) {
	t.Run("valid platform voucher with null shop accepted", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		voucherID := newMigrationTestUUID(t)
		_, err := insertVoucherTestRow(ctx, tx, voucherID, "PLATFORM-VND-01", "platform", nil, "VND", "fixed_amount", 50000, nil, 10)
		if err != nil {
			t.Fatalf("insert platform voucher: %v", err)
		}
	})

	t.Run("platform voucher with shop rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		shopVND := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		_, err := insertVoucherTestRow(ctx, tx, voucherID, "PLATFORM-INVALID", "platform", &shopVND, "VND", "fixed_amount", 50000, nil, 10)
		assertMigrationSQLState(t, err, "23514") // ck_vouchers_scope_shop
	})

	t.Run("shop voucher without shop rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		voucherID := newMigrationTestUUID(t)
		_, err := insertVoucherTestRow(ctx, tx, voucherID, "SHOP-INVALID", "shop", nil, "VND", "fixed_amount", 50000, nil, 10)
		assertMigrationSQLState(t, err, "23514") // ck_vouchers_scope_shop
	})

	t.Run("shop voucher with matching currency accepted", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		shopVND := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		_, err := insertVoucherTestRow(ctx, tx, voucherID, "SHOP-VND-OK", "shop", &shopVND, "VND", "fixed_amount", 20000, nil, 10)
		if err != nil {
			t.Fatalf("insert shop voucher with matching currency: %v", err)
		}
	})

	t.Run("shop voucher with mismatched currency rejected by composite FK", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		shopVND := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		// Shop VND nhưng tạo Voucher USD -> vi phạm fk_vouchers_shop_currency
		_, err := insertVoucherTestRow(ctx, tx, voucherID, "SHOP-MISMATCH-CURRENCY", "shop", &shopVND, "USD", "fixed_amount", 10, nil, 10)
		assertMigrationSQLState(t, err, "23503") // fk_vouchers_shop_currency
	})

	t.Run("usage with mismatched currency rejected by composite FK", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		userID := insertSellerCatalogTestUser(t, ctx, tx)
		shopUSD := insertSellerCatalogTestShop(t, ctx, tx, "USD")
		voucherID := newMigrationTestUUID(t)
		if _, err := insertVoucherTestRow(ctx, tx, voucherID, "VOUCHER-USD-01", "shop", &shopUSD, "USD", "fixed_amount", 5, nil, 10); err != nil {
			t.Fatalf("setup voucher USD: %v", err)
		}

		usageID := newMigrationTestUUID(t)
		checkoutRef := newMigrationTestUUID(t)
		// Voucher là USD nhưng Usage cố tình ghi VND -> vi phạm fk_voucher_usages_voucher_currency
		_, err := insertVoucherUsageTestRow(ctx, tx, usageID, voucherID, userID, checkoutRef, 50000, "VND", "reserved", nil, nil)
		assertMigrationSQLState(t, err, "23503") // fk_voucher_usages_voucher_currency
	})
}

// TestVoucherMigrationEnforcesCodeAndDiscountInvariants kiểm tra các ràng buộc giá trị, canonical format và limits.
func TestVoucherMigrationEnforcesCodeAndDiscountInvariants(t *testing.T) {
	t.Run("valid fixed and percentage discounts accepted", func(t *testing.T) {
		t.Run("fixed amount", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			shopID := insertSellerCatalogTestShop(t, ctx, tx, "VND")
			voucherID := newMigrationTestUUID(t)
			if _, err := insertVoucherTestRow(ctx, tx, voucherID, "VALID-FIXED", "shop", &shopID, "VND", "fixed_amount", 10000, nil, 10); err != nil {
				t.Fatalf("insert valid fixed discount: %v", err)
			}
		})

		t.Run("percentage with cap", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			shopID := insertSellerCatalogTestShop(t, ctx, tx, "VND")
			voucherID := newMigrationTestUUID(t)
			maxDiscount := int64(50000)
			if _, err := insertVoucherTestRow(ctx, tx, voucherID, "VALID-PERCENT", "shop", &shopID, "VND", "percentage", 2500, &maxDiscount, 10); err != nil {
				t.Fatalf("insert valid percentage discount: %v", err)
			}
		})
	})

	t.Run("code canonical format enforced", func(t *testing.T) {
		for _, invalidCode := range []string{"lowercase", " UPPER ", "", "  "} {
			ctx, tx := beginMigrationTest(t)
			shopID := insertSellerCatalogTestShop(t, ctx, tx, "VND")
			voucherID := newMigrationTestUUID(t)
			_, err := insertVoucherTestRow(ctx, tx, voucherID, invalidCode, "shop", &shopID, "VND", "fixed_amount", 10000, nil, 10)
			assertMigrationSQLState(t, err, "23514") // ck_vouchers_code_canonical
		}
	})

	t.Run("duplicate code globally rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		shopID := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		firstID := newMigrationTestUUID(t)
		secondID := newMigrationTestUUID(t)
		code := "SAVE20K"
		if _, err := insertVoucherTestRow(ctx, tx, firstID, code, "shop", &shopID, "VND", "fixed_amount", 20000, nil, 10); err != nil {
			t.Fatalf("insert first voucher: %v", err)
		}
		_, err := insertVoucherTestRow(ctx, tx, secondID, code, "platform", nil, "VND", "fixed_amount", 20000, nil, 10)
		assertMigrationSQLState(t, err, "23505") // uq_vouchers_code
	})

	t.Run("discount percentage value outside 1..10000 rejected", func(t *testing.T) {
		for _, invalidValue := range []int64{0, 10001} {
			ctx, tx := beginMigrationTest(t)
			shopID := insertSellerCatalogTestShop(t, ctx, tx, "VND")
			voucherID := newMigrationTestUUID(t)
			code := fmt.Sprintf("PERCENT-%d", invalidValue)
			_, err := insertVoucherTestRow(ctx, tx, voucherID, code, "shop", &shopID, "VND", "percentage", invalidValue, nil, 10)
			assertMigrationSQLState(t, err, "23514") // ck_vouchers_discount_type_value
		}
	})

	t.Run("fixed discount with cap rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		shopID := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		capAmount := int64(50000)
		_, err := insertVoucherTestRow(ctx, tx, voucherID, "FIXED-WITH-CAP", "shop", &shopID, "VND", "fixed_amount", 20000, &capAmount, 10)
		assertMigrationSQLState(t, err, "23514") // ck_vouchers_discount_cap
	})

	t.Run("negative minimum order amount rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		shopID := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		_, err := tx.Exec(ctx, `
			INSERT INTO vouchers (
				id, code, scope, shop_id, discount_type, discount_value,
				minimum_order_amount, currency_code, starts_at
			)
			VALUES ($1, 'NEG-MIN-ORDER', 'shop', $2, 'fixed_amount', 10000, -1, 'VND', now())
		`, voucherID, shopID)
		assertMigrationSQLState(t, err, "23514") // ck_vouchers_minimum_order_amount
	})

	t.Run("negative boundary limits rejected", func(t *testing.T) {
		for _, invalidLimit := range []int64{0, -1} {
			ctx, tx := beginMigrationTest(t)
			shopID := insertSellerCatalogTestShop(t, ctx, tx, "VND")
			voucherID := newMigrationTestUUID(t)
			_, err := tx.Exec(ctx, `
				INSERT INTO vouchers (
					id, code, scope, shop_id, discount_type, discount_value,
					usage_limit, currency_code, starts_at
				)
				VALUES ($1, 'NEG-LIMIT', 'shop', $2, 'fixed_amount', 10000, $3, 'VND', now())
			`, voucherID, shopID, invalidLimit)
			assertMigrationSQLState(t, err, "23514") // ck_vouchers_usage_limit_positive
		}

		for _, invalidPerUser := range []int64{0, -1} {
			ctx, tx := beginMigrationTest(t)
			shopID := insertSellerCatalogTestShop(t, ctx, tx, "VND")
			voucherID := newMigrationTestUUID(t)
			_, err := tx.Exec(ctx, `
				INSERT INTO vouchers (
					id, code, scope, shop_id, discount_type, discount_value,
					usage_limit_per_user, currency_code, starts_at
				)
				VALUES ($1, 'NEG-PER-USER', 'shop', $2, 'fixed_amount', 10000, $3, 'VND', now())
			`, voucherID, shopID, invalidPerUser)
			assertMigrationSQLState(t, err, "23514") // ck_vouchers_usage_limit_per_user_positive
		}

		{
			ctx, tx := beginMigrationTest(t)
			shopID := insertSellerCatalogTestShop(t, ctx, tx, "VND")
			voucherID := newMigrationTestUUID(t)
			_, err := tx.Exec(ctx, `
				INSERT INTO vouchers (
					id, code, scope, shop_id, discount_type, discount_value,
					allocated_usage_count, currency_code, starts_at
				)
				VALUES ($1, 'NEG-ALLOCATED', 'shop', $2, 'fixed_amount', 10000, -1, 'VND', now())
			`, voucherID, shopID)
			assertMigrationSQLState(t, err, "23514") // ck_vouchers_allocated_usage
		}
	})

	t.Run("allocated usage count exceeding limit rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		shopID := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		limit := int64(10)
		_, err := tx.Exec(ctx, `
			INSERT INTO vouchers (
				id, code, scope, shop_id, discount_type, discount_value,
				usage_limit, allocated_usage_count, currency_code, starts_at
			)
			VALUES ($1, 'OVER-ALLOCATED', 'shop', $2, 'fixed_amount', 10000, $3, 11, 'VND', now())
		`, voucherID, shopID, limit)
		assertMigrationSQLState(t, err, "23514") // ck_vouchers_allocated_usage
	})

	t.Run("validity window equal or before start rejected", func(t *testing.T) {
		for _, endOffset := range []string{"0 seconds", "-1 hour"} {
			ctx, tx := beginMigrationTest(t)
			shopID := insertSellerCatalogTestShop(t, ctx, tx, "VND")
			voucherID := newMigrationTestUUID(t)
			_, err := tx.Exec(ctx, `
				INSERT INTO vouchers (
					id, code, scope, shop_id, discount_type, discount_value,
					currency_code, starts_at, ends_at
				)
				VALUES (
					$1, 'INVALID-DATES', 'shop', $2, 'fixed_amount', 10000,
					'VND', now(), now() + $3::interval
				)
			`, voucherID, shopID, endOffset)
			assertMigrationSQLState(t, err, "23514") // ck_vouchers_validity_period
		}
	})
}

// TestVoucherMigrationRejectsUnavailableReservations xác nhận reserve workflow chỉ cấp quota cho Voucher active trong thời gian hiệu lực.
func TestVoucherMigrationRejectsUnavailableReservations(t *testing.T) {
	testCases := []struct {
		name         string
		status       string
		startsOffset string
		endsOffset   string
	}{
		{name: "draft voucher", status: "draft", startsOffset: "-1 hour", endsOffset: "1 hour"},
		{name: "inactive voucher", status: "inactive", startsOffset: "-1 hour", endsOffset: "1 hour"},
		{name: "future voucher", status: "active", startsOffset: "1 hour", endsOffset: "2 hours"},
		{name: "expired voucher", status: "active", startsOffset: "-2 hours", endsOffset: "-1 hour"},
	}

	for index, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			buyerID := insertSellerCatalogTestUser(t, ctx, tx)
			voucherID := newMigrationTestUUID(t)
			code := fmt.Sprintf("UNAVAILABLE-%d", index)
			if _, err := insertVoucherTestRow(ctx, tx, voucherID, code, "platform", nil, "VND", "fixed_amount", 10000, nil, 10); err != nil {
				t.Fatalf("setup unavailable Voucher: %v", err)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE vouchers
				SET status = $2,
					starts_at = clock_timestamp() + $3::interval,
					ends_at = clock_timestamp() + $4::interval,
					updated_at = clock_timestamp()
				WHERE id = $1
			`, voucherID, testCase.status, testCase.startsOffset, testCase.endsOffset); err != nil {
				t.Fatalf("configure unavailable Voucher: %v", err)
			}

			usageID, reserved, err := reserveVoucherUsageTest(
				ctx,
				tx,
				voucherID,
				buyerID,
				newMigrationTestUUID(t),
				newMigrationTestUUID(t),
				10000,
			)
			if err != nil {
				t.Fatalf("reserve unavailable Voucher: %v", err)
			}
			if reserved || usageID != "" {
				t.Fatalf("unavailable Voucher reserved = %v, usageID = %q; want false and empty", reserved, usageID)
			}

			var allocatedCount, usageCount int64
			if err := tx.QueryRow(ctx, "SELECT allocated_usage_count FROM vouchers WHERE id = $1", voucherID).Scan(&allocatedCount); err != nil {
				t.Fatalf("query unavailable Voucher counter: %v", err)
			}
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM voucher_usages WHERE voucher_id = $1", voucherID).Scan(&usageCount); err != nil {
				t.Fatalf("query unavailable Voucher usages: %v", err)
			}
			if allocatedCount != 0 || usageCount != 0 {
				t.Fatalf("unavailable Voucher allocated=%d usages=%d, want 0 and 0", allocatedCount, usageCount)
			}
		})
	}
}

// TestVoucherMigrationEnforcesUsageAndCommitInvariants kiểm tra vòng đời usage và ràng buộc commit sang Parent Order.
func TestVoucherMigrationEnforcesUsageAndCommitInvariants(t *testing.T) {
	t.Run("duplicate reservation on same checkout reference rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		buyerA := insertSellerCatalogTestUser(t, ctx, tx)
		shopVND := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		if _, err := insertVoucherTestRow(ctx, tx, voucherID, "PROMO-DUP-REF", "shop", &shopVND, "VND", "fixed_amount", 10000, nil, 100); err != nil {
			t.Fatalf("insert test voucher: %v", err)
		}

		checkoutRef := newMigrationTestUUID(t)
		firstUsage := newMigrationTestUUID(t)
		secondUsage := newMigrationTestUUID(t)

		if _, err := insertVoucherUsageTestRow(ctx, tx, firstUsage, voucherID, buyerA, checkoutRef, 10000, "VND", "reserved", nil, nil); err != nil {
			t.Fatalf("insert first usage: %v", err)
		}
		// Cùng (voucher_id, checkout_reference_id) -> vi phạm uq_voucher_usages_voucher_checkout
		_, err := insertVoucherUsageTestRow(ctx, tx, secondUsage, voucherID, buyerA, checkoutRef, 10000, "VND", "reserved", nil, nil)
		assertMigrationSQLState(t, err, "23505")
	})

	t.Run("reserved usage with order_id rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		buyerA := insertSellerCatalogTestUser(t, ctx, tx)
		shopVND := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		if _, err := insertVoucherTestRow(ctx, tx, voucherID, "PROMO-RES-ORD", "shop", &shopVND, "VND", "fixed_amount", 10000, nil, 100); err != nil {
			t.Fatalf("insert test voucher: %v", err)
		}

		usageID := newMigrationTestUUID(t)
		checkoutRef := newMigrationTestUUID(t)
		orderID := insertVoucherTestParentOrder(t, ctx, tx, buyerA, checkoutRef, "VND")

		parentType := "parent"
		// reserved status không được mang order_id
		_, err := insertVoucherUsageTestRow(ctx, tx, usageID, voucherID, buyerA, checkoutRef, 10000, "VND", "reserved", &orderID, &parentType)
		assertMigrationSQLState(t, err, "23514") // ck_voucher_usages_state_timestamps
	})

	t.Run("committed usage attaching to matching Parent Order accepted", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		buyerA := insertSellerCatalogTestUser(t, ctx, tx)
		shopVND := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		if _, err := insertVoucherTestRow(ctx, tx, voucherID, "PROMO-COM-MATCH", "shop", &shopVND, "VND", "fixed_amount", 10000, nil, 100); err != nil {
			t.Fatalf("insert test voucher: %v", err)
		}

		usageID := newMigrationTestUUID(t)
		checkoutRef := newMigrationTestUUID(t)
		orderID := insertVoucherTestParentOrder(t, ctx, tx, buyerA, checkoutRef, "VND")

		parentType := "parent"
		if _, err := insertVoucherUsageTestRow(ctx, tx, usageID, voucherID, buyerA, checkoutRef, 10000, "VND", "committed", &orderID, &parentType); err != nil {
			t.Fatalf("commit usage to matching parent order: %v", err)
		}
	})

	t.Run("committed usage attaching to mismatched user rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		buyerA := insertSellerCatalogTestUser(t, ctx, tx)
		buyerB := insertSellerCatalogTestUser(t, ctx, tx)
		shopVND := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		if _, err := insertVoucherTestRow(ctx, tx, voucherID, "PROMO-MIS-USER", "shop", &shopVND, "VND", "fixed_amount", 10000, nil, 100); err != nil {
			t.Fatalf("insert test voucher: %v", err)
		}

		usageID := newMigrationTestUUID(t)
		checkoutRef := newMigrationTestUUID(t)
		orderID := insertVoucherTestParentOrder(t, ctx, tx, buyerA, checkoutRef, "VND")

		parentType := "parent"
		// VoucherUsage thuộc Buyer B nhưng Order thuộc Buyer A -> vi phạm fk_voucher_usages_order_parent
		_, err := insertVoucherUsageTestRow(ctx, tx, usageID, voucherID, buyerB, checkoutRef, 10000, "VND", "committed", &orderID, &parentType)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("committed usage attaching to mismatched checkout_reference_id rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		buyerA := insertSellerCatalogTestUser(t, ctx, tx)
		shopVND := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		if _, err := insertVoucherTestRow(ctx, tx, voucherID, "PROMO-MIS-REF", "shop", &shopVND, "VND", "fixed_amount", 10000, nil, 100); err != nil {
			t.Fatalf("insert test voucher: %v", err)
		}

		usageID := newMigrationTestUUID(t)
		checkoutRefOrder := newMigrationTestUUID(t)
		checkoutRefUsage := newMigrationTestUUID(t)
		orderID := insertVoucherTestParentOrder(t, ctx, tx, buyerA, checkoutRefOrder, "VND")

		parentType := "parent"
		// VoucherUsage mang checkoutRefUsage != checkoutRefOrder của Parent Order -> vi phạm composite FK
		_, err := insertVoucherUsageTestRow(ctx, tx, usageID, voucherID, buyerA, checkoutRefUsage, 10000, "VND", "committed", &orderID, &parentType)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("committed usage attaching to mismatched currency rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		buyerA := insertSellerCatalogTestUser(t, ctx, tx)
		shopVND := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		if _, err := insertVoucherTestRow(ctx, tx, voucherID, "PROMO-MIS-CURR", "shop", &shopVND, "VND", "fixed_amount", 10000, nil, 100); err != nil {
			t.Fatalf("insert test voucher: %v", err)
		}

		usageID := newMigrationTestUUID(t)
		checkoutRef := newMigrationTestUUID(t)
		orderID := insertVoucherTestParentOrder(t, ctx, tx, buyerA, checkoutRef, "USD")

		parentType := "parent"
		// VoucherUsage currency là VND nhưng Order là USD -> vi phạm composite FK
		_, err := insertVoucherUsageTestRow(ctx, tx, usageID, voucherID, buyerA, checkoutRef, 10000, "VND", "committed", &orderID, &parentType)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("committed usage attaching to seller order rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		buyerA := insertSellerCatalogTestUser(t, ctx, tx)
		shopVND := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		if _, err := insertVoucherTestRow(ctx, tx, voucherID, "PROMO-SEL-ORD", "shop", &shopVND, "VND", "fixed_amount", 10000, nil, 100); err != nil {
			t.Fatalf("insert test voucher: %v", err)
		}

		usageID := newMigrationTestUUID(t)
		checkoutRef := newMigrationTestUUID(t)
		parentID := insertVoucherTestParentOrder(t, ctx, tx, buyerA, checkoutRef, "VND")
		sellerOrderID := insertVoucherTestSellerOrder(t, ctx, tx, parentID, buyerA, shopVND, "VND")

		sellerType := "seller"
		_, err := insertVoucherUsageTestRow(ctx, tx, usageID, voucherID, buyerA, checkoutRef, 10000, "VND", "committed", &sellerOrderID, &sellerType)
		assertMigrationSQLState(t, err, "23514") // ck_voucher_usages_state_timestamps đòi hỏi parent_order_type = 'parent'
	})

	t.Run("invalid state timestamp combinations rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		buyerA := insertSellerCatalogTestUser(t, ctx, tx)
		shopVND := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		if _, err := insertVoucherTestRow(ctx, tx, voucherID, "PROMO-TS-COMB", "shop", &shopVND, "VND", "fixed_amount", 10000, nil, 100); err != nil {
			t.Fatalf("insert test voucher: %v", err)
		}

		// 1. status = 'released' nhưng có committed_at
		{
			usageID := newMigrationTestUUID(t)
			checkoutRef := newMigrationTestUUID(t)
			assertVoucherTransactionSQLState(t, ctx, tx, "23514", func() error {
				_, err := tx.Exec(ctx, `
					INSERT INTO voucher_usages (
						id, voucher_id, user_id, checkout_reference_id, discount_amount,
						currency_code, status, expires_at, committed_at, released_at
					)
					VALUES ($1, $2, $3, $4, 10000, 'VND', 'released', now() + interval '15m', now(), now())
				`, usageID, voucherID, buyerA, checkoutRef)
				return err
			})
		}

		// 2. status = 'expired' nhưng có released_at
		{
			usageID := newMigrationTestUUID(t)
			checkoutRef := newMigrationTestUUID(t)
			assertVoucherTransactionSQLState(t, ctx, tx, "23514", func() error {
				_, err := tx.Exec(ctx, `
					INSERT INTO voucher_usages (
						id, voucher_id, user_id, checkout_reference_id, discount_amount,
						currency_code, status, expires_at, created_at, released_at, expired_at
					)
					VALUES (
						$1, $2, $3, $4, 10000, 'VND', 'expired',
						now() - interval '1m', now() - interval '2m', now(), now()
					)
				`, usageID, voucherID, buyerA, checkoutRef)
				return err
			})
		}

		// 3. status = 'committed' nhưng không có committed_at
		{
			usageID := newMigrationTestUUID(t)
			checkoutRef := newMigrationTestUUID(t)
			orderID := insertVoucherTestParentOrder(t, ctx, tx, buyerA, checkoutRef, "VND")
			assertVoucherTransactionSQLState(t, ctx, tx, "23514", func() error {
				_, err := tx.Exec(ctx, `
					INSERT INTO voucher_usages (
						id, voucher_id, user_id, checkout_reference_id, order_id, parent_order_type,
						discount_amount, currency_code, status, expires_at, committed_at
					)
					VALUES ($1, $2, $3, $4, $5, 'parent', 10000, 'VND', 'committed', now() + interval '15m', NULL)
				`, usageID, voucherID, buyerA, checkoutRef, orderID)
				return err
			})
		}
	})
}

// TestVoucherMigrationEnforcesIdempotentLifecycle kiểm tra retry checkout, release/expire/commit idempotency
// và việc counter không bị decrement hoặc increment sai lệch.
func TestVoucherMigrationEnforcesIdempotentLifecycle(t *testing.T) {
	t.Run("retry reservation does not double increment counter", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		buyer := insertSellerCatalogTestUser(t, ctx, tx)
		shopVND := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		if _, err := insertVoucherTestRow(ctx, tx, voucherID, "IDEMP-RETRY", "shop", &shopVND, "VND", "fixed_amount", 10000, nil, 10); err != nil {
			t.Fatalf("setup voucher: %v", err)
		}
		if err := activateVoucherTestRow(ctx, tx, voucherID); err != nil {
			t.Fatalf("activate Voucher: %v", err)
		}

		checkoutRef := newMigrationTestUUID(t)
		firstCandidateID := newMigrationTestUUID(t)
		firstUsageID, firstCreated, err := reserveVoucherUsageTest(ctx, tx, voucherID, buyer, checkoutRef, firstCandidateID, 10000)
		if err != nil {
			t.Fatalf("first reserve: %v", err)
		}
		if !firstCreated || firstUsageID != firstCandidateID {
			t.Fatalf("first reserve created=%v usageID=%q, want true and %q", firstCreated, firstUsageID, firstCandidateID)
		}

		secondCandidateID := newMigrationTestUUID(t)
		secondUsageID, secondCreated, err := reserveVoucherUsageTest(ctx, tx, voucherID, buyer, checkoutRef, secondCandidateID, 10000)
		if err != nil {
			t.Fatalf("retry reserve: %v", err)
		}
		if secondCreated || secondUsageID != firstUsageID {
			t.Fatalf("retry reserve created=%v usageID=%q, want false and existing %q", secondCreated, secondUsageID, firstUsageID)
		}

		var allocatedCount, usageCount int64
		if err := tx.QueryRow(ctx, "SELECT allocated_usage_count FROM vouchers WHERE id = $1", voucherID).Scan(&allocatedCount); err != nil {
			t.Fatalf("query counter: %v", err)
		}
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM voucher_usages WHERE voucher_id = $1", voucherID).Scan(&usageCount); err != nil {
			t.Fatalf("query usage count: %v", err)
		}
		if allocatedCount != 1 || usageCount != 1 {
			t.Errorf("retry result allocated=%d usages=%d, want 1 and 1", allocatedCount, usageCount)
		}
	})

	t.Run("release idempotency", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		buyer := insertSellerCatalogTestUser(t, ctx, tx)
		shopVND := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		if _, err := insertVoucherTestRow(ctx, tx, voucherID, "IDEMP-REL", "shop", &shopVND, "VND", "fixed_amount", 10000, nil, 10); err != nil {
			t.Fatalf("setup voucher: %v", err)
		}
		checkoutRef := newMigrationTestUUID(t)
		usageID := newMigrationTestUUID(t)
		if _, err := insertVoucherUsageTestRow(ctx, tx, usageID, voucherID, buyer, checkoutRef, 10000, "VND", "reserved", nil, nil); err != nil {
			t.Fatalf("setup usage: %v", err)
		}
		if _, err := tx.Exec(ctx, "UPDATE vouchers SET allocated_usage_count = 1 WHERE id = $1", voucherID); err != nil {
			t.Fatalf("set counter: %v", err)
		}

		firstReleased, err := releaseVoucherUsageTest(ctx, tx, usageID)
		if err != nil {
			t.Fatalf("first release: %v", err)
		}
		secondReleased, err := releaseVoucherUsageTest(ctx, tx, usageID)
		if err != nil {
			t.Fatalf("second release: %v", err)
		}
		if !firstReleased || secondReleased {
			t.Errorf("release results first=%v second=%v, want true and false", firstReleased, secondReleased)
		}

		var finalCount int64
		var finalStatus string
		if err := tx.QueryRow(ctx, `
			SELECT v.allocated_usage_count, u.status
			FROM vouchers v
			JOIN voucher_usages u ON u.voucher_id = v.id
			WHERE v.id = $1 AND u.id = $2
		`, voucherID, usageID).Scan(&finalCount, &finalStatus); err != nil {
			t.Fatalf("query release result: %v", err)
		}
		if finalCount != 0 || finalStatus != "released" {
			t.Errorf("release result counter=%d status=%s, want 0 and released", finalCount, finalStatus)
		}
	})

	t.Run("expiration idempotency", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		buyer := insertSellerCatalogTestUser(t, ctx, tx)
		shopVND := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		if _, err := insertVoucherTestRow(ctx, tx, voucherID, "IDEMP-EXP", "shop", &shopVND, "VND", "fixed_amount", 10000, nil, 10); err != nil {
			t.Fatalf("setup voucher: %v", err)
		}
		if _, err := tx.Exec(ctx, "UPDATE vouchers SET allocated_usage_count = 1 WHERE id = $1", voucherID); err != nil {
			t.Fatalf("set counter: %v", err)
		}

		usageID := newMigrationTestUUID(t)
		if _, err := tx.Exec(ctx, `
			INSERT INTO voucher_usages (
				id, voucher_id, user_id, checkout_reference_id,
				discount_amount, currency_code, status, created_at, updated_at, expires_at
			)
			VALUES (
				$1, $2, $3, $4, 10000, 'VND', 'reserved',
				now() - interval '2 minutes', now(), now() - interval '1 minute'
			)
		`, usageID, voucherID, buyer, newMigrationTestUUID(t)); err != nil {
			t.Fatalf("setup expired reservation: %v", err)
		}

		firstExpired, err := expireVoucherUsageTest(ctx, tx, usageID)
		if err != nil {
			t.Fatalf("first expiration: %v", err)
		}
		secondExpired, err := expireVoucherUsageTest(ctx, tx, usageID)
		if err != nil {
			t.Fatalf("second expiration: %v", err)
		}
		if !firstExpired || secondExpired {
			t.Errorf("expiration results first=%v second=%v, want true and false", firstExpired, secondExpired)
		}

		var finalCount int64
		var finalStatus string
		if err := tx.QueryRow(ctx, `
			SELECT v.allocated_usage_count, u.status
			FROM vouchers v
			JOIN voucher_usages u ON u.voucher_id = v.id
			WHERE v.id = $1 AND u.id = $2
		`, voucherID, usageID).Scan(&finalCount, &finalStatus); err != nil {
			t.Fatalf("query expiration result: %v", err)
		}
		if finalCount != 0 || finalStatus != "expired" {
			t.Errorf("expiration result counter=%d status=%s, want 0 and expired", finalCount, finalStatus)
		}
	})

	t.Run("commit idempotency", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		buyer := insertSellerCatalogTestUser(t, ctx, tx)
		shopVND := insertSellerCatalogTestShop(t, ctx, tx, "VND")
		voucherID := newMigrationTestUUID(t)
		if _, err := insertVoucherTestRow(ctx, tx, voucherID, "IDEMP-COM", "shop", &shopVND, "VND", "fixed_amount", 10000, nil, 10); err != nil {
			t.Fatalf("setup voucher: %v", err)
		}
		checkoutRef := newMigrationTestUUID(t)
		usageID := newMigrationTestUUID(t)
		orderID := insertVoucherTestParentOrder(t, ctx, tx, buyer, checkoutRef, "VND")
		if _, err := insertVoucherUsageTestRow(ctx, tx, usageID, voucherID, buyer, checkoutRef, 10000, "VND", "reserved", nil, nil); err != nil {
			t.Fatalf("setup usage: %v", err)
		}
		if _, err := tx.Exec(ctx, "UPDATE vouchers SET allocated_usage_count = 1 WHERE id = $1", voucherID); err != nil {
			t.Fatalf("set counter: %v", err)
		}

		firstCommitted, err := commitVoucherUsageTest(ctx, tx, usageID, orderID)
		if err != nil {
			t.Fatalf("first commit: %v", err)
		}
		secondCommitted, err := commitVoucherUsageTest(ctx, tx, usageID, orderID)
		if err != nil {
			t.Fatalf("second commit: %v", err)
		}
		releasedCommitted, err := releaseVoucherUsageTest(ctx, tx, usageID)
		if err != nil {
			t.Fatalf("release committed usage: %v", err)
		}
		if !firstCommitted || secondCommitted || releasedCommitted {
			t.Errorf(
				"commit/release results firstCommit=%v secondCommit=%v releaseCommitted=%v, want true, false, false",
				firstCommitted,
				secondCommitted,
				releasedCommitted,
			)
		}

		var finalCount int64
		var finalStatus string
		if err := tx.QueryRow(ctx, `
			SELECT v.allocated_usage_count, u.status
			FROM vouchers v
			JOIN voucher_usages u ON u.voucher_id = v.id
			WHERE v.id = $1 AND u.id = $2
		`, voucherID, usageID).Scan(&finalCount, &finalStatus); err != nil {
			t.Fatalf("query commit result: %v", err)
		}
		if finalCount != 1 || finalStatus != "committed" {
			t.Errorf("commit result counter=%d status=%s, want 1 and committed", finalCount, finalStatus)
		}
	})
}

// TestVoucherMigrationEnforcesDeleteRestrictRules xác nhận không thể hard delete Shop, User, Voucher, Parent Order khi đang được tham chiếu.
func TestVoucherMigrationEnforcesDeleteRestrictRules(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	buyer := insertSellerCatalogTestUser(t, ctx, tx)
	shopVND := insertSellerCatalogTestShop(t, ctx, tx, "VND")
	voucherID := newMigrationTestUUID(t)
	if _, err := insertVoucherTestRow(ctx, tx, voucherID, "DEL-RESTRICT", "shop", &shopVND, "VND", "fixed_amount", 10000, nil, 10); err != nil {
		t.Fatalf("setup voucher: %v", err)
	}

	checkoutRef := newMigrationTestUUID(t)
	usageID := newMigrationTestUUID(t)
	orderID := insertVoucherTestParentOrder(t, ctx, tx, buyer, checkoutRef, "VND")
	parentType := "parent"
	if _, err := insertVoucherUsageTestRow(ctx, tx, usageID, voucherID, buyer, checkoutRef, 10000, "VND", "committed", &orderID, &parentType); err != nil {
		t.Fatalf("setup committed usage: %v", err)
	}

	t.Run("delete shop with voucher rejected", func(t *testing.T) {
		assertVoucherTransactionSQLState(t, ctx, tx, "23503", func() error {
			_, err := tx.Exec(ctx, "DELETE FROM shops WHERE id = $1", shopVND)
			return err
		})
	})

	t.Run("delete voucher with usage rejected", func(t *testing.T) {
		assertVoucherTransactionSQLState(t, ctx, tx, "23503", func() error {
			_, err := tx.Exec(ctx, "DELETE FROM vouchers WHERE id = $1", voucherID)
			return err
		})
	})

	t.Run("delete user with usage rejected", func(t *testing.T) {
		assertVoucherTransactionSQLState(t, ctx, tx, "23503", func() error {
			_, err := tx.Exec(ctx, "DELETE FROM users WHERE id = $1", buyer)
			return err
		})
	})

	t.Run("delete parent order with committed usage rejected", func(t *testing.T) {
		assertVoucherTransactionSQLState(t, ctx, tx, "23503", func() error {
			_, err := tx.Exec(ctx, "DELETE FROM orders WHERE id = $1", orderID)
			return err
		})
	})
}

// TestVoucherMigrationEnforcesGlobalQuotaConcurrency xác nhận 100 goroutines tranh chấp slot cuối
// chỉ có chính xác một lượt thành công và counter luôn bằng số usage đã lưu.
func TestVoucherMigrationEnforcesGlobalQuotaConcurrency(t *testing.T) {
	ctx, pool := openMigrationTestPool(t)
	const concurrentWorkers = 100

	setupTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin setup tx: %v", err)
	}
	userIDs := make([]string, concurrentWorkers)
	for index := range userIDs {
		userIDs[index] = insertSellerCatalogTestUser(t, ctx, setupTx)
	}
	shopID := insertSellerCatalogTestShop(t, ctx, setupTx, "VND")
	voucherID := newMigrationTestUUID(t)
	const totalQuota = int64(1)
	if _, err := insertVoucherTestRow(ctx, setupTx, voucherID, "FLASH-SALE-LAST-SLOT", "shop", &shopID, "VND", "fixed_amount", 20000, nil, totalQuota); err != nil {
		t.Fatalf("setup flash voucher: %v", err)
	}
	if err := activateVoucherTestRow(ctx, setupTx, voucherID); err != nil {
		t.Fatalf("activate flash voucher: %v", err)
	}
	if err := setupTx.Commit(ctx); err != nil {
		t.Fatalf("commit setup tx: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM voucher_usages WHERE voucher_id = $1", voucherID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM vouchers WHERE id = $1", voucherID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM shops WHERE id = $1", shopID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM users WHERE id::text = ANY($1::text[])", userIDs)
	})

	start := make(chan struct{})
	results := make(chan voucherConcurrentResult, concurrentWorkers)
	var waitGroup sync.WaitGroup

	for i := 0; i < concurrentWorkers; i++ {
		userID := userIDs[i]
		checkoutRef := newMigrationTestUUID(t)
		usageID := newMigrationTestUUID(t)
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()
			<-start

			tx, txErr := pool.Begin(ctx)
			if txErr != nil {
				results <- voucherConcurrentResult{err: txErr}
				return
			}
			defer func() { _ = tx.Rollback(ctx) }()

			_, reserved, reserveErr := reserveVoucherUsageTest(ctx, tx, voucherID, userID, checkoutRef, usageID, 20000)
			if reserveErr != nil {
				results <- voucherConcurrentResult{err: reserveErr}
				return
			}
			if !reserved {
				results <- voucherConcurrentResult{success: false}
				return
			}

			if commitErr := tx.Commit(ctx); commitErr != nil {
				results <- voucherConcurrentResult{err: commitErr}
				return
			}

			results <- voucherConcurrentResult{success: true}
		}()
	}

	close(start)
	waitGroup.Wait()
	close(results)

	successCount := 0
	rejectedCount := 0
	for res := range results {
		if res.err != nil {
			t.Fatalf("concurrent quota reservation error: %v", res.err)
		}
		if res.success {
			successCount++
		} else {
			rejectedCount++
		}
	}

	if successCount != int(totalQuota) || rejectedCount != concurrentWorkers-int(totalQuota) {
		t.Fatalf("concurrency quota result: successes=%d, rejected=%d; want 1 and 99", successCount, rejectedCount)
	}

	// Xác nhận tính atomic: số bản ghi voucher_usages đúng bằng allocated_usage_count = 1.
	var finalAllocated int64
	if err := pool.QueryRow(ctx, "SELECT allocated_usage_count FROM vouchers WHERE id = $1", voucherID).Scan(&finalAllocated); err != nil {
		t.Fatalf("query final quota: %v", err)
	}
	if finalAllocated != totalQuota {
		t.Errorf("final allocated_usage_count = %d, want %d", finalAllocated, totalQuota)
	}

	var storedUsagesCount int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM voucher_usages WHERE voucher_id = $1", voucherID).Scan(&storedUsagesCount); err != nil {
		t.Fatalf("query stored usages: %v", err)
	}
	if storedUsagesCount != totalQuota {
		t.Errorf("stored voucher_usages = %d, want %d", storedUsagesCount, totalQuota)
	}
}

// TestVoucherMigrationEnforcesExpireVsCommitCASRace chứng minh không thể commit usage đã hết hạn TTL,
// và TTL worker giải phóng quota an toàn không gây split-brain.
func TestVoucherMigrationEnforcesExpireVsCommitCASRace(t *testing.T) {
	ctx, pool := openMigrationTestPool(t)

	setupTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin setup tx: %v", err)
	}
	userID := insertSellerCatalogTestUser(t, ctx, setupTx)
	shopID := insertSellerCatalogTestShop(t, ctx, setupTx, "VND")
	voucherID := newMigrationTestUUID(t)
	if _, err := insertVoucherTestRow(ctx, setupTx, voucherID, "CAS-RACE-VOUCHER", "shop", &shopID, "VND", "fixed_amount", 10000, nil, 10); err != nil {
		t.Fatalf("setup voucher: %v", err)
	}
	// Đặt counter là 1 tương ứng với 1 reserved usage
	if _, err := setupTx.Exec(ctx, "UPDATE vouchers SET allocated_usage_count = 1 WHERE id = $1", voucherID); err != nil {
		t.Fatalf("set counter: %v", err)
	}

	checkoutRef := newMigrationTestUUID(t)
	orderID := insertVoucherTestParentOrder(t, ctx, setupTx, userID, checkoutRef, "VND")
	usageID := newMigrationTestUUID(t)

	// Tạo 1 reserved usage có expires_at ở quá khứ (đã quá hạn TTL 50ms)
	if _, err := setupTx.Exec(ctx, `
		INSERT INTO voucher_usages (
			id, voucher_id, user_id, checkout_reference_id,
			discount_amount, currency_code, status, expires_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, 10000, 'VND', 'reserved', now() + interval '50 milliseconds', now(), now())
	`, usageID, voucherID, userID, checkoutRef); err != nil {
		t.Fatalf("setup past reserved usage: %v", err)
	}
	if err := setupTx.Commit(ctx); err != nil {
		t.Fatalf("commit setup tx: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM voucher_usages WHERE id = $1", usageID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM orders WHERE id = $1", orderID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM vouchers WHERE id = $1", voucherID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM shops WHERE id = $1", shopID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", userID)
	})

	// Chờ nhẹ để bảo đảm vượt qua mốc 50ms expires_at
	time.Sleep(60 * time.Millisecond)

	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	var workerAExpired bool
	var workerBCommitted bool
	var workerAErr, workerBErr error

	waitGroup.Add(2)

	// Luồng A: TTL Worker quét quá hạn -> CAS chuyển reserved sang expired VÀ giảm allocated_usage_count
	go func() {
		defer waitGroup.Done()
		<-start

		tx, txErr := pool.Begin(ctx)
		if txErr != nil {
			workerAErr = txErr
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()

		tag, err := tx.Exec(ctx, `
			UPDATE voucher_usages
			SET status = 'expired', expired_at = now(), updated_at = now()
			WHERE id = $1 AND status = 'reserved' AND expires_at <= clock_timestamp()
		`, usageID)
		if err != nil {
			workerAErr = err
			return
		}

		if tag.RowsAffected() == 1 {
			if _, decErr := tx.Exec(ctx, `
				UPDATE vouchers
				SET allocated_usage_count = allocated_usage_count - 1, updated_at = now()
				WHERE id = $1
			`, voucherID); decErr != nil {
				workerAErr = decErr
				return
			}
			if commitErr := tx.Commit(ctx); commitErr != nil {
				workerAErr = commitErr
				return
			}
			workerAExpired = true
		}
	}()

	// Luồng B: Payment Webhook -> Cố gắng commit nhưng PHẢI kiểm tra deadline: expires_at > clock_timestamp()
	go func() {
		defer waitGroup.Done()
		<-start

		tag, err := pool.Exec(ctx, `
			UPDATE voucher_usages
			SET status = 'committed', order_id = $2, parent_order_type = 'parent', committed_at = now(), updated_at = now()
			WHERE id = $1 AND status = 'reserved' AND expires_at > clock_timestamp()
		`, usageID, orderID)
		workerBErr = err
		workerBCommitted = tag.RowsAffected() == 1
	}()

	close(start)
	waitGroup.Wait()

	if workerAErr != nil || workerBErr != nil {
		t.Fatalf("race execution error: workerA=%v, workerB=%v", workerAErr, workerBErr)
	}

	// Invariant quan trọng: Luồng B (commit sau khi hết hạn) BẮT BUỘC phải thất bại (workerBCommitted = false)
	if workerBCommitted {
		t.Fatalf("commit succeeded on an expired voucher usage! Invariant violated")
	}

	// Luồng A (expire) phải thành công chuyển sang expired
	if !workerAExpired {
		t.Fatalf("TTL worker failed to expire past usage")
	}

	var finalStatus string
	if err := pool.QueryRow(ctx, "SELECT status FROM voucher_usages WHERE id = $1", usageID).Scan(&finalStatus); err != nil {
		t.Fatalf("query final usage status: %v", err)
	}
	if finalStatus != "expired" {
		t.Errorf("status = %s, want expired", finalStatus)
	}

	var finalAllocated int64
	if err := pool.QueryRow(ctx, "SELECT allocated_usage_count FROM vouchers WHERE id = $1", voucherID).Scan(&finalAllocated); err != nil {
		t.Fatalf("query counter: %v", err)
	}
	if finalAllocated != 0 {
		t.Errorf("allocated_usage_count = %d, want 0 after expiration", finalAllocated)
	}
}

// TestVoucherMigrationEnforcesPerUserAdvisoryLock chứng minh advisory lock theo (voucher_id, user_id)
// kết hợp atomic counter và insert usage chặn 100 requests đồng thời vượt usage_limit_per_user = 1.
func TestVoucherMigrationEnforcesPerUserAdvisoryLock(t *testing.T) {
	ctx, pool := openMigrationTestPool(t)

	setupTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin setup tx: %v", err)
	}
	userID := insertSellerCatalogTestUser(t, ctx, setupTx)
	shopID := insertSellerCatalogTestShop(t, ctx, setupTx, "VND")
	voucherID := newMigrationTestUUID(t)
	if _, err := setupTx.Exec(ctx, `
		INSERT INTO vouchers (
			id, code, scope, shop_id, discount_type, discount_value,
			currency_code, usage_limit, usage_limit_per_user, starts_at, status
		)
		VALUES ($1, 'ONE-PER-USER', 'shop', $2, 'fixed_amount', 10000, 'VND', 100, 1, now() - interval '1 minute', 'active')
	`, voucherID, shopID); err != nil {
		t.Fatalf("setup per-user voucher: %v", err)
	}
	if err := setupTx.Commit(ctx); err != nil {
		t.Fatalf("commit setup tx: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM voucher_usages WHERE voucher_id = $1", voucherID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM vouchers WHERE id = $1", voucherID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM shops WHERE id = $1", shopID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", userID)
	})

	const concurrentAttempts = 100
	start := make(chan struct{})
	results := make(chan voucherConcurrentResult, concurrentAttempts)
	var waitGroup sync.WaitGroup

	for i := 0; i < concurrentAttempts; i++ {
		checkoutRef := newMigrationTestUUID(t)
		usageID := newMigrationTestUUID(t)
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()
			<-start

			tx, txErr := pool.Begin(ctx)
			if txErr != nil {
				results <- voucherConcurrentResult{err: txErr}
				return
			}
			defer func() { _ = tx.Rollback(ctx) }()

			_, reserved, reserveErr := reserveVoucherUsageTest(ctx, tx, voucherID, userID, checkoutRef, usageID, 10000)
			if reserveErr != nil {
				results <- voucherConcurrentResult{err: reserveErr}
				return
			}
			if !reserved {
				results <- voucherConcurrentResult{success: false}
				return
			}

			if err := tx.Commit(ctx); err != nil {
				results <- voucherConcurrentResult{err: err}
				return
			}

			results <- voucherConcurrentResult{success: true}
		}()
	}

	close(start)
	waitGroup.Wait()
	close(results)

	successCount := 0
	rejectedCount := 0
	for res := range results {
		if res.err != nil {
			t.Fatalf("per-user concurrency error: %v", res.err)
		}
		if res.success {
			successCount++
		} else {
			rejectedCount++
		}
	}

	if successCount != 1 || rejectedCount != concurrentAttempts-1 {
		t.Fatalf("per-user advisory lock results: successes=%d, rejected=%d; want exactly 1 and 99", successCount, rejectedCount)
	}

	// Đảm bảo global allocated_usage_count cũng chỉ tăng đúng 1
	var finalAllocated int64
	if err := pool.QueryRow(ctx, "SELECT allocated_usage_count FROM vouchers WHERE id = $1", voucherID).Scan(&finalAllocated); err != nil {
		t.Fatalf("query counter: %v", err)
	}
	if finalAllocated != 1 {
		t.Errorf("allocated_usage_count = %d, want 1", finalAllocated)
	}

	var storedUsagesCount int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM voucher_usages WHERE voucher_id = $1 AND user_id = $2", voucherID, userID).Scan(&storedUsagesCount); err != nil {
		t.Fatalf("query per-user usages: %v", err)
	}
	if storedUsagesCount != 1 {
		t.Errorf("per-user stored voucher_usages = %d, want 1", storedUsagesCount)
	}
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

// assertVoucherTransactionSQLState cô lập một lỗi PostgreSQL mong đợi trong savepoint để transaction cha tiếp tục dùng được.
func assertVoucherTransactionSQLState(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	expectedCode string,
	operation func() error,
) {
	t.Helper()

	if _, err := tx.Exec(ctx, "SAVEPOINT voucher_expected_error"); err != nil {
		t.Fatalf("create Voucher expected-error savepoint: %v", err)
	}
	assertMigrationSQLState(t, operation(), expectedCode)
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT voucher_expected_error"); err != nil {
		t.Fatalf("rollback Voucher expected-error savepoint: %v", err)
	}
	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT voucher_expected_error"); err != nil {
		t.Fatalf("release Voucher expected-error savepoint: %v", err)
	}
}

// activateVoucherTestRow chuyển Voucher fixture sang active với cửa sổ hiệu lực bao phủ thời điểm test.
func activateVoucherTestRow(ctx context.Context, tx pgx.Tx, voucherID string) error {
	_, err := tx.Exec(ctx, `
		UPDATE vouchers
		SET status = 'active',
			starts_at = clock_timestamp() - interval '1 minute',
			ends_at = clock_timestamp() + interval '1 hour',
			updated_at = clock_timestamp()
		WHERE id = $1
	`, voucherID)
	return err
}

// reserveVoucherUsageTest mô phỏng transaction reserve canonical: khóa per-user, trả usage cũ khi retry, rồi cấp counter và insert atomically.
func reserveVoucherUsageTest(
	ctx context.Context,
	tx pgx.Tx,
	voucherID string,
	userID string,
	checkoutReferenceID string,
	candidateUsageID string,
	discountAmount int64,
) (string, bool, error) {
	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtext($1::text), hashtext($2::text))
	`, voucherID, userID); err != nil {
		return "", false, fmt.Errorf("acquire per-user Voucher lock: %w", err)
	}

	var existingUsageID string
	err := tx.QueryRow(ctx, `
		SELECT id
		FROM voucher_usages
		WHERE voucher_id = $1 AND checkout_reference_id = $2
	`, voucherID, checkoutReferenceID).Scan(&existingUsageID)
	if err == nil {
		return existingUsageID, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, fmt.Errorf("lookup existing Voucher usage: %w", err)
	}

	var usageLimitPerUser *int64
	if err := tx.QueryRow(ctx, `
		SELECT usage_limit_per_user
		FROM vouchers
		WHERE id = $1
	`, voucherID).Scan(&usageLimitPerUser); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("load Voucher per-user limit: %w", err)
	}

	if usageLimitPerUser != nil {
		var activeUsageCount int64
		if err := tx.QueryRow(ctx, `
			SELECT count(*)
			FROM voucher_usages
			WHERE voucher_id = $1
			  AND user_id = $2
			  AND status IN ('reserved', 'committed')
		`, voucherID, userID).Scan(&activeUsageCount); err != nil {
			return "", false, fmt.Errorf("count per-user Voucher usages: %w", err)
		}
		if activeUsageCount >= *usageLimitPerUser {
			return "", false, nil
		}
	}

	var currencyCode string
	err = tx.QueryRow(ctx, `
		UPDATE vouchers
		SET allocated_usage_count = allocated_usage_count + 1,
			updated_at = clock_timestamp()
		WHERE id = $1
		  AND status = 'active'
		  AND starts_at <= clock_timestamp()
		  AND (ends_at IS NULL OR ends_at > clock_timestamp())
		  AND (usage_limit IS NULL OR allocated_usage_count < usage_limit)
		RETURNING btrim(currency_code)
	`, voucherID).Scan(&currencyCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("allocate global Voucher quota: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO voucher_usages (
			id, voucher_id, user_id, checkout_reference_id,
			discount_amount, currency_code, status, expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'reserved', clock_timestamp() + interval '15 minutes')
	`, candidateUsageID, voucherID, userID, checkoutReferenceID, discountAmount, currencyCode); err != nil {
		return "", false, fmt.Errorf("insert reserved Voucher usage: %w", err)
	}

	return candidateUsageID, true, nil
}

// releaseVoucherUsageTest chuyển một reservation sang released và hoàn đúng một quota slot trong cùng transaction.
func releaseVoucherUsageTest(ctx context.Context, tx pgx.Tx, usageID string) (bool, error) {
	var voucherID string
	err := tx.QueryRow(ctx, `
		UPDATE voucher_usages
		SET status = 'released', released_at = clock_timestamp(), updated_at = clock_timestamp()
		WHERE id = $1 AND status = 'reserved'
		RETURNING voucher_id
	`, usageID).Scan(&voucherID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("release Voucher usage: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		UPDATE vouchers
		SET allocated_usage_count = allocated_usage_count - 1,
			updated_at = clock_timestamp()
		WHERE id = $1 AND allocated_usage_count > 0
	`, voucherID)
	if err != nil {
		return false, fmt.Errorf("return released Voucher quota: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return false, fmt.Errorf("return released Voucher quota: counter was not positive")
	}

	return true, nil
}

// expireVoucherUsageTest chuyển reservation quá hạn sang expired và hoàn quota đúng một lần bằng CAS status.
func expireVoucherUsageTest(ctx context.Context, tx pgx.Tx, usageID string) (bool, error) {
	var voucherID string
	err := tx.QueryRow(ctx, `
		UPDATE voucher_usages
		SET status = 'expired', expired_at = clock_timestamp(), updated_at = clock_timestamp()
		WHERE id = $1
		  AND status = 'reserved'
		  AND expires_at <= clock_timestamp()
		RETURNING voucher_id
	`, usageID).Scan(&voucherID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("expire Voucher usage: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		UPDATE vouchers
		SET allocated_usage_count = allocated_usage_count - 1,
			updated_at = clock_timestamp()
		WHERE id = $1 AND allocated_usage_count > 0
	`, voucherID)
	if err != nil {
		return false, fmt.Errorf("return expired Voucher quota: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return false, fmt.Errorf("return expired Voucher quota: counter was not positive")
	}

	return true, nil
}

// commitVoucherUsageTest chuyển reservation còn hạn sang committed bằng CAS và không thay đổi quota đã cấp.
func commitVoucherUsageTest(ctx context.Context, tx pgx.Tx, usageID, orderID string) (bool, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE voucher_usages
		SET status = 'committed',
			order_id = $2,
			parent_order_type = 'parent',
			committed_at = clock_timestamp(),
			updated_at = clock_timestamp()
		WHERE id = $1
		  AND status = 'reserved'
		  AND expires_at > clock_timestamp()
	`, usageID, orderID)
	if err != nil {
		return false, fmt.Errorf("commit Voucher usage: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// insertVoucherTestRow chèn một bản ghi Voucher tùy chỉnh để kiểm thử các trường hợp hợp lệ hoặc vi phạm ràng buộc DDL.
func insertVoucherTestRow(
	ctx context.Context,
	tx pgx.Tx,
	id, code, scope string,
	shopID *string,
	currency, discountType string,
	discountValue int64,
	maxDiscount *int64,
	usageLimit int64,
) (string, error) {
	_, err := tx.Exec(ctx, `
		INSERT INTO vouchers (
			id, code, scope, shop_id, discount_type, discount_value,
			max_discount_amount, currency_code, usage_limit, starts_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
	`, id, code, scope, shopID, discountType, discountValue, maxDiscount, currency, usageLimit)
	return id, err
}

// insertVoucherUsageTestRow chèn một bản ghi VoucherUsage tùy biến với đầy đủ các mốc thời gian và liên kết đơn hàng.
func insertVoucherUsageTestRow(
	ctx context.Context,
	tx pgx.Tx,
	id, voucherID, userID, checkoutRef string,
	discountAmount int64,
	currency, status string,
	orderID *string,
	parentOrderType *string,
) (string, error) {
	var committedAt, releasedAt, expiredAt *time.Time
	now := time.Now()
	switch status {
	case "committed":
		committedAt = &now
	case "released":
		releasedAt = &now
	case "expired":
		expiredAt = &now
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO voucher_usages (
			id, voucher_id, user_id, checkout_reference_id, order_id, parent_order_type,
			discount_amount, currency_code, status, expires_at, created_at, updated_at,
			committed_at, released_at, expired_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now() + interval '15 minutes', now(), now(), $10, $11, $12)
	`, id, voucherID, userID, checkoutRef, orderID, parentOrderType,
		discountAmount, currency, status, committedAt, releasedAt, expiredAt)
	return id, err
}

// insertVoucherTestParentOrder chèn một Parent Order hợp lệ gắn với User, Checkout Reference và Currency nhất định.
func insertVoucherTestParentOrder(t *testing.T, ctx context.Context, tx pgx.Tx, userID, checkoutRefID, currency string) string {
	t.Helper()
	orderID := newMigrationTestUUID(t)
	orderNumber := "ORD-PARENT-" + newMigrationTestUUID(t)[:8]
	if _, err := tx.Exec(ctx, `
		INSERT INTO orders (
			id, order_number, order_type, checkout_reference_id, user_id, currency,
			subtotal_amount, total_amount, customer_name_snapshot,
			customer_email_snapshot, customer_phone_snapshot
		)
		VALUES ($1, $2, 'parent', $3, $4, $5, 100000, 100000, 'Buyer Snapshot', 'buyer@example.com', '0900000000')
	`, orderID, orderNumber, checkoutRefID, userID, currency); err != nil {
		t.Fatalf("insert Parent Order for Voucher test: %v", err)
	}
	return orderID
}

// insertVoucherTestSellerOrder chèn một Seller Order hợp lệ gắn với Shop và Parent Order để kiểm tra loại trừ attachment.
func insertVoucherTestSellerOrder(t *testing.T, ctx context.Context, tx pgx.Tx, parentID, userID, shopID, currency string) string {
	t.Helper()
	orderID := newMigrationTestUUID(t)
	orderNumber := "ORD-SELLER-" + newMigrationTestUUID(t)[:8]
	if _, err := tx.Exec(ctx, `
		INSERT INTO orders (
			id, order_number, order_type, parent_order_id, parent_order_type,
			user_id, shop_id, currency, subtotal_amount, total_amount,
			customer_name_snapshot, customer_email_snapshot, customer_phone_snapshot,
			shop_name_snapshot
		)
		VALUES ($1, $2, 'seller', $3, 'parent', $4, $5, $6, 100000, 100000,
			'Buyer Snapshot', 'buyer@example.com', '0900000000', 'Shop Snapshot')
	`, orderID, orderNumber, parentID, userID, shopID, currency); err != nil {
		t.Fatalf("insert Seller Order for Voucher test: %v", err)
	}
	return orderID
}
