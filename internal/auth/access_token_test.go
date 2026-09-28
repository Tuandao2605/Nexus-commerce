// File này kiểm thử JWT access token: RS256 signature, claims, expiration, malformed input và policy bounds.
package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestRS256AccessTokenManagerIssuesAndValidatesProfileToken proves the token contains the configured identity and RFC 9068 access-token markers.
func TestRS256AccessTokenManagerIssuesAndValidatesProfileToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	manager, err := NewRS256AccessTokenManager(pemEncodePrivateKey(t, key), testAccessTokenConfig())
	if err != nil {
		t.Fatalf("NewRS256AccessTokenManager() error = %v", err)
	}

	issued, err := manager.Issue(AuthenticatedIdentity{UserID: "0199f9d2-2e83-7000-8000-000000000001", Email: "private@example.com"})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if issued.TokenType != "Bearer" || issued.ExpiresIn != int64(defaultAccessTokenLifetime.Seconds()) {
		t.Fatalf("Issue() metadata = %#v", issued)
	}
	if issued.Value == "" || len(issued.Value) > 8192 {
		t.Fatalf("Issue() token length = %d", len(issued.Value))
	}

	claims, err := manager.Validate(issued.Value)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	publicKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: mustMarshalPublicKey(t, &key.PublicKey)})
	verifier, err := NewRS256AccessTokenVerifier(publicKeyPEM, testAccessTokenConfig())
	if err != nil {
		t.Fatalf("NewRS256AccessTokenVerifier() error = %v", err)
	}
	if _, err := verifier.Validate(issued.Value); err != nil {
		t.Fatalf("public-key Validate() error = %v", err)
	}
	if claims.Subject != "0199f9d2-2e83-7000-8000-000000000001" ||
		claims.Issuer != "https://auth.example.test" ||
		claims.Audience != "nexus-api" ||
		claims.ClientID != "nexus-web" ||
		claims.TokenID == "" ||
		claims.ExpiresAt.Sub(claims.IssuedAt) > defaultAccessTokenLifetime {
		t.Fatalf("Validate() claims = %#v", claims)
	}
	if containsJWTHeaderValue(t, issued.Value, "typ") != "at+jwt" || containsJWTHeaderValue(t, issued.Value, "alg") != "RS256" {
		t.Fatal("access token header did not identify at+jwt and RS256")
	}
	if containsJWTPayloadValue(t, issued.Value, "email") != "" {
		t.Fatal("access token disclosed email")
	}
}

// TestRS256AccessTokenManagerRejectsTamperedAndWrongClaims checks signature, typ, algorithm, issuer, audience and expiration enforcement.
func TestRS256AccessTokenManagerRejectsTamperedAndWrongClaims(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	manager, err := NewRS256AccessTokenManager(pemEncodePrivateKey(t, key), testAccessTokenConfig())
	if err != nil {
		t.Fatalf("NewRS256AccessTokenManager() error = %v", err)
	}

	valid, err := manager.Issue(AuthenticatedIdentity{UserID: "user-1"})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	invalidTokens := []string{
		valid.Value[:len(valid.Value)-2] + "xx",
		signTestJWT(t, key, map[string]any{"alg": "none", "typ": "at+jwt", "kid": "test-key"}, testAccessTokenPayload("user-1", time.Now().Add(time.Minute).Unix(), "nexus-api")),
		signTestJWT(t, key, map[string]any{"alg": "RS256", "typ": "JWT", "kid": "test-key"}, testAccessTokenPayload("user-1", time.Now().Add(time.Minute).Unix(), "nexus-api")),
		signTestJWT(t, key, map[string]any{"alg": "RS256", "typ": "at+jwt", "kid": "test-key"}, testAccessTokenPayload("user-1", time.Now().Add(-time.Minute).Unix(), "nexus-api")),
		signTestJWT(t, key, map[string]any{"alg": "RS256", "typ": "at+jwt", "kid": "test-key"}, map[string]any{"iss": "wrong", "sub": "user-1", "aud": []string{"nexus-api"}, "client_id": "nexus-web", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(), "jti": "id"}),
		signTestJWT(t, key, map[string]any{"alg": "RS256", "typ": "at+jwt", "kid": "test-key"}, map[string]any{"iss": "https://auth.example.test", "sub": "user-1", "aud": []string{"wrong-audience"}, "client_id": "nexus-web", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(), "jti": "id"}),
		signTestJWT(t, key, map[string]any{"alg": "RS256", "typ": "at+jwt", "kid": "wrong-key"}, testAccessTokenPayload("user-1", time.Now().Add(time.Minute).Unix(), "nexus-api")),
	}
	for index, token := range invalidTokens {
		if _, err := manager.Validate(token); !errors.Is(err, ErrInvalidAccessToken) {
			t.Errorf("Validate(invalid token %d) error = %v, want ErrInvalidAccessToken", index, err)
		}
	}
	if _, err := manager.Validate("one.two"); !errors.Is(err, ErrInvalidAccessToken) {
		t.Fatalf("Validate(malformed) error = %v", err)
	}
}

