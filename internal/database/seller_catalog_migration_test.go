// File này kiểm thử migration Seller/Catalog bằng PostgreSQL thật và chứng minh các invariant tenant, currency, ownership được database cưỡng chế.
package database

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// sellerCatalogTestFixture giữ ID của một chuỗi Seller, Shop, Product, Variant và SKU hợp lệ dùng trong test.
type sellerCatalogTestFixture struct {
	userID          string
	sellerAccountID string
	shopID          string
	categoryID      string
	brandID         string
	productID       string
	variantID       string
	skuID           string
}

// TestSellerCatalogMigrationCreatesExpectedObjects xác nhận đủ tám bảng cùng các index quan trọng của Seller và Catalog tồn tại.
func TestSellerCatalogMigrationCreatesExpectedObjects(t *testing.T) {
	ctx, tx := beginMigrationTest(t)

	objects := []string{
		"seller_accounts",
		"shops",
		"shop_memberships",
		"categories",
		"brands",
		"products",
		"product_variants",
		"skus",
		"uq_shop_memberships_active_owner",
		"idx_shop_memberships_seller_status",
		"idx_shop_memberships_shop_status",
		"uq_categories_root_slug",
		"uq_categories_parent_slug",
		"uq_brands_name_ci",
		"idx_products_shop_status",
		"uq_product_variants_name_ci",
		"idx_skus_shop_status",
		"idx_skus_shop_price_active",
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

// TestSellerCatalogMigrationUsesCanonicalTypesAndDeleteRules xác nhận UUID do Go tạo, timestamp chuẩn và mọi FK đều RESTRICT.
func TestSellerCatalogMigrationUsesCanonicalTypesAndDeleteRules(t *testing.T) {
	ctx, tx := beginMigrationTest(t)

	tables := []string{
		"seller_accounts",
		"shops",
		"shop_memberships",
		"categories",
		"brands",
		"products",
		"product_variants",
		"skus",
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
		"fk_seller_accounts_user",
		"fk_shop_memberships_shop",
		"fk_shop_memberships_seller_account",
		"fk_shop_memberships_created_by_seller_account",
		"fk_categories_parent",
		"fk_products_shop",
		"fk_products_category",
		"fk_products_brand",
		"fk_product_variants_product_shop",
		"fk_skus_variant_shop",
		"fk_skus_shop_currency",
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
		t.Fatalf("inspect Seller/Catalog timestamps: %v", err)
	}
	if invalidTimestampColumns != 0 {
		t.Errorf("Seller/Catalog has %d non-TIMESTAMPTZ absolute-time columns", invalidTimestampColumns)
	}

	var priceType string
	if err := tx.QueryRow(ctx, `
		SELECT data_type
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'skus' AND column_name = 'price_amount_minor'
	`).Scan(&priceType); err != nil {
		t.Fatalf("inspect skus.price_amount_minor: %v", err)
	}
	if priceType != "bigint" {
		t.Errorf("skus.price_amount_minor type = %s, want bigint", priceType)
	}
}

// TestSellerCatalogMigrationAcceptsValidGraph xác nhận một chuỗi User đến SKU hợp lệ có thể được lưu đầy đủ.
func TestSellerCatalogMigrationAcceptsValidGraph(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	fixture := insertSellerCatalogTestFixture(t, ctx, tx, "VND")

	ids := []string{
		fixture.userID,
		fixture.sellerAccountID,
		fixture.shopID,
		fixture.categoryID,
		fixture.brandID,
		fixture.productID,
		fixture.variantID,
		fixture.skuID,
	}
	for _, id := range ids {
		if id == "" {
			t.Fatal("valid Seller/Catalog fixture contains an empty ID")
		}
	}
}

// TestSellerCatalogMigrationEnforcesSellerInvariants kiểm tra một SellerAccount/User và một membership/Seller/Shop là duy nhất.
func TestSellerCatalogMigrationEnforcesSellerInvariants(t *testing.T) {
	t.Run("one seller account per user", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		userID := insertSellerCatalogTestUser(t, ctx, tx)
		insertSellerCatalogTestSellerAccount(t, ctx, tx, userID)

		_, err := tx.Exec(ctx, `
			INSERT INTO seller_accounts (id, user_id, status)
			VALUES ($1, $2, 'active')
		`, newMigrationTestUUID(t), userID)
		assertMigrationSQLState(t, err, "23505")
	})

	t.Run("one membership per seller and shop", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		fixture := insertSellerCatalogTestFixture(t, ctx, tx, "VND")

		_, err := tx.Exec(ctx, `
			INSERT INTO shop_memberships (
				id, shop_id, seller_account_id, role, status, accepted_at
			)
			VALUES ($1, $2, $3, 'viewer', 'active', now())
		`, newMigrationTestUUID(t), fixture.shopID, fixture.sellerAccountID)
		assertMigrationSQLState(t, err, "23505")
	})

	t.Run("one active owner per shop", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		fixture := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		secondUserID := insertSellerCatalogTestUser(t, ctx, tx)
		secondSellerID := insertSellerCatalogTestSellerAccount(t, ctx, tx, secondUserID)

		_, err := tx.Exec(ctx, `
			INSERT INTO shop_memberships (
				id, shop_id, seller_account_id, role, status, accepted_at
			)
			VALUES ($1, $2, $3, 'owner', 'active', now())
		`, newMigrationTestUUID(t), fixture.shopID, secondSellerID)
		assertMigrationSQLState(t, err, "23505")
	})
}

