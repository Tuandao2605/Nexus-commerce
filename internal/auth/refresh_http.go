// File này nhận POST /auth/refresh dạng JSON và trả cặp token mà không log credential hay yêu cầu access JWT.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

// RefreshUseCase là application boundary duy nhất refresh HTTP adapter cần gọi.
type RefreshUseCase interface {
	Refresh(context.Context, string) (IssuedTokenPair, error)
}

// RefreshHandler decode bounded input và dùng cùng safe JSON response conventions với login.
type RefreshHandler struct {
	service  RefreshUseCase
	response *LoginHandler
}

// NewRefreshHandler inject use case và logger, không chứa business/persistence logic.
func NewRefreshHandler(service RefreshUseCase, logger *slog.Logger) *RefreshHandler {
	return &RefreshHandler{service: service, response: NewLoginHandler(nil, logger)}
}

// ServeHTTP validate đúng một JSON object rồi đổi refresh credential qua request context.
func (h *RefreshHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if !hasLoginJSONContentType(r) {
		h.response.writeError(w, 415, "request.unsupported_media_type", "Content-Type must be application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input *struct {
		Token string `json:"refresh_token"`
	}
	err := decoder.Decode(&input)
	if err == nil {
		var extra any
		if trailing := decoder.Decode(&extra); !errors.Is(trailing, io.EOF) {
			if trailing != nil {
				err = trailing
			} else {
				err = errors.New("trailing JSON")
			}
		}
	}
	if err != nil || input == nil {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			h.response.writeError(w, 413, "request.body_too_large", "Request body is too large")
		} else {
			h.response.writeError(w, 400, "request.invalid_json", "Request body must be one valid JSON object")
		}
		return
	}
	if h.service == nil {
		h.response.writeError(w, 500, "auth.refresh_failed", "Refresh could not be completed")
		return
	}
	pair, err := h.service.Refresh(r.Context(), input.Token)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidRefreshToken):
			h.response.writeError(w, 401, "auth.invalid_refresh_token", "Refresh token is invalid")
		case errors.Is(err, context.Canceled):
			h.response.writeError(w, 408, "request.cancelled", "Request was cancelled")
		case errors.Is(err, context.DeadlineExceeded):
			h.response.writeError(w, 504, "request.timeout", "Request timed out")
		default:
			// Adapter trả log metadata cố định; lỗi dependency có thể vô tình mang raw token.
			h.response.logger.Error("refresh failed")
			h.response.writeError(w, 500, "auth.refresh_failed", "Refresh could not be completed")
		}
		return
	}
	h.response.writeJSON(w, 200, loginHTTPResponse{AccessToken: pair.Value, TokenType: pair.TokenType, ExpiresIn: pair.ExpiresIn, RefreshToken: pair.RefreshToken, RefreshExpiresIn: pair.RefreshExpiresIn})
}
