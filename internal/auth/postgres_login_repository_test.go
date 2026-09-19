// File này kiểm thử login với PostgreSQL thật: generic failures, User status, legacy-hash upgrade và compare-and-swap safety.
package auth

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"

	usermodule "nexus-commerce/internal/user"
)

// TestPostgresLoginAuthenticatesCanonicalEmailAndRejectsGenericFailures xác nhận lookup canonical và cùng error cho wrong/unknown credential.
func TestPostgresLoginAuthenticatesCanonicalEmailAndRejectsGenericFailures(t *testing.T) {
	ctx, pool := openRegistrationTestPool(t)
	hasher := newDefaultArgon2idTestHasher(t)
	registrationRepository := newPostgresRegistrationTestRepository(pool)
	record := newRegistrationTestRecord(t, "login-generic")
	record.Email = strings.ToLower(record.Email)
	record.PasswordHash = mustHashLoginPassword(t, hasher, "correct password")
	cleanupRegistrationRecords(t, pool, record.UserID)
	if err := registrationRepository.Register(ctx, record); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	service := newPostgresLoginTestService(t, pool, hasher)
	identity, err := service.Authenticate(ctx, LoginInput{
		Email:    "  " + strings.ToUpper(record.Email) + "  ",
		Password: "correct password",
	})
	if err != nil {
		t.Fatalf("Authenticate(correct) error = %v", err)
	}
	if identity.UserID != record.UserID || identity.Email != record.Email {
		t.Fatalf("Authenticate(correct) identity = %#v", identity)
	}

	for _, input := range []LoginInput{
		{Email: record.Email, Password: "wrong password"},
		{Email: "missing-" + record.Email, Password: "correct password"},
	} {
		identity, err = service.Authenticate(ctx, input)
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("Authenticate(%q) error = %v, want ErrInvalidCredentials", input.Email, err)
		}
		if identity != (AuthenticatedIdentity{}) {
			t.Fatalf("Authenticate(%q) identity = %#v, want empty", input.Email, identity)
		}
	}
}

