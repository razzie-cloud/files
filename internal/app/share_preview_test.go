package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/store"
	"golang.org/x/net/html"
)

func sharePageMetadata(t *testing.T, body string) (map[string]string, string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{}
	var title string
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Parent != nil && n.Parent.Data == "head" {
			if n.Data == "meta" {
				attrs := map[string]string{}
				for _, attr := range n.Attr {
					attrs[attr.Key] = attr.Val
				}
				key := attrs["property"]
				if key == "" {
					key = attrs["name"]
				}
				metadata[key] = attrs["content"]
			}
			if n.Data == "title" && n.FirstChild != nil {
				title = n.FirstChild.Data
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	return metadata, title
}

func TestPublicShareLinkPreviews(t *testing.T) {
	for _, tc := range []struct {
		name, path, relative, title, mediaType string
		directory                              bool
	}{
		{"escaped image", `outside/photo + % árvíz "<img src=x>.JPG`, "", `photo + % árvíz "<img src=x>.JPG`, "image/jpeg", false},
		{"png", "outside/photo.png", "", "photo.png", "image/png", false},
		{"gif", "outside/photo.gif", "", "photo.gif", "image/gif", false},
		{"webp", "outside/photo.webp", "", "photo.webp", "image/webp", false},
		{"avif", "outside/photo.avif", "", "photo.avif", "image/avif", false},
		{"mp4", "outside/video.MP4", "", "video.MP4", "video/mp4", false},
		{"webm", "outside/video.webm", "", "video.webm", "video/webm", false},
		{"mov", "outside/video.mov", "", "video.mov", "video/quicktime", false},
		{"mkv", "outside/video.mkv", "", "video.mkv", "video/x-matroska", false},
		{"avi", "outside/video.avi", "", "video.avi", "video/x-msvideo", false},
		{"mpeg", "outside/video.mpeg", "", "video.mpeg", "video/mpeg", false},
		{"ogv", "outside/video.ogv", "", "video.ogv", "video/ogg", false},
		{"mp3", `outside/song + % árvíz "<sample>.MP3`, "", `song + % árvíz "<sample>.MP3`, "audio/mpeg", false},
		{"m4a", "outside/song.m4a", "", "song.m4a", "audio/mp4", false},
		{"aac", "outside/song.aac", "", "song.aac", "audio/aac", false},
		{"ogg", "outside/song.ogg", "", "song.ogg", "audio/ogg", false},
		{"wav", "outside/song.wav", "", "song.wav", "audio/wav", false},
		{"flac", "outside/song.flac", "", "song.flac", "audio/flac", false},
		{"weba", "outside/song.weba", "", "song.weba", "audio/webm", false},
		{"aiff", "outside/song.aiff", "", "song.aiff", "audio/aiff", false},
		{"document", "outside/paper.pdf", "", "paper.pdf", "", false},
		{"unsupported image", "outside/photo.heic", "", "photo.heic", "", false},
		{"active image", "outside/photo.svg", "", "photo.svg", "", false},
		{"folder with image extension", "outside/Trip.JPG", "", "Trip.JPG", "", true},
		{"folder with video extension", "outside/Trip.mp4", "", "Trip.mp4", "", true},
		{"folder with audio extension", "outside/Trip.mp3", "", "Trip.mp3", "", true},
		{"mapping root", "", "", "Family & friends", "", true},
		{"subfolder", "outside/Trip", `nested/árvíz + % "<2026>`, `árvíz + % "<2026>`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{Origin: "https://files.example.com", ShareOrigin: "https://shared-files.example.com:8443"}
			factory := &shareFactory{fs: &shareFS{}}
			srv, err := New(cfg, nil, nil, factory, nil)
			if err != nil {
				t.Fatal(err)
			}
			id := strings.Repeat("a", 64)
			share := store.Share{ID: id, Path: tc.path, IsDirectory: tc.directory}
			srv.shares.entries = map[string]shareCacheEntry{id: {
				value: shareSnapshot{Share: share, Directory: store.Directory{Name: "Family & friends"}},
				until: time.Now().Add(time.Minute),
			}}
			canonical := cfg.ShareOrigin + "/s/" + id
			if tc.relative != "" {
				canonical += "?path=" + url.QueryEscape(tc.relative)
			}
			separator := "?"
			if tc.relative != "" {
				separator = "&"
			}
			r := httptest.NewRequest("GET", canonical+separator+"page=0&untrusted=value", nil)
			r.Header.Set("X-Forwarded-Host", "untrusted.example")
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, r)
			if w.Code != 200 || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
				t.Fatal("share page failed", w.Code, w.Header())
			}
			meta, title := sharePageMetadata(t, w.Body.String())
			if meta["og:title"] != tc.title || title != tc.title+" · Razzie Cloud" || meta["og:url"] != canonical || meta["og:site_name"] != "Razzie Cloud Files" {
				t.Fatalf("incorrect share identity: %v, title %q", meta, title)
			}
			kind := "File"
			if tc.directory {
				kind = "Folder"
			}
			objectType := "website"
			mediaKind, _, _ := strings.Cut(tc.mediaType, "/")
			if mediaKind == "video" {
				objectType = "video.other"
			}
			if meta["og:description"] != kind+" shared with you via Razzie Cloud Files." || meta["og:type"] != objectType {
				t.Fatal("missing share description or type", meta)
			}
			for _, other := range []string{"image", "video", "audio"} {
				if other != mediaKind && (meta["og:"+other] != "" || meta["og:"+other+":type"] != "") {
					t.Fatal("unrelated media advertised", meta)
				}
			}
			card := "summary"
			if mediaKind == "image" {
				card = "summary_large_image"
				if meta["og:image:alt"] != tc.title {
					t.Fatal("incorrect image alternative text", meta)
				}
			}
			if meta["twitter:card"] != card {
				t.Fatal("incorrect preview card", meta)
			}
			if tc.mediaType == "" {
				return
			}
			mediaURL := cfg.ShareOrigin + "/s/" + id + "/download?inline=1"
			if meta["og:"+mediaKind] != mediaURL || meta["og:"+mediaKind+":type"] != tc.mediaType {
				t.Fatal("incorrect media preview", meta)
			}
			w = httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", meta["og:"+mediaKind], nil))
			if w.Code != 200 || w.Header().Get("Content-Type") != tc.mediaType || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline") || factory.fs.keys[len(factory.fs.keys)-1] != tc.path {
				t.Fatal("advertised media is not accessible within the share scope", w.Code, w.Header())
			}
		})
	}
}

