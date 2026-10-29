// File này kiểm thử refresh JSON contract, safe errors, cache headers và input bounds độc lập persistence.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubRefreshUseCase struct {
	input string
	calls int
	err   error
}

// Refresh ghi nhận request và trả token pair giả hoặc lỗi được cấu hình.
func (s *stubRefreshUseCase) Refresh(_ context.Context, raw string) (IssuedTokenPair, error) {
	s.calls++
	s.input = raw
	return IssuedTokenPair{IssuedAccessToken: IssuedAccessToken{Value: "jwt", TokenType: "Bearer", ExpiresIn: 900}, RefreshToken: "replacement", RefreshExpiresIn: 3600}, s.err
}

// TestRefreshHandlerContractAndBoundaries chứng minh handler không yêu cầu access JWT và giữ safe response contract.
func TestRefreshHandlerContractAndBoundaries(t *testing.T) {
	cases := []struct {
		name, body, content, code string
		err                       error
		status, calls             int
	}{
		{name: "success", body: `{"refresh_token":"submitted"}`, content: "application/json", status: 200, calls: 1},
		{name: "invalid credential", body: `{}`, content: "application/json", status: 401, code: "auth.invalid_refresh_token", err: ErrInvalidRefreshToken, calls: 1},
		{name: "null", body: `null`, content: "application/json", status: 400, code: "request.invalid_json"},
		{name: "unknown fields", body: `{"user_id":"forged"}`, content: "application/json", status: 400, code: "request.invalid_json"},
		{name: "trailing document", body: `{} {}`, content: "application/json", status: 400, code: "request.invalid_json"},
		{name: "wrong type", body: `{"refresh_token":42}`, content: "application/json", status: 400, code: "request.invalid_json"},
		{name: "media type", body: `{}`, status: 415, code: "request.unsupported_media_type"},
		{name: "oversized", body: `{"refresh_token":"` + strings.Repeat("x", int(maxLoginRequestBytes)) + `"}`, content: "application/json", status: 413, code: "request.body_too_large"},
		{name: "internal", body: `{}`, content: "application/json", status: 500, code: "auth.refresh_failed", err: errors.New("sensitive-token"), calls: 1},
		{name: "cancelled", body: `{}`, content: "application/json", status: 408, code: "request.cancelled", err: context.Canceled, calls: 1},
		{name: "timeout", body: `{}`, content: "application/json", status: 504, code: "request.timeout", err: context.DeadlineExceeded, calls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs strings.Builder
			usecase := &stubRefreshUseCase{err: tc.err}
			handler := NewRefreshHandler(usecase, slog.New(slog.NewTextHandler(&logs, nil)))
			request := httptest.NewRequest("POST", "/auth/refresh", strings.NewReader(tc.body))
			if tc.content != "" {
				request.Header.Set("Content-Type", tc.content)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if usecase.calls != tc.calls {
				t.Fatalf("calls = %d, want %d", usecase.calls, tc.calls)
			}
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Pragma") != "no-cache" {
				t.Fatal("token response can be cached")
			}
			if tc.status != 200 {
				assertLoginErrorResponse(t, response, tc.status, tc.code)
			} else {
				if response.Code != 200 || usecase.input != "submitted" {
					t.Fatal("refresh use case was not called correctly")
				}
				var pair loginHTTPResponse
				if err := json.NewDecoder(response.Body).Decode(&pair); err != nil {
					t.Fatal(err)
				}
				if pair.AccessToken != "jwt" || pair.RefreshToken != "replacement" || pair.RefreshExpiresIn != 3600 || pair.ExpiresIn != 900 {
					t.Fatal("token pair metadata missing")
				}
			}
			if strings.Contains(response.Body.String(), "sensitive-token") || strings.Contains(logs.String(), "sensitive-token") {
				t.Fatal("secret leaked into errors or logs")
			}
		})
	}
	// Explicitly ensure the nil-service failure path is safe.
	h := NewRefreshHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	response := performLoginRequest(h, `{}`, "application/json")
	assertLoginErrorResponse(t, response, 500, "auth.refresh_failed")
}
