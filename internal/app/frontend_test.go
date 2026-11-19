package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/identity"
	"github.com/razzie-cloud/files/internal/storage"
	"github.com/razzie-cloud/files/internal/store"
)

func TestFrontendPublicAndReadOnlyViews(t *testing.T) {
	srv, err := New(config.Config{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := &store.Directory{ID: testOwner, Name: "<private-directory>", Bucket: "bucket"}
	for _, tc := range []struct {
		name                 string
		data                 view
		management, writable bool
	}{
		{"writable", view{Page: "browse", Directory: d, Writable: true}, true, true},
		{"read-only", view{Page: "browse", Directory: d}, true, false},
		{"public-folder", view{Page: "shared", Public: true, ShareDirectory: true, ShareURL: "/s/sample"}, false, false},
		{"public-file", view{Page: "shared", Public: true, ShareURL: "/s/sample"}, false, false},
		{"public-password", view{Page: "share-password", Public: true, ShareURL: "/s/sample"}, false, false},
		{"public-error", view{Page: "error", Public: true, Message: "Unavailable"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.data.LandingOrigin = "http://localhost:9191"
			tc.data.User = identity.User{Name: "private-owner-metadata"}
			tc.data.Entries = []storage.Entry{{Name: "<file>.png", Path: "<file>.png"}}
			var page bytes.Buffer
			if err := srv.templates.ExecuteTemplate(&page, "page", tc.data); err != nil {
				t.Fatal(err)
			}
			html := page.String()
			if !strings.Contains(html, `rel="icon" href="/static/razzie-cloud-logo-favicon.png"`) {
				t.Fatal("page must use the dedicated favicon")
			}
			if !strings.Contains(html, `href="http://localhost:9191/" class="brand"`) && !strings.Contains(html, `class="brand" href="http://localhost:9191/"`) {
				t.Fatal("cloud brand does not link to landing")
			}
			if tc.management && !strings.Contains(html, `href="/" class="service-name"`) {
				t.Fatal("app name does not link to Files")
			}
			if strings.Contains(html, `aria-label="Primary"`) != tc.management {
				t.Fatal("management navigation visibility is wrong")
			}
			if strings.Contains(html, `data-upload`) != tc.writable {
				t.Fatal("write controls do not match directory access")
			}
			if strings.Contains(html, `name="confirm"`) != tc.writable {
				t.Fatal("delete controls do not match directory access")
			}
			if strings.Contains(html, "<private-directory>") || strings.Contains(html, "<file>") {
				t.Fatal("metadata was not escaped")
			}
			if !tc.management {
				if strings.Contains(html, "private-owner-metadata") {
					t.Fatal("public shell exposes owner metadata")
				}
				for _, u := range pageURLs(t, html, "a", "href") {
					if !u.IsAbs() && (u.Path == "/" || strings.HasPrefix(u.Path, "/connections") || strings.HasPrefix(u.Path, "/directories")) {
						t.Fatalf("public view links to management: %s", u)
					}
				}
			}
			if tc.management && !strings.Contains(html, `aria-label="breadcrumb"`) {
				t.Fatal("browse view lacks Pico breadcrumbs")
			}
		})
	}
}

func TestFrontendFragmentsRefreshNavigation(t *testing.T) {
	srv, err := New(config.Config{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []view{
		{Page: "home"},
		{Page: "connections", Connection: &store.Connection{Provider: "Other"}},
		{Page: "directory-form", Directory: &store.Directory{}},
		{Page: "shares", Directory: &store.Directory{ID: testOwner}},
	} {
		var page bytes.Buffer
		if err := srv.templates.ExecuteTemplate(&page, "content", data); err != nil {
			t.Fatalf("%s: %v", data.Page, err)
		}
		html := page.String()
		if !strings.Contains(html, `aria-label="Primary"`) {
			t.Fatalf("%s fragment does not refresh navigation", data.Page)
		}
		if strings.Contains(html, `<html`) || strings.Contains(html, `id="main"`) {
			t.Fatalf("%s fragment nests the page shell", data.Page)
		}
	}
}

func TestPublicRecipientContent(t *testing.T) {
	srv, err := New(config.Config{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, label   string
		preview, cast bool
	}{
		{`photo + % "<sample>.GIF`, "GIF image", true, false},
		{"clip.mp4", "MP4 video", true, true},
		{"notes.md", "Markdown document", true, false},
		{"archive.zip", "ZIP file", false, false},
		{"backup", "File", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := srv.templates.ExecuteTemplate(&out, "page", view{
				Page: "shared", Public: true, ShareURL: "/s/capability",
				Entries: []storage.Entry{{Name: tc.name, Size: 3565158}},
			}); err != nil {
				t.Fatal(err)
			}
			body := out.String()
			if strings.Count(body, "Read-only") != 1 || strings.Count(body, "<h1>") != 1 || strings.Contains(body, "<table") || strings.Contains(body, "<form") {
				t.Fatal("single file must have one heading/access label, without listing or mutation controls")
			}
			if !strings.Contains(body, tc.label) || !strings.Contains(body, "3.4 MiB") || strings.Contains(body, "<sample>") {
				t.Fatal("file metadata or escaping incorrect")
			}
			if strings.Contains(body, `data-preview="`) != tc.preview || strings.Contains(body, `data-mode="video"`) != tc.cast {
				t.Fatal("file actions do not match the supported format")
			}
			inline, attachment := 0, 0
			for _, u := range pageURLs(t, body, "a", "href") {
				if !strings.HasSuffix(u.Path, "/download") {
					continue
				}
				if u.Path != "/s/capability/download" || u.Query().Get("path") != "" || u.Host != "" || u.Fragment != "" {
					t.Fatalf("single-file action escaped capability: %s", u)
				}
				if u.Query().Get("inline") == "1" {
					inline++
				} else {
					attachment++
				}
			}
			wantPreview := 0
			if tc.preview {
				wantPreview = 1
			}
			if inline != wantPreview || attachment != 1 {
				t.Fatal("missing or duplicate file actions", inline, attachment)
			}
		})
	}
	for _, tc := range []struct {
		name                 string
		entries              []storage.Entry
		next, previous, want string
	}{
		{"mixed", []storage.Entry{{Directory: true}, {Name: "file.txt"}}, "", "", "1 folder · 1 file"},
		{"empty", nil, "", "", "0 folders · 0 files"},
		{"next", []storage.Entry{{Name: "a"}, {Name: "b"}}, "/s/capability?page=1", "", "0 folders · 2 files on this page"},
		{"previous", []storage.Entry{{Directory: true}}, "", "/s/capability?page=0", "1 folder · 0 files on this page"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := srv.templates.ExecuteTemplate(&out, "page", view{Page: "shared", Public: true, ShareDirectory: true, Message: "Shared folder", ShareURL: "/s/capability", Entries: tc.entries, Next: tc.next, Previous: tc.previous}); err != nil {
				t.Fatal(err)
			}
			if body := out.String(); strings.Count(body, "Read-only") != 1 || !strings.Contains(body, tc.want) || strings.Contains(body, "<form") {
				t.Fatal("folder summary/access incorrect")
			}
		})
	}
}
