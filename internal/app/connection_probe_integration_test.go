package app

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/secrets"
	rclonestorage "github.com/razzie-cloud/files/internal/storage/rclone"
	"github.com/razzie-cloud/files/internal/store"
	"github.com/razzie-cloud/files/internal/testutil"
)

func TestConnectionListingWithEmptyUserPrefix(t *testing.T) {
	const creator = "11111111-1111-4111-8111-111111111111"
	const member = "22222222-2222-4222-8222-222222222222"
	ctx := context.Background()
	repo := &store.Store{DB: testutil.Database(t)}
	secretStore := secrets.New(repo.DB)
	remote, _ := testutil.S3(t, "bucket")
	endpoint, _ := url.Parse(remote.URL)
	policy, err := rclonestorage.NewPolicy(endpoint.Host, true)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := rclonestorage.StartEgress(policy)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	factory := &rclonestorage.Factory{Policy: policy, ProxyURL: proxy.URL, MaxEntries: 100}
	server, err := New(config.Config{Origin: "http://example.com", MaxUploads: 1}, repo, secretStore, factory, policy)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := secretStore.Put(ctx, creator, secrets.Value{AccessKey: "test", SecretKey: "test-secret"})
	if err != nil {
		t.Fatal(err)
	}
	c := store.Connection{Owner: creator, Name: "Shared", Provider: "Other", Endpoint: remote.URL, Region: "us-east-1", PathStyle: true, AccessMode: "writable", SecretRef: ref, Shared: true, UserIDPrefix: true, Bucket: "bucket"}
	if err := repo.SaveConnection(store.WithAdmin(ctx, true), &c); err != nil {
		t.Fatal(err)
	}
	d := store.Directory{Owner: member, ConnectionID: c.ID, Name: "Shared", Bucket: "bucket", EncryptionMode: "none"}
	if err := repo.SaveDirectory(ctx, &d); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, values url.Values, htmx bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
		r.Header.Set("X-Auth-Request-User", member)
		r.Header.Set("Origin", "http://example.com")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if htmx {
			r.Header.Set("HX-Request", "true")
			r.Header.Set("HX-Target", "main")
		}
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/directories/"+d.ID, nil, false); w.Code != 200 {
		t.Fatal("empty shared directory browse", w.Code, w.Body.String())
	}

	for _, prefix := range []string{"", "not-created-yet"} {
		for _, htmx := range []bool{false, true} {
			t.Run(fmt.Sprintf("prefix=%q/htmx=%v", prefix, htmx), func(t *testing.T) {
				w := request("POST", "/connections/"+c.ID+"/test", url.Values{"prefix": {prefix}}, htmx)
				if w.Code != 200 || !strings.Contains(w.Body.String(), "Listing succeeded") {
					t.Fatal("listing permission test rejected an empty user prefix", w.Code, w.Body.String())
				}
				if htmx && strings.Contains(w.Body.String(), "<!doctype") {
					t.Fatal("HTMX received an entire page")
				}
			})
		}
	}
	if w := request("POST", "/connections/"+c.ID+"/test", url.Values{"bucket": {"another-bucket"}}, true); w.Code != 422 {
		t.Fatal("fixed bucket override", w.Code, w.Body.String())
	}
	// Reading existing objects still uses the same owner-prefixed physical root.
	keys, err := secretStore.Get(ctx, creator, c.SecretRef)
	if err != nil {
		t.Fatal(err)
	}
	fs, err := factory.Open(ctx, c, d, keys, secrets.Value{})
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.Put(ctx, "example.txt", strings.NewReader("fixture file")); err != nil {
		t.Fatal(err)
	}
	fs.Close()
	if w := request("GET", "/directories/"+d.ID, nil, false); w.Code != 200 || !strings.Contains(w.Body.String(), "example.txt") {
		t.Fatal("populated directory browse", w.Code, w.Body.String())
	}
	if w := request("POST", "/connections/"+c.ID+"/test", nil, true); w.Code != 200 {
		t.Fatal("populated prefix probe", w.Code, w.Body.String())
	}
	missing := c
	missing.ID = ""
	missing.Name = "Unrestricted"
	missing.Bucket = ""
	if err := repo.SaveConnection(store.WithAdmin(ctx, true), &missing); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/connections/"+missing.ID+"/test", url.Values{"bucket": {"missing-bucket"}}, true); w.Code != 404 {
		t.Fatal("missing bucket should fail", w.Code, w.Body.String())
	}
}
