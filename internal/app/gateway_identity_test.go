package app

import (
	"aidanwoods.dev/go-paseto"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/razzie-cloud/files/internal/config"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSignedIdentityRequired(t *testing.T) {
	const origin = "https://files.example"
	const ownerID = "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa"
	key := paseto.NewV4AsymmetricSecretKey()
	sum := sha256.Sum256(key.Public().ExportBytes())
	kid := hex.EncodeToString(sum[:16])
	now := time.Now().UTC().Truncate(time.Second)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": "https://auth.example", "expires_at": now.Add(5 * time.Minute), "keys": []any{map[string]any{"kid": kid, "public_key": key.Public().ExportHex(), "not_before": now.Add(-time.Hour), "expires_at": now.Add(time.Hour)}}})
	}))
	defer gateway.Close()
	server, err := New(config.Config{Origin: origin, ShareOrigin: "https://shared.example", AccessGatewayURL: gateway.URL, MaxUploads: 1}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	request := func(path string, signed, admin bool) *http.Request {
		r := httptest.NewRequest("GET", origin+path, nil)
		r.Header.Set("X-Auth-Request-User", ownerID)
		r.Header.Set("X-Auth-Request-Name", "alice")
		if signed {
			token := paseto.NewToken()
			token.SetIssuer("https://auth.example")
			token.SetAudience(origin)
			token.SetSubject(ownerID)
			token.SetIssuedAt(now)
			token.SetNotBefore(now)
			token.SetExpiration(now.Add(time.Minute))
			token.SetString("name", "alice")
			token.SetString("method", "GET")
			token.SetString("uri", path)
			_ = token.Set("admin", admin)
			token.SetFooter([]byte(kid))
			r.Header.Set("X-Auth-Request-Token", token.V4Sign(key, nil))
		}
		return r
	}
	for _, signed := range []bool{false, true} {
		r := request("/static/app.css", signed, false)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := 401
		if signed {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("signed=%v: got %d want %d: %s", signed, w.Code, want, w.Body.String())
		}
	}
	r := request("/static/app.css", true, false)
	r.Header.Set("X-Auth-Request-Name", "mallory")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("tampered name accepted")
	}
	r = httptest.NewRequest("GET", origin+"/healthz", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("health probe requires signature")
	}
	r = httptest.NewRequest("GET", "https://shared.example/static/app.css", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("public share asset requires signature")
	}
}