// TestSellerCatalogMigrationRejectsInvalidSellerLifecycle kiểm tra timestamp phải khớp lifecycle của Seller, Shop và Membership.
func TestSellerCatalogMigrationRejectsInvalidSellerLifecycle(t *testing.T) {
	t.Run("suspended seller requires timestamp", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		userID := insertSellerCatalogTestUser(t, ctx, tx)

		_, err := tx.Exec(ctx, `
			INSERT INTO seller_accounts (id, user_id, status)
			VALUES ($1, $2, 'suspended')
		`, newMigrationTestUUID(t), userID)
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("closed shop requires timestamp", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)

		_, err := tx.Exec(ctx, `
			INSERT INTO shops (id, name, slug, currency_code, status)
			VALUES ($1, 'Closed Shop', $2, 'VND', 'closed')
		`, newMigrationTestUUID(t), sellerCatalogTestSlug(t, "closed-shop"))
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("active membership requires acceptance", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		userID := insertSellerCatalogTestUser(t, ctx, tx)
		sellerID := insertSellerCatalogTestSellerAccount(t, ctx, tx, userID)
		shopID := insertSellerCatalogTestShop(t, ctx, tx, "VND")

		_, err := tx.Exec(ctx, `
			INSERT INTO shop_memberships (id, shop_id, seller_account_id, role, status)
			VALUES ($1, $2, $3, 'owner', 'active')
		`, newMigrationTestUUID(t), shopID, sellerID)
		assertMigrationSQLState(t, err, "23514")
	})
}

