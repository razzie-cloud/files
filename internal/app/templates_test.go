package app

import (
	"bytes"
	"net/url"
	"strings"
	"testing"

	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/storage"
	"github.com/razzie-cloud/files/internal/store"
	"golang.org/x/net/html"
)

func pageURLs(t *testing.T, page, tag, attribute string) []*url.URL {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	var urls []*url.URL
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == tag {
			for _, attr := range n.Attr {
				if attr.Key == attribute {
					u, err := url.Parse(attr.Val)
					if err != nil {
						t.Fatal(err)
					}
					urls = append(urls, u)
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	return urls
}

func TestBrowsePathURLs(t *testing.T) {
	srv, err := New(config.Config{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"simple.txt", "hello world.txt", "hello + %25.txt", "árvíz.txt", "folder/nested/file.txt", `folder & name/"<file>?x=1&path=other#.txt`} {
		t.Run(key, func(t *testing.T) {
			d := store.Directory{ID: "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa", Revision: 7, EncryptionMode: "rclone_crypt"}
			base := "/directories/" + d.ID
			var body bytes.Buffer
			err := srv.templates.ExecuteTemplate(&body, "page", view{
				Page: "browse", Directory: &d, Path: key, Writable: true,
				Entries: []storage.Entry{{Name: "file", Path: key}, {Name: "folder", Path: key, Directory: true}},
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, attr := range []struct {
				tag, name, target string
				count             int
			}{
				{"a", "href", base + "/download", 1},
				{"a", "href", base, 1},
				{"form", "action", base + "/upload", 2},
				{"form", "hx-post", base + "/upload", 2},
			} {
				count := 0
				for _, u := range pageURLs(t, body.String(), attr.tag, attr.name) {
					if u.Path != attr.target {
						continue
					}
					count++
					query, err := url.ParseQuery(u.RawQuery)
					if err != nil || len(query["path"]) != 1 || query.Get("path") != key {
						t.Errorf("%s %s URL %q: path=%q, want %q (error %v)", attr.tag, attr.name, u, query["path"], key, err)
					}
					if u.Host != "" || u.Scheme != "" || u.Fragment != "" {
						t.Errorf("path escaped the URL query: %q", u)
					}
					if attr.tag == "form" && query.Get("revision") != "7" {
						t.Errorf("upload URL lost revision: %q", u)
					}
				}
				if count != attr.count {
					t.Errorf("found %d %s %s URLs for %s, want %d", count, attr.tag, attr.name, attr.target, attr.count)
				}
			}
		})
	}
}
