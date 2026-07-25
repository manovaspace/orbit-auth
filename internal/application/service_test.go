package application

import (
	"context"
	"testing"
	"time"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

type fakeOTP struct {
	ch       domain.OTPChallenge
	consumed bool
}

func (f *fakeOTP) UpsertChallenge(_ context.Context, ch domain.OTPChallenge) error {
	f.ch = ch
	f.consumed = false
	return nil
}

func (f *fakeOTP) GetLatestChallenge(_ context.Context, identifier, channel string) (domain.OTPChallenge, error) {
	if f.consumed {
		return domain.OTPChallenge{}, context.Canceled
	}
	if f.ch.Identifier == identifier && f.ch.Channel == channel {
		return f.ch, nil
	}
	return domain.OTPChallenge{}, context.Canceled
}

func (f *fakeOTP) IncrementAttempts(context.Context, string) error { return nil }

func (f *fakeOTP) ConsumeChallenge(_ context.Context, id string) error {
	if f.ch.ID == id {
		f.consumed = true
	}
	return nil
}

func (f *fakeOTP) DeleteExpiredChallenges(context.Context) error { return nil }

type fakeUsers struct {
	user domain.User
}

func (f *fakeUsers) FindOrCreateByEmail(_ context.Context, email string) (domain.User, error) {
	if f.user.Email == "" {
		f.user = domain.User{ID: "u1", Email: email}
	}
	return f.user, nil
}

func (f *fakeUsers) FindOrCreateByMobile(_ context.Context, mobile string) (domain.User, error) {
	if f.user.Mobile == "" {
		f.user = domain.User{ID: "u1", Mobile: mobile}
	}
	return f.user, nil
}

func (f *fakeUsers) FindByEmail(_ context.Context, email string) (domain.User, error) {
	return f.user, nil
}

func (f *fakeUsers) UpdateLastLogin(context.Context, string) error { return nil }

func (f *fakeUsers) UpsertDemoUser(context.Context, string, string, string) error { return nil }

type fakeApiTokens struct{}

func (fakeApiTokens) Create(context.Context, domain.ApiToken) error { return nil }
func (fakeApiTokens) ListByUser(context.Context, string) ([]domain.ApiTokenInfo, error) {
	return nil, nil
}
func (fakeApiTokens) RevokeToken(context.Context, string, string) error { return nil }
func (fakeApiTokens) FindByPrefix(context.Context, string) (domain.ApiToken, error) {
	return domain.ApiToken{}, context.Canceled
}

type fakeRefresh struct{}

func (fakeRefresh) Store(context.Context, string, string, time.Time) error { return nil }
func (fakeRefresh) FindValid(context.Context, string) (string, error) {
	return "", context.Canceled
}
func (fakeRefresh) Revoke(context.Context, string) error { return nil }

type fakeNotifier struct {
	lastChannel   string
	lastRecipient string
}

func (f *fakeNotifier) SendOTP(_ context.Context, channel, recipient, _, _ string) error {
	f.lastChannel = channel
	f.lastRecipient = recipient
	return nil
}

type fakeFlags struct{ mobile, email bool }

func (f fakeFlags) Bool(_ context.Context, flag string, _ bool) bool {
	switch flag {
	case "manova.auth.mobile_otp":
		return f.mobile
	case "manova.auth.email_otp":
		return f.email
	default:
		return false
	}
}

func mustService(t *testing.T, otp domain.OTPRepository, users domain.UserRepository, n domain.Notifier, flags FlagEvaluator) *Service {
	t.Helper()
	t.Setenv("DEPLOYMENT_ENVIRONMENT", "dev")
	t.Setenv("JWT_SECRET", "test-secret")
	svc, err := NewService(otp, users, fakeRefresh{}, fakeApiTokens{}, n, flags, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestRequestOTP_MobileRequiresE164(t *testing.T) {
	svc := mustService(t, &fakeOTP{}, &fakeUsers{}, &fakeNotifier{}, fakeFlags{mobile: true})
	_, err := svc.RequestOTP(context.Background(), "09121234567", domain.ChannelSMS, "c1")
	if err == nil {
		t.Fatal("expected E.164 error")
	}
}

func TestRequestOTP_MobileHappyPath(t *testing.T) {
	otp := &fakeOTP{}
	notifier := &fakeNotifier{}
	svc := mustService(t, otp, &fakeUsers{}, notifier, fakeFlags{mobile: true})
	_, err := svc.RequestOTP(context.Background(), "+989121234567", domain.ChannelSMS, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if notifier.lastChannel != domain.ChannelSMS {
		t.Fatalf("channel %q", notifier.lastChannel)
	}
	if otp.ch.Channel != domain.ChannelSMS {
		t.Fatalf("stored channel %q", otp.ch.Channel)
	}
}
