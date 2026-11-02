package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/secrets"
	"github.com/razzie-cloud/files/internal/storage"
	"github.com/razzie-cloud/files/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func TestSharedPathScope(t *testing.T) {
	for _, root := range []string{"", "photos", "photos/hello + %.txt"} {
		for _, relative := range []string{"..", "../private", "/private", "a/../b", "a//b", `a\b`, "a/", "\x00"} {
			if _, err := sharedPath(store.Share{Path: root, IsDirectory: true}, relative); err == nil {
				t.Fatalf("accepted %q under %q", relative, root)
			}
		}
	}
	file := store.Share{Path: "photos/file.txt"}
	if _, err := sharedPath(file, "other.txt"); err == nil {
		t.Fatal("file scope expanded")
	}
	if got, err := sharedPath(file, ""); err != nil || got != file.Path {
		t.Fatalf("file: %q %v", got, err)
	}
	if got, err := sharedPath(store.Share{Path: "photos", IsDirectory: true}, "nested/file"); err != nil || got != "photos/nested/file" {
		t.Fatalf("directory: %q %v", got, err)
	}
}

func TestShareSessionValidation(t *testing.T) {
	v := store.Share{ID: strings.Repeat("a", 64), SessionKey: bytes.Repeat([]byte{1}, 32)}
	now := time.Now()
	expiry := strconv.FormatInt(now.Add(time.Hour).Unix(), 10)
	value := expiry + "." + shareMAC(v, expiry)
	check := func(value string, share store.Share, at time.Time) bool {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: shareCookieName, Value: value})
		return validShareSession(r, share, at)
	}
	if !check(value, v, now) {
		t.Fatal("valid session rejected")
	}
	for _, value := range []string{value + "x", expiry + ".bad", "bad.signature", "", strings.Repeat("x", 200)} {
		if check(value, v, now) {
			t.Fatal("forged cookie accepted")
		}
	}
	if check(value, v, now.Add(2*time.Hour)) {
		t.Fatal("expired cookie accepted")
	}
	other := v
	other.ID = strings.Repeat("b", 64)
	if check(value, other, now) {
		t.Fatal("cookie replayed on another share")
	}
	other = v
	other.SessionKey = bytes.Repeat([]byte{2}, 32)
	if check(value, other, now) {
		t.Fatal("rotated session key accepted")
	}
}

type shareFS struct {
	storage.FS
	keys []string
}

func (f *shareFS) Close() {}
func (f *shareFS) List(_ context.Context, key string) ([]storage.Entry, error) {
	f.keys = append(f.keys, key)
	return []storage.Entry{{Name: "hello + %.txt", Path: "untrusted/other", Size: 7}, {Name: "nested", Directory: true}}, nil
}
func (f *shareFS) Stat(_ context.Context, key string) (storage.Entry, error) {
	f.keys = append(f.keys, key)
	return storage.Entry{Size: 7}, nil
}
func (f *shareFS) Open(_ context.Context, key string, start, end int64) (storage.Object, error) {
	f.keys = append(f.keys, key)
	value := "content"
	if start >= 0 {
		value = value[start : end+1]
	}
	return storage.Object{ReadCloser: io.NopCloser(strings.NewReader(value)), Size: 7}, nil
}

type shareFactory struct {
	fs    *shareFS
	calls int
}

func (f *shareFactory) Open(context.Context, store.Connection, store.Directory, secrets.Value, secrets.Value) (storage.FS, error) {
	f.calls++
	return f.fs, nil
}

