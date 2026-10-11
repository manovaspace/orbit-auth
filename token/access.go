// Package token signs and verifies Orbit Auth access tokens for trusted
// platform and product services. It does not handle refresh tokens or role
// assignments.
package token

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// Issuer identifies tokens created by Orbit Auth.
	Issuer = "orbit-auth"
	// Algorithm is the only accepted access-token signing algorithm.
	Algorithm = "RS256"
)

// AccessClaims contains the registered claims accepted by Orbit consumers.
type AccessClaims struct {
	jwt.RegisteredClaims
}

// KeySet maps JWT key IDs to the public keys that may verify access tokens.
type KeySet map[string]*rsa.PublicKey

// JWK and JWKSet are the public JSON representation served by a JWKS endpoint.
type JWK struct {
	KeyType  string `json:"kty"`
	Use      string `json:"use"`
	Alg      string `json:"alg"`
	KeyID    string `json:"kid"`
	Modulus  string `json:"n"`
	Exponent string `json:"e"`
}

type JWKSet struct {
	Keys []JWK `json:"keys"`
}

// LoadRSAPrivateKey reads an RSA PKCS#8 or PKCS#1 private PEM. Keys smaller
// than 2048 bits are rejected.
func LoadRSAPrivateKey(path string) (*rsa.PrivateKey, error) {
	contents, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("read JWT private key: %w", err)
	}
	block, _ := pem.Decode(contents)
	if block == nil {
		return nil, fmt.Errorf("JWT private key file does not contain PEM data")
	}
	var key *rsa.PrivateKey
	if parsed, parseErr := x509.ParsePKCS8PrivateKey(block.Bytes); parseErr == nil {
		key, _ = parsed.(*rsa.PrivateKey)
	} else if parsed, parseErr := x509.ParsePKCS1PrivateKey(block.Bytes); parseErr == nil {
		key = parsed
	}
	if key == nil {
		return nil, fmt.Errorf("JWT private key must be an RSA PKCS#8 or PKCS#1 PEM key")
	}
	if err := key.Validate(); err != nil {
		return nil, fmt.Errorf("JWT RSA private key is invalid: %w", err)
	}
	if key.N.BitLen() < 2048 {
		return nil, fmt.Errorf("JWT RSA private key must be at least 2048 bits")
	}
	return key, nil
}

// LoadRSAPublicKey reads an RSA PKIX or PKCS#1 public PEM. Keys smaller than
// 2048 bits are rejected.
func LoadRSAPublicKey(path string) (*rsa.PublicKey, error) {
	contents, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("read JWT public key: %w", err)
	}
	block, _ := pem.Decode(contents)
	if block == nil {
		return nil, fmt.Errorf("JWT public key file does not contain PEM data")
	}
	var key *rsa.PublicKey
	if parsed, parseErr := x509.ParsePKIXPublicKey(block.Bytes); parseErr == nil {
		key, _ = parsed.(*rsa.PublicKey)
	}
	if key == nil {
		key, _ = x509.ParsePKCS1PublicKey(block.Bytes)
	}
	if key == nil {
		return nil, fmt.Errorf("JWT public key must be an RSA PKIX or PKCS#1 PEM key")
	}
	if key.N.BitLen() < 2048 {
		return nil, fmt.Errorf("JWT RSA public key must be at least 2048 bits")
	}
	return key, nil
}

// LoadKeySet reads public PEM files named <kid>.pem from a directory.
func LoadKeySet(directory string) (KeySet, error) {
	paths, err := filepath.Glob(filepath.Join(strings.TrimSpace(directory), "*.pem"))
	if err != nil {
		return nil, fmt.Errorf("list JWT public keys: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("JWT public key directory contains no PEM keys")
	}
	keys := make(KeySet, len(paths))
	for _, path := range paths {
		keyID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if keyID == "" {
			return nil, fmt.Errorf("JWT public key filename must provide a key ID")
		}
		key, err := LoadRSAPublicKey(path)
		if err != nil {
			return nil, fmt.Errorf("load JWT key %q: %w", keyID, err)
		}
		keys[keyID] = key
	}
	return keys, nil
}

// Document converts the key set to the public JWKS representation.
func (keys KeySet) Document() JWKSet {
	ids := make([]string, 0, len(keys))
	for keyID := range keys {
		ids = append(ids, keyID)
	}
	sort.Strings(ids)
	document := JWKSet{Keys: make([]JWK, 0, len(ids))}
	for _, keyID := range ids {
		key := keys[keyID]
		document.Keys = append(document.Keys, JWK{
			KeyType:  "RSA",
			Use:      "sig",
			Alg:      Algorithm,
			KeyID:    keyID,
			Modulus:  base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			Exponent: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		})
	}
	return document
}

// SignAccessToken signs a short-lived access token for an Orbit subject.
func SignAccessToken(key *rsa.PrivateKey, keyID, subject string, expires time.Time) (string, error) {
	if key == nil {
		return "", fmt.Errorf("JWT private key is required")
	}
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return "", fmt.Errorf("JWT key ID is required")
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "", fmt.Errorf("JWT subject is required")
	}
	claims := AccessClaims{RegisteredClaims: jwt.RegisteredClaims{
		Issuer:    Issuer,
		Subject:   subject,
		ExpiresAt: jwt.NewNumericDate(expires),
		IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
	}}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = keyID
	return token.SignedString(key)
}

