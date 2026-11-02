package application

// Characterization tests pin the wire-visible identity behaviour of the
// previous implementation: credential hashing cost and encoding, refresh-token
// and API-token formats, OTP rules and access-token shape. They were written
// and run against the pre-refactor code and must stay green without changing
// an expected value.

import (
	"context"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestCharacterizationRefreshTokenHash(t *testing.T) {
	// SHA-256("char-refresh-token")
	const want = "6a9c19bc50ab57612a3cf34b2db5243c36eb61071570c43161bf0d365cf63ece"
	got := charHashToken("char-refresh-token")
	if len(got) != 64 {
		t.Fatalf("refresh-token hash length = %d, want 64 hex characters", len(got))
	}
	if got != want {
		t.Fatalf("refresh-token hash = %q, want %q", got, want)
	}
	if got == charHashToken("char-refresh-token-2") {
		t.Fatal("different refresh tokens produced the same hash")
	}
}

func TestCharacterizationIdentifierHash(t *testing.T) {
	// The first 8 bytes of SHA-256("person@example.com").
	const want = "542d240129883c01"
	got := charHashIdentifier("person@example.com")
	if len(got) != 16 {
		t.Fatalf("identifier hash length = %d, want 16 hex characters", len(got))
	}
	if got != want {
		t.Fatalf("identifier hash = %q, want %q", got, want)
	}
}

func TestCharacterizationPasswordHashing(t *testing.T) {
	if got := charBcryptCost(); got != 10 {
		t.Fatalf("bcrypt cost = %d, want 10 (bcrypt.DefaultCost)", got)
	}
	// A hash produced by the previous implementation for "s3cret-pass".
	const previousHash = "$2a$10$Sp78X511kCy7HF4tr7v7UeDNTPbVApnPbtsVaemtX.uH2DveefNJS"
	if err := bcrypt.CompareHashAndPassword([]byte(previousHash), []byte("s3cret-pass")); err != nil {
		t.Fatalf("previous password hash no longer verifies: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(previousHash), []byte("wrong-pass")) == nil {
		t.Fatal("previous password hash accepted a wrong password")
	}
}

func TestCharacterizationAPITokenFormatAndHashing(t *testing.T) {
	svc, tokens := charServiceWithTokenStore(t)
	id, prefix, secret, err := svc.CreateApiToken(context.Background(), "u1", "deploy", []string{"read"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefix) != 8 {
		t.Fatalf("prefix length = %d, want 8", len(prefix))
	}
	pattern := regexp.MustCompile(`^oat_[A-Za-z0-9_-]{8}\.[A-Za-z0-9_-]{35}$`)
	if !pattern.MatchString(secret) {
		t.Fatalf("api token = %q, does not match oat_<8>.<35 base64url chars>", secret)
	}
	if !strings.HasPrefix(secret, "oat_"+prefix+".") {
		t.Fatalf("api token %q does not embed prefix %q", secret, prefix)
	}
	if id == "" {
		t.Fatal("api token id is empty")
	}
	if len(tokens.created) != 1 {
		t.Fatalf("stored %d api tokens, want 1", len(tokens.created))
	}
	stored := tokens.created[0]
	if stored.Prefix != prefix || stored.UserID != "u1" || stored.Name != "deploy" {
		t.Fatalf("stored token = %+v", stored)
	}
	cost, err := bcrypt.Cost([]byte(stored.TokenHash))
	if err != nil || cost != 10 {
		t.Fatalf("stored token hash cost = %d, err = %v, want 10", cost, err)
	}
	tail := strings.TrimPrefix(secret, "oat_"+prefix+".")
	if err := bcrypt.CompareHashAndPassword([]byte(stored.TokenHash), []byte(prefix+tail)); err != nil {
		t.Fatalf("stored token hash does not verify the token secret: %v", err)
	}

	// Round trip through validation, with and without the documented oat_ prefix.
	userID, scopes, err := svc.ValidateApiToken(context.Background(), secret)
	if err != nil || userID != "u1" || len(scopes) != 1 || scopes[0] != "read" {
		t.Fatalf("validate = %q %v %v", userID, scopes, err)
	}
	userID, _, err = svc.ValidateApiToken(context.Background(), strings.TrimPrefix(secret, "oat_"))
	if err != nil || userID != "u1" {
		t.Fatalf("validate without prefix = %q %v", userID, err)
	}
}

func TestCharacterizationAPITokenRejectsMalformedSecrets(t *testing.T) {
	svc, _ := charServiceWithTokenStore(t)
	for _, secret := range []string{
		"",
		"oat_",
		"oat_short.tail",
		"oat_12345678.",
		"12345678.tail",
		"oat_12345678.tail.extra",
	} {
		if _, _, err := svc.ValidateApiToken(context.Background(), secret); err == nil {
			t.Fatalf("ValidateApiToken(%q) accepted a malformed token", secret)
		}
	}
}

func TestCharacterizationOTPCodeFormat(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9]{6}$`)
	for i := 0; i < 100; i++ {
		code, err := charOTPCode()
		if err != nil {
			t.Fatal(err)
		}
		if !pattern.MatchString(code) {
			t.Fatalf("otp code = %q, want six digits", code)
		}
	}
}

func TestCharacterizationAPISecretFormat(t *testing.T) {
	pattern := regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	for i := 0; i < 50; i++ {
		secret, err := charAPISecret()
		if err != nil {
			t.Fatal(err)
		}
		if !pattern.MatchString(secret) {
			t.Fatalf("api secret = %q, want 43 base64url characters (32 bytes)", secret)
		}
		decoded, err := base64.RawURLEncoding.DecodeString(secret)
		if err != nil || len(decoded) != 32 {
			t.Fatalf("api secret decodes to %d bytes, err = %v, want 32", len(decoded), err)
		}
	}
}

func TestCharacterizationE164Rules(t *testing.T) {
	accepted := []string{"+989121234567", "+14155552671", "+1234567", "+123456789012345"}
	rejected := []string{"", "09121234567", "989121234567", "+0123456789", "+1", "+123456", "+1234567890123456", "+98 912 123 4567", "person@example.com"}
	for _, value := range accepted {
		if !charIsE164(value) {
			t.Fatalf("%q was rejected but is valid E.164", value)
		}
	}
	for _, value := range rejected {
		if charIsE164(value) {
			t.Fatalf("%q was accepted but is not valid E.164", value)
		}
	}
}

func TestCharacterizationRefreshTokenFormat(t *testing.T) {
	svc, refresh := charServiceForTokens(t)
	_, token, _, err := svc.issueTokens(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	if !pattern.MatchString(token) {
		t.Fatalf("refresh token = %q, want two concatenated UUIDs", token)
	}
	if len(token) != 72 {
		t.Fatalf("refresh token length = %d, want 72", len(token))
	}
	if token != strings.ToLower(token) {
		t.Fatal("refresh token is not lowercase")
	}
	if _, ok := refresh.tokens[charHashToken(token)]; !ok {
		t.Fatal("refresh token was not stored under its SHA-256 hash")
	}
}

func TestCharacterizationAccessTokenClaims(t *testing.T) {
	expires := time.Date(2026, 11, 2, 12, 15, 0, 0, time.UTC)
	raw := charSignAccessToken(t, "u1", expires)
	header, claims := charParseUnverified(t, raw)
	if header["alg"] != "RS256" {
		t.Fatalf("alg = %v, want RS256", header["alg"])
	}
	if header["kid"] != "char-key" {
		t.Fatalf("kid = %v, want char-key", header["kid"])
	}
	if header["typ"] != "JWT" {
		t.Fatalf("typ = %v, want JWT", header["typ"])
	}
	if claims["iss"] != "orbit-auth" {
		t.Fatalf("iss = %v, want orbit-auth", claims["iss"])
	}
	if claims["sub"] != "u1" {
		t.Fatalf("sub = %v, want u1", claims["sub"])
	}
	if got := int64(claims["exp"].(float64)); got != expires.Unix() {
		t.Fatalf("exp = %d, want %d", got, expires.Unix())
	}
	if _, ok := claims["iat"].(float64); !ok {
		t.Fatal("iat claim is missing")
	}
}

func TestCharacterizationOTPRules(t *testing.T) {
	if got := charOTPTTL(); got != 10*time.Minute {
		t.Fatalf("otp ttl = %v, want 10m", got)
	}
	if got := charMaxOTPAttempts(); got != 5 {
		t.Fatalf("max otp attempts = %d, want 5", got)
	}
	if got := charAccessTokenTTL(); got != 15*time.Minute {
		t.Fatalf("access token ttl = %v, want 15m", got)
	}
	if got := charRefreshTokenTTL(); got != 30*24*time.Hour {
		t.Fatalf("refresh token ttl = %v, want 720h", got)
	}
}
