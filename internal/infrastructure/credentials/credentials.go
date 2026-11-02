// Package credentials holds the bcrypt and randomness adapters. The algorithm,
// cost and encodings are the credential contract: characterization tests pin
// them and they must not change without a deliberate migration.
package credentials

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"

	"golang.org/x/crypto/bcrypt"
)

// Hasher hashes passwords and API-token secrets with bcrypt at DefaultCost.
type Hasher struct{}

// NewHasher returns the bcrypt adapter.
func NewHasher() Hasher { return Hasher{} }

// Hash returns a bcrypt hash at bcrypt.DefaultCost.
func (Hasher) Hash(secret string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// Compare reports whether secret matches hash.
func (Hasher) Compare(hash, secret string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(secret)) == nil
}

// Codes generates six-digit OTP codes.
type Codes struct{}

// NewCodes returns the OTP code generator.
func NewCodes() Codes { return Codes{} }

// NewCode returns a cryptographically random six-digit code.
func (Codes) NewCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// Secrets generates API-token secrets.
type Secrets struct{}

// NewSecrets returns the secret generator.
func NewSecrets() Secrets { return Secrets{} }

// NewSecret returns 32 random bytes as unpadded base64url (43 characters).
func (Secrets) NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
