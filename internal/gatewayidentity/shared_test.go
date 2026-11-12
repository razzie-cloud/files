package gatewayidentity

import (
	"aidanwoods.dev/go-paseto"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAdminAuthorityRequiresSignedRole(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	key := paseto.NewV4AsymmetricSecretKey()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(document(now, key)) }))
	defer server.Close()
	verifier, err := New(t.Context(), server.URL, testAudience)
	if err != nil {
		t.Fatal(err)
	}
	for _, admin := range []bool{false, true} {
		req := signedRequest(sign(key, now, func(token *paseto.Token) { _ = token.Set("admin", admin) }))
		req.Header.Set("X-Auth-Request-Admin", "true")
		if verifier.IsAdmin(req) != admin {
			t.Fatal("role did not follow signed claim")
		}
		var legacy *Verifier
		if legacy.IsAdmin(req) {
			t.Fatal("legacy mode granted admin")
		}
		req.Header.Del(Header)
		if verifier.IsAdmin(req) {
			t.Fatal("unsigned request granted admin")
		}
	}
}
