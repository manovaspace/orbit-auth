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
