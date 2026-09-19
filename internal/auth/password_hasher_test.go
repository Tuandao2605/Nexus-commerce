// File này kiểm thử Argon2id hashing, random salt, constant-time verification contract, PHC parsing bounds và rehash detection.
package auth

import (
	"errors"
	"strings"
	"testing"
)

// TestArgon2idHasherHashesAndVerifiesPassword xác nhận default policy tạo PHC hash và chỉ password đúng mới verify thành công.
func TestArgon2idHasherHashesAndVerifiesPassword(t *testing.T) {
	hasher := newDefaultArgon2idTestHasher(t)
	encodedHash, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if !strings.HasPrefix(encodedHash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("Hash() = %q, want default Argon2id PHC prefix", encodedHash)
	}
	if strings.Contains(encodedHash, "correct horse battery staple") {
		t.Fatal("Hash() contains plaintext password")
	}
	if err := hasher.Verify("correct horse battery staple", encodedHash); err != nil {
		t.Fatalf("Verify(correct) error = %v", err)
	}
	if err := hasher.Verify("wrong password", encodedHash); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("Verify(wrong) error = %v, want ErrPasswordMismatch", err)
	}
}

// TestArgon2idHasherUsesUniqueRandomSalt xác nhận cùng password tạo hai encoded hashes khác nhau nhưng cả hai đều verify được.
func TestArgon2idHasherUsesUniqueRandomSalt(t *testing.T) {
	hasher := newDefaultArgon2idTestHasher(t)
	first, err := hasher.Hash("same password")
	if err != nil {
		t.Fatalf("first Hash() error = %v", err)
	}
	second, err := hasher.Hash("same password")
	if err != nil {
		t.Fatalf("second Hash() error = %v", err)
	}
	if first == second {
		t.Fatal("Hash() reused salt for identical passwords")
	}
	if err := hasher.Verify("same password", first); err != nil {
		t.Fatalf("Verify(first) error = %v", err)
	}
	if err := hasher.Verify("same password", second); err != nil {
		t.Fatalf("Verify(second) error = %v", err)
	}
}

// TestArgon2idHasherRejectsInvalidPasswordInput xác nhận empty/oversized password không chạy hashing work.
func TestArgon2idHasherRejectsInvalidPasswordInput(t *testing.T) {
	hasher := newDefaultArgon2idTestHasher(t)
	for _, password := range []string{"", strings.Repeat("x", maxPasswordBytes+1)} {
		if _, err := hasher.Hash(password); !errors.Is(err, ErrInvalidPassword) {
			t.Fatalf("Hash(password length %d) error = %v, want ErrInvalidPassword", len(password), err)
		}
	}
}

// TestArgon2idHasherRejectsUnsafePolicy xác nhận constructor không cho tạo hash mới dưới baseline hoặc vượt resource caps.
func TestArgon2idHasherRejectsUnsafePolicy(t *testing.T) {
	valid := DefaultArgon2idParams()
	testCases := []struct {
		name   string
		mutate func(*Argon2idParams)
	}{
		{name: "low memory", mutate: func(params *Argon2idParams) { params.MemoryKiB-- }},
		{name: "high memory", mutate: func(params *Argon2idParams) { params.MemoryKiB = maximumArgon2MemoryKiB + 1 }},
		{name: "low iterations", mutate: func(params *Argon2idParams) { params.Iterations = 1 }},
		{name: "high iterations", mutate: func(params *Argon2idParams) { params.Iterations = maximumArgon2Iterations + 1 }},
		{name: "zero parallelism", mutate: func(params *Argon2idParams) { params.Parallelism = 0 }},
		{name: "short salt", mutate: func(params *Argon2idParams) { params.SaltLength-- }},
		{name: "short key", mutate: func(params *Argon2idParams) { params.KeyLength-- }},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			params := valid
			testCase.mutate(&params)
			if _, err := NewArgon2idHasher(params); err == nil {
				t.Fatal("NewArgon2idHasher() error = nil, want unsafe policy error")
			}
		})
	}
}

// TestArgon2idHasherRejectsMalformedOrExcessiveHash xác nhận parser dừng trước KDF cho format/version/cost không hợp lệ.
func TestArgon2idHasherRejectsMalformedOrExcessiveHash(t *testing.T) {
	hasher := newDefaultArgon2idTestHasher(t)
	testCases := []string{
		"",
		"not-a-phc-hash",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U",
		"$argon2id$v=18$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U",
		"$argon2id$v=19$m=999999,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U",
		"$argon2id$v=19$m=19456,t=99,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U",
		"$argon2id$v=19$m=19456,t=2,p=0$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U",
		"$argon2id$v=19$m=19456,t=2,p=1$%%%$%%%",
		strings.Repeat("x", maximumEncodedHashLength+1),
	}

	for _, encodedHash := range testCases {
		if err := hasher.Verify("password", encodedHash); !errors.Is(err, ErrInvalidPasswordHash) {
			t.Fatalf("Verify(%q) error = %v, want ErrInvalidPasswordHash", encodedHash, err)
		}
	}
}

// TestArgon2idHasherDetectsRehashNeed xác nhận current policy không rehash còn hash hợp lệ với work factor cũ thì có.
func TestArgon2idHasherDetectsRehashNeed(t *testing.T) {
	hasher := newDefaultArgon2idTestHasher(t)
	currentHash, err := hasher.Hash("password")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	needsRehash, err := hasher.NeedsRehash(currentHash)
	if err != nil {
		t.Fatalf("NeedsRehash(current) error = %v", err)
	}
	if needsRehash {
		t.Fatal("NeedsRehash(current) = true, want false")
	}

	legacyParams := Argon2idParams{MemoryKiB: 12 * 1024, Iterations: 3, Parallelism: 1, SaltLength: 16, KeyLength: 32}
	legacyHash := encodeArgon2idHash(legacyParams, []byte("0123456789abcdef"), []byte("0123456789abcdef0123456789abcdef"))
	needsRehash, err = hasher.NeedsRehash(legacyHash)
	if err != nil {
		t.Fatalf("NeedsRehash(legacy) error = %v", err)
	}
	if !needsRehash {
		t.Fatal("NeedsRehash(legacy) = false, want true")
	}
}

// newDefaultArgon2idTestHasher tạo default production policy và fail test ngay nếu cấu hình source trở nên không hợp lệ.
func newDefaultArgon2idTestHasher(t *testing.T) *Argon2idHasher {
	t.Helper()

	hasher, err := NewArgon2idHasher(DefaultArgon2idParams())
	if err != nil {
		t.Fatalf("NewArgon2idHasher() error = %v", err)
	}
	return hasher
}
