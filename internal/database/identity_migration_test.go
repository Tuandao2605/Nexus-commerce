// File này kiểm thử migration Identity bằng PostgreSQL thật và xác nhận các invariant quan trọng được database cưỡng chế.
package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestIdentityMigrationCreatesExpectedObjects xác nhận migration đã tạo đủ bốn bảng và các index quan trọng của Identity.
func TestIdentityMigrationCreatesExpectedObjects(t *testing.T) {
	ctx, tx := beginIdentityTest(t)

	objects := []string{
		"users",
		"credentials",
		"sessions",
		"user_addresses",
		"idx_users_status",
		"uq_credentials_email",
		"idx_sessions_user_revoked_expires",
		"idx_sessions_expires_at",
		"uq_user_addresses_one_default",
		"idx_user_addresses_user_id",
	}

	for _, object := range objects {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+object).Scan(&exists); err != nil {
			t.Fatalf("check database object %s: %v", object, err)
		}
		if !exists {
			t.Errorf("database object %s does not exist", object)
		}
	}
}

// TestIdentityMigrationUsesCanonicalTypesAndDeleteRules xác nhận UUID/TIMESTAMPTZ không có default sai và mọi Identity FK đều RESTRICT.
func TestIdentityMigrationUsesCanonicalTypesAndDeleteRules(t *testing.T) {
	ctx, tx := beginIdentityTest(t)

	uuidColumns := [][2]string{
		{"users", "id"},
		{"credentials", "user_id"},
		{"sessions", "id"},
		{"sessions", "user_id"},
		{"user_addresses", "id"},
		{"user_addresses", "user_id"},
	}
	for _, column := range uuidColumns {
		var dataType string
		var defaultValue string
		if err := tx.QueryRow(ctx, `
			SELECT data_type, COALESCE(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
		`, column[0], column[1]).Scan(&dataType, &defaultValue); err != nil {
			t.Fatalf("inspect %s.%s: %v", column[0], column[1], err)
		}
		if dataType != "uuid" {
			t.Errorf("%s.%s type = %s, want uuid", column[0], column[1], dataType)
		}
		if defaultValue != "" {
			t.Errorf("%s.%s default = %q, want no database-generated UUID", column[0], column[1], defaultValue)
		}
	}

	timestampColumns := [][2]string{
		{"users", "created_at"},
		{"users", "updated_at"},
		{"users", "deleted_at"},
		{"credentials", "email_verified_at"},
		{"credentials", "password_changed_at"},
		{"credentials", "created_at"},
		{"credentials", "updated_at"},
		{"sessions", "created_at"},
		{"sessions", "last_activity_at"},
		{"sessions", "expires_at"},
		{"sessions", "revoked_at"},
		{"user_addresses", "created_at"},
		{"user_addresses", "updated_at"},
	}
	for _, column := range timestampColumns {
		var dataType string
		if err := tx.QueryRow(ctx, `
			SELECT data_type
			FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
		`, column[0], column[1]).Scan(&dataType); err != nil {
			t.Fatalf("inspect %s.%s: %v", column[0], column[1], err)
		}
		if dataType != "timestamp with time zone" {
			t.Errorf("%s.%s type = %s, want timestamp with time zone", column[0], column[1], dataType)
		}
	}

	foreignKeys := []string{
		"fk_credentials_user",
		"fk_sessions_user",
		"fk_user_addresses_user",
	}
	for _, foreignKey := range foreignKeys {
		var deleteAction string
		if err := tx.QueryRow(ctx, `
			SELECT confdeltype::text
			FROM pg_constraint
			WHERE conname = $1
		`, foreignKey).Scan(&deleteAction); err != nil {
			t.Fatalf("inspect foreign key %s: %v", foreignKey, err)
		}
		if deleteAction != "r" {
			t.Errorf("foreign key %s delete action = %q, want RESTRICT", foreignKey, deleteAction)
		}
	}

	var forbiddenUserColumns int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name = 'users'
		  AND column_name IN ('email', 'password', 'password_hash')
	`).Scan(&forbiddenUserColumns); err != nil {
		t.Fatalf("inspect users authentication columns: %v", err)
	}
	if forbiddenUserColumns != 0 {
		t.Errorf("users contains %d authentication columns, want none", forbiddenUserColumns)
	}
}

// TestIdentityMigrationAcceptsValidRows xác nhận một User có credential, session và address hợp lệ có thể được lưu đầy đủ.
func TestIdentityMigrationAcceptsValidRows(t *testing.T) {
	ctx, tx := beginIdentityTest(t)
	userID := insertIdentityTestUser(t, ctx, tx)
	now := time.Now().UTC().Truncate(time.Microsecond)

	if _, err := tx.Exec(ctx, `
		INSERT INTO credentials (
			user_id, email, password_hash, email_verified_at,
			password_changed_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $4, $4, $4)
	`, userID, identityTestEmail(userID), "$argon2id$test-hash", now); err != nil {
		t.Fatalf("insert valid credential: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO sessions (
			id, user_id, ip_address, user_agent, created_at,
			last_activity_at, expires_at, revoked_at
		)
		VALUES ($1, $2, '127.0.0.1', 'identity migration test', $3, $3, $4, NULL)
	`, newIdentityTestUUID(t), userID, now, now.Add(time.Hour)); err != nil {
		t.Fatalf("insert valid session: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_addresses (
			id, user_id, label, recipient_name, recipient_phone,
			address_line1, province, country_code, is_default
		)
		VALUES ($1, $2, 'Home', 'Test User', '0900000000', '1 Test Street', 'Ha Noi', 'VN', true)
	`, newIdentityTestUUID(t), userID); err != nil {
		t.Fatalf("insert valid address: %v", err)
	}
}

// TestIdentityMigrationRejectsInvalidUsers kiểm tra tên, status, soft-delete và chronology của bảng users.
func TestIdentityMigrationRejectsInvalidUsers(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		args    func(t *testing.T) []any
		SQLCode string
	}{
		{
			name:    "blank display name",
			query:   "INSERT INTO users (id, display_name) VALUES ($1, '   ')",
			args:    func(t *testing.T) []any { return []any{newIdentityTestUUID(t)} },
			SQLCode: "23514",
		},
		{
			name:    "unknown status",
			query:   "INSERT INTO users (id, display_name, status) VALUES ($1, 'Test User', 'blocked')",
			args:    func(t *testing.T) []any { return []any{newIdentityTestUUID(t)} },
			SQLCode: "23514",
		},
		{
			name:    "deleted status without deleted timestamp",
			query:   "INSERT INTO users (id, display_name, status) VALUES ($1, 'Test User', 'deleted')",
			args:    func(t *testing.T) []any { return []any{newIdentityTestUUID(t)} },
			SQLCode: "23514",
		},
		{
			name:    "active status with deleted timestamp",
			query:   "INSERT INTO users (id, display_name, status, deleted_at) VALUES ($1, 'Test User', 'active', now())",
			args:    func(t *testing.T) []any { return []any{newIdentityTestUUID(t)} },
			SQLCode: "23514",
		},
		{
			name:    "updated before created",
			query:   "INSERT INTO users (id, display_name, created_at, updated_at) VALUES ($1, 'Test User', now(), now() - interval '1 second')",
			args:    func(t *testing.T) []any { return []any{newIdentityTestUUID(t)} },
			SQLCode: "23514",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, tx := beginIdentityTest(t)
			_, err := tx.Exec(ctx, test.query, test.args(t)...)
			assertIdentitySQLState(t, err, test.SQLCode)
		})
	}
}

// TestIdentityMigrationEnforcesCredentialInvariants kiểm tra email canonical/unique, một credential mỗi User và FK ownership.
func TestIdentityMigrationEnforcesCredentialInvariants(t *testing.T) {
	t.Run("email must be canonical", func(t *testing.T) {
		ctx, tx := beginIdentityTest(t)
		userID := insertIdentityTestUser(t, ctx, tx)
		_, err := tx.Exec(ctx, `
			INSERT INTO credentials (user_id, email, password_hash)
			VALUES ($1, 'User@Example.COM', '$argon2id$test-hash')
		`, userID)
		assertIdentitySQLState(t, err, "23514")
	})

	t.Run("email must be unique", func(t *testing.T) {
		ctx, tx := beginIdentityTest(t)
		firstUserID := insertIdentityTestUser(t, ctx, tx)
		secondUserID := insertIdentityTestUser(t, ctx, tx)
		email := identityTestEmail(firstUserID)

		if _, err := tx.Exec(ctx, `
			INSERT INTO credentials (user_id, email, password_hash)
			VALUES ($1, $2, '$argon2id$first')
		`, firstUserID, email); err != nil {
			t.Fatalf("insert first credential: %v", err)
		}

		_, err := tx.Exec(ctx, `
			INSERT INTO credentials (user_id, email, password_hash)
			VALUES ($1, $2, '$argon2id$second')
		`, secondUserID, email)
		assertIdentitySQLState(t, err, "23505")
	})

	t.Run("one credential per user", func(t *testing.T) {
		ctx, tx := beginIdentityTest(t)
		userID := insertIdentityTestUser(t, ctx, tx)

		if _, err := tx.Exec(ctx, `
			INSERT INTO credentials (user_id, email, password_hash)
			VALUES ($1, $2, '$argon2id$first')
		`, userID, identityTestEmail(userID)); err != nil {
			t.Fatalf("insert first credential: %v", err)
		}

		_, err := tx.Exec(ctx, `
			INSERT INTO credentials (user_id, email, password_hash)
			VALUES ($1, $2, '$argon2id$second')
		`, userID, "second-"+identityTestEmail(userID))
		assertIdentitySQLState(t, err, "23505")
	})

	t.Run("credential requires existing user", func(t *testing.T) {
		ctx, tx := beginIdentityTest(t)
		missingUserID := newIdentityTestUUID(t)
		_, err := tx.Exec(ctx, `
			INSERT INTO credentials (user_id, email, password_hash)
			VALUES ($1, $2, '$argon2id$test-hash')
		`, missingUserID, identityTestEmail(missingUserID))
		assertIdentitySQLState(t, err, "23503")
	})

	t.Run("password hash must not be empty", func(t *testing.T) {
		ctx, tx := beginIdentityTest(t)
		userID := insertIdentityTestUser(t, ctx, tx)
		_, err := tx.Exec(ctx, `
			INSERT INTO credentials (user_id, email, password_hash)
			VALUES ($1, $2, '')
		`, userID, identityTestEmail(userID))
		assertIdentitySQLState(t, err, "23514")
	})
}

// TestIdentityMigrationEnforcesSessionChronology kiểm tra expiration, activity và revocation không thể đi ngược thời gian tạo session.
func TestIdentityMigrationEnforcesSessionChronology(t *testing.T) {
	tests := []struct {
		name           string
		activityOffset time.Duration
		expiresOffset  time.Duration
		revokedOffset  *time.Duration
	}{
		{
			name:           "expiration must be after creation",
			activityOffset: 0,
			expiresOffset:  0,
		},
		{
			name:           "activity must not be before creation",
			activityOffset: -time.Second,
			expiresOffset:  time.Hour,
		},
		{
			name:           "revocation must not be before creation",
			activityOffset: 0,
			expiresOffset:  time.Hour,
			revokedOffset:  identityDurationPointer(-time.Second),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, tx := beginIdentityTest(t)
			userID := insertIdentityTestUser(t, ctx, tx)
			createdAt := time.Now().UTC().Truncate(time.Microsecond)

			var revokedAt any
			if test.revokedOffset != nil {
				revokedAt = createdAt.Add(*test.revokedOffset)
			}

			_, err := tx.Exec(ctx, `
				INSERT INTO sessions (
					id, user_id, created_at, last_activity_at, expires_at, revoked_at
				)
				VALUES ($1, $2, $3, $4, $5, $6)
			`,
				newIdentityTestUUID(t),
				userID,
				createdAt,
				createdAt.Add(test.activityOffset),
				createdAt.Add(test.expiresOffset),
				revokedAt,
			)
			assertIdentitySQLState(t, err, "23514")
		})
	}
}

// TestIdentityMigrationEnforcesAddressInvariants kiểm tra field bắt buộc, country code và tối đa một default address cho mỗi User.
func TestIdentityMigrationEnforcesAddressInvariants(t *testing.T) {
	t.Run("recipient name must not be blank", func(t *testing.T) {
		ctx, tx := beginIdentityTest(t)
		userID := insertIdentityTestUser(t, ctx, tx)
		_, err := tx.Exec(ctx, `
			INSERT INTO user_addresses (
				id, user_id, recipient_name, recipient_phone, address_line1, province
			)
			VALUES ($1, $2, '   ', '0900000000', '1 Test Street', 'Ha Noi')
		`, newIdentityTestUUID(t), userID)
		assertIdentitySQLState(t, err, "23514")
	})

	t.Run("country code must be two uppercase letters", func(t *testing.T) {
		ctx, tx := beginIdentityTest(t)
		userID := insertIdentityTestUser(t, ctx, tx)
		_, err := tx.Exec(ctx, `
			INSERT INTO user_addresses (
				id, user_id, recipient_name, recipient_phone,
				address_line1, province, country_code
			)
			VALUES ($1, $2, 'Test User', '0900000000', '1 Test Street', 'Ha Noi', 'vn')
		`, newIdentityTestUUID(t), userID)
		assertIdentitySQLState(t, err, "23514")
	})

	t.Run("only one default address per user", func(t *testing.T) {
		ctx, tx := beginIdentityTest(t)
		userID := insertIdentityTestUser(t, ctx, tx)

		if _, err := tx.Exec(ctx, `
			INSERT INTO user_addresses (
				id, user_id, recipient_name, recipient_phone,
				address_line1, province, is_default
			)
			VALUES ($1, $2, 'First User', '0900000000', '1 Test Street', 'Ha Noi', true)
		`, newIdentityTestUUID(t), userID); err != nil {
			t.Fatalf("insert first default address: %v", err)
		}

		_, err := tx.Exec(ctx, `
			INSERT INTO user_addresses (
				id, user_id, recipient_name, recipient_phone,
				address_line1, province, is_default
			)
			VALUES ($1, $2, 'Second User', '0900000001', '2 Test Street', 'Ha Noi', true)
		`, newIdentityTestUUID(t), userID)
		assertIdentitySQLState(t, err, "23505")
	})
}

// TestIdentityMigrationConcurrentDefaultAddresses xác nhận partial unique index chỉ cho một request đồng thời tạo default address thành công.
func TestIdentityMigrationConcurrentDefaultAddresses(t *testing.T) {
	ctx, pool := openIdentityTestPool(t)
	userID := newIdentityTestUUID(t)
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, display_name)
		VALUES ($1, 'Concurrent Address Test User')
	`, userID); err != nil {
		t.Fatalf("insert concurrent address test user: %v", err)
	}

	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupContext, "DELETE FROM user_addresses WHERE user_id = $1", userID)
		_, _ = pool.Exec(cleanupContext, "DELETE FROM users WHERE id = $1", userID)
	})

	addressIDs := []string{newIdentityTestUUID(t), newIdentityTestUUID(t)}
	start := make(chan struct{})
	results := make(chan error, len(addressIDs))
	var waitGroup sync.WaitGroup

	for index, addressID := range addressIDs {
		waitGroup.Add(1)
		go func(index int, addressID string) {
			defer waitGroup.Done()
			<-start
			_, err := pool.Exec(ctx, `
				INSERT INTO user_addresses (
					id, user_id, recipient_name, recipient_phone,
					address_line1, province, is_default
				)
				VALUES ($1, $2, $3, '0900000000', $4, 'Ha Noi', true)
			`, addressID, userID, "Concurrent User", strings.Join([]string{"Address", string(rune('A' + index))}, " "))
			results <- err
		}(index, addressID)
	}

	close(start)
	waitGroup.Wait()
	close(results)

	successCount := 0
	uniqueViolationCount := 0
	for err := range results {
		if err == nil {
			successCount++
			continue
		}

		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			uniqueViolationCount++
			continue
		}

		t.Fatalf("concurrent default address returned unexpected error: %v", err)
	}

	if successCount != 1 || uniqueViolationCount != 1 {
		t.Fatalf(
			"concurrent default addresses: successes = %d, unique violations = %d; want 1 and 1",
			successCount,
			uniqueViolationCount,
		)
	}
}

