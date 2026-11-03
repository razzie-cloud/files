package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/secrets"
	"github.com/razzie-cloud/files/internal/storage"
	rclonestorage "github.com/razzie-cloud/files/internal/storage/rclone"
	"github.com/razzie-cloud/files/internal/store"
	"github.com/razzie-cloud/files/internal/testutil"
)

type castFS struct {
	storage.FS
	files          map[string][]byte
	entries        []storage.Entry
	listError      error
	opens          int
	streamDeadline bool
}

func (f *castFS) Close() {}
func (f *castFS) List(context.Context, string) ([]storage.Entry, error) {
	return f.entries, f.listError
}
func (f *castFS) Stat(_ context.Context, key string) (storage.Entry, error) {
	b, ok := f.files[key]
	if !ok {
		return storage.Entry{}, storage.ErrNotFound
	}
	return storage.Entry{Size: int64(len(b))}, nil
}
func (f *castFS) Open(ctx context.Context, key string, start, end int64) (storage.Object, error) {
	b, ok := f.files[key]
	if !ok {
		return storage.Object{}, storage.ErrNotFound
	}
	f.opens++
	if start == 2 || start == -1 {
		_, bounded := ctx.Deadline()
		f.streamDeadline = f.streamDeadline || bounded
	}
	size := int64(len(b))
	if start >= 0 {
		b = b[start:min(int64(len(b)), end+1)]
	}
	return storage.Object{ReadCloser: io.NopCloser(bytes.NewReader(b)), Size: size}, nil
}
func castPictures(t *testing.T) ([]byte, []byte) {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 20, 40))
	var j, p bytes.Buffer
	if err := jpeg.Encode(&j, im, nil); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&p, im); err != nil {
		t.Fatal(err)
	}
	return j.Bytes(), p.Bytes()
}
func castMP4() []byte {
	return append([]byte{0, 0, 0, 24}, []byte("ftypisom\x00\x00\x00\x00isommp42\x00\x00\x00\x0fmdat-sample")...)
}

