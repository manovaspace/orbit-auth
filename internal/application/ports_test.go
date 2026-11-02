package application

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// testSigner stands in for the RS256 adapter. Token signing itself is covered
// by internal/infrastructure/jwt and token/access_test.go.
type testSigner struct{}

func (testSigner) SignAccessToken(subject string, _ time.Time) (string, error) {
	return "signed-access-token-for-" + subject, nil
}

// testHasher mirrors the production bcrypt adapter so password-login tests run
// the same comparison path.
type testHasher struct{}

func (testHasher) Hash(secret string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func (testHasher) Compare(hash, secret string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(secret)) == nil
}

type testCodes struct{}

func (testCodes) NewCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

type testSecrets struct{}

func (testSecrets) NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// testUUIDs returns real UUIDs so refresh-token format assertions hold.
type testUUIDs struct{}

func (testUUIDs) NewID() string { return uuid.NewString() }

// fixedClock and fixedIDs give deterministic tests that do not depend on the
// wall clock.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

type fixedIDs struct{ id string }

func (i fixedIDs) NewID() string { return i.id }
