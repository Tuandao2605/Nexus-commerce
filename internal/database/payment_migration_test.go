// Package database contains migration and constraint integration tests.
// This file covers ECOM-DB-004H: Payment Schema and validates PostgreSQL
// invariants like allocation concurrency, timestamp matrices, and webhook dedup.
package database

import (
	"sync"
	"testing"
)

// allocatePaymentRefundTestSQL mô phỏng boundary repository: giữ refundable capacity
// và tạo refund row trong cùng một PostgreSQL statement để lỗi insert rollback counter.
const allocatePaymentRefundTestSQL = `
	WITH allocated_payment AS (
		UPDATE payment_transactions
		SET allocated_refund_amount = allocated_refund_amount + $6,
			updated_at = clock_timestamp()
		WHERE id = $1
		  AND status = 'succeeded'
		  AND allocated_refund_amount + $6 <= amount
		RETURNING id, provider, currency_code
	)
	INSERT INTO payment_refunds (
		id, payment_transaction_id, provider, provider_refund_id, idempotency_key,
		request_hash, amount, currency_code, status, processing_at
	)
	SELECT
		$2, id, provider, $3, $4, repeat($5, 64), $6, currency_code, $7::varchar(20),
		CASE WHEN $7::varchar(20) = 'processing' THEN clock_timestamp() END
	FROM allocated_payment
`

