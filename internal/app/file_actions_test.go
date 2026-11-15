package app

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/razzie-cloud/files/internal/secrets"
	"github.com/razzie-cloud/files/internal/storage"
	"github.com/razzie-cloud/files/internal/store"
)

func fileActionStorage(t *testing.T, s *Server, d store.Directory) storage.FS {
	t.Helper()
	ctx := context.Background()
	c, err := s.Repo.Connection(ctx, d.Owner, d.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := s.Secrets.Get(ctx, c.Owner, c.SecretRef)
	if err != nil {
		t.Fatal(err)
	}
	keys := secrets.Value{}
	if d.EncryptionMode == "rclone_crypt" {
		keys, err = s.Secrets.Get(ctx, d.Owner, d.CryptSecretRef)
		if err != nil {
			t.Fatal(err)
		}
	}
	fs, err := s.Storage.Open(ctx, c, d, credentials, keys)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fs.Close)
	return fs
}
func fileActionRequest(s *Server, method, target, owner, hxTarget string, values url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(values.Encode()))
	req.Header.Set("X-Auth-Request-User", owner)
	req.Header.Set("Origin", s.Config.Origin)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if hxTarget != "" {
		req.Header.Set("HX-Request", "true")
		req.Header.Set("HX-Target", hxTarget)
	}
	out := httptest.NewRecorder()
	s.Handler().ServeHTTP(out, req)
	return out
}
func TestFileActionsWorkflow(t *testing.T) {
	for _, mode := range []string{"none", "rclone_crypt"} {
		t.Run(mode, func(t *testing.T) {
			s, d := uploadTestServer(t, mode)
			fs := fileActionStorage(t, s, d)
			ctx := context.Background()
			base := "/directories/" + d.ID
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(fs.Put(ctx, "Projects/Brand refresh/brief + % árvíz.txt", strings.NewReader("private contents")))
			must(fs.Mkdir(ctx, "Archive"))
			must(fs.Mkdir(ctx, "Projects/empty"))
			source := "Projects/Brand refresh/brief + % árvíz.txt"
			for _, kind := range []string{"rename", "move"} {
				for _, target := range []string{"", "file-action-dialog-content"} {
					out := fileActionRequest(s, "GET", base+"/"+kind+"?path="+url.QueryEscape(source), d.Owner, target, nil)
					if out.Code != 200 || !strings.Contains(out.Body.String(), `data-file-action="`+kind+`"`) {
						t.Fatalf("form: %d %s", out.Code, out.Body.String())
					}
					if strings.Contains(out.Body.String(), "<html") != (target == "") {
						t.Fatal("wrong full-page/fragment form")
					}
				}
				if out := fileActionRequest(s, "GET", base+"/"+kind+"?path="+url.QueryEscape(source), uuid.NewString(), "file-action-dialog-content", nil); out.Code != 404 {
					t.Fatal("other owner opened form", out.Code)
				}
			}
			out := fileActionRequest(s, "GET", base+"/folders?path=Projects", d.Owner, "folder-children", nil)
			if out.Code != 200 || strings.Contains(out.Body.String(), "brief") || !strings.Contains(out.Body.String(), "Brand refresh") || !strings.Contains(out.Body.String(), "empty") {
				t.Fatalf("folder listing %d %s", out.Code, out.Body.String())
			}
			for _, key := range []string{"../escape", "/absolute", "Projects//bad"} {
				if out := fileActionRequest(s, "GET", base+"/folders?path="+url.QueryEscape(key), d.Owner, "folder-children", nil); out.Code != 422 || out.Header().Get("HX-Retarget") != "#file-action-feedback" {
					t.Fatal("invalid tree path accepted", key, out.Code)
				}
			}
			if out := fileActionRequest(s, "GET", base+"/folders", uuid.NewString(), "folder-children", nil); out.Code != 404 {
				t.Fatal("other owner listed folders", out.Code)
			}
			for _, tc := range []struct {
				kind   string
				values url.Values
				code   int
			}{
				{"rename", url.Values{"name": {"../escape"}}, 422}, {"rename", url.Values{"name": {""}}, 422},
				{"rename", url.Values{"name": {"brief + % árvíz.txt"}}, 422}, {"move", url.Values{"destination": {"Projects/Brand refresh"}}, 422},
				{"move", url.Values{"destination": {"../escape"}}, 422}, {"move", url.Values{"destination": {"missing"}}, 404},
				{"move", url.Values{"destination": {"Archive"}, "revision": {"999"}}, 409},
			} {
				tc.values.Set("path", source)
				if tc.values.Get("revision") == "" {
					tc.values.Set("revision", strconv.FormatInt(d.Revision, 10))
				}
				out := fileActionRequest(s, "POST", base+"/"+tc.kind, d.Owner, "file-action-feedback", tc.values)
				if out.Code != tc.code || out.Header().Get("HX-Retarget") != "#file-action-feedback" || strings.Contains(out.Body.String(), "<form") {
					t.Fatalf("validation %s: %d %s", tc.kind, out.Code, out.Body.String())
				}
				if _, err := fs.Stat(ctx, source); err != nil {
					t.Fatal("validation lost source", err)
				}
			}
			create := url.Values{"destination": {"Archive"}, "name": {"Review + % árvíz"}, "revision": {fmt.Sprint(d.Revision)}}
			out = fileActionRequest(s, "POST", base+"/move-folder", d.Owner, "folder-create-feedback", create)
			if out.Code != 200 || out.Header().Get("HX-Retarget") != "#folder-create-result" || !strings.Contains(html.UnescapeString(out.Body.String()), `data-created-folder="Archive/Review + % árvíz"`) {
				t.Fatalf("create %d %s", out.Code, out.Body.String())
			}
			if out = fileActionRequest(s, "POST", base+"/move-folder", d.Owner, "folder-create-feedback", create); out.Code != 409 || out.Header().Get("HX-Retarget") != "#folder-create-feedback" {
				t.Fatal("folder duplicate", out.Code)
			}
			create.Set("name", "../bad")
			if out = fileActionRequest(s, "POST", base+"/move-folder", d.Owner, "folder-create-feedback", create); out.Code != 422 {
				t.Fatal("invalid child", out.Code)
			}
			rename := url.Values{"path": {source}, "name": {"Renamed <brief> + %.txt"}, "revision": {fmt.Sprint(d.Revision)}}
			out = fileActionRequest(s, "POST", base+"/rename", d.Owner, "file-action-feedback", rename)
			if out.Code != 200 || !strings.Contains(out.Header().Get("HX-Location"), base) {
				t.Fatal("rename", out.Code, out.Body.String())
			}
			renamed := "Projects/Brand refresh/Renamed <brief> + %.txt"
			if _, err := fs.Stat(ctx, source); !errors.Is(err, storage.ErrNotFound) {
				t.Fatal("old name remains", err)
			}
			// Collision with an existing file must preserve both files.
			must(fs.Put(ctx, "Archive/Review + % árvíz/Renamed <brief> + %.txt", strings.NewReader("existing")))
			move := url.Values{"path": {renamed}, "destination": {"Archive/Review + % árvíz"}, "revision": {fmt.Sprint(d.Revision)}}
			if out = fileActionRequest(s, "POST", base+"/move", d.Owner, "file-action-feedback", move); out.Code != 409 {
				t.Fatal("destination overwrite", out.Code)
			}
			move.Set("destination", "Archive")
			out = fileActionRequest(s, "POST", base+"/move", d.Owner, "", move)
			if out.Code != 303 {
				t.Fatalf("move %d %s", out.Code, out.Body.String())
			}
			moved := "Archive/Renamed <brief> + %.txt"
			in, err := fs.Open(ctx, moved, -1, -1)
			must(err)
			got, err := io.ReadAll(in)
			in.Close()
			must(err)
			if string(got) != "private contents" {
				t.Fatal("move content changed")
			}
			move.Set("path", moved)
			move.Set("destination", "")
			if out = fileActionRequest(s, "POST", base+"/move", d.Owner, "", move); out.Code != 303 {
				t.Fatal("move to root", out.Code)
			}
			for _, kind := range []string{"rename", "move", "move-folder"} {
				if out = fileActionRequest(s, "POST", base+"/"+kind, uuid.NewString(), "file-action-feedback", move); out.Code != 404 {
					t.Fatal("cross-owner write", kind, out.Code)
				}
			}
		})
	}
}