func TestPublicSharingIsolationAndPassword(t *testing.T) {
	cfg := config.Config{Origin: "https://files.example.com", ShareOrigin: "https://shared-files.example.com"}
	fs := &shareFS{}
	factory := &shareFactory{fs: fs}
	srv, err := New(cfg, nil, nil, factory, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 64)
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret password"), bcrypt.MinCost)
	v := store.Share{ID: id, Path: "photos", IsDirectory: true, PasswordHash: string(hash), SessionKey: bytes.Repeat([]byte{1}, 32)}
	srv.shares.entries = map[string]shareCacheEntry{id: {value: shareSnapshot{Share: v}, until: time.Now().Add(time.Hour)}}
	h := srv.Handler()
	request := func(method, target, origin, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		// A forged identity must never expose management on the public host.
		r.Header.Set("X-Auth-Request-User", testOwner)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	base := cfg.ShareOrigin + "/s/" + id
	for _, target := range []string{"/", "/connections", "/directories/" + testOwner} {
		if w := request("GET", cfg.ShareOrigin+target, "", "", nil); w.Code != 404 {
			t.Fatalf("management exposed: %s %d", target, w.Code)
		}
	}
	if w := request("GET", cfg.Origin+"/s/"+id, "", "", nil); w.Code != 404 {
		t.Fatal("share served on management host")
	}
	for _, suffix := range []string{"", "/download?path=file"} {
		w := request("GET", base+suffix, "", "", nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "requires a password") || strings.Contains(w.Body.String(), "/connections") {
			t.Fatal("password gate missing", w.Code, w.Body.String())
		}
	}
	if factory.calls != 0 {
		t.Fatal("storage opened before password")
	}
	if w := request("POST", base+"/unlock", cfg.Origin, "password=secret+password", nil); w.Code != 403 {
		t.Fatal("cross-origin unlock accepted")
	}
	if w := request("POST", base+"/unlock", cfg.ShareOrigin, "password=wrong", nil); w.Code != 403 || len(w.Result().Cookies()) != 0 {
		t.Fatal("wrong password accepted")
	}
	w := request("POST", base+"/unlock", cfg.ShareOrigin, "password=secret+password", nil)
	if w.Code != 303 || len(w.Result().Cookies()) != 1 {
		t.Fatal("unlock failed", w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/s/"+id || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Fatalf("unsafe session cookie: %+v", cookie)
	}
	// HTMX submits same-origin XHRs under no-referrer and follows HX-Location
	// after accepting the cookie; native navigation POSTs can send Origin: null.
	unlock := httptest.NewRequest("POST", base+"/unlock", strings.NewReader("password=secret+password"))
	unlock.Header.Set("Origin", cfg.ShareOrigin)
	unlock.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	unlock.Header.Set("HX-Request", "true")
	unlocked := httptest.NewRecorder()
	h.ServeHTTP(unlocked, unlock)
	if unlocked.Code != 200 || !strings.Contains(unlocked.Header().Get("HX-Location"), "/s/"+id) || len(unlocked.Result().Cookies()) != 1 {
		t.Fatal("HTMX unlock contract", unlocked.Code, unlocked.Header())
	}
	w = request("GET", base, "", "", cookie)
	if w.Code != 200 || strings.Contains(w.Body.String(), "requires a password") {
		t.Fatal("session not accepted", w.Code, w.Body.String())
	}
	var download *url.URL
	for _, u := range pageURLs(t, w.Body.String(), "a", "href") {
		if strings.HasSuffix(u.Path, "/download") {
			download = u
		}
	}
	if download == nil || download.Query().Get("path") != "hello + %.txt" {
		t.Fatal("shared path escaped incorrectly", download)
	}
	w = request("GET", cfg.ShareOrigin+download.String(), "", "", cookie)
	if w.Code != 200 || w.Body.String() != "content" || fs.keys[len(fs.keys)-1] != "photos/hello + %.txt" {
		t.Fatal("bad scoped download", w.Code, w.Body.String(), fs.keys)
	}
	w = request("HEAD", cfg.ShareOrigin+download.String(), "", "", cookie)
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "7" {
		t.Fatal("HEAD failed")
	}
	for _, relative := range []string{"../other", "/other", "nested/../../other"} {
		before := factory.calls
		w := request("GET", base+"/download?path="+url.QueryEscape(relative), "", "", cookie)
		if w.Code != 422 || factory.calls != before {
			t.Fatal("traversal reached storage", relative, w.Code)
		}
	}
	for _, action := range []string{"upload", "mkdir", "delete-file", "rmdir"} {
		if w := request("POST", base+"/"+action, cfg.ShareOrigin, "", cookie); w.Code != 404 {
			t.Fatal("public write route exposed", action, w.Code)
		}
	}
}

func TestShareCacheLifetimeBoundsAndErrors(t *testing.T) {
	var c shareCache
	calls := 0
	load := func(_ context.Context, id string) (shareSnapshot, error) {
		calls++
		return shareSnapshot{Share: store.Share{ID: id}}, nil
	}
	ctx := context.Background()
	for range 3 {
		if _, err := c.get(ctx, "one", load); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("refresh queried backend", calls)
	}
	c.mu.Lock()
	entry := c.entries["one"]
	entry.until = time.Now().Add(-time.Second)
	c.entries["one"] = entry
	c.mu.Unlock()
	c.get(ctx, "one", load)
	if calls != 2 {
		t.Fatal("expired cache did not reload")
	}
	c.invalidate()
	c.get(ctx, "one", load)
	if calls != 3 {
		t.Fatal("invalidation ignored")
	}
	for i := range shareCacheLimit + 10 {
		c.get(ctx, strconv.Itoa(i), load)
	}
	if len(c.entries) != shareCacheLimit {
		t.Fatal("cache is not bounded", len(c.entries))
	}
	calls = 0
	missing := func(context.Context, string) (shareSnapshot, error) {
		calls++
		return shareSnapshot{}, store.ErrNotFound
	}
	for range 2 {
		_, err := c.get(ctx, "missing", missing)
		if !errors.Is(err, store.ErrNotFound) {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("negative cache missed")
	}
	calls = 0
	failed := func(context.Context, string) (shareSnapshot, error) {
		calls++
		return shareSnapshot{}, errors.New("offline")
	}
	for range 2 {
		if _, err := c.get(ctx, "failed", failed); err == nil {
			t.Fatal("failed load accepted")
		}
	}
	if calls != 2 {
		t.Fatal("backend error cached")
	}
}

func TestShareCacheCoalescingAndInvalidationDuringLoad(t *testing.T) {
	var c shareCache
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	load := func(context.Context, string) (shareSnapshot, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return shareSnapshot{}, nil
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { c.get(context.Background(), "one", load) })
	}
	<-started
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("concurrent misses not coalesced", calls.Load())
	}
	c.invalidate()
	started, release = make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	go func() {
		c.get(context.Background(), "stale", func(context.Context, string) (shareSnapshot, error) {
			close(started)
			<-release
			return shareSnapshot{}, nil
		})
		close(done)
	}()
	<-started
	c.invalidate()
	close(release)
	<-done
	if len(c.entries) != 0 {
		t.Fatal("in-flight load repopulated invalidated cache")
	}
}

func TestShareExpiration(t *testing.T) {
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		value   string
		minutes int
		valid   bool
	}{
		{"", 0, true}, {"1", 1, true}, {"60", 60, true}, {"43200", 43200, true},
		{"0", 0, false}, {"-1", 0, false}, {"1.5", 0, false}, {"tomorrow", 0, false},
		{"153722868", 0, false}, {"9223372036854775807", 0, false}, {"999999999999999999999", 0, false},
	} {
		t.Run(test.value, func(t *testing.T) {
			expires, err := shareExpiration(test.value, now)
			if (err == nil) != test.valid {
				t.Fatalf("expiration %q: %v", test.value, err)
			}
			if !test.valid {
				return
			}
			if test.value == "" {
				if expires != nil {
					t.Fatal("blank expiry is not permanent")
				}
				return
			}
			if expires == nil || !expires.Equal(now.Add(time.Duration(test.minutes)*time.Minute)) {
				t.Fatal("incorrect deadline", expires)
			}
		})
	}
	v := store.Share{ID: strings.Repeat("a", 64), SessionKey: bytes.Repeat([]byte{1}, 32)}
	if v.Expired(now) {
		t.Fatal("permanent share expired")
	}
	expires := now.Add(time.Minute)
	v.ExpiresAt = &expires
	if v.Expired(now) || !v.Expired(expires) || !v.Expired(expires.Add(time.Nanosecond)) {
		t.Fatal("incorrect expiration boundary")
	}
	// Even a correctly signed cookie with time remaining cannot outlive a share.
	cookieEnd := strconv.FormatInt(now.Add(time.Hour).Unix(), 10)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: shareCookieName, Value: cookieEnd + "." + shareMAC(v, cookieEnd)})
	if !validShareSession(req, v, now) || validShareSession(req, v, expires) {
		t.Fatal("session bypassed share expiration")
	}
}

