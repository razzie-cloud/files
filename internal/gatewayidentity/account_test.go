package gatewayidentity

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aidanwoods.dev/go-paseto"
)

func TestAccountURLUsesPublicIssuer(t *testing.T) {
	key := paseto.NewV4AsymmetricSecretKey()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(document(time.Now().UTC(), key))
	}))
	defer server.Close()
	v, err := New(t.Context(), server.URL, testAudience)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.AccountURL("https://unrelated.example"); got != testIssuer+"/" {
		t.Fatal("account link must use public issuer, not private discovery or cloud home:", got)
	}
	v.expires = time.Time{}
	if got := v.AccountURL(""); got != testIssuer+"/" {
		t.Fatal("expired keys changed navigation address:", got)
	}
	if v.Verify(signedRequest(sign(key, time.Now().UTC().Truncate(time.Second), nil)), false) == nil {
		t.Fatal("navigation address allowed expired verification keys")
	}
}

func TestLegacyAccountURL(t *testing.T) {
	var v *Verifier
	for home, want := range map[string]string{
		"https://razzie.cloud":       "https://auth.razzie.cloud/",
		"http://localhost:8082":      "http://auth.localhost:8082/",
		"https://cloud.example:8443": "https://auth.cloud.example:8443/",
		"":                           "https://auth.razzie.cloud/",
		"https://user@evil.example":  "https://auth.razzie.cloud/",
		"javascript:alert(1)":        "https://auth.razzie.cloud/",
		"http://127.0.0.1:8080":      "https://auth.razzie.cloud/",
	} {
		if got := v.AccountURL(home); got != want {
			t.Errorf("%s: got %s, want %s", home, got, want)
		}
	}
}
