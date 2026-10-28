package identity

import (
	"net/http/httptest"
	"testing"
)

func TestProxyIdentity(t *testing.T) {
	for _, headers := range [][]string{nil, {"not-a-uuid"}, {"00000000-0000-0000-0000-000000000000"}, {"aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb"}} {
		r := httptest.NewRequest("GET", "/", nil)
		for _, v := range headers {
			r.Header.Add("X-Auth-Request-User", v)
		}
		if _, e := FromRequest(r); e == nil {
			t.Fatalf("accepted %v", headers)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Auth-Request-User", "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa")
	r.Header.Set("X-Auth-Request-Name", "Alice <admin>")
	u, e := FromRequest(r)
	if e != nil || u.Name != "Alice <admin>" {
		t.Fatal(e)
	}
}
