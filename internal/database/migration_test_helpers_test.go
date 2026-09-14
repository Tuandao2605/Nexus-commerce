// File này cung cấp helper dùng chung cho các integration test migration PostgreSQL của mọi domain.
package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// beginMigrationTest mở pool và transaction riêng, sau đó luôn rollback và đóng tài nguyên khi test kết thúc.
func beginMigrationTest(t *testing.T) (context.Context, pgx.Tx) {
	t.Helper()

	ctx, pool := openMigrationTestPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin migration test transaction: %v", err)
	}

	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		_ = tx.Rollback(cleanupContext)
	})

	return ctx, tx
}

// openMigrationTestPool kết nối TEST_DATABASE_URL bằng cấu hình test và đăng ký đóng pool khi test kết thúc.
func openMigrationTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	pool, err := NewPool(ctx, testDatabaseConfig(requireTestDatabaseURL(t)))
	if err != nil {
		cancel()
		t.Fatalf("open migration test database: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		cancel()
	})

	return ctx, pool
}

// newMigrationTestUUID tạo UUIDv7 với Unix timestamp millisecond để test đúng chiến lược ID mà không thêm dependency mới.
func newMigrationTestUUID(t *testing.T) string {
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

// assertMigrationSQLState xác nhận PostgreSQL từ chối dữ liệu bằng đúng SQLSTATE của invariant đang kiểm tra.
func assertMigrationSQLState(t *testing.T, err error, expectedCode string) {
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
