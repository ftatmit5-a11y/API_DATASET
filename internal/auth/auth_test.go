package auth

import (
	"encoding/hex"
	"strings"
	"testing"
)

// Vectors produced by Python's hashlib.pbkdf2_hmac("sha256", pw, salt, c, 32).
func TestPBKDF2SHA256Vectors(t *testing.T) {
	cases := []struct {
		password string
		salt     string
		rounds   int
		want     string
	}{
		{"password", "salt", 1, "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b"},
		{"password", "salt", 2, "ae4d0c95af6b46d32d0adff928f06dd02a303f8ef3c251dfd6e2d85a95474c43"},
		{"password", "salt", 4096, "c5e478d59288c841aa530db6845c4c8d962893a001ce4e11a4963873aa98134a"},
		{"password", "salt", 1000, "632c2812e46d4604102ba7618e9d6d7d2f8128f6266b4a03264d2a0460b7dcb3"},
		{"correct horse battery staple", "c2FsdCBzYWx0", 100000,
			"db850802e5c15b7b37acd64b3945574448b7651239c6f5bb7a824f7e538715c9"},
	}
	for _, c := range cases {
		got := pbkdf2SHA256([]byte(c.password), []byte(c.salt), c.rounds, 32)
		if hex.EncodeToString(got) != c.want {
			t.Errorf("rounds=%d: got %x want %s", c.rounds, got, c.want)
		}
	}
}

func TestPBKDF2LongerThanOneBlock(t *testing.T) {
	// 40 bytes spans two PBKDF2 blocks, which exercises the block index path.
	got := pbkdf2SHA256([]byte("password"), []byte("salt"), 1, 40)
	if len(got) != 40 {
		t.Fatalf("len = %d", len(got))
	}
	first := pbkdf2SHA256([]byte("password"), []byte("salt"), 1, 32)
	for i := range first {
		if got[i] != first[i] {
			t.Fatalf("prefix byte %d differs: %02x vs %02x", i, got[i], first[i])
		}
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	hashed, err := HashPassword("teacher1234")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hashed, algorithm+"$") {
		t.Fatalf("hash = %q", hashed)
	}
	if hashed == "teacher1234" || strings.Contains(hashed, "teacher1234") {
		t.Fatal("hash leaks the password")
	}
	if !VerifyPassword(hashed, "teacher1234") {
		t.Fatal("correct password rejected")
	}
	if VerifyPassword(hashed, "teacher1235") {
		t.Fatal("wrong password accepted")
	}

	other, err := HashPassword("teacher1234")
	if err != nil {
		t.Fatal(err)
	}
	if other == hashed {
		t.Fatal("hashes must use a random salt")
	}
	if !VerifyPassword(other, "teacher1234") {
		t.Fatal("re-hashed password rejected")
	}
}

func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	for _, stored := range []string{
		"", "plain-password", "bcrypt$1$abc$def",
		algorithm + "$0$AAAA$AAAA",
		algorithm + "$1000$not-base64!!$AAAA",
		algorithm + "$1000$" + strings.Repeat("QQ", 16) + "$short",
	} {
		if VerifyPassword(stored, "anything") {
			t.Errorf("malformed hash accepted: %q", stored)
		}
	}
}

func TestValidateUsername(t *testing.T) {
	good := map[string]string{
		" Somchai ": "somchai",
		"naruto.1":  "naruto.1",
		"A_9":       "a_9",
	}
	for raw, want := range good {
		got, err := ValidateUsername(raw)
		if err != nil {
			t.Errorf("%q: %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("%q -> %q want %q", raw, got, want)
		}
	}
	bad := []string{
		"",
		"   ",
		"ab",                               // too short
		strings.Repeat("u", MaxUsername+1), // too long
		"user@x",                           // illegal character
		"มีชื่อ",                           // non-ASCII
		"1234",                             // no letter
	}
	for _, raw := range bad {
		if _, err := ValidateUsername(raw); err == nil {
			t.Errorf("accepted bad username %q", raw)
		}
	}
}

func TestValidatePasswordAndRole(t *testing.T) {
	if err := ValidatePassword("12345"); err == nil {
		t.Error("short password accepted")
	}
	if err := ValidatePassword("123456"); err != nil {
		t.Errorf("min length rejected: %v", err)
	}
	if err := ValidatePassword(strings.Repeat("p", MaxPassword+1)); err == nil {
		t.Error("long password accepted")
	}
	if !ValidRole(RoleTeacher) || !ValidRole(RoleStudent) {
		t.Fatal("seeded roles must be valid")
	}
	for _, role := range []string{"", "admin", "TEACHER", "guest"} {
		if ValidRole(role) {
			t.Errorf("role %q accepted", role)
		}
	}
}

func TestNewTokenAndHash(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 64; i++ {
		token, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(token) < 40 || strings.ContainsAny(token, "+/=") {
			t.Fatalf("token not URL-safe: %q", token)
		}
		if _, dup := seen[token]; dup {
			t.Fatalf("duplicate token %q", token)
		}
		seen[token] = struct{}{}
		if got := HashToken(token); len(got) != 64 || strings.ContainsAny(got, "ghijklmnopqrstuvwxyz") {
			t.Fatalf("hash token = %q", got)
		}
	}
	if HashToken("a") == HashToken("b") {
		t.Fatal("hash collision")
	}
}
