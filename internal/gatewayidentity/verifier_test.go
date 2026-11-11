package gatewayidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"aidanwoods.dev/go-paseto"
)

const testIssuer = "https://auth.example"
const testAudience = "https://files.example"
const testUser = "dd9dd321-d642-4391-8179-14cd80ba7197"

func keyID(key paseto.V4AsymmetricSecretKey) string {
	sum := sha256.Sum256(key.Public().ExportBytes())
	return hex.EncodeToString(sum[:16])
}
func sign(key paseto.V4AsymmetricSecretKey, now time.Time, change func(*paseto.Token)) string {
	token := paseto.NewToken()
	token.SetIssuer(testIssuer)
	token.SetAudience(testAudience)
	token.SetSubject(testUser)
	token.SetIssuedAt(now)
	token.SetNotBefore(now)
	token.SetExpiration(now.Add(time.Minute))
	token.SetString("name", "alice")
	token.SetString("method", "GET")
	token.SetString("uri", "/v1/notes?q=one")
	_ = token.Set("admin", false)
	token.SetFooter([]byte(keyID(key)))
	if change != nil {
		change(&token)
	}
	return token.V4Sign(key, nil)
}
func signedRequest(signed string) *http.Request {
	r := httptest.NewRequest("GET", "https://files.example/v1/notes?q=one", nil)
	r.Header.Set(Header, signed)
	r.Header.Set("X-Auth-Request-User", testUser)
	r.Header.Set("X-Auth-Request-Name", "alice")
	return r
}
func document(now time.Time, keys ...paseto.V4AsymmetricSecretKey) keySet {
	doc := keySet{Issuer: testIssuer, ExpiresAt: now.Add(5 * time.Minute)}
	for _, key := range keys {
		doc.Keys = append(doc.Keys, publicKey{keyID(key), key.Public().ExportHex(), now.Add(-time.Hour), now.Add(time.Hour)})
	}
	return doc
}
func TestRequestVerification(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	key := paseto.NewV4AsymmetricSecretKey()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/paseto-keys" {
			t.Errorf("wrong discovery path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(document(now, key))
	}))
	defer srv.Close()
	v, err := New(t.Context(), srv.URL, testAudience)
	if err != nil {
		t.Fatal(err)
	}
	v.now = func() time.Time { return now }
	valid := sign(key, now, nil)
	if err := v.Verify(signedRequest(valid), false); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*paseto.Token){
		"issuer":           func(t *paseto.Token) { t.SetIssuer("https://evil.example") },
		"audience":         func(t *paseto.Token) { t.SetAudience("https://notes.example") },
		"expired":          func(t *paseto.Token) { t.SetExpiration(now) },
		"future":           func(t *paseto.Token) { t.SetIssuedAt(now.Add(time.Second)) },
		"not before":       func(t *paseto.Token) { t.SetNotBefore(now.Add(time.Second)) },
		"long lifetime":    func(t *paseto.Token) { t.SetExpiration(now.Add(2 * time.Minute)) },
		"uuid":             func(t *paseto.Token) { t.SetSubject("other") },
		"name":             func(t *paseto.Token) { t.SetString("name", "mallory") },
		"method":           func(t *paseto.Token) { t.SetString("method", "POST") },
		"uri":              func(t *paseto.Token) { t.SetString("uri", "/v1/notes?q=two") },
		"wrong admin type": func(t *paseto.Token) { t.SetString("admin", "true") },
	} {
		t.Run(name, func(t *testing.T) {
			if v.Verify(signedRequest(sign(key, now, change)), false) == nil {
				t.Fatal("accepted invalid claims")
			}
		})
	}
	for name, mutate := range map[string]func(*http.Request){
		"missing":         func(r *http.Request) { r.Header.Del(Header) },
		"duplicate token": func(r *http.Request) { r.Header.Add(Header, valid) },
		"duplicate uuid":  func(r *http.Request) { r.Header.Add("X-Auth-Request-User", testUser) },
		"duplicate name":  func(r *http.Request) { r.Header.Add("X-Auth-Request-Name", "alice") },
		"tampered":        func(r *http.Request) { r.Header.Set(Header, strings.Replace(valid, "v4.public.", "v4.public.A", 1)) },
		"oversized":       func(r *http.Request) { r.Header.Set(Header, strings.Repeat("x", 4097)) },
		"other key":       func(r *http.Request) { r.Header.Set(Header, sign(paseto.NewV4AsymmetricSecretKey(), now, nil)) },
	} {
		t.Run(name, func(t *testing.T) {
			r := signedRequest(valid)
			mutate(r)
			if v.Verify(r, false) == nil {
				t.Fatal("accepted invalid headers")
			}
		})
	}
	if v.Verify(signedRequest(valid), true) == nil {
		t.Fatal("ordinary assertion granted admin")
	}
	admin := sign(key, now, func(t *paseto.Token) { _ = t.Set("admin", true) })
	if err := v.Verify(signedRequest(admin), true); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshRotationOutageAndConcurrency(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	first, next := paseto.NewV4AsymmetricSecretKey(), paseto.NewV4AsymmetricSecretKey()
	var mu sync.Mutex
	doc, status, calls := document(now, first, next), http.StatusOK, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(doc)
	}))
	defer srv.Close()
	v, err := New(t.Context(), srv.URL, testAudience)
	if err != nil {
		t.Fatal(err)
	}
	v.now = func() time.Time { return now }
	if err := v.Verify(signedRequest(sign(next, now, nil)), false); err != nil {
		t.Fatal("prepublished rotation failed:", err)
	}
	mu.Lock()
	status = 503
	mu.Unlock()
	if err := v.Verify(signedRequest(sign(first, now, nil)), false); err != nil {
		t.Fatal("unexpired keys failed:", err)
	}
	now = now.Add(6 * time.Minute)
	if v.Verify(signedRequest(sign(first, now, nil)), false) == nil {
		t.Fatal("stale keys accepted during outage")
	}
	mu.Lock()
	status = 200
	doc = document(now, next)
	mu.Unlock()
	now = now.Add(6 * time.Second)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if err := v.Verify(signedRequest(sign(next, now, nil)), false); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if calls != 3 {
		t.Fatalf("concurrent refresh made %d calls, want 3", calls)
	}
}

