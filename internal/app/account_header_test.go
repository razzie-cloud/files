package app

import (
	"context"
	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/identity"
	"github.com/razzie-cloud/files/internal/store"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAccountHeaderIdentityAndDestination(t *testing.T) {
	const origin = "https://files.example"
	gateway, sign := sharedGateway(t, origin)
	signed, err := New(config.Config{Origin: origin, AccessGatewayURL: gateway}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := New(config.Config{LandingOrigin: "http://localhost:8082"}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		signed, admin bool
	}{{"signed-admin", true, true}, {"signed-member", true, false}, {"forged-legacy-admin", false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			s := legacy
			if tc.signed {
				s = signed
			}
			r := httptest.NewRequest("GET", "https://untrusted.example/", nil)
			r.Header.Set("X-Auth-Request-Name", "<member>")
			r.Header.Set("X-Auth-Request-Admin", "true")
			if tc.signed {
				sign(r, "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa", tc.admin)
				if err := s.identityVerifier.Verify(r, false); err != nil {
					t.Fatal(err)
				}
			}
			r = r.WithContext(store.WithAdmin(r.Context(), s.identityVerifier.IsAdmin(r)))
			r = r.WithContext(context.WithValue(r.Context(), userKey{}, identity.User{Name: r.Header.Get("X-Auth-Request-Name")}))
			w := httptest.NewRecorder()
			s.render(w, r, view{Page: "home"})
			header := strings.Split(w.Body.String(), "</header>")[0]
			want := "http://auth.localhost:8082/"
			if tc.signed {
				want = "https://auth.example/"
			}
			if !strings.Contains(header, `href="`+want+`"`) {
				t.Fatal("wrong account destination", header)
			}
			if strings.Contains(header, ">Administrator<") != tc.admin {
				t.Fatal("role badge must use signed admin", header)
			}
			if !strings.Contains(header, `aria-label="Signed-in account"`) {
				t.Fatal("missing account identity")
			}
			if strings.Contains(header, "<member>") {
				t.Fatal("unescaped username")
			}
			if !tc.signed && !strings.Contains(header, "&lt;member&gt;") {
				t.Fatal("missing escaped username")
			}
		})
	}
}
