package auth

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

const passwordCost = 12

// HashPassword converts plaintext into a bcrypt hash for storage.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("hash password: password is required")
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), passwordCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hashed), nil
}

// CheckPassword validates plaintext against the stored hash.
func CheckPassword(password, hash string) bool {
	if password == "" || hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
