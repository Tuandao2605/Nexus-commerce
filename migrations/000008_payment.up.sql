-- File UP này tạo schema Payment V1 gồm payment_transactions, webhook inbox và refunds,
-- cùng các FK, uniqueness, lifecycle checks và counter bảo vệ refund allocation.

BEGIN;

CREATE TABLE payment_transactions (
    id UUID PRIMARY KEY,
    parent_order_id UUID NOT NULL,
    parent_order_type VARCHAR(20) NOT NULL,
    provider VARCHAR(40) NOT NULL,
    provider_payment_id VARCHAR(191),
    idempotency_key VARCHAR(128) NOT NULL,
    request_hash CHAR(64) NOT NULL,
    amount BIGINT NOT NULL,
    currency_code CHAR(3) NOT NULL,
    allocated_refund_amount BIGINT NOT NULL DEFAULT 0,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    failure_code VARCHAR(100),
    failure_message VARCHAR(512),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processing_at TIMESTAMPTZ,
    succeeded_at TIMESTAMPTZ,
    failed_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,

    CONSTRAINT fk_payment_transactions_parent_order
        FOREIGN KEY (parent_order_id, currency_code, parent_order_type)
        REFERENCES orders(id, currency, order_type) ON DELETE RESTRICT,
    CONSTRAINT ck_payment_transactions_parent_order_type
        CHECK (parent_order_type = 'parent'),
    CONSTRAINT uq_payment_transactions_id_provider
        UNIQUE (id, provider),
    CONSTRAINT uq_payment_transactions_id_provider_currency
        UNIQUE (id, provider, currency_code),
    CONSTRAINT uq_payment_transactions_idempotency_key
        UNIQUE (idempotency_key),
    CONSTRAINT ck_payment_transactions_request_hash_length
        CHECK (char_length(request_hash) = 64),
    CONSTRAINT ck_payment_transactions_amount_positive
        CHECK (amount > 0),
    CONSTRAINT ck_payment_transactions_refund_allocation
        CHECK (allocated_refund_amount >= 0 AND allocated_refund_amount <= amount),
    CONSTRAINT ck_payment_transactions_status_enum
        CHECK (status IN ('pending', 'processing', 'succeeded', 'failed', 'cancelled')),
    CONSTRAINT ck_payment_transactions_state_timestamp_matrix
        CHECK (
            (status = 'pending'
             AND processing_at IS NULL AND succeeded_at IS NULL
             AND failed_at IS NULL AND cancelled_at IS NULL)
            OR
            (status = 'processing'
             AND processing_at IS NOT NULL AND succeeded_at IS NULL
             AND failed_at IS NULL AND cancelled_at IS NULL)
            OR
            (status = 'succeeded'
             AND processing_at IS NOT NULL AND succeeded_at IS NOT NULL
             AND failed_at IS NULL AND cancelled_at IS NULL)
            OR
            (status = 'failed'
             AND succeeded_at IS NULL AND failed_at IS NOT NULL
             AND cancelled_at IS NULL)
            OR
            (status = 'cancelled'
             AND succeeded_at IS NULL AND failed_at IS NULL
             AND cancelled_at IS NOT NULL)
        ),
    CONSTRAINT ck_payment_transactions_timeline
        CHECK (
            updated_at >= created_at
            AND expires_at > created_at
            AND (processing_at IS NULL OR processing_at >= created_at)
            AND (succeeded_at IS NULL OR succeeded_at >= created_at)
            AND (failed_at IS NULL OR failed_at >= created_at)
            AND (cancelled_at IS NULL OR cancelled_at >= created_at)
        ),
    CONSTRAINT ck_payment_transactions_currency_format
        CHECK (currency_code = upper(currency_code) AND char_length(currency_code) = 3),
    CONSTRAINT ck_payment_transactions_provider_format
        CHECK (char_length(btrim(provider)) BETWEEN 1 AND 40)
);

CREATE UNIQUE INDEX uq_payment_parent_live_attempt
    ON payment_transactions(parent_order_id)
    WHERE status IN ('pending', 'processing', 'succeeded');

CREATE UNIQUE INDEX uq_payment_provider_payment
    ON payment_transactions(provider, provider_payment_id)
    WHERE provider_payment_id IS NOT NULL;

CREATE INDEX idx_payment_parent_created
    ON payment_transactions(parent_order_id, created_at DESC);

CREATE INDEX idx_payment_reconciliation
    ON payment_transactions(status, created_at)
    WHERE status IN ('pending', 'processing');

