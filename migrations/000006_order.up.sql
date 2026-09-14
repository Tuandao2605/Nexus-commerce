-- File UP này tạo schema Order V1 với Parent/Seller hierarchy, commercial snapshots và status transition history.
BEGIN;

CREATE TABLE orders (
    id UUID PRIMARY KEY,
    order_number VARCHAR(64) NOT NULL,
    order_type VARCHAR(20) NOT NULL,
    parent_order_id UUID,
    parent_order_type VARCHAR(20),
    checkout_reference_id UUID,
    user_id UUID NOT NULL,
    shop_id UUID,
    status VARCHAR(32) NOT NULL DEFAULT 'awaiting_payment',
    currency CHAR(3) NOT NULL,
    subtotal_amount BIGINT NOT NULL DEFAULT 0,
    discount_amount BIGINT NOT NULL DEFAULT 0,
    tax_amount BIGINT NOT NULL DEFAULT 0,
    shipping_amount BIGINT NOT NULL DEFAULT 0,
    total_amount BIGINT NOT NULL DEFAULT 0,
    customer_name_snapshot VARCHAR(160) NOT NULL,
    customer_email_snapshot VARCHAR(320),
    customer_phone_snapshot VARCHAR(40),
    shipping_recipient_name VARCHAR(160),
    shipping_phone VARCHAR(40),
    shipping_address_line1 VARCHAR(255),
    shipping_address_line2 VARCHAR(255),
    shipping_ward VARCHAR(120),
    shipping_district VARCHAR(120),
    shipping_city VARCHAR(120),
    shipping_country_code CHAR(2),
    shop_name_snapshot VARCHAR(160),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    confirmed_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,

    CONSTRAINT uq_orders_order_number
        UNIQUE (order_number),
    CONSTRAINT uq_orders_id_shop_id
        UNIQUE (id, shop_id),
    CONSTRAINT uq_orders_id_order_type
        UNIQUE (id, order_type),
    CONSTRAINT uq_orders_id_user_currency_type
        UNIQUE (id, user_id, currency, order_type),
    CONSTRAINT uq_orders_id_user_checkout_currency_type
        UNIQUE (id, user_id, checkout_reference_id, currency, order_type),
    CONSTRAINT uq_orders_id_currency_type
        UNIQUE (id, currency, order_type),
    CONSTRAINT fk_orders_user
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT fk_orders_shop_currency
        FOREIGN KEY (shop_id, currency) REFERENCES shops(id, currency_code) ON DELETE RESTRICT,
    CONSTRAINT fk_orders_parent_identity
        FOREIGN KEY (parent_order_id, user_id, currency, parent_order_type)
        REFERENCES orders(id, user_id, currency, order_type) ON DELETE RESTRICT,
    CONSTRAINT ck_orders_order_type
        CHECK (order_type IN ('parent', 'seller')),
    CONSTRAINT ck_orders_structure
        CHECK (
            (
                order_type = 'parent'
                AND parent_order_id IS NULL
                AND parent_order_type IS NULL
                AND shop_id IS NULL
                AND shop_name_snapshot IS NULL
                AND checkout_reference_id IS NOT NULL
            )
            OR
            (
                order_type = 'seller'
                AND parent_order_id IS NOT NULL
                AND parent_order_type = 'parent'
                AND shop_id IS NOT NULL
                AND shop_name_snapshot IS NOT NULL
                AND checkout_reference_id IS NULL
            )
        ),
    CONSTRAINT ck_orders_status_for_type
        CHECK (
            (
                order_type = 'parent'
                AND status IN (
                    'awaiting_payment', 'confirmed', 'partially_completed',
                    'completed', 'partially_cancelled', 'cancelled'
                )
            )
            OR
            (
                order_type = 'seller'
                AND status IN (
                    'awaiting_payment', 'confirmed', 'processing',
                    'shipped', 'delivered', 'cancelled'
                )
            )
        ),
    CONSTRAINT ck_orders_currency
        CHECK (currency = upper(currency) AND btrim(currency) ~ '^[A-Z]{3}$'),
    CONSTRAINT ck_orders_subtotal_non_negative
        CHECK (subtotal_amount >= 0),
    CONSTRAINT ck_orders_discount_non_negative
        CHECK (discount_amount >= 0),
    CONSTRAINT ck_orders_tax_non_negative
        CHECK (tax_amount >= 0),
    CONSTRAINT ck_orders_shipping_non_negative
        CHECK (shipping_amount >= 0),
    CONSTRAINT ck_orders_total_non_negative
        CHECK (total_amount >= 0),
    CONSTRAINT ck_orders_total_formula
        CHECK (total_amount = subtotal_amount - discount_amount + tax_amount + shipping_amount),
    CONSTRAINT ck_orders_customer_name_not_blank
        CHECK (char_length(btrim(customer_name_snapshot)) BETWEEN 1 AND 160),
    CONSTRAINT ck_orders_shop_name_not_blank
        CHECK (shop_name_snapshot IS NULL OR char_length(btrim(shop_name_snapshot)) BETWEEN 1 AND 160),
    CONSTRAINT ck_orders_shipping_country_code
        CHECK (
            shipping_country_code IS NULL
            OR (
                shipping_country_code = upper(shipping_country_code)
                AND btrim(shipping_country_code) ~ '^[A-Z]{2}$'
            )
        ),
    CONSTRAINT ck_orders_updated_at_not_before_created_at
        CHECK (updated_at >= created_at),
    CONSTRAINT ck_orders_confirmed_at_not_before_created_at
        CHECK (confirmed_at IS NULL OR confirmed_at >= created_at),
    CONSTRAINT ck_orders_completed_at_not_before_created_at
        CHECK (completed_at IS NULL OR completed_at >= created_at),
    CONSTRAINT ck_orders_cancelled_at_not_before_created_at
        CHECK (cancelled_at IS NULL OR cancelled_at >= created_at),
    CONSTRAINT ck_orders_status_timestamps
        CHECK (
            (
                status = 'awaiting_payment'
                AND confirmed_at IS NULL
                AND completed_at IS NULL
                AND cancelled_at IS NULL
            )
            OR
            (
                status IN ('confirmed', 'processing', 'shipped', 'partially_completed', 'partially_cancelled')
                AND confirmed_at IS NOT NULL
                AND completed_at IS NULL
                AND cancelled_at IS NULL
            )
            OR
            (
                status IN ('completed', 'delivered')
                AND confirmed_at IS NOT NULL
                AND completed_at IS NOT NULL
                AND cancelled_at IS NULL
            )
            OR
            (
                status = 'cancelled'
                AND completed_at IS NULL
                AND cancelled_at IS NOT NULL
            )
        )
);

