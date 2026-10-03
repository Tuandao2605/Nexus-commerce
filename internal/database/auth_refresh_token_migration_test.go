// File này kiểm thử các ràng buộc PostgreSQL của refresh_tokens: digest, session ownership và chuỗi predecessor.
package database

import (
	"bytes"
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestAuthRefreshTokenAcceptsSameSessionRotationChain xác nhận digest SHA-256 và predecessor cùng session tạo được một chuỗi hợp lệ.
func TestAuthRefreshTokenAcceptsSameSessionRotationChain(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	userID := newMigrationTestUUID(t)
	sessionID := insertAuthRefreshTokenTestSession(t, ctx, tx, userID)
	firstID := newMigrationTestUUID(t)
	firstHash := bytes.Repeat([]byte{1}, 32)
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (id, session_id, token_hash, expires_at)
		VALUES ($1, $2, $3, now() + interval '1 hour')
	`, firstID, sessionID, firstHash); err != nil {
		t.Fatalf("insert first refresh token: %v", err)
	}

	secondHash := bytes.Repeat([]byte{2}, 32)
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (id, session_id, token_hash, previous_token_id, expires_at)
		VALUES ($1, $2, $3, $4, now() + interval '1 hour')
	`, newMigrationTestUUID(t), sessionID, secondHash, firstID); err != nil {
		t.Fatalf("insert rotated refresh token: %v", err)
	}
}

// TestAuthRefreshTokenRejectsCrossSessionPredecessor xác nhận predecessor không thể nối chuỗi giữa hai login session.
func TestAuthRefreshTokenRejectsCrossSessionPredecessor(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	firstSessionID := insertAuthRefreshTokenTestSession(t, ctx, tx, newMigrationTestUUID(t))
	secondSessionID := insertAuthRefreshTokenTestSession(t, ctx, tx, newMigrationTestUUID(t))
	previousID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (id, session_id, token_hash, expires_at)
		VALUES ($1, $2, $3, now() + interval '1 hour')
	`, previousID, firstSessionID, bytes.Repeat([]byte{3}, 32)); err != nil {
		t.Fatalf("insert predecessor token: %v", err)
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (id, session_id, token_hash, previous_token_id, expires_at)
		VALUES ($1, $2, $3, $4, now() + interval '1 hour')
	`, newMigrationTestUUID(t), secondSessionID, bytes.Repeat([]byte{4}, 32), previousID)
	assertMigrationSQLState(t, err, "23503")
}

// TestAuthRefreshTokenAllowsOnlyOneSuccessorPerPredecessor xác nhận một predecessor chỉ có tối đa một token kế nhiệm.
func TestAuthRefreshTokenAllowsOnlyOneSuccessorPerPredecessor(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	sessionID := insertAuthRefreshTokenTestSession(t, ctx, tx, newMigrationTestUUID(t))
	previousID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (id, session_id, token_hash, expires_at)
		VALUES ($1, $2, $3, now() + interval '1 hour')
	`, previousID, sessionID, bytes.Repeat([]byte{5}, 32)); err != nil {
		t.Fatalf("insert predecessor token: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (id, session_id, token_hash, previous_token_id, expires_at)
		VALUES ($1, $2, $3, $4, now() + interval '1 hour')
	`, newMigrationTestUUID(t), sessionID, bytes.Repeat([]byte{6}, 32), previousID); err != nil {
		t.Fatalf("insert first successor: %v", err)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (id, session_id, token_hash, previous_token_id, expires_at)
		VALUES ($1, $2, $3, $4, now() + interval '1 hour')
	`, newMigrationTestUUID(t), sessionID, bytes.Repeat([]byte{8}, 32), previousID)
	assertMigrationSQLState(t, err, "23505")
}

// TestAuthRefreshTokenRejectsWrongDigestLength xác nhận cột BYTEA chỉ nhận SHA-256 digest có đúng 32 bytes.
func TestAuthRefreshTokenRejectsWrongDigestLength(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	sessionID := insertAuthRefreshTokenTestSession(t, ctx, tx, newMigrationTestUUID(t))
	_, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (id, session_id, token_hash, expires_at)
		VALUES ($1, $2, $3, now() + interval '1 hour')
	`, newMigrationTestUUID(t), sessionID, []byte{1})
	assertMigrationSQLState(t, err, "23514")
}

// TestAuthRefreshTokenRequiresUniqueFixedLengthDigest xác nhận DB chỉ chấp nhận digest 32-byte và không nhận digest trùng.
func TestAuthRefreshTokenRequiresUniqueFixedLengthDigest(t *testing.T) {
	ctx, tx := beginMigrationTest(t)
	sessionID := insertAuthRefreshTokenTestSession(t, ctx, tx, newMigrationTestUUID(t))
	digest := bytes.Repeat([]byte{7}, 32)
	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (id, session_id, token_hash, expires_at)
		VALUES ($1, $2, $3, now() + interval '1 hour')
	`, newMigrationTestUUID(t), sessionID, digest); err != nil {
		t.Fatalf("insert first digest: %v", err)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (id, session_id, token_hash, expires_at)
		VALUES ($1, $2, $3, now() + interval '1 hour')
	`, newMigrationTestUUID(t), sessionID, digest)
	assertMigrationSQLState(t, err, "23505")
}

// insertAuthRefreshTokenTestSession tạo một User và login session cô lập để migration tests không dùng dữ liệu thật.
func insertAuthRefreshTokenTestSession(t *testing.T, ctx context.Context, tx pgx.Tx, userID string) string {
	t.Helper()
	sessionID := newMigrationTestUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, display_name) VALUES ($1, 'AUTH refresh migration test')
	`, userID); err != nil {
		t.Fatalf("insert refresh token test User: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO sessions (id, user_id, expires_at) VALUES ($1, $2, now() + interval '2 hours')
	`, sessionID, userID); err != nil {
		t.Fatalf("insert refresh token test session: %v", err)
	}
	return sessionID
}