// TestPostgresLoginRejectsInactiveUserWithGenericError xác nhận password đúng không bypass được User lifecycle state.
func TestPostgresLoginRejectsInactiveUserWithGenericError(t *testing.T) {
	ctx, pool := openRegistrationTestPool(t)
	hasher := newDefaultArgon2idTestHasher(t)
	record := newRegistrationTestRecord(t, "login-suspended")
	record.PasswordHash = mustHashLoginPassword(t, hasher, "correct password")
	cleanupRegistrationRecords(t, pool, record.UserID)
	if err := newPostgresRegistrationTestRepository(pool).Register(ctx, record); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status = 'suspended', updated_at = now() WHERE id = $1`, record.UserID); err != nil {
		t.Fatalf("suspend login test User: %v", err)
	}

	service := newPostgresLoginTestService(t, pool, hasher)
	identity, err := service.Authenticate(ctx, LoginInput{Email: record.Email, Password: "correct password"})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate(suspended) error = %v, want ErrInvalidCredentials", err)
	}
	if identity != (AuthenticatedIdentity{}) {
		t.Fatalf("Authenticate(suspended) identity = %#v, want empty", identity)
	}
}

// TestPostgresLoginRehashesLegacyCredentialWithoutChangingPasswordTimestamp xác nhận successful login nâng work factor nhưng không giả lập password change.
func TestPostgresLoginRehashesLegacyCredentialWithoutChangingPasswordTimestamp(t *testing.T) {
	ctx, pool := openRegistrationTestPool(t)
	hasher := newDefaultArgon2idTestHasher(t)
	record := newRegistrationTestRecord(t, "login-rehash")
	record.PasswordHash = makeLegacyLoginHash("correct password")
	cleanupRegistrationRecords(t, pool, record.UserID)
	if err := newPostgresRegistrationTestRepository(pool).Register(ctx, record); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	var passwordChangedBefore time.Time
	if err := pool.QueryRow(ctx, `SELECT password_changed_at FROM credentials WHERE user_id = $1`, record.UserID).Scan(&passwordChangedBefore); err != nil {
		t.Fatalf("query password_changed_at before login: %v", err)
	}

	service := newPostgresLoginTestService(t, pool, hasher)
	if _, err := service.Authenticate(ctx, LoginInput{Email: record.Email, Password: "correct password"}); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	var upgradedHash string
	var passwordChangedAfter time.Time
	if err := pool.QueryRow(ctx, `
		SELECT password_hash, password_changed_at
		FROM credentials
		WHERE user_id = $1
	`, record.UserID).Scan(&upgradedHash, &passwordChangedAfter); err != nil {
		t.Fatalf("query upgraded Credential: %v", err)
	}
	if upgradedHash == record.PasswordHash {
		t.Fatal("password_hash was not upgraded after successful legacy login")
	}
	if err := hasher.Verify("correct password", upgradedHash); err != nil {
		t.Fatalf("Verify(upgraded hash) error = %v", err)
	}
	needsRehash, err := hasher.NeedsRehash(upgradedHash)
	if err != nil {
		t.Fatalf("NeedsRehash(upgraded hash) error = %v", err)
	}
	if needsRehash {
		t.Fatal("NeedsRehash(upgraded hash) = true, want false")
	}
	if !passwordChangedAfter.Equal(passwordChangedBefore) {
		t.Fatalf("password_changed_at changed from %s to %s during work-factor upgrade", passwordChangedBefore, passwordChangedAfter)
	}
}

// TestPostgresLoginRehashCompareAndSwapDoesNotOverwriteNewPassword xác nhận stale login không thể ghi đè hash vừa đổi bởi luồng khác.
func TestPostgresLoginRehashCompareAndSwapDoesNotOverwriteNewPassword(t *testing.T) {
	ctx, pool := openRegistrationTestPool(t)
	hasher := newDefaultArgon2idTestHasher(t)
	record := newRegistrationTestRecord(t, "login-cas")
	record.PasswordHash = mustHashLoginPassword(t, hasher, "new password")
	cleanupRegistrationRecords(t, pool, record.UserID)
	if err := newPostgresRegistrationTestRepository(pool).Register(ctx, record); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	repository := NewPostgresLoginRepository(pool)
	updated, err := repository.UpgradePasswordHash(ctx, record.UserID, "$stale-hash", "$would-overwrite-new-password")
	if err != nil {
		t.Fatalf("UpgradePasswordHash() error = %v", err)
	}
	if updated {
		t.Fatal("UpgradePasswordHash() updated stale Credential, want false")
	}

	var storedHash string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM credentials WHERE user_id = $1`, record.UserID).Scan(&storedHash); err != nil {
		t.Fatalf("query CAS-protected Credential: %v", err)
	}
	if storedHash != record.PasswordHash {
		t.Fatalf("stored hash = %q, want concurrent new-password hash unchanged", storedHash)
	}
}

// newPostgresLoginTestService wiring Auth repository và User-owned status reader như composition root của AUTH-004 sẽ dùng.
func newPostgresLoginTestService(t *testing.T, pool *pgxpool.Pool, hasher *Argon2idHasher) *LoginService {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service, err := NewLoginService(
		NewPostgresLoginRepository(pool),
		usermodule.NewPostgresLoginStatusReader(pool),
		hasher,
		logger,
	)
	if err != nil {
		t.Fatalf("NewLoginService() error = %v", err)
	}
	return service
}

// mustHashLoginPassword tạo current-policy hash và fail integration test ngay khi crypto setup lỗi.
func mustHashLoginPassword(t *testing.T, hasher *Argon2idHasher, password string) string {
	t.Helper()

	encodedHash, err := hasher.Hash(password)
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	return encodedHash
}

// makeLegacyLoginHash tạo hash hợp lệ với policy cũ để chứng minh rehash sau verify thật, không dùng hash giả.
func makeLegacyLoginHash(password string) string {
	params := Argon2idParams{MemoryKiB: 12 * 1024, Iterations: 3, Parallelism: 1, SaltLength: 16, KeyLength: 32}
	salt := []byte("legacy-salt-0001")
	passwordBytes := []byte(password)
	key := argon2.IDKey(passwordBytes, salt, params.Iterations, params.MemoryKiB, params.Parallelism, params.KeyLength)
	clear(passwordBytes)
	encodedHash := encodeArgon2idHash(params, salt, key)
	clear(key)
	return encodedHash
}
