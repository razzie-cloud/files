package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/store"
	"github.com/razzie-cloud/files/tests/browser"
	"golang.org/x/crypto/bcrypt"
)

// Native submissions must get their Origin from Chromium, including retries
// from the wrong-password response. No fixture supplies an Origin header.
func TestNativeFormsBrowser(t *testing.T) {
	if os.Getenv("CHROMIUM_EXECUTABLE_PATH") == "" {
		t.Skip("set CHROMIUM_EXECUTABLE_PATH for browser acceptance")
	}
	repo := shareListFixture()
	cfg := config.Config{Origin: "https://files.test", ShareOrigin: "https://shared.test"}
	s, err := New(cfg, repo, nil, &shareFactory{fs: &shareFS{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("d", 64)
	hash, err := bcrypt.GenerateFromPassword([]byte("secret password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	share := store.Share{ID: id, Path: "photos", IsDirectory: true, PasswordHash: string(hash), SessionKey: bytes.Repeat([]byte{1}, 32)}
	handler := s.Handler()
	type observed struct {
		request browser.Request
		status  int
		header  http.Header
		body    string
	}
	posts, external := make(chan observed, 4), make(chan browser.Request, 1)
	gets := make(chan browser.Request, 4)
	base := cfg.ShareOrigin + "/s/" + id
	b := browser.Start(func(request browser.Request) browser.Response {
		if strings.HasPrefix(request.URL, "https://external.test/") {
			external <- request
			return browser.Response{Type: "text/html", Body: []byte("External destination")}
		}
		// Model HTMX being unavailable; the native forms and real handler stay intact.
		if strings.Contains(request.URL, "/static/htmx") {
			return browser.Response{Type: "text/javascript"}
		}
		r := httptest.NewRequest(request.Method, request.URL, strings.NewReader(request.PostData))
		for k, v := range request.Headers {
			r.Header.Set(k, v)
		}
		r.Header.Set("X-Auth-Request-User", testOwner)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		response := w.Result()
		headers := map[string]string{}
		for k, v := range response.Header {
			headers[k] = strings.Join(v, ", ")
		}
		if request.Method == "GET" && (request.URL == cfg.Origin+"/shares" || request.URL == base) {
			gets <- request
		}
		if request.Method == "POST" {
			posts <- observed{request, w.Code, response.Header, w.Body.String()}
		}
		return browser.Response{Status: w.Code, Type: response.Header.Get("Content-Type"), Headers: headers, Body: w.Body.Bytes()}
	})
	defer b.Close()
	nextPost := func(origin string, status int) observed {
		t.Helper()
		select {
		case got := <-posts:
			h := http.Header{}
			for k, v := range got.request.Headers {
				h.Set(k, v)
			}
			if h.Get("Origin") != origin || h.Get("HX-Request") != "" || got.status != status {
				t.Fatalf("native POST: Origin=%q HX-Request=%q status=%d, want %q/%d", h.Get("Origin"), h.Get("HX-Request"), got.status, origin, status)
			}
			return got
		case <-time.After(5 * time.Second):
			t.Fatal("native POST not observed")
			return observed{}
		}
	}
	nextGet := func(target string) browser.Request {
		t.Helper()
		select {
		case got := <-gets:
			if got.URL != target {
				t.Fatal("unexpected native GET", got.URL, target)
			}
			return got
		case <-time.After(5 * time.Second):
			t.Fatal("native GET not observed", target)
			return browser.Request{}
		}
	}
	loader := func() string {
		var tree struct {
			FrameTree struct {
				Frame struct {
					LoaderID string `json:"loaderId"`
				} `json:"frame"`
			} `json:"frameTree"`
		}
		if err := json.Unmarshal(b.Call("Page.getFrameTree", nil), &tree); err != nil {
			t.Fatal(err)
		}
		return tree.FrameTree.Frame.LoaderID
	}
	waitDocument := func(previous, target string) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
			if loader() != previous && string(b.Eval(`location.href===`+browser.Quote(target)+`&&document.readyState==='complete'`)) == "true" {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("native navigation did not commit", target)
	}
	b.Navigate(cfg.Origin + "/shares")
	nextGet(cfg.Origin + "/shares")
	previous := loader()
	b.Eval(`setTimeout(()=>document.querySelector('.share-card form').requestSubmit(),0)`)
	got := nextPost(cfg.Origin, 303)
	if got.header.Get("Location") != "/shares" {
		t.Fatal("native revoke redirect", got.header)
	}
	nextGet(cfg.Origin + "/shares")
	waitDocument(previous, cfg.Origin+"/shares")
	remaining, err := repo.Shares(t.Context(), testOwner, "")
	if err != nil || len(remaining) != 1 || remaining[0].ID != strings.Repeat("b", 64) {
		t.Fatal("selected share was not revoked", remaining, err)
	}
	b.Run(`assert(document.querySelectorAll('.share-card').length===1,'native revoke result')`)
	s.shares.entries = map[string]shareCacheEntry{id: {value: shareSnapshot{Share: share}, until: time.Now().Add(time.Hour)}}
	b.Navigate(base)
	nextGet(base)
	for _, attempt := range []struct {
		password string
		status   int
	}{{"wrong", 403}, {"secret password", 303}} {
		previous = loader()
		b.Eval(`document.querySelector('input[name="password"]').value=` + browser.Quote(attempt.password) + `;setTimeout(()=>document.querySelector('form').requestSubmit(),0)`)
		got = nextPost(cfg.ShareOrigin, attempt.status)
		target := base
		if attempt.status == 403 {
			target += "/unlock"
			if got.header.Get("Referrer-Policy") != "same-origin" || len(got.header.Values("Set-Cookie")) != 0 || !strings.Contains(got.body, "requires a password") {
				t.Fatal("wrong-password retry response", got.header, got.body)
			}
		} else {
			cookies := (&http.Response{Header: got.header}).Cookies()
			follow := nextGet(base)
			followHeaders := http.Header{}
			for k, v := range follow.Headers {
				followHeaders.Set(k, v)
			}
			if !strings.Contains(followHeaders.Get("Cookie"), shareCookieName+"=") {
				t.Fatal("native redirect omitted share cookie")
			}
			if len(cookies) != 1 || cookies[0].Domain != "" || cookies[0].Path != "/s/"+id || !cookies[0].HttpOnly || !cookies[0].Secure || got.header.Get("Location") != "/s/"+id {
				t.Fatal("native unlock cookie/redirect", got.header)
			}
		}
		waitDocument(previous, target)
	}
	b.Run(`assert(!document.body.textContent.includes('requires a password'),'native unlock failed');assert(!document.querySelector('a[href="/connections"]'),'management link exposed')`)
	// The real capability document must not disclose its URL to another origin.
	b.Eval(`const a=document.createElement('a');a.href='https://external.test/destination';document.body.append(a);setTimeout(()=>a.click(),0)`)
	select {
	case request := <-external:
		for k, v := range request.Headers {
			if strings.EqualFold(k, "Referer") && v != "" {
				t.Fatal("cross-origin capability Referer leaked", v)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("external navigation not observed")
	}
	// Even an unlocked public browser cannot enter management routes.
	b.Navigate(cfg.ShareOrigin + "/connections")
	b.Run(`assert(document.body.textContent.includes('404'),'public management route exposed')`)
	b.AssertNoErrors()
}