func TestConcurrentFileMovesDoNotOverwrite(t *testing.T) {
	for _, mode := range []string{"none", "rclone_crypt"} {
		t.Run(mode, func(t *testing.T) {
			s, d := uploadTestServer(t, mode)
			s.uploads = make(chan struct{}, 2)
			fs := fileActionStorage(t, s, d)
			ctx := context.Background()
			for _, parent := range []string{"one", "two"} {
				if err := fs.Put(ctx, parent+"/same.txt", strings.NewReader(parent)); err != nil {
					t.Fatal(err)
				}
			}
			codes := make(chan int, 2)
			var wg sync.WaitGroup
			for _, parent := range []string{"one", "two"} {
				wg.Add(1)
				go func(parent string) {
					defer wg.Done()
					out := fileActionRequest(s, "POST", "/directories/"+d.ID+"/move", d.Owner, "", url.Values{"path": {parent + "/same.txt"}, "destination": {""}, "revision": {fmt.Sprint(d.Revision)}})
					codes <- out.Code
				}(parent)
			}
			wg.Wait()
			close(codes)
			counts := map[int]int{}
			for code := range codes {
				counts[code]++
			}
			if counts[303] != 1 || counts[409] != 1 {
				t.Fatal("concurrent results", counts)
			}
			retained := 0
			for _, parent := range []string{"one", "two"} {
				if _, err := fs.Stat(ctx, parent+"/same.txt"); err == nil {
					retained++
				}
			}
			if retained != 1 {
				t.Fatal("losing source not retained", retained)
			}
		})
	}
}

