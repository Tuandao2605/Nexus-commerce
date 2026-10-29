// File này kiểm thử opaque-token entropy/format, digest-only data và rejection trước persistence.
package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

type unusedRefreshRepository struct{ calls int }

// Transact đếm DB access để malformed credentials/RNG failure không đụng persistence.
func (r *unusedRefreshRepository) Transact(context.Context, func(RefreshTransaction) error) error {
	r.calls++
	return errors.New("unexpected persistence access")
}

// TestRefreshCredentialGeneration chứng minh token canonical, đủ entropy, unique và digest hash đúng chuỗi client giữ.
func TestRefreshCredentialGeneration(t *testing.T) {
	repo := &unusedRefreshRepository{}
	service, err := NewRefreshService(repo, &stubAccessTokenIssuer{}, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	first, digest, err := service.newCredential()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(first)
	if err != nil || len(decoded) != 32 || len(first) != 43 {
		t.Fatal("refresh token format is not canonical 256-bit Base64URL")
	}
	expected := sha256.Sum256([]byte(first))
	if !bytes.Equal(digest, expected[:]) {
		t.Fatal("stored digest differs from SHA-256 of submitted value")
	}
	second, _, err := service.newCredential()
	if err != nil || first == second {
		t.Fatal("fresh refresh credential was not unique")
	}
	service.random = strings.NewReader("short entropy")
	if pair, err := service.IssueLogin(context.Background(), AuthenticatedIdentity{UserID: "user"}); err == nil || pair != (IssuedTokenPair{}) || repo.calls != 0 {
		t.Fatal("RNG failure must not issue credentials or access persistence")
	}
}

// TestRefreshRejectsMalformedBeforeDatabase xác nhận token thiếu/sai alphabet/sai độ dài không tới DB.
func TestRefreshRejectsMalformedBeforeDatabase(t *testing.T) {
	repo := &unusedRefreshRepository{}
	service, err := NewRefreshService(repo, &stubAccessTokenIssuer{}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "secret", strings.Repeat("a", 42), strings.Repeat("a", 44), strings.Repeat("+", 43), strings.Repeat("A", 42) + "B"} {
		if pair, err := service.Refresh(context.Background(), token); !errors.Is(err, ErrInvalidRefreshToken) || pair != (IssuedTokenPair{}) {
			t.Fatal("malformed refresh token was not rejected")
		}
	}
	if repo.calls != 0 {
		t.Fatal("malformed token reached database")
	}
}

// TestRefreshServiceRejectsInvalidDependenciesAndPolicy giữ constructor fail-closed với missing dependency và lifetime ngoài giới hạn.
func TestRefreshServiceRejectsInvalidDependenciesAndPolicy(t *testing.T) {
	repo := &unusedRefreshRepository{}
	issuer := &stubAccessTokenIssuer{}
	for _, lifetime := range []time.Duration{0, 59 * time.Second, 721 * time.Hour} {
		if _, err := NewRefreshService(repo, issuer, lifetime); err == nil {
			t.Fatal("invalid lifetime accepted")
		}
	}
	if _, err := NewRefreshService(nil, issuer, time.Hour); err == nil {
		t.Fatal("nil repository accepted")
	}
	if _, err := NewRefreshService(repo, nil, time.Hour); err == nil {
		t.Fatal("nil issuer accepted")
	}
}
