// File này kiểm thử registration repository với PostgreSQL thật, gồm atomic rollback, duplicate email và concurrency race.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"nexus-commerce/internal/config"
	"nexus-commerce/internal/database"
	usermodule "nexus-commerce/internal/user"
)

// TestRegistrationHTTPIntegrationStoresVerifiableArgon2idHash xác nhận HTTP → service → transaction lưu hash thật và verify được.
func TestRegistrationHTTPIntegrationStoresVerifiableArgon2idHash(t *testing.T) {
	ctx, pool := openRegistrationTestPool(t)
	hasher := newDefaultArgon2idTestHasher(t)
	repository := newPostgresRegistrationTestRepository(pool)
	service := NewRegistrationService(repository, hasher)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewRegistrationHandler(service, logger)

	suffix, err := newUUIDv7()
	if err != nil {
		t.Fatalf("newUUIDv7() error = %v", err)
	}
	email := "auth-002-http-" + suffix + "@example.test"
	cleanupRegistrationEmails(t, pool, email)
	payload, err := json.Marshal(registrationHTTPRequest{
		DisplayName: "AUTH-002 HTTP",
		Email:       email,
		Password:    "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("marshal registration request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/auth/registrations", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("registration status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}

	var passwordHash string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM credentials WHERE email = $1`, email).Scan(&passwordHash); err != nil {
		t.Fatalf("query registration password hash: %v", err)
	}
	if passwordHash == "correct horse battery staple" || !strings.HasPrefix(passwordHash, "$argon2id$") {
		t.Fatalf("stored password hash has unsafe format: %q", passwordHash)
	}
	if err := hasher.Verify("correct horse battery staple", passwordHash); err != nil {
		t.Fatalf("verify stored password hash: %v", err)
	}
}

// TestPostgresRegistrationRepositoryPersistsUserAndCredentialAtomically xác nhận happy path tạo đúng một User và Credential liên kết.
func TestPostgresRegistrationRepositoryPersistsUserAndCredentialAtomically(t *testing.T) {
	ctx, pool := openRegistrationTestPool(t)
	repository := newPostgresRegistrationTestRepository(pool)
	record := newRegistrationTestRecord(t, "success")
	cleanupRegistrationRecords(t, pool, record.UserID)

	if err := repository.Register(ctx, record); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	var displayName, email, passwordHash, status string
	err := pool.QueryRow(ctx, `
		SELECT u.display_name, c.email, c.password_hash, u.status
		FROM users AS u
		JOIN credentials AS c ON c.user_id = u.id
		WHERE u.id = $1
	`, record.UserID).Scan(&displayName, &email, &passwordHash, &status)
	if err != nil {
		t.Fatalf("query persisted registration: %v", err)
	}
	if displayName != record.DisplayName || email != record.Email || passwordHash != record.PasswordHash || status != "active" {
		t.Fatalf("persisted registration = (%q, %q, %q, %q)", displayName, email, passwordHash, status)
	}
}

// TestPostgresRegistrationRepositoryRollsBackUserWhenCredentialFails xác nhận lỗi insert Credential không để lại orphan User.
func TestPostgresRegistrationRepositoryRollsBackUserWhenCredentialFails(t *testing.T) {
	ctx, pool := openRegistrationTestPool(t)
	repository := newPostgresRegistrationTestRepository(pool)
	record := newRegistrationTestRecord(t, "rollback")
	record.PasswordHash = ""
	cleanupRegistrationRecords(t, pool, record.UserID)

	err := repository.Register(ctx, record)
	if err == nil {
		t.Fatal("Register() error = nil, want credential constraint error")
	}

	var userCount int
	if queryErr := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`, record.UserID).Scan(&userCount); queryErr != nil {
		t.Fatalf("count rolled back user: %v", queryErr)
	}
	if userCount != 0 {
		t.Fatalf("rolled back user count = %d, want 0", userCount)
	}
}

