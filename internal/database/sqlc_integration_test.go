// File này kiểm thử generated sqlc package bằng PostgreSQL thật, gồm pool, transaction WithTx và context cancellation.
package database

import (
	"context"
	"errors"
	"testing"
	"time"

	dbsqlc "nexus-commerce/internal/database/sqlc"
)

// TestSQLCGeneratedDatabasePing xác nhận query được sinh bởi sqlc chạy qua pgxpool và trả đúng kiểu int64.
func TestSQLCGeneratedDatabasePing(t *testing.T) {
	ctx, pool := openMigrationTestPool(t)
	queries := dbsqlc.New(pool)

	value, err := queries.DatabasePing(ctx)
	if err != nil {
		t.Fatalf("run generated DatabasePing query: %v", err)
	}
	if value != 1 {
		t.Fatalf("DatabasePing value = %d, want 1", value)
	}
}

// TestSQLCGeneratedQueriesSupportTransactions xác nhận Queries.WithTx dùng cùng pgx.Tx cho unit of work do caller quản lý.
func TestSQLCGeneratedQueriesSupportTransactions(t *testing.T) {
	ctx, pool := openMigrationTestPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin sqlc integration transaction: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		_ = tx.Rollback(cleanupContext)
	})

	queries := dbsqlc.New(pool).WithTx(tx)
	value, err := queries.DatabasePing(ctx)
	if err != nil {
		t.Fatalf("run generated DatabasePing with transaction: %v", err)
	}
	if value != 1 {
		t.Fatalf("transactional DatabasePing value = %d, want 1", value)
	}
}

// TestSQLCGeneratedQueryHonorsContextCancellation xác nhận request context bị hủy được truyền đến pgx thay vì bị thay bằng context nền.
func TestSQLCGeneratedQueryHonorsContextCancellation(t *testing.T) {
	ctx, pool := openMigrationTestPool(t)
	queries := dbsqlc.New(pool)
	queryContext, cancelQuery := context.WithCancel(ctx)
	cancelQuery()

	_, err := queries.DatabasePing(queryContext)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DatabasePing error = %v, want context.Canceled", err)
	}
}
