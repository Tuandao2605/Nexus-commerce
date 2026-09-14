-- File UP này tạo schema Seller V1, gồm SellerAccount, Shop và membership làm nguồn quyền theo Shop.
BEGIN;

CREATE TABLE seller_accounts (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    status VARCHAR(30) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    suspended_at TIMESTAMPTZ,
    closed_at TIMESTAMPTZ,

    CONSTRAINT fk_seller_accounts_user
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT uq_seller_accounts_user_id
        UNIQUE (user_id),
    CONSTRAINT ck_seller_accounts_status
        CHECK (status IN ('pending', 'active', 'suspended', 'closed')),
    CONSTRAINT ck_seller_accounts_updated_at_not_before_created_at
        CHECK (updated_at >= created_at),
    CONSTRAINT ck_seller_accounts_suspended_at_matches_status
        CHECK (
            (status = 'suspended' AND suspended_at IS NOT NULL)
            OR
            (status <> 'suspended' AND suspended_at IS NULL)
        ),
    CONSTRAINT ck_seller_accounts_closed_at_matches_status
        CHECK (
            (status = 'closed' AND closed_at IS NOT NULL)
            OR
            (status <> 'closed' AND closed_at IS NULL)
        )
);

CREATE INDEX idx_seller_accounts_status
    ON seller_accounts(status);

CREATE TABLE shops (
    id UUID PRIMARY KEY,
    name VARCHAR(160) NOT NULL,
    slug VARCHAR(160) NOT NULL,
    description TEXT,
    logo_object_key VARCHAR(512),
    banner_object_key VARCHAR(512),
    phone VARCHAR(32),
    email VARCHAR(254),
    is_official BOOLEAN NOT NULL DEFAULT false,
    currency_code CHAR(3) NOT NULL DEFAULT 'VND',
    status VARCHAR(20) NOT NULL DEFAULT 'draft',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at TIMESTAMPTZ,

    CONSTRAINT uq_shops_slug
        UNIQUE (slug),
    CONSTRAINT uq_shops_id_currency_code
        UNIQUE (id, currency_code),
    CONSTRAINT ck_shops_name_not_blank
        CHECK (char_length(btrim(name)) BETWEEN 1 AND 160),
    CONSTRAINT ck_shops_slug_canonical
        CHECK (slug = lower(btrim(slug)) AND slug <> ''),
    CONSTRAINT ck_shops_phone_not_blank
        CHECK (phone IS NULL OR char_length(btrim(phone)) > 0),
    CONSTRAINT ck_shops_email_canonical
        CHECK (
            email IS NULL
            OR (
                email = lower(btrim(email))
                AND char_length(email) BETWEEN 3 AND 254
            )
        ),
    CONSTRAINT ck_shops_currency_code
        CHECK (
            currency_code = upper(currency_code)
            AND btrim(currency_code) ~ '^[A-Z]{3}$'
        ),
    CONSTRAINT ck_shops_status
        CHECK (status IN ('draft', 'active', 'suspended', 'closed')),
    CONSTRAINT ck_shops_updated_at_not_before_created_at
        CHECK (updated_at >= created_at),
    CONSTRAINT ck_shops_closed_at_matches_status
        CHECK (
            (status = 'closed' AND closed_at IS NOT NULL)
            OR
            (status <> 'closed' AND closed_at IS NULL)
        )
);

CREATE INDEX idx_shops_status
    ON shops(status);

CREATE TABLE shop_memberships (
    id UUID PRIMARY KEY,
    shop_id UUID NOT NULL,
    seller_account_id UUID NOT NULL,
    created_by_seller_account_id UUID,
    role VARCHAR(30) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    invited_at TIMESTAMPTZ,
    accepted_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fk_shop_memberships_shop
        FOREIGN KEY (shop_id) REFERENCES shops(id) ON DELETE RESTRICT,
    CONSTRAINT fk_shop_memberships_seller_account
        FOREIGN KEY (seller_account_id) REFERENCES seller_accounts(id) ON DELETE RESTRICT,
    CONSTRAINT fk_shop_memberships_created_by_seller_account
        FOREIGN KEY (created_by_seller_account_id) REFERENCES seller_accounts(id) ON DELETE RESTRICT,
    CONSTRAINT uq_shop_memberships_shop_seller_account
        UNIQUE (shop_id, seller_account_id),
    CONSTRAINT ck_shop_memberships_role
        CHECK (role IN ('owner', 'admin', 'catalog_manager', 'order_manager', 'viewer')),
    CONSTRAINT ck_shop_memberships_status
        CHECK (status IN ('pending', 'active', 'suspended', 'revoked')),
    CONSTRAINT ck_shop_memberships_invited_at_not_before_created_at
        CHECK (invited_at IS NULL OR invited_at >= created_at),
    CONSTRAINT ck_shop_memberships_accepted_at_chronology
        CHECK (accepted_at IS NULL OR accepted_at >= COALESCE(invited_at, created_at)),
    CONSTRAINT ck_shop_memberships_pending_has_no_acceptance
        CHECK (status <> 'pending' OR accepted_at IS NULL),
    CONSTRAINT ck_shop_memberships_accepted_status_has_acceptance
        CHECK (status NOT IN ('active', 'suspended', 'revoked') OR accepted_at IS NOT NULL),
    CONSTRAINT ck_shop_memberships_revoked_at_matches_status
        CHECK (
            (status = 'revoked' AND revoked_at IS NOT NULL)
            OR
            (status <> 'revoked' AND revoked_at IS NULL)
        ),
    CONSTRAINT ck_shop_memberships_revoked_at_chronology
        CHECK (revoked_at IS NULL OR revoked_at >= accepted_at),
    CONSTRAINT ck_shop_memberships_updated_at_not_before_created_at
        CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX uq_shop_memberships_active_owner
    ON shop_memberships(shop_id)
    WHERE role = 'owner' AND status = 'active';

CREATE INDEX idx_shop_memberships_seller_status
    ON shop_memberships(seller_account_id, status);

CREATE INDEX idx_shop_memberships_shop_status
    ON shop_memberships(shop_id, status);

CREATE INDEX idx_shop_memberships_shop_seller_active
    ON shop_memberships(shop_id, seller_account_id)
    WHERE status = 'active';

CREATE INDEX idx_shop_memberships_pending
    ON shop_memberships(seller_account_id)
    WHERE status = 'pending';

COMMIT;
