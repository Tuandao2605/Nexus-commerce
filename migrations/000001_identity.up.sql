-- File UP này tạo schema Identity V1 cho User và Auth, gồm các constraint/index bảo vệ invariant đã duyệt ở DATA-003A.
BEGIN;

CREATE TABLE users (
    id UUID PRIMARY KEY,
    display_name VARCHAR(120) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,

    CONSTRAINT ck_users_display_name_not_blank
        CHECK (char_length(btrim(display_name)) BETWEEN 1 AND 120),
    CONSTRAINT ck_users_status
        CHECK (status IN ('active', 'suspended', 'deleted')),
    CONSTRAINT ck_users_deleted_at_matches_status
        CHECK (
            (status = 'deleted' AND deleted_at IS NOT NULL)
            OR
            (status <> 'deleted' AND deleted_at IS NULL)
        ),
    CONSTRAINT ck_users_updated_at_not_before_created_at
        CHECK (updated_at >= created_at)
);

CREATE INDEX idx_users_status
    ON users(status);

CREATE TABLE credentials (
    user_id UUID PRIMARY KEY,
    email VARCHAR(254) NOT NULL,
    password_hash TEXT NOT NULL,
    email_verified_at TIMESTAMPTZ,
    password_changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fk_credentials_user
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT uq_credentials_email
        UNIQUE (email),
    CONSTRAINT ck_credentials_email_canonical
        CHECK (
            email = lower(btrim(email))
            AND char_length(email) BETWEEN 3 AND 254
        ),
    CONSTRAINT ck_credentials_password_hash_not_empty
        CHECK (char_length(password_hash) > 0),
    CONSTRAINT ck_credentials_updated_at_not_before_created_at
        CHECK (updated_at >= created_at),
    CONSTRAINT ck_credentials_email_verified_at_not_before_created_at
        CHECK (email_verified_at IS NULL OR email_verified_at >= created_at),
    CONSTRAINT ck_credentials_password_changed_at_not_before_created_at
        CHECK (password_changed_at >= created_at)
);

CREATE TABLE sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    ip_address INET,
    user_agent VARCHAR(1024),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_activity_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,

    CONSTRAINT fk_sessions_user
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT ck_sessions_expires_at_after_created_at
        CHECK (expires_at > created_at),
    CONSTRAINT ck_sessions_last_activity_at_not_before_created_at
        CHECK (last_activity_at >= created_at),
    CONSTRAINT ck_sessions_revoked_at_not_before_created_at
        CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX idx_sessions_user_revoked_expires
    ON sessions(user_id, revoked_at, expires_at);

CREATE INDEX idx_sessions_expires_at
    ON sessions(expires_at);

CREATE TABLE user_addresses (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    label VARCHAR(50),
    recipient_name VARCHAR(120) NOT NULL,
    recipient_phone VARCHAR(32) NOT NULL,
    address_line1 VARCHAR(255) NOT NULL,
    address_line2 VARCHAR(255),
    ward VARCHAR(120),
    district VARCHAR(120),
    province VARCHAR(120) NOT NULL,
    postal_code VARCHAR(20),
    country_code CHAR(2) NOT NULL DEFAULT 'VN',
    is_default BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fk_user_addresses_user
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT ck_user_addresses_recipient_name_not_blank
        CHECK (char_length(btrim(recipient_name)) > 0),
    CONSTRAINT ck_user_addresses_recipient_phone_not_blank
        CHECK (char_length(btrim(recipient_phone)) > 0),
    CONSTRAINT ck_user_addresses_address_line1_not_blank
        CHECK (char_length(btrim(address_line1)) > 0),
    CONSTRAINT ck_user_addresses_province_not_blank
        CHECK (char_length(btrim(province)) > 0),
    CONSTRAINT ck_user_addresses_country_code
        CHECK (btrim(country_code) ~ '^[A-Z]{2}$'),
    CONSTRAINT ck_user_addresses_updated_at_not_before_created_at
        CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX uq_user_addresses_one_default
    ON user_addresses(user_id)
    WHERE is_default = true;

CREATE INDEX idx_user_addresses_user_id
    ON user_addresses(user_id);

COMMIT;
