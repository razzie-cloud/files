package app

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/storage"
	"github.com/razzie-cloud/files/internal/store"
	"golang.org/x/net/html"
)

func TestPanelNavigationBoundaries(t *testing.T) {
	s, err := New(config.Config{ShareOrigin: "https://shared.test"}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := store.Directory{ID: testOwner, Name: "Photos", Bucket: "photos"}
	for _, public := range []bool{false, true} {
		origin, base, page := "https://files.test", "/directories/"+testOwner, "browse"
		if public {
			origin, base, page = "https://shared.test", "/s/capability", "shared"
		}
		for _, fragment := range []bool{false, true} {
			r := httptest.NewRequest("GET", origin+base+"?path=album", nil)
			if fragment {
				r.Header.Set("HX-Request", "true")
				r.Header.Set("HX-Target", "main")
			}
			w := httptest.NewRecorder()
			s.render(w, r, view{Page: page, Directory: &d, Writable: !public, Path: "album", Parent: base,
				ShareDirectory: true, ShareURL: base, Crumbs: []crumb{{Name: "Photos", URL: base}},
				Previous: base + "?offset=0", Next: base + "?offset=10",
				Entries: []storage.Entry{{Name: "nested", Path: "album/nested", Directory: true}, {Name: "song.mp3", Path: "album/song.mp3"}}})
			body := w.Body.String()
			if strings.Contains(body, "<!doctype html>") == fragment || strings.Contains(body, `id="main"`) == fragment {
				t.Fatalf("incorrect shell ownership (public=%v fragment=%v)", public, fragment)
			}
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			navigation, downloads, dialogs := 0, 0, 0
			var visit func(*html.Node)
			visit = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "a" {
					a := map[string]string{}
					for _, attr := range n.Attr {
						a[attr.Key] = attr.Val
					}
					switch {
					case strings.Contains(a["href"], "/download"):
						downloads++
						if a["hx-boost"] != "" || a["hx-get"] != "" {
							t.Error("download/preview was converted to panel navigation")
						}
					case a["aria-haspopup"] == "dialog":
						dialogs++
						if a["hx-boost"] != "" || a["hx-target"] == "#main" {
							t.Error("dialog was converted to panel navigation")
						}
					case strings.HasPrefix(a["href"], base):
						navigation++
						if a["hx-boost"] != "true" || a["hx-target"] != "#main" {
							t.Errorf("navigation lacks a main-panel target: %v", a)
						}
					case strings.HasPrefix(a["href"], "#"), strings.HasPrefix(a["href"], "https:"):
						if a["hx-boost"] != "" {
							t.Error("anchor or cross-app link was boosted")
						}
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					visit(c)
				}
			}
			visit(doc)
			if navigation < 5 || downloads < 2 || (!public && dialogs != 2) {
				t.Fatalf("fixture missing navigation boundaries: navigation=%d downloads=%d dialogs=%d", navigation, downloads, dialogs)
			}
		}
	}
}
