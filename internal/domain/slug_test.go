package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateSlug(t *testing.T) {
	valid := []string{"fryto", "acme-cafe", "a1b", "team-42", strings.Repeat("a", 30)}
	for _, slug := range valid {
		if err := ValidateSlug(slug); err != nil {
			t.Errorf("ValidateSlug(%q) = %v, want nil", slug, err)
		}
	}
	invalid := []string{
		"", "ab", strings.Repeat("a", 31), "1abc", "-abc", "abc-", "ab--cd", "Fryto", "has space",
		"under_score", "dot.ted", "فرایتو", "xn--abc", "abc‍", "a/b",
		"www", "api", "admin", "falak", "internal", "demo", "staging", "login",
	}
	for _, slug := range invalid {
		if err := ValidateSlug(slug); !errors.Is(err, ErrInvalidSlug) {
			t.Errorf("ValidateSlug(%q) = %v, want ErrInvalidSlug", slug, err)
		}
	}
}

func TestSlugFromName(t *testing.T) {
	tests := map[string]string{
		"Fryto":                   "fryto",
		"  Acme Café & Co.  ":     "acme-caf-co",
		"Team 42":                 "team-42",
		"فرایتو":                  "",
		"فرایتو Cafe":             "cafe",
		"---":                     "",
		"A":                       "a",
		strings.Repeat("ab ", 20): "ab-ab-ab-ab-ab-ab-ab-ab-ab-ab",
	}
	for name, want := range tests {
		if got := SlugFromName(name); got != want {
			t.Errorf("SlugFromName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestFallbackSlugIsValid(t *testing.T) {
	slug := FallbackSlug("461C9AD2-afef-4f6c-810c-0a828a51c23f")
	if slug != "org-461c9ad2afef" {
		t.Fatalf("FallbackSlug = %q", slug)
	}
	if err := ValidateSlug(slug); err != nil {
		t.Fatalf("fallback slug invalid: %v", err)
	}
}
