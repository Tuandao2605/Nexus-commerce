-- File UP này thêm bảng refresh_tokens lưu digest, vòng đời và quan hệ predecessor trong một session Auth.
BEGIN;

CREATE TABLE refresh_tokens (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL,
    token_hash BYTEA NOT NULL,
    previous_token_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,

    CONSTRAINT fk_refresh_tokens_session
        FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
    CONSTRAINT uq_refresh_tokens_id_session
        UNIQUE (id, session_id),
    CONSTRAINT fk_refresh_tokens_previous_same_session
        FOREIGN KEY (previous_token_id, session_id)
        REFERENCES refresh_tokens(id, session_id) ON DELETE RESTRICT,
    CONSTRAINT uq_refresh_tokens_previous_token
        UNIQUE (previous_token_id),
    CONSTRAINT uq_refresh_tokens_hash
        UNIQUE (token_hash),
    CONSTRAINT ck_refresh_tokens_hash_sha256_length
        CHECK (octet_length(token_hash) = 32),
    CONSTRAINT ck_refresh_tokens_not_own_predecessor
        CHECK (previous_token_id IS NULL OR previous_token_id <> id),
    CONSTRAINT ck_refresh_tokens_expires_after_created
        CHECK (expires_at > created_at),
    CONSTRAINT ck_refresh_tokens_consumed_after_created
        CHECK (consumed_at IS NULL OR consumed_at >= created_at),
    CONSTRAINT ck_refresh_tokens_revoked_after_created
        CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX idx_refresh_tokens_session_created
    ON refresh_tokens(session_id, created_at DESC);

CREATE INDEX idx_refresh_tokens_expiry
    ON refresh_tokens(expires_at);

CREATE INDEX idx_refresh_tokens_live_session
    ON refresh_tokens(session_id, expires_at)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;

COMMIT;
