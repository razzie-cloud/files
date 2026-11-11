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
			audio := "folder/audio + % árvíz.MP3"
			var body bytes.Buffer
			err := srv.templates.ExecuteTemplate(&body, "page", view{
				Page: page, Directory: &d, ShareURL: "/s/capability",
				Entries: []storage.Entry{
					{Name: `"<photo> + % árvíz.JPG`, Path: image},
					{Name: "video + %.mp4", Path: video},
					{Name: "notes.html", Path: "notes.html"},
					{Name: "readme.MD", Path: "readme.MD"},
					{Name: "paper.pdf", Path: "paper.pdf"},
					{Name: "log.txt", Path: "log.txt"},
					{Name: "audio + % árvíz.MP3", Path: audio},
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
			if len(opened) != 7 || opened[image] != "image" || opened[video] != "video" || opened[audio] != "audio" || opened["notes.html"] != "document" || opened["readme.MD"] != "document" || opened["log.txt"] != "document" || opened["paper.pdf"] != "pdf" {
				t.Fatalf("unexpected Open actions: %v", opened)
			}
			if !strings.Contains(body.String(), `<dialog id="media-preview"`) || !strings.Contains(body.String(), "controls playsinline") || !strings.Contains(body.String(), `<audio data-preview-audio aria-label="Audio playback" controls preload="metadata" hidden>`) {
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
		{"paper.PDF", "GET", "inline=1", "bytes=2-4", "application/pdf", "inline", "nte", 206},
		{"paper.pdf", "HEAD", "inline=1", "", "application/pdf", "inline", "", 200},
		{"song.MP3", "GET", "inline=1", "bytes=2-4", "audio/mpeg", "inline", "nte", 206},
		{"song.m4a", "HEAD", "inline=1", "", "audio/mp4", "inline", "", 200},
		{"book.m4b", "GET", "inline=1", "", "audio/mp4", "inline", "content", 200},
		{"song.aac", "GET", "inline=1", "", "audio/aac", "inline", "content", 200},
		{"song.ogg", "GET", "inline=1", "", "audio/ogg", "inline", "content", 200},
		{"song.oga", "GET", "inline=1", "", "audio/ogg", "inline", "content", 200},
		{"song.opus", "GET", "inline=1", "", "audio/ogg", "inline", "content", 200},
		{"song.wav", "GET", "inline=1", "", "audio/wav", "inline", "content", 200},
		{"song.wave", "GET", "inline=1", "", "audio/wav", "inline", "content", 200},
		{"song.flac", "GET", "inline=1", "", "audio/flac", "inline", "content", 200},
		{"song.weba", "GET", "inline=1", "", "audio/webm", "inline", "content", 200},
		{"song.aif", "GET", "inline=1", "", "audio/aiff", "inline", "content", 200},
		{"song.aiff", "GET", "inline=1", "", "audio/aiff", "inline", "content", 200},
		{"song.mp3", "GET", "", "", "application/octet-stream", "attachment", "content", 200},
		{"archive.zip", "GET", "inline=1", "", "", "", "", 415},
		{"script.exe", "GET", "inline=1", "", "", "", "", 415},
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
			if tc.disposition == "inline" && tc.contentType != "application/pdf" && !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
				t.Fatal("inline content missing sandbox")
			}
			if tc.contentType == "application/pdf" && !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'self'") {
				t.Fatal("PDF cannot be embedded by this origin")
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
	for _, tc := range []struct{ name, contentType string }{
		{"photo.jpg", "image/jpeg"},
		{"note.txt", "text/html; charset=utf-8"},
		{"README.md", "text/html; charset=utf-8"},
		{"paper.pdf", "application/pdf"},
		{"song.mp3", "audio/mpeg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{Origin: "https://files.example.com", ShareOrigin: "https://shared-files.example.com"}
			factory := &shareFactory{fs: &shareFS{}}
			srv, err := New(cfg, nil, nil, factory, nil)
			if err != nil {
				t.Fatal(err)
			}
			id := strings.Repeat("b", 64)
			share := store.Share{ID: id, Path: "private/" + tc.name, SessionKey: bytes.Repeat([]byte{1}, 32)}
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
			if w := request(base); w.Code != 200 || w.Header().Get("Content-Type") != tc.contentType || factory.fs.keys[len(factory.fs.keys)-1] != share.Path {
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
		})
	}
}
