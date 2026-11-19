package app

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/store"
)

type shareListRepo struct {
	store.Repository
	mu          sync.Mutex
	shares      []store.Share
	directories []store.Directory
	err         error
}

func (f *shareListRepo) Shares(_ context.Context, owner, directory string) ([]store.Share, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := []store.Share{}
	for _, v := range f.shares {
		if v.Owner == owner && (directory == "" || directory == v.DirectoryID) {
			out = append(out, v)
		}
	}
	return out, nil
}
func (f *shareListRepo) Directories(_ context.Context, owner string) ([]store.Directory, error) {
	out := []store.Directory{}
	for _, d := range f.directories {
		if d.Owner == owner {
			out = append(out, d)
		}
	}
	return out, nil
}
func (f *shareListRepo) Directory(_ context.Context, owner, id string) (store.Directory, error) {
	for _, d := range f.directories {
		if d.Owner == owner && d.ID == id {
			return d, nil
		}
	}
	return store.Directory{}, store.ErrNotFound
}
func (f *shareListRepo) RemoveShare(_ context.Context, owner, directory, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	for i, v := range f.shares {
		if v.Owner == owner && v.DirectoryID == directory && v.ID == id {
			f.shares = append(f.shares[:i], f.shares[i+1:]...)
			return nil
		}
	}
	return store.ErrNotFound
}
func shareListFixture() *shareListRepo {
	past := time.Now().Add(-time.Hour)
	return &shareListRepo{
		directories: []store.Directory{{ID: testOwner, Owner: testOwner, Name: "Workspace <private>"}, {ID: "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb", Owner: testOwner, Name: "Photo archive"}},
		shares: []store.Share{
			{ID: strings.Repeat("a", 64), Owner: testOwner, DirectoryID: testOwner, Path: "Projects/<brand>", IsDirectory: true, PasswordHash: "never-render-password-hash", SessionKey: []byte("never-render-session-key")},
			{ID: strings.Repeat("b", 64), Owner: testOwner, DirectoryID: "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb", Path: "report.pdf", ExpiresAt: &past},
			{ID: strings.Repeat("c", 64), Owner: "cccccccc-cccc-4ccc-cccc-cccccccccccc", DirectoryID: testOwner, Path: "other-owner-private-file"},
		},
	}
}
func shareListRequest(s *Server, method, target, owner string, values url.Values, fragment bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(values.Encode()))
	if owner != "" {
		r.Header.Set("X-Auth-Request-User", owner)
	}
	r.Header.Set("Origin", s.Config.Origin)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if fragment {
		r.Header.Set("HX-Request", "true")
		r.Header.Set("HX-Target", "main")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestShareListAndRevoke(t *testing.T) {
	repo := shareListFixture()
	s, err := New(config.Config{Origin: "https://files.test", ShareOrigin: "https://shared.test"}, repo, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []bool{false, true} {
		w := shareListRequest(s, "GET", "https://files.test/shares", testOwner, nil, fragment)
		body := w.Body.String()
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Result().Header.Get("Referrer-Policy") != "same-origin" {
			t.Fatal(w.Code, w.Header())
		}
		for _, want := range []string{"<h1>Shares</h1>", "Workspace &lt;private&gt;", "Photo archive", "Projects/&lt;brand&gt;", "Password required", ">Expired</span>", ">Active</span>", `name="return_to" value="/shares"`, `href="/shares" aria-current="page"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("missing %s: %s", want, body)
			}
		}
		for _, secret := range []string{"never-render-password-hash", "never-render-session-key", "other-owner-private-file"} {
			if strings.Contains(body, secret) {
				t.Fatal("private metadata rendered", secret)
			}
		}
		if strings.Contains(body, "<!doctype html>") == fragment {
			t.Fatal("incorrect page/fragment shell")
		}
		if strings.Count(body, ">Open share</a>") != 1 {
			t.Fatal("expired link should not offer Open share")
		}
	}
	// A second owner gets neither names nor links from the first owner.
	w := shareListRequest(s, "GET", "https://files.test/shares", "dddddddd-dddd-4ddd-dddd-dddddddddddd", nil, false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "No shares yet") || strings.Contains(w.Body.String(), "Photo archive") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, owner := range []string{"", "dddddddd-dddd-4ddd-dddd-dddddddddddd"} {
		w = shareListRequest(s, "POST", "https://files.test/directories/"+testOwner+"/shares/"+strings.Repeat("a", 64)+"/remove", owner, url.Values{"return_to": {"/shares"}}, false)
		if w.Code != 401 && w.Code != 404 {
			t.Fatal("unauthorized revoke", w.Code)
		}
	}
	for i, fragment := range []bool{false, true} {
		id := strings.Repeat(string(rune('a'+i)), 64)
		directory := repo.directories[i].ID
		w = shareListRequest(s, "POST", "https://files.test/directories/"+directory+"/shares/"+id+"/remove", testOwner, url.Values{"return_to": {"/shares"}}, fragment)
		if fragment {
			if w.Code != 200 || !strings.Contains(w.Header().Get("HX-Location"), `"path":"/shares"`) {
				t.Fatal(w.Code, w.Header())
			}
		} else if w.Code != 303 || w.Header().Get("Location") != "/shares" {
			t.Fatal(w.Code, w.Header())
		}
	}
	w = shareListRequest(s, "GET", "https://files.test/shares", testOwner, nil, false)
	if !strings.Contains(w.Body.String(), "No shares yet") {
		t.Fatal(w.Body.String())
	}
	repo.err = errors.New("private backend error")
	w = shareListRequest(s, "GET", "https://files.test/shares", testOwner, nil, true)
	if w.Code != 500 || !strings.Contains(w.Body.String(), `aria-label="Primary"`) || strings.Contains(w.Body.String(), repo.err.Error()) {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestShareListAvailability(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := config.Config{Origin: "https://files.test"}
		if enabled {
			cfg.ShareOrigin = "https://shared.test"
		}
		s, err := New(cfg, shareListFixture(), nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		w := shareListRequest(s, "GET", "https://files.test/shares", testOwner, nil, false)
		want := 404
		if enabled {
			want = 200
		}
		if w.Code != want {
			t.Fatal(w.Code)
		}
		w = shareListRequest(s, "GET", "https://files.test/", testOwner, nil, false)
		if strings.Contains(w.Body.String(), `href="/shares"`) != enabled {
			t.Fatal("sidebar availability")
		}
		if enabled {
			if w := shareListRequest(s, "GET", "https://files.test/shares", "", nil, false); w.Code != 401 {
				t.Fatal(w.Code)
			}
			if w := shareListRequest(s, "GET", "https://shared.test/shares", testOwner, nil, false); w.Code != 404 {
				t.Fatal(w.Code)
			}
		}
	}
}
