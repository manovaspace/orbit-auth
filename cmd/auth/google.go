package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// googleConfig is the Google sign-in configuration. The secret is read from
// GOOGLE_CLIENT_SECRET_FILE and is never logged.
type googleConfig struct {
	clientID     string
	clientSecret string
	redirectURIs []string
}

// googleConfigFromEnv reads GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET_FILE and
// GOOGLE_REDIRECT_URIS. All three empty turns Google sign-in off. Some but not
// all set is an error that names each missing variable.
func googleConfigFromEnv() (googleConfig, bool, error) {
	return googleConfigFrom(os.Getenv, os.ReadFile)
}

// googleConfigFrom is googleConfigFromEnv with the environment and the file
// read injected, so tests need no real files or variables.
func googleConfigFrom(getenv func(string) string, readFile func(string) ([]byte, error)) (googleConfig, bool, error) {
	clientID := strings.TrimSpace(getenv("GOOGLE_CLIENT_ID"))
	secretFile := strings.TrimSpace(getenv("GOOGLE_CLIENT_SECRET_FILE"))
	redirectURIs := splitRedirectURIs(getenv("GOOGLE_REDIRECT_URIS"))
	if clientID == "" && secretFile == "" && len(redirectURIs) == 0 {
		return googleConfig{}, false, nil
	}

	var missing []string
	if clientID == "" {
		missing = append(missing, "GOOGLE_CLIENT_ID")
	}
	if secretFile == "" {
		missing = append(missing, "GOOGLE_CLIENT_SECRET_FILE")
	}
	if len(redirectURIs) == 0 {
		missing = append(missing, "GOOGLE_REDIRECT_URIS")
	}
	if len(missing) > 0 {
		return googleConfig{}, false, fmt.Errorf("google sign-in is partly configured: set %s too", strings.Join(missing, ", "))
	}

	raw, err := readFile(secretFile)
	if err != nil {
		return googleConfig{}, false, fmt.Errorf("GOOGLE_CLIENT_SECRET_FILE cannot be read: %w", err)
	}
	secret := strings.TrimSpace(string(raw))
	if secret == "" {
		return googleConfig{}, false, fmt.Errorf("GOOGLE_CLIENT_SECRET_FILE is empty")
	}
	for _, uri := range redirectURIs {
		if !domain.ValidRedirectURI(uri) {
			return googleConfig{}, false, fmt.Errorf("GOOGLE_REDIRECT_URIS has an entry that is not an https URL (http only for localhost or 127.0.0.1), or has a fragment or user information")
		}
	}
	return googleConfig{clientID: clientID, clientSecret: secret, redirectURIs: redirectURIs}, true, nil
}

// splitRedirectURIs splits a comma-separated list and drops blank entries.
func splitRedirectURIs(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
