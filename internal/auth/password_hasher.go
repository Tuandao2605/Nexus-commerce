// File này triển khai password hashing Argon2id, PHC encoding, constant-time verification và rehash detection cho AUTH-002.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	defaultArgon2MemoryKiB   uint32 = 19 * 1024
	defaultArgon2Iterations  uint32 = 2
	defaultArgon2Parallelism uint8  = 1
	defaultArgon2SaltLength  uint32 = 16
	defaultArgon2KeyLength   uint32 = 32

	minimumArgon2MemoryKiB   uint32 = 8
	maximumArgon2MemoryKiB   uint32 = 256 * 1024
	maximumArgon2Iterations  uint32 = 10
	maximumArgon2Parallelism uint8  = 16
	minimumArgon2SaltLength         = 8
	maximumArgon2SaltLength         = 64
	minimumArgon2KeyLength          = 16
	maximumArgon2KeyLength          = 64
	maximumEncodedHashLength        = 512
)

var (
	ErrInvalidPassword     = errors.New("password is invalid")
	ErrPasswordMismatch    = errors.New("password does not match")
	ErrInvalidPasswordHash = errors.New("encoded password hash is invalid")
)

// Argon2idParams chứa work factor cùng kích thước salt/key được encode vào PHC string.
type Argon2idParams struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultArgon2idParams trả baseline Argon2id versioned trong source để thay đổi sau này kích hoạt rehash rõ ràng.
func DefaultArgon2idParams() Argon2idParams {
	return Argon2idParams{
		MemoryKiB:   defaultArgon2MemoryKiB,
		Iterations:  defaultArgon2Iterations,
		Parallelism: defaultArgon2Parallelism,
		SaltLength:  defaultArgon2SaltLength,
		KeyLength:   defaultArgon2KeyLength,
	}
}

// Argon2idHasher tạo và xác minh PHC-encoded Argon2id password hashes bằng một policy được kiểm tra khi khởi tạo.
type Argon2idHasher struct {
	params Argon2idParams
}

// NewArgon2idHasher tạo hasher và từ chối policy yếu hơn baseline hoặc có resource cost không an toàn.
func NewArgon2idHasher(params Argon2idParams) (*Argon2idHasher, error) {
	if err := validateHashingParams(params); err != nil {
		return nil, err
	}

	return &Argon2idHasher{params: params}, nil
}

// Hash tạo salt bằng crypto/rand, derive Argon2id key rồi serialize đầy đủ algorithm/version/parameters theo PHC format.
func (h *Argon2idHasher) Hash(password string) (string, error) {
	if h == nil {
		return "", ErrRegistrationUnavailable
	}
	if len(password) == 0 || len(password) > maxPasswordBytes {
		return "", ErrInvalidPassword
	}

	salt := make([]byte, h.params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}

	passwordBytes := []byte(password)
	derivedKey := argon2.IDKey(
		passwordBytes,
		salt,
		h.params.Iterations,
		h.params.MemoryKiB,
		h.params.Parallelism,
		h.params.KeyLength,
	)
	clear(passwordBytes)

	encoded := encodeArgon2idHash(h.params, salt, derivedKey)
	clear(derivedKey)
	return encoded, nil
}

// Verify parse hash trong giới hạn an toàn, derive candidate key và so sánh constant-time để tránh timing leak theo prefix.
func (h *Argon2idHasher) Verify(password, encodedHash string) error {
	if h == nil {
		return ErrInvalidPasswordHash
	}
	if len(password) == 0 || len(password) > maxPasswordBytes {
		return ErrPasswordMismatch
	}

	params, salt, expectedKey, err := parseArgon2idHash(encodedHash)
	if err != nil {
		return err
	}

	passwordBytes := []byte(password)
	actualKey := argon2.IDKey(
		passwordBytes,
		salt,
		params.Iterations,
		params.MemoryKiB,
		params.Parallelism,
		params.KeyLength,
	)
	clear(passwordBytes)
	defer clear(actualKey)
	defer clear(expectedKey)

	if subtle.ConstantTimeCompare(actualKey, expectedKey) != 1 {
		return ErrPasswordMismatch
	}

	return nil
}

// NeedsRehash cho biết hash hợp lệ có khác policy hiện tại hay không để Login ticket có thể nâng cấp sau khi verify thành công.
func (h *Argon2idHasher) NeedsRehash(encodedHash string) (bool, error) {
	if h == nil {
		return false, ErrInvalidPasswordHash
	}

	params, _, key, err := parseArgon2idHash(encodedHash)
	if err != nil {
		return false, err
	}
	clear(key)

	return params != h.params, nil
}

// validateHashingParams bảo đảm hash mới đạt baseline và không thể cấu hình vượt giới hạn tài nguyên đã review.
func validateHashingParams(params Argon2idParams) error {
	if params.MemoryKiB < defaultArgon2MemoryKiB || params.MemoryKiB > maximumArgon2MemoryKiB {
		return fmt.Errorf("Argon2id memory must be between %d and %d KiB", defaultArgon2MemoryKiB, maximumArgon2MemoryKiB)
	}
	if params.Iterations < defaultArgon2Iterations || params.Iterations > maximumArgon2Iterations {
		return fmt.Errorf("Argon2id iterations must be between %d and %d", defaultArgon2Iterations, maximumArgon2Iterations)
	}
	if params.Parallelism < defaultArgon2Parallelism || params.Parallelism > maximumArgon2Parallelism {
		return fmt.Errorf("Argon2id parallelism must be between %d and %d", defaultArgon2Parallelism, maximumArgon2Parallelism)
	}
	if params.MemoryKiB < minimumArgon2MemoryKiB*uint32(params.Parallelism) {
		return fmt.Errorf("Argon2id memory is too small for parallelism")
	}
	if params.SaltLength < defaultArgon2SaltLength || params.SaltLength > maximumArgon2SaltLength {
		return fmt.Errorf("Argon2id salt length must be between %d and %d bytes", defaultArgon2SaltLength, maximumArgon2SaltLength)
	}
	if params.KeyLength < defaultArgon2KeyLength || params.KeyLength > maximumArgon2KeyLength {
		return fmt.Errorf("Argon2id key length must be between %d and %d bytes", defaultArgon2KeyLength, maximumArgon2KeyLength)
	}

	return nil
}

