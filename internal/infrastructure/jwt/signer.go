// Package jwt is the production domain.TokenSigner adapter. It loads the
// RS256 signing key and the public key set from the environment and signs
// access tokens through the public token package. Key formats, the issuer and
// the claims stay in token/ and are unchanged.
package jwt

import (
	"crypto/rsa"
	"fmt"
	"os"
	"strings"
	"time"

	orbittoken "github.com/manovaspace/orbit-auth/token"
)

// Signer signs access tokens with one active RSA key.
type Signer struct {
	key   *rsa.PrivateKey
	keyID string
	keys  orbittoken.KeySet
}

// NewFromEnv loads JWT_PRIVATE_KEY_FILE, JWT_ACTIVE_KEY_ID and
// JWT_PUBLIC_KEYS_DIR and verifies that the active key ID names the matching
// public key.
func NewFromEnv() (*Signer, error) {
	privateKeyPath := strings.TrimSpace(os.Getenv("JWT_PRIVATE_KEY_FILE"))
	if privateKeyPath == "" {
		return nil, fmt.Errorf("JWT_PRIVATE_KEY_FILE is required")
	}
	privateKey, err := orbittoken.LoadRSAPrivateKey(privateKeyPath)
	if err != nil {
		return nil, err
	}
	keyID := strings.TrimSpace(os.Getenv("JWT_ACTIVE_KEY_ID"))
	if keyID == "" {
		return nil, fmt.Errorf("JWT_ACTIVE_KEY_ID is required")
	}
	publicKeysDirectory := strings.TrimSpace(os.Getenv("JWT_PUBLIC_KEYS_DIR"))
	if publicKeysDirectory == "" {
		return nil, fmt.Errorf("JWT_PUBLIC_KEYS_DIR is required")
	}
	publicKeys, err := orbittoken.LoadKeySet(publicKeysDirectory)
	if err != nil {
		return nil, err
	}
	activePublicKey := publicKeys[keyID]
	if activePublicKey == nil || activePublicKey.E != privateKey.E || activePublicKey.N.Cmp(privateKey.N) != 0 {
		return nil, fmt.Errorf("active JWT key ID %q must match a public key in JWT_PUBLIC_KEYS_DIR", keyID)
	}
	return &Signer{key: privateKey, keyID: keyID, keys: publicKeys}, nil
}

// KeySet returns the public keys that verify the service's access tokens.
func (s *Signer) KeySet() orbittoken.KeySet {
	return s.keys
}

// SignAccessToken signs a short-lived access token for subject.
func (s *Signer) SignAccessToken(subject string, expires time.Time) (string, error) {
	return orbittoken.SignAccessToken(s.key, s.keyID, subject, expires)
}

// SignAccessTokenWithOrg signs a short-lived access token carrying the active
// organization claim. A blank orgID omits the claim.
func (s *Signer) SignAccessTokenWithOrg(subject, orgID string, expires time.Time) (string, error) {
	return orbittoken.SignAccessTokenWithOrg(s.key, s.keyID, subject, orgID, expires)
}
