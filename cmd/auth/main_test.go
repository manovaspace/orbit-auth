package main

import (
	"reflect"
	"testing"
)

func TestDefaultCallingCodeFromEnv(t *testing.T) {
	valid := map[string]string{"": "", "98": "98", " 98 ": "98", "1": "1"}
	for value, want := range valid {
		t.Setenv("AUTH_DEFAULT_CALLING_CODE", value)
		got, err := defaultCallingCodeFromEnv()
		if err != nil || got != want {
			t.Fatalf("AUTH_DEFAULT_CALLING_CODE=%q: got %q, %v; want %q", value, got, err, want)
		}
	}
	for _, value := range []string{"+98", "98a", "9 8 +", "٩٨"} {
		t.Setenv("AUTH_DEFAULT_CALLING_CODE", value)
		if _, err := defaultCallingCodeFromEnv(); err == nil {
			t.Fatalf("AUTH_DEFAULT_CALLING_CODE=%q accepted", value)
		}
	}
}

func TestInviteAcceptHostsFromEnv(t *testing.T) {
	cases := map[string][]string{
		"":                                nil,
		" , ":                             nil,
		"App.Example.Com":                 {"app.example.com"},
		" localhost , falak.example.com ": {"localhost", "falak.example.com"},
	}
	for value, want := range cases {
		t.Setenv("AUTH_INVITE_ACCEPT_HOSTS", value)
		if got := inviteAcceptHostsFromEnv(); !reflect.DeepEqual(got, want) {
			t.Fatalf("AUTH_INVITE_ACCEPT_HOSTS=%q: got %q, want %q", value, got, want)
		}
	}
}

func TestDevSMSCodeFromEnv(t *testing.T) {
	t.Setenv("DEPLOYMENT_ENVIRONMENT", "dev")
	t.Setenv("AUTH_DEV_SMS_CODE", "")
	if got, err := devSMSCodeFromEnv(); err != nil || got != "" {
		t.Fatalf("unset: got %q, %v; want empty, nil", got, err)
	}
	t.Setenv("AUTH_DEV_SMS_CODE", "123456")
	if got, err := devSMSCodeFromEnv(); err != nil || got != "123456" {
		t.Fatalf("six digits in dev: got %q, %v", got, err)
	}
	for _, value := range []string{"12345", "1234567", "12345a", " 123456", "١٢٣٤٥٦"} {
		t.Setenv("AUTH_DEV_SMS_CODE", value)
		if _, err := devSMSCodeFromEnv(); err == nil {
			t.Fatalf("AUTH_DEV_SMS_CODE=%q accepted in dev", value)
		}
	}
}

func TestDevSMSCodeRefusedOutsideDev(t *testing.T) {
	t.Setenv("AUTH_DEV_SMS_CODE", "123456")
	for _, env := range []string{"", "staging", "production", "Dev"} {
		t.Setenv("DEPLOYMENT_ENVIRONMENT", env)
		if _, err := devSMSCodeFromEnv(); err == nil {
			t.Fatalf("DEPLOYMENT_ENVIRONMENT=%q accepted AUTH_DEV_SMS_CODE", env)
		}
	}
}
