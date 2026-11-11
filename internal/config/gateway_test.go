package config

import "testing"

func TestAccessGatewayURL(t *testing.T) {
	for _, raw := range []string{"https://auth.example", "http://access-gateway:8080", "https://auth.example/path", "https://user@auth.example", "https://auth.example?", "file:///tmp/keys"} {
		cfg, err := Load([]string{"--access-gateway-url", raw}, func(key string) string {
			switch key {
			case "ORIGIN":
				return "https://app.example"
			case "POSTGRES_URI":
				return "postgres://u:p@localhost/test"
			}
			return ""
		})
		valid := raw == "https://auth.example" || raw == "http://access-gateway:8080"
		if (err == nil) != valid || valid && cfg.AccessGatewayURL != raw {
			t.Fatalf("URL %q: %v", raw, err)
		}
	}
}
