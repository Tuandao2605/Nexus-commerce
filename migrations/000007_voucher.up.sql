-- File UP này tạo schema Voucher V1 (vouchers và voucher_usages), khóa chặt tính toàn vẹn currency,
-- phân định scope, bảo vệ concurrency allocation counter và đảm bảo cross-module consistency khi commit sang Parent Order.
BEGIN;

CREATE TABLE vouchers (
    id UUID PRIMARY KEY,
    code VARCHAR(64) NOT NULL,
    scope VARCHAR(20) NOT NULL,
    shop_id UUID,
    discount_type VARCHAR(20) NOT NULL,
    discount_value BIGINT NOT NULL,
    max_discount_amount BIGINT,
    minimum_order_amount BIGINT NOT NULL DEFAULT 0,
    currency_code CHAR(3) NOT NULL,
    usage_limit BIGINT,
    usage_limit_per_user BIGINT,
    allocated_usage_count BIGINT NOT NULL DEFAULT 0,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ,
    status VARCHAR(20) NOT NULL DEFAULT 'draft',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Invariant 1: Namespace mã voucher toàn hệ thống (V1 global uniqueness, canonical uppercase)
    CONSTRAINT uq_vouchers_code
        UNIQUE (code),

    -- Invariant 2: Supporting unique key phục vụ composite FK từ voucher_usages để khóa currency
    CONSTRAINT uq_vouchers_id_currency
        UNIQUE (id, currency_code),

    -- Invariant 3: Shop Voucher bắt buộc phải khớp currency với Shop đang sở hữu
    CONSTRAINT fk_vouchers_shop_currency
        FOREIGN KEY (shop_id, currency_code)
        REFERENCES shops(id, currency_code)
        ON DELETE RESTRICT,

    -- Invariant 4: Phân định scope chặt chẽ giữa Platform và Shop
    CONSTRAINT ck_vouchers_scope
        CHECK (scope IN ('platform', 'shop')),
    CONSTRAINT ck_vouchers_scope_shop
        CHECK (
            (scope = 'platform' AND shop_id IS NULL)
            OR
            (scope = 'shop' AND shop_id IS NOT NULL)
        ),

    -- Invariant 5: Canonical code format (viết hoa, trim khoảng trắng, độ dài 1-64 ký tự)
    CONSTRAINT ck_vouchers_code_canonical
        CHECK (
            code = upper(btrim(code))
            AND char_length(code) BETWEEN 1 AND 64
        ),

    -- Invariant 6: Loại discount và giá trị discount
    -- fixed_amount: giá trị > 0 tính theo minor units
    -- percentage: giá trị từ 1 đến 10000 basis points (100% = 10000 bp)
    CONSTRAINT ck_vouchers_discount_type
        CHECK (discount_type IN ('fixed_amount', 'percentage')),
    CONSTRAINT ck_vouchers_discount_type_value
        CHECK (
            (discount_type = 'fixed_amount' AND discount_value > 0)
            OR
            (discount_type = 'percentage' AND discount_value BETWEEN 1 AND 10000)
        ),

    -- Invariant 7: Giới hạn trần giảm giá cho percentage (fixed_amount không có trần)
    CONSTRAINT ck_vouchers_discount_cap
        CHECK (
            (discount_type = 'fixed_amount' AND max_discount_amount IS NULL)
            OR
            (discount_type = 'percentage' AND (max_discount_amount IS NULL OR max_discount_amount >= 0))
        ),

    -- Invariant 8: Ngưỡng đơn hàng tối thiểu không âm
    CONSTRAINT ck_vouchers_minimum_order_amount
        CHECK (minimum_order_amount >= 0),

    -- Invariant 9: Giới hạn lượt sử dụng nếu được cấu hình thì phải là số dương
    CONSTRAINT ck_vouchers_usage_limit_positive
        CHECK (usage_limit IS NULL OR usage_limit > 0),
    CONSTRAINT ck_vouchers_usage_limit_per_user_positive
        CHECK (usage_limit_per_user IS NULL OR usage_limit_per_user > 0),

    -- Invariant 10: Concurrency quota ceiling: số lượng slot đã cấp phát (reserved + committed) không vượt quá trần
    CONSTRAINT ck_vouchers_allocated_usage
        CHECK (
            allocated_usage_count >= 0
            AND (usage_limit IS NULL OR allocated_usage_count <= usage_limit)
        ),

    -- Invariant 11: Định dạng chuẩn 3 ký tự viết hoa cho currency code
    CONSTRAINT ck_vouchers_currency_code
        CHECK (
            currency_code = upper(currency_code)
            AND btrim(currency_code) ~ '^[A-Z]{3}$'
        ),

    -- Invariant 12: Khoảng thời gian hiệu lực hợp lệ
    CONSTRAINT ck_vouchers_validity_period
        CHECK (ends_at IS NULL OR ends_at > starts_at),

    -- Invariant 13: Trạng thái lifecycle hợp lệ
    CONSTRAINT ck_vouchers_status
        CHECK (status IN ('draft', 'active', 'inactive')),

    -- Invariant 14: Tính thời gian chronology của audit timestamps
    CONSTRAINT ck_vouchers_updated_at_not_before_created_at
        CHECK (updated_at >= created_at)
);

CREATE INDEX idx_vouchers_shop_status
    ON vouchers(shop_id, status)
    WHERE scope = 'shop';