// TestRS256AccessTokenManagerRejectsUnsafeKeyAndLifetime checks RSA minimum size and short token lifetime policy.
func TestRS256AccessTokenManagerRejectsUnsafeKeyAndLifetime(t *testing.T) {
	smallKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	if _, err := NewRS256AccessTokenManager(pemEncodePrivateKey(t, smallKey), testAccessTokenConfig()); err == nil {
		t.Fatal("NewRS256AccessTokenManager(1024-bit key) error = nil")
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	config := testAccessTokenConfig()
	config.Lifetime = 16 * time.Minute
	if _, err := NewRS256AccessTokenManager(pemEncodePrivateKey(t, key), config); err == nil {
		t.Fatal("NewRS256AccessTokenManager(16m) error = nil")
	}
	if _, err := NewRS256AccessTokenManager([]byte("not PEM"), testAccessTokenConfig()); err == nil {
		t.Fatal("NewRS256AccessTokenManager(invalid PEM) error = nil")
	}
}

// TestLoginTokenServiceIssuesOnlyAfterSuccessfulAuthentication ensures an auth failure cannot reach the token issuer.
func TestLoginTokenServiceIssuesOnlyAfterSuccessfulAuthentication(t *testing.T) {
	authenticator := &stubLoginAuthenticator{err: ErrInvalidCredentials}
	issuer := &stubAccessTokenIssuer{}
	service, err := NewLoginTokenService(authenticator, issuer)
	if err != nil {
		t.Fatalf("NewLoginTokenService() error = %v", err)
	}
	if _, err := service.Login(context.Background(), LoginInput{Email: "user@example.com", Password: "wrong"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
	if issuer.calls != 0 {
		t.Fatalf("Issue() calls = %d, want 0", issuer.calls)
	}
}

// testAccessTokenConfig returns a valid short-lived token policy with deterministic public claim values.
func testAccessTokenConfig() AccessTokenConfig {
	return AccessTokenConfig{Issuer: "https://auth.example.test", Audience: "nexus-api", ClientID: "nexus-web", KeyID: "test-key", Lifetime: defaultAccessTokenLifetime}
}

// pemEncodePrivateKey serializes the generated test RSA key as PKCS#1 PEM.
func pemEncodePrivateKey(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

// mustMarshalPublicKey serializes the generated RSA public key as SubjectPublicKeyInfo for verifier tests.
func mustMarshalPublicKey(t *testing.T, key *rsa.PublicKey) []byte {
	t.Helper()
	encoded, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey() error = %v", err)
	}
	return encoded
}

// splitJWT parses a compact JWT for negative assertions and fails on malformed fixture construction.
func splitJWT(t *testing.T, token string) []string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT parts = %d, want 3", len(parts))
	}
	return parts
}

// signTestJWT signs custom claims so validation tests exercise correctly signed but policy-invalid tokens.
func signTestJWT(t *testing.T, key *rsa.PrivateKey, header any, payload any) string {
	t.Helper()
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal test header: %v", err)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal test payload: %v", err)
	}
	input := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(payloadJSON)
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// testAccessTokenPayload constructs claims with the remaining configured values held constant.
func testAccessTokenPayload(subject string, expiresAt int64, audience string) map[string]any {
	return map[string]any{"iss": "https://auth.example.test", "sub": subject, "aud": []string{audience}, "exp": expiresAt, "iat": time.Now().Unix(), "jti": "test-id", "client_id": "nexus-web"}
}

// containsJWTHeaderValue decodes the signed header and returns one string field for contract assertions.
func containsJWTHeaderValue(t *testing.T, token string, field string) string {
	t.Helper()
	value := decodeJWTObjectField(t, strings.Split(token, ".")[0], field)
	return value
}

// containsJWTPayloadValue decodes the signed claims and returns one string field for disclosure assertions.
func containsJWTPayloadValue(t *testing.T, token string, field string) string {
	t.Helper()
	return decodeJWTObjectField(t, strings.Split(token, ".")[1], field)
}

// decodeJWTObjectField decodes one JSON JWT segment and returns the selected string field.
func decodeJWTObjectField(t *testing.T, segment string, field string) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("decode JWT segment: %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("unmarshal JWT segment: %v", err)
	}
	result, _ := value[field].(string)
	return result
}

type stubLoginAuthenticator struct {
	identity AuthenticatedIdentity
	err      error
}

// Authenticate returns the identity/error configured by the test.
func (s *stubLoginAuthenticator) Authenticate(_ context.Context, _ LoginInput) (AuthenticatedIdentity, error) {
	return s.identity, s.err
}

type stubAccessTokenIssuer struct {
	calls  int
	result IssuedAccessToken
}

// Issue records invocation so tests can prove failed authentication never signs a token.
func (s *stubAccessTokenIssuer) Issue(_ AuthenticatedIdentity) (IssuedAccessToken, error) {
	s.calls++
	return s.result, nil
}
