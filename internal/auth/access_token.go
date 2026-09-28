// File này phát và xác minh JWT access token RS256 theo contract của AUTH-004.
package auth

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	defaultAccessTokenLifetime = 15 * time.Minute
	maximumAccessTokenLifetime = 15 * time.Minute
	accessTokenClockSkew       = 30 * time.Second
	minimumRSABits             = 2048
	maximumKeyPEMBytes         = 64 * 1024
)

var ErrInvalidAccessToken = errors.New("invalid access token")

// AccessTokenConfig chứa issuer, audience, client id, key id và thời hạn ngắn của access token.
type AccessTokenConfig struct {
	Issuer   string
	Audience string
	ClientID string
	KeyID    string
	Lifetime time.Duration
}

// AccessTokenClaims là tập claim đã được xác minh mà resource handler có thể tin cậy.
type AccessTokenClaims struct {
	Issuer    string
	Subject   string
	Audience  string
	ClientID  string
	TokenID   string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// IssuedAccessToken là token bearer cùng thời gian hết hạn, không chứa dữ liệu credential.
type IssuedAccessToken struct {
	Value     string
	TokenType string
	ExpiresIn int64
}

// AccessTokenIssuer định nghĩa thao tác phát token để Login use case không phụ thuộc crypto implementation.
type AccessTokenIssuer interface {
	Issue(identity AuthenticatedIdentity) (IssuedAccessToken, error)
}

// RS256AccessTokenManager ký và xác minh access token bằng cùng một cặp RSA key.
type RS256AccessTokenManager struct {
	privateKey *rsa.PrivateKey
	config     AccessTokenConfig
	verifier   *RS256AccessTokenVerifier
}

// RS256AccessTokenVerifier validates signed tokens using public key material only.
type RS256AccessTokenVerifier struct {
	publicKey *rsa.PublicKey
	config    AccessTokenConfig
}

// accessTokenHeader chỉ chấp nhận algorithm, media type và key id mà issuer này cấu hình.
type accessTokenHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
	KeyID     string `json:"kid"`
}

// accessTokenPayload mang các claim cốt lõi của JWT access token.
type accessTokenPayload struct {
	Issuer    string   `json:"iss"`
	Subject   string   `json:"sub"`
	Audience  []string `json:"aud"`
	ExpiresAt int64    `json:"exp"`
	IssuedAt  int64    `json:"iat"`
	TokenID   string   `json:"jti"`
	ClientID  string   `json:"client_id"`
}

// NewRS256AccessTokenManagerFromFile reads a PEM private key and builds a bounded RS256 issuer/verifier.
func NewRS256AccessTokenManagerFromFile(path string, config AccessTokenConfig) (*RS256AccessTokenManager, error) {
	privateKeyPEM, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read access token signing key: %w", err)
	}
	manager, err := NewRS256AccessTokenManager(privateKeyPEM, config)
	clear(privateKeyPEM)
	return manager, err
}

// NewRS256AccessTokenManager parses a PKCS#1 or PKCS#8 RSA private key and validates token policy.
func NewRS256AccessTokenManager(privateKeyPEM []byte, config AccessTokenConfig) (*RS256AccessTokenManager, error) {
	privateKey, err := parseRSAPrivateKey(privateKeyPEM)
	if err != nil {
		return nil, err
	}
	if err := validateAccessTokenConfig(config); err != nil {
		return nil, err
	}
	verifier := &RS256AccessTokenVerifier{publicKey: &privateKey.PublicKey, config: config}
	return &RS256AccessTokenManager{privateKey: privateKey, config: config, verifier: verifier}, nil
}

// NewRS256AccessTokenVerifier parses a public RSA key so resource servers never need signing-key access.
func NewRS256AccessTokenVerifier(publicKeyPEM []byte, config AccessTokenConfig) (*RS256AccessTokenVerifier, error) {
	if err := validateAccessTokenConfig(config); err != nil {
		return nil, err
	}
	publicKey, err := parseRSAPublicKey(publicKeyPEM)
	if err != nil {
		return nil, err
	}
	return &RS256AccessTokenVerifier{publicKey: publicKey, config: config}, nil
}

