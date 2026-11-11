package devs3

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestConditionalWrites(t *testing.T) {
	h, _, err := New("test", nil)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, body, match, none string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://s3/test/note.md", strings.NewReader(body))
		r.Header.Set("Content-Length", strconv.Itoa(len(body)))
		if match != "" {
			r.Header.Set("If-Match", match)
		}
		if none != "" {
			r.Header.Set("If-None-Match", none)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := request("PUT", "first", "", "*")
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	if w := request("PUT", "lost", "", "*"); w.Code != 412 {
		t.Fatal(w.Code)
	}
	if w := request("PUT", "second", etag, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodGet} {
		if w := request(method, "stale", etag, ""); w.Code != 412 {
			t.Fatal(method, w.Code)
		}
	}
	w := request("GET", "", "", "")
	if w.Code != 200 || w.Body.String() != "second" {
		t.Fatal(w.Code, w.Body.String())
	}
}
