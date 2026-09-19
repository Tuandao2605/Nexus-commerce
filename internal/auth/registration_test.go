// File này kiểm thử registration application service: validation, canonicalization, hash boundary, UUIDv7 và error propagation.
package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type stubRegistrationRepository struct {
	record RegistrationRecord
	err    error
	calls  int
}

// Register ghi nhận dữ liệu service gửi xuống để unit test xác nhận không có plaintext password.
func (s *stubRegistrationRepository) Register(_ context.Context, record RegistrationRecord) error {
	s.calls++
	s.record = record
	return s.err
}

type stubPasswordHasher struct {
	password string
	hash     string
	err      error
	calls    int
}

// Hash ghi nhận plaintext tại hash boundary và trả kết quả được cấu hình cho từng test.
func (s *stubPasswordHasher) Hash(password string) (string, error) {
	s.calls++
	s.password = password
	return s.hash, s.err
}

// TestRegistrationServiceRegistersCanonicalAccount xác nhận service chuẩn hóa dữ liệu và chỉ persist encoded hash.
func TestRegistrationServiceRegistersCanonicalAccount(t *testing.T) {
	repository := &stubRegistrationRepository{}
	hasher := &stubPasswordHasher{hash: "$test$encoded-password"}
	service := NewRegistrationService(repository, hasher)

	result, err := service.Register(context.Background(), RegisterInput{
		DisplayName: "  Tuan Nguyen  ",
		Email:       "  Tuan@Example.COM  ",
		Password:    "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if result.ID == "" || repository.record.UserID != result.ID {
		t.Fatalf("registered ID = %q, persisted ID = %q", result.ID, repository.record.UserID)
	}
	if result.DisplayName != "Tuan Nguyen" || repository.record.DisplayName != result.DisplayName {
		t.Fatalf("display name result=%q record=%q", result.DisplayName, repository.record.DisplayName)
	}
	if result.Email != "tuan@example.com" || repository.record.Email != result.Email {
		t.Fatalf("email result=%q record=%q", result.Email, repository.record.Email)
	}
	if hasher.password != "correct horse battery staple" {
		t.Fatalf("hasher password = %q", hasher.password)
	}
	if repository.record.PasswordHash != hasher.hash {
		t.Fatalf("repository password hash = %q, want encoded hash", repository.record.PasswordHash)
	}
	if strings.Contains(repository.record.PasswordHash, hasher.password) {
		t.Fatal("repository received plaintext password")
	}
}

// TestRegistrationServiceRejectsInvalidInput xác nhận invalid input dừng trước hashing và persistence.
func TestRegistrationServiceRejectsInvalidInput(t *testing.T) {
	testCases := []struct {
		name  string
		input RegisterInput
		field string
		code  string
	}{
		{name: "blank display name", input: RegisterInput{DisplayName: "  ", Email: "a@example.com", Password: "secret"}, field: "display_name", code: "required"},
		{name: "long display name", input: RegisterInput{DisplayName: strings.Repeat("a", 121), Email: "a@example.com", Password: "secret"}, field: "display_name", code: "too_long"},
		{name: "invalid email", input: RegisterInput{DisplayName: "Tuan", Email: "not-an-email", Password: "secret"}, field: "email", code: "invalid"},
		{name: "oversized email", input: RegisterInput{DisplayName: "Tuan", Email: strings.Repeat("a", 243) + "@example.com", Password: "secret"}, field: "email", code: "invalid"},
		{name: "address with display name", input: RegisterInput{DisplayName: "Tuan", Email: "Tuan <a@example.com>", Password: "secret"}, field: "email", code: "invalid"},
		{name: "empty password", input: RegisterInput{DisplayName: "Tuan", Email: "a@example.com"}, field: "password", code: "required"},
		{name: "oversized password", input: RegisterInput{DisplayName: "Tuan", Email: "a@example.com", Password: strings.Repeat("x", 1025)}, field: "password", code: "too_long"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &stubRegistrationRepository{}
			hasher := &stubPasswordHasher{hash: "$test$hash"}
			service := NewRegistrationService(repository, hasher)

			_, err := service.Register(context.Background(), testCase.input)
			var fieldError *FieldError
			if !errors.As(err, &fieldError) {
				t.Fatalf("Register() error = %v, want *FieldError", err)
			}
			if fieldError.Field != testCase.field || fieldError.Code != testCase.code {
				t.Fatalf("FieldError = %#v, want field=%q code=%q", fieldError, testCase.field, testCase.code)
			}
			if hasher.calls != 0 || repository.calls != 0 {
				t.Fatalf("invalid input called hasher=%d repository=%d", hasher.calls, repository.calls)
			}
		})
	}
}

