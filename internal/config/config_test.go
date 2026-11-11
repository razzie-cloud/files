package config

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoadValidation(t *testing.T) {
	base := map[string]string{"ORIGIN": "https://files.example.com", "POSTGRES_URI": "postgres://u:p@localhost/files"}
	if _, e := Load(nil, func(k string) string { return base[k] }); e != nil {
		t.Fatal(e)
	}
	for _, test := range []struct{ key, value string }{{"LANDING_ORIGIN", "javascript:alert(1)"}, {"LANDING_ORIGIN", "https://home.example/path"}, {"ORIGIN", "https://files.example.com/path"}, {"POSTGRES_URI_FROM", "http://broker/uri"}, {"MAX_UPLOAD_BYTES", "0"}, {"S3_ALLOW_HTTP", "maybe"}, {"MAX_CONCURRENT_UPLOADS", "999"}} {
		t.Run(test.key, func(t *testing.T) {
			env := func(k string) string {
				if k == test.key {
					return test.value
				}
				return base[k]
			}
			if _, e := Load(nil, env); e == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}
func TestBrokerDiscovery(t *testing.T) {
	for _, test := range []struct {
		name, body string
		code       int
		valid      bool
	}{{"valid", "postgres://u:p@localhost/files\n", 200, true}, {"bad uri", "not a uri", 200, false}, {"oversized", strings.Repeat("x", 4097), 200, false}, {"redirect", "", 302, false}, {"error", "not found", 404, false}} {
		t.Run(test.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(test.code); w.Write([]byte(test.body)) }))
			defer srv.Close()
			uri, e := (Config{PostgresURIFrom: srv.URL}).DatabaseURI(context.Background())
			if (e == nil) != test.valid {
				t.Fatalf("valid=%v error=%v uri=%q", test.valid, e, uri)
			}
		})
	}
}
func TestBrokerRetriesAndCancellation(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte("postgres://u:p@localhost/files"))
	}))
	defer srv.Close()
	if _, e := (Config{PostgresURIFrom: srv.URL}).DatabaseURI(context.Background()); e != nil || calls != 2 {
		t.Fatalf("calls=%d error=%v", calls, e)
	}
}

func TestShareOriginValidation(t *testing.T) {
	for _, origin := range []string{"", "https://shared-files.example.com", "http://shared-files.localhost:8082"} {
		cfg, err := Load(nil, func(key string) string {
			switch key {
			case "ORIGIN":
				return "https://files.example.com"
			case "SHARE_ORIGIN":
				return origin
			case "POSTGRES_URI":
				return "postgres://u:p@localhost/files"
			}
			return ""
		})
		if err != nil || cfg.ShareOrigin != origin {
			t.Fatalf("valid share origin %q: %v", origin, err)
		}
	}
	for _, origin := range []string{"https://files.example.com", "http://FILES.example.com:8083", "https://files.example.com.", "https://shared.example.com/path", "https://user@shared.example.com", "https://shared.example.com?q=1", "https://shared.example.com#fragment", "https://shared.example.com?", "ftp://shared.example.com", "shared.example.com"} {
		if _, err := Load([]string{"--share-origin", origin}, func(key string) string {
			switch key {
			case "ORIGIN":
				return "https://files.example.com"
			case "POSTGRES_URI":
				return "postgres://u:p@localhost/files"
			}
			return ""
		}); err == nil {
			t.Fatalf("invalid share origin accepted: %q", origin)
		}
	}
}

func TestCastShareTTL(t *testing.T) {
	env := func(key string) string {
		switch key {
		case "ORIGIN":
			return "https://files.example.com"
		case "POSTGRES_URI":
			return "postgres://u:p@localhost/files"
		}
		return ""
	}
	cfg, err := Load(nil, env)
	if err != nil || cfg.CastShareTTL.String() != "6h0m0s" {
		t.Fatal("default TTL", cfg.CastShareTTL, err)
	}
	for _, value := range []string{"1s", "1h", "24h"} {
		if _, err := Load([]string{"--cast-share-ttl", value}, env); err != nil {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"0", "-1h", "24h1s", "forever"} {
		if _, err := Load(nil, func(key string) string {
			if key == "CAST_SHARE_TTL" {
				return value
			}
			return env(key)
		}); err == nil {
			t.Fatal("invalid TTL", value)
		}
	}
}

func TestLoadBrokerTokenAndTrustFlags(t *testing.T) {
	env := func(key string) string {
		if key == "POSTGRES_URI_FROM" {
			return "https://broker/uri?client=app"
		}
		if key == "ORIGIN" {
			return "https://app.example"
		}
		return ""
	}
	c, e := Load([]string{"--broker-token-file", "/missing-token", "--broker-ca-file", "/missing-ca"}, env)
	if e != nil || c.BrokerTokenFile != "/missing-token" || c.BrokerCAFile != "/missing-ca" {
		t.Fatal("broker flags")
	}
	if _, e := c.DatabaseURI(context.Background()); e == nil {
		t.Fatal("unreadable token must fail startup")
	}
}
