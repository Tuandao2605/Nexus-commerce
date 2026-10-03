// File này kiểm thử các sqlc refresh-token queries trên PostgreSQL thật, gồm lineage, conditional consume và revoke.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"nexus-commerce/internal/database/sqlc"
)

// TestPostgresRefreshTokenQueriesEnforceLifecycleAndIdempotency xác nhận tạo/đọc, consume một lần và revoke session an toàn.
func TestPostgresRefreshTokenQueriesEnforceLifecycleAndIdempotency(t *testing.T) {
	ctx, pool := openRegistrationTestPool(t)
	record := newRegistrationTestRecord(t, "refresh-query")
	cleanupRegistrationRecords(t, pool, record.UserID)
	if err := newPostgresRegistrationTestRepository(pool).Register(ctx, record); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	var sessionID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO sessions (id, user_id, expires_at)
		VALUES ($1, $2, now() + interval '2 hours')
		RETURNING id
	`, mustRefreshTokenTestUUID(t), record.UserID).Scan(&sessionID); err != nil {
		t.Fatalf("insert refresh-token test session: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupContext, `DELETE FROM refresh_tokens WHERE session_id = $1`, sessionID); err != nil {
			t.Errorf("cleanup refresh tokens: %v", err)
		}
		if _, err := pool.Exec(cleanupContext, `DELETE FROM sessions WHERE id = $1`, sessionID); err != nil {
			t.Errorf("cleanup refresh-token session: %v", err)
		}
	})

	queries := dbsqlc.New(pool)
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	expiresAt := createdAt.Add(time.Hour)
	rawToken := []byte("test-only-random-looking-refresh-secret")
	digest := sha256.Sum256(rawToken)
	firstID := mustRefreshTokenTestUUID(t)
	created, err := queries.CreateRefreshToken(ctx, dbsqlc.CreateRefreshTokenParams{
		ID:        firstID,
		SessionID: sessionID,
		TokenHash: digest[:],
		CreatedAt: pgtype.Timestamptz{Time: createdAt, Valid: true},
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil || created != 1 {
		t.Fatalf("CreateRefreshToken() = (%d, %v), want (1, nil)", created, err)
	}

	stored, err := queries.GetRefreshTokenByHash(ctx, digest[:])
	if err != nil {
		t.Fatalf("GetRefreshTokenByHash() error = %v", err)
	}
	if stored.ID != firstID || stored.SessionID != sessionID || string(stored.TokenHash) != string(digest[:]) || stored.PreviousTokenID.Valid {
		t.Fatalf("GetRefreshTokenByHash() = %#v, want original token and no predecessor", stored)
	}

	type consumeResult struct {
		rows int64
		err  error
	}
	start := make(chan struct{})
	results := make(chan consumeResult, 2)
	var consumers sync.WaitGroup
	for range 2 {
		consumers.Add(1)
		go func() {
			defer consumers.Done()
			<-start
			rows, err := queries.ConsumeRefreshToken(ctx, firstID)
			results <- consumeResult{rows: rows, err: err}
		}()
	}
	close(start)
	consumers.Wait()
	close(results)
	var consumeWinners int
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent ConsumeRefreshToken() error = %v", result.err)
		}
		if result.rows == 1 {
			consumeWinners++
		} else if result.rows != 0 {
			t.Fatalf("concurrent ConsumeRefreshToken() rows = %d, want 0 or 1", result.rows)
		}
	}
	if consumeWinners != 1 {
		t.Fatalf("concurrent ConsumeRefreshToken() winners = %d, want exactly 1", consumeWinners)
	}
	consumed, err := queries.ConsumeRefreshToken(ctx, firstID)
	if err != nil || consumed != 0 {
		t.Fatalf("post-race ConsumeRefreshToken() = (%d, %v), want (0, nil)", consumed, err)
	}

	secondID := mustRefreshTokenTestUUID(t)
	secondDigest := sha256.Sum256([]byte("next test-only refresh secret"))
	created, err = queries.CreateRefreshToken(ctx, dbsqlc.CreateRefreshTokenParams{
		ID:              secondID,
		SessionID:       sessionID,
		TokenHash:       secondDigest[:],
		PreviousTokenID: firstID,
		CreatedAt:       pgtype.Timestamptz{Time: createdAt, Valid: true},
		ExpiresAt:       pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil || created != 1 {
		t.Fatalf("CreateRefreshToken(successor) = (%d, %v), want (1, nil)", created, err)
	}

	revoked, err := queries.RevokeSessionRefreshTokens(ctx, sessionID)
	if err != nil || revoked != 2 {
		t.Fatalf("RevokeSessionRefreshTokens() = (%d, %v), want (2, nil)", revoked, err)
	}
	revoked, err = queries.RevokeSessionRefreshTokens(ctx, sessionID)
	if err != nil || revoked != 0 {
		t.Fatalf("second RevokeSessionRefreshTokens() = (%d, %v), want (0, nil)", revoked, err)
	}

	var revokedAt pgtype.Timestamptz
	if err := pool.QueryRow(ctx, `SELECT revoked_at FROM refresh_tokens WHERE id = $1`, secondID).Scan(&revokedAt); err != nil {
		t.Fatalf("read successor revocation: %v", err)
	}
	if !revokedAt.Valid {
		t.Fatal("successor revoked_at is NULL after session-wide revocation")
	}
}

// mustRefreshTokenTestUUID converts a newly generated UUIDv7 to pgtype.UUID for generated sqlc method arguments.
func mustRefreshTokenTestUUID(t *testing.T) pgtype.UUID {
	t.Helper()
	value, err := newUUIDv7()
	if err != nil {
		t.Fatalf("newUUIDv7() error = %v", err)
	}
	encoded := strings.ReplaceAll(value, "-", "")
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != 16 {
		t.Fatalf("decode UUIDv7 %q: %v", value, err)
	}
	var uuid pgtype.UUID
	copy(uuid.Bytes[:], decoded)
	uuid.Valid = true
	return uuid
}