// Issue signs a short-lived access JWT with explicit issuer, audience, subject, client, issue, expiry and unique ID claims.
func (m *RS256AccessTokenManager) Issue(identity AuthenticatedIdentity) (IssuedAccessToken, error) {
	if m == nil || m.privateKey == nil {
		return IssuedAccessToken{}, errors.New("access token manager is not configured")
	}
	subject := strings.TrimSpace(identity.UserID)
	if subject == "" {
		return IssuedAccessToken{}, errors.New("access token subject is required")
	}

	tokenIDBytes := make([]byte, 16)
	if _, err := rand.Read(tokenIDBytes); err != nil {
		return IssuedAccessToken{}, fmt.Errorf("generate access token ID: %w", err)
	}
	now := time.Now().UTC()
	expiresAt := now.Add(m.config.Lifetime)
	payload := accessTokenPayload{
		Issuer:    m.config.Issuer,
		Subject:   subject,
		Audience:  []string{m.config.Audience},
		ExpiresAt: expiresAt.Unix(),
		IssuedAt:  now.Unix(),
		TokenID:   base64.RawURLEncoding.EncodeToString(tokenIDBytes),
		ClientID:  m.config.ClientID,
	}
	header := accessTokenHeader{Algorithm: "RS256", Type: "at+jwt", KeyID: m.config.KeyID}
	encodedHeader, err := jsonBase64(header)
	if err != nil {
		return IssuedAccessToken{}, fmt.Errorf("encode access token header: %w", err)
	}
	encodedPayload, err := jsonBase64(payload)
	if err != nil {
		return IssuedAccessToken{}, fmt.Errorf("encode access token claims: %w", err)
	}
	signingInput := encodedHeader + "." + encodedPayload
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, m.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return IssuedAccessToken{}, fmt.Errorf("sign access token: %w", err)
	}
	value := signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
	return IssuedAccessToken{
		Value:     value,
		TokenType: "Bearer",
		ExpiresIn: int64(m.config.Lifetime.Seconds()),
	}, nil
}

// Validate delegates validation to the public-key verifier held by the token manager.
func (m *RS256AccessTokenManager) Validate(value string) (AccessTokenClaims, error) {
	if m == nil || m.verifier == nil {
		return AccessTokenClaims{}, ErrInvalidAccessToken
	}
	return m.verifier.Validate(value)
}

// Validate verifies RS256 signature, token type, configured key/claims, and bounded time claims.
func (v *RS256AccessTokenVerifier) Validate(value string) (AccessTokenClaims, error) {
	if v == nil || v.publicKey == nil || len(value) > 8192 {
		return AccessTokenClaims{}, ErrInvalidAccessToken
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return AccessTokenClaims{}, ErrInvalidAccessToken
	}
	headerBytes, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		return AccessTokenClaims{}, ErrInvalidAccessToken
	}
	var header accessTokenHeader
	if err := decodeSingleJSON(headerBytes, &header); err != nil ||
		header.Algorithm != "RS256" || header.Type != "at+jwt" || header.KeyID != v.config.KeyID {
		return AccessTokenClaims{}, ErrInvalidAccessToken
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil {
		return AccessTokenClaims{}, ErrInvalidAccessToken
	}
	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(v.publicKey, crypto.SHA256, digest[:], signature); err != nil {
		return AccessTokenClaims{}, ErrInvalidAccessToken
	}

	payloadBytes, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return AccessTokenClaims{}, ErrInvalidAccessToken
	}
	var payload accessTokenPayload
	if err := decodeSingleJSON(payloadBytes, &payload); err != nil {
		return AccessTokenClaims{}, ErrInvalidAccessToken
	}
	now := time.Now().UTC()
	issuedAt := time.Unix(payload.IssuedAt, 0)
	expiresAt := time.Unix(payload.ExpiresAt, 0)
	if payload.Issuer != v.config.Issuer ||
		payload.Subject == "" ||
		!hasAudience(payload.Audience, v.config.Audience) ||
		payload.ClientID != v.config.ClientID ||
		payload.TokenID == "" ||
		payload.IssuedAt <= 0 ||
		issuedAt.After(now.Add(accessTokenClockSkew)) ||
		!expiresAt.After(now) ||
		!expiresAt.After(issuedAt) ||
		expiresAt.Sub(issuedAt) > v.config.Lifetime+time.Second {
		return AccessTokenClaims{}, ErrInvalidAccessToken
	}

	return AccessTokenClaims{
		Issuer:    payload.Issuer,
		Subject:   payload.Subject,
		Audience:  v.config.Audience,
		ClientID:  payload.ClientID,
		TokenID:   payload.TokenID,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
	}, nil
}

