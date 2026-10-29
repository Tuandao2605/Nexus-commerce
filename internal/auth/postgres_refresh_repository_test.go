// File này kiểm thử token-pair workflow trên PostgreSQL thật: transaction rollback, reuse revocation, isolation và lock contention.
package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	usermodule "nexus-commerce/internal/user"
)

type refreshPostgresFixture struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	repo     *PostgresRefreshRepository
	service  *RefreshService
	identity AuthenticatedIdentity
}

type refreshTestIssuer func(AuthenticatedIdentity) (IssuedAccessToken, error)

// Issue cho phép inject signing failure mà không cần private key hay log token material.
func (f refreshTestIssuer) Issue(identity AuthenticatedIdentity) (IssuedAccessToken, error) {
	return f(identity)
}

// newRefreshPostgresFixture tạo User riêng, service thật và cleanup đúng FK order, không đụng dữ liệu ngoài test.
func newRefreshPostgresFixture(t *testing.T) refreshPostgresFixture {
	t.Helper()
	ctx, pool := openRegistrationTestPool(t)
	record := newRegistrationTestRecord(t, "rotation")
	cleanupRegistrationRecords(t, pool, record.UserID)
	if err := newPostgresRegistrationTestRepository(pool).Register(ctx, record); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE session_id IN (SELECT id FROM sessions WHERE user_id=$1)`, record.UserID); err != nil {
			t.Errorf("cleanup refresh history: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, record.UserID); err != nil {
			t.Errorf("cleanup sessions: %v", err)
		}
	})
	repo := NewPostgresRefreshRepository(pool, usermodule.NewPostgresAuthSessionReader())
	issuer := refreshTestIssuer(func(AuthenticatedIdentity) (IssuedAccessToken, error) {
		return IssuedAccessToken{Value: "signed-test-token", TokenType: "Bearer", ExpiresIn: 900}, nil
	})
	service, err := NewRefreshService(repo, issuer, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return refreshPostgresFixture{ctx: ctx, pool: pool, repo: repo, service: service, identity: AuthenticatedIdentity{UserID: record.UserID}}
}

// issue tạo session ban đầu và yêu cầu response đầy đủ credential metadata.
func (f refreshPostgresFixture) issue(t *testing.T) IssuedTokenPair {
	t.Helper()
	pair, err := f.service.IssueLogin(f.ctx, f.identity)
	if err != nil {
		t.Fatalf("IssueLogin: %v", err)
	}
	if pair.Value == "" || pair.RefreshToken == "" || pair.RefreshExpiresIn < 3590 || pair.RefreshExpiresIn > 3600 {
		t.Fatal("initial token pair is incomplete")
	}
	return pair
}

// tokenSession resolve session bằng digest giống client request, chỉ dùng cho test assertions.
func (f refreshPostgresFixture) tokenSession(t *testing.T, raw string) string {
	t.Helper()
	hash := sha256.Sum256([]byte(raw))
	var session string
	if err := f.pool.QueryRow(f.ctx, `SELECT session_id::text FROM refresh_tokens WHERE token_hash=$1`, hash[:]).Scan(&session); err != nil {
		t.Fatal(err)
	}
	return session
}

// TestPostgresRefreshLifecycleAndReuse kiểm tra initial issuance, lineage, fixed deadline, expired-parent replay và isolation theo device.
func TestPostgresRefreshLifecycleAndReuse(t *testing.T) {
	f := newRefreshPostgresFixture(t)
	initial := f.issue(t)
	independent := f.issue(t)
	hash := sha256.Sum256([]byte(initial.RefreshToken))
	var stored []byte
	var id, session string
	var expiry, originalActivity time.Time
	if err := f.pool.QueryRow(f.ctx, `
  SELECT r.id::text, r.session_id::text, r.token_hash, r.expires_at, s.last_activity_at
  FROM refresh_tokens r JOIN sessions s ON s.id=r.session_id WHERE r.token_hash=$1
 `, hash[:]).Scan(&id, &session, &stored, &expiry, &originalActivity); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, hash[:]) || bytes.Equal(stored, []byte(initial.RefreshToken)) {
		t.Fatal("raw credential persisted instead of digest")
	}
	next, err := f.service.Refresh(f.ctx, initial.RefreshToken)
	if err != nil || next.RefreshToken == initial.RefreshToken {
		t.Fatalf("rotation failed: %v", err)
	}
	nextHash := sha256.Sum256([]byte(next.RefreshToken))
	var predecessor, childSession string
	var childExpiry, activity time.Time
	var consumed bool
	if err := f.pool.QueryRow(f.ctx, `
  SELECT r.previous_token_id::text, r.session_id::text, r.expires_at, p.consumed_at IS NOT NULL, s.last_activity_at
  FROM refresh_tokens r JOIN refresh_tokens p ON p.id=r.previous_token_id JOIN sessions s ON s.id=r.session_id WHERE r.token_hash=$1
 `, nextHash[:]).Scan(&predecessor, &childSession, &childExpiry, &consumed, &activity); err != nil {
		t.Fatal(err)
	}
	if predecessor != id || childSession != session || !childExpiry.Equal(expiry) || !consumed || !activity.After(originalActivity) || next.RefreshExpiresIn > initial.RefreshExpiresIn {
		t.Fatal("rotation lineage, deadline or activity invariant failed")
	}
	// Parent đã hết hạn vẫn phải giữ reuse detection để không để live descendant sống sót.
	if _, err := f.pool.Exec(f.ctx, `UPDATE refresh_tokens SET created_at=clock_timestamp()-interval '2 hours', expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	assertInvalidRefresh(t, f, initial.RefreshToken)
	var revoked bool
	var live int
	if err := f.pool.QueryRow(f.ctx, `SELECT revoked_at IS NOT NULL FROM sessions WHERE id=$1`, session).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM refresh_tokens WHERE session_id=$1 AND revoked_at IS NULL`, session).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if !revoked || live != 0 {
		t.Fatal("reuse returned 401 without committing session-wide revocation")
	}
	assertInvalidRefresh(t, f, next.RefreshToken)
	if _, err := f.service.Refresh(f.ctx, independent.RefreshToken); err != nil {
		t.Fatalf("unrelated device session was revoked: %v", err)
	}
}

// assertInvalidRefresh yêu cầu không có token material khi authentication bị từ chối.
func assertInvalidRefresh(t *testing.T, f refreshPostgresFixture, raw string) {
	t.Helper()
	pair, err := f.service.Refresh(f.ctx, raw)
	if !errors.Is(err, ErrInvalidRefreshToken) || pair != (IssuedTokenPair{}) {
		t.Fatalf("expected generic rejection and empty pair; err=%v", err)
	}
}

// TestPostgresRefreshRejectsInvalidState chứng minh expired/revoked token/session và inactive Users không rotate.
func TestPostgresRefreshRejectsInvalidState(t *testing.T) {
	cases := []struct{ name, mutation string }{
		{"expired token", `UPDATE refresh_tokens SET created_at=clock_timestamp()-interval '2 hours', expires_at=clock_timestamp()-interval '1 second' WHERE session_id=$1`},
		{"revoked token", `UPDATE refresh_tokens SET revoked_at=clock_timestamp() WHERE session_id=$1`},
		{"expired session", `UPDATE sessions SET created_at=clock_timestamp()-interval '2 hours', expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`},
		{"revoked session", `UPDATE sessions SET revoked_at=clock_timestamp() WHERE id=$1`},
		{"suspended user", `UPDATE users SET status='suspended' WHERE id=(SELECT user_id FROM sessions WHERE id=$1)`},
		{"deleted user", `UPDATE users SET status='deleted', deleted_at=clock_timestamp() WHERE id=(SELECT user_id FROM sessions WHERE id=$1)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRefreshPostgresFixture(t)
			pair := f.issue(t)
			session := f.tokenSession(t, pair.RefreshToken)
			if _, err := f.pool.Exec(f.ctx, tc.mutation, session); err != nil {
				t.Fatal(err)
			}
			assertInvalidRefresh(t, f, pair.RefreshToken)
			var count int
			if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM refresh_tokens WHERE session_id=$1`, session).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatal("invalid state created successor")
			}
		})
	}
	f := newRefreshPostgresFixture(t)
	unknown, _, err := f.service.newCredential()
	if err != nil {
		t.Fatal(err)
	}
	assertInvalidRefresh(t, f, unknown)
	if pair, err := f.service.IssueLogin(f.ctx, AuthenticatedIdentity{UserID: mustRefreshTokenTestUUID(t).String()}); !errors.Is(err, ErrInvalidCredentials) || pair != (IssuedTokenPair{}) {
		t.Fatal("missing User received credentials")
	}
}

// TestPostgresRefreshConcurrentReuse có hai client dùng cùng credential, chỉ một successor và loser commit session revocation.
func TestPostgresRefreshConcurrentReuse(t *testing.T) {
	f := newRefreshPostgresFixture(t)
	pair := f.issue(t)
	session := f.tokenSession(t, pair.RefreshToken)
	type result struct {
		pair IssuedTokenPair
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			next, err := f.service.Refresh(f.ctx, pair.RefreshToken)
			results <- result{next, err}
		}()
	}
	close(start)
	group.Wait()
	close(results)
	winners, rejected := 0, 0
	var winner IssuedTokenPair
	for r := range results {
		if r.err == nil {
			winners++
			winner = r.pair
		} else if errors.Is(r.err, ErrInvalidRefreshToken) {
			rejected++
		} else {
			t.Fatalf("unexpected concurrent error: %v", r.err)
		}
	}
	if winners != 1 || rejected != 1 {
		t.Fatalf("winners=%d rejected=%d", winners, rejected)
	}
	var total, revoked int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*), count(*) FILTER (WHERE revoked_at IS NOT NULL) FROM refresh_tokens WHERE session_id=$1`, session).Scan(&total, &revoked); err != nil {
		t.Fatal(err)
	}
	if total != 2 || revoked != 2 {
		t.Fatalf("history=%d revoked=%d, want 2/2", total, revoked)
	}
	assertInvalidRefresh(t, f, winner.RefreshToken)
}

