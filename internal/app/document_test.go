package app

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/storage"
)

type documentFS struct {
	storage.FS
	data              string
	size, objectSize  int64
	directory, opened bool
	readBytes         int
}

func (f *documentFS) Stat(context.Context, string) (storage.Entry, error) {
	return storage.Entry{Size: f.size, Directory: f.directory}, nil
}
func (f *documentFS) Open(_ context.Context, _ string, start, end int64) (storage.Object, error) {
	f.opened = true
	if start != -1 || end != -1 {
		panic("document previews must read the whole bounded source")
	}
	return storage.Object{ReadCloser: &documentReader{Reader: strings.NewReader(f.data), fs: f}, Size: f.objectSize}, nil
}

type documentReader struct {
	*strings.Reader
	fs *documentFS
}

func (r *documentReader) Close() error { return nil }
func (r *documentReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.fs.readBytes += n
	return n, err
}

func TestDocumentPreviews(t *testing.T) {
	srv, err := New(config.Config{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, source string
		want         []string
		status       int
	}{
		{"note.txt", "<script>alert(1)</script>\nárvíz & text", []string{"&lt;script&gt;", "árvíz &amp; text", `class="plain-text"`}, 200},
		{"page.html", `<img src=x onerror=alert(1)>`, []string{"&lt;img src=x onerror=alert(1)&gt;"}, 200},
		{"script.js", "alert('hello');", []string{"alert(&#39;hello&#39;);"}, 200},
		{"README", "hello", []string{"hello"}, 200},
		{"settings.JSON", "{\"key\": true}", []string{"&#34;key&#34;"}, 200},
		{"note.md", "# Heading\n\n**bold** and *italic*\n\n- item\n\n```go\nfmt.Println(1)\n```\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n- [x] Done\n\n~~old~~", []string{`<h1 id="heading">Heading</h1>`, "<strong>bold</strong>", "<em>italic</em>", "<li>item</li>", `<code class="language-go">`, "<table>", `type="checkbox"`, "<del>old</del>"}, 200},
		{"empty.md", "", []string{"<!doctype html>"}, 200},
		{"bom.txt", "\xef\xbb\xbfhello", []string{"<code>hello</code>"}, 200},
		{"binary.txt", "hello\x00world", []string{"binary or uses an unsupported text encoding"}, 415},
		{"utf16.txt", "\xff\xfeh\x00i\x00", []string{"Text previews require UTF-8"}, 415},
		{"invalid.txt", "\xff", []string{"Text previews require UTF-8"}, 415},
		{"large.md", strings.Repeat("x", maxDocumentBytes+1), []string{"2 MiB text preview limit"}, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := &documentFS{data: tc.source, size: int64(len(tc.source)), objectSize: int64(len(tc.source))}
			r := httptest.NewRequest("GET", "/download?inline=1", nil)
			r.Header.Set("Range", "bytes=0-1") // Source ranges must not truncate formatted documents.
			w := httptest.NewRecorder()
			srv.serveDownload(w, r, fs, tc.name)
			if w.Code != tc.status || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
				t.Fatalf("status %d, headers %v", w.Code, w.Header())
			}
			for _, want := range tc.want {
				if !strings.Contains(w.Body.String(), want) {
					t.Errorf("missing %q in %s", want, w.Body.String())
				}
			}
			if tc.status == 413 && fs.opened {
				t.Fatal("opened known oversized file")
			}
			if w.Header().Get("Content-Range") != "" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
				t.Fatal("document was not isolated or retained a source byte range")
			}
		})
	}
}

func TestMarkdownIsolation(t *testing.T) {
	srv, _ := New(config.Config{}, nil, nil, nil, nil)
	source := `<script>alert(1)</script>

<iframe src="/connections"></iframe>

[bad](javascript:alert%281%29) [data](data:text/html,bad) [relative](../private.md) [root](/connections) [network](//evil.example/)

[external](https://example.com/?a=1&b=2) <https://example.org/> [anchor](#heading)

![tracking](https://evil.example/track) ![local](/connections)
`
	fs := &documentFS{data: source, size: int64(len(source)), objectSize: int64(len(source))}
	w := httptest.NewRecorder()
	srv.serveDownload(w, httptest.NewRequest("GET", "/download?inline=1", nil), fs, "note.md")
	body := w.Body.String()
	for _, forbidden := range []string{"<script", "<iframe", "<img", "evil.example", "javascript:", "data:text", "../private", `href="/connections"`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("unsafe output %q: %s", forbidden, body)
		}
	}
	for _, want := range []string{`href="https://example.com/?a=1&amp;b=2" target="_blank" rel="noopener noreferrer"`, `href="https://example.org/"`, `href="#heading"`, "tracking", "local"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %s", want, body)
		}
	}
}

func TestDocumentBoundsAndHead(t *testing.T) {
	srv, _ := New(config.Config{}, nil, nil, nil, nil)
	for _, tc := range []struct {
		name, method, data string
		size, objectSize   int64
		directory          bool
		status             int
	}{
		{"head", "HEAD", "hello", 5, 5, false, 200},
		{"limit", "GET", strings.Repeat("x", maxDocumentBytes), maxDocumentBytes, maxDocumentBytes, false, 200},
		{"untrusted size", "GET", strings.Repeat("x", maxDocumentBytes+100), 1, 1, false, 413},
		{"changed", "GET", "hello", 5, 6, false, 409},
		{"truncated", "GET", "hi", 5, 5, false, 409},
		{"directory", "GET", "", 0, 0, true, 415},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := &documentFS{data: tc.data, size: tc.size, objectSize: tc.objectSize, directory: tc.directory}
			w := httptest.NewRecorder()
			srv.serveDownload(w, httptest.NewRequest(tc.method, "/download?inline=1", nil), fs, "note.txt")
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d", w.Code, tc.status)
			}
			if tc.method == "HEAD" && w.Body.Len() != 0 || fs.readBytes > maxDocumentBytes+1 {
				t.Fatal("HEAD returned a body or read exceeded preview limit")
			}
		})
	}
}

var _ io.ReadCloser = (*documentReader)(nil)
