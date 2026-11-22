package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeUsername(t *testing.T) {
	valid := map[string]string{
		"chef":                        "chef",
		"Chef.One":                    "chef.one",
		"  Chef_One-2  ":              "chef_one-2",
		"abc":                         "abc",
		"a" + strings.Repeat("b", 31): "a" + strings.Repeat("b", 31),
	}
	for in, want := range valid {
		got, ok := NormalizeUsername(in)
		if !ok || got != want {
			t.Errorf("NormalizeUsername(%q) = %q, %v; want %q, true", in, got, ok, want)
		}
	}
	invalid := []string{
		"", "ab", "1abc", "a b", "+abc", "a@b.com", "_abc", "-abc", "abc!",
		"a" + strings.Repeat("b", 32), "چef", "abc‍",
	}
	for _, in := range invalid {
		if got, ok := NormalizeUsername(in); ok {
			t.Errorf("NormalizeUsername(%q) = %q, true; want rejection", in, got)
		}
	}
}

func TestParseLoginIdentifier(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		callingCode string
		kind        LoginIdentifierKind
		want        string
		ok          bool
	}{
		{"e164 with plus", "+989121234567", "98", LoginIdentifierMobile, "+989121234567", true},
		{"e164 spaces and dashes", " +98 912 123-4567 ", "", LoginIdentifierMobile, "+989121234567", true},
		{"e164 without calling code", "+989121234567", "", LoginIdentifierMobile, "+989121234567", true},
		{"international 00 prefix", "00989121234567", "", LoginIdentifierMobile, "+989121234567", true},
		{"local number with calling code", "09121234567", "98", LoginIdentifierMobile, "+989121234567", true},
		{"local number with brackets", "(0912) 123 4567", "98", LoginIdentifierMobile, "+989121234567", true},
		{"calling code without plus", "989121234567", "98", LoginIdentifierMobile, "+989121234567", true},
		{"persian digits", "۰۹۱۲۱۲۳۴۵۶۷", "98", LoginIdentifierMobile, "+989121234567", true},
		{"arabic-indic digits", "٠٩١٢١٢٣٤٥٦٧", "98", LoginIdentifierMobile, "+989121234567", true},
		{"email", " Chef@Example.com ", "98", LoginIdentifierEmail, "chef@example.com", true},
		{"plain email", "chef@example.com", "", LoginIdentifierEmail, "chef@example.com", true},
		{"username", "Chef.One", "98", LoginIdentifierUsername, "chef.one", true},
		{"username with digits and symbols", "chef_one-2", "", LoginIdentifierUsername, "chef_one-2", true},
		{"local number without calling code", "09121234567", "", "", "", false},
		{"local number with zero zero is not local", "0098912", "98", "", "", false},
		{"empty", "", "98", "", "", false},
		{"blank", "   ", "98", "", "", false},
		{"too short", "ab", "98", "", "", false},
		{"digit first", "1abc", "98", "", "", false},
		{"space inside", "a b", "98", "", "", false},
		{"plus then letters", "+abc", "98", "", "", false},
		{"33 characters", "a" + strings.Repeat("b", 32), "98", "", "", false},
		{"bare zero", "0", "98", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kind, got, ok := ParseLoginIdentifier(tc.value, tc.callingCode)
			if ok != tc.ok || kind != tc.kind || got != tc.want {
				t.Fatalf("ParseLoginIdentifier(%q, %q) = %q, %q, %v; want %q, %q, %v",
					tc.value, tc.callingCode, kind, got, ok, tc.kind, tc.want, tc.ok)
			}
		})
	}
}

func TestParseLoginIdentifierNeverSwapsKinds(t *testing.T) {
	// A username cannot start with a digit, '@' or '+', so no username can be
	// read as a mobile number or an email address.
	for _, value := range []string{"chef", "chef.one", "abc123"} {
		kind, _, ok := ParseLoginIdentifier(value, "98")
		if !ok || kind != LoginIdentifierUsername {
			t.Fatalf("ParseLoginIdentifier(%q) kind = %q, ok = %v; want username", value, kind, ok)
		}
	}
}

func TestUsernameErrorsAreDistinct(t *testing.T) {
	if errors.Is(ErrInvalidUsername, ErrUsernameTaken) || errors.Is(ErrUsernameTaken, ErrInvalidUsername) {
		t.Fatal("username errors must be distinct")
	}
}