type fileActionPolicyRepo struct {
	store.Repository
	change func()
}

func (r *fileActionPolicyRepo) LockMany(ctx context.Context, keys []string) (func(), error) {
	unlock, err := r.Repository.LockMany(ctx, keys)
	if err == nil {
		r.change()
	}
	return unlock, err
}
func TestFileMoveRechecksPolicyAfterLock(t *testing.T) {
	for _, change := range []string{"directory read-only", "connection read-only", "directory revision", "connection revision"} {
		t.Run(change, func(t *testing.T) {
			s, d := uploadTestServer(t, "none")
			fs := fileActionStorage(t, s, d)
			ctx := context.Background()
			if err := fs.Put(ctx, "source", strings.NewReader("keep")); err != nil {
				t.Fatal(err)
			}
			repo := s.Repo
			c, err := repo.Connection(ctx, d.Owner, d.ConnectionID)
			if err != nil {
				t.Fatal(err)
			}
			s.Repo = &fileActionPolicyRepo{Repository: repo, change: func() {
				switch change {
				case "directory read-only":
					d.ReadOnly = true
				case "directory revision":
					d.Name = "New display name"
				case "connection read-only":
					c.AccessMode = "read_only"
				case "connection revision":
					c.Name = "New connection name"
				}
				var err error
				if strings.HasPrefix(change, "directory") {
					err = repo.SaveDirectory(ctx, &d)
				} else {
					err = repo.SaveConnection(ctx, &c)
				}
				if err != nil {
					t.Fatal(err)
				}
			}}
			out := fileActionRequest(s, "POST", "/directories/"+d.ID+"/rename", d.Owner, "file-action-feedback", url.Values{"path": {"source"}, "name": {"target"}, "revision": {fmt.Sprint(d.Revision)}})
			want := 409
			if strings.HasSuffix(change, "read-only") {
				want = 403
			}
			if out.Code != want {
				t.Fatalf("status %d want %d: %s", out.Code, want, out.Body.String())
			}
			if _, err := fs.Stat(ctx, "source"); err != nil {
				t.Fatal("source changed after policy update", err)
			}
			if _, err := fs.Stat(ctx, "target"); !errors.Is(err, storage.ErrNotFound) {
				t.Fatal("destination written after policy update", err)
			}
		})
	}
}