// parseRSAPrivateKey accepts unencrypted PKCS#1/PKCS#8 PEM keys and rejects undersized keys.
func parseRSAPrivateKey(value []byte) (*rsa.PrivateKey, error) {
	if len(value) == 0 || len(value) > maximumKeyPEMBytes {
		return nil, errors.New("access token signing key size is invalid")
	}
	block, rest := pem.Decode(value)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 || block.Type != "RSA PRIVATE KEY" && block.Type != "PRIVATE KEY" {
		return nil, errors.New("access token signing key must be an unencrypted RSA PKCS#1 or PKCS#8 PEM")
	}
	var privateKey *rsa.PrivateKey
	if block.Type == "RSA PRIVATE KEY" {
		parsed, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("access token signing key is invalid")
		}
		privateKey = parsed
	} else {
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("access token signing key is invalid")
		}
		var ok bool
		privateKey, ok = parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("access token signing key must use RSA")
		}
	}
	if privateKey.N.BitLen() < minimumRSABits {
		return nil, fmt.Errorf("access token RSA key must be at least %d bits", minimumRSABits)
	}
	if err := privateKey.Validate(); err != nil {
		return nil, errors.New("access token signing key is invalid")
	}
	return privateKey, nil
}

// parseRSAPublicKey accepts a SubjectPublicKeyInfo PEM and rejects undersized or non-RSA public keys.
func parseRSAPublicKey(value []byte) (*rsa.PublicKey, error) {
	if len(value) == 0 || len(value) > maximumKeyPEMBytes {
		return nil, errors.New("access token verification key size is invalid")
	}
	block, rest := pem.Decode(value)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 || block.Type != "PUBLIC KEY" {
		return nil, errors.New("access token verification key must be an RSA PUBLIC KEY PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("access token verification key is invalid")
	}
	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("access token verification key must use RSA")
	}
	if publicKey.N.BitLen() < minimumRSABits {
		return nil, fmt.Errorf("access token RSA key must be at least %d bits", minimumRSABits)
	}
	return publicKey, nil
}

// validateAccessTokenConfig rejects missing or ambiguous JWT identity settings and excessive token lifetimes.
func validateAccessTokenConfig(config AccessTokenConfig) error {
	if strings.TrimSpace(config.Issuer) == "" || strings.TrimSpace(config.Audience) == "" ||
		strings.TrimSpace(config.ClientID) == "" || strings.TrimSpace(config.KeyID) == "" {
		return errors.New("access token issuer, audience, client ID and key ID are required")
	}
	if config.Lifetime < time.Minute || config.Lifetime > maximumAccessTokenLifetime {
		return fmt.Errorf("access token lifetime must be between 1m and %s", maximumAccessTokenLifetime)
	}
	return nil
}

// jsonBase64 encodes a struct as compact JSON and unpadded base64url for JWT segments.
func jsonBase64(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

// decodeSingleJSON rejects malformed, unknown-field, or trailing data in signed JWT headers and claims.
func decodeSingleJSON(value []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing data")
	}
	return nil
}

// hasAudience reports whether the configured resource audience occurs in the token audience claim.
func hasAudience(audiences []string, expected string) bool {
	for _, audience := range audiences {
		if audience == expected {
			return true
		}
	}
	return false
}
