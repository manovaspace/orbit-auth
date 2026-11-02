package credentials

import (
	"encoding/base64"
	"regexp"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHasherUsesBcryptDefaultCost(t *testing.T) {
	hasher := NewHasher()
	hash, err := hasher.Hash("s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatal(err)
	}
	if cost != 10 || bcrypt.DefaultCost != 10 {
		t.Fatalf("cost = %d, want 10", cost)
	}
	if !hasher.Compare(hash, "s3cret-pass") {
		t.Fatal("hash did not verify its secret")
	}
	if hasher.Compare(hash, "wrong-pass") {
		t.Fatal("hash accepted a wrong secret")
	}
}

func TestHasherVerifiesAPreviousHash(t *testing.T) {
	// A bcrypt hash produced by the previous implementation.
	const previousHash = "$2a$10$Sp78X511kCy7HF4tr7v7UeDNTPbVApnPbtsVaemtX.uH2DveefNJS"
	if !NewHasher().Compare(previousHash, "s3cret-pass") {
		t.Fatal("previous password hash no longer verifies")
	}
	if NewHasher().Compare(previousHash, "wrong-pass") {
		t.Fatal("previous password hash accepted a wrong password")
	}
}

func TestCodesAreSixDigits(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9]{6}$`)
	codes := NewCodes()
	for i := 0; i < 100; i++ {
		code, err := codes.NewCode()
		if err != nil {
			t.Fatal(err)
		}
		if !pattern.MatchString(code) {
			t.Fatalf("code = %q, want six digits", code)
		}
	}
}

func TestSecretsAre32BytesBase64URL(t *testing.T) {
	pattern := regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	secrets := NewSecrets()
	for i := 0; i < 50; i++ {
		secret, err := secrets.NewSecret()
		if err != nil {
			t.Fatal(err)
		}
		if !pattern.MatchString(secret) {
			t.Fatalf("secret = %q, want 43 base64url characters", secret)
		}
		decoded, err := base64.RawURLEncoding.DecodeString(secret)
		if err != nil || len(decoded) != 32 {
			t.Fatalf("secret decodes to %d bytes, err = %v, want 32", len(decoded), err)
		}
	}
}