CREATE INDEX idx_vouchers_status_time
    ON vouchers(status, starts_at, ends_at);

CREATE TABLE voucher_usages (
    id UUID PRIMARY KEY,
    voucher_id UUID NOT NULL,
    user_id UUID NOT NULL,
    checkout_reference_id UUID NOT NULL,
    order_id UUID,
    parent_order_type VARCHAR(20),
    discount_amount BIGINT NOT NULL,
    currency_code CHAR(3) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'reserved',
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    committed_at TIMESTAMPTZ,
    released_at TIMESTAMPTZ,
    expired_at TIMESTAMPTZ,

    -- Invariant 15: Checkout idempotency safety net: 1 checkout chỉ có tối đa 1 logical reservation attempt
    CONSTRAINT uq_voucher_usages_voucher_checkout
        UNIQUE (voucher_id, checkout_reference_id),

    -- Invariant 16: Khóa currency giữa voucher usage và voucher definition
    CONSTRAINT fk_voucher_usages_voucher_currency
        FOREIGN KEY (voucher_id, currency_code)
        REFERENCES vouchers(id, currency_code)
        ON DELETE RESTRICT,

    -- Invariant 17: Ràng buộc người dùng sở hữu usage
    CONSTRAINT fk_voucher_usages_user
        FOREIGN KEY (user_id)
        REFERENCES users(id)
        ON DELETE RESTRICT,

    -- Invariant 18: Cross-module composite FK bảo đảm khi committed, usage phải gắn đúng vào Parent Order
    -- của cùng User, Checkout Reference và Currency (RESOLUTION-01 & RESOLUTION-02 trong DATA-003G)
    CONSTRAINT fk_voucher_usages_order_parent
        FOREIGN KEY (
            order_id,
            user_id,
            checkout_reference_id,
            currency_code,
            parent_order_type
        )
        REFERENCES orders(
            id,
            user_id,
            checkout_reference_id,
            currency,
            order_type
        )
        ON DELETE RESTRICT,

    -- Invariant 19: Trạng thái vòng đời usage hợp lệ
    CONSTRAINT ck_voucher_usages_status
        CHECK (status IN ('reserved', 'committed', 'released', 'expired')),

    -- Invariant 20: Số tiền chiết khấu snapshot không âm
    CONSTRAINT ck_voucher_usages_discount_amount
        CHECK (discount_amount >= 0),

    -- Invariant 21: Định dạng tiền tệ snapshot
    CONSTRAINT ck_voucher_usages_currency_code
        CHECK (
            currency_code = upper(currency_code)
            AND btrim(currency_code) ~ '^[A-Z]{3}$'
        ),

    -- Invariant 22: Thời hạn giữ chỗ hợp lệ tại thời điểm tạo
    CONSTRAINT ck_voucher_usages_expires_at
        CHECK (expires_at > created_at),

    -- Invariant 23: Kiểm soát trạng thái - mốc thời gian và tính gắn kết với Parent Order
    -- Chỉ 'committed' mới có order_id và parent_order_type = 'parent'. Các trạng thái khác bắt buộc NULL
    CONSTRAINT ck_voucher_usages_state_timestamps
        CHECK (
            (
                status = 'reserved'
                AND order_id IS NULL
                AND parent_order_type IS NULL
                AND committed_at IS NULL
                AND released_at IS NULL
                AND expired_at IS NULL
            )
            OR
            (
                status = 'committed'
                AND order_id IS NOT NULL
                AND parent_order_type = 'parent'
                AND committed_at IS NOT NULL
                AND released_at IS NULL
                AND expired_at IS NULL
            )
            OR
            (
                status = 'released'
                AND order_id IS NULL
                AND parent_order_type IS NULL
                AND committed_at IS NULL
                AND released_at IS NOT NULL
                AND expired_at IS NULL
            )
            OR
            (
                status = 'expired'
                AND order_id IS NULL
                AND parent_order_type IS NULL
                AND committed_at IS NULL
                AND released_at IS NULL
                AND expired_at IS NOT NULL
            )
        ),

    -- Invariant 24: Tính tuần tự mốc thời gian (chronology)
    CONSTRAINT ck_voucher_usages_updated_at_not_before_created_at
        CHECK (updated_at >= created_at),
    CONSTRAINT ck_voucher_usages_committed_at_chronology
        CHECK (committed_at IS NULL OR committed_at >= created_at),
    CONSTRAINT ck_voucher_usages_released_at_chronology
        CHECK (released_at IS NULL OR released_at >= created_at),
    CONSTRAINT ck_voucher_usages_expired_at_chronology
        CHECK (expired_at IS NULL OR expired_at >= expires_at)
);

CREATE INDEX idx_voucher_usages_voucher_status
    ON voucher_usages(voucher_id, status);

CREATE INDEX idx_voucher_usages_user_voucher
    ON voucher_usages(user_id, voucher_id);

CREATE INDEX idx_voucher_usages_order
    ON voucher_usages(order_id)
    WHERE order_id IS NOT NULL;

CREATE INDEX idx_voucher_usages_checkout
    ON voucher_usages(checkout_reference_id);

-- Index phục vụ background TTL worker quét các holds đã hết hạn để giải phóng quota
CREATE INDEX idx_voucher_usages_reserved_expires
    ON voucher_usages(expires_at)
    WHERE status = 'reserved';

COMMIT;

