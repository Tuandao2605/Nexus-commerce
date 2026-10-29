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

// LoginTokenService xác thực Credential trước khi phát token pair và ghi session qua Auth boundary.
type LoginTokenService struct {
	authenticator LoginAuthenticator
	issuer        LoginSessionIssuer
}

// LoginSessionIssuer phát cặp token với session persistence atomically sau successful authentication.
type LoginSessionIssuer interface {
	IssueLogin(context.Context, AuthenticatedIdentity) (IssuedTokenPair, error)
}

// NewLoginTokenService injects the AUTH-003 authenticator and transactional session/token issuer.
func NewLoginTokenService(authenticator LoginAuthenticator, issuer LoginSessionIssuer) (*LoginTokenService, error) {
	if authenticator == nil || issuer == nil {
		return nil, errors.New("login token service is not configured")
	}
	return &LoginTokenService{authenticator: authenticator, issuer: issuer}, nil
}

// Login authenticates the user first, then persists a session and issues its initial token pair.
func (s *LoginTokenService) Login(ctx context.Context, input LoginInput) (IssuedTokenPair, error) {
	if s == nil || s.authenticator == nil || s.issuer == nil {
		return IssuedTokenPair{}, errors.New("login token service is not configured")
	}
	identity, err := s.authenticator.Authenticate(ctx, input)
	if err != nil {
		return IssuedTokenPair{}, err
	}
	if err := ctx.Err(); err != nil {
		return IssuedTokenPair{}, err
	}
	token, err := s.issuer.IssueLogin(ctx, identity)
	if err != nil {
		return IssuedTokenPair{}, fmt.Errorf("issue login token pair: %w", err)
	}
	return token, nil
}
