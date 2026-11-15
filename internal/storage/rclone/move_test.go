package rclone

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/razzie-cloud/files/internal/devs3"
	"github.com/razzie-cloud/files/internal/secrets"
	"github.com/razzie-cloud/files/internal/storage"
	"github.com/razzie-cloud/files/internal/store"
	"github.com/rclone/rclone/fs"
)

func moveTestFS(t *testing.T, mode, names string, denyCopy, denyDelete *atomic.Bool) storage.FS {
	t.Helper()
	backend, _, err := devs3.New("move-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (denyCopy.Load() && r.Header.Get("X-Amz-Copy-Source") != "") || (denyDelete.Load() && r.Method == http.MethodDelete) {
			http.Error(w, "denied", 403)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(remote.Close)
	u, _ := url.Parse(remote.URL)
	policy, err := NewPolicy(u.Host, true)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := StartEgress(policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proxy.Close)
	factory := &Factory{ProxyURL: proxy.URL, Policy: policy, MaxEntries: 100}
	handle, err := factory.Open(context.Background(), store.Connection{Provider: "Other", Endpoint: remote.URL, Region: "us-east-1", PathStyle: true}, store.Directory{Bucket: "move-test", Prefix: "mapped-root", EncryptionMode: mode, FilenameEncryption: names, DirectoryEncryption: true, FilenameEncoding: "base32", Suffix: ".bin"}, secrets.Value{AccessKey: "dummy", SecretKey: "dummy"}, secrets.Value{Password: "move encryption fixture", Password2: "salt"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(handle.Close)
	return handle
}

func TestMoveS3AndCrypt(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"none", "rclone_crypt"} {
		for _, names := range []string{"standard", "off", "obfuscate"} {
			if mode == "none" && names != "standard" {
				continue
			}
			t.Run(mode+"/"+names, func(t *testing.T) {
				var denyCopy, denyDelete atomic.Bool
				h := moveTestFS(t, mode, names, &denyCopy, &denyDelete)
				content := bytes.Repeat([]byte("private + % árvíz\n"), 10000)
				must := func(err error) {
					t.Helper()
					if err != nil {
						t.Fatal(err)
					}
				}
				must(h.Put(ctx, "source + %.txt", bytes.NewReader(content)))
				must(h.Mkdir(ctx, "destination/child"))
				must(h.Move(ctx, "source + %.txt", "renamed árvíz.txt"))
				if _, err := h.Stat(ctx, "source + %.txt"); !errors.Is(err, storage.ErrNotFound) {
					t.Fatal("source retained", err)
				}
				must(h.Move(ctx, "renamed árvíz.txt", "destination/child/renamed árvíz.txt"))
				// A fresh handle is not needed for the read, but byte ranges ensure crypt
				// remains compatible after copying encrypted content to a different name.
				in, err := h.Open(ctx, "destination/child/renamed árvíz.txt", 65000, 66000)
				must(err)
				got, err := io.ReadAll(in)
				in.Close()
				must(err)
				if !bytes.Equal(got, content[65000:66001]) {
					t.Fatal("moved content changed")
				}
				must(h.Put(ctx, "existing", strings.NewReader("do not overwrite")))
				must(h.Mkdir(ctx, "folder"))
				for _, tc := range []struct {
					source, target string
					want           error
				}{
					{"destination/child/renamed árvíz.txt", "existing", storage.ErrConflict},
					{"destination/child/renamed árvíz.txt", "folder", storage.ErrConflict},
					{"destination/child/renamed árvíz.txt", "missing/target", storage.ErrNotFound},
					{"missing", "new", storage.ErrNotFound}, {"folder", "new", storage.ErrNotFound},
					{"existing", "existing", storage.ErrConflict}, {"existing", "../escape", storage.ErrInvalidPath},
					{"../escape", "new", storage.ErrInvalidPath}, {"existing", "", storage.ErrInvalidPath},
				} {
					if err := h.Move(ctx, tc.source, tc.target); !errors.Is(err, tc.want) {
						t.Fatalf("move %q -> %q: %v want %v", tc.source, tc.target, err, tc.want)
					}
				}
				// Providers without server-side Copy use the same exported rclone/crypt
				// upload APIs. The fallback must preserve content and remove only on success.
				h.(*handle).fs.Features().Copy = func(context.Context, fs.Object, string) (fs.Object, error) { return nil, fs.ErrorCantCopy }
				must(h.Move(ctx, "destination/child/renamed árvíz.txt", "back at root.txt"))
				in, err = h.Open(ctx, "back at root.txt", -1, -1)
				must(err)
				got, err = io.ReadAll(in)
				in.Close()
				must(err)
				if !bytes.Equal(got, content) {
					t.Fatal("fallback content changed")
				}
				must(h.Put(ctx, "zero", strings.NewReader("")))
				must(h.Move(ctx, "zero", "zero renamed"))
				if info, err := h.Stat(ctx, "zero renamed"); err != nil || info.Size != 0 {
					t.Fatalf("empty move: %+v %v", info, err)
				}
			})
		}
	}
}

func TestMoveFailureKeepsSource(t *testing.T) {
	for _, mode := range []string{"none", "rclone_crypt"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			var denyCopy, denyDelete atomic.Bool
			h := moveTestFS(t, mode, "standard", &denyCopy, &denyDelete)
			if err := h.Put(ctx, "source", strings.NewReader("keep me")); err != nil {
				t.Fatal(err)
			}
			denyCopy.Store(true)
			if err := h.Move(ctx, "source", "target"); err == nil {
				t.Fatal("copy failure accepted")
			}
			if _, err := h.Stat(ctx, "source"); err != nil {
				t.Fatal("copy failure lost source", err)
			}
			if _, err := h.Stat(ctx, "target"); !errors.Is(err, storage.ErrNotFound) {
				t.Fatal("copy failure created target", err)
			}
			denyCopy.Store(false)
			denyDelete.Store(true)
			if err := h.Move(ctx, "source", "target"); !errors.Is(err, storage.ErrMoveIncomplete) {
				t.Fatal("missing partial-move signal", err)
			}
			for _, name := range []string{"source", "target"} {
				in, err := h.Open(ctx, name, -1, -1)
				if err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(in)
				in.Close()
				if err != nil || string(got) != "keep me" {
					t.Fatal("partial move lost content", name, err)
				}
			}
			denyDelete.Store(false)
			if err := h.Move(ctx, "source", "target"); !errors.Is(err, storage.ErrConflict) {
				t.Fatal("retry overwrote destination", err)
			}
		})
	}
}
