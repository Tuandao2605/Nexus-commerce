// File này hiện thực Auth-owned login Credential lookup và compare-and-swap password rehash bằng pgx/sqlc.
package auth

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbsqlc "nexus-commerce/internal/database/sqlc"
)

// PostgresLoginRepository đọc và nâng cấp Auth Credential trong PostgreSQL.
type PostgresLoginRepository struct {
	pool    *pgxpool.Pool
	queries *dbsqlc.Queries
}

// NewPostgresLoginRepository tạo login repository dùng pool do application bootstrap sở hữu lifecycle.
func NewPostgresLoginRepository(pool *pgxpool.Pool) *PostgresLoginRepository {
	if pool == nil {
		return &PostgresLoginRepository{}
	}
	return &PostgresLoginRepository{pool: pool, queries: dbsqlc.New(pool)}
}

// FindByEmail lookup canonical email qua unique index và không biến database failure thành invalid-credential giả.
func (r *PostgresLoginRepository) FindByEmail(ctx context.Context, email string) (LoginCredential, bool, error) {
	if r == nil || r.pool == nil || r.queries == nil {
		return LoginCredential{}, false, ErrLoginUnavailable
	}

	row, err := r.queries.FindLoginCredentialByEmail(ctx, email)
	if err != nil {
		if err == pgx.ErrNoRows {
			return LoginCredential{}, false, nil
		}
		return LoginCredential{}, false, fmt.Errorf("find login credential by email: %w", err)
	}

	return LoginCredential{
		UserID:       row.UserID,
		Email:        row.Email,
		PasswordHash: row.PasswordHash,
	}, true, nil
}

// UpgradePasswordHash update hash khi user ID và old hash cùng khớp, giữ nguyên password_changed_at vì password không đổi.
func (r *PostgresLoginRepository) UpgradePasswordHash(
	ctx context.Context,
	userID string,
	oldHash string,
	newHash string,
) (bool, error) {
	if r == nil || r.pool == nil || r.queries == nil {
		return false, ErrLoginUnavailable
	}

	var parsedUserID pgtype.UUID
	if err := parsedUserID.Scan(userID); err != nil {
		return false, fmt.Errorf("parse login user ID: %w", err)
	}

	rowsAffected, err := r.queries.UpgradeLoginPasswordHash(ctx, dbsqlc.UpgradeLoginPasswordHashParams{
		NewPasswordHash: newHash,
		UserID:          parsedUserID,
		OldPasswordHash: oldHash,
	})
	if err != nil {
		return false, fmt.Errorf("upgrade login password hash: %w", err)
	}

	return rowsAffected == 1, nil
}
