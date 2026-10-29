// File này triển khai token-pair issuance, single-use refresh rotation và commit revocation khi phát hiện reuse.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"
)

var ErrInvalidRefreshToken = errors.New("invalid refresh token")

// IssuedTokenPair là response credential của login/refresh; token thô chỉ sống trong request/response memory.
type IssuedTokenPair struct {
	IssuedAccessToken
	RefreshToken     string
	RefreshExpiresIn int64
}

// RefreshSession mô tả deadline và owner session đã được đọc dưới transaction lock.
type RefreshSession struct {
	ID, UserID           string
	CreatedAt, ExpiresAt time.Time
	Revoked              bool
}

// RefreshRecord giữ digest và lineage; không chứa refresh token thô.
type RefreshRecord struct {
	ID, SessionID, PreviousID string
	Hash                      []byte
	CreatedAt, ExpiresAt      time.Time
	Consumed, Revoked         bool
}

// RefreshTransaction là các persistence operation cần chạy trên cùng transaction và session row lock.
type RefreshTransaction interface {
	CreateSession(context.Context, string, string, time.Duration) (RefreshSession, error)
	LockToken(context.Context, []byte) (RefreshSession, RefreshRecord, error)
	CurrentTime(context.Context) (time.Time, error)
	ActiveUser(context.Context, string) (bool, error)
	InsertToken(context.Context, RefreshRecord) error
	Consume(context.Context, string) error
	Touch(context.Context, string) error
	Revoke(context.Context, string) error
}

// RefreshRepository commit callback thành công; callback lỗi hoặc commit failure không trả credential ra client.
type RefreshRepository interface {
	Transact(context.Context, func(RefreshTransaction) error) error
}

// RefreshService điều phối credential lifecycle qua repository; dependencies hỗ trợ kiểm thử RNG/signing/commit failure.
type RefreshService struct {
	repository RefreshRepository
	issuer     AccessTokenIssuer
	lifetime   time.Duration
	random     io.Reader
	newID      func() (string, error)
}

// NewRefreshService kiểm tra policy và dùng crypto/rand cho token entropy, UUIDv7 cho database IDs.
func NewRefreshService(repository RefreshRepository, issuer AccessTokenIssuer, lifetime time.Duration) (*RefreshService, error) {
	if repository == nil || issuer == nil || lifetime < time.Minute || lifetime > 30*24*time.Hour {
		return nil, errors.New("refresh service requires dependencies and lifetime between 1m and 720h")
	}
	return &RefreshService{repository: repository, issuer: issuer, lifetime: lifetime, random: rand.Reader, newID: newUUIDv7}, nil
}

// newCredential sinh opaque 256-bit token và digest; không bao giờ persist raw value.
func (s *RefreshService) newCredential() (string, []byte, error) {
	var entropy [32]byte
	if _, err := io.ReadFull(s.random, entropy[:]); err != nil {
		return "", nil, errors.New("generate refresh credential failed")
	}
	value := base64.RawURLEncoding.EncodeToString(entropy[:])
	digest := sha256.Sum256([]byte(value))
	return value, digest[:], nil
}

