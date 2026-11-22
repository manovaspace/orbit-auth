package domain

import (
	"strings"
	"testing"
)

func TestValidateReturnTo(t *testing.T) {
	ok := map[string]string{
		"":                              "/",
		"/":                             "/",
		"/inbox":                        "/inbox",
		"/a/b?c=d#e":                    "/a/b?c=d#e",
		"/سلام":                         "/سلام",
		"/" + strings.Repeat("a", 1023): "/" + strings.Repeat("a", 1023),
	}
	for value, want := range ok {
		got, valid := ValidateReturnTo(value)
		if !valid || got != want {
			t.Fatalf("ValidateReturnTo(%q) = %q, %v; want %q, true", value, got, valid, want)
		}
	}

	bad := []string{
		"//evil.example.com",
		"/\\evil.example.com",
		"\\\\evil",
		"https://evil.example.com",
		"http://x",
		"javascript:alert(1)",
		"inbox",
		"/\x00",
		"/a\nb",
		"/a\tb",
		"/a\\b",
		"/" + strings.Repeat("a", 1024),
		"/\xff",
	}
	for _, value := range bad {
		if got, valid := ValidateReturnTo(value); valid {
			t.Fatalf("ValidateReturnTo(%q) accepted as %q", value, got)
		}
	}
}

func TestGoogleEmailIsAuthoritative(t *testing.T) {
	cases := []struct {
		name string
		id   GoogleIdentity
		want bool
	}{
		{"gmail verified", GoogleIdentity{Email: "Ali@Gmail.com", EmailVerified: true}, true},
		{"gmail unverified", GoogleIdentity{Email: "ali@gmail.com"}, false},
		{"gmail is not a hosted domain", GoogleIdentity{Email: "ali@gmail.com", EmailVerified: true, HostedDomain: "gmail.com"}, true},
		{"workspace domain matches hosted domain", GoogleIdentity{Email: "ali@Example.COM", EmailVerified: true, HostedDomain: "example.com"}, true},
		{"workspace domain without hosted domain", GoogleIdentity{Email: "ali@example.com", EmailVerified: true}, false},
		{"hosted domain differs", GoogleIdentity{Email: "ali@example.com", EmailVerified: true, HostedDomain: "other.example"}, false},
		{"subdomain is not the hosted domain", GoogleIdentity{Email: "ali@mail.example.com", EmailVerified: true, HostedDomain: "example.com"}, false},
		{"googlemail is not gmail", GoogleIdentity{Email: "ali@googlemail.com", EmailVerified: true}, false},
		{"empty email", GoogleIdentity{EmailVerified: true, HostedDomain: "example.com"}, false},
		{"no at sign", GoogleIdentity{Email: "ali.gmail.com", EmailVerified: true}, false},
		{"empty domain", GoogleIdentity{Email: "ali@", EmailVerified: true}, false},
		{"empty local part", GoogleIdentity{Email: "@gmail.com", EmailVerified: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := GoogleEmailIsAuthoritative(tc.id); got != tc.want {
				t.Fatalf("GoogleEmailIsAuthoritative(%+v) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

func TestValidRedirectURI(t *testing.T) {
	ok := []string{
		"https://app.example.com/auth/google/callback",
		"https://app.example.com:8443/cb?x=1",
		"http://localhost:3000/cb",
		"http://127.0.0.1/cb",
	}
	for _, value := range ok {
		if !ValidRedirectURI(value) {
			t.Fatalf("%q rejected", value)
		}
	}
	bad := []string{
		"",
		"http://app.example.com/cb",
		"http://localhost.example.com/cb",
		"ftp://app.example.com/cb",
		"https://",
		"https://user@app.example.com/cb",
		"https://app.example.com/cb#frag",
		"https://app.example.com/cb#",
		"app.example.com/cb",
	}
	for _, value := range bad {
		if ValidRedirectURI(value) {
			t.Fatalf("%q accepted", value)
		}
	}
}
