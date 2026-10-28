package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
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

const testOwner = "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa"

type readOnlyRepo struct {
	store.Repository
	d store.Directory
	c store.Connection
}

func (r readOnlyRepo) Directory(context.Context, string, string) (store.Directory, error) {
	return r.d, nil
}
func (r readOnlyRepo) Connection(context.Context, string, string) (store.Connection, error) {
	return r.c, nil
}

type forbiddenFactory struct{ t *testing.T }

func (f forbiddenFactory) Open(context.Context, store.Connection, store.Directory, secrets.Value, secrets.Value) (storage.FS, error) {
	f.t.Error("storage opened before read-only enforcement")
	return nil, errors.New("forbidden")
}
func TestReadOnlyBlocksEveryMutation(t *testing.T) {
	for _, mode := range []string{"none", "rclone_crypt"} {
		for _, access := range []string{"read_only", "directory_read_only"} {
			t.Run(mode+"/"+access, func(t *testing.T) {
				repo := readOnlyRepo{d: store.Directory{Owner: testOwner, EncryptionMode: mode, ReadOnly: access == "directory_read_only"}, c: store.Connection{AccessMode: "read_only"}}
				if access == "directory_read_only" {
					repo.c.AccessMode = "writable"
				}
				policy, _ := rclonestorage.NewPolicy("", false)
				server, e := New(config.Config{Origin: "http://files.localhost:8082", MaxUploads: 1}, repo, nil, forbiddenFactory{t}, policy)
				if e != nil {
					t.Fatal(e)
				}
				for _, action := range []string{"upload", "mkdir", "delete-file", "rmdir"} {
					request := httptest.NewRequest("POST", "/directories/"+uuid.NewString()+"/"+action, strings.NewReader("path=file&confirm=on"))
					request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					request.Header.Set("Origin", server.Config.Origin)
					request.Header.Set("X-Auth-Request-User", testOwner)
					rec := httptest.NewRecorder()
					server.Handler().ServeHTTP(rec, request)
					if rec.Code != 403 {
						t.Fatalf("%s status %d: %s", action, rec.Code, rec.Body.String())
					}
				}
			})
		}
	}
}
func TestRangeParsing(t *testing.T) {
	for _, test := range []struct {
		value            string
		size, start, end int64
		valid            bool
	}{{"bytes=2-4", 10, 2, 4, true}, {"bytes=-3", 10, 7, 9, true}, {"bytes=3-", 10, 3, 9, true}, {"bytes=0-999", 10, 0, 9, true}, {"bytes=10-", 10, 0, 0, false}, {"bytes=0-1,4-5", 10, 0, 0, false}, {"bytes=0-0", 0, 0, 0, false}} {
		start, end, _, e := parseRange(test.value, test.size)
		if (e == nil) != test.valid || (e == nil && (start != test.start || end != test.end)) {
			t.Fatalf("%s: %d-%d error %v", test.value, start, end, e)
		}
	}
}

