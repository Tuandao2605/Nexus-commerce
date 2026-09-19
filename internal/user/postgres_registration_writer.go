// Package user sở hữu profile User; file này cung cấp thao tác tạo profile tối thiểu trong registration transaction.
package user

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbsqlc "nexus-commerce/internal/database/sqlc"
)

// PostgresRegistrationWriter ghi dữ liệu do User module sở hữu bằng transaction do registration orchestrator truyền vào.
type PostgresRegistrationWriter struct{}

// NewPostgresRegistrationWriter tạo User-owned writer không giữ mutable state hoặc lifecycle tài nguyên.
func NewPostgresRegistrationWriter() *PostgresRegistrationWriter {
	return &PostgresRegistrationWriter{}
}

// CreateRegistrationUser tạo profile User tối thiểu trong cùng pgx transaction với Credential của Auth.
func (w *PostgresRegistrationWriter) CreateRegistrationUser(
	ctx context.Context,
	tx pgx.Tx,
	userID pgtype.UUID,
	displayName string,
) error {
	return dbsqlc.New(tx).CreateRegistrationUser(ctx, dbsqlc.CreateRegistrationUserParams{
		ID:          userID,
		DisplayName: displayName,
	})
}