// TestSellerCatalogMigrationEnforcesTenantAndCurrency kiểm tra Variant, SKU và currency không thể gắn chéo Shop.
func TestSellerCatalogMigrationEnforcesTenantAndCurrency(t *testing.T) {
	t.Run("cross-shop variant rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		fixture := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		otherShopID := insertSellerCatalogTestShop(t, ctx, tx, "USD")

		_, err := tx.Exec(ctx, `
			INSERT INTO product_variants (id, product_id, shop_id, name)
			VALUES ($1, $2, $3, 'Cross Shop Variant')
		`, newMigrationTestUUID(t), fixture.productID, otherShopID)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("cross-shop sku rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		fixture := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		otherShopID := insertSellerCatalogTestShop(t, ctx, tx, "USD")

		_, err := tx.Exec(ctx, `
			INSERT INTO skus (
				id, variant_id, shop_id, sku_code, name, price_amount_minor, currency_code
			)
			VALUES ($1, $2, $3, $4, 'Cross Shop SKU', 1000, 'USD')
		`, newMigrationTestUUID(t), fixture.variantID, otherShopID, sellerCatalogTestSlug(t, "cross-shop-sku"))
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("wrong sku currency rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		fixture := insertSellerCatalogTestFixture(t, ctx, tx, "VND")

		_, err := tx.Exec(ctx, `
			INSERT INTO skus (
				id, variant_id, shop_id, sku_code, name, price_amount_minor, currency_code
			)
			VALUES ($1, $2, $3, $4, 'Wrong Currency SKU', 1000, 'USD')
		`, newMigrationTestUUID(t), fixture.variantID, fixture.shopID, sellerCatalogTestSlug(t, "wrong-currency"))
		assertMigrationSQLState(t, err, "23503")
	})
}

// TestSellerCatalogMigrationEnforcesScopedUniqueness kiểm tra slug/name/SKU code chỉ được lặp trong đúng phạm vi thiết kế cho phép.
func TestSellerCatalogMigrationEnforcesScopedUniqueness(t *testing.T) {
	t.Run("duplicate sku code in one shop rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		fixture := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		secondVariantID := insertSellerCatalogTestVariant(t, ctx, tx, fixture.productID, fixture.shopID, "Second Variant")

		_, err := tx.Exec(ctx, `
			INSERT INTO skus (
				id, variant_id, shop_id, sku_code, name, price_amount_minor, currency_code
			)
			VALUES ($1, $2, $3, 'DEFAULT-SKU', 'Duplicate SKU', 1000, 'VND')
		`, newMigrationTestUUID(t), secondVariantID, fixture.shopID)
		assertMigrationSQLState(t, err, "23505")
	})

	t.Run("same sku code in different shops allowed", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		first := insertSellerCatalogTestFixture(t, ctx, tx, "VND")
		second := insertSellerCatalogTestFixture(t, ctx, tx, "USD")

		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM skus
			WHERE sku_code = 'DEFAULT-SKU' AND shop_id IN ($1, $2)
		`, first.shopID, second.shopID).Scan(&count); err != nil {
			t.Fatalf("count repeated per-shop SKU codes: %v", err)
		}
		if count != 2 {
			t.Fatalf("same SKU code across shops count = %d, want 2", count)
		}
	})

	t.Run("duplicate root category slug rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		slug := sellerCatalogTestSlug(t, "root-category")
		insertSellerCatalogTestCategoryWithSlug(t, ctx, tx, nil, slug)

		_, err := tx.Exec(ctx, `
			INSERT INTO categories (id, name, slug)
			VALUES ($1, 'Duplicate Root', $2)
		`, newMigrationTestUUID(t), slug)
		assertMigrationSQLState(t, err, "23505")
	})

	t.Run("duplicate sibling category slug rejected", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		parentID := insertSellerCatalogTestCategory(t, ctx, tx)
		slug := sellerCatalogTestSlug(t, "child-category")
		insertSellerCatalogTestCategoryWithSlug(t, ctx, tx, &parentID, slug)

		_, err := tx.Exec(ctx, `
			INSERT INTO categories (id, parent_id, name, slug)
			VALUES ($1, $2, 'Duplicate Child', $3)
		`, newMigrationTestUUID(t), parentID, slug)
		assertMigrationSQLState(t, err, "23505")
	})

	t.Run("brand normalized name is unique", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		_, err := tx.Exec(ctx, `
			INSERT INTO brands (id, name, slug)
			VALUES ($1, 'Nexus Brand', $2), ($3, '  nexus brand  ', $4)
		`,
			newMigrationTestUUID(t), sellerCatalogTestSlug(t, "brand-one"),
			newMigrationTestUUID(t), sellerCatalogTestSlug(t, "brand-two"),
		)
		assertMigrationSQLState(t, err, "23505")
	})
}

// TestSellerCatalogMigrationRejectsInvalidCatalogValues kiểm tra lifecycle, JSON attributes và money constraints của Catalog.
func TestSellerCatalogMigrationRejectsInvalidCatalogValues(t *testing.T) {
	t.Run("active product requires publication timestamp", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		fixture := insertSellerCatalogTestFixture(t, ctx, tx, "VND")

		_, err := tx.Exec(ctx, `
			INSERT INTO products (id, shop_id, category_id, name, slug, status)
			VALUES ($1, $2, $3, 'Unpublished Product', $4, 'active')
		`, newMigrationTestUUID(t), fixture.shopID, fixture.categoryID, sellerCatalogTestSlug(t, "unpublished"))
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("rejected product requires reason", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		fixture := insertSellerCatalogTestFixture(t, ctx, tx, "VND")

		_, err := tx.Exec(ctx, `
			INSERT INTO products (id, shop_id, category_id, name, slug, status)
			VALUES ($1, $2, $3, 'Rejected Product', $4, 'rejected')
		`, newMigrationTestUUID(t), fixture.shopID, fixture.categoryID, sellerCatalogTestSlug(t, "rejected"))
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("variant attributes must be object", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		fixture := insertSellerCatalogTestFixture(t, ctx, tx, "VND")

		_, err := tx.Exec(ctx, `
			INSERT INTO product_variants (id, product_id, shop_id, name, attributes)
			VALUES ($1, $2, $3, 'Array Attributes', '[]'::jsonb)
		`, newMigrationTestUUID(t), fixture.productID, fixture.shopID)
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("sku price cannot be negative", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		fixture := insertSellerCatalogTestFixture(t, ctx, tx, "VND")

		_, err := tx.Exec(ctx, `
			INSERT INTO skus (
				id, variant_id, shop_id, sku_code, name, price_amount_minor, currency_code
			)
			VALUES ($1, $2, $3, $4, 'Negative Price', -1, 'VND')
		`, newMigrationTestUUID(t), fixture.variantID, fixture.shopID, sellerCatalogTestSlug(t, "negative-price"))
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("compare-at price cannot be below sale price", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		fixture := insertSellerCatalogTestFixture(t, ctx, tx, "VND")

		_, err := tx.Exec(ctx, `
			INSERT INTO skus (
				id, variant_id, shop_id, sku_code, name,
				price_amount_minor, compare_at_price_amount_minor, currency_code
			)
			VALUES ($1, $2, $3, $4, 'Invalid Compare Price', 2000, 1000, 'VND')
		`, newMigrationTestUUID(t), fixture.variantID, fixture.shopID, sellerCatalogTestSlug(t, "compare-price"))
		assertMigrationSQLState(t, err, "23514")
	})

	t.Run("category cannot parent itself", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		categoryID := newMigrationTestUUID(t)

		_, err := tx.Exec(ctx, `
			INSERT INTO categories (id, parent_id, name, slug)
			VALUES ($1, $1, 'Self Parent', $2)
		`, categoryID, sellerCatalogTestSlug(t, "self-parent"))
		assertMigrationSQLState(t, err, "23514")
	})
}

// TestSellerCatalogMigrationRequiresCatalogParents xác nhận Product, Variant và SKU không thể bỏ qua parent bắt buộc.
func TestSellerCatalogMigrationRequiresCatalogParents(t *testing.T) {
	t.Run("product requires existing shop", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		categoryID := insertSellerCatalogTestCategory(t, ctx, tx)

		_, err := tx.Exec(ctx, `
			INSERT INTO products (id, shop_id, category_id, name, slug)
			VALUES ($1, $2, $3, 'Missing Shop Product', $4)
		`, newMigrationTestUUID(t), newMigrationTestUUID(t), categoryID, sellerCatalogTestSlug(t, "missing-shop"))
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("variant requires existing product", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		shopID := insertSellerCatalogTestShop(t, ctx, tx, "VND")

		_, err := tx.Exec(ctx, `
			INSERT INTO product_variants (id, product_id, shop_id, name)
			VALUES ($1, $2, $3, 'Missing Product Variant')
		`, newMigrationTestUUID(t), newMigrationTestUUID(t), shopID)
		assertMigrationSQLState(t, err, "23503")
	})

	t.Run("sku requires existing variant", func(t *testing.T) {
		ctx, tx := beginMigrationTest(t)
		shopID := insertSellerCatalogTestShop(t, ctx, tx, "VND")

		_, err := tx.Exec(ctx, `
			INSERT INTO skus (
				id, variant_id, shop_id, sku_code, name, price_amount_minor, currency_code
			)
			VALUES ($1, $2, $3, $4, 'Missing Variant SKU', 1000, 'VND')
		`, newMigrationTestUUID(t), newMigrationTestUUID(t), shopID, sellerCatalogTestSlug(t, "missing-variant"))
		assertMigrationSQLState(t, err, "23503")
	})
}

// TestSellerCatalogMigrationExcludesInventoryFields xác nhận SKU chỉ giữ dữ liệu Catalog và không sở hữu quantity tồn kho.
func TestSellerCatalogMigrationExcludesInventoryFields(t *testing.T) {
	ctx, tx := beginMigrationTest(t)

	var forbiddenColumnCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name = 'skus'
		  AND column_name IN ('stock_quantity', 'available_quantity', 'reserved_quantity', 'on_hand_quantity')
	`).Scan(&forbiddenColumnCount); err != nil {
		t.Fatalf("inspect SKU inventory fields: %v", err)
	}
	if forbiddenColumnCount != 0 {
		t.Fatalf("skus contains %d inventory-owned columns, want none", forbiddenColumnCount)
	}
}

