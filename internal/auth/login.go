// File này triển khai login authentication use case, generic credential failures, dummy-hash work và opportunistic rehash cho AUTH-003.
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrLoginUnavailable   = errors.New("login service is not configured")
)

const dummyLoginPassword = "nexus-commerce-login-timing-equalizer"

// LoginInput chứa credential plaintext chỉ tồn tại trong authentication application boundary.
type LoginInput struct {
	Email    string
	Password string
}

// AuthenticatedIdentity là kết quả xác thực nội bộ để AUTH-004 phát token, không chứa credential material.
type AuthenticatedIdentity struct {
	UserID string
	Email  string
}

// LoginCredential chứa Auth-owned persistence data cần để verify password.
type LoginCredential struct {
	UserID       string
	Email        string
	PasswordHash string
}

// LoginCredentialRepository đọc Credential và compare-and-swap password hash khi policy cần nâng cấp.
type LoginCredentialRepository interface {
	// FindByEmail trả found=false cho email không tồn tại và chỉ trả error cho persistence failure.
	FindByEmail(ctx context.Context, email string) (credential LoginCredential, found bool, err error)
	// UpgradePasswordHash chỉ update khi old hash vẫn hiện hành để không ghi đè password change đồng thời.
	UpgradePasswordHash(ctx context.Context, userID, oldHash, newHash string) (updated bool, err error)
}

// LoginUserStatusReader là User-owned boundary để Auth kiểm tra account có còn active hay không.
type LoginUserStatusReader interface {
	// FindLoginUserStatus trả found=false khi User không còn tồn tại và không làm lộ profile data khác.
	FindLoginUserStatus(ctx context.Context, userID string) (status string, found bool, err error)
}

// LoginPasswordManager gom các password operations AUTH-003 cần từ concrete Argon2id implementation.
type LoginPasswordManager interface {
	// Hash tạo hash mới cho dummy timing work hoặc opportunistic rehash.
	Hash(password string) (string, error)
	// Verify so sánh password với encoded hash bằng safe comparison.
	Verify(password, encodedHash string) error
	// NeedsRehash phát hiện stored hash không còn khớp current policy.
	NeedsRehash(encodedHash string) (bool, error)
}

// LoginService xác thực Credential, kiểm tra User status và nâng cấp hash mà không phát token/session.
type LoginService struct {
	credentials LoginCredentialRepository
	users       LoginUserStatusReader
	passwords   LoginPasswordManager
	dummyHash   string
	logger      *slog.Logger
}

// NewLoginService tạo login use case và sinh một dummy hash có cùng current work factor để chống quick-exit timing leak.
func NewLoginService(
	credentials LoginCredentialRepository,
	users LoginUserStatusReader,
	passwords LoginPasswordManager,
	logger *slog.Logger,
) (*LoginService, error) {
	if credentials == nil || users == nil || passwords == nil {
		return nil, ErrLoginUnavailable
	}
	if logger == nil {
		logger = slog.Default()
	}

	dummyHash, err := passwords.Hash(dummyLoginPassword)
	if err != nil {
		return nil, fmt.Errorf("create login timing-equalization hash: %w", err)
	}
	if strings.TrimSpace(dummyHash) == "" {
		return nil, fmt.Errorf("create login timing-equalization hash: empty encoded hash")
	}

	return &LoginService{
		credentials: credentials,
		users:       users,
		passwords:   passwords,
		dummyHash:   dummyHash,
		logger:      logger,
	}, nil
}

// Authenticate canonicalize email, equalize unknown-user work, verify password/status và trả identity cho token layer kế tiếp.
func (s *LoginService) Authenticate(ctx context.Context, input LoginInput) (AuthenticatedIdentity, error) {
	if err := ctx.Err(); err != nil {
		return AuthenticatedIdentity{}, err
	}
	if s == nil || s.credentials == nil || s.users == nil || s.passwords == nil || s.dummyHash == "" {
		return AuthenticatedIdentity{}, ErrLoginUnavailable
	}

	email := strings.ToLower(strings.TrimSpace(input.Email))
	if !isValidEmail(email) || len(input.Password) == 0 || len(input.Password) > maxPasswordBytes {
		s.consumeDummyPasswordWork(input.Password)
		return AuthenticatedIdentity{}, ErrInvalidCredentials
	}

	credential, found, err := s.credentials.FindByEmail(ctx, email)
	if err != nil {
		return AuthenticatedIdentity{}, fmt.Errorf("load login credential: %w", err)
	}
	if !found {
		s.consumeDummyPasswordWork(input.Password)
		return AuthenticatedIdentity{}, ErrInvalidCredentials
	}

	if err := s.passwords.Verify(input.Password, credential.PasswordHash); err != nil {
		if errors.Is(err, ErrPasswordMismatch) || errors.Is(err, ErrInvalidPasswordHash) {
			return AuthenticatedIdentity{}, ErrInvalidCredentials
		}
		return AuthenticatedIdentity{}, fmt.Errorf("verify login password: %w", err)
	}

	status, found, err := s.users.FindLoginUserStatus(ctx, credential.UserID)
	if err != nil {
		return AuthenticatedIdentity{}, fmt.Errorf("load login user status: %w", err)
	}
	if !found || status != "active" {
		return AuthenticatedIdentity{}, ErrInvalidCredentials
	}

	s.upgradePasswordHash(ctx, input.Password, credential)
	if err := ctx.Err(); err != nil {
		return AuthenticatedIdentity{}, err
	}

	return AuthenticatedIdentity{UserID: credential.UserID, Email: credential.Email}, nil
}

// consumeDummyPasswordWork chạy cùng Argon2id verify path cho unknown/invalid email và cố ý bỏ kết quả generic.
func (s *LoginService) consumeDummyPasswordWork(password string) {
	_ = s.passwords.Verify(password, s.dummyHash)
}

// upgradePasswordHash rehash best-effort sau successful authentication và không làm login thất bại nếu maintenance write lỗi.
func (s *LoginService) upgradePasswordHash(ctx context.Context, password string, credential LoginCredential) {
	needsRehash, err := s.passwords.NeedsRehash(credential.PasswordHash)
	if err != nil {
		s.logger.Warn("password rehash check failed", "user_id", credential.UserID, "error", err)
		return
	}
	if !needsRehash {
		return
	}

	newHash, err := s.passwords.Hash(password)
	if err != nil {
		s.logger.Warn("password rehash failed", "user_id", credential.UserID, "error", err)
		return
	}

	updated, err := s.credentials.UpgradePasswordHash(ctx, credential.UserID, credential.PasswordHash, newHash)
	if err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			s.logger.Warn("password rehash persistence failed", "user_id", credential.UserID, "error", err)
		}
		return
	}
	if !updated {
		s.logger.Info("password rehash skipped after concurrent credential change", "user_id", credential.UserID)
	}
}
