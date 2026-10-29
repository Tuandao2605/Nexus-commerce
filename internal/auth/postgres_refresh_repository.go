// File này hiện thực refresh/session persistence bằng pgx/sqlc và serialize rotation/revocation qua session lock.
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	dbsqlc "nexus-commerce/internal/database/sqlc"
)

type refreshBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

// AuthSessionUserReader là User-owned boundary được gọi trong transaction giữ account status ổn định đến commit.
type AuthSessionUserReader interface {
	IsActiveAuthUser(context.Context, pgx.Tx, string) (bool, error)
}

// PostgresRefreshRepository giữ transaction source và User boundary; application sở hữu pool lifecycle.
type PostgresRefreshRepository struct {
	pool  refreshBeginner
	users AuthSessionUserReader
}

// NewPostgresRefreshRepository inject pool và User-owned reader vào Auth persistence adapter.
func NewPostgresRefreshRepository(pool refreshBeginner, users AuthSessionUserReader) *PostgresRefreshRepository {
	return &PostgresRefreshRepository{pool: pool, users: users}
}

// Transact rollback callback failure và commit success; token pair chỉ được service trả sau kết quả này.
func (r *PostgresRefreshRepository) Transact(ctx context.Context, work func(RefreshTransaction) error) error {
	if r == nil || r.pool == nil || r.users == nil {
		return errors.New("refresh repository is not configured")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin refresh transaction: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	adapter := &postgresRefreshTransaction{tx: tx, queries: dbsqlc.New(tx), users: r.users}
	if err := work(adapter); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit refresh transaction: %w", err)
	}
	return nil
}

type postgresRefreshTransaction struct {
	tx      pgx.Tx
	queries *dbsqlc.Queries
	users   AuthSessionUserReader
}

// refreshUUID chuyển UUID string do application sinh thành pgx argument, giữ nullable predecessor rõ ràng.
func refreshUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if value == "" {
		return id, nil
	}
	err := id.Scan(value)
	return id, err
}

// CreateSession tạo session với absolute deadline bằng database clock để tránh clock skew giữa API instances.
func (p *postgresRefreshTransaction) CreateSession(ctx context.Context, id, user string, lifetime time.Duration) (RefreshSession, error) {
	sessionID, err := refreshUUID(id)
	if err != nil {
		return RefreshSession{}, err
	}
	userID, err := refreshUUID(user)
	if err != nil {
		return RefreshSession{}, err
	}
	row, err := p.queries.CreateAuthSession(ctx, dbsqlc.CreateAuthSessionParams{ID: sessionID, UserID: userID, Lifetime: pgtype.Interval{Microseconds: lifetime.Microseconds(), Valid: true}})
	if err != nil {
		return RefreshSession{}, err
	}
	return refreshSessionFromRow(row), nil
}

// refreshSessionFromRow map persistence model sang session state không chứa driver-specific fields.
func refreshSessionFromRow(row dbsqlc.Session) RefreshSession {
	return RefreshSession{ID: row.ID.String(), UserID: row.UserID.String(), CreatedAt: row.CreatedAt.Time, ExpiresAt: row.ExpiresAt.Time, Revoked: row.RevokedAt.Valid}
}

// LockToken resolve digest, khóa session trước rồi re-read token để concurrent reuse thấy consumed state mới nhất.
func (p *postgresRefreshTransaction) LockToken(ctx context.Context, digest []byte) (RefreshSession, RefreshRecord, error) {
	empty := RefreshSession{}
	token, err := p.queries.GetRefreshTokenByHash(ctx, digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, RefreshRecord{}, ErrInvalidRefreshToken
	}
	if err != nil {
		return empty, RefreshRecord{}, err
	}
	session, err := p.queries.LockAuthSession(ctx, token.SessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, RefreshRecord{}, ErrInvalidRefreshToken
	}
	if err != nil {
		return empty, RefreshRecord{}, err
	}
	token, err = p.queries.LockAuthRefreshToken(ctx, digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, RefreshRecord{}, ErrInvalidRefreshToken
	}
	if err != nil {
		return empty, RefreshRecord{}, err
	}
	return refreshSessionFromRow(session), RefreshRecord{ID: token.ID.String(), SessionID: token.SessionID.String(), Hash: token.TokenHash, CreatedAt: token.CreatedAt.Time, ExpiresAt: token.ExpiresAt.Time, Consumed: token.ConsumedAt.Valid, Revoked: token.RevokedAt.Valid}, nil
}

// CurrentTime đọc thời gian PostgreSQL thực sau khi chờ row lock, không dùng snapshot time đầu transaction.
func (p *postgresRefreshTransaction) CurrentTime(ctx context.Context) (time.Time, error) {
	instant, err := p.queries.AuthDatabaseTime(ctx)
	return instant.Time, err
}

// ActiveUser kiểm tra lifecycle qua User-owned reader với cùng transaction để tránh dùng pool connection khác dưới lock.
func (p *postgresRefreshTransaction) ActiveUser(ctx context.Context, id string) (bool, error) {
	return p.users.IsActiveAuthUser(ctx, p.tx, id)
}

// InsertToken lưu duy nhất digest cùng predecessor và kiểm tra affected-row count của insert có điều kiện.
func (p *postgresRefreshTransaction) InsertToken(ctx context.Context, record RefreshRecord) error {
	id, err := refreshUUID(record.ID)
	if err != nil {
		return err
	}
	sessionID, err := refreshUUID(record.SessionID)
	if err != nil {
		return err
	}
	previous, err := refreshUUID(record.PreviousID)
	if err != nil {
		return err
	}
	rows, err := p.queries.CreateRefreshToken(ctx, dbsqlc.CreateRefreshTokenParams{ID: id, SessionID: sessionID, PreviousTokenID: previous, TokenHash: record.Hash, CreatedAt: pgtype.Timestamptz{Time: record.CreatedAt, Valid: true}, ExpiresAt: pgtype.Timestamptz{Time: record.ExpiresAt, Valid: true}})
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrInvalidRefreshToken
	}
	return nil
}

// Consume chỉ thành công với một live token; session phải đã được caller lock trước khi gọi.
func (p *postgresRefreshTransaction) Consume(ctx context.Context, value string) error {
	id, err := refreshUUID(value)
	if err != nil {
		return err
	}
	rows, err := p.queries.ConsumeRefreshToken(ctx, id)
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrInvalidRefreshToken
	}
	return nil
}

// Touch ghi activity và từ chối commit rotation nếu session đã hết hạn trong lúc xử lý.
func (p *postgresRefreshTransaction) Touch(ctx context.Context, value string) error {
	id, err := refreshUUID(value)
	if err != nil {
		return err
	}
	rows, err := p.queries.TouchAuthSession(ctx, id)
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrInvalidRefreshToken
	}
	return nil
}

// Revoke thu hồi session và toàn bộ token dưới session lock; caller phải commit nhánh reuse trước khi trả 401.
func (p *postgresRefreshTransaction) Revoke(ctx context.Context, value string) error {
	id, err := refreshUUID(value)
	if err != nil {
		return err
	}
	if _, err := p.queries.RevokeAuthSession(ctx, id); err != nil {
		return err
	}
	_, err = p.queries.RevokeSessionRefreshTokens(ctx, id)
	return err
}
