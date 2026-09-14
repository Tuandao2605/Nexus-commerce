-- File UP này tạo schema Inventory V1 cho warehouse, current stock, temporary reservation và physical movement audit.
BEGIN;

CREATE TABLE warehouses (
    id UUID PRIMARY KEY,
    shop_id UUID NOT NULL,
    name VARCHAR(160) NOT NULL,
    code VARCHAR(64) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at TIMESTAMPTZ,

    CONSTRAINT fk_warehouses_shop
        FOREIGN KEY (shop_id) REFERENCES shops(id) ON DELETE RESTRICT,
    CONSTRAINT uq_warehouses_shop_code
        UNIQUE (shop_id, code),
    CONSTRAINT uq_warehouses_id_shop_id
        UNIQUE (id, shop_id),
    CONSTRAINT ck_warehouses_name_not_blank
        CHECK (char_length(btrim(name)) BETWEEN 1 AND 160),
    CONSTRAINT ck_warehouses_code_not_blank
        CHECK (char_length(btrim(code)) BETWEEN 1 AND 64),
    CONSTRAINT ck_warehouses_status
        CHECK (status IN ('active', 'inactive', 'archived')),
    CONSTRAINT ck_warehouses_archived_at_matches_status
        CHECK (
            (status = 'archived' AND archived_at IS NOT NULL)
            OR
            (status <> 'archived' AND archived_at IS NULL)
        ),
    CONSTRAINT ck_warehouses_updated_at_not_before_created_at
        CHECK (updated_at >= created_at)
);

CREATE INDEX idx_warehouses_shop_status
    ON warehouses(shop_id, status);

CREATE TABLE inventory_stocks (
    id UUID PRIMARY KEY,
    shop_id UUID NOT NULL,
    warehouse_id UUID NOT NULL,
    sku_id UUID NOT NULL,
    on_hand_quantity BIGINT NOT NULL DEFAULT 0,
    reserved_quantity BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fk_inventory_stocks_warehouse_shop
        FOREIGN KEY (warehouse_id, shop_id) REFERENCES warehouses(id, shop_id) ON DELETE RESTRICT,
    CONSTRAINT fk_inventory_stocks_sku_shop
        FOREIGN KEY (sku_id, shop_id) REFERENCES skus(id, shop_id) ON DELETE RESTRICT,
    CONSTRAINT uq_inventory_stocks_warehouse_sku
        UNIQUE (warehouse_id, sku_id),
    CONSTRAINT ck_inventory_stocks_on_hand_non_negative
        CHECK (on_hand_quantity >= 0),
    CONSTRAINT ck_inventory_stocks_reserved_non_negative
        CHECK (reserved_quantity >= 0),
    CONSTRAINT ck_inventory_stocks_reserved_not_above_on_hand
        CHECK (reserved_quantity <= on_hand_quantity),
    CONSTRAINT ck_inventory_stocks_updated_at_not_before_created_at
        CHECK (updated_at >= created_at)
);

CREATE INDEX idx_inventory_stocks_shop_sku
    ON inventory_stocks(shop_id, sku_id);