type refreshFaultRepository struct {
	base  RefreshRepository
	stage string
}
type refreshFaultTransaction struct {
	RefreshTransaction
	stage string
}

// Transact inject lỗi vào callback thật để PostgreSQL phải rollback state đã ghi.
func (r refreshFaultRepository) Transact(ctx context.Context, work func(RefreshTransaction) error) error {
	return r.base.Transact(ctx, func(tx RefreshTransaction) error { return work(refreshFaultTransaction{tx, r.stage}) })
}

// InsertToken báo lỗi sau write để test rollback cả predecessor consume và successor insert.
func (tx refreshFaultTransaction) InsertToken(ctx context.Context, record RefreshRecord) error {
	if err := tx.RefreshTransaction.InsertToken(ctx, record); err != nil {
		return err
	}
	if tx.stage == "insert" {
		return errors.New("injected insert failure")
	}
	return nil
}

// Consume báo lỗi sau conditional update để chứng minh consumed_at không bị giữ lại sau rollback.
func (tx refreshFaultTransaction) Consume(ctx context.Context, id string) error {
	if err := tx.RefreshTransaction.Consume(ctx, id); err != nil {
		return err
	}
	if tx.stage == "consume" {
		return errors.New("injected consume failure")
	}
	return nil
}

// Touch báo lỗi sau activity write để test rollback toàn bộ transaction.
func (tx refreshFaultTransaction) Touch(ctx context.Context, id string) error {
	if err := tx.RefreshTransaction.Touch(ctx, id); err != nil {
		return err
	}
	if tx.stage == "touch" {
		return errors.New("injected touch failure")
	}
	return nil
}

