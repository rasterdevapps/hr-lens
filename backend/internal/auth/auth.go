// Package auth holds roles, password hashing and session tokens.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

type Role string

const (
	SuperAdmin Role = "super_admin"
	Manager    Role = "manager"
	User       Role = "user"
)

func ValidRole(r string) bool {
	return Role(r) == SuperAdmin || Role(r) == Manager || Role(r) == User
}

const (
	MinPasswordLength = 8
	maxPasswordBytes  = 72 // bcrypt ignores anything longer
	bcryptCost        = 12
)

var ErrPassword = errors.New("password must be 8 to 72 characters")

func HashPassword(password string) (string, error) {
	if utf8.RuneCountInString(password) < MinPasswordLength || len(password) > maxPasswordBytes {
		return "", ErrPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	return string(hash), err
}

// dummyHash lets a sign-in for an unknown email take as long as a real one.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcryptCost)

// CheckPassword reports whether password matches hash. Pass an empty hash for an unknown user.
func CheckPassword(hash, password string) bool {
	if hash == "" {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// NewToken returns a random session token for the browser and the hash to store.
func NewToken() (token string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