// ParseAccessToken verifies an RS256 access token against its kid in the key set.
func ParseAccessToken(raw string, keys KeySet) (*AccessClaims, error) {
	unverified := &AccessClaims{}
	parsedHeader, _, err := new(jwt.Parser).ParseUnverified(raw, unverified)
	if err != nil {
		return nil, err
	}
	if parsedHeader == nil {
		return nil, fmt.Errorf("invalid JWT access token")
	}
	keyID, ok := parsedHeader.Header["kid"].(string)
	if !ok || strings.TrimSpace(keyID) == "" {
		return nil, fmt.Errorf("JWT key ID is required")
	}
	key := keys[keyID]
	if key == nil {
		return nil, fmt.Errorf("unknown JWT key ID %q", keyID)
	}
	claims := &AccessClaims{}
	parsed, err := jwt.ParseWithClaims(raw, claims, func(parsed *jwt.Token) (any, error) {
		if parsed.Method != jwt.SigningMethodRS256 {
			return nil, fmt.Errorf("unexpected JWT signing algorithm")
		}
		return key, nil
	}, jwt.WithValidMethods([]string{Algorithm}), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithIssuer(Issuer))
	if err != nil {
		return nil, err
	}
	if parsed == nil || !parsed.Valid {
		return nil, fmt.Errorf("invalid JWT access token")
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return nil, fmt.Errorf("JWT subject is required")
	}
	return claims, nil
}

// RemoteVerifier fetches and caches a JWKS for verifying Orbit access tokens.
type RemoteVerifier struct {
	url                string
	client             *http.Client
	cacheLifetime      time.Duration
	maxStale           time.Duration
	mu                 sync.Mutex
	keys               KeySet
	fetchedAt          time.Time
	expiresAt          time.Time
	lastRefreshAttempt time.Time
	lastUnknownRefresh time.Time
}

// NewRemoteVerifier loads the initial JWKS and returns a verifier. The cache
// lifetime defaults to five minutes; known cached keys remain usable for up to
// one hour during a JWKS endpoint outage.
func NewRemoteVerifier(jwksURL string, cacheLifetime time.Duration) (*RemoteVerifier, error) {
	jwksURL = strings.TrimSpace(jwksURL)
	if jwksURL == "" {
		return nil, fmt.Errorf("Orbit JWKS URL is required")
	}
	parsedURL, err := url.ParseRequestURI(jwksURL)
	if err != nil || parsedURL.Host == "" || parsedURL.Fragment != "" || parsedURL.User != nil {
		return nil, fmt.Errorf("Orbit JWKS URL must be an absolute HTTP(S) URL without a fragment")
	}
	localHTTP := parsedURL.Scheme == "http" && (parsedURL.Hostname() == "localhost" || parsedURL.Hostname() == "127.0.0.1" || parsedURL.Hostname() == "host.docker.internal")
	if parsedURL.Scheme != "https" && !localHTTP {
		return nil, fmt.Errorf("Orbit JWKS URL must use HTTPS outside local development")
	}
	if cacheLifetime <= 0 {
		cacheLifetime = 5 * time.Minute
	}
	verifier := &RemoteVerifier{
		url: jwksURL,
		client: &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) >= 3 || request.URL.Scheme != parsedURL.Scheme || !strings.EqualFold(request.URL.Host, parsedURL.Host) {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
		cacheLifetime: cacheLifetime,
		maxStale:      time.Hour,
	}
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	if err := verifier.refreshLocked(); err != nil {
		return nil, err
	}
	return verifier, nil
}

