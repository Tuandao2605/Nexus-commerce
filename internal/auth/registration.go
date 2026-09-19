// Package auth triển khai các use case xác thực; file này định nghĩa registration orchestration và contract phụ thuộc của AUTH-001.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxDisplayNameRunes = 120
	maxEmailRunes       = 254
	maxPasswordBytes    = 1024
)

var (
	ErrEmailAlreadyRegistered  = errors.New("email is already registered")
	ErrRegistrationUnavailable = errors.New("registration service is not configured")
)

// FieldError mô tả lỗi input ổn định theo field mà không làm lộ chi tiết persistence nội bộ.
type FieldError struct {
	Field string
	Code  string
}

// Error trả về thông báo validation ngắn, phù hợp để boundary HTTP map sang error contract sau này.
func (e *FieldError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Code)
}

// RegisterInput chứa dữ liệu plaintext chỉ tồn tại tại application boundary của registration.
type RegisterInput struct {
	DisplayName string
	Email       string
	Password    string
}

// RegisteredUser là kết quả an toàn của registration, không chứa password hoặc password hash.
type RegisteredUser struct {
	ID          string
	DisplayName string
	Email       string
}

// RegistrationRecord là dữ liệu đã chuẩn hóa và đã hash được phép truyền xuống persistence layer.
type RegistrationRecord struct {
	UserID       string
	DisplayName  string
	Email        string
	PasswordHash string
}

// RegistrationRepository lưu User và Credential trong cùng một transaction.
type RegistrationRepository interface {
	// Register ghi trọn registration hoặc không ghi gì, đồng thời trả ErrEmailAlreadyRegistered khi email bị trùng.
	Register(ctx context.Context, record RegistrationRecord) error
}

// PasswordHasher chuyển plaintext password thành encoded password hash; implementation thực tế thuộc AUTH-002.
type PasswordHasher interface {
	// Hash tạo encoded hash có chứa algorithm parameters và salt, không được log plaintext đầu vào.
	Hash(password string) (string, error)
}

// RegistrationService điều phối validation, canonicalization, hashing và persistence cho registration.
type RegistrationService struct {
	repository     RegistrationRepository
	passwordHasher PasswordHasher
}

// NewRegistrationService tạo registration use case với đúng hai dependency cần thiết của application layer.
func NewRegistrationService(repository RegistrationRepository, passwordHasher PasswordHasher) *RegistrationService {
	return &RegistrationService{
		repository:     repository,
		passwordHasher: passwordHasher,
	}
}

// Register chuẩn hóa input, hash password rồi lưu User và Credential atomically mà không truyền plaintext xuống repository.
func (s *RegistrationService) Register(ctx context.Context, input RegisterInput) (RegisteredUser, error) {
	if err := ctx.Err(); err != nil {
		return RegisteredUser{}, err
	}
	if s == nil || s.repository == nil || s.passwordHasher == nil {
		return RegisteredUser{}, ErrRegistrationUnavailable
	}

	displayName, email, err := validateRegistrationInput(input)
	if err != nil {
		return RegisteredUser{}, err
	}

	passwordHash, err := s.passwordHasher.Hash(input.Password)
	if err != nil {
		return RegisteredUser{}, fmt.Errorf("hash registration password: %w", err)
	}
	if strings.TrimSpace(passwordHash) == "" {
		return RegisteredUser{}, fmt.Errorf("hash registration password: empty encoded hash")
	}
	if err := ctx.Err(); err != nil {
		return RegisteredUser{}, err
	}

	userID, err := newUUIDv7()
	if err != nil {
		return RegisteredUser{}, fmt.Errorf("generate registration user ID: %w", err)
	}

	record := RegistrationRecord{
		UserID:       userID,
		DisplayName:  displayName,
		Email:        email,
		PasswordHash: passwordHash,
	}
	if err := s.repository.Register(ctx, record); err != nil {
		if errors.Is(err, ErrEmailAlreadyRegistered) {
			return RegisteredUser{}, ErrEmailAlreadyRegistered
		}
		return RegisteredUser{}, fmt.Errorf("persist registration: %w", err)
	}

	return RegisteredUser{
		ID:          userID,
		DisplayName: displayName,
		Email:       email,
	}, nil
}

// validateRegistrationInput kiểm tra giới hạn schema/API tối thiểu và trả display name cùng email ở dạng canonical.
func validateRegistrationInput(input RegisterInput) (string, string, error) {
	displayName := strings.TrimSpace(input.DisplayName)
	displayNameLength := utf8.RuneCountInString(displayName)
	if displayNameLength == 0 {
		return "", "", &FieldError{Field: "display_name", Code: "required"}
	}
	if displayNameLength > maxDisplayNameRunes {
		return "", "", &FieldError{Field: "display_name", Code: "too_long"}
	}

	email := strings.ToLower(strings.TrimSpace(input.Email))
	if !isValidEmail(email) {
		return "", "", &FieldError{Field: "email", Code: "invalid"}
	}

	passwordLength := len(input.Password)
	if passwordLength == 0 {
		return "", "", &FieldError{Field: "password", Code: "required"}
	}
	if passwordLength > maxPasswordBytes {
		return "", "", &FieldError{Field: "password", Code: "too_long"}
	}

	return displayName, email, nil
}

// isValidEmail yêu cầu một mailbox đơn, canonical và nằm trong giới hạn VARCHAR(254) của schema.
func isValidEmail(email string) bool {
	if utf8.RuneCountInString(email) < 3 || utf8.RuneCountInString(email) > maxEmailRunes {
		return false
	}

	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return false
	}

	separator := strings.LastIndexByte(email, '@')
	return separator > 0 && separator < len(email)-1
}

// newUUIDv7 tạo UUIDv7 ở application layer bằng Unix millisecond và entropy từ crypto/rand.
func newUUIDv7() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("read UUID entropy: %w", err)
	}

	timestamp := uint64(time.Now().UnixMilli())
	value[0] = byte(timestamp >> 40)
	value[1] = byte(timestamp >> 32)
	value[2] = byte(timestamp >> 24)
	value[3] = byte(timestamp >> 16)
	value[4] = byte(timestamp >> 8)
	value[5] = byte(timestamp)
	value[6] = (value[6] & 0x0f) | 0x70
	value[8] = (value[8] & 0x3f) | 0x80

	encoded := hex.EncodeToString(value[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}
