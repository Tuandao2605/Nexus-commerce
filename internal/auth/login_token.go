// File này ghép login authentication với access-token issuer để chỉ phát token sau khi Credential và User đều hợp lệ.
package auth

import (
	"context"
	"errors"
	"fmt"
)

// LoginAuthenticator xác minh password và User status trước khi token layer được gọi.
type LoginAuthenticator interface {
	Authenticate(ctx context.Context, input LoginInput) (AuthenticatedIdentity, error)
}

// LoginTokenService điều phối đăng nhập và ký access token mà không lưu token trong database.
type LoginTokenService struct {
	authenticator LoginAuthenticator
	issuer        AccessTokenIssuer
}

// NewLoginTokenService injects the AUTH-003 authenticator and configured access-token issuer.
func NewLoginTokenService(authenticator LoginAuthenticator, issuer AccessTokenIssuer) (*LoginTokenService, error) {
	if authenticator == nil || issuer == nil {
		return nil, errors.New("login token service is not configured")
	}
	return &LoginTokenService{authenticator: authenticator, issuer: issuer}, nil
}

// Login authenticates the user first, then issues a short-lived bearer token for the returned identity.
func (s *LoginTokenService) Login(ctx context.Context, input LoginInput) (IssuedAccessToken, error) {
	if s == nil || s.authenticator == nil || s.issuer == nil {
		return IssuedAccessToken{}, errors.New("login token service is not configured")
	}
	identity, err := s.authenticator.Authenticate(ctx, input)
	if err != nil {
		return IssuedAccessToken{}, err
	}
	if err := ctx.Err(); err != nil {
		return IssuedAccessToken{}, err
	}
	token, err := s.issuer.Issue(identity)
	if err != nil {
		return IssuedAccessToken{}, fmt.Errorf("issue access token: %w", err)
	}
	return token, nil
}