func TestPublicSharePreviewPrivacy(t *testing.T) {
	for _, filename := range []string{"photo.jpg", "video.mp4", "song.mp3"} {
		t.Run(filename, func(t *testing.T) {
			for _, scenario := range []string{"password", "unlocked password", "expired", "revoked", "invalid file path"} {
				t.Run(scenario, func(t *testing.T) {
					cfg := config.Config{Origin: "https://files.example.com", ShareOrigin: "https://shared-files.example.com"}
					factory := &shareFactory{fs: &shareFS{}}
					srv, err := New(cfg, nil, nil, factory, nil)
					if err != nil {
						t.Fatal(err)
					}
					id := strings.Repeat("b", 64)
					share := store.Share{ID: id, Path: "private/" + filename, SessionKey: bytes.Repeat([]byte{1}, 32)}
					r := httptest.NewRequest("GET", cfg.ShareOrigin+"/s/"+id, nil)
					status, opens := http.StatusOK, 0
					var cacheErr error
					switch scenario {
					case "password", "unlocked password":
						share.PasswordHash = "password required"
						if scenario == "unlocked password" {
							expiry := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
							r.AddCookie(&http.Cookie{Name: shareCookieName, Value: expiry + "." + shareMAC(share, expiry)})
							opens = 1
						}
					case "expired":
						expired := time.Now().Add(-time.Minute)
						share.ExpiresAt = &expired
						status = http.StatusNotFound
					case "revoked":
						cacheErr = store.ErrNotFound
						status = http.StatusNotFound
					case "invalid file path":
						r.URL.RawQuery = "path=other.jpg"
						status = http.StatusNotFound
					}
					srv.shares.entries = map[string]shareCacheEntry{id: {value: shareSnapshot{Share: share}, err: cacheErr, until: time.Now().Add(time.Minute)}}
					w := httptest.NewRecorder()
					srv.Handler().ServeHTTP(w, r)
					meta, title := sharePageMetadata(t, w.Body.String())
					if w.Code != status || factory.calls != opens || title != "Shared files · Razzie Cloud" {
						t.Fatal("share access changed", w.Code, factory.calls, title)
					}
					for key := range meta {
						if strings.HasPrefix(key, "og:") || strings.HasPrefix(key, "twitter:") {
							t.Fatal("private or inaccessible share advertises metadata", meta)
						}
					}
				})
			}
		})
	}
}
