package app

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/store"
)

type firstUseRepo struct {
	store.Repository
	t           *testing.T
	directories []store.Directory
	connections []store.Connection
	err         error
}

func (r firstUseRepo) Directories(_ context.Context, owner string) ([]store.Directory, error) {
	if owner != testOwner {
		r.t.Fatalf("directories queried for %q", owner)
	}
	return r.directories, nil
}

func (r firstUseRepo) Connections(_ context.Context, owner string) ([]store.Connection, error) {
	if owner != testOwner {
		r.t.Fatalf("connections queried for %q", owner)
	}
	if len(r.directories) != 0 {
		r.t.Fatal("populated home unnecessarily queried connections")
	}
	return r.connections, r.err
}

func TestHomeFirstUseConnections(t *testing.T) {
	for _, tc := range []struct {
		name        string
		connections []store.Connection
		directories []store.Directory
		err         error
		status      int
		want, avoid string
	}{
		{name: "none", status: 200, want: "Add your first connection", avoid: "Add your first directory"},
		{name: "personal", connections: []store.Connection{{ID: testOwner}}, status: 200, want: "Add your first directory", avoid: "Add your first connection"},
		{name: "shared", connections: []store.Connection{{ID: testOwner, Shared: true}}, status: 200, want: "Add your first directory", avoid: "Add your first connection"},
		{name: "populated", directories: []store.Directory{{ID: testOwner, Name: "Existing directory"}}, status: 200, want: "Existing directory", avoid: "Add your first"},
		{name: "query-error", err: errors.New("private database error"), status: 500, want: "Request failed", avoid: "Add your first connection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := firstUseRepo{t: t, connections: tc.connections, directories: tc.directories, err: tc.err}
			s, err := New(config.Config{}, repo, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, fragment := range []bool{false, true} {
				r := httptest.NewRequest("GET", "/", nil)
				r.Header.Set("X-Auth-Request-User", testOwner)
				if fragment {
					r.Header.Set("HX-Request", "true")
				}
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				body := w.Body.String()
				if w.Code != tc.status || !strings.Contains(body, tc.want) || strings.Contains(body, tc.avoid) || strings.Contains(body, "private database error") {
					t.Fatalf("fragment=%v: %d %s", fragment, w.Code, body)
				}
				if len(tc.connections) > 0 && !strings.Contains(body, `href="/directories/new" role="button" hx-boost="true"`) {
					t.Fatal("first directory action lost its direct URL or HTMX navigation")
				}
			}
		})
	}
}
