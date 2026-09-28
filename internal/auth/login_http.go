// File này cung cấp POST /auth/login: bounded credential JSON input, bearer-token response và generic credential errors.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
)

const maxLoginRequestBytes int64 = 16 * 1024

// LoginUseCase là contract HTTP handler cần để xác minh người dùng và phát access token.
type LoginUseCase interface {
	Login(ctx context.Context, input LoginInput) (IssuedAccessToken, error)
}

type loginHTTPRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginHTTPResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

type loginErrorResponse struct {
	Error loginErrorBody `json:"error"`
}

type loginErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// LoginHandler maps the login token use case to a bounded JSON HTTP contract.
type LoginHandler struct {
	login  LoginUseCase
	logger *slog.Logger
}

// NewLoginHandler creates a login HTTP adapter with a safe default logger.
func NewLoginHandler(login LoginUseCase, logger *slog.Logger) *LoginHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &LoginHandler{login: login, logger: logger}
}

// ServeHTTP decodes one bounded login request and never logs or returns submitted credentials or tokens.
func (h *LoginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if !hasLoginJSONContentType(r) {
		h.writeError(w, http.StatusUnsupportedMediaType, "request.unsupported_media_type", "Content-Type must be application/json")
		return
	}
	input, err := decodeLoginHTTPRequest(w, r)
	if err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			h.writeError(w, http.StatusRequestEntityTooLarge, "request.body_too_large", "Request body is too large")
			return
		}
		h.writeError(w, http.StatusBadRequest, "request.invalid_json", "Request body must be one valid JSON object")
		return
	}
	if h == nil || h.login == nil {
		h.writeError(w, http.StatusInternalServerError, "auth.login_failed", "Login could not be completed")
		return
	}

	token, err := h.login.Login(r.Context(), LoginInput{Email: input.Email, Password: input.Password})
	if err != nil {
		h.writeLoginError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, loginHTTPResponse{
		AccessToken: token.Value,
		TokenType:   token.TokenType,
		ExpiresIn:   token.ExpiresIn,
	})
}

// hasLoginJSONContentType accepts application/json with parameters and rejects ambiguous media types.
func hasLoginJSONContentType(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

// decodeLoginHTTPRequest bounds request bytes, rejects unknown fields and requires exactly one JSON document.
func decodeLoginHTTPRequest(w http.ResponseWriter, r *http.Request) (loginHTTPRequest, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input loginHTTPRequest
	if err := decoder.Decode(&input); err != nil {
		return loginHTTPRequest{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return loginHTTPRequest{}, errors.New("login request has trailing JSON data")
	}
	return input, nil
}

// writeLoginError maps generic credential errors and infrastructure failures without exposing internal details.
func (h *LoginHandler) writeLoginError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		h.writeError(w, http.StatusUnauthorized, "auth.invalid_credentials", "Email or password is incorrect")
	case errors.Is(err, context.DeadlineExceeded):
		h.writeError(w, http.StatusGatewayTimeout, "request.timeout", "Request timed out")
	case errors.Is(err, context.Canceled):
		h.writeError(w, http.StatusRequestTimeout, "request.cancelled", "Request was cancelled")
	default:
		h.logger.Error("login failed", "error", err)
		h.writeError(w, http.StatusInternalServerError, "auth.login_failed", "Login could not be completed")
	}
}

// writeError writes the stable login error envelope after setting the HTTP status and content type.
func (h *LoginHandler) writeError(w http.ResponseWriter, status int, code string, message string) {
	h.writeJSON(w, status, loginErrorResponse{Error: loginErrorBody{Code: code, Message: message}})
}

// writeJSON encodes the response once and logs only response metadata if the client disconnects.
func (h *LoginHandler) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		h.logger.Error("write login response failed", "error", err, "status", status)
	}
}