// validateParsedParams giới hạn hash đọc từ database trước khi Argon2 cấp phát memory hoặc chạy CPU-intensive work.
func validateParsedParams(params Argon2idParams) error {
	if params.MemoryKiB < minimumArgon2MemoryKiB || params.MemoryKiB > maximumArgon2MemoryKiB {
		return ErrInvalidPasswordHash
	}
	if params.Iterations == 0 || params.Iterations > maximumArgon2Iterations {
		return ErrInvalidPasswordHash
	}
	if params.Parallelism == 0 || params.Parallelism > maximumArgon2Parallelism {
		return ErrInvalidPasswordHash
	}
	if params.MemoryKiB < minimumArgon2MemoryKiB*uint32(params.Parallelism) {
		return ErrInvalidPasswordHash
	}
	if params.SaltLength < minimumArgon2SaltLength || params.SaltLength > maximumArgon2SaltLength {
		return ErrInvalidPasswordHash
	}
	if params.KeyLength < minimumArgon2KeyLength || params.KeyLength > maximumArgon2KeyLength {
		return ErrInvalidPasswordHash
	}

	return nil
}

// encodeArgon2idHash serialize hash bằng unpadded Base64 và PHC fields để mỗi record tự mô tả policy đã dùng.
func encodeArgon2idHash(params Argon2idParams, salt, key []byte) string {
	encoding := base64.RawStdEncoding
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		params.MemoryKiB,
		params.Iterations,
		params.Parallelism,
		encoding.EncodeToString(salt),
		encoding.EncodeToString(key),
	)
}

// parseArgon2idHash parse strict PHC fields, decode bounded salt/key và không chạy KDF khi format hoặc cost không hợp lệ.
func parseArgon2idHash(encodedHash string) (Argon2idParams, []byte, []byte, error) {
	if len(encodedHash) == 0 || len(encodedHash) > maximumEncodedHashLength {
		return Argon2idParams{}, nil, nil, ErrInvalidPasswordHash
	}

	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Argon2idParams{}, nil, nil, ErrInvalidPasswordHash
	}

	version, err := parsePrefixedUint(parts[2], "v=", 8)
	if err != nil || int(version) != argon2.Version {
		return Argon2idParams{}, nil, nil, ErrInvalidPasswordHash
	}

	params, err := parseArgon2idParams(parts[3])
	if err != nil {
		return Argon2idParams{}, nil, nil, err
	}

	encoding := base64.RawStdEncoding.Strict()
	salt, err := encoding.DecodeString(parts[4])
	if err != nil {
		return Argon2idParams{}, nil, nil, ErrInvalidPasswordHash
	}
	key, err := encoding.DecodeString(parts[5])
	if err != nil {
		return Argon2idParams{}, nil, nil, ErrInvalidPasswordHash
	}
	params.SaltLength = uint32(len(salt))
	params.KeyLength = uint32(len(key))
	if err := validateParsedParams(params); err != nil {
		clear(key)
		return Argon2idParams{}, nil, nil, err
	}

	return params, salt, key, nil
}

// parseArgon2idParams yêu cầu chính xác m,t,p theo thứ tự canonical để loại bỏ format mơ hồ hoặc field lặp.
func parseArgon2idParams(value string) (Argon2idParams, error) {
	fields := strings.Split(value, ",")
	if len(fields) != 3 {
		return Argon2idParams{}, ErrInvalidPasswordHash
	}

	memory, err := parsePrefixedUint(fields[0], "m=", 32)
	if err != nil {
		return Argon2idParams{}, ErrInvalidPasswordHash
	}
	iterations, err := parsePrefixedUint(fields[1], "t=", 32)
	if err != nil {
		return Argon2idParams{}, ErrInvalidPasswordHash
	}
	parallelism, err := parsePrefixedUint(fields[2], "p=", 8)
	if err != nil {
		return Argon2idParams{}, ErrInvalidPasswordHash
	}

	params := Argon2idParams{
		MemoryKiB:   uint32(memory),
		Iterations:  uint32(iterations),
		Parallelism: uint8(parallelism),
	}
	if err := validateParsedParams(Argon2idParams{
		MemoryKiB:   params.MemoryKiB,
		Iterations:  params.Iterations,
		Parallelism: params.Parallelism,
		SaltLength:  minimumArgon2SaltLength,
		KeyLength:   minimumArgon2KeyLength,
	}); err != nil {
		return Argon2idParams{}, err
	}

	return params, nil
}

// parsePrefixedUint parse một unsigned decimal field và từ chối prefix thiếu, value rỗng, dấu hoặc overflow.
func parsePrefixedUint(value, prefix string, bitSize int) (uint64, error) {
	if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) {
		return 0, ErrInvalidPasswordHash
	}

	parsed, err := strconv.ParseUint(value[len(prefix):], 10, bitSize)
	if err != nil {
		return 0, ErrInvalidPasswordHash
	}

	return parsed, nil
}
