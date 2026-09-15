-- File DOWN này rollback schema Payment V1 theo thứ tự refunds, webhook events rồi payment transactions.

BEGIN;

DROP TABLE payment_refunds;
DROP TABLE payment_webhook_events;
DROP TABLE payment_transactions;

COMMIT;
