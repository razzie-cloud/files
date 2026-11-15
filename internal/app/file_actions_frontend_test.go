package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/storage"
	"github.com/razzie-cloud/files/internal/store"
)

func TestFileActionControlsAndEscaping(t *testing.T) {
	s, err := New(config.Config{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := &store.Directory{ID: testOwner, Revision: 7, Name: `<root>`}
	for _, writable := range []bool{false, true} {
		var out bytes.Buffer
		if err := s.templates.ExecuteTemplate(&out, "page", view{Page: "browse", Directory: d, Writable: writable, ShareEnabled: true, Entries: []storage.Entry{{Name: `<file>.png`, Path: `folder & name/<file>.png`}, {Name: "subfolder", Path: "subfolder", Directory: true}}}); err != nil {
			t.Fatal(err)
		}
		body := out.String()
		for _, action := range []string{"Rename", "Move"} {
			if strings.Contains(body, `aria-label="`+action+`"`) != writable {
				t.Fatal("write action visibility", action, writable)
			}
			if writable && strings.Count(body, `aria-label="`+action+`"`) != 1 {
				t.Fatal("folder gained file-only action", action)
			}
		}
		for _, action := range []string{"Open", "Download", "Share", "Share folder", "Cast slideshow"} {
			if !strings.Contains(body, `data-tooltip="`+action+`"`) {
				t.Fatal("missing action tooltip", action)
			}
		}
		if strings.Contains(body, "<file>") || strings.Contains(body, "<root>") {
			t.Fatal("unescaped metadata")
		}
	}
	for _, kind := range []string{"rename", "move"} {
		var out bytes.Buffer
		v := view{Directory: d, FileAction: &fileActionView{Kind: kind, Source: `folder/<file>.txt`, Name: `<file>.txt`, Parent: "folder", Destination: "folder"}, Folders: []folderNode{{Name: `<folder>`, Path: `<folder>`, DirectoryID: d.ID}}}
		if err := s.templates.ExecuteTemplate(&out, "file-action-form", v); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "<file>") || strings.Contains(out.String(), "<folder>") {
			t.Fatal("dialog metadata not escaped")
		}
		if !strings.Contains(out.String(), `name="revision" value="7"`) || !strings.Contains(out.String(), `hx-target="#file-action-feedback"`) {
			t.Fatal("lost revision/error target")
		}
	}
}
