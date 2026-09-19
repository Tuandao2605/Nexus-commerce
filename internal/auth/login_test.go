// File này kiểm thử login service: generic credential error, timing-equalization work, User status boundary và opportunistic rehash.
package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

type stubLoginCredentialRepository struct {
	credential       LoginCredential
	found            bool
	findErr          error
	findEmail        string
	findCalls        int
	upgradeErr       error
	upgradeUpdated   bool
	upgradeUserID    string
	upgradeOldHash   string
	upgradeNewHash   string
	upgradeCallCount int
}

// FindByEmail ghi lại canonical email và trả Credential outcome được cấu hình cho unit test.
func (s *stubLoginCredentialRepository) FindByEmail(_ context.Context, email string) (LoginCredential, bool, error) {
	s.findCalls++
	s.findEmail = email
	return s.credential, s.found, s.findErr
}

// UpgradePasswordHash ghi lại compare-and-swap arguments để test không cần PostgreSQL.
func (s *stubLoginCredentialRepository) UpgradePasswordHash(
	_ context.Context,
	userID string,
	oldHash string,
	newHash string,
) (bool, error) {
	s.upgradeCallCount++
	s.upgradeUserID = userID
	s.upgradeOldHash = oldHash
	s.upgradeNewHash = newHash
	return s.upgradeUpdated, s.upgradeErr
}

type stubLoginUserStatusReader struct {
	status    string
	found     bool
	err       error
	userID    string
	callCount int
}

// FindLoginUserStatus ghi lại User ID và trả lifecycle state được cấu hình cho unit test.
func (s *stubLoginUserStatusReader) FindLoginUserStatus(_ context.Context, userID string) (string, bool, error) {
	s.callCount++
	s.userID = userID
	return s.status, s.found, s.err
}

type loginVerifyCall struct {
	password    string
	encodedHash string
}

type stubLoginPasswordManager struct {
	hashResults     []string
	hashErrors      []error
	hashInputs      []string
	verifyErrors    map[string]error
	verifyCalls     []loginVerifyCall
	needsRehash     bool
	needsRehashErr  error
	needsRehashHash string
	needsCalls      int
}

// Hash trả lần lượt dummy hash rồi rehash result, đồng thời ghi lại plaintext chỉ trong test memory.
func (s *stubLoginPasswordManager) Hash(password string) (string, error) {
	callIndex := len(s.hashInputs)
	s.hashInputs = append(s.hashInputs, password)
	var result string
	if callIndex < len(s.hashResults) {
		result = s.hashResults[callIndex]
	}
	var err error
	if callIndex < len(s.hashErrors) {
		err = s.hashErrors[callIndex]
	}
	return result, err
}

// Verify ghi lại password/hash path và trả lỗi theo encoded hash để phân biệt dummy với stored Credential.
func (s *stubLoginPasswordManager) Verify(password, encodedHash string) error {
	s.verifyCalls = append(s.verifyCalls, loginVerifyCall{password: password, encodedHash: encodedHash})
	return s.verifyErrors[encodedHash]
}

// NeedsRehash ghi lại stored hash và trả policy decision được cấu hình cho unit test.
func (s *stubLoginPasswordManager) NeedsRehash(encodedHash string) (bool, error) {
	s.needsCalls++
	s.needsRehashHash = encodedHash
	return s.needsRehash, s.needsRehashErr
}

