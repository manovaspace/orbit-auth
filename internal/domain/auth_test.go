package domain

import (
	"testing"
	"time"
)

func TestHashRefreshTokenIsStableSHA256(t *testing.T) {
	// SHA-256("char-refresh-token")
	const want = "6a9c19bc50ab57612a3cf34b2db5243c36eb61071570c43161bf0d365cf63ece"
	got := HashRefreshToken("char-refresh-token")
	if len(got) != 64 {
		t.Fatalf("hash length = %d, want 64", len(got))
	}
	if got != want {
		t.Fatalf("HashRefreshToken = %q, want %q", got, want)
	}
	if got == HashRefreshToken("other") {
		t.Fatal("different tokens produced the same hash")
	}
}

func TestHashIdentifierIsStableShortDigest(t *testing.T) {
	// The first 8 bytes of SHA-256("person@example.com").
	const want = "542d240129883c01"
	got := HashIdentifier("person@example.com")
	if len(got) != 16 {
		t.Fatalf("hash length = %d, want 16", len(got))
	}
	if got != want {
		t.Fatalf("HashIdentifier = %q, want %q", got, want)
	}
}

func TestIsE164(t *testing.T) {
	accepted := []string{"+989121234567", "+14155552671", "+1234567", "+123456789012345"}
	rejected := []string{"", "09121234567", "989121234567", "+0123456789", "+1", "+123456", "+1234567890123456", "+98 912 123 4567", "person@example.com"}
	for _, value := range accepted {
		if !IsE164(value) {
			t.Fatalf("%q was rejected but is valid E.164", value)
		}
	}
	for _, value := range rejected {
		if IsE164(value) {
			t.Fatalf("%q was accepted but is not valid E.164", value)
		}
	}
}

func TestTokenRulesArePinned(t *testing.T) {
	if OTPTTL != 10*time.Minute {
		t.Fatalf("OTPTTL = %v, want 10m", OTPTTL)
	}
	if MaxOTPAttempts != 5 {
		t.Fatalf("MaxOTPAttempts = %d, want 5", MaxOTPAttempts)
	}
	if AccessTokenTTL != 15*time.Minute {
		t.Fatalf("AccessTokenTTL = %v, want 15m", AccessTokenTTL)
	}
	if RefreshTokenTTL != 30*24*time.Hour {
		t.Fatalf("RefreshTokenTTL = %v, want 720h", RefreshTokenTTL)
	}
	if APITokenPrefixLength != 8 {
		t.Fatalf("APITokenPrefixLength = %d, want 8", APITokenPrefixLength)
	}
	if APITokenScheme != "oat_" {
		t.Fatalf("APITokenScheme = %q, want oat_", APITokenScheme)
	}
}