func TestPlaybackTicketBoundaries(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	expiry := now.Add(time.Hour)
	v := store.Share{ID: strings.Repeat("a", 64), Path: "photos", IsDirectory: true, SessionKey: bytes.Repeat([]byte{1}, 32), ExpiresAt: &expiry}
	token := signPlayback(v, "nested", "slideshow", expiry)
	decoded, err := verifyPlayback(v, token, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		p, m string
		ok   bool
	}{
		{"nested", "slideshow", true}, {"nested/pic.jpg", "slideshow", true},
		{"nested/pic.png", "slideshow", true}, {"nested/deep/pic.jpg", "slideshow", false},
		{"nested2/pic.jpg", "slideshow", false}, {"other.jpg", "slideshow", false},
		{"nested/pic.svg", "slideshow", false}, {"nested/movie.mp4", "video", false},
	} {
		if decoded.permits(tc.p, tc.m) != tc.ok {
			t.Fatal(tc)
		}
	}
	for _, bad := range []string{"", token + "x", strings.Replace(token, ".", "x.", 1), strings.Repeat("x", 4097), shareMAC(v, strconv.FormatInt(expiry.Unix(), 10))} {
		if _, err := verifyPlayback(v, bad, now); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
	for _, scope := range []string{"../outside", "/outside", "a//b"} {
		if _, err := verifyPlayback(v, signPlayback(v, scope, "slideshow", expiry), now); err == nil {
			t.Fatal("unsafe scope", scope)
		}
	}
	for _, other := range []store.Share{
		{ID: strings.Repeat("b", 64), SessionKey: v.SessionKey, IsDirectory: true},
		{ID: v.ID, SessionKey: bytes.Repeat([]byte{2}, 32), IsDirectory: true},
		{ID: v.ID, SessionKey: v.SessionKey, IsDirectory: false},
	} {
		if _, err := verifyPlayback(other, token, now); err == nil {
			t.Fatal("wrong parent accepted")
		}
	}
	if _, err := verifyPlayback(v, token, expiry); err == nil {
		t.Fatal("expired token accepted")
	}
	shorter := now.Add(time.Minute)
	v.ExpiresAt = &shorter
	if _, err := verifyPlayback(v, token, now); err == nil {
		t.Fatal("ticket exceeds parent expiry")
	}
	v.ExpiresAt = nil
	video := signPlayback(v, "movie.mp4", "video", expiry)
	vt, err := verifyPlayback(v, video, now)
	if err != nil || !vt.permits("movie.mp4", "video") || vt.permits("other.mp4", "video") || vt.permits("movie.mp4", "slideshow") {
		t.Fatal("video scope expanded")
	}
}

type castFactory struct{ fs storage.FS }

func (f castFactory) Open(context.Context, store.Connection, store.Directory, secrets.Value, secrets.Value) (storage.FS, error) {
	return f.fs, nil
}

func cachedCastServer(t *testing.T, f *castFS, protected bool) (*Server, store.Share) {
	t.Helper()
	cfg := config.Config{Origin: "https://files.example.com", ShareOrigin: "https://shared.example.com", MaxEntries: 1000}
	s, err := New(cfg, nil, nil, castFactory{fs: f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	v := store.Share{ID: strings.Repeat("a", 64), Owner: testOwner, Path: "photos", IsDirectory: true, SessionKey: bytes.Repeat([]byte{3}, 32)}
	if protected {
		v.PasswordHash = "protected"
	}
	s.shares.entries = map[string]shareCacheEntry{v.ID: {value: shareSnapshot{Share: v}, until: time.Now().Add(time.Hour)}}
	return s, v
}
func castRequest(s *Server, v store.Share, method, suffix, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, s.Config.ShareOrigin+"/s/"+v.ID+suffix, strings.NewReader(body))
	r.Header.Set("Origin", s.Config.ShareOrigin)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestPublicCastPasswordDelegationAndScope(t *testing.T) {
	j, p := castPictures(t)
	f := &castFS{files: map[string][]byte{"photos/nested/a.jpg": j, "photos/nested/b.png": p, "photos/nested/deep/c.jpg": j}, entries: []storage.Entry{{Name: "b.png", Path: "untrusted"}, {Name: "a.jpg"}, {Name: "../escape.jpg"}, {Name: "folder.jpg", Directory: true}, {Name: "active.svg"}}}
	s, v := cachedCastServer(t, f, true)
	for _, route := range []struct{ method, path, body string }{{"POST", "/cast", "mode=slideshow&path=nested"}, {"GET", "/media?path=nested/a.jpg", ""}, {"HEAD", "/media?path=nested/a.jpg", ""}, {"GET", "/slideshow?path=nested", ""}} {
		w := castRequest(s, v, route.method, route.path, route.body, nil)
		if w.Code != 403 || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Fatal("locked API", route, w.Code, w.Body.String())
		}
	}
	if f.opens != 0 {
		t.Fatal("locked request opened storage")
	}
	end := time.Now().Add(20 * time.Minute).Unix()
	raw := strconv.FormatInt(end, 10)
	cookie := &http.Cookie{Name: shareCookieName, Value: raw + "." + shareMAC(v, raw)}
	w := castRequest(s, v, "POST", "/cast", "mode=slideshow&path=nested", cookie)
	var out playback
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
		t.Fatal("delegation", w.Code, w.Body.String(), err)
	}
	if out.Expires.Unix() != end || out.Cleanup != "" || out.ShareID != "" || len(out.Entries) != 2 || out.Entries[0].Name != "a.jpg" {
		t.Fatal("bad description", out)
	}
	first, _ := url.Parse(out.Entries[0].URL)
	token := first.Query().Get("ticket")
	if token == "" || strings.Contains(w.Body.String(), "SessionKey") {
		t.Fatal("missing delegation")
	}
	// A fresh server/replica needs only the persisted snapshot and key.
	second, _ := cachedCastServer(t, f, true)
	suffix := "/media?" + first.RawQuery
	if got := castRequest(second, v, "GET", suffix, "", nil); got.Code != 200 || !bytes.Equal(got.Body.Bytes(), j) {
		t.Fatal("ticket failed across instances", got.Code)
	}
	for _, relative := range []string{"other.jpg", "nested/deep/c.jpg", "nested/movie.mp4", "../other.jpg", "nested2/a.jpg"} {
		got := castRequest(s, v, "GET", "/media?path="+url.QueryEscape(relative)+"&ticket="+url.QueryEscape(token), "", nil)
		if got.Code == 200 {
			t.Fatal("scope expanded", relative)
		}
	}
	if got := castRequest(s, v, "GET", "/slideshow?path=nested&ticket="+token, "", nil); got.Code != 200 {
		t.Fatal("ticket manifest", got.Code, got.Body.String())
	}
	if got := castRequest(s, v, "GET", "/slideshow?path=nested/deep&ticket="+token, "", nil); got.Code != 403 {
		t.Fatal("expanded manifest", got.Code)
	}
	expired := time.Now().Add(-time.Second)
	s.shares.entries[v.ID] = shareCacheEntry{value: shareSnapshot{Share: store.Share{ID: v.ID, ExpiresAt: &expired}}, until: time.Now().Add(time.Hour)}
	if got := castRequest(s, v, "GET", suffix, "", nil); got.Code != 404 {
		t.Fatal("cache bypassed expiry", got.Code)
	}
}

func TestCastMediaValidationRangesAndDeadlines(t *testing.T) {
	j, p := castPictures(t)
	f := &castFS{files: map[string][]byte{"photos/a.jpg": j, "photos/b.png": p, "photos/video.mp4": castMP4(), "photos/mismatch.jpg": p, "photos/active.png": []byte("<svg onload='alert(1)'/>"), "photos/malformed.mp4": []byte("not an mp4 container"), "photos/active.svg": j}}
	s, v := cachedCastServer(t, f, false)
	for _, name := range []string{"a.jpg", "b.png", "video.mp4"} {
		for _, method := range []string{"GET", "HEAD"} {
			for _, rangeValue := range []string{"", "bytes=2-5", "bytes=-4", "bytes=999999-", "bytes=0-1,4-5"} {
				r := httptest.NewRequest(method, s.Config.ShareOrigin+"/s/"+v.ID+"/media?path="+name, nil)
				r.Header.Set("Range", rangeValue)
				r.Header.Set("Origin", s.Config.Origin)
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				expected := 200
				if rangeValue != "" {
					expected = 206
				}
				if strings.Contains(rangeValue, "999999") || strings.Contains(rangeValue, ",") {
					expected = 416
				}
				if w.Code != expected {
					t.Fatalf("%s %s %s: %d %s", method, name, rangeValue, w.Code, w.Body.String())
				}
				if method == "HEAD" && w.Body.Len() != 0 {
					t.Fatal("HEAD body")
				}
				if expected != 416 && (w.Header().Get("Content-Type") != castType(name) || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline")) {
					t.Fatal("wrong media headers")
				}
				if method == "GET" && expected == 206 && w.Body.Len() != 4 {
					t.Fatal("wrong range bytes")
				}
				if w.Header().Get("Access-Control-Allow-Origin") != s.Config.Origin || w.Header().Get("Access-Control-Allow-Credentials") != "" {
					t.Fatal("CORS")
				}
			}
		}
	}
	for _, name := range []string{"mismatch.jpg", "active.png", "malformed.mp4", "active.svg"} {
		if w := castRequest(s, v, "HEAD", "/media?path="+name, "", nil); w.Code != 415 {
			t.Fatal("invalid media accepted", name, w.Code)
		}
	}
	if f.streamDeadline {
		t.Fatal("stream inherited page/auth deadline")
	}
	r := httptest.NewRequest("GET", s.Config.ShareOrigin+"/s/"+v.ID+"/media?path=a.jpg", nil)
	r.Header.Set("Origin", "https://untrusted.example")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("untrusted CORS")
	}
}
func TestSlideshowListsBeforePagination(t *testing.T) {
	for _, root := range []string{"", "nested"} {
		f := &castFS{}
		for i := range 125 {
			f.entries = append(f.entries, storage.Entry{Name: fmt.Sprintf("%03d.jpg", 124-i), Path: "evil/backend/path"})
		}
		s, v := cachedCastServer(t, f, false)
		w := castRequest(s, v, "GET", "/slideshow?path="+root, "", nil)
		var out playback
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || w.Code != 200 || len(out.Entries) != 125 || out.Entries[0].Name != "000.jpg" || out.Entries[124].Name != "124.jpg" {
			t.Fatal("list truncated or unsorted", w.Code)
		}
		expected := "000.jpg"
		if root != "" {
			expected = root + "/000.jpg"
		}
		if out.Entries[0].Path != expected {
			t.Fatal("invalid child path", out.Entries[0])
		}
		s.Config.MaxEntries = 100
		if w := castRequest(s, v, "GET", "/slideshow?path="+root, "", nil); w.Code != 413 {
			t.Fatal("listing limit ignored")
		}
		f.entries = nil
		if w := castRequest(s, v, "GET", "/slideshow?path="+root, "", nil); w.Code != 200 {
			t.Fatal("empty folder failed")
		}
	}
}