// TestLoginServiceAuthenticatesCanonicalActiveAccount xác nhận email canonical, password verify và User status check theo đúng thứ tự boundary.
func TestLoginServiceAuthenticatesCanonicalActiveAccount(t *testing.T) {
	repository := &stubLoginCredentialRepository{
		credential: LoginCredential{UserID: "user-1", Email: "tuan@example.com", PasswordHash: "$stored"},
		found:      true,
	}
	users := &stubLoginUserStatusReader{status: "active", found: true}
	passwords := &stubLoginPasswordManager{
		hashResults:  []string{"$dummy"},
		verifyErrors: map[string]error{},
	}
	service := newLoginUnitService(t, repository, users, passwords)

	identity, err := service.Authenticate(context.Background(), LoginInput{
		Email:    "  Tuan@Example.COM  ",
		Password: "correct password",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity.UserID != "user-1" || identity.Email != "tuan@example.com" {
		t.Fatalf("Authenticate() identity = %#v", identity)
	}
	if repository.findEmail != "tuan@example.com" {
		t.Fatalf("FindByEmail() email = %q, want canonical email", repository.findEmail)
	}
	if len(passwords.verifyCalls) != 1 || passwords.verifyCalls[0].encodedHash != "$stored" {
		t.Fatalf("Verify() calls = %#v, want stored Credential once", passwords.verifyCalls)
	}
	if users.callCount != 1 || users.userID != "user-1" {
		t.Fatalf("FindLoginUserStatus() calls=%d userID=%q", users.callCount, users.userID)
	}
	if passwords.needsCalls != 1 || repository.upgradeCallCount != 0 {
		t.Fatalf("rehash checks=%d upgrades=%d, want 1 and 0", passwords.needsCalls, repository.upgradeCallCount)
	}
}

// TestLoginServiceReturnsGenericErrorForCredentialFailures xác nhận mọi failure có thể tiết lộ account đều dùng cùng public error.
func TestLoginServiceReturnsGenericErrorForCredentialFailures(t *testing.T) {
	testCases := []struct {
		name          string
		input         LoginInput
		credential    LoginCredential
		credentialOK  bool
		verifyError   error
		userStatus    string
		userFound     bool
		wantDummyWork bool
	}{
		{name: "invalid email", input: LoginInput{Email: "not-email", Password: "secret"}, wantDummyWork: true},
		{name: "unknown email", input: LoginInput{Email: "missing@example.com", Password: "secret"}, wantDummyWork: true},
		{name: "wrong password", input: LoginInput{Email: "user@example.com", Password: "wrong"}, credential: LoginCredential{UserID: "user-1", Email: "user@example.com", PasswordHash: "$stored"}, credentialOK: true, verifyError: ErrPasswordMismatch},
		{name: "malformed stored hash", input: LoginInput{Email: "user@example.com", Password: "secret"}, credential: LoginCredential{UserID: "user-1", Email: "user@example.com", PasswordHash: "$stored"}, credentialOK: true, verifyError: ErrInvalidPasswordHash},
		{name: "suspended account", input: LoginInput{Email: "user@example.com", Password: "secret"}, credential: LoginCredential{UserID: "user-1", Email: "user@example.com", PasswordHash: "$stored"}, credentialOK: true, userStatus: "suspended", userFound: true},
		{name: "deleted account", input: LoginInput{Email: "user@example.com", Password: "secret"}, credential: LoginCredential{UserID: "user-1", Email: "user@example.com", PasswordHash: "$stored"}, credentialOK: true, userStatus: "deleted", userFound: true},
		{name: "missing user", input: LoginInput{Email: "user@example.com", Password: "secret"}, credential: LoginCredential{UserID: "user-1", Email: "user@example.com", PasswordHash: "$stored"}, credentialOK: true, userStatus: "", userFound: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &stubLoginCredentialRepository{credential: testCase.credential, found: testCase.credentialOK}
			users := &stubLoginUserStatusReader{status: testCase.userStatus, found: testCase.userFound}
			passwords := &stubLoginPasswordManager{
				hashResults: []string{"$dummy"},
				verifyErrors: map[string]error{
					"$stored": testCase.verifyError,
				},
			}
			service := newLoginUnitService(t, repository, users, passwords)

			identity, err := service.Authenticate(context.Background(), testCase.input)
			if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("Authenticate() error = %v, want ErrInvalidCredentials", err)
			}
			if identity != (AuthenticatedIdentity{}) {
				t.Fatalf("Authenticate() identity = %#v, want empty", identity)
			}
			if testCase.wantDummyWork {
				if len(passwords.verifyCalls) != 1 || passwords.verifyCalls[0].encodedHash != "$dummy" {
					t.Fatalf("dummy Verify() calls = %#v", passwords.verifyCalls)
				}
			}
			if users.callCount > 0 && testCase.verifyError != nil {
				t.Fatalf("User status read after failed password verify: calls=%d", users.callCount)
			}
		})
	}
}

