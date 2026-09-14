-- File UP này tạo schema Cart V1 cho global multi-Shop purchase intent và bảo vệ CartItem theo đúng SKU/Shop.
BEGIN;

CREATE TABLE carts (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    checked_out_at TIMESTAMPTZ,
    abandoned_at TIMESTAMPTZ,

    CONSTRAINT fk_carts_user
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT ck_carts_status
        CHECK (status IN ('active', 'checked_out', 'abandoned')),
    CONSTRAINT ck_carts_checked_out_at_matches_status
        CHECK (
            (status = 'checked_out' AND checked_out_at IS NOT NULL AND abandoned_at IS NULL)
            OR
            (status <> 'checked_out' AND checked_out_at IS NULL)
        ),
    CONSTRAINT ck_carts_abandoned_at_matches_status
        CHECK (
            (status = 'abandoned' AND abandoned_at IS NOT NULL AND checked_out_at IS NULL)
            OR
            (status <> 'abandoned' AND abandoned_at IS NULL)
        ),
    CONSTRAINT ck_carts_updated_at_not_before_created_at
        CHECK (updated_at >= created_at),
    CONSTRAINT ck_carts_checked_out_at_not_before_created_at
        CHECK (checked_out_at IS NULL OR checked_out_at >= created_at),
    CONSTRAINT ck_carts_abandoned_at_not_before_created_at
        CHECK (abandoned_at IS NULL OR abandoned_at >= created_at)
);

CREATE UNIQUE INDEX uq_carts_user_active
    ON carts(user_id)
    WHERE status = 'active';

CREATE INDEX idx_carts_user_created
    ON carts(user_id, created_at DESC);

CREATE TABLE cart_items (
    id UUID PRIMARY KEY,
    cart_id UUID NOT NULL,
    shop_id UUID NOT NULL,
    sku_id UUID NOT NULL,
    quantity BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fk_cart_items_cart
        FOREIGN KEY (cart_id) REFERENCES carts(id) ON DELETE RESTRICT,
    CONSTRAINT fk_cart_items_sku_shop
        FOREIGN KEY (sku_id, shop_id) REFERENCES skus(id, shop_id) ON DELETE RESTRICT,
    CONSTRAINT uq_cart_items_cart_sku
        UNIQUE (cart_id, sku_id),
    CONSTRAINT ck_cart_items_quantity
        CHECK (quantity BETWEEN 1 AND 99),
    CONSTRAINT ck_cart_items_updated_at_not_before_created_at
        CHECK (updated_at >= created_at)
);

CREATE INDEX idx_cart_items_cart_shop
    ON cart_items(cart_id, shop_id);

CREATE INDEX idx_cart_items_sku
    ON cart_items(sku_id);

COMMIT;