CREATE UNIQUE INDEX uq_parent_order_checkout_reference
    ON orders(checkout_reference_id)
    WHERE order_type = 'parent';

CREATE UNIQUE INDEX uq_orders_parent_shop
    ON orders(parent_order_id, shop_id)
    WHERE order_type = 'seller';

CREATE INDEX idx_orders_parent
    ON orders(parent_order_id);

CREATE INDEX idx_orders_user_created
    ON orders(user_id, created_at DESC);

CREATE INDEX idx_orders_shop_status_created
    ON orders(shop_id, status, created_at DESC)
    WHERE order_type = 'seller';

CREATE TABLE order_items (
    id UUID PRIMARY KEY,
    order_id UUID NOT NULL,
    shop_id UUID NOT NULL,
    product_id UUID NOT NULL,
    variant_id UUID NOT NULL,
    sku_id UUID NOT NULL,
    sku_code_snapshot VARCHAR(100) NOT NULL,
    product_name_snapshot VARCHAR(255) NOT NULL,
    variant_name_snapshot VARCHAR(255),
    sku_name_snapshot VARCHAR(255),
    attributes_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    quantity BIGINT NOT NULL,
    unit_price_amount BIGINT NOT NULL,
    discount_amount BIGINT NOT NULL DEFAULT 0,
    tax_amount BIGINT NOT NULL DEFAULT 0,
    line_subtotal_amount BIGINT NOT NULL,
    line_total_amount BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fk_order_items_order_shop
        FOREIGN KEY (order_id, shop_id) REFERENCES orders(id, shop_id) ON DELETE RESTRICT,
    CONSTRAINT fk_order_items_variant_product_shop
        FOREIGN KEY (variant_id, product_id, shop_id)
        REFERENCES product_variants(id, product_id, shop_id) ON DELETE RESTRICT,
    CONSTRAINT fk_order_items_sku_variant_shop
        FOREIGN KEY (sku_id, variant_id, shop_id)
        REFERENCES skus(id, variant_id, shop_id) ON DELETE RESTRICT,
    CONSTRAINT uq_order_items_order_sku
        UNIQUE (order_id, sku_id),
    CONSTRAINT ck_order_items_sku_code_not_blank
        CHECK (char_length(btrim(sku_code_snapshot)) BETWEEN 1 AND 100),
    CONSTRAINT ck_order_items_product_name_not_blank
        CHECK (char_length(btrim(product_name_snapshot)) BETWEEN 1 AND 255),
    CONSTRAINT ck_order_items_attributes_object
        CHECK (jsonb_typeof(attributes_snapshot) = 'object'),
    CONSTRAINT ck_order_items_quantity
        CHECK (quantity BETWEEN 1 AND 99),
    CONSTRAINT ck_order_items_unit_price_non_negative
        CHECK (unit_price_amount >= 0),
    CONSTRAINT ck_order_items_discount_non_negative
        CHECK (discount_amount >= 0),
    CONSTRAINT ck_order_items_tax_non_negative
        CHECK (tax_amount >= 0),
    CONSTRAINT ck_order_items_line_subtotal_non_negative
        CHECK (line_subtotal_amount >= 0),
    CONSTRAINT ck_order_items_line_total_non_negative
        CHECK (line_total_amount >= 0),
    CONSTRAINT ck_order_items_line_subtotal_formula
        CHECK (line_subtotal_amount = unit_price_amount * quantity),
    CONSTRAINT ck_order_items_line_total_formula
        CHECK (line_total_amount = line_subtotal_amount - discount_amount + tax_amount)
);