// TestPaymentMigration covers 28 acceptance criteria for ECOM-DB-004H.
func TestPaymentMigration(t *testing.T) {
	t.Run("Group 1: payment_transactions Validation & Constraints", func(t *testing.T) {
		t.Run("Insert valid transaction", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)

			txID := newMigrationTestUUID(t)
			pi := "pi_" + newMigrationTestUUID(t)
			idemp := "idemp_" + newMigrationTestUUID(t)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', now() + interval '1 hour'
				)
			`, txID, parentID, pi, idemp)
			if err != nil {
				t.Fatalf("failed to insert valid payment transaction: %v", err)
			}
		})

		t.Run("Verify downstream FK reliance on Supporting Keys", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)
			txID := newMigrationTestUUID(t)
			pi := "pi_" + newMigrationTestUUID(t)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', now() + interval '1 hour'
				)
			`, txID, parentID, pi, "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed to setup transaction: %v", err)
			}

			_, err = tx.Exec(ctx, `
				INSERT INTO payment_webhook_events (
					id, provider, provider_event_id, provider_payment_id, payment_transaction_id,
					event_type, payload_hash, processing_status
				) VALUES (
					$1, 'PAYPAL', $2, $3, $4, 'payment_intent.succeeded', repeat('x', 64), 'received'
				)
			`, newMigrationTestUUID(t), "evt_"+newMigrationTestUUID(t), pi, txID)
			assertMigrationSQLState(t, err, "23503")
		})

		t.Run("Reject seller order type", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, expires_at
				) VALUES (
					$1, $2, 'seller', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', now() + interval '1 hour'
				)
			`, newMigrationTestUUID(t), parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			assertMigrationSQLState(t, err, "23514")
		})

		t.Run("Reject currency mismatch", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'USD', now() + interval '1 hour'
				)
			`, newMigrationTestUUID(t), parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			assertMigrationSQLState(t, err, "23503")
		})

		t.Run("Reject zero amount", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 0, 'VND', now() + interval '1 hour'
				)
			`, newMigrationTestUUID(t), parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			assertMigrationSQLState(t, err, "23514")
		})

		t.Run("Reject duplicate idempotency key", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)
			idemp := "idemp_dup_" + newMigrationTestUUID(t)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', now() + interval '1 hour'
				)
			`, newMigrationTestUUID(t), parentID, "pi_"+newMigrationTestUUID(t), idemp)
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			_, err = tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, expires_at
				) VALUES (
					$1, $2, 'parent', 'PAYPAL', $3, $4, repeat('b', 64), 100000, 'VND', now() + interval '1 hour'
				)
			`, newMigrationTestUUID(t), parentID, "pi_2_"+newMigrationTestUUID(t), idemp)
			assertMigrationSQLState(t, err, "23505")
		})

		t.Run("Reject expires_at <= created_at", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, created_at, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', now(), now() - interval '1 second'
				)
			`, newMigrationTestUUID(t), parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			assertMigrationSQLState(t, err, "23514")
		})

		t.Run("Reject updated_at < created_at", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, created_at, updated_at, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', now(), now() - interval '1 second', now() + interval '1 hour'
				)
			`, newMigrationTestUUID(t), parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			assertMigrationSQLState(t, err, "23514")
		})

		t.Run("Reject deleting order with existing transaction", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', now() + interval '1 hour'
				)
			`, newMigrationTestUUID(t), parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			_, err = tx.Exec(ctx, `DELETE FROM orders WHERE id = $1`, parentID)
			assertMigrationSQLState(t, err, "23503")
		})
	})

	t.Run("Group 2: Multi-attempt & Live Attempt Policy", func(t *testing.T) {
		t.Run("Allow Attempt 2 if Attempt 1 failed", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, failed_at, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'failed', now(), now() + interval '1 hour'
				)
			`, newMigrationTestUUID(t), parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			_, err = tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'pending', now() + interval '1 hour'
				)
			`, newMigrationTestUUID(t), parentID, "pi_2_"+newMigrationTestUUID(t), "idemp_2_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed to insert second attempt: %v", err)
			}
		})

		t.Run("Reject Attempt 2 if Attempt 1 is pending", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'pending', now() + interval '1 hour'
				)
			`, newMigrationTestUUID(t), parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			_, err = tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'pending', now() + interval '1 hour'
				)
			`, newMigrationTestUUID(t), parentID, "pi_2_"+newMigrationTestUUID(t), "idemp_2_"+newMigrationTestUUID(t))
			assertMigrationSQLState(t, err, "23505")
		})

		t.Run("Reject Attempt 2 if Attempt 1 is succeeded", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, processing_at, succeeded_at, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'succeeded', now(), now(), now() + interval '1 hour'
				)
			`, newMigrationTestUUID(t), parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			_, err = tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'pending', now() + interval '1 hour'
				)
			`, newMigrationTestUUID(t), parentID, "pi_2_"+newMigrationTestUUID(t), "idemp_2_"+newMigrationTestUUID(t))
			assertMigrationSQLState(t, err, "23505")
		})

		t.Run("Provider ID Partial Unique handles NULLs and duplicates", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID1 := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)
			parentID2 := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)
			parentID3 := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, expires_at
				) VALUES 
					($1, $2, 'parent', 'STRIPE', NULL, $3, repeat('a', 64), 100000, 'VND', now() + interval '1 hour'),
					($4, $5, 'parent', 'STRIPE', NULL, $6, repeat('b', 64), 100000, 'VND', now() + interval '1 hour')
			`, newMigrationTestUUID(t), parentID1, "idemp_"+newMigrationTestUUID(t), newMigrationTestUUID(t), parentID2, "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed to insert multiple NULL provider_payment_id: %v", err)
			}

			piDup := "pi_dup_" + newMigrationTestUUID(t)
			_, err = tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, failed_at, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('c', 64), 100000, 'VND', 'failed', now(), now() + interval '1 hour'
				)
			`, newMigrationTestUUID(t), parentID3, piDup, "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			_, err = tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('d', 64), 100000, 'VND', now() + interval '1 hour'
				)
			`, newMigrationTestUUID(t), parentID3, piDup, "idemp_new_"+newMigrationTestUUID(t))
			assertMigrationSQLState(t, err, "23505")
		})
	})

	t.Run("Group 3: State-Timestamp Matrix", func(t *testing.T) {
		t.Run("Reject succeeded without timestamps", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'succeeded', now() + interval '1 hour'
				)
			`, newMigrationTestUUID(t), parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			assertMigrationSQLState(t, err, "23514")
		})

		t.Run("Reject pending with timestamps", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, processing_at, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'pending', now(), now() + interval '1 hour'
				)
			`, newMigrationTestUUID(t), parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			assertMigrationSQLState(t, err, "23514")
		})
	})

	t.Run("Group 4: payment_webhook_events", func(t *testing.T) {
		t.Run("Insert valid inbox event", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)
			txID := newMigrationTestUUID(t)
			pi := "pi_" + newMigrationTestUUID(t)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', now() + interval '1 hour'
				)
			`, txID, parentID, pi, "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			_, err = tx.Exec(ctx, `
				INSERT INTO payment_webhook_events (
					id, provider, provider_event_id, provider_payment_id, payment_transaction_id,
					event_type, payload_hash, processing_status
				) VALUES (
					$1, 'STRIPE', $2, $3, $4, 'payment_intent.succeeded', repeat('x', 64), 'received'
				)
			`, newMigrationTestUUID(t), "evt_"+newMigrationTestUUID(t), pi, txID)
			if err != nil {
				t.Fatalf("failed to insert webhook event: %v", err)
			}
		})

		t.Run("Reject composite FK mismatch", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)
			txID := newMigrationTestUUID(t)
			pi := "pi_" + newMigrationTestUUID(t)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', now() + interval '1 hour'
				)
			`, txID, parentID, pi, "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			_, err = tx.Exec(ctx, `
				INSERT INTO payment_webhook_events (
					id, provider, provider_event_id, provider_payment_id, payment_transaction_id,
					event_type, payload_hash, processing_status
				) VALUES (
					$1, 'PAYPAL', $2, $3, $4, 'payment_intent.succeeded', repeat('x', 64), 'received'
				)
			`, newMigrationTestUUID(t), "evt_"+newMigrationTestUUID(t), pi, txID)
			assertMigrationSQLState(t, err, "23503")
		})

		t.Run("Reject duplicate provider event", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			evtDup := "evt_dup_" + newMigrationTestUUID(t)
			pi := "pi_" + newMigrationTestUUID(t)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_webhook_events (
					id, provider, provider_event_id, provider_payment_id,
					event_type, payload_hash, processing_status
				) VALUES (
					$1, 'STRIPE', $2, $3, 'charge.succeeded', repeat('x', 64), 'received'
				)
			`, newMigrationTestUUID(t), evtDup, pi)
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			_, err = tx.Exec(ctx, `
				INSERT INTO payment_webhook_events (
					id, provider, provider_event_id, provider_payment_id,
					event_type, payload_hash, processing_status
				) VALUES (
					$1, 'STRIPE', $2, $3, 'charge.failed', repeat('y', 64), 'received'
				)
			`, newMigrationTestUUID(t), evtDup, pi)
			assertMigrationSQLState(t, err, "23505")
		})

		t.Run("Reject invalid processing_status", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			_, err := tx.Exec(ctx, `
				INSERT INTO payment_webhook_events (
					id, provider, provider_event_id, provider_payment_id,
					event_type, payload_hash, processing_status
				) VALUES (
					$1, 'STRIPE', $2, $3, 'charge.succeeded', repeat('x', 64), 'INVALID_STATUS'
				)
			`, newMigrationTestUUID(t), "evt_"+newMigrationTestUUID(t), "pi_"+newMigrationTestUUID(t))
			assertMigrationSQLState(t, err, "23514")
		})

		t.Run("Guarded out-of-order webhook transition keeps terminal state", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)
			txID := newMigrationTestUUID(t)
			pi := "pi_" + newMigrationTestUUID(t)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, processing_at, succeeded_at, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'succeeded', now(), now(), now() + interval '1 hour'
				)
			`, txID, parentID, pi, "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			// 1. Prove we CANNOT roll back the transaction state to processing
			// Mô phỏng guarded repository transition. Việc clear succeeded_at khiến
			// row shape "processing" hợp lệ; status guard mới là terminal-state latch.
			transitionTag, err := tx.Exec(ctx, `
				UPDATE payment_transactions
				SET status = 'processing',
					processing_at = clock_timestamp(),
					succeeded_at = NULL,
					updated_at = clock_timestamp()
				WHERE id = $1 AND status = 'pending'
			`, txID)
			if err != nil {
				t.Fatalf("guarded out-of-order transition: %v", err)
			}
			if transitionTag.RowsAffected() != 0 {
				t.Fatalf("out-of-order transition changed a terminal payment")
			}

			var currentStatus string
			var succeededAtPresent bool
			err = tx.QueryRow(ctx, `
				SELECT status, succeeded_at IS NOT NULL
				FROM payment_transactions
				WHERE id = $1
			`, txID).Scan(&currentStatus, &succeededAtPresent)
			if err != nil || currentStatus != "succeeded" || !succeededAtPresent {
				t.Fatalf("expected unchanged succeeded payment, got status=%q succeeded_at_present=%t err=%v", currentStatus, succeededAtPresent, err)
			}

			// 2. Prove we CAN insert the ignored out-of-order webhook safely
			_, err = tx.Exec(ctx, `
				INSERT INTO payment_webhook_events (
					id, provider, provider_event_id, provider_payment_id, payment_transaction_id,
					event_type, payload_hash, processing_status
				) VALUES (
					$1, 'STRIPE', $2, $3, $4, 'payment_intent.processing', repeat('x', 64), 'ignored'
				)
			`, newMigrationTestUUID(t), "evt_"+newMigrationTestUUID(t), pi, txID)
			if err != nil {
				t.Fatalf("failed to insert ignored out-of-order webhook event: %v", err)
			}
		})
	})

	t.Run("Group 5: payment_refunds", func(t *testing.T) {
		t.Run("Refund Allocation Flow and Idempotency", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)
			txID := newMigrationTestUUID(t)
			failedRefundID := newMigrationTestUUID(t)
			cancelledRefundID := newMigrationTestUUID(t)
			fullRefundID := newMigrationTestUUID(t)

			// Step 0: Insert Payment with 0 allocated
			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, processing_at, succeeded_at, expires_at, allocated_refund_amount
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'succeeded', now(), now(), now() + interval '1 hour', 0
				)
			`, txID, parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			// Hai active refunds cùng giữ toàn bộ refundable capacity.
			for _, refund := range []struct {
				id     string
				amount int64
				status string
				seed   string
			}{
				{id: failedRefundID, amount: 40000, status: "processing", seed: "f"},
				{id: cancelledRefundID, amount: 60000, status: "pending", seed: "c"},
			} {
				tag, allocationErr := tx.Exec(ctx, allocatePaymentRefundTestSQL,
					txID,
					refund.id,
					"re_"+newMigrationTestUUID(t),
					"ref_"+newMigrationTestUUID(t),
					refund.seed,
					refund.amount,
					refund.status,
				)
				if allocationErr != nil {
					t.Fatalf("allocate %s refund: %v", refund.status, allocationErr)
				}
				if tag.RowsAffected() != 1 {
					t.Fatalf("allocate %s refund affected %d rows, want 1", refund.status, tag.RowsAffected())
				}
			}

			// releaseRefund mô phỏng repository transaction: chỉ terminalize một active
			// refund đúng một lần, và chỉ lần transition thành công mới giảm counter.
			releaseRefund := func(refundID, terminalStatus string) bool {
				var transitionSQL string
				switch terminalStatus {
				case "failed":
					transitionSQL = `
						UPDATE payment_refunds
						SET status = 'failed', failed_at = clock_timestamp(), updated_at = clock_timestamp()
						WHERE id = $1 AND status IN ('pending', 'processing')
					`
				case "cancelled":
					transitionSQL = `
						UPDATE payment_refunds
						SET status = 'cancelled', cancelled_at = clock_timestamp(), updated_at = clock_timestamp()
						WHERE id = $1 AND status IN ('pending', 'processing')
					`
				default:
					t.Fatalf("unsupported refund terminal status %q", terminalStatus)
				}

				transitionTag, transitionErr := tx.Exec(ctx, transitionSQL, refundID)
				if transitionErr != nil {
					t.Fatalf("transition refund to %s: %v", terminalStatus, transitionErr)
				}
				if transitionTag.RowsAffected() == 0 {
					return false
				}

				releaseTag, releaseErr := tx.Exec(ctx, `
					UPDATE payment_transactions AS payment
					SET allocated_refund_amount = payment.allocated_refund_amount - refund.amount,
						updated_at = clock_timestamp()
					FROM payment_refunds AS refund
					WHERE refund.id = $1
					  AND payment.id = refund.payment_transaction_id
					  AND payment.allocated_refund_amount >= refund.amount
				`, refundID)
				if releaseErr != nil {
					t.Fatalf("release %s refund capacity: %v", terminalStatus, releaseErr)
				}
				if releaseTag.RowsAffected() != 1 {
					t.Fatalf("release %s refund capacity affected %d rows, want 1", terminalStatus, releaseTag.RowsAffected())
				}
				return true
			}

			if !releaseRefund(failedRefundID, "failed") {
				t.Fatal("first failed transition was not applied")
			}
			if releaseRefund(failedRefundID, "failed") {
				t.Fatal("repeated failed transition released capacity twice")
			}

			var allocated int64
			if err := tx.QueryRow(ctx, `SELECT allocated_refund_amount FROM payment_transactions WHERE id = $1`, txID).Scan(&allocated); err != nil {
				t.Fatalf("query allocation after failed refund: %v", err)
			}
			if allocated != 60000 {
				t.Fatalf("allocation after idempotent failed release = %d, want 60000", allocated)
			}

			if !releaseRefund(cancelledRefundID, "cancelled") {
				t.Fatal("first cancelled transition was not applied")
			}
			if releaseRefund(cancelledRefundID, "cancelled") {
				t.Fatal("repeated cancelled transition released capacity twice")
			}

			fullAllocationTag, err := tx.Exec(ctx, allocatePaymentRefundTestSQL,
				txID,
				fullRefundID,
				"re_"+newMigrationTestUUID(t),
				"ref_"+newMigrationTestUUID(t),
				"n",
				int64(100000),
				"pending",
			)
			if err != nil {
				t.Fatalf("allocate replacement full refund: %v", err)
			}
			if fullAllocationTag.RowsAffected() != 1 {
				t.Fatalf("replacement full refund affected %d rows, want 1", fullAllocationTag.RowsAffected())
			}

			if err := tx.QueryRow(ctx, `SELECT allocated_refund_amount FROM payment_transactions WHERE id = $1`, txID).Scan(&allocated); err != nil {
				t.Fatalf("query allocation after replacement refund: %v", err)
			}
			if allocated != 100000 {
				t.Fatalf("allocation after replacement refund = %d, want 100000", allocated)
			}

			var activeCount, activeAmount int64
			err = tx.QueryRow(ctx, `
				SELECT count(*), COALESCE(sum(amount), 0)
				FROM payment_refunds
				WHERE payment_transaction_id = $1
				  AND status IN ('pending', 'processing', 'succeeded')
			`, txID).Scan(&activeCount, &activeAmount)
			if err != nil {
				t.Fatalf("query active refunds after releases: %v", err)
			}
			if activeCount != 1 || activeAmount != 100000 {
				t.Fatalf("active refunds count=%d amount=%d, want 1 and 100000", activeCount, activeAmount)
			}
		})

		t.Run("Two partial refunds allocate correctly", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)
			txID := newMigrationTestUUID(t)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, processing_at, succeeded_at, expires_at, allocated_refund_amount
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'succeeded', now(), now(), now() + interval '1 hour', 0
				)
			`, txID, parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			for index, amount := range []int64{40000, 60000} {
				tag, allocationErr := tx.Exec(ctx, allocatePaymentRefundTestSQL,
					txID,
					newMigrationTestUUID(t),
					"re_"+newMigrationTestUUID(t),
					"ref_"+newMigrationTestUUID(t),
					[]string{"p", "q"}[index],
					amount,
					"pending",
				)
				if allocationErr != nil {
					t.Fatalf("allocate partial refund %d: %v", index+1, allocationErr)
				}
				if tag.RowsAffected() != 1 {
					t.Fatalf("partial refund %d affected %d rows, want 1", index+1, tag.RowsAffected())
				}
			}

			thirdTag, err := tx.Exec(ctx, allocatePaymentRefundTestSQL,
				txID,
				newMigrationTestUUID(t),
				"re_"+newMigrationTestUUID(t),
				"ref_"+newMigrationTestUUID(t),
				"z",
				int64(1),
				"pending",
			)
			if err != nil {
				t.Fatalf("reject over-allocation without SQL error: %v", err)
			}
			if thirdTag.RowsAffected() != 0 {
				t.Fatalf("over-limit partial refund inserted %d rows, want 0", thirdTag.RowsAffected())
			}

			var allocated, refundCount, refundAmount int64
			err = tx.QueryRow(ctx, `
				SELECT
					payment.allocated_refund_amount,
					count(refund.id),
					COALESCE(sum(refund.amount), 0)
				FROM payment_transactions AS payment
				LEFT JOIN payment_refunds AS refund
				  ON refund.payment_transaction_id = payment.id
				 AND refund.status IN ('pending', 'processing', 'succeeded')
				WHERE payment.id = $1
				GROUP BY payment.allocated_refund_amount
			`, txID).Scan(&allocated, &refundCount, &refundAmount)
			if err != nil {
				t.Fatalf("query partial refund allocation: %v", err)
			}
			if allocated != 100000 || refundCount != 2 || refundAmount != 100000 {
				t.Fatalf("partial refund state counter=%d rows=%d sum=%d, want 100000, 2, 100000", allocated, refundCount, refundAmount)
			}
		})

		t.Run("Refund deduplication prevents double allocation", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)
			txID := newMigrationTestUUID(t)
			idemp := "idemp_dup_" + newMigrationTestUUID(t)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, processing_at, succeeded_at, expires_at, allocated_refund_amount
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'succeeded', now(), now(), now() + interval '1 hour', 0
				)
			`, txID, parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			_, err = tx.Exec(ctx, allocatePaymentRefundTestSQL,
				txID,
				newMigrationTestUUID(t),
				"re_"+newMigrationTestUUID(t),
				idemp,
				"r",
				int64(50000),
				"pending",
			)
			if err != nil {
				t.Fatalf("failed first refund: %v", err)
			}

			if _, err := tx.Exec(ctx, "SAVEPOINT payment_refund_duplicate"); err != nil {
				t.Fatalf("create duplicate-refund savepoint: %v", err)
			}
			_, err = tx.Exec(ctx, allocatePaymentRefundTestSQL,
				txID,
				newMigrationTestUUID(t),
				"re_2_"+newMigrationTestUUID(t),
				idemp,
				"x",
				int64(50000),
				"pending",
			)
			assertMigrationSQLState(t, err, "23505")
			if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT payment_refund_duplicate"); err != nil {
				t.Fatalf("rollback duplicate-refund savepoint: %v", err)
			}
			if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT payment_refund_duplicate"); err != nil {
				t.Fatalf("release duplicate-refund savepoint: %v", err)
			}

			var allocated, refundCount int64
			err = tx.QueryRow(ctx, `
				SELECT payment.allocated_refund_amount, count(refund.id)
				FROM payment_transactions AS payment
				LEFT JOIN payment_refunds AS refund ON refund.payment_transaction_id = payment.id
				WHERE payment.id = $1
				GROUP BY payment.allocated_refund_amount
			`, txID).Scan(&allocated, &refundCount)
			if err != nil {
				t.Fatalf("query deduplicated refund allocation: %v", err)
			}
			if allocated != 50000 || refundCount != 1 {
				t.Fatalf("duplicate refund state counter=%d rows=%d, want 50000 and 1", allocated, refundCount)
			}
		})

		t.Run("Failed refund insert rolls back without leaking capacity", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)
			txID := newMigrationTestUUID(t)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, processing_at, succeeded_at, expires_at, allocated_refund_amount
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'succeeded', now(), now(), now() + interval '1 hour', 0
				)
			`, txID, parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			if _, err := tx.Exec(ctx, "SAVEPOINT invalid_refund_insert"); err != nil {
				t.Fatalf("create invalid-refund savepoint: %v", err)
			}
			_, err = tx.Exec(ctx, allocatePaymentRefundTestSQL,
				txID,
				newMigrationTestUUID(t),
				"re_"+newMigrationTestUUID(t),
				"ref_"+newMigrationTestUUID(t),
				"r",
				int64(50000),
				"invalid",
			)
			assertMigrationSQLState(t, err, "23514")
			if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT invalid_refund_insert"); err != nil {
				t.Fatalf("rollback invalid-refund savepoint: %v", err)
			}
			if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT invalid_refund_insert"); err != nil {
				t.Fatalf("release invalid-refund savepoint: %v", err)
			}

			var allocated, refundCount int64
			err = tx.QueryRow(ctx, `
				SELECT payment.allocated_refund_amount, count(refund.id)
				FROM payment_transactions AS payment
				LEFT JOIN payment_refunds AS refund ON refund.payment_transaction_id = payment.id
				WHERE payment.id = $1
				GROUP BY payment.allocated_refund_amount
			`, txID).Scan(&allocated, &refundCount)
			if err != nil {
				t.Fatalf("query failed refund allocation rollback: %v", err)
			}
			if allocated != 0 || refundCount != 0 {
				t.Fatalf("failed refund insert left counter=%d rows=%d, want 0 and 0", allocated, refundCount)
			}
		})

		t.Run("Reject composite FK provider mismatch", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)
			txID := newMigrationTestUUID(t)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status,
					processing_at, succeeded_at, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000,
					'VND', 'succeeded', now(), now(), now() + interval '1 hour'
				)
			`, txID, parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			_, err = tx.Exec(ctx, `
				INSERT INTO payment_refunds (
					id, payment_transaction_id, provider, provider_refund_id, idempotency_key,
					request_hash, amount, currency_code
				) VALUES (
					$1, $2, 'PAYPAL', $3, $4, repeat('r', 64), 50000, 'VND'
				)
			`, newMigrationTestUUID(t), txID, "re_"+newMigrationTestUUID(t), "ref_"+newMigrationTestUUID(t))
			assertMigrationSQLState(t, err, "23503")
		})

		t.Run("Reject composite FK currency mismatch", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)
			txID := newMigrationTestUUID(t)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, processing_at, succeeded_at, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'succeeded', now(), now(), now() + interval '1 hour'
				)
			`, txID, parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			_, err = tx.Exec(ctx, `
				INSERT INTO payment_refunds (
					id, payment_transaction_id, provider, provider_refund_id, idempotency_key,
					request_hash, amount, currency_code
				) VALUES (
					$1, $2, 'STRIPE', $3, $4, repeat('r', 64), 50000, 'USD'
				)
			`, newMigrationTestUUID(t), txID, "re_"+newMigrationTestUUID(t), "ref_"+newMigrationTestUUID(t))
			assertMigrationSQLState(t, err, "23503")
		})

		t.Run("Reject zero amount", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)
			txID := newMigrationTestUUID(t)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, processing_at, succeeded_at, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'succeeded', now(), now(), now() + interval '1 hour'
				)
			`, txID, parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			_, err = tx.Exec(ctx, `
				INSERT INTO payment_refunds (
					id, payment_transaction_id, provider, provider_refund_id, idempotency_key,
					request_hash, amount, currency_code
				) VALUES (
					$1, $2, 'STRIPE', $3, $4, repeat('r', 64), 0, 'VND'
				)
			`, newMigrationTestUUID(t), txID, "re_"+newMigrationTestUUID(t), "ref_"+newMigrationTestUUID(t))
			assertMigrationSQLState(t, err, "23514")
		})

		t.Run("Reject succeeded without timestamps", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)
			txID := newMigrationTestUUID(t)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, processing_at, succeeded_at, expires_at
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'succeeded', now(), now(), now() + interval '1 hour'
				)
			`, txID, parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			_, err = tx.Exec(ctx, `
				INSERT INTO payment_refunds (
					id, payment_transaction_id, provider, provider_refund_id, idempotency_key,
					request_hash, amount, currency_code, status
				) VALUES (
					$1, $2, 'STRIPE', $3, $4, repeat('r', 64), 50000, 'VND', 'succeeded'
				)
			`, newMigrationTestUUID(t), txID, "re_"+newMigrationTestUUID(t), "ref_"+newMigrationTestUUID(t))
			assertMigrationSQLState(t, err, "23514")
		})

		t.Run("Provider Refund ID Partial Unique handles NULLs and duplicates", func(t *testing.T) {
			ctx, tx := beginMigrationTest(t)
			userID := insertSellerCatalogTestUser(t, ctx, tx)
			parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)
			txID := newMigrationTestUUID(t)

			_, err := tx.Exec(ctx, `
				INSERT INTO payment_transactions (
					id, parent_order_id, parent_order_type, provider, provider_payment_id,
					idempotency_key, request_hash, amount, currency_code, status, processing_at, succeeded_at, expires_at, allocated_refund_amount
				) VALUES (
					$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'succeeded', now(), now(), now() + interval '1 hour', 100000
				)
			`, txID, parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed setup: %v", err)
			}

			// Allow multiple NULLs
			_, err = tx.Exec(ctx, `
				INSERT INTO payment_refunds (
					id, payment_transaction_id, provider, provider_refund_id, idempotency_key,
					request_hash, amount, currency_code
				) VALUES 
					($1, $2, 'STRIPE', NULL, $3, repeat('r', 64), 10000, 'VND'),
					($4, $2, 'STRIPE', NULL, $5, repeat('x', 64), 10000, 'VND')
			`, newMigrationTestUUID(t), txID, "idemp_1_"+newMigrationTestUUID(t), newMigrationTestUUID(t), "idemp_2_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed to insert multiple NULL provider_refund_id: %v", err)
			}

			reDup := "re_dup_" + newMigrationTestUUID(t)
			// Reject duplicate NOT NULL
			_, err = tx.Exec(ctx, `
				INSERT INTO payment_refunds (
					id, payment_transaction_id, provider, provider_refund_id, idempotency_key,
					request_hash, amount, currency_code
				) VALUES (
					$1, $2, 'STRIPE', $3, $4, repeat('y', 64), 10000, 'VND'
				)
			`, newMigrationTestUUID(t), txID, reDup, "idemp_3_"+newMigrationTestUUID(t))
			if err != nil {
				t.Fatalf("failed insert duplicate 1: %v", err)
			}

			_, err = tx.Exec(ctx, `
				INSERT INTO payment_refunds (
					id, payment_transaction_id, provider, provider_refund_id, idempotency_key,
					request_hash, amount, currency_code
				) VALUES (
					$1, $2, 'STRIPE', $3, $4, repeat('z', 64), 10000, 'VND'
				)
			`, newMigrationTestUUID(t), txID, reDup, "idemp_4_"+newMigrationTestUUID(t))
			assertMigrationSQLState(t, err, "23505")
		})
	})

	t.Run("Group 6: Stress Concurrency", func(t *testing.T) {
		ctx, pool := openMigrationTestPool(t)

		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("failed to begin setup tx: %v", err)
		}
		userID := insertSellerCatalogTestUser(t, ctx, tx)
		parentID := insertOrderTestParent(t, ctx, tx, userID, "VND", 100000)
		txID := newMigrationTestUUID(t)
		_, err = tx.Exec(ctx, `
			INSERT INTO payment_transactions (
				id, parent_order_id, parent_order_type, provider, provider_payment_id,
				idempotency_key, request_hash, amount, currency_code, status, processing_at, succeeded_at, expires_at, allocated_refund_amount
			) VALUES (
				$1, $2, 'parent', 'STRIPE', $3, $4, repeat('a', 64), 100000, 'VND', 'succeeded', now(), now(), now() + interval '1 hour', 0
			)
		`, txID, parentID, "pi_"+newMigrationTestUUID(t), "idemp_"+newMigrationTestUUID(t))
		if err != nil {
			t.Fatalf("failed to insert stress transaction: %v", err)
		}

		err = tx.Commit(ctx)
		if err != nil {
			t.Fatalf("failed to commit setup tx: %v", err)
		}

		defer func() {
			if _, cleanupErr := pool.Exec(ctx, "DELETE FROM payment_refunds WHERE payment_transaction_id = $1", txID); cleanupErr != nil {
				t.Logf("cleanup refunds warning: %v", cleanupErr)
			}
			if _, cleanupErr := pool.Exec(ctx, "DELETE FROM payment_transactions WHERE id = $1", txID); cleanupErr != nil {
				t.Logf("cleanup payment warning: %v", cleanupErr)
			}
		}()

		const workers = 100
		const allocateAmount = 100000
		refundIDs := make([]string, workers)
		providerRefundIDs := make([]string, workers)
		idempotencyKeys := make([]string, workers)
		for i := 0; i < workers; i++ {
			refundIDs[i] = newMigrationTestUUID(t)
			providerRefundIDs[i] = "re_" + newMigrationTestUUID(t)
			idempotencyKeys[i] = "ref_" + newMigrationTestUUID(t)
		}

		type concurrentResult struct {
			success bool
			err     error
		}

		var wg sync.WaitGroup
		results := make(chan concurrentResult, workers)

		for i := 0; i < workers; i++ {
			workerIndex := i
			wg.Add(1)
			go func() {
				defer wg.Done()
				tag, err := pool.Exec(ctx, allocatePaymentRefundTestSQL,
					txID,
					refundIDs[workerIndex],
					providerRefundIDs[workerIndex],
					idempotencyKeys[workerIndex],
					"s",
					int64(allocateAmount),
					"pending",
				)
				if err != nil {
					results <- concurrentResult{err: err}
					return
				}
				results <- concurrentResult{success: tag.RowsAffected() == 1}
			}()
		}
		wg.Wait()
		close(results)

		successCount := 0
		rejectedCount := 0
		for result := range results {
			if result.err != nil {
				t.Fatalf("concurrent refund allocation: %v", result.err)
			}
			if result.success {
				successCount++
			} else {
				rejectedCount++
			}
		}

		if successCount != 1 {
			t.Errorf("expected 1 successful allocation, got %d", successCount)
		}
		if rejectedCount != 99 {
			t.Errorf("expected 99 rejected allocations, got %d", rejectedCount)
		}

		var finalAllocation int64
		err = pool.QueryRow(ctx, "SELECT allocated_refund_amount FROM payment_transactions WHERE id = $1", txID).Scan(&finalAllocation)
		if err != nil {
			t.Fatalf("failed to query final allocation: %v", err)
		}

		if finalAllocation != allocateAmount {
			t.Fatalf("expected final allocated_refund_amount to be %d, got %d", allocateAmount, finalAllocation)
		}

		var storedRefunds, storedRefundAmount int64
		err = pool.QueryRow(ctx, `
			SELECT count(*), COALESCE(sum(amount), 0)
			FROM payment_refunds
			WHERE payment_transaction_id = $1
		`, txID).Scan(&storedRefunds, &storedRefundAmount)
		if err != nil {
			t.Fatalf("query concurrent refund rows: %v", err)
		}
		if storedRefunds != 1 || storedRefundAmount != allocateAmount {
			t.Fatalf("concurrent refund rows=%d amount=%d, want 1 and %d", storedRefunds, storedRefundAmount, allocateAmount)
		}
	})
}
