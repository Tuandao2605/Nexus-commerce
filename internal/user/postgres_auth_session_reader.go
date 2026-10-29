// File này giữ User-owned status check trong cùng transaction phát session/refresh token của Auth.
package user

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	dbsqlc "nexus-commerce/internal/database/sqlc"
)

// PostgresAuthSessionReader đọc account status dưới row lock để thay đổi lifecycle không vượt qua commit.
type PostgresAuthSessionReader struct{}

// NewPostgresAuthSessionReader tạo reader không giữ pool hay mutable state.
func NewPostgresAuthSessionReader() *PostgresAuthSessionReader { return &PostgresAuthSessionReader{} }

// IsActiveAuthUser kiểm tra User active trong transaction do Auth cung cấp, chỉ đọc User-owned data.
func (r *PostgresAuthSessionReader) IsActiveAuthUser(ctx context.Context, tx pgx.Tx, id string) (bool, error) {
	var userID pgtype.UUID
	if err := userID.Scan(id); err != nil {
		return false, err
	}
	status, err := dbsqlc.New(tx).LockAuthUserStatus(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return status == "active" && err == nil, err
}
