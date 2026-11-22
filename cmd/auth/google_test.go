package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// googleEnv returns a getenv over a fixed map.
func googleEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func writeSecret(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "client-secret")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGoogleConfigNoneSetIsOff(t *testing.T) {
	cfg, enabled, err := googleConfigFrom(googleEnv(map[string]string{}), os.ReadFile)
	if err != nil || enabled || cfg.clientID != "" {
		t.Fatalf("none set: cfg=%+v enabled=%v err=%v; want off", cfg, enabled, err)
	}
	// Blank values count as unset.
	cfg, enabled, err = googleConfigFrom(googleEnv(map[string]string{
		"GOOGLE_CLIENT_ID":     "  ",
		"GOOGLE_REDIRECT_URIS": " , ",
	}), os.ReadFile)
	if err != nil || enabled {
		t.Fatalf("blank values: enabled=%v err=%v; want off", enabled, err)
	}
}

func TestGoogleConfigAllSetReadsTrimmedSecret(t *testing.T) {
	secret := writeSecret(t, "  client-secret-value\n")
	cfg, enabled, err := googleConfigFrom(googleEnv(map[string]string{
		"GOOGLE_CLIENT_ID":          " client.example.com ",
		"GOOGLE_CLIENT_SECRET_FILE": secret,
		"GOOGLE_REDIRECT_URIS":      "https://app.example.com/cb, http://localhost:3000/cb,,http://127.0.0.1/cb",
	}), os.ReadFile)
	if err != nil || !enabled {
		t.Fatalf("all set: enabled=%v err=%v", enabled, err)
	}
	if cfg.clientID != "client.example.com" || cfg.clientSecret != "client-secret-value" {
		t.Fatalf("cfg = %+v", cfg)
	}
	want := []string{"https://app.example.com/cb", "http://localhost:3000/cb", "http://127.0.0.1/cb"}
	if strings.Join(cfg.redirectURIs, "|") != strings.Join(want, "|") {
		t.Fatalf("redirect URIs = %v, want %v", cfg.redirectURIs, want)
	}
}

func TestGoogleConfigPartialNamesEveryMissingVariable(t *testing.T) {
	secret := writeSecret(t, "secret")
	cases := []struct {
		name    string
		values  map[string]string
		missing string
	}{
		{"no client id", map[string]string{"GOOGLE_CLIENT_SECRET_FILE": secret, "GOOGLE_REDIRECT_URIS": "https://a.example.com/cb"}, "GOOGLE_CLIENT_ID"},
		{"no secret file", map[string]string{"GOOGLE_CLIENT_ID": "id", "GOOGLE_REDIRECT_URIS": "https://a.example.com/cb"}, "GOOGLE_CLIENT_SECRET_FILE"},
		{"no redirect uris", map[string]string{"GOOGLE_CLIENT_ID": "id", "GOOGLE_CLIENT_SECRET_FILE": secret}, "GOOGLE_REDIRECT_URIS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, enabled, err := googleConfigFrom(googleEnv(tc.values), os.ReadFile)
			if err == nil || enabled {
				t.Fatalf("partial config accepted: enabled=%v err=%v", enabled, err)
			}
			if !strings.Contains(err.Error(), tc.missing) {
				t.Fatalf("error %q does not name %s", err, tc.missing)
			}
		})
	}
}

func TestGoogleConfigRejectsBadSecretFile(t *testing.T) {
	values := map[string]string{
		"GOOGLE_CLIENT_ID":          "id",
		"GOOGLE_CLIENT_SECRET_FILE": filepath.Join(t.TempDir(), "missing"),
		"GOOGLE_REDIRECT_URIS":      "https://a.example.com/cb",
	}
	if _, enabled, err := googleConfigFrom(googleEnv(values), os.ReadFile); err == nil || enabled {
		t.Fatalf("missing secret file accepted: enabled=%v err=%v", enabled, err)
	}
	values["GOOGLE_CLIENT_SECRET_FILE"] = writeSecret(t, " \n\t ")
	if _, enabled, err := googleConfigFrom(googleEnv(values), os.ReadFile); err == nil || enabled {
		t.Fatalf("blank secret file accepted: enabled=%v err=%v", enabled, err)
	}
}

func TestGoogleConfigRejectsBadRedirectURIs(t *testing.T) {
	secret := writeSecret(t, "secret")
	bad := []string{
		"http://app.example.com/cb",
		"ftp://app.example.com/cb",
		"https:///cb",
		"https://user:pass@app.example.com/cb",
		"https://app.example.com/cb#fragment",
		"https://app.example.com/cb#",
		"not a url",
		"https://app.example.com/cb, http://example.com/cb",
	}
	for _, uri := range bad {
		values := map[string]string{
			"GOOGLE_CLIENT_ID":          "id",
			"GOOGLE_CLIENT_SECRET_FILE": secret,
			"GOOGLE_REDIRECT_URIS":      uri,
		}
		_, enabled, err := googleConfigFrom(googleEnv(values), os.ReadFile)
		if err == nil || enabled {
			t.Fatalf("redirect %q accepted", uri)
		}
		if strings.Contains(err.Error(), "user:pass") {
			t.Fatalf("error echoes a redirect URI: %v", err)
		}
	}
}

func TestGoogleConfigReadErrorDoesNotPanic(t *testing.T) {
	values := map[string]string{
		"GOOGLE_CLIENT_ID":          "id",
		"GOOGLE_CLIENT_SECRET_FILE": "/unused",
		"GOOGLE_REDIRECT_URIS":      "https://a.example.com/cb",
	}
	_, _, err := googleConfigFrom(googleEnv(values), func(string) ([]byte, error) { return nil, errors.New("denied") })
	if err == nil {
		t.Fatal("unreadable secret accepted")
	}
}
