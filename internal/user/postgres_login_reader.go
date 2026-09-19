// File này hiện thực User-owned account-status lookup để Auth kiểm tra lifecycle sau khi password đã đúng.
package user

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbsqlc "nexus-commerce/internal/database/sqlc"
)

// PostgresLoginStatusReader đọc đúng User status cần cho authentication và không trả profile fields khác.
type PostgresLoginStatusReader struct {
	pool    *pgxpool.Pool
	queries *dbsqlc.Queries
}

// NewPostgresLoginStatusReader tạo User-owned status reader với pool lifecycle bên ngoài.
func NewPostgresLoginStatusReader(pool *pgxpool.Pool) *PostgresLoginStatusReader {
	if pool == nil {
		return &PostgresLoginStatusReader{}
	}
	return &PostgresLoginStatusReader{pool: pool, queries: dbsqlc.New(pool)}
}

// FindLoginUserStatus trả account lifecycle status hoặc found=false nếu User không tồn tại.
func (r *PostgresLoginStatusReader) FindLoginUserStatus(ctx context.Context, userID string) (string, bool, error) {
	if r == nil || r.pool == nil || r.queries == nil {
		return "", false, fmt.Errorf("login user status reader is not configured")
	}

	var parsedUserID pgtype.UUID
	if err := parsedUserID.Scan(userID); err != nil {
		return "", false, fmt.Errorf("parse login user ID: %w", err)
	}

	status, err := r.queries.FindLoginUserStatus(ctx, parsedUserID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", false, nil
		}
		return "", false, fmt.Errorf("find login user status: %w", err)
	}

	return status, true, nil
}