// ParseAccessToken verifies an Orbit token, refreshing the cached JWKS when
// it expires or the token references an unknown kid.
func (v *RemoteVerifier) ParseAccessToken(raw string) (*AccessClaims, error) {
	unverified := &AccessClaims{}
	parsedHeader, _, err := new(jwt.Parser).ParseUnverified(raw, unverified)
	if err != nil {
		return nil, err
	}
	if parsedHeader == nil {
		return nil, fmt.Errorf("invalid JWT access token")
	}
	keyID, ok := parsedHeader.Header["kid"].(string)
	if !ok || strings.TrimSpace(keyID) == "" {
		return nil, fmt.Errorf("JWT key ID is required")
	}

	v.mu.Lock()
	key := v.keys[keyID]
	now := time.Now()
	cacheExpired := now.After(v.expiresAt)
	unknownKey := key == nil
	canRefreshExpired := v.lastRefreshAttempt.IsZero() || now.Sub(v.lastRefreshAttempt) >= 15*time.Second
	canRefreshUnknown := v.lastUnknownRefresh.IsZero() || now.Sub(v.lastUnknownRefresh) >= 15*time.Second
	if (cacheExpired && canRefreshExpired) || (unknownKey && canRefreshUnknown) {
		v.lastRefreshAttempt = now
		if unknownKey {
			v.lastUnknownRefresh = now
		}
		refreshErr := v.refreshLocked()
		key = v.keys[keyID]
		if refreshErr != nil && (key == nil || now.Sub(v.fetchedAt) > v.maxStale) {
			v.mu.Unlock()
			return nil, refreshErr
		}
	}
	if key != nil && now.Sub(v.fetchedAt) > v.maxStale {
		v.mu.Unlock()
		return nil, fmt.Errorf("cached Orbit JWKS exceeded its maximum stale age")
	}
	v.mu.Unlock()
	if key == nil {
		return nil, fmt.Errorf("unknown JWT key ID %q", keyID)
	}
	return ParseAccessToken(raw, KeySet{keyID: key})
}

func (v *RemoteVerifier) refreshLocked() error {
	request, err := http.NewRequest(http.MethodGet, v.url, nil)
	if err != nil {
		return fmt.Errorf("create JWKS request: %w", err)
	}
	response, err := v.client.Do(request)
	if err != nil {
		return fmt.Errorf("fetch Orbit JWKS: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch Orbit JWKS: unexpected status %d", response.StatusCode)
	}
	var document JWKSet
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&document); err != nil {
		return fmt.Errorf("decode Orbit JWKS: %w", err)
	}
	keys, err := document.KeySet()
	if err != nil {
		return err
	}
	v.keys = keys
	v.fetchedAt = time.Now()
	v.expiresAt = v.fetchedAt.Add(v.cacheLifetime)
	return nil
}

// KeySet validates and converts a JWKS document to RSA public keys.
func (document JWKSet) KeySet() (KeySet, error) {
	if len(document.Keys) == 0 || len(document.Keys) > 100 {
		return nil, fmt.Errorf("Orbit JWKS must contain between 1 and 100 keys")
	}
	keys := make(KeySet, len(document.Keys))
	for _, jwk := range document.Keys {
		if jwk.KeyType != "RSA" || jwk.Alg != Algorithm || jwk.Use != "sig" || strings.TrimSpace(jwk.KeyID) == "" {
			return nil, fmt.Errorf("Orbit JWKS contains an unsupported key")
		}
		modulus, err := base64.RawURLEncoding.DecodeString(jwk.Modulus)
		if err != nil {
			return nil, fmt.Errorf("Orbit JWKS key %q has an invalid modulus", jwk.KeyID)
		}
		exponent, err := base64.RawURLEncoding.DecodeString(jwk.Exponent)
		if err != nil || len(exponent) == 0 || len(exponent) > 4 {
			return nil, fmt.Errorf("Orbit JWKS key %q has an invalid exponent", jwk.KeyID)
		}
		exponentValue := 0
		for _, b := range exponent {
			exponentValue = exponentValue<<8 | int(b)
		}
		key := &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: exponentValue}
		if key.N.BitLen() < 2048 || key.E < 3 || key.E%2 == 0 {
			return nil, fmt.Errorf("Orbit JWKS key %q is not a valid 2048-bit RSA signing key", jwk.KeyID)
		}
		if _, exists := keys[jwk.KeyID]; exists {
			return nil, fmt.Errorf("Orbit JWKS contains duplicate key ID %q", jwk.KeyID)
		}
		keys[jwk.KeyID] = key
	}
	return keys, nil
}