func TestPublicShareExpirationBypassesNeitherCacheNorPassword(t *testing.T) {
	for _, directory := range []bool{false, true} {
		for _, protected := range []bool{false, true} {
			t.Run(strconv.FormatBool(directory)+"/"+strconv.FormatBool(protected), func(t *testing.T) {
				cfg := config.Config{Origin: "https://files.example.com", ShareOrigin: "https://shared-files.example.com"}
				factory := &shareFactory{fs: &shareFS{}}
				// A nil repository ensures the warm cache never needs DB I/O.
				srv, err := New(cfg, nil, nil, factory, nil)
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now()
				expires := now.Add(time.Minute)
				share := store.Share{ID: strings.Repeat("c", 64), Path: "folder/file", IsDirectory: directory, ExpiresAt: &expires, SessionKey: bytes.Repeat([]byte{1}, 32)}
				if protected {
					hash, err := bcrypt.GenerateFromPassword([]byte("password"), bcrypt.MinCost)
					if err != nil {
						t.Fatal(err)
					}
					share.PasswordHash = string(hash)
				}
				cookieEnd := strconv.FormatInt(now.Add(time.Hour).Unix(), 10)
				cookie := &http.Cookie{Name: shareCookieName, Value: cookieEnd + "." + shareMAC(share, cookieEnd)}
				srv.shares.entries = map[string]shareCacheEntry{share.ID: {value: shareSnapshot{Share: share}, until: now.Add(shareCacheTTL)}}
				base := cfg.ShareOrigin + "/s/" + share.ID
				h := srv.Handler()
				request := func(method, suffix string, cookie *http.Cookie) *httptest.ResponseRecorder {
					req := httptest.NewRequest(method, base+suffix, strings.NewReader("password=password"))
					req.Header.Set("Origin", cfg.ShareOrigin)
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					if cookie != nil {
						req.AddCookie(cookie)
					}
					w := httptest.NewRecorder()
					h.ServeHTTP(w, req)
					return w
				}
				if w := request("GET", "", cookie); w.Code != 200 {
					t.Fatal("unexpired share unavailable", w.Code, w.Body.String())
				}
				if protected {
					w := request("POST", "/unlock", nil)
					if w.Code != 303 || len(w.Result().Cookies()) != 1 {
						t.Fatal("unlock failed", w.Code)
					}
					end, err := strconv.ParseInt(strings.Split(w.Result().Cookies()[0].Value, ".")[0], 10, 64)
					if err != nil || end != expires.Unix() {
						t.Fatal("session exceeds share lifetime", end, expires, err)
					}
				}
				// Keep the metadata cache valid while advancing the share's deadline.
				past := now.Add(-time.Second)
				srv.shares.mu.Lock()
				entry := srv.shares.entries[share.ID]
				entry.value.Share.ExpiresAt = &past
				srv.shares.entries[share.ID] = entry
				srv.shares.mu.Unlock()
				before := factory.calls
				for _, route := range []struct{ method, path string }{{"GET", ""}, {"GET", "/download"}, {"HEAD", "/download"}, {"POST", "/unlock"}} {
					for _, cookie := range []*http.Cookie{nil, cookie} {
						w := request(route.method, route.path, cookie)
						if w.Code != 404 || len(w.Result().Cookies()) != 0 {
							t.Fatal("expired share accepted", route, w.Code, w.Header())
						}
					}
				}
				if factory.calls != before {
					t.Fatal("expired share reached storage")
				}
			})
		}
	}
}