func TestHTTPStorageWorkflow(t *testing.T) {
	repo := &store.Store{DB: testutil.Database(t)}
	remote, _ := testutil.S3(t, "files-test")
	defer remote.Close()
	u, _ := url.Parse(remote.URL)
	policy, _ := rclonestorage.NewPolicy(u.Host, true)
	proxy, e := rclonestorage.StartEgress(policy)
	if e != nil {
		t.Fatal(e)
	}
	defer proxy.Close()
	secretStore, e := secrets.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer secretStore.Close()
	factory := &rclonestorage.Factory{ProxyURL: proxy.URL, Policy: policy, MaxEntries: 100}
	cfg := config.Config{Origin: "http://files.localhost:8082", MaxUploads: 2, MaxUploadBytes: 1 << 20}
	srv, e := New(cfg, repo, secretStore, factory, policy)
	if e != nil {
		t.Fatal(e)
	}
	handler := srv.Handler()
	request := func(method, target string, values url.Values, owner string) *httptest.ResponseRecorder {
		var body io.Reader
		if values != nil {
			body = strings.NewReader(values.Encode())
		}
		req := httptest.NewRequest(method, target, body)
		req.Header.Set("X-Auth-Request-User", owner)
		req.Header.Set("X-Auth-Request-Name", "Alice <script>")
		req.Header.Set("Origin", cfg.Origin)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	values := url.Values{"name": {"Test storage"}, "provider": {"Other"}, "endpoint": {remote.URL}, "region": {"us-east-1"}, "access_key": {"dummy"}, "secret_key": {"dummy-secret"}, "access_mode": {"writable"}, "path_style": {"on"}}
	if rec := request("POST", "/connections", values, testOwner); rec.Code != 303 {
		t.Fatalf("create connection: %d %s", rec.Code, rec.Body.String())
	}
	connections, e := repo.Connections(context.Background(), testOwner)
	if e != nil || len(connections) != 1 {
		t.Fatal(e)
	}
	connection := connections[0]
	for _, mode := range []string{"none", "rclone_crypt"} {
		t.Run(mode, func(t *testing.T) {
			values := url.Values{"name": {mode}, "connection_id": {connection.ID}, "bucket": {"files-test"}, "prefix": {mode}, "encryption_mode": {mode}, "filename_encryption": {"standard"}, "directory_encryption": {"on"}, "filename_encoding": {"base32"}, "suffix": {".bin"}, "crypt_password": {"test crypt password"}, "new_crypt_prefix": {"on"}}
			if rec := request("POST", "/directories", values, testOwner); rec.Code != 303 {
				t.Fatalf("create directory: %d %s", rec.Code, rec.Body.String())
			}
			dirs, e := repo.Directories(context.Background(), testOwner)
			if e != nil {
				t.Fatal(e)
			}
			var d store.Directory
			for _, entry := range dirs {
				if entry.Name == mode {
					d = entry
				}
			}
			base := "/directories/" + d.ID
			if rec := request("GET", base, nil, testOwner); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Upload file") {
				t.Fatalf("browse status %d: %s", rec.Code, rec.Body.String())
			}
			if rec := request("GET", base, nil, uuid.NewString()); rec.Code != 404 {
				t.Fatal("cross-user browsing accepted")
			}
			for _, action := range []string{"upload", "mkdir", "delete-file", "rmdir", "update", "remove", "test"} {
				if rec := request("POST", base+"/"+action, url.Values{}, uuid.NewString()); rec.Code != 404 {
					t.Fatalf("cross-owner %s returned %d", action, rec.Code)
				}
			}
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			file, _ := writer.CreateFormFile("file", "hello + %.txt")
			content := bytes.Repeat([]byte("private upload data\n"), 5000)
			file.Write(content)
			writer.Close()
			upload := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest("POST", base+"/upload?revision="+strconv.FormatInt(d.Revision, 10), bytes.NewReader(body.Bytes()))
				req.Header.Set("Content-Type", writer.FormDataContentType())
				req.Header.Set("Origin", cfg.Origin)
				req.Header.Set("X-Auth-Request-User", testOwner)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				return rec
			}
			if rec := upload(); rec.Code != 303 {
				t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
			}
			if rec := upload(); rec.Code != 409 {
				t.Fatalf("unconfirmed overwrite: %d", rec.Code)
			}
			rec := request("GET", base+"/download?path="+url.QueryEscape("hello + %.txt"), nil, testOwner)
			if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), content) {
				t.Fatalf("download %d length %d", rec.Code, rec.Body.Len())
			}
			rangeRequest := httptest.NewRequest("GET", base+"/download?path="+url.QueryEscape("hello + %.txt"), nil)
			rangeRequest.Header.Set("X-Auth-Request-User", testOwner)
			rangeRequest.Header.Set("Range", "bytes=65000-66000")
			ranged := httptest.NewRecorder()
			handler.ServeHTTP(ranged, rangeRequest)
			if ranged.Code != 206 || ranged.Header().Get("Content-Range") != "bytes 65000-66000/"+strconv.Itoa(len(content)) || !bytes.Equal(ranged.Body.Bytes(), content[65000:66001]) {
				t.Fatalf("HTTP range %d: %s", ranged.Code, ranged.Header().Get("Content-Range"))
			}
			var large bytes.Buffer
			largeWriter := multipart.NewWriter(&large)
			largeFile, _ := largeWriter.CreateFormFile("file", "too-large.bin")
			largeFile.Write(bytes.Repeat([]byte("x"), int(cfg.MaxUploadBytes)+1))
			largeWriter.Close()
			largeRequest := httptest.NewRequest("POST", base+"/upload?revision=1", &large)
			largeRequest.Header.Set("Content-Type", largeWriter.FormDataContentType())
			largeRequest.Header.Set("Origin", cfg.Origin)
			largeRequest.Header.Set("X-Auth-Request-User", testOwner)
			largeResponse := httptest.NewRecorder()
			handler.ServeHTTP(largeResponse, largeRequest)
			if largeResponse.Code != 413 {
				t.Fatalf("oversized upload status %d", largeResponse.Code)
			}
			if rec = request("GET", base, nil, testOwner); !strings.Contains(rec.Body.String(), "Alice &lt;script&gt;") {
				t.Fatal("display name not escaped")
			}
			if rec = request("POST", base+"/mkdir", url.Values{"path": {"folder"}, "revision": {"1"}}, testOwner); rec.Code != 303 {
				t.Fatalf("mkdir %d %s", rec.Code, rec.Body.String())
			}
			if rec = request("POST", base+"/delete-file", url.Values{"path": {"hello + %.txt"}, "revision": {"1"}, "confirm": {"on"}}, testOwner); rec.Code != 303 {
				t.Fatalf("delete %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestOriginAndIdentityAreRequired(t *testing.T) {
	policy, _ := rclonestorage.NewPolicy("", false)
	srv, e := New(config.Config{Origin: "http://127.0.0.2:8082", MaxUploads: 1}, nil, nil, nil, policy)
	if e != nil {
		t.Fatal(e)
	}
	for _, test := range []struct {
		owner, origin string
		status        int
	}{{"", "", 401}, {testOwner, "", 403}, {testOwner, "http://attacker.example", 403}} {
		r := httptest.NewRequest("POST", "/connections", strings.NewReader(""))
		if test.owner != "" {
			r.Header.Set("X-Auth-Request-User", test.owner)
		}
		if test.origin != "" {
			r.Header.Set("Origin", test.origin)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, r)
		if rec.Code != test.status {
			t.Fatalf("got %d want %d", rec.Code, test.status)
		}
	}
	r := httptest.NewRequest("POST", "/connections", strings.NewReader("field="+strings.Repeat("x", 65537)))
	r.Header.Set("X-Auth-Request-User", testOwner)
	r.Header.Set("Origin", srv.Config.Origin)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	if rec.Code != 422 {
		t.Fatalf("oversized settings form returned %d", rec.Code)
	}
}
