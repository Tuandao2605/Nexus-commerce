// File này kiểm thử public registration HTTP contract, bounded decoding, mass-assignment rejection và safe error mapping.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubRegistrationUseCase struct {
	input RegisterInput
	user  RegisteredUser
	err   error
	calls int
}

// Register ghi nhận input/context từ HTTP handler và trả kết quả được cấu hình cho từng test.
func (s *stubRegistrationUseCase) Register(ctx context.Context, input RegisterInput) (RegisteredUser, error) {
	if ctx == nil {
		return RegisteredUser{}, errors.New("request context is nil")
	}
	s.calls++
	s.input = input
	return s.user, s.err
}

// TestRegistrationHandlerCreatesAccount xác nhận success contract là 201 JSON và không phản hồi password/hash.
func TestRegistrationHandlerCreatesAccount(t *testing.T) {
	registrar := &stubRegistrationUseCase{user: RegisteredUser{
		ID:          "0199f9d2-2e83-7000-8000-000000000001",
		DisplayName: "Tuan Nguyen",
		Email:       "tuan@example.com",
	}}
	handler := newRegistrationTestHandler(registrar)
	response := performRegistrationRequest(handler, `{"display_name":"Tuan Nguyen","email":"tuan@example.com","password":"secret"}`, "application/json")

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", response.Header().Get("Content-Type"))
	}
	if registrar.calls != 1 || registrar.input.Password != "secret" {
		t.Fatalf("registrar calls=%d input=%#v", registrar.calls, registrar.input)
	}

	rawBody := response.Body.String()
	var body registrationHTTPResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body != (registrationHTTPResponse{ID: registrar.user.ID, DisplayName: registrar.user.DisplayName, Email: registrar.user.Email}) {
		t.Fatalf("response = %#v", body)
	}
	if strings.Contains(rawBody, "secret") || strings.Contains(rawBody, "hash") {
		t.Fatal("registration response exposed password material")
	}
}

// TestRegistrationHandlerRejectsUnsafeRequests xác nhận content type, body size, unknown field và trailing JSON bị chặn trước service.
func TestRegistrationHandlerRejectsUnsafeRequests(t *testing.T) {
	testCases := []struct {
		name        string
		body        string
		contentType string
		wantStatus  int
		wantCode    string
	}{
		{name: "missing content type", body: `{}`, wantStatus: http.StatusUnsupportedMediaType, wantCode: "request.unsupported_media_type"},
		{name: "malformed json", body: `{"email":`, contentType: "application/json", wantStatus: http.StatusBadRequest, wantCode: "request.invalid_json"},
		{name: "unknown field", body: `{"display_name":"Tuan","email":"a@example.com","password":"secret","admin":true}`, contentType: "application/json", wantStatus: http.StatusBadRequest, wantCode: "request.invalid_json"},
		{name: "multiple documents", body: `{} {}`, contentType: "application/json", wantStatus: http.StatusBadRequest, wantCode: "request.invalid_json"},
		{name: "body too large", body: `{"padding":"` + strings.Repeat("x", int(maxRegistrationRequestBytes)) + `"}`, contentType: "application/json", wantStatus: http.StatusRequestEntityTooLarge, wantCode: "request.body_too_large"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			registrar := &stubRegistrationUseCase{}
			handler := newRegistrationTestHandler(registrar)
			response := performRegistrationRequest(handler, testCase.body, testCase.contentType)

			assertRegistrationErrorResponse(t, response, testCase.wantStatus, testCase.wantCode)
			if registrar.calls != 0 {
				t.Fatalf("registrar calls = %d, want 0", registrar.calls)
			}
		})
	}
}

// TestRegistrationHandlerMapsApplicationErrors xác nhận validation/conflict/internal errors dùng status và stable code đúng contract.
func TestRegistrationHandlerMapsApplicationErrors(t *testing.T) {
	testCases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "validation", err: &FieldError{Field: "email", Code: "invalid"}, wantStatus: http.StatusBadRequest, wantCode: "auth.invalid_registration"},
		{name: "duplicate", err: ErrEmailAlreadyRegistered, wantStatus: http.StatusConflict, wantCode: "auth.email_already_registered"},
		{name: "cancelled", err: context.Canceled, wantStatus: http.StatusRequestTimeout, wantCode: "request.cancelled"},
		{name: "deadline", err: context.DeadlineExceeded, wantStatus: http.StatusGatewayTimeout, wantCode: "request.timeout"},
		{name: "internal", err: errors.New("database secret detail"), wantStatus: http.StatusInternalServerError, wantCode: "auth.registration_failed"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			registrar := &stubRegistrationUseCase{err: testCase.err}
			handler := newRegistrationTestHandler(registrar)
			response := performRegistrationRequest(handler, `{"display_name":"Tuan","email":"a@example.com","password":"secret"}`, "application/json")

			rawBody := response.Body.String()
			assertRegistrationErrorResponse(t, response, testCase.wantStatus, testCase.wantCode)
			if strings.Contains(rawBody, "database secret detail") || strings.Contains(rawBody, "secret") {
				t.Fatal("registration error response exposed internal or password data")
			}
		})
	}
}

// newRegistrationTestHandler tạo handler với logger discard để unit test không ghi diagnostic noise hoặc sensitive fixture.
func newRegistrationTestHandler(registrar RegistrationUseCase) *RegistrationHandler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewRegistrationHandler(registrar, logger)
}

// performRegistrationRequest gửi POST request trực tiếp tới handler với body/content type do test chỉ định.
func performRegistrationRequest(handler http.Handler, body, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/auth/registrations", bytes.NewBufferString(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// assertRegistrationErrorResponse xác nhận status, JSON content type và stable error code của public contract.
func assertRegistrationErrorResponse(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()

	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, wantStatus, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", response.Header().Get("Content-Type"))
	}

	var body registrationErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q", body.Error.Code, wantCode)
	}
}
