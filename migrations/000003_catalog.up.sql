-- File UP này tạo schema Catalog V1 và dùng composite FK để bảo vệ chuỗi Product, Variant, SKU theo Shop và currency.
BEGIN;

CREATE TABLE categories (
    id UUID PRIMARY KEY,
    parent_id UUID,
    name VARCHAR(160) NOT NULL,
    slug VARCHAR(160) NOT NULL,
    icon_object_key VARCHAR(512),
    image_object_key VARCHAR(512),
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fk_categories_parent
        FOREIGN KEY (parent_id) REFERENCES categories(id) ON DELETE RESTRICT,
    CONSTRAINT ck_categories_parent_not_self
        CHECK (parent_id IS NULL OR parent_id <> id),
    CONSTRAINT ck_categories_name_not_blank
        CHECK (char_length(btrim(name)) BETWEEN 1 AND 160),
    CONSTRAINT ck_categories_slug_canonical
        CHECK (slug = lower(btrim(slug)) AND slug <> ''),
    CONSTRAINT ck_categories_status
        CHECK (status IN ('active', 'inactive')),
    CONSTRAINT ck_categories_sort_order_non_negative
        CHECK (sort_order >= 0),
    CONSTRAINT ck_categories_updated_at_not_before_created_at
        CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX uq_categories_root_slug
    ON categories(slug)
    WHERE parent_id IS NULL;

CREATE UNIQUE INDEX uq_categories_parent_slug
    ON categories(parent_id, slug)
    WHERE parent_id IS NOT NULL;

CREATE INDEX idx_categories_parent
    ON categories(parent_id);

CREATE INDEX idx_categories_status
    ON categories(status);

CREATE TABLE brands (
    id UUID PRIMARY KEY,
    name VARCHAR(160) NOT NULL,
    slug VARCHAR(160) NOT NULL,
    description TEXT,
    logo_object_key VARCHAR(512),
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_brands_slug
        UNIQUE (slug),
    CONSTRAINT ck_brands_name_not_blank
        CHECK (char_length(btrim(name)) BETWEEN 1 AND 160),
    CONSTRAINT ck_brands_slug_canonical
        CHECK (slug = lower(btrim(slug)) AND slug <> ''),
    CONSTRAINT ck_brands_status
        CHECK (status IN ('active', 'inactive')),
    CONSTRAINT ck_brands_updated_at_not_before_created_at
        CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX uq_brands_name_ci
    ON brands(lower(btrim(name)));

CREATE TABLE products (
    id UUID PRIMARY KEY,
    shop_id UUID NOT NULL,
    category_id UUID NOT NULL,
    brand_id UUID,
    name VARCHAR(255) NOT NULL,
    slug VARCHAR(255) NOT NULL,
    description TEXT,
    main_image_object_key VARCHAR(512),
    weight_g INTEGER,
    package_length_mm INTEGER,
    package_width_mm INTEGER,
    package_height_mm INTEGER,
    status VARCHAR(30) NOT NULL DEFAULT 'draft',
    rejection_reason VARCHAR(512),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    archived_at TIMESTAMPTZ,

    CONSTRAINT fk_products_shop
        FOREIGN KEY (shop_id) REFERENCES shops(id) ON DELETE RESTRICT,
    CONSTRAINT fk_products_category
        FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE RESTRICT,
    CONSTRAINT fk_products_brand
        FOREIGN KEY (brand_id) REFERENCES brands(id) ON DELETE RESTRICT,
    CONSTRAINT uq_products_shop_slug
        UNIQUE (shop_id, slug),
    CONSTRAINT uq_products_id_shop_id
        UNIQUE (id, shop_id),
    CONSTRAINT ck_products_name_not_blank
        CHECK (char_length(btrim(name)) BETWEEN 1 AND 255),
    CONSTRAINT ck_products_slug_canonical
        CHECK (slug = lower(btrim(slug)) AND slug <> ''),
    CONSTRAINT ck_products_weight_positive
        CHECK (weight_g IS NULL OR weight_g > 0),
    CONSTRAINT ck_products_package_length_positive
        CHECK (package_length_mm IS NULL OR package_length_mm > 0),
    CONSTRAINT ck_products_package_width_positive
        CHECK (package_width_mm IS NULL OR package_width_mm > 0),
    CONSTRAINT ck_products_package_height_positive
        CHECK (package_height_mm IS NULL OR package_height_mm > 0),
    CONSTRAINT ck_products_status
        CHECK (status IN ('draft', 'pending_review', 'active', 'rejected', 'inactive', 'archived')),
    CONSTRAINT ck_products_rejection_reason_matches_status
        CHECK (
            (status = 'rejected' AND rejection_reason IS NOT NULL)
            OR
            (status <> 'rejected' AND rejection_reason IS NULL)
        ),
    CONSTRAINT ck_products_archived_at_matches_status
        CHECK (
            (status = 'archived' AND archived_at IS NOT NULL)
            OR
            (status <> 'archived' AND archived_at IS NULL)
        ),
    CONSTRAINT ck_products_published_status_has_timestamp
        CHECK (status NOT IN ('active', 'inactive') OR published_at IS NOT NULL),
    CONSTRAINT ck_products_updated_at_not_before_created_at
        CHECK (updated_at >= created_at)
);

CREATE INDEX idx_products_shop_status
    ON products(shop_id, status);

CREATE INDEX idx_products_category_status
    ON products(category_id, status);

CREATE INDEX idx_products_brand_status
    ON products(brand_id, status)
    WHERE brand_id IS NOT NULL;

CREATE INDEX idx_products_status_created
    ON products(status, created_at);

CREATE TABLE product_variants (
    id UUID PRIMARY KEY,
    product_id UUID NOT NULL,
    shop_id UUID NOT NULL,
    name VARCHAR(160) NOT NULL,
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    image_object_key VARCHAR(512),
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    position INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at TIMESTAMPTZ,

    CONSTRAINT fk_product_variants_product_shop
        FOREIGN KEY (product_id, shop_id) REFERENCES products(id, shop_id) ON DELETE RESTRICT,
    CONSTRAINT uq_product_variants_id_shop_id
        UNIQUE (id, shop_id),
    CONSTRAINT uq_product_variants_id_product_shop
        UNIQUE (id, product_id, shop_id),
    CONSTRAINT ck_product_variants_name_not_blank
        CHECK (char_length(btrim(name)) BETWEEN 1 AND 160),
    CONSTRAINT ck_product_variants_attributes_object
        CHECK (jsonb_typeof(attributes) = 'object'),
    CONSTRAINT ck_product_variants_status
        CHECK (status IN ('active', 'inactive', 'archived')),
    CONSTRAINT ck_product_variants_position_non_negative
        CHECK (position >= 0),
    CONSTRAINT ck_product_variants_archived_at_matches_status
        CHECK (
            (status = 'archived' AND archived_at IS NOT NULL)
            OR
            (status <> 'archived' AND archived_at IS NULL)
        ),
    CONSTRAINT ck_product_variants_updated_at_not_before_created_at
        CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX uq_product_variants_name_ci
    ON product_variants(product_id, lower(btrim(name)));

CREATE INDEX idx_product_variants_product_status
    ON product_variants(product_id, status);

CREATE INDEX idx_product_variants_shop
    ON product_variants(shop_id);

CREATE TABLE skus (
    id UUID PRIMARY KEY,
    variant_id UUID NOT NULL,
    shop_id UUID NOT NULL,
    sku_code VARCHAR(80) NOT NULL,
    barcode VARCHAR(100),
    name VARCHAR(160) NOT NULL,
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    image_object_key VARCHAR(512),
    price_amount_minor BIGINT NOT NULL,
    compare_at_price_amount_minor BIGINT,
    currency_code CHAR(3) NOT NULL,
    weight_g INTEGER,
    package_length_mm INTEGER,
    package_width_mm INTEGER,
    package_height_mm INTEGER,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at TIMESTAMPTZ,

    CONSTRAINT fk_skus_variant_shop
        FOREIGN KEY (variant_id, shop_id) REFERENCES product_variants(id, shop_id) ON DELETE RESTRICT,
    CONSTRAINT fk_skus_shop_currency
        FOREIGN KEY (shop_id, currency_code) REFERENCES shops(id, currency_code) ON DELETE RESTRICT,
    CONSTRAINT uq_skus_shop_sku_code
        UNIQUE (shop_id, sku_code),
    CONSTRAINT uq_skus_id_shop_id
        UNIQUE (id, shop_id),
    CONSTRAINT uq_skus_id_variant_shop
        UNIQUE (id, variant_id, shop_id),
    CONSTRAINT ck_skus_sku_code_not_blank
        CHECK (char_length(btrim(sku_code)) BETWEEN 1 AND 80),
    CONSTRAINT ck_skus_name_not_blank
        CHECK (char_length(btrim(name)) BETWEEN 1 AND 160),
    CONSTRAINT ck_skus_attributes_object
        CHECK (jsonb_typeof(attributes) = 'object'),
    CONSTRAINT ck_skus_price_non_negative
        CHECK (price_amount_minor >= 0),
    CONSTRAINT ck_skus_compare_at_price
        CHECK (
            compare_at_price_amount_minor IS NULL
            OR compare_at_price_amount_minor >= price_amount_minor
        ),
    CONSTRAINT ck_skus_currency_code
        CHECK (
            currency_code = upper(currency_code)
            AND btrim(currency_code) ~ '^[A-Z]{3}$'
        ),
    CONSTRAINT ck_skus_weight_positive
        CHECK (weight_g IS NULL OR weight_g > 0),
    CONSTRAINT ck_skus_package_length_positive
        CHECK (package_length_mm IS NULL OR package_length_mm > 0),
    CONSTRAINT ck_skus_package_width_positive
        CHECK (package_width_mm IS NULL OR package_width_mm > 0),
    CONSTRAINT ck_skus_package_height_positive
        CHECK (package_height_mm IS NULL OR package_height_mm > 0),
    CONSTRAINT ck_skus_status
        CHECK (status IN ('active', 'inactive', 'archived')),
    CONSTRAINT ck_skus_archived_at_matches_status
        CHECK (
            (status = 'archived' AND archived_at IS NOT NULL)
            OR
            (status <> 'archived' AND archived_at IS NULL)
        ),
    CONSTRAINT ck_skus_updated_at_not_before_created_at
        CHECK (updated_at >= created_at)
);

CREATE INDEX idx_skus_variant_status
    ON skus(variant_id, status);

CREATE INDEX idx_skus_shop_status
    ON skus(shop_id, status);

CREATE INDEX idx_skus_shop_price_active
    ON skus(shop_id, price_amount_minor)
    WHERE status = 'active';

COMMIT;