// Revoke báo lỗi sau security writes để kiểm tra reuse failure không được báo giả thành authentication rejection.
func (tx refreshFaultTransaction) Revoke(ctx context.Context, id string) error {
	if err := tx.RefreshTransaction.Revoke(ctx, id); err != nil {
		return err
	}
	if tx.stage == "revoke" {
		return errors.New("injected revoke failure")
	}
	return nil
}

type refreshCommitFaultPool struct{ pool *pgxpool.Pool }
type refreshCommitFaultTx struct{ pgx.Tx }

// Begin dùng PostgreSQL transaction thật nhưng thay Commit để kiểm tra commit error path của repository.
func (p refreshCommitFaultPool) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return refreshCommitFaultTx{tx}, nil
}

// Commit giả lập commit thất bại trước khi gửi COMMIT; repository phải rollback và service không lộ token.
func (tx refreshCommitFaultTx) Commit(context.Context) error {
	return errors.New("injected commit failure")
}

// TestPostgresRefreshRollbackFailures kiểm tra RNG/signing/ID/consume/insert/touch/commit failure với assertions trên dữ liệu thật.
func TestPostgresRefreshRollbackFailures(t *testing.T) {
	for _, stage := range []string{"rng", "sign", "id", "consume", "insert", "touch", "commit"} {
		t.Run(stage, func(t *testing.T) {
			f := newRefreshPostgresFixture(t)
			pair := f.issue(t)
			session := f.tokenSession(t, pair.RefreshToken)
			var activity time.Time
			if err := f.pool.QueryRow(f.ctx, `SELECT last_activity_at FROM sessions WHERE id=$1`, session).Scan(&activity); err != nil {
				t.Fatal(err)
			}
			f.service.repository = refreshFaultRepository{base: f.repo, stage: stage}
			switch stage {
			case "rng":
				f.service.random = strings.NewReader("")
			case "id":
				f.service.newID = func() (string, error) { return "", errors.New("injected ID failure") }
			case "sign":
				f.service.issuer = refreshTestIssuer(func(AuthenticatedIdentity) (IssuedAccessToken, error) {
					return IssuedAccessToken{}, errors.New("injected signing failure")
				})
			case "commit":
				f.service.repository = NewPostgresRefreshRepository(refreshCommitFaultPool{f.pool}, usermodule.NewPostgresAuthSessionReader())
			}
			result, err := f.service.Refresh(f.ctx, pair.RefreshToken)
			if err == nil || result != (IssuedTokenPair{}) {
				t.Fatal("failure returned credential material")
			}
			var total, consumed int
			var after time.Time
			if err := f.pool.QueryRow(f.ctx, `SELECT count(*), count(*) FILTER (WHERE consumed_at IS NOT NULL) FROM refresh_tokens WHERE session_id=$1`, session).Scan(&total, &consumed); err != nil {
				t.Fatal(err)
			}
			if err := f.pool.QueryRow(f.ctx, `SELECT last_activity_at FROM sessions WHERE id=$1`, session).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if total != 1 || consumed != 0 || !after.Equal(activity) {
				t.Fatal("failed rotation left partial database changes")
			}
		})
	}
}

