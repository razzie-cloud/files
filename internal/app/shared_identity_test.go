package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aidanwoods.dev/go-paseto"
)

// A disposable key server signs the same role/method/URI contract as the gateway.
func sharedGateway(t *testing.T, origin string) (string, func(*http.Request, string, bool)) {
	t.Helper()
	key := paseto.NewV4AsymmetricSecretKey()
	sum := sha256.Sum256(key.Public().ExportBytes())
	kid := hex.EncodeToString(sum[:16])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": "https://auth.example", "expires_at": now.Add(5 * time.Minute), "keys": []any{map[string]any{"kid": kid, "public_key": key.Public().ExportHex(), "not_before": now.Add(-time.Hour), "expires_at": now.Add(time.Hour)}}})
	}))
	t.Cleanup(server.Close)
	return server.URL, func(r *http.Request, owner string, admin bool) {
		now := time.Now().UTC().Truncate(time.Second)
		token := paseto.NewToken()
		token.SetIssuer("https://auth.example")
		token.SetAudience(origin)
		token.SetSubject(owner)
		token.SetIssuedAt(now)
		token.SetNotBefore(now)
		token.SetExpiration(now.Add(time.Minute))
		token.SetString("name", "test-user")
		token.SetString("method", r.Method)
		token.SetString("uri", r.URL.RequestURI())
		_ = token.Set("admin", admin)
		token.SetFooter([]byte(kid))
		r.Header.Set("X-Auth-Request-User", owner)
		r.Header.Set("X-Auth-Request-Name", "test-user")
		r.Header.Set("X-Auth-Request-Token", token.V4Sign(key, nil))
	}
}