func TestOwnerCastIntegration(t *testing.T) {
	repo := &store.Store{DB: testutil.Database(t)}
	remote, _ := testutil.S3(t, "cast-test")
	defer remote.Close()
	endpoint, _ := url.Parse(remote.URL)
	policy, _ := rclonestorage.NewPolicy(endpoint.Host, true)
	proxy, err := rclonestorage.StartEgress(policy)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	secretStore := secrets.New(repo.DB)
	ctx := context.Background()
	ref, err := secretStore.Put(ctx, testOwner, secrets.Value{AccessKey: "test", SecretKey: "test-only"})
	if err != nil {
		t.Fatal(err)
	}
	c := store.Connection{Owner: testOwner, Name: "cast", Provider: "Other", Endpoint: remote.URL, Region: "us-east-1", PathStyle: true, AccessMode: "read_only", SecretRef: ref}
	if err := repo.SaveConnection(ctx, &c); err != nil {
		t.Fatal(err)
	}
	factory := &rclonestorage.Factory{ProxyURL: proxy.URL, Policy: policy, MaxEntries: 1000}
	cfg := config.Config{Origin: "https://files.example.com", ShareOrigin: "https://shared.example.com", CastShareTTL: time.Hour, MaxEntries: 1000}
	s, err := New(cfg, repo, secretStore, factory, policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, encryption := range []string{"none", "rclone_crypt"} {
		t.Run(encryption, func(t *testing.T) {
			keys := secrets.Value{Password: "test-only crypt password"}
			cryptRef, err := secretStore.Put(ctx, testOwner, keys)
			if err != nil {
				t.Fatal(err)
			}
			d := store.Directory{Owner: testOwner, ConnectionID: c.ID, Name: encryption, Bucket: "cast-test", Prefix: encryption, ReadOnly: true, EncryptionMode: encryption, CryptSecretRef: cryptRef, FilenameEncryption: "standard", DirectoryEncryption: true, FilenameEncoding: "base32", Suffix: ".bin"}
			if err := repo.SaveDirectory(ctx, &d); err != nil {
				t.Fatal(err)
			}
			fs, err := factory.Open(ctx, c, d, secrets.Value{AccessKey: "test", SecretKey: "test-only"}, keys)
			if err != nil {
				t.Fatal(err)
			}
			defer fs.Close()
			_, p := castPictures(t)
			if err := fs.Put(ctx, "photo + %.png", bytes.NewReader(p)); err != nil {
				t.Fatal(err)
			}
			if err := fs.Put(ctx, "movie.mp4", bytes.NewReader(castMP4())); err != nil {
				t.Fatal(err)
			}
			request := func(owner, body string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", cfg.Origin+"/directories/"+d.ID+"/cast", strings.NewReader(body))
				r.Header.Set("Origin", cfg.Origin)
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r.Header.Set("X-Auth-Request-User", owner)
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				return w
			}
			if w := request(uuid.NewString(), "mode=slideshow"); w.Code != 404 {
				t.Fatal("foreign directory accepted", w.Code)
			}
			for _, body := range []string{"mode=unknown", "mode=video&path=../movie.mp4", "mode=slideshow&path=movie.mp4", "mode=video&path=photo+%2B+%25.png"} {
				if w := request(testOwner, body); w.Code == 200 {
					t.Fatal("invalid cast accepted", body)
				}
			}
			for _, mode := range []string{"slideshow", "video"} {
				body := "mode=" + mode
				if mode == "video" {
					body += "&path=movie.mp4"
				}
				w := request(testOwner, body)
				var out playback
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
					t.Fatal("owner cast", w.Code, w.Body.String())
				}
				shares, err := repo.Shares(ctx, testOwner, d.ID)
				if err != nil || len(shares) != 1 {
					t.Fatal("temporary share missing", err, len(shares))
				}
				v := shares[0]
				if v.ExpiresAt == nil || time.Until(*v.ExpiresAt) > time.Hour || time.Until(*v.ExpiresAt) < 59*time.Minute || v.PasswordHash != "" || out.Cleanup == "" || out.ShareID != v.ID {
					t.Fatal("bad temporary share")
				}
				media := out.Media
				if mode == "slideshow" {
					media = &out.Entries[0]
				}
				r := httptest.NewRequest("GET", media.URL, nil)
				r.Header.Set("Range", "bytes=2-8")
				rec := httptest.NewRecorder()
				s.Handler().ServeHTTP(rec, r)
				if rec.Code != 206 || rec.Body.Len() != 7 {
					t.Fatal("plain/crypt Cast range", rec.Code, rec.Body.String())
				}
				revoke := httptest.NewRequest("POST", cfg.Origin+out.Cleanup, strings.NewReader(""))
				revoke.Header.Set("Origin", cfg.Origin)
				revoke.Header.Set("X-Auth-Request-User", testOwner)
				revoke.Header.Set("Accept", "application/json")
				rec = httptest.NewRecorder()
				s.Handler().ServeHTTP(rec, revoke)
				if rec.Code != 204 {
					t.Fatal("cleanup failed", rec.Code)
				}
				rec = httptest.NewRecorder()
				s.Handler().ServeHTTP(rec, r)
				if rec.Code != 404 {
					t.Fatal("revocation ignored", rec.Code)
				}
			}
		})
	}
}