// TestIdentityMigrationRestrictsUserDeletion xác nhận database không cascade-delete credential, session hoặc address khi xóa User vật lý.
func TestIdentityMigrationRestrictsUserDeletion(t *testing.T) {
	tests := []struct {
		name        string
		insertChild func(t *testing.T, ctx context.Context, tx pgx.Tx, userID string)
	}{
		{
			name: "credential",
			insertChild: func(t *testing.T, ctx context.Context, tx pgx.Tx, userID string) {
				t.Helper()
				_, err := tx.Exec(ctx, `
					INSERT INTO credentials (user_id, email, password_hash)
					VALUES ($1, $2, '$argon2id$test-hash')
				`, userID, identityTestEmail(userID))
				if err != nil {
					t.Fatalf("insert credential: %v", err)
				}
			},
		},
		{
			name: "session",
			insertChild: func(t *testing.T, ctx context.Context, tx pgx.Tx, userID string) {
				t.Helper()
				_, err := tx.Exec(ctx, `
					INSERT INTO sessions (id, user_id, expires_at)
					VALUES ($1, $2, now() + interval '1 hour')
				`, newIdentityTestUUID(t), userID)
				if err != nil {
					t.Fatalf("insert session: %v", err)
				}
			},
		},
		{
			name: "address",
			insertChild: func(t *testing.T, ctx context.Context, tx pgx.Tx, userID string) {
				t.Helper()
				_, err := tx.Exec(ctx, `
					INSERT INTO user_addresses (
						id, user_id, recipient_name, recipient_phone, address_line1, province
					)
					VALUES ($1, $2, 'Test User', '0900000000', '1 Test Street', 'Ha Noi')
				`, newIdentityTestUUID(t), userID)
				if err != nil {
					t.Fatalf("insert address: %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, tx := beginIdentityTest(t)
			userID := insertIdentityTestUser(t, ctx, tx)
			test.insertChild(t, ctx, tx, userID)

			_, err := tx.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)
			assertIdentitySQLState(t, err, "23503")
		})
	}
}

// beginIdentityTest mở pool và transaction riêng cho test, sau đó luôn rollback và đóng pool khi test kết thúc.
func beginIdentityTest(t *testing.T) (context.Context, pgx.Tx) {
	t.Helper()

	ctx, pool := openIdentityTestPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin identity test transaction: %v", err)
	}

	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		_ = tx.Rollback(cleanupContext)
	})

	return ctx, tx
}

