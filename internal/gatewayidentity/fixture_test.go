package gatewayidentity

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// The fixture is emitted and checked by access-gateway's real signing code.
func TestGatewayWireFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/identity-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Now    time.Time `json:"now"`
		KeySet keySet    `json:"key_set"`
		Token  string    `json:"token"`
	}
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(fixture.KeySet) }))
	defer gateway.Close()
	v := &Verifier{endpoint: gateway.URL, audience: "https://app.example", client: gateway.Client(), now: func() time.Time { return fixture.Now }}
	if err := v.refresh(t.Context(), fixture.Now); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "https://app.example/static/app.css?check=hello%20world", nil)
	r.Header.Set(Header, fixture.Token)
	r.Header.Set("X-Auth-Request-User", "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa")
	r.Header.Set("X-Auth-Request-Name", "alice")
	if err := v.Verify(r, false); err != nil {
		t.Fatal("gateway assertion incompatible:", err)
	}
}