// TestPostgresRefreshLoginRollback kiểm tra initial session/token không persist nếu signing/insert/commit lỗi.
func TestPostgresRefreshLoginRollback(t *testing.T) {
	for _, stage := range []string{"sign", "insert", "commit"} {
		t.Run(stage, func(t *testing.T) {
			f := newRefreshPostgresFixture(t)
			if stage == "sign" {
				f.service.issuer = refreshTestIssuer(func(AuthenticatedIdentity) (IssuedAccessToken, error) {
					return IssuedAccessToken{}, errors.New("sign failed")
				})
			}
			if stage == "insert" {
				f.service.repository = refreshFaultRepository{f.repo, stage}
			}
			if stage == "commit" {
				f.service.repository = NewPostgresRefreshRepository(refreshCommitFaultPool{f.pool}, usermodule.NewPostgresAuthSessionReader())
			}
			pair, err := f.service.IssueLogin(f.ctx, f.identity)
			if err == nil || pair != (IssuedTokenPair{}) {
				t.Fatal("failed login issuance leaked pair")
			}
			var count int
			if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM sessions WHERE user_id=$1`, f.identity.UserID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatal("failed login persisted session")
			}
		})
	}
}

// TestPostgresRefreshHTTPLoginToRotation kiểm tra HTTP → password auth → session transaction → signed JWT → refresh thực tế.
func TestPostgresRefreshHTTPLoginToRotation(t *testing.T) {
	f := newRefreshPostgresFixture(t)
	hasher := newDefaultArgon2idTestHasher(t)
	hash, err := hasher.Hash("correct password")
	if err != nil {
		t.Fatal(err)
	}
	var email string
	if err := f.pool.QueryRow(f.ctx, `UPDATE credentials SET password_hash=$1 WHERE user_id=$2 RETURNING email`, hash, f.identity.UserID).Scan(&email); err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := NewRS256AccessTokenManager(pemEncodePrivateKey(t, key), testAccessTokenConfig())
	if err != nil {
		t.Fatal(err)
	}
	f.service.issuer = issuer
	loginService := newPostgresLoginTestService(t, f.pool, hasher)
	login, err := NewLoginTokenService(loginService, f.service)
	if err != nil {
		t.Fatal(err)
	}
	handler := newLoginTestHandler(login)
	failed := performLoginRequest(handler, fmt.Sprintf(`{"email":%q,"password":"wrong"}`, email), "application/json")
	assertLoginErrorResponse(t, failed, 401, "auth.invalid_credentials")
	var count int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM sessions WHERE user_id=$1`, f.identity.UserID).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed authentication created session")
	}
	response := performLoginRequest(handler, fmt.Sprintf(`{"email":%q,"password":"correct password"}`, email), "application/json")
	if response.Code != 200 {
		t.Fatalf("login status=%d", response.Code)
	}
	var initial loginHTTPResponse
	if err := json.NewDecoder(response.Body).Decode(&initial); err != nil {
		t.Fatal(err)
	}
	refreshHTTP := NewRefreshHandler(f.service, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rotated := performLoginRequest(refreshHTTP, fmt.Sprintf(`{"refresh_token":%q}`, initial.RefreshToken), "application/json")
	if rotated.Code != 200 {
		t.Fatalf("refresh status=%d", rotated.Code)
	}
	var next loginHTTPResponse
	if err := json.NewDecoder(rotated.Body).Decode(&next); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{initial.AccessToken, next.AccessToken} {
		claims, err := issuer.Validate(token)
		if err != nil || claims.Subject != f.identity.UserID {
			t.Fatalf("issued access JWT invalid: %v", err)
		}
	}
	if initial.AccessToken == next.AccessToken || initial.RefreshToken == next.RefreshToken {
		t.Fatal("refresh reused credential material")
	}
	reused := performLoginRequest(refreshHTTP, fmt.Sprintf(`{"refresh_token":%q}`, initial.RefreshToken), "application/json")
	assertLoginErrorResponse(t, reused, 401, "auth.invalid_refresh_token")
}

type refreshLockObserverPool struct {
	pool    *pgxpool.Pool
	locking chan struct{}
}
type refreshLockObserverTx struct {
	pgx.Tx
	locking chan struct{}
}

// Begin giữ transaction thật và chỉ thêm tín hiệu để test biết rotation đã tới session lock.
func (p refreshLockObserverPool) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return refreshLockObserverTx{tx, p.locking}, nil
}

// QueryRow báo thời điểm trước session lock wait mà không thay đổi query hay transaction semantics.
func (tx refreshLockObserverTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "-- name: LockAuthSession") {
		close(tx.locking)
	}
	return tx.Tx.QueryRow(ctx, sql, args...)
}