// insertSellerCatalogTestFixture tạo một graph hợp lệ từ User đến SKU cho các test cần dữ liệu nền đầy đủ.
func insertSellerCatalogTestFixture(t *testing.T, ctx context.Context, tx pgx.Tx, currencyCode string) sellerCatalogTestFixture {
	t.Helper()

	fixture := sellerCatalogTestFixture{}
	fixture.userID = insertSellerCatalogTestUser(t, ctx, tx)
	fixture.sellerAccountID = insertSellerCatalogTestSellerAccount(t, ctx, tx, fixture.userID)
	fixture.shopID = insertSellerCatalogTestShop(t, ctx, tx, currencyCode)
	insertSellerCatalogTestOwner(t, ctx, tx, fixture.shopID, fixture.sellerAccountID)
	fixture.categoryID = insertSellerCatalogTestCategory(t, ctx, tx)
	fixture.brandID = insertSellerCatalogTestBrand(t, ctx, tx)
	fixture.productID = insertSellerCatalogTestProduct(t, ctx, tx, fixture.shopID, fixture.categoryID, fixture.brandID)
	fixture.variantID = insertSellerCatalogTestVariant(t, ctx, tx, fixture.productID, fixture.shopID, "Default Variant")
	fixture.skuID = insertSellerCatalogTestSKU(t, ctx, tx, fixture.variantID, fixture.shopID, currencyCode)

	return fixture
}

