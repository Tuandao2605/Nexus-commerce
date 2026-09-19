// File này cung cấp HTTP boundary cho registration: bounded JSON decode, explicit DTO và stable success/error responses.
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

const maxRegistrationRequestBytes int64 = 16 * 1024

// RegistrationUseCase là contract tối thiểu HTTP handler cần từ application registration service.
type RegistrationUseCase interface {
	// Register xử lý một registration request và không trả password hoặc password hash.
	Register(ctx context.Context, input RegisterInput) (RegisteredUser, error)
}

// RegistrationHandler chuyển HTTP JSON request thành registration use case và map typed errors ra public contract.
type RegistrationHandler struct {
	registrar RegistrationUseCase
	logger    *slog.Logger
}

type registrationHTTPRequest struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Password    string `json:"password"`
}

type registrationHTTPResponse struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

type registrationErrorResponse struct {
	Error registrationErrorBody `json:"error"`
}

type registrationErrorBody struct {
	Code    string                    `json:"code"`
	Message string                    `json:"message"`
	Details []registrationErrorDetail `json:"details,omitempty"`
}

type registrationErrorDetail struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}

// NewRegistrationHandler tạo HTTP adapter với registrar bắt buộc và logger không chứa request payload.
func NewRegistrationHandler(registrar RegistrationUseCase, logger *slog.Logger) *RegistrationHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &RegistrationHandler{registrar: registrar, logger: logger}
}

// ServeHTTP xử lý POST registration, giới hạn body, từ chối field lạ và không bao giờ echo password/hash.
func (h *RegistrationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !hasJSONContentType(r) {
		h.writeError(w, http.StatusUnsupportedMediaType, "request.unsupported_media_type", "Content-Type must be application/json", nil)
		return
	}

	request, err := decodeRegistrationHTTPRequest(w, r)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			h.writeError(w, http.StatusRequestEntityTooLarge, "request.body_too_large", "Request body is too large", nil)
			return
		}
		h.writeError(w, http.StatusBadRequest, "request.invalid_json", "Request body must be one valid JSON object", nil)
		return
	}

	if h == nil || h.registrar == nil {
		h.writeError(w, http.StatusInternalServerError, "auth.registration_failed", "Registration could not be completed", nil)
		return
	}

	registeredUser, err := h.registrar.Register(r.Context(), RegisterInput{
		DisplayName: request.DisplayName,
		Email:       request.Email,
		Password:    request.Password,
	})
	if err != nil {
		h.writeRegistrationError(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, registrationHTTPResponse{
		ID:          registeredUser.ID,
		DisplayName: registeredUser.DisplayName,
		Email:       registeredUser.Email,
	})
}

// hasJSONContentType parse media type để chấp nhận application/json có charset nhưng từ chối content type mơ hồ.
func hasJSONContentType(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

// decodeRegistrationHTTPRequest giới hạn body ở 16 KiB, reject unknown fields và yêu cầu đúng một JSON document.
func decodeRegistrationHTTPRequest(w http.ResponseWriter, r *http.Request) (registrationHTTPRequest, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRegistrationRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var request registrationHTTPRequest
	if err := decoder.Decode(&request); err != nil {
		return registrationHTTPRequest{}, err
	}

	var trailingValue any
	if err := decoder.Decode(&trailingValue); !errors.Is(err, io.EOF) {
		if err == nil {
			return registrationHTTPRequest{}, errors.New("registration request contains multiple JSON values")
		}
		return registrationHTTPRequest{}, err
	}

	return request, nil
}

// writeRegistrationError map validation/conflict/timeout errors và che toàn bộ internal failure khỏi client.
func (h *RegistrationHandler) writeRegistrationError(w http.ResponseWriter, err error) {
	var fieldError *FieldError
	switch {
	case errors.As(err, &fieldError):
		h.writeError(
			w,
			http.StatusBadRequest,
			"auth.invalid_registration",
			"Registration request is invalid",
			[]registrationErrorDetail{{Field: fieldError.Field, Code: fieldError.Code}},
		)
	case errors.Is(err, ErrEmailAlreadyRegistered):
		h.writeError(w, http.StatusConflict, "auth.email_already_registered", "Email is already registered", nil)
	case errors.Is(err, context.DeadlineExceeded):
		h.writeError(w, http.StatusGatewayTimeout, "request.timeout", "Request timed out", nil)
	case errors.Is(err, context.Canceled):
		h.writeError(w, http.StatusRequestTimeout, "request.cancelled", "Request was cancelled", nil)
	default:
		h.logger.Error("registration failed", "error", err)
		h.writeError(w, http.StatusInternalServerError, "auth.registration_failed", "Registration could not be completed", nil)
	}
}

// writeError tạo error envelope thống nhất và không đưa internal error text vào response.
func (h *RegistrationHandler) writeError(
	w http.ResponseWriter,
	status int,
	code string,
	message string,
	details []registrationErrorDetail,
) {
	h.writeJSON(w, status, registrationErrorResponse{
		Error: registrationErrorBody{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}

// writeJSON đặt status/content type một lần và log write failure mà không log request body hoặc credential.
func (h *RegistrationHandler) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		h.logger.Error("write registration response failed", "error", err, "status", status)
	}
}