func TestCastPublicOriginSessionAndParentDeadlines(t *testing.T) {
	f := &castFS{entries: []storage.Entry{{Name: "a.jpg"}}}
	s, v := cachedCastServer(t, f, true)
	cookie := func(end time.Time) *http.Cookie {
		expiry := strconv.FormatInt(end.Unix(), 10)
		return &http.Cookie{Name: shareCookieName, Value: expiry + "." + shareMAC(v, expiry)}
	}
	if w := castRequest(s, v, "POST", "/cast", "mode=slideshow", cookie(time.Now().Add(-time.Minute))); w.Code != 403 {
		t.Fatal("expired session minted ticket", w.Code)
	}
	parentEnd := time.Now().Add(time.Minute).Truncate(time.Second)
	entry := s.shares.entries[v.ID]
	entry.value.Share.ExpiresAt = &parentEnd
	s.shares.entries[v.ID] = entry
	w := castRequest(s, v, "POST", "/cast", "mode=slideshow", cookie(time.Now().Add(time.Hour)))
	var out playback
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.Expires.Equal(parentEnd) {
		t.Fatal("ticket outlived parent", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		origin, body string
		status       int
	}{
		{s.Config.Origin, "mode=slideshow", 403},
		{"", "mode=slideshow", 403},
		{s.Config.ShareOrigin, "mode=slideshow&path=" + strings.Repeat("a", 4097), 422},
	} {
		r := httptest.NewRequest("POST", s.Config.ShareOrigin+"/s/"+v.ID+"/cast", strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(cookie(time.Now().Add(time.Hour)))
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, r)
		if rec.Code != tc.status {
			t.Fatal("origin/body checks bypassed", rec.Code)
		}
	}
	expiredTicket := signPlayback(v, "", "slideshow", time.Now().Add(-time.Second))
	if w := castRequest(s, v, "GET", "/slideshow?ticket="+expiredTicket, "", nil); w.Code != 403 {
		t.Fatal("expired ticket accepted", w.Code)
	}
}