// insertSellerCatalogTestUser tạo User nền hợp lệ cho SellerAccount trong integration test.
func insertSellerCatalogTestUser(t *testing.T, ctx context.Context, tx pgx.Tx) string {
	t.Helper()

	userID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, display_name)
		VALUES ($1, 'Seller Catalog Test User')
	`, userID); err != nil {
		t.Fatalf("insert Seller/Catalog test user: %v", err)
	}

	return userID
}

// insertSellerCatalogTestSellerAccount tạo SellerAccount active gắn với User đã có.
func insertSellerCatalogTestSellerAccount(t *testing.T, ctx context.Context, tx pgx.Tx, userID string) string {
	t.Helper()

	sellerAccountID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO seller_accounts (id, user_id, status)
		VALUES ($1, $2, 'active')
	`, sellerAccountID, userID); err != nil {
		t.Fatalf("insert SellerAccount: %v", err)
	}

	return sellerAccountID
}

// insertSellerCatalogTestShop tạo Shop active với currency duy nhất dùng cho SKU của Shop đó.
func insertSellerCatalogTestShop(t *testing.T, ctx context.Context, tx pgx.Tx, currencyCode string) string {
	t.Helper()

	shopID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO shops (id, name, slug, currency_code, status)
		VALUES ($1, 'Seller Catalog Test Shop', $2, $3, 'active')
	`, shopID, sellerCatalogSlugFromID("shop", shopID), currencyCode); err != nil {
		t.Fatalf("insert Shop: %v", err)
	}

	return shopID
}

// insertSellerCatalogTestOwner tạo active owner membership hợp lệ cho Shop.
func insertSellerCatalogTestOwner(t *testing.T, ctx context.Context, tx pgx.Tx, shopID, sellerAccountID string) string {
	t.Helper()

	membershipID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO shop_memberships (
			id, shop_id, seller_account_id, role, status, accepted_at
		)
		VALUES ($1, $2, $3, 'owner', 'active', now())
	`, membershipID, shopID, sellerAccountID); err != nil {
		t.Fatalf("insert active owner membership: %v", err)
	}

	return membershipID
}

