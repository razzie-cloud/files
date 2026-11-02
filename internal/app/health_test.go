package app

import (
	"net/http/httptest"
	"testing"
)

func TestHealthProbesBypassIdentityAndStorage(t *testing.T) {
	// No repository, identity, or storage: any request-time I/O would panic.
	h := (&Server{}).Handler()
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "/healthz", 200}, {"HEAD", "/healthz", 200}, {"GET", "/readyz", 503}, {"POST", "/readyz", 405}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d", tc.method, tc.path, w.Code)
		}
	}
}
