package domain

import "testing"

func TestPasswordIdentifierUsableNeedsAVerifiedEmailMobileOrUsername(t *testing.T) {
	cases := []struct {
		name string
		user User
		want bool
	}{
		{"verified email", User{Email: "ali@example.com", EmailVerified: true}, true},
		{"unverified email only", User{Email: "ali@example.com"}, false},
		{"mobile", User{Mobile: "+989121230003"}, true},
		{"username", User{Username: "ali"}, true},
		{"nothing", User{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PasswordIdentifierUsable(tc.user); got != tc.want {
				t.Fatalf("PasswordIdentifierUsable = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHasUsablePasswordNeedsAHashAndAnIdentifier(t *testing.T) {
	if HasUsablePassword(User{Username: "ali"}) {
		t.Fatal("an account without a password hash has a usable password")
	}
	if HasUsablePassword(User{PasswordHash: "hash", Email: "ali@example.com"}) {
		t.Fatal("an unverified email makes the password usable")
	}
	if !HasUsablePassword(User{PasswordHash: "hash", Username: "ali"}) {
		t.Fatal("a password with a username is not usable")
	}
}
