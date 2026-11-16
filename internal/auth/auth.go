// Package auth hashes passwords and mints session tokens for the annotation
// server. It is implemented with the standard library only, so the project
// keeps its single-dependency build (modernc.org/sqlite) and its CGO-free
// Docker image.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Roles accepted by the server. Teachers own projects, define their classes,
// and approve submissions; students submit photos for review.
const (
	RoleTeacher = "teacher"
	RoleStudent = "student"
)

const (
	algorithm   = "pbkdf2-sha256"
	iterations  = 150_000
	keyLength   = 32
	saltLength  = 16
	tokenBytes  = 32
	MinPassword = 6
	MaxPassword = 256
	MinUsername = 3
	MaxUsername = 32
)

// ErrAuth is returned for bad credentials or malformed stored hashes. Callers
// must not reveal whether a username exists.
var ErrAuth = errors.New("invalid username or password")

// ValidRole reports whether role is one of the supported roles.
func ValidRole(role string) bool {
	return role == RoleTeacher || role == RoleStudent
}

// NormalizeUsername lowercases and trims a login name.
func NormalizeUsername(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// ValidateUsername keeps logins safe to display and store.
func ValidateUsername(raw string) (string, error) {
	name := NormalizeUsername(raw)
	if len(name) < MinUsername || len(name) > MaxUsername {
		return "", fmt.Errorf("username must be %d-%d characters", MinUsername, MaxUsername)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return "", errors.New("username may only contain letters, digits, dot, underscore, dash")
		}
	}
	if !hasLetter(name) {
		return "", errors.New("username must contain a letter")
	}
	return name, nil
}

func hasLetter(s string) bool {
	// Usernames are already limited to ASCII by ValidateUsername's switch.
	for i := 0; i < len(s); i++ {
		if s[i] >= 'a' && s[i] <= 'z' {
			return true
		}
	}
	return false
}

// ValidatePassword checks length only; strength policy is deliberately light
// for a classroom tool but long enough to resist quick guessing.
func ValidatePassword(raw string) error {
	if len(raw) < MinPassword {
		return fmt.Errorf("password must be at least %d characters", MinPassword)
	}
	if len(raw) > MaxPassword {
		return fmt.Errorf("password must be at most %d characters", MaxPassword)
	}
	return nil
}

// pbkdf2SHA256 derives keyLen bytes from password and salt (RFC 2898 with the
// HMAC-SHA256 PRF of RFC 4868).
func pbkdf2SHA256(password, salt []byte, rounds, keyLen int) []byte {
	const hashLen = sha256.Size
	blocks := (keyLen + hashLen - 1) / hashLen
	out := make([]byte, 0, blocks*hashLen)
	block := make([]byte, hashLen)
	// Each block hashes salt || INT_32_BE(blockIndex).
	buffer := make([]byte, len(salt)+4)
	copy(buffer, salt)

	for i := 1; i <= blocks; i++ {
		buffer[len(salt)] = byte(i >> 24)
		buffer[len(salt)+1] = byte(i >> 16)
		buffer[len(salt)+2] = byte(i >> 8)
		buffer[len(salt)+3] = byte(i)

		mac := hmac.New(sha256.New, password)
		mac.Write(buffer)
		u := mac.Sum(nil)
		copy(block, u)

		for j := 2; j <= rounds; j++ {
			mac.Reset()
			mac.Write(u)
			u = mac.Sum(u[:0])
			for k := range block {
				block[k] ^= u[k]
			}
		}
		out = append(out, block...)
	}
	return out[:keyLen]
}

// HashPassword returns a self-describing hash: pbkdf2-sha256$rounds$salt$key.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	derived := pbkdf2SHA256([]byte(password), salt, iterations, keyLength)
	return fmt.Sprintf("%s$%d$%s$%s",
		algorithm, iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(derived),
	), nil
}

// VerifyPassword compares password against a stored hash in constant time.
func VerifyPassword(stored, password string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != algorithm {
		return false
	}
	rounds, err := strconv.Atoi(parts[1])
	if err != nil || rounds < 1 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) == 0 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) != keyLength {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, rounds, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NewToken returns a URL-safe random session token. Only its hash is stored.
func NewToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken maps a token to a stable database key.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
