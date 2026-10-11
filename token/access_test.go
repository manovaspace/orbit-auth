package token

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func generateTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestSignAndParseAccessToken(t *testing.T) {
	key := generateTestKey(t)
	wantSubject := "orbit-user-123"
	raw, err := SignAccessToken(key, "key-1", wantSubject, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	claims, err := ParseAccessToken(raw, KeySet{"key-1": &key.PublicKey})
	if err != nil {
		t.Fatal(err)
	}
	if claims.Issuer != Issuer || claims.Subject != wantSubject || claims.ExpiresAt == nil || claims.IssuedAt == nil {
		t.Fatalf("unexpected verified claims: %+v", claims.RegisteredClaims)
	}
}

func TestParseAccessTokenFailsClosed(t *testing.T) {
	key := generateTestKey(t)
	valid, err := SignAccessToken(key, "key-1", "user-1", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	expired, err := SignAccessToken(key, "key-1", "user-1", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	missingKID := jwt.NewWithClaims(jwt.SigningMethodRS256, AccessClaims{RegisteredClaims: jwt.RegisteredClaims{
		Issuer: Issuer, Subject: "user-1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}})
	missingKIDRaw, err := missingKID.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	wrongAlgorithm := jwt.NewWithClaims(jwt.SigningMethodHS256, AccessClaims{RegisteredClaims: jwt.RegisteredClaims{
		Issuer: Issuer, Subject: "user-1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}})
	wrongAlgorithm.Header["kid"] = "key-1"
	wrongAlgorithmRaw, err := wrongAlgorithm.SignedString([]byte("not-an-rsa-key"))
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		raw  string
		keys KeySet
	}{
		{name: "expired token", raw: expired, keys: KeySet{"key-1": &key.PublicKey}},
		{name: "missing key ID", raw: missingKIDRaw, keys: KeySet{"key-1": &key.PublicKey}},
		{name: "unknown key ID", raw: valid, keys: KeySet{"other": &key.PublicKey}},
		{name: "wrong algorithm", raw: wrongAlgorithmRaw, keys: KeySet{"key-1": &key.PublicKey}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseAccessToken(test.raw, test.keys); err == nil {
				t.Fatal("ParseAccessToken() accepted an invalid token")
			}
		})
	}
}

func TestKeySetDocumentRoundTrip(t *testing.T) {
	key := generateTestKey(t)
	document := (KeySet{"key-1": &key.PublicKey}).Document()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var decoded JWKSet
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	keys, err := decoded.KeySet()
	if err != nil {
		t.Fatal(err)
	}
	if keys["key-1"].N.Cmp(key.N) != 0 || keys["key-1"].E != key.E {
		t.Fatal("JWKS round trip changed the RSA public key")
	}
}

func TestRemoteVerifierRefreshesForNewKeyID(t *testing.T) {
	oldKey := generateTestKey(t)
	newKey := generateTestKey(t)
	var activeDocument atomic.Value
	activeDocument.Store((KeySet{"old": &oldKey.PublicKey}).Document())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(activeDocument.Load())
	}))
	defer server.Close()

	verifier, err := NewRemoteVerifier(server.URL, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	activeDocument.Store((KeySet{"new": &newKey.PublicKey}).Document())
	raw, err := SignAccessToken(newKey, "new", "user-1", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifier.ParseAccessToken(raw)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "user-1" {
		t.Fatalf("subject = %q, want user-1", claims.Subject)
	}
}