CREATE INDEX idx_order_items_order
    ON order_items(order_id);

CREATE INDEX idx_order_items_sku
    ON order_items(sku_id);

CREATE TABLE order_status_histories (
    id UUID PRIMARY KEY,
    order_id UUID NOT NULL,
    from_status VARCHAR(32),
    to_status VARCHAR(32) NOT NULL,
    actor_type VARCHAR(30) NOT NULL,
    actor_id UUID,
    reason_code VARCHAR(64),
    reason VARCHAR(512),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fk_order_status_histories_order
        FOREIGN KEY (order_id) REFERENCES orders(id) ON DELETE RESTRICT,
    CONSTRAINT ck_order_status_histories_from_status
        CHECK (
            from_status IS NULL
            OR from_status IN (
                'awaiting_payment', 'confirmed', 'partially_completed', 'completed',
                'partially_cancelled', 'processing', 'shipped', 'delivered', 'cancelled'
            )
        ),
    CONSTRAINT ck_order_status_histories_to_status
        CHECK (
            to_status IN (
                'awaiting_payment', 'confirmed', 'partially_completed', 'completed',
                'partially_cancelled', 'processing', 'shipped', 'delivered', 'cancelled'
            )
        ),
    CONSTRAINT ck_order_status_histories_actor_type
        CHECK (actor_type IN ('customer', 'seller', 'system', 'admin', 'payment', 'shipping'))
);

CREATE INDEX idx_order_status_histories_order_created
    ON order_status_histories(order_id, created_at ASC);

COMMIT;