// TestRegistrationServiceRejectsEmptyEncodedHash xác nhận hasher không được phép trả giá trị rỗng xuống persistence layer.
func TestRegistrationServiceRejectsEmptyEncodedHash(t *testing.T) {
	repository := &stubRegistrationRepository{}
	service := NewRegistrationService(repository, &stubPasswordHasher{hash: "  "})

	_, err := service.Register(context.Background(), RegisterInput{
		DisplayName: "Tuan",
		Email:       "tuan@example.com",
		Password:    "secret",
	})
	if err == nil || !strings.Contains(err.Error(), "empty encoded hash") {
		t.Fatalf("Register() error = %v, want empty encoded hash error", err)
	}
	if repository.calls != 0 {
		t.Fatalf("repository calls = %d, want 0", repository.calls)
	}
}

// TestRegistrationServiceStopsWhenHashingFails xác nhận hash failure không tạo bất kỳ database write nào.
func TestRegistrationServiceStopsWhenHashingFails(t *testing.T) {
	hashError := errors.New("hash unavailable")
	repository := &stubRegistrationRepository{}
	service := NewRegistrationService(repository, &stubPasswordHasher{err: hashError})

	_, err := service.Register(context.Background(), RegisterInput{
		DisplayName: "Tuan",
		Email:       "tuan@example.com",
		Password:    "secret",
	})
	if !errors.Is(err, hashError) {
		t.Fatalf("Register() error = %v, want hash error", err)
	}
	if repository.calls != 0 {
		t.Fatalf("repository calls = %d, want 0", repository.calls)
	}
}

// TestRegistrationServicePropagatesDuplicateEmail xác nhận conflict có type ổn định để HTTP boundary map thành 409 sau này.
func TestRegistrationServicePropagatesDuplicateEmail(t *testing.T) {
	repository := &stubRegistrationRepository{err: ErrEmailAlreadyRegistered}
	service := NewRegistrationService(repository, &stubPasswordHasher{hash: "$test$hash"})

	_, err := service.Register(context.Background(), RegisterInput{
		DisplayName: "Tuan",
		Email:       "tuan@example.com",
		Password:    "secret",
	})
	if !errors.Is(err, ErrEmailAlreadyRegistered) {
		t.Fatalf("Register() error = %v, want ErrEmailAlreadyRegistered", err)
	}
}

// TestRegistrationServiceHonorsCancelledContext xác nhận request cancellation dừng trước CPU work và persistence.
func TestRegistrationServiceHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repository := &stubRegistrationRepository{}
	hasher := &stubPasswordHasher{hash: "$test$hash"}
	service := NewRegistrationService(repository, hasher)

	_, err := service.Register(ctx, RegisterInput{
		DisplayName: "Tuan",
		Email:       "tuan@example.com",
		Password:    "secret",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Register() error = %v, want context.Canceled", err)
	}
	if hasher.calls != 0 || repository.calls != 0 {
		t.Fatalf("cancelled context called hasher=%d repository=%d", hasher.calls, repository.calls)
	}
}

// TestNewUUIDv7ProducesRFCVersionAndVariant xác nhận ID do Go tạo có layout UUIDv7 và RFC 4122 variant.
func TestNewUUIDv7ProducesRFCVersionAndVariant(t *testing.T) {
	value, err := newUUIDv7()
	if err != nil {
		t.Fatalf("newUUIDv7() error = %v", err)
	}
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		t.Fatalf("newUUIDv7() = %q, want canonical UUID text", value)
	}
	if value[14] != '7' {
		t.Fatalf("newUUIDv7() version nibble = %q, want 7", value[14])
	}
	if !strings.ContainsRune("89ab", rune(value[19])) {
		t.Fatalf("newUUIDv7() variant nibble = %q, want RFC 4122 variant", value[19])
	}
}