// IssueLogin ghi session và initial digest atomically, chỉ trả pair sau khi database commit thành công.
func (s *RefreshService) IssueLogin(ctx context.Context, identity AuthenticatedIdentity) (IssuedTokenPair, error) {
	var pair IssuedTokenPair
	raw, digest, err := s.newCredential()
	if err != nil {
		return pair, err
	}
	sessionID, err := s.newID()
	if err != nil {
		return pair, errors.New("generate session ID failed")
	}
	tokenID, err := s.newID()
	if err != nil {
		return pair, errors.New("generate refresh ID failed")
	}
	err = s.repository.Transact(ctx, func(tx RefreshTransaction) error {
		active, err := tx.ActiveUser(ctx, identity.UserID)
		if err != nil {
			return err
		}
		if !active {
			return ErrInvalidCredentials
		}
		session, err := tx.CreateSession(ctx, sessionID, identity.UserID, s.lifetime)
		if err != nil {
			return err
		}
		if err := tx.InsertToken(ctx, RefreshRecord{ID: tokenID, SessionID: session.ID, Hash: digest, CreatedAt: session.CreatedAt, ExpiresAt: session.ExpiresAt}); err != nil {
			return err
		}
		access, err := s.issuer.Issue(identity)
		if err != nil {
			return errors.New("sign login access token failed")
		}
		now, err := tx.CurrentTime(ctx)
		if err != nil {
			return err
		}
		if !session.ExpiresAt.After(now) {
			return ErrInvalidCredentials
		}
		pair = IssuedTokenPair{IssuedAccessToken: access, RefreshToken: raw, RefreshExpiresIn: int64(session.ExpiresAt.Sub(now).Seconds())}
		return ctx.Err()
	})
	if err != nil {
		return IssuedTokenPair{}, fmt.Errorf("issue login session: %w", err)
	}
	return pair, nil
}

// Refresh khóa session trước token, rotate một lần hoặc commit thu hồi session trước khi trả reuse error.
func (s *RefreshService) Refresh(ctx context.Context, raw string) (IssuedTokenPair, error) {
	var pair IssuedTokenPair
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(decoded) != 32 || len(raw) != 43 {
		return pair, ErrInvalidRefreshToken
	}
	digest := sha256.Sum256([]byte(raw))
	reused := false
	err = s.repository.Transact(ctx, func(tx RefreshTransaction) error {
		session, token, err := tx.LockToken(ctx, digest[:])
		if err != nil {
			return err
		}
		now, err := tx.CurrentTime(ctx)
		if err != nil {
			return err
		}
		// Reuse được xét trước token expiry để expired predecessor không che một live descendant.
		if token.Consumed {
			if err := tx.Revoke(ctx, session.ID); err != nil {
				return err
			}
			reused = true
			return nil
		}
		if token.Revoked || session.Revoked || !token.ExpiresAt.After(now) || !session.ExpiresAt.After(now) {
			return ErrInvalidRefreshToken
		}
		active, err := tx.ActiveUser(ctx, session.UserID)
		if err != nil {
			return err
		}
		if !active {
			return ErrInvalidRefreshToken
		}
		value, hash, err := s.newCredential()
		if err != nil {
			return err
		}
		id, err := s.newID()
		if err != nil {
			return errors.New("generate successor ID failed")
		}
		access, err := s.issuer.Issue(AuthenticatedIdentity{UserID: session.UserID})
		if err != nil {
			return errors.New("sign refresh access token failed")
		}
		// Re-read clock after potentially expensive signing and User lock waits.
		now, err = tx.CurrentTime(ctx)
		if err != nil {
			return err
		}
		expiry := token.ExpiresAt
		if session.ExpiresAt.Before(expiry) {
			expiry = session.ExpiresAt
		}
		if !expiry.After(now) {
			return ErrInvalidRefreshToken
		}
		if err := tx.Consume(ctx, token.ID); err != nil {
			return err
		}
		if err := tx.InsertToken(ctx, RefreshRecord{ID: id, SessionID: session.ID, PreviousID: token.ID, Hash: hash, CreatedAt: now, ExpiresAt: expiry}); err != nil {
			return err
		}
		if err := tx.Touch(ctx, session.ID); err != nil {
			return err
		}
		pair = IssuedTokenPair{IssuedAccessToken: access, RefreshToken: value, RefreshExpiresIn: int64(expiry.Sub(now).Seconds())}
		return ctx.Err()
	})
	if err != nil {
		return IssuedTokenPair{}, fmt.Errorf("refresh session: %w", err)
	}
	if reused {
		return IssuedTokenPair{}, ErrInvalidRefreshToken
	}
	return pair, nil
}
