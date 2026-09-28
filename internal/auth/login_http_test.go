// File này kiểm thử public login HTTP contract, bounded JSON handling, stable errors và việc không lộ credential.
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

type stubLoginUseCase struct {
	input  LoginInput
	result IssuedAccessToken
	err    error
	calls  int
}

// Login captures the request and returns the configured token/error to the handler.
func (s *stubLoginUseCase) Login(_ context.Context, input LoginInput) (IssuedAccessToken, error) {
	s.calls++
	s.input = input
	return s.result, s.err
}

// TestLoginHandlerReturnsBearerToken verifies successful login returns only token metadata and bearer token.
func TestLoginHandlerReturnsBearerToken(t *testing.T) {
	login := &stubLoginUseCase{result: IssuedAccessToken{Value: "signed.jwt.value", TokenType: "Bearer", ExpiresIn: 900}}
	handler := newLoginTestHandler(login)
	response := performLoginRequest(handler, `{"email":"user@example.com","password":"correct horse"}`, "application/json")

	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response status=%d content-type=%q body=%s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("token response cache headers = (%q, %q), want no-store/no-cache", response.Header().Get("Cache-Control"), response.Header().Get("Pragma"))
	}
	var body loginHTTPResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if body != (loginHTTPResponse{AccessToken: "signed.jwt.value", TokenType: "Bearer", ExpiresIn: 900}) {
		t.Fatalf("login response = %#v", body)
	}
	if login.calls != 1 || login.input != (LoginInput{Email: "user@example.com", Password: "correct horse"}) {
		t.Fatalf("login call=%d input=%#v", login.calls, login.input)
	}
	if strings.Contains(response.Body.String(), "correct horse") || strings.Contains(response.Body.String(), "password_hash") {
		t.Fatal("login response exposed credentials")
	}
}

// TestLoginHandlerRejectsUnsafeRequests ensures content type, body size, unknown fields, and trailing JSON are rejected before authentication.
func TestLoginHandlerRejectsUnsafeRequests(t *testing.T) {
	testCases := []struct {
		name        string
		body        string
		contentType string
		status      int
		code        string
	}{
		{name: "missing content type", body: `{}`, status: http.StatusUnsupportedMediaType, code: "request.unsupported_media_type"},
		{name: "malformed json", body: `{"email":`, contentType: "application/json", status: http.StatusBadRequest, code: "request.invalid_json"},
		{name: "unknown field", body: `{"email":"a@example.com","password":"secret","admin":true}`, contentType: "application/json", status: http.StatusBadRequest, code: "request.invalid_json"},
		{name: "multiple documents", body: `{} {}`, contentType: "application/json", status: http.StatusBadRequest, code: "request.invalid_json"},
		{name: "body too large", body: `{"padding":"` + strings.Repeat("x", int(maxLoginRequestBytes)) + `"}`, contentType: "application/json", status: http.StatusRequestEntityTooLarge, code: "request.body_too_large"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			login := &stubLoginUseCase{}
			response := performLoginRequest(newLoginTestHandler(login), testCase.body, testCase.contentType)
			assertLoginErrorResponse(t, response, testCase.status, testCase.code)
			if login.calls != 0 {
				t.Fatalf("login calls = %d, want 0", login.calls)
			}
		})
	}
}

// TestLoginHandlerMapsCredentialAndInternalErrors maps credentials to generic 401 and hides internal diagnostics.
func TestLoginHandlerMapsCredentialAndInternalErrors(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "invalid credentials", err: ErrInvalidCredentials, status: http.StatusUnauthorized, code: "auth.invalid_credentials"},
		{name: "internal error", err: errors.New("database password_hash secret"), status: http.StatusInternalServerError, code: "auth.login_failed"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := performLoginRequest(newLoginTestHandler(&stubLoginUseCase{err: testCase.err}), `{"email":"user@example.com","password":"secret"}`, "application/json")
			assertLoginErrorResponse(t, response, testCase.status, testCase.code)
			if strings.Contains(response.Body.String(), "database password_hash secret") || strings.Contains(response.Body.String(), "secret") {
				t.Fatal("login error response exposed internal or credential data")
			}
		})
	}
}

// newLoginTestHandler creates the login handler with a discard logger to keep test credentials out of logs.
func newLoginTestHandler(login LoginUseCase) *LoginHandler {
	return NewLoginHandler(login, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// performLoginRequest sends one request directly to the handler with the provided body and content type.
func performLoginRequest(handler http.Handler, body string, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// assertLoginErrorResponse checks the public status, JSON media type, and stable error code.
func assertLoginErrorResponse(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if response.Code != wantStatus || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response status=%d content-type=%q body=%s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	var body loginErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode login error: %v", err)
	}
	if body.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q", body.Error.Code, wantCode)
	}
}