// TestPostgresRegistrationRepositoryMapsDuplicateEmail xác nhận UNIQUE(email) thành typed conflict và rollback User thứ hai.
func TestPostgresRegistrationRepositoryMapsDuplicateEmail(t *testing.T) {
	ctx, pool := openRegistrationTestPool(t)
	repository := newPostgresRegistrationTestRepository(pool)
	first := newRegistrationTestRecord(t, "duplicate-first")
	second := newRegistrationTestRecord(t, "duplicate-second")
	second.Email = first.Email
	cleanupRegistrationRecords(t, pool, first.UserID, second.UserID)

	if err := repository.Register(ctx, first); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if err := repository.Register(ctx, second); !errors.Is(err, ErrEmailAlreadyRegistered) {
		t.Fatalf("second Register() error = %v, want ErrEmailAlreadyRegistered", err)
	}

	var secondUserCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`, second.UserID).Scan(&secondUserCount); err != nil {
		t.Fatalf("count duplicate user: %v", err)
	}
	if secondUserCount != 0 {
		t.Fatalf("duplicate user count = %d, want 0", secondUserCount)
	}
}

// TestPostgresRegistrationRepositoryHandlesConcurrentDuplicateEmail xác nhận hai request race chỉ có một registration commit.
func TestPostgresRegistrationRepositoryHandlesConcurrentDuplicateEmail(t *testing.T) {
	ctx, pool := openRegistrationTestPool(t)
	repository := newPostgresRegistrationTestRepository(pool)
	first := newRegistrationTestRecord(t, "race-first")
	second := newRegistrationTestRecord(t, "race-second")
	second.Email = first.Email
	cleanupRegistrationRecords(t, pool, first.UserID, second.UserID)

	start := make(chan struct{})
	errorsByRequest := make(chan error, 2)
	var workers sync.WaitGroup
	for _, record := range []RegistrationRecord{first, second} {
		record := record
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			errorsByRequest <- repository.Register(ctx, record)
		}()
	}
	close(start)
	workers.Wait()
	close(errorsByRequest)

	successes := 0
	duplicates := 0
	for err := range errorsByRequest {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrEmailAlreadyRegistered):
			duplicates++
		default:
			t.Fatalf("concurrent Register() error = %v", err)
		}
	}
	if successes != 1 || duplicates != 1 {
		t.Fatalf("concurrent results successes=%d duplicates=%d, want 1 and 1", successes, duplicates)
	}

	var userCount, credentialCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1 OR id = $2`, first.UserID, second.UserID).Scan(&userCount); err != nil {
		t.Fatalf("count concurrent users: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM credentials WHERE email = $1`, first.Email).Scan(&credentialCount); err != nil {
		t.Fatalf("count concurrent credentials: %v", err)
	}
	if userCount != 1 || credentialCount != 1 {
		t.Fatalf("concurrent rows users=%d credentials=%d, want 1 and 1", userCount, credentialCount)
	}
}

// openRegistrationTestPool mở PostgreSQL integration pool và skip khi test thường không cung cấp TEST_DATABASE_URL.
func openRegistrationTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("REQUIRE_DATABASE_INTEGRATION") == "1" {
			t.Fatal("TEST_DATABASE_URL is required for auth integration tests")
		}
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	pool, err := database.NewPool(ctx, config.DatabaseConfig{
		URL:               databaseURL,
		MaxConns:          10,
		MinConns:          1,
		MaxConnLifetime:   time.Hour,
		MaxConnIdleTime:   30 * time.Minute,
		HealthCheckPeriod: time.Minute,
		StartupTimeout:    5 * time.Second,
	})
	if err != nil {
		cancel()
		t.Fatalf("open auth test database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cancel()
	})

	return ctx, pool
}

// newPostgresRegistrationTestRepository wiring Auth repository với User-owned writer giống application bootstrap tương lai.
func newPostgresRegistrationTestRepository(pool *pgxpool.Pool) *PostgresRegistrationRepository {
	return NewPostgresRegistrationRepository(pool, usermodule.NewPostgresRegistrationWriter())
}

// newRegistrationTestRecord tạo dữ liệu registration riêng biệt bằng UUIDv7 để các integration test không va chạm nhau.
func newRegistrationTestRecord(t *testing.T, label string) RegistrationRecord {
	t.Helper()

	userID, err := newUUIDv7()
	if err != nil {
		t.Fatalf("newUUIDv7() error = %v", err)
	}

	return RegistrationRecord{
		UserID:       userID,
		DisplayName:  "AUTH-001 " + label,
		Email:        fmt.Sprintf("auth-001-%s-%s@example.test", label, userID),
		PasswordHash: "$test$encoded-password",
	}
}

// cleanupRegistrationRecords xóa đúng các test row đã biết theo thứ tự FK và không đụng dữ liệu ngoài scope.
func cleanupRegistrationRecords(t *testing.T, pool *pgxpool.Pool, userIDs ...string) {
	t.Helper()

	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		for _, userID := range userIDs {
			if _, err := pool.Exec(cleanupContext, `DELETE FROM credentials WHERE user_id = $1`, userID); err != nil {
				t.Errorf("cleanup credential %s: %v", userID, err)
			}
			if _, err := pool.Exec(cleanupContext, `DELETE FROM users WHERE id = $1`, userID); err != nil {
				t.Errorf("cleanup user %s: %v", userID, err)
			}
		}
	})
}

// cleanupRegistrationEmails xóa đúng registration test rows theo email qua một data-modifying CTE giữ đúng FK order.
func cleanupRegistrationEmails(t *testing.T, pool *pgxpool.Pool, emails ...string) {
	t.Helper()

	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		for _, email := range emails {
			if _, err := pool.Exec(cleanupContext, `
				WITH deleted_credentials AS (
					DELETE FROM credentials
					WHERE email = $1
					RETURNING user_id
				)
				DELETE FROM users
				WHERE id IN (SELECT user_id FROM deleted_credentials)
			`, email); err != nil {
				t.Errorf("cleanup registration email %s: %v", email, err)
			}
		}
	})
}
