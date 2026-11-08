package domain

import (
	"errors"
	"testing"
)

func TestNormalizeInviteTarget(t *testing.T) {
	email, err := NormalizeInviteTarget(InviteTarget{Email: "  Member@Example.COM "})
	if err != nil || email != (InviteTarget{Email: "member@example.com"}) {
		t.Fatalf("email target = %+v, %v", email, err)
	}

	phone, err := NormalizeInviteTarget(InviteTarget{Phone: " +989121234567 "})
	if err != nil || phone != (InviteTarget{Phone: "+989121234567"}) {
		t.Fatalf("phone target = %+v, %v", phone, err)
	}
}

func TestNormalizeInviteTargetRefusesMalformedTargets(t *testing.T) {
	cases := []struct {
		name   string
		target InviteTarget
		want   error
	}{
		{"both fields", InviteTarget{Email: "a@example.com", Phone: "+989121234567"}, ErrInvalidInviteTarget},
		{"neither field", InviteTarget{}, ErrInvalidEmail},
		{"whitespace only", InviteTarget{Email: "  ", Phone: "  "}, ErrInvalidEmail},
		{"malformed email", InviteTarget{Email: "not-an-email"}, ErrInvalidEmail},
		{"national phone format", InviteTarget{Phone: "09121234567"}, ErrInvalidPhone},
		{"phone without plus", InviteTarget{Phone: "989121234567"}, ErrInvalidPhone},
		{"phone with spaces inside", InviteTarget{Phone: "+98 912 123 4567"}, ErrInvalidPhone},
		{"phone with a leading zero country code", InviteTarget{Phone: "+0989121234567"}, ErrInvalidPhone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NormalizeInviteTarget(tc.target); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}
