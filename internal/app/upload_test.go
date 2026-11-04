package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/secrets"
	"github.com/razzie-cloud/files/internal/storage"
	rclonestorage "github.com/razzie-cloud/files/internal/storage/rclone"
	"github.com/razzie-cloud/files/internal/store"
	"github.com/razzie-cloud/files/internal/testutil"
)

type uploadTestFile struct{ name, data, field string }

func uploadTestServer(t *testing.T, mode string) (*Server, store.Directory) {
	t.Helper()
	repo := &store.Store{DB: testutil.Database(t)}
	remote, _ := testutil.S3(t, "upload-test")
	u, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := rclonestorage.NewPolicy(u.Host, true)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := rclonestorage.StartEgress(policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proxy.Close)
	secretStore := secrets.New(repo.DB)
	ref, err := secretStore.Put(context.Background(), testOwner, secrets.Value{AccessKey: "dummy", SecretKey: "dummy-secret", Password: "test crypt password"})
	if err != nil {
		t.Fatal(err)
	}
	c := store.Connection{Owner: testOwner, Name: "Upload storage", Provider: "Other", Endpoint: remote.URL, Region: "us-east-1", PathStyle: true, AccessMode: "writable", SecretRef: ref}
	if err := repo.SaveConnection(context.Background(), &c); err != nil {
		t.Fatal(err)
	}
	d := store.Directory{Owner: testOwner, ConnectionID: c.ID, Name: "Uploads", Bucket: "upload-test", EncryptionMode: mode, FilenameEncryption: "standard", FilenameEncoding: "base32", DirectoryEncryption: true, Suffix: ".bin"}
	if mode == "rclone_crypt" {
		d.CryptSecretRef = ref
	}
	if err := repo.SaveDirectory(context.Background(), &d); err != nil {
		t.Fatal(err)
	}
	factory := &rclonestorage.Factory{ProxyURL: proxy.URL, Policy: policy, MaxEntries: 1000}
	srv, err := New(config.Config{Origin: "http://files.localhost:8082", MaxUploads: 1, MaxUploadBytes: 8}, repo, secretStore, factory, policy)
	if err != nil {
		t.Fatal(err)
	}
	return srv, d
}

