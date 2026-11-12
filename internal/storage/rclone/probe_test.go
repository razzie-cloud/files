package rclone

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/razzie-cloud/files/internal/devs3"
	"github.com/razzie-cloud/files/internal/secrets"
	"github.com/razzie-cloud/files/internal/storage"
	"github.com/razzie-cloud/files/internal/store"
)

func TestListingProbeChecksAccessWithoutDirectoryMarkers(t *testing.T) {
	ctx := context.Background()
	backend, _, err := devs3.New("bucket", nil)
	if err != nil {
		t.Fatal(err)
	}
	type request struct{ method, path, prefix, listType string }
	var mu sync.Mutex
	var requests []request
	var deny atomic.Bool
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, request{r.Method, r.URL.Path, r.URL.Query().Get("prefix"), r.URL.Query().Get("list-type")})
		mu.Unlock()
		if deny.Load() {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, "<Error><Code>AccessDenied</Code></Error>")
			return
		}
		backend.ServeHTTP(w, r)
	}))
	defer remote.Close()
	endpoint, _ := url.Parse(remote.URL)
	policy, err := NewPolicy(endpoint.Host, true)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := StartEgress(policy)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	factory := &Factory{Policy: policy, ProxyURL: proxy.URL, MaxEntries: 100}
	credentials := secrets.Value{AccessKey: "test", SecretKey: "test-secret"}
	reset := func() { mu.Lock(); requests = nil; mu.Unlock() }
	assertListing := func(t *testing.T, bucket, prefix string) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if len(requests) != 1 {
			t.Fatalf("probe should only list, got %+v", requests)
		}
		r := requests[0]
		if r.method != "GET" || r.path != "/"+bucket || r.prefix != prefix || (r.listType != "" && r.listType != "2") {
			t.Fatalf("probe changed scope or issued a non-list request: %+v", r)
		}
	}
	const owner = "22222222-2222-4222-8222-222222222222"
	for _, mode := range []string{"personal", "shared", "shared-user-prefix"} {
		t.Run(mode, func(t *testing.T) {
			c := store.Connection{Provider: "Other", Endpoint: remote.URL, Region: "us-east-1", PathStyle: true, Shared: mode != "personal", UserIDPrefix: mode == "shared-user-prefix", Bucket: "bucket"}
			d := store.Directory{Owner: owner, Bucket: "bucket", Prefix: mode, EncryptionMode: "none"}
			physical := c.StoragePrefix(owner, d.Prefix) + "/"
			reset()
			if err := factory.ProbeListing(ctx, c, d, credentials); err != nil {
				t.Fatal("empty prefix has valid listing access", err)
			}
			assertListing(t, "bucket", physical)
			// The probe must not create a marker or change normal directory semantics.
			fs, err := factory.Open(ctx, c, d, credentials, secrets.Value{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fs.List(ctx, ""); !errors.Is(err, storage.ErrNotFound) {
				t.Fatal("probe initialized the prefix or changed normal directory listing", err)
			}
			if err := fs.Put(ctx, "present.txt", strings.NewReader("fixture data")); err != nil {
				t.Fatal(err)
			}
			fs.Close()
			reset()
			if err := factory.ProbeListing(ctx, c, d, credentials); err != nil {
				t.Fatal("populated prefix", err)
			}
			assertListing(t, "bucket", physical)
			deny.Store(true)
			reset()
			err = factory.ProbeListing(ctx, c, d, credentials)
			deny.Store(false)
			if err == nil {
				t.Fatal("denied access reported success")
			}
			assertListing(t, "bucket", physical)
			reset()
			other := d
			other.Bucket = "another-bucket"
			if err := factory.ProbeListing(ctx, c, other, credentials); !errors.Is(err, store.ErrBucket) {
				t.Fatal("fixed bucket bypass", err)
			}
			mu.Lock()
			count := len(requests)
			mu.Unlock()
			if count != 0 {
				t.Fatal("fixed bucket rejection must precede S3 access")
			}
			c.Bucket = ""
			if err := factory.ProbeListing(ctx, c, other, credentials); !errors.Is(err, storage.ErrNotFound) {
				t.Fatal("missing bucket reported success", err)
			}
			assertListing(t, "another-bucket", physical)
		})
	}
}