func TestDiscoveryValidation(t *testing.T) {
	for _, raw := range []string{"https://user:pass@auth.example", "https://auth.example/path", "https://auth.example?", "https://auth.example#x", "file:///tmp/keys"} {
		if _, err := New(t.Context(), raw, testAudience); err == nil {
			t.Errorf("accepted URL %q", raw)
		}
	}
	now := time.Now().UTC()
	key := paseto.NewV4AsymmetricSecretKey()
	for _, name := range []string{"redirect", "large", "expired", "duplicate", "invalid key", "key id", "issuer", "empty"} {
		t.Run(name, func(t *testing.T) {
			doc := document(now, key)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch name {
				case "redirect":
					w.Header().Set("Location", "/keys")
					w.WriteHeader(302)
					return
				case "large":
					_, _ = w.Write([]byte(strings.Repeat("x", 16385)))
					return
				case "expired":
					doc.ExpiresAt = now.Add(-time.Second)
				case "duplicate":
					doc.Keys = append(doc.Keys, doc.Keys[0])
				case "invalid key":
					doc.Keys[0].PublicKey = "bad"
				case "key id":
					doc.Keys[0].ID = "bad"
				case "issuer":
					doc.Issuer = "file:///bad"
				case "empty":
					doc.Keys = nil
				}
				_ = json.NewEncoder(w).Encode(doc)
			}))
			defer srv.Close()
			if _, err := New(t.Context(), srv.URL, testAudience); err == nil {
				t.Fatal("accepted invalid discovery")
			}
		})
	}
	var disabled *Verifier
	if disabled.Verify(httptest.NewRequest("GET", "/", nil), true) != nil {
		t.Fatal("disabled verifier must preserve proxy mode")
	}
}
