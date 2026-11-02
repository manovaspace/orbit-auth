package jwt

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"

	orbittoken "github.com/manovaspace/orbit-auth/token"
)

type keys struct {
	privatePath string
	publicDir   string
}

func writeKeys(t *testing.T) keys {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(dir, "private.pem")
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	publicDir := filepath.Join(dir, "public")
	if err := os.Mkdir(publicDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(publicDir, "test-key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return keys{privatePath: privatePath, publicDir: publicDir}
}

func setEnv(t *testing.T, k keys) {
	t.Helper()
	t.Setenv("JWT_PRIVATE_KEY_FILE", k.privatePath)
	t.Setenv("JWT_ACTIVE_KEY_ID", "test-key")
	t.Setenv("JWT_PUBLIC_KEYS_DIR", k.publicDir)
}

func TestNewFromEnvRequiresEverySetting(t *testing.T) {
	k := writeKeys(t)
	setEnv(t, k)
	t.Setenv("JWT_PRIVATE_KEY_FILE", "")
	if _, err := NewFromEnv(); err == nil || err.Error() != "JWT_PRIVATE_KEY_FILE is required" {
		t.Fatalf("err = %v", err)
	}
}

func TestNewFromEnvRequiresMatchingActiveKey(t *testing.T) {
	k := writeKeys(t)
	setEnv(t, k)
	t.Setenv("JWT_ACTIVE_KEY_ID", "other-key")
	if _, err := NewFromEnv(); err == nil {
		t.Fatal("a public key that does not match the private key was accepted")
	}
}

func TestSignerProducesTheAccessTokenContract(t *testing.T) {
	k := writeKeys(t)
	setEnv(t, k)
	signer, err := NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Date(2026, 11, 2, 12, 15, 0, 0, time.UTC)
	raw, err := signer.SignAccessToken("u1", expires)
	if err != nil {
		t.Fatal(err)
	}

	claims := jwtlib.MapClaims{}
	parsed, _, err := new(jwtlib.Parser).ParseUnverified(raw, claims)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Header["alg"] != "RS256" || parsed.Header["kid"] != "test-key" || parsed.Header["typ"] != "JWT" {
		t.Fatalf("header = %v", parsed.Header)
	}
	if claims["iss"] != "orbit-auth" || claims["sub"] != "u1" {
		t.Fatalf("claims = %v", claims)
	}
	if int64(claims["exp"].(float64)) != expires.Unix() {
		t.Fatalf("exp = %v, want %v", claims["exp"], expires.Unix())
	}

	publicKeys, err := orbittoken.LoadKeySet(k.publicDir)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := orbittoken.ParseAccessToken(raw, publicKeys)
	if err != nil {
		t.Fatalf("signed token does not verify: %v", err)
	}
	if verified.Subject != "u1" || verified.Issuer != orbittoken.Issuer {
		t.Fatalf("verified claims = %+v", verified)
	}
}