func TestHTTPMultiFileUpload(t *testing.T) {
	for _, mode := range []string{"none", "rclone_crypt"} {
		t.Run(mode, func(t *testing.T) {
			srv, d := uploadTestServer(t, mode)
			handler := srv.Handler()
			atLimit := make([]uploadTestFile, maxUploadFiles)
			for i := range atLimit {
				atLimit[i] = uploadTestFile{name: strconv.Itoa(i) + ".txt", data: "x"}
			}
			for _, tc := range []struct {
				name     string
				files    []uploadTestFile
				query    string
				status   int
				complete int
				hx       bool
				badTail  bool
			}{
				{name: "single", files: []uploadTestFile{{name: "one.txt", data: "12345678"}}, status: 303, complete: 1},
				{name: "multiple", files: []uploadTestFile{{name: "one + %.txt", data: "12345678"}, {name: "árvíz.txt", data: "abcdefgh"}, {name: "empty.txt"}}, status: 303, complete: 3},
				{name: "htmx", files: []uploadTestFile{{name: "one.txt", data: "12345678"}, {name: "two.txt", data: "abcdefgh"}}, status: 200, complete: 2, hx: true},
				{name: "duplicate", files: []uploadTestFile{{name: "same.txt", data: "old"}, {name: "same.txt", data: "new"}, {name: "later.txt"}}, status: 409, complete: 1},
				{name: "oversized first", files: []uploadTestFile{{name: "large.txt", data: "123456789"}, {name: "later.txt"}}, status: 413},
				{name: "oversized later", files: []uploadTestFile{{name: "one.txt", data: "12345678"}, {name: "large.txt", data: "123456789"}, {name: "later.txt"}}, status: 413, complete: 1, hx: true},
				{name: "traversal", files: []uploadTestFile{{name: "one.txt"}, {name: "../bad.txt"}, {name: "later.txt"}}, status: 422, complete: 1},
				{name: "backslash", files: []uploadTestFile{{name: "one.txt"}, {name: `folder\bad.txt`}}, status: 422, complete: 1},
				{name: "invalid field", files: []uploadTestFile{{name: "one.txt"}, {name: "bad.txt", field: "other"}}, status: 422, complete: 1},
				{name: "empty selection", status: 422},
				{name: "empty filename", files: []uploadTestFile{{}}, status: 422},
				{name: "malformed trailing part", files: []uploadTestFile{{name: "one.txt", data: "data"}}, status: 422, complete: 1, badTail: true},
				{name: "stale revision", files: []uploadTestFile{{name: "one.txt"}, {name: "two.txt"}}, query: "&revision=2", status: 409},
				{name: "file limit", files: atLimit, status: 303, complete: maxUploadFiles},
				{name: "too many files", files: append(append([]uploadTestFile{}, atLimit...), uploadTestFile{name: "later.txt"}), status: 422, complete: maxUploadFiles},
				{name: "replace single", files: []uploadTestFile{{name: "one.txt", data: "new"}}, query: "&replace=yes", status: 303, complete: 1},
				{name: "replace remains single", files: []uploadTestFile{{name: "one.txt", data: "new"}, {name: "two.txt"}}, query: "&replace=yes", status: 422, complete: 1},
			} {
				t.Run(tc.name, func(t *testing.T) {
					folder := "folder + % árvíz/" + tc.name
					var body bytes.Buffer
					writer := multipart.NewWriter(&body)
					for _, file := range tc.files {
						field := file.field
						if field == "" {
							field = "file"
						}
						part, err := writer.CreateFormFile(field, file.name)
						if err != nil {
							t.Fatal(err)
						}
						if _, err := io.WriteString(part, file.data); err != nil {
							t.Fatal(err)
						}
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
					bodyBytes := body.Bytes()
					if tc.badTail {
						tail := "--" + writer.Boundary() + "--\r\n"
						bodyBytes = append(bytes.TrimSuffix(bodyBytes, []byte(tail)), []byte("--"+writer.Boundary()+"\r\ninvalid header\r\n\r\n")...)
					}
					query := url.Values{"path": {folder}, "revision": {"1"}}
					if tc.query != "" {
						extra, err := url.ParseQuery(strings.TrimPrefix(tc.query, "&"))
						if err != nil {
							t.Fatal(err)
						}
						for key := range extra {
							query.Set(key, extra.Get(key))
						}
					}
					base := "/directories/" + d.ID
					r := httptest.NewRequest("POST", base+"/upload?"+query.Encode(), bytes.NewReader(bodyBytes))
					r.Header.Set("Content-Type", writer.FormDataContentType())
					r.Header.Set("Origin", srv.Config.Origin)
					r.Header.Set("X-Auth-Request-User", testOwner)
					if tc.hx {
						r.Header.Set("HX-Request", "true")
					}
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, r)
					if w.Code != tc.status {
						t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body.String())
					}
					if tc.name == "replace single" {
						// Repeat against the now-existing target to exercise replacement.
						r = httptest.NewRequest("POST", base+"/upload?"+query.Encode(), bytes.NewReader(bodyBytes))
						r.Header.Set("Content-Type", writer.FormDataContentType())
						r.Header.Set("Origin", srv.Config.Origin)
						r.Header.Set("X-Auth-Request-User", testOwner)
						w = httptest.NewRecorder()
						handler.ServeHTTP(w, r)
						if w.Code != 303 {
							t.Fatalf("replacement failed: status %d: %s", w.Code, w.Body.String())
						}
					}
					destination := base + "?path=" + url.QueryEscape(folder)
					if tc.status < 400 {
						if tc.hx {
							if !strings.Contains(w.Header().Get("HX-Location"), destination) {
								t.Fatal("HTMX did not return to the upload folder", w.Header())
							}
						} else if w.Header().Get("Location") != destination {
							t.Fatal("redirect lost the upload folder", w.Header())
						}
					} else if tc.complete > 0 {
						if !strings.Contains(w.Body.String(), "Files uploaded before this error: "+strconv.Itoa(tc.complete)) {
							t.Fatal("partial completion was not reported", w.Body.String())
						}
						found := false
						for _, link := range pageURLs(t, w.Body.String(), "a", "href") {
							found = found || link.String() == destination
						}
						if !found {
							t.Fatal("error lost the return link", w.Body.String())
						}
					}
					for i, file := range tc.files {
						if file.name == "" || strings.ContainsAny(file.name, "/\\") {
							continue
						}
						get := httptest.NewRequest("GET", base+"/download?path="+url.QueryEscape(folder+"/"+file.name), nil)
						get.Header.Set("X-Auth-Request-User", testOwner)
						result := httptest.NewRecorder()
						handler.ServeHTTP(result, get)
						if i < tc.complete {
							if result.Code != 200 || result.Body.String() != file.data {
								t.Fatalf("download %q: status %d body %q", file.name, result.Code, result.Body.String())
							}
						} else if tc.name == "duplicate" && i == 1 {
							if result.Code != 200 || result.Body.String() != "old" {
								t.Fatal("duplicate overwrote the completed file")
							}
						} else if tc.status == 413 && i == tc.complete {
							// An interrupted crypt stream may leave a partial object.
							if result.Code != 404 && (result.Code != 200 || int64(result.Body.Len()) > srv.Config.MaxUploadBytes) {
								t.Fatalf("oversized file was stored beyond its limit: status %d, bytes %d", result.Code, result.Body.Len())
							}
						} else if result.Code != 404 {
							t.Fatalf("incomplete file %q exists: status %d", file.name, result.Code)
						}
					}
				})
			}
		})
	}
}

type uploadPolicyRepo struct {
	readOnlyRepo
	locked bool
}

func (r *uploadPolicyRepo) Lock(context.Context, string) (func(), error) {
	r.locked = true
	return func() { r.locked = false }, nil
}

type uploadPolicyFS struct{ storage.FS }

func (uploadPolicyFS) PhysicalKey(key string) string { return key }

func TestUploadFileRechecksPolicy(t *testing.T) {
	d := store.Directory{ID: uuid.NewString(), Owner: testOwner, Revision: 1}
	c := store.Connection{ID: uuid.NewString(), AccessMode: "writable", Revision: 1}
	for _, change := range []string{"directory read-only", "connection read-only", "directory revision", "connection revision"} {
		t.Run(change, func(t *testing.T) {
			repo := &uploadPolicyRepo{readOnlyRepo: readOnlyRepo{d: d, c: c}}
			want := errReadOnly
			switch change {
			case "directory read-only":
				repo.d.ReadOnly = true
			case "connection read-only":
				repo.c.AccessMode = "read_only"
			case "directory revision":
				repo.d.Revision++
				want = store.ErrConflict
			case "connection revision":
				repo.c.Revision++
				want = store.ErrConflict
			}
			srv := &Server{Repo: repo}
			err := srv.uploadFile(httptest.NewRequest(http.MethodPost, "/", nil), d, c, uploadPolicyFS{}, "file.txt", strings.NewReader("data"), false)
			if !errors.Is(err, want) || repo.locked {
				t.Fatalf("error %v, want %v; lock held: %v", err, want, repo.locked)
			}
		})
	}
}