// TestLoginServicePreservesOperationalErrors xác nhận persistence/password infrastructure failures không bị che thành invalid credentials.
func TestLoginServicePreservesOperationalErrors(t *testing.T) {
	databaseError := errors.New("database unavailable")
	repository := &stubLoginCredentialRepository{findErr: databaseError}
	service := newLoginUnitService(
		t,
		repository,
		&stubLoginUserStatusReader{},
		&stubLoginPasswordManager{hashResults: []string{"$dummy"}, verifyErrors: map[string]error{}},
	)

	_, err := service.Authenticate(context.Background(), LoginInput{Email: "user@example.com", Password: "secret"})
	if !errors.Is(err, databaseError) || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate() error = %v, want wrapped database error", err)
	}

	statusError := errors.New("user read unavailable")
	repository = &stubLoginCredentialRepository{
		credential: LoginCredential{UserID: "user-1", Email: "user@example.com", PasswordHash: "$stored"},
		found:      true,
	}
	service = newLoginUnitService(
		t,
		repository,
		&stubLoginUserStatusReader{err: statusError},
		&stubLoginPasswordManager{hashResults: []string{"$dummy"}, verifyErrors: map[string]error{}},
	)
	_, err = service.Authenticate(context.Background(), LoginInput{Email: "user@example.com", Password: "secret"})
	if !errors.Is(err, statusError) || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate() error = %v, want wrapped User read error", err)
	}
}

// TestLoginServiceRehashesWithCompareAndSwap xác nhận legacy hash được thay bằng current hash nhưng old hash vẫn là CAS guard.
func TestLoginServiceRehashesWithCompareAndSwap(t *testing.T) {
	repository := &stubLoginCredentialRepository{
		credential:     LoginCredential{UserID: "user-1", Email: "user@example.com", PasswordHash: "$legacy"},
		found:          true,
		upgradeUpdated: true,
	}
	users := &stubLoginUserStatusReader{status: "active", found: true}
	passwords := &stubLoginPasswordManager{
		hashResults:  []string{"$dummy", "$current"},
		verifyErrors: map[string]error{},
		needsRehash:  true,
	}
	service := newLoginUnitService(t, repository, users, passwords)

	identity, err := service.Authenticate(context.Background(), LoginInput{Email: "user@example.com", Password: "secret"})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity.UserID != "user-1" {
		t.Fatalf("Authenticate() identity = %#v", identity)
	}
	if len(passwords.hashInputs) != 2 || passwords.hashInputs[1] != "secret" {
		t.Fatalf("Hash() inputs = %#v, want dummy then authenticated password", passwords.hashInputs)
	}
	if passwords.needsRehashHash != "$legacy" {
		t.Fatalf("NeedsRehash() hash = %q, want legacy hash", passwords.needsRehashHash)
	}
	if repository.upgradeCallCount != 1 || repository.upgradeUserID != "user-1" || repository.upgradeOldHash != "$legacy" || repository.upgradeNewHash != "$current" {
		t.Fatalf("UpgradePasswordHash() call = (%d, %q, %q, %q)", repository.upgradeCallCount, repository.upgradeUserID, repository.upgradeOldHash, repository.upgradeNewHash)
	}
}