CREATE TABLE payment_webhook_events (
    id UUID PRIMARY KEY,
    provider VARCHAR(40) NOT NULL,
    provider_event_id VARCHAR(191) NOT NULL,
    provider_payment_id VARCHAR(191),
    payment_transaction_id UUID,
    event_type VARCHAR(100) NOT NULL,
    payload_hash CHAR(64) NOT NULL,
    processing_status VARCHAR(20) NOT NULL DEFAULT 'received',
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ,
    last_error VARCHAR(512),

    CONSTRAINT uq_payment_webhook_provider_event
        UNIQUE (provider, provider_event_id),
    CONSTRAINT fk_payment_webhook_transaction
        FOREIGN KEY (payment_transaction_id, provider)
        REFERENCES payment_transactions(id, provider) ON DELETE RESTRICT,
    CONSTRAINT ck_payment_webhook_processing_status
        CHECK (processing_status IN ('received', 'processed', 'ignored', 'failed')),
    CONSTRAINT ck_payment_webhook_payload_hash_length
        CHECK (char_length(payload_hash) = 64),
    CONSTRAINT ck_payment_webhook_processed_timeline
        CHECK (processed_at IS NULL OR processed_at >= received_at)
);

CREATE INDEX idx_payment_webhook_processing
    ON payment_webhook_events(processing_status, received_at)
    WHERE processing_status IN ('received', 'failed');

CREATE INDEX idx_payment_webhook_payment
    ON payment_webhook_events(payment_transaction_id, received_at DESC)
    WHERE payment_transaction_id IS NOT NULL;

CREATE TABLE payment_refunds (
    id UUID PRIMARY KEY,
    payment_transaction_id UUID NOT NULL,
    provider VARCHAR(40) NOT NULL,
    provider_refund_id VARCHAR(191),
    idempotency_key VARCHAR(128) NOT NULL,
    request_hash CHAR(64) NOT NULL,
    amount BIGINT NOT NULL,
    currency_code CHAR(3) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    reason VARCHAR(512),
    failure_code VARCHAR(100),
    failure_message VARCHAR(512),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processing_at TIMESTAMPTZ,
    succeeded_at TIMESTAMPTZ,
    failed_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,

    CONSTRAINT fk_payment_refunds_transaction
        FOREIGN KEY (payment_transaction_id, provider, currency_code)
        REFERENCES payment_transactions(id, provider, currency_code) ON DELETE RESTRICT,
    CONSTRAINT uq_payment_refunds_idempotency_key
        UNIQUE (idempotency_key),
    CONSTRAINT ck_payment_refunds_request_hash_length
        CHECK (char_length(request_hash) = 64),
    CONSTRAINT ck_payment_refunds_amount_positive
        CHECK (amount > 0),
    CONSTRAINT ck_payment_refunds_status_enum
        CHECK (status IN ('pending', 'processing', 'succeeded', 'failed', 'cancelled')),
    CONSTRAINT ck_payment_refunds_state_timestamp_matrix
        CHECK (
            (status = 'pending'
             AND processing_at IS NULL AND succeeded_at IS NULL
             AND failed_at IS NULL AND cancelled_at IS NULL)
            OR
            (status = 'processing'
             AND processing_at IS NOT NULL AND succeeded_at IS NULL
             AND failed_at IS NULL AND cancelled_at IS NULL)
            OR
            (status = 'succeeded'
             AND processing_at IS NOT NULL AND succeeded_at IS NOT NULL
             AND failed_at IS NULL AND cancelled_at IS NULL)
            OR
            (status = 'failed'
             AND succeeded_at IS NULL AND failed_at IS NOT NULL
             AND cancelled_at IS NULL)
            OR
            (status = 'cancelled'
             AND succeeded_at IS NULL AND failed_at IS NULL
             AND cancelled_at IS NOT NULL)
        ),
    CONSTRAINT ck_payment_refunds_timeline
        CHECK (
            updated_at >= created_at
            AND (processing_at IS NULL OR processing_at >= created_at)
            AND (succeeded_at IS NULL OR succeeded_at >= created_at)
            AND (failed_at IS NULL OR failed_at >= created_at)
            AND (cancelled_at IS NULL OR cancelled_at >= created_at)
        ),
    CONSTRAINT ck_payment_refunds_currency_format
        CHECK (currency_code = upper(currency_code) AND char_length(currency_code) = 3)
);

CREATE UNIQUE INDEX uq_refund_provider_refund
    ON payment_refunds(provider, provider_refund_id)
    WHERE provider_refund_id IS NOT NULL;

CREATE INDEX idx_payment_refunds_payment_created
    ON payment_refunds(payment_transaction_id, created_at DESC);

CREATE INDEX idx_payment_refunds_status_created
    ON payment_refunds(status, created_at)
    WHERE status IN ('pending', 'processing');

COMMIT;
