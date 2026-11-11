package app

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/store"
)

// The list, explicit full-page fallback and HTMX fragment must share the same
// revision-aware form without rendering saved credentials into HTML.
func TestConnectionSettingsPresentation(t *testing.T) {
	app, err := New(config.Config{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := store.Connection{ID: "11111111-1111-4111-8111-111111111111", Name: `<storage>`, Provider: "AWS", Region: "eu-central-1", Revision: 7, AccessMode: "writable", SecretRef: "stored-secret-sentinel"}
	for _, tc := range []struct {
		name                string
		populated, explicit bool
	}{
		{"first-use", false, false}, {"list", true, false}, {"fallback", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := view{Page: "connections", Connection: &c, ConnectionForm: tc.explicit}
			if tc.populated {
				data.Connections = []store.Connection{c}
			}
			var out bytes.Buffer
			if err := app.templates.ExecuteTemplate(&out, "page", data); err != nil {
				t.Fatal(err)
			}
			html := out.String()
			hasForm := strings.Contains(html, `data-connection-form`)
			if hasForm != (!tc.populated || tc.explicit) {
				t.Fatalf("form visibility: %s", html)
			}
			if tc.populated && !strings.Contains(html, `hx-target="#connection-dialog-content"`) {
				t.Fatal("missing dialog actions")
			}
			for _, secret := range []string{"stored-access-sentinel", "stored-secret-sentinel", "stored-token-sentinel"} {
				if strings.Contains(html, secret) {
					t.Fatal("saved credential rendered")
				}
			}
			if strings.Contains(html, `<storage>`) {
				t.Fatal("connection metadata was not escaped")
			}
			if hasForm && !strings.Contains(html, `name="revision" value="7"`) {
				t.Fatal("form lost revision")
			}
		})
	}
	data := view{Page: "connections", Connection: &c}
	req := httptest.NewRequest("GET", "/connections/11111111-1111-4111-8111-111111111111/edit", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "connection-dialog-content")
	out := httptest.NewRecorder()
	app.render(out, req, data)
	html := out.Body.String()
	if out.Code != 200 || strings.Contains(html, "<html") || strings.Contains(html, `aria-label="Primary"`) || !strings.Contains(html, `data-close-connection`) || !strings.Contains(html, `name="revision" value="7"`) {
		t.Fatalf("invalid dialog fragment: %d %s", out.Code, html)
	}
	for _, secret := range []string{"stored-access-sentinel", "stored-secret-sentinel", "stored-token-sentinel"} {
		if strings.Contains(html, secret) {
			t.Fatal("saved credential in dialog")
		}
	}
}

func TestConnectionErrorsKeepFormInBrowser(t *testing.T) {
	app, err := New(config.Config{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"connection-feedback", "connection-dialog-content"} {
		req := httptest.NewRequest("POST", "/connections", strings.NewReader("secret_key=submitted-secret-sentinel"))
		req.Header.Set("HX-Request", "true")
		req.Header.Set("HX-Target", target)
		out := httptest.NewRecorder()
		app.fail(out, req, store.ErrConflict, 409)
		want := "#connection-feedback"
		if target == "connection-dialog-content" {
			want = "#connection-list-feedback"
		}
		html := out.Body.String()
		if out.Code != 409 || out.Header().Get("HX-Retarget") != want || !strings.Contains(html, `role="alert"`) || strings.Contains(html, "<form") || strings.Contains(html, "submitted-secret-sentinel") {
			t.Fatalf("invalid error fragment: %d %s", out.Code, html)
		}
	}
}
