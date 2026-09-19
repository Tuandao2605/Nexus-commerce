// File này hiện thực registration persistence bằng pgx/sqlc và bảo đảm User cùng Credential commit hoặc rollback cùng nhau.
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbsqlc "nexus-commerce/internal/database/sqlc"
)

const registrationRollbackTimeout = 3 * time.Second

// PostgresRegistrationRepository lưu registration vào PostgreSQL thông qua generated sqlc queries.
type PostgresRegistrationRepository struct {
	pool       *pgxpool.Pool
	userWriter RegistrationUserWriter
}

// RegistrationUserWriter là boundary để Auth yêu cầu User module tạo profile trong caller-owned transaction.
type RegistrationUserWriter interface {
	// CreateRegistrationUser chỉ ghi bảng do User module sở hữu và không tự commit transaction.
	CreateRegistrationUser(ctx context.Context, tx pgx.Tx, userID pgtype.UUID, displayName string) error
}

// NewPostgresRegistrationRepository tạo repository với pool lifecycle bên ngoài và User-owned writer được inject.
func NewPostgresRegistrationRepository(
	pool *pgxpool.Pool,
	userWriter RegistrationUserWriter,
) *PostgresRegistrationRepository {
	return &PostgresRegistrationRepository{
		pool:       pool,
		userWriter: userWriter,
	}
}

// Register tạo User trước rồi Credential trong cùng transaction, dựa vào UNIQUE(email) để xử lý race an toàn.
func (r *PostgresRegistrationRepository) Register(ctx context.Context, record RegistrationRecord) error {
	if r == nil || r.pool == nil || r.userWriter == nil {
		return ErrRegistrationUnavailable
	}

	var userID pgtype.UUID
	if err := userID.Scan(record.UserID); err != nil {
		return fmt.Errorf("parse registration user ID: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin registration transaction: %w", err)
	}
	defer rollbackRegistration(tx, ctx)

	if err := r.userWriter.CreateRegistrationUser(ctx, tx, userID, record.DisplayName); err != nil {
		return fmt.Errorf("create registration user: %w", err)
	}

	queries := dbsqlc.New(tx)
	if err := queries.CreateRegistrationCredential(ctx, dbsqlc.CreateRegistrationCredentialParams{
		UserID:       userID,
		Email:        record.Email,
		PasswordHash: record.PasswordHash,
	}); err != nil {
		return mapRegistrationPersistenceError("create registration credential", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return mapRegistrationPersistenceError("commit registration transaction", err)
	}

	return nil
}

// rollbackRegistration giải phóng transaction chưa commit bằng context cleanup có timeout kể cả khi request đã bị hủy.
func rollbackRegistration(tx pgx.Tx, requestContext context.Context) {
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(requestContext), registrationRollbackTimeout)
	defer cancel()
	_ = tx.Rollback(cleanupContext)
}

// mapRegistrationPersistenceError chuyển đúng unique constraint email thành domain conflict và bọc kín lỗi SQL còn lại.
func mapRegistrationPersistenceError(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_credentials_email" {
		return ErrEmailAlreadyRegistered
	}

	return fmt.Errorf("%s: %w", operation, err)
}
