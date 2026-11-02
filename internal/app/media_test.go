package app

import (
	"bytes"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/storage"
	"github.com/razzie-cloud/files/internal/store"
	"golang.org/x/net/html"
)

func TestMediaActions(t *testing.T) {
	srv, err := New(config.Config{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{"browse", "shared"} {
		t.Run(page, func(t *testing.T) {
			d := store.Directory{ID: testOwner}
			image := `folder & name/"<photo> + % árvíz.JPG`
			video := "folder/video + %.mp4"
			var body bytes.Buffer
			err := srv.templates.ExecuteTemplate(&body, "page", view{
				Page: page, Directory: &d, ShareURL: "/s/capability",
				Entries: []storage.Entry{
					{Name: `"<photo> + % árvíz.JPG`, Path: image},
					{Name: "video + %.mp4", Path: video},
					{Name: "notes.html", Path: "notes.html"},
					{Name: "folder.jpg", Path: "folder.jpg", Directory: true},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			doc, err := html.Parse(strings.NewReader(body.String()))
			if err != nil {
				t.Fatal(err)
			}
			opened := map[string]string{}
			var visit func(*html.Node)
			visit = func(n *html.Node) {
				attrs := map[string]string{}
				for _, attr := range n.Attr {
					attrs[attr.Key] = attr.Val
				}
				if kind := attrs["data-preview"]; kind != "" {
					u, err := url.Parse(attrs["href"])
					if err != nil {
						t.Fatal(err)
					}
					base := "/directories/" + d.ID
					if page == "shared" {
						base = "/s/capability"
					}
					if u.Path != base+"/download" || u.Host != "" || u.Fragment != "" || u.Query().Get("inline") != "1" {
						t.Fatalf("bad preview URL %q", u)
					}
					opened[u.Query().Get("path")] = kind
				}
				for child := n.FirstChild; child != nil; child = child.NextSibling {
					visit(child)
				}
			}
			visit(doc)
			if len(opened) != 2 || opened[image] != "image" || opened[video] != "video" {
				t.Fatalf("unexpected Open actions: %v", opened)
			}
			if !strings.Contains(body.String(), `<dialog id="media-preview"`) || !strings.Contains(body.String(), "controls playsinline") {
				t.Fatal("native preview elements missing")
			}
		})
	}
}

func TestInlineMediaResponses(t *testing.T) {
	srv := &Server{}
	for _, tc := range []struct {
		name, method, query, rangeHeader, contentType, disposition, body string
		status                                                           int
	}{
		{"photo.JPG", "GET", "inline=1", "", "image/jpeg", "inline", "content", 200},
		{"video.mp4", "GET", "inline=1", "bytes=2-4", "video/mp4", "inline", "nte", 206},
		{"video.webm", "HEAD", "inline=1", "", "video/webm", "inline", "", 200},
		{"image.svg", "GET", "inline=1", "", "image/svg+xml", "inline", "content", 200},
		{"image.png", "GET", "", "", "application/octet-stream", "attachment", "content", 200},
		{"page.html", "GET", "inline=1", "", "", "", "", 415},
		{"script.js", "GET", "inline=1", "", "", "", "", 415},
	} {
		t.Run(tc.name+tc.method+tc.query, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/download?"+tc.query, nil)
			r.Header.Set("Range", tc.rangeHeader)
			w := httptest.NewRecorder()
			fs := &shareFS{}
			srv.serveDownload(w, r, fs, tc.name)
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d", w.Code, tc.status)
			}
			if tc.status == 415 {
				if len(fs.keys) != 0 {
					t.Fatal("unsupported preview reached storage")
				}
				return
			}
			disposition, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
			if err != nil || disposition != tc.disposition || params["filename"] != tc.name || w.Header().Get("Content-Type") != tc.contentType || w.Body.String() != tc.body {
				t.Fatalf("bad response: %v %q", w.Header(), w.Body.String())
			}
			if tc.disposition == "inline" && !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
				t.Fatal("inline content missing sandbox")
			}
			if tc.contentType == "image/svg+xml" && w.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox; frame-ancestors 'none'" {
				t.Fatal("SVG can load subresources or use the application origin")
			}
			if tc.status == 206 && w.Header().Get("Content-Range") != "bytes 2-4/7" {
				t.Fatal("range lost", w.Header())
			}
		})
	}
}

func TestPublicMediaAuthorization(t *testing.T) {
	cfg := config.Config{Origin: "https://files.example.com", ShareOrigin: "https://shared-files.example.com"}
	factory := &shareFactory{fs: &shareFS{}}
	srv, err := New(cfg, nil, nil, factory, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("b", 64)
	share := store.Share{ID: id, Path: "private/photo.jpg", SessionKey: bytes.Repeat([]byte{1}, 32)}
	setShare := func() {
		srv.shares.entries = map[string]shareCacheEntry{id: {value: shareSnapshot{Share: share}, until: time.Now().Add(time.Minute)}}
	}
	setShare()
	h := srv.Handler()
	request := func(target string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", target, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	base := cfg.ShareOrigin + "/s/" + id + "/download?inline=1"
	if w := request(base); w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || factory.fs.keys[len(factory.fs.keys)-1] != share.Path {
		t.Fatal("single-file inline preview failed", w.Code, w.Header())
	}
	before := factory.calls
	if w := request(base + "&path=other.jpg"); w.Code != 404 || factory.calls != before {
		t.Fatal("preview escaped single-file capability", w.Code)
	}
	share.PasswordHash = "password required"
	setShare()
	if w := request(base); !strings.Contains(w.Body.String(), "requires a password") || factory.calls != before {
		t.Fatal("preview bypassed password gate", w.Code)
	}
	expired := time.Now().Add(-time.Minute)
	share.ExpiresAt = &expired
	setShare()
	if w := request(base); w.Code != 404 || factory.calls != before {
		t.Fatal("preview bypassed expiration", w.Code)
	}
	if w := request(cfg.Origin + "/directories/" + testOwner + "/download?path=image.jpg&inline=1"); w.Code != http.StatusUnauthorized {
		t.Fatal("management preview bypassed identity", w.Code)
	}
}