// openIdentityTestPool mở pgxpool tới TEST_DATABASE_URL và đăng ký đóng pool sau khi test kết thúc.
func openIdentityTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	pool, err := NewPool(ctx, testDatabaseConfig(requireTestDatabaseURL(t)))
	if err != nil {
		cancel()
		t.Fatalf("open identity test database: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		cancel()
	})

	return ctx, pool
}

// insertIdentityTestUser tạo một active User hợp lệ và trả về ID để test các bảng con.
func insertIdentityTestUser(t *testing.T, ctx context.Context, tx pgx.Tx) string {
	t.Helper()

	userID := newIdentityTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, display_name)
		VALUES ($1, 'Identity Test User')
	`, userID); err != nil {
		t.Fatalf("insert identity test user: %v", err)
	}

	return userID
}

// newIdentityTestUUID tạo UUIDv7 với Unix timestamp millisecond để test đúng chiến lược ID mà không thêm dependency mới.
func newIdentityTestUUID(t *testing.T) string {
	t.Helper()

	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		t.Fatalf("generate test UUID: %v", err)
	}

	timestamp := uint64(time.Now().UnixMilli())
	value[0] = byte(timestamp >> 40)
	value[1] = byte(timestamp >> 32)
	value[2] = byte(timestamp >> 24)
	value[3] = byte(timestamp >> 16)
	value[4] = byte(timestamp >> 8)
	value[5] = byte(timestamp)
	value[6] = (value[6] & 0x0f) | 0x70
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])

	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

// identityTestEmail tạo email lowercase duy nhất từ UUID để các test không xung đột dữ liệu.
func identityTestEmail(userID string) string {
	return strings.ReplaceAll(userID, "-", "") + "@example.com"
}

// identityDurationPointer trả con trỏ duration để biểu diễn timestamp tùy chọn trong bảng test case.
func identityDurationPointer(value time.Duration) *time.Duration {
	return &value
}

// assertIdentitySQLState xác nhận PostgreSQL từ chối dữ liệu bằng đúng nhóm lỗi constraint mong đợi.
func assertIdentitySQLState(t *testing.T, err error, expectedCode string) {
	t.Helper()

	if err == nil {
		t.Fatalf("database error = nil, want SQLSTATE %s", expectedCode)
	}

	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		t.Fatalf("database error type = %T, want *pgconn.PgError: %v", err, err)
	}
	if postgresError.Code != expectedCode {
		t.Fatalf("database SQLSTATE = %s, want %s: %v", postgresError.Code, expectedCode, err)
	}
}