CREATE TABLE inventory_reservations (
    id UUID PRIMARY KEY,
    reference_type VARCHAR(40) NOT NULL,
    reference_id UUID NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    committed_at TIMESTAMPTZ,
    released_at TIMESTAMPTZ,
    expired_at TIMESTAMPTZ,

    CONSTRAINT uq_inventory_reservations_idempotency_key
        UNIQUE (idempotency_key),
    CONSTRAINT ck_inventory_reservations_reference_type
        CHECK (reference_type = 'checkout'),
    CONSTRAINT ck_inventory_reservations_idempotency_key_not_blank
        CHECK (char_length(btrim(idempotency_key)) BETWEEN 1 AND 128),
    CONSTRAINT ck_inventory_reservations_request_hash_length
        CHECK (char_length(request_hash) = 64),
    CONSTRAINT ck_inventory_reservations_status
        CHECK (status IN ('active', 'committed', 'released', 'expired')),
    CONSTRAINT ck_inventory_reservations_expires_after_created_at
        CHECK (expires_at > created_at),
    CONSTRAINT ck_inventory_reservations_committed_at_matches_status
        CHECK (
            (status = 'committed' AND committed_at IS NOT NULL)
            OR
            (status <> 'committed' AND committed_at IS NULL)
        ),
    CONSTRAINT ck_inventory_reservations_released_at_matches_status
        CHECK (
            (status = 'released' AND released_at IS NOT NULL)
            OR
            (status <> 'released' AND released_at IS NULL)
        ),
    CONSTRAINT ck_inventory_reservations_expired_at_matches_status
        CHECK (
            (status = 'expired' AND expired_at IS NOT NULL)
            OR
            (status <> 'expired' AND expired_at IS NULL)
        ),
    CONSTRAINT ck_inventory_reservations_committed_at_chronology
        CHECK (committed_at IS NULL OR committed_at >= created_at),
    CONSTRAINT ck_inventory_reservations_released_at_chronology
        CHECK (released_at IS NULL OR released_at >= created_at),
    CONSTRAINT ck_inventory_reservations_expired_at_chronology
        CHECK (expired_at IS NULL OR expired_at >= expires_at),
    CONSTRAINT ck_inventory_reservations_updated_at_not_before_created_at
        CHECK (updated_at >= created_at)
);

CREATE INDEX idx_inventory_reservations_active_expires
    ON inventory_reservations(expires_at)
    WHERE status = 'active';

CREATE INDEX idx_inventory_reservations_reference
    ON inventory_reservations(reference_type, reference_id);

CREATE TABLE inventory_reservation_items (
    id UUID PRIMARY KEY,
    reservation_id UUID NOT NULL,
    inventory_stock_id UUID NOT NULL,
    quantity BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fk_inventory_reservation_items_reservation
        FOREIGN KEY (reservation_id) REFERENCES inventory_reservations(id) ON DELETE RESTRICT,
    CONSTRAINT fk_inventory_reservation_items_stock
        FOREIGN KEY (inventory_stock_id) REFERENCES inventory_stocks(id) ON DELETE RESTRICT,
    CONSTRAINT uq_inventory_reservation_items_reservation_stock
        UNIQUE (reservation_id, inventory_stock_id),
    CONSTRAINT ck_inventory_reservation_items_quantity_positive
        CHECK (quantity > 0)
);

CREATE INDEX idx_inventory_reservation_items_stock
    ON inventory_reservation_items(inventory_stock_id);

CREATE TABLE stock_movements (
    id UUID PRIMARY KEY,
    inventory_stock_id UUID NOT NULL,
    movement_type VARCHAR(30) NOT NULL,
    quantity_delta BIGINT NOT NULL,
    reference_type VARCHAR(40),
    reference_id UUID,
    reason VARCHAR(512),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fk_stock_movements_stock
        FOREIGN KEY (inventory_stock_id) REFERENCES inventory_stocks(id) ON DELETE RESTRICT,
    CONSTRAINT ck_stock_movements_type
        CHECK (movement_type IN ('receipt', 'sale', 'adjustment_in', 'adjustment_out', 'return')),
    CONSTRAINT ck_stock_movements_quantity_delta_non_zero
        CHECK (quantity_delta <> 0),
    CONSTRAINT ck_stock_movements_delta_direction
        CHECK (
            (movement_type IN ('receipt', 'adjustment_in', 'return') AND quantity_delta > 0)
            OR
            (movement_type IN ('sale', 'adjustment_out') AND quantity_delta < 0)
        )
);

CREATE INDEX idx_stock_movements_stock_created
    ON stock_movements(inventory_stock_id, created_at DESC);

CREATE INDEX idx_stock_movements_reference
    ON stock_movements(reference_type, reference_id)
    WHERE reference_id IS NOT NULL;

COMMIT;