// TestLoginServiceTreatsRehashAsBestEffort xác nhận maintenance hash/write failure không khóa một login đã xác thực đúng.
func TestLoginServiceTreatsRehashAsBestEffort(t *testing.T) {
	testCases := []struct {
		name          string
		hashErrors    []error
		upgradeError  error
		upgradeResult bool
	}{
		{name: "hash failure", hashErrors: []error{nil, errors.New("random source unavailable")}},
		{name: "persistence failure", upgradeError: errors.New("database unavailable")},
		{name: "concurrent password change", upgradeResult: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &stubLoginCredentialRepository{
				credential:     LoginCredential{UserID: "user-1", Email: "user@example.com", PasswordHash: "$legacy"},
				found:          true,
				upgradeErr:     testCase.upgradeError,
				upgradeUpdated: testCase.upgradeResult,
			}
			passwords := &stubLoginPasswordManager{
				hashResults:  []string{"$dummy", "$current"},
				hashErrors:   testCase.hashErrors,
				verifyErrors: map[string]error{},
				needsRehash:  true,
			}
			service := newLoginUnitService(t, repository, &stubLoginUserStatusReader{status: "active", found: true}, passwords)

			identity, err := service.Authenticate(context.Background(), LoginInput{Email: "user@example.com", Password: "secret"})
			if err != nil {
				t.Fatalf("Authenticate() error = %v", err)
			}
			if identity.UserID != "user-1" {
				t.Fatalf("Authenticate() identity = %#v", identity)
			}
		})
	}
}

// TestLoginServiceHonorsCancelledContext xác nhận cancellation dừng trước repository và password CPU work.
func TestLoginServiceHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repository := &stubLoginCredentialRepository{}
	passwords := &stubLoginPasswordManager{hashResults: []string{"$dummy"}, verifyErrors: map[string]error{}}
	service := newLoginUnitService(t, repository, &stubLoginUserStatusReader{}, passwords)

	_, err := service.Authenticate(ctx, LoginInput{Email: "user@example.com", Password: "secret"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Authenticate() error = %v, want context.Canceled", err)
	}
	if repository.findCalls != 0 || len(passwords.verifyCalls) != 0 {
		t.Fatalf("cancelled login performed find=%d verify=%d", repository.findCalls, len(passwords.verifyCalls))
	}
}

// TestNewLoginServiceRejectsInvalidDependencies xác nhận constructor fail closed khi thiếu port hoặc không tạo được dummy hash.
func TestNewLoginServiceRejectsInvalidDependencies(t *testing.T) {
	repository := &stubLoginCredentialRepository{}
	users := &stubLoginUserStatusReader{}
	passwords := &stubLoginPasswordManager{hashResults: []string{"$dummy"}, verifyErrors: map[string]error{}}

	for _, build := range []func() (*LoginService, error){
		func() (*LoginService, error) { return NewLoginService(nil, users, passwords, nil) },
		func() (*LoginService, error) { return NewLoginService(repository, nil, passwords, nil) },
		func() (*LoginService, error) { return NewLoginService(repository, users, nil, nil) },
	} {
		if _, err := build(); !errors.Is(err, ErrLoginUnavailable) {
			t.Fatalf("NewLoginService() error = %v, want ErrLoginUnavailable", err)
		}
	}

	hashError := errors.New("random source unavailable")
	if _, err := NewLoginService(repository, users, &stubLoginPasswordManager{hashErrors: []error{hashError}}, nil); !errors.Is(err, hashError) {
		t.Fatalf("NewLoginService() error = %v, want wrapped hash error", err)
	}
	if _, err := NewLoginService(repository, users, &stubLoginPasswordManager{hashResults: []string{"   "}}, nil); err == nil || !strings.Contains(err.Error(), "empty encoded hash") {
		t.Fatalf("NewLoginService() error = %v, want empty encoded hash error", err)
	}
}

// newLoginUnitService tạo service với logger discard và fail test ngay nếu dummy hash setup không hợp lệ.
func newLoginUnitService(
	t *testing.T,
	repository LoginCredentialRepository,
	users LoginUserStatusReader,
	passwords LoginPasswordManager,
) *LoginService {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service, err := NewLoginService(repository, users, passwords, logger)
	if err != nil {
		t.Fatalf("NewLoginService() error = %v", err)
	}
	return service
}