// insertSellerCatalogTestCategory tạo root Category hợp lệ với slug duy nhất.
func insertSellerCatalogTestCategory(t *testing.T, ctx context.Context, tx pgx.Tx) string {
	t.Helper()

	return insertSellerCatalogTestCategoryWithSlug(t, ctx, tx, nil, sellerCatalogTestSlug(t, "category"))
}

// insertSellerCatalogTestCategoryWithSlug tạo Category root hoặc child với parent và slug được chỉ định.
func insertSellerCatalogTestCategoryWithSlug(t *testing.T, ctx context.Context, tx pgx.Tx, parentID *string, slug string) string {
	t.Helper()

	categoryID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO categories (id, parent_id, name, slug)
		VALUES ($1, $2, 'Seller Catalog Test Category', $3)
	`, categoryID, parentID, slug); err != nil {
		t.Fatalf("insert Category: %v", err)
	}

	return categoryID
}

// insertSellerCatalogTestBrand tạo Brand hợp lệ với normalized name và slug duy nhất.
func insertSellerCatalogTestBrand(t *testing.T, ctx context.Context, tx pgx.Tx) string {
	t.Helper()

	brandID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO brands (id, name, slug)
		VALUES ($1, $2, $3)
	`, brandID, "Brand "+sellerCatalogCompactID(brandID), sellerCatalogSlugFromID("brand", brandID)); err != nil {
		t.Fatalf("insert Brand: %v", err)
	}

	return brandID
}

// insertSellerCatalogTestProduct tạo Product active đã publish thuộc đúng Shop, Category và Brand.
func insertSellerCatalogTestProduct(t *testing.T, ctx context.Context, tx pgx.Tx, shopID, categoryID, brandID string) string {
	t.Helper()

	productID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO products (
			id, shop_id, category_id, brand_id, name, slug, status, published_at
		)
		VALUES ($1, $2, $3, $4, 'Seller Catalog Test Product', $5, 'active', now())
	`, productID, shopID, categoryID, brandID, sellerCatalogSlugFromID("product", productID)); err != nil {
		t.Fatalf("insert Product: %v", err)
	}

	return productID
}

// insertSellerCatalogTestVariant tạo ProductVariant hợp lệ và giữ shop_id trùng Product cha.
func insertSellerCatalogTestVariant(t *testing.T, ctx context.Context, tx pgx.Tx, productID, shopID, name string) string {
	t.Helper()

	variantID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO product_variants (id, product_id, shop_id, name, attributes)
		VALUES ($1, $2, $3, $4, '{"color":"black"}'::jsonb)
	`, variantID, productID, shopID, name); err != nil {
		t.Fatalf("insert ProductVariant: %v", err)
	}

	return variantID
}

// insertSellerCatalogTestSKU tạo SKU hợp lệ với giá minor-unit và currency khớp Shop.
func insertSellerCatalogTestSKU(t *testing.T, ctx context.Context, tx pgx.Tx, variantID, shopID, currencyCode string) string {
	t.Helper()

	skuID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO skus (
			id, variant_id, shop_id, sku_code, name, attributes,
			price_amount_minor, compare_at_price_amount_minor, currency_code
		)
		VALUES ($1, $2, $3, 'DEFAULT-SKU', 'Default SKU', '{"size":"default"}'::jsonb, 100000, 120000, $4)
	`, skuID, variantID, shopID, currencyCode); err != nil {
		t.Fatalf("insert SKU: %v", err)
	}

	return skuID
}

// sellerCatalogTestSlug tạo slug lowercase duy nhất cho dữ liệu test không xung đột giữa các transaction.
func sellerCatalogTestSlug(t *testing.T, prefix string) string {
	t.Helper()

	return sellerCatalogSlugFromID(prefix, newMigrationTestUUID(t))
}

// sellerCatalogSlugFromID ghép prefix với UUID bỏ dấu gạch để tạo slug canonical ổn định.
func sellerCatalogSlugFromID(prefix, id string) string {
	return strings.ToLower(prefix) + "-" + sellerCatalogCompactID(id)
}

// sellerCatalogCompactID loại bỏ dấu gạch khỏi UUID để dùng làm phần duy nhất trong name, slug và email test.
func sellerCatalogCompactID(id string) string {
	return strings.ReplaceAll(id, "-", "")
}