// TestPostgresRefreshExpiresDuringLockWait chứng minh DB clock sau lock từ chối token hết hạn khi transaction đã bắt đầu từ trước.
func TestPostgresRefreshExpiresDuringLockWait(t *testing.T) {
	f := newRefreshPostgresFixture(t)
	pair := f.issue(t)
	session := f.tokenSession(t, pair.RefreshToken)
	hash := sha256.Sum256([]byte(pair.RefreshToken))
	var expiry time.Time
	if err := f.pool.QueryRow(f.ctx, `UPDATE refresh_tokens SET expires_at=clock_timestamp()+interval '400 milliseconds' WHERE token_hash=$1 RETURNING expires_at`, hash[:]).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	blocker, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(f.ctx, `SELECT id FROM sessions WHERE id=$1 FOR UPDATE`, session); err != nil {
		t.Fatal(err)
	}
	locking := make(chan struct{})
	f.service.repository = NewPostgresRefreshRepository(refreshLockObserverPool{f.pool, locking}, usermodule.NewPostgresAuthSessionReader())
	type outcome struct {
		pair IssuedTokenPair
		err  error
	}
	done := make(chan outcome, 1)
	go func() { pair, err := f.service.Refresh(f.ctx, pair.RefreshToken); done <- outcome{pair, err} }()
	select {
	case <-locking:
	case <-f.ctx.Done():
		t.Fatal("rotation never reached session lock")
	}
	// Bounded database wait crosses the exact expiry; no reliance on a guessed scheduler delay.
	if _, err := blocker.Exec(f.ctx, `SELECT pg_sleep(GREATEST(0, EXTRACT(EPOCH FROM ($1::timestamptz-clock_timestamp()))) + 0.02)`, expiry); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if !errors.Is(result.err, ErrInvalidRefreshToken) || result.pair != (IssuedTokenPair{}) {
		t.Fatalf("expired lock waiter received credentials: %v", result.err)
	}
	var count int
	var consumed bool
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*), bool_or(consumed_at IS NOT NULL) FROM refresh_tokens WHERE session_id=$1`, session).Scan(&count, &consumed); err != nil {
		t.Fatal(err)
	}
	if count != 1 || consumed {
		t.Fatal("expired lock waiter changed token history")
	}
}

// TestPostgresRefreshReuseFailureRollsBackAndSurfacesInfrastructureError không che revoke/commit lỗi thành 401 giả.
func TestPostgresRefreshReuseFailureRollsBackAndSurfacesInfrastructureError(t *testing.T) {
	for _, stage := range []string{"revoke", "commit"} {
		t.Run(stage, func(t *testing.T) {
			f := newRefreshPostgresFixture(t)
			initial := f.issue(t)
			session := f.tokenSession(t, initial.RefreshToken)
			if _, err := f.service.Refresh(f.ctx, initial.RefreshToken); err != nil {
				t.Fatal(err)
			}
			if stage == "revoke" {
				f.service.repository = refreshFaultRepository{f.repo, stage}
			} else {
				f.service.repository = NewPostgresRefreshRepository(refreshCommitFaultPool{f.pool}, usermodule.NewPostgresAuthSessionReader())
			}
			pair, err := f.service.Refresh(f.ctx, initial.RefreshToken)
			if err == nil || errors.Is(err, ErrInvalidRefreshToken) || pair != (IssuedTokenPair{}) {
				t.Fatal("revocation failure hidden as successful security rejection")
			}
			var revoked bool
			var count int
			if err := f.pool.QueryRow(f.ctx, `SELECT revoked_at IS NOT NULL FROM sessions WHERE id=$1`, session).Scan(&revoked); err != nil {
				t.Fatal(err)
			}
			if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM refresh_tokens WHERE session_id=$1 AND revoked_at IS NOT NULL`, session).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if revoked || count != 0 {
				t.Fatal("failed security transaction partially committed")
			}
			f.service.repository = f.repo
			assertInvalidRefresh(t, f, initial.RefreshToken)
		})
	}
}

// TestPostgresRefreshRevocationRacesSuccessorRotation chứng minh session lock bảo vệ cả token con khi reuse và rotation khác cạnh tranh.
func TestPostgresRefreshRevocationRacesSuccessorRotation(t *testing.T) {
	f := newRefreshPostgresFixture(t)
	parent := f.issue(t)
	child, err := f.service.Refresh(f.ctx, parent.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	session := f.tokenSession(t, child.RefreshToken)
	independent := f.issue(t)
	start := make(chan struct{})
	done := make(chan error, 2)
	for _, raw := range []string{parent.RefreshToken, child.RefreshToken} {
		go func(raw string) { <-start; _, err := f.service.Refresh(f.ctx, raw); done <- err }(raw)
	}
	close(start)
	for range 2 {
		if err := <-done; err != nil && !errors.Is(err, ErrInvalidRefreshToken) {
			t.Fatal(err)
		}
	}
	var live int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM refresh_tokens WHERE session_id=$1 AND revoked_at IS NULL`, session).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Fatal("rotation raced past revocation leaving a live successor")
	}
	if _, err := f.service.Refresh(f.ctx, independent.RefreshToken); err != nil {
		t.Fatalf("unrelated session affected: %v", err)
	}
}
