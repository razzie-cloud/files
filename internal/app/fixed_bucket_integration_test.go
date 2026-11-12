package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/secrets"
	rclonestorage "github.com/razzie-cloud/files/internal/storage/rclone"
	"github.com/razzie-cloud/files/internal/store"
	"github.com/razzie-cloud/files/internal/testutil"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestFixedBucketPolicyAndConnectionDefaults(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint("shared=", shared), func(t *testing.T) {
			const origin = "http://example.com"
			const admin = "11111111-1111-4111-8111-111111111111"
			const member = "22222222-2222-4222-8222-222222222222"
			ctx := context.Background()
			gateway, sign := sharedGateway(t, origin)
			repo := &store.Store{DB: testutil.Database(t)}
			remote, _ := testutil.S3(t, "test")
			defer remote.Close()
			u, _ := url.Parse(remote.URL)
			policy, err := rclonestorage.NewPolicy(u.Host, true)
			if err != nil {
				t.Fatal(err)
			}
			proxy, err := rclonestorage.StartEgress(policy)
			if err != nil {
				t.Fatal(err)
			}
			defer proxy.Close()
			factory := &rclonestorage.Factory{ProxyURL: proxy.URL, Policy: policy, MaxEntries: 100}
			cfg := config.Config{Origin: origin, AccessGatewayURL: gateway, MaxUploads: 1}
			a, err := New(cfg, repo, secrets.New(repo.DB), factory, policy)
			endpoint := remote.URL
			if err != nil {
				t.Fatal(err)
			}
			handler := a.Handler()
			request := func(method, path, actor string, isAdmin bool, values url.Values) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
				r.Header.Set("Origin", origin)
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				sign(r, actor, isAdmin)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			actor := member
			if shared {
				actor = admin
			}
			out := request("GET", "/connections/new", actor, shared, nil)
			field := regexp.MustCompile(`<input[^>]*name="path_style"[^>]*>`).FindString(out.Body.String())
			if out.Code != 200 || field == "" || strings.Contains(field, "checked") {
				t.Fatal("new connections must default path style off", out.Code, field)
			}
			values := url.Values{"name": {"Fixed storage"}, "provider": {"Other"}, "endpoint": {endpoint}, "region": {"us-east-1"}, "access_key": {"test"}, "secret_key": {"test-secret"}, "bucket": {"test"}, "access_mode": {"writable"}, "conditional_writes": {"on"}}
			if shared {
				values.Set("shared", "on")
			}
			if out := request("POST", "/connections", actor, shared, values); out.Code != 303 {
				t.Fatal("connection create", out.Code, out.Body.String())
			}
			all, err := repo.Connections(ctx, member)
			if err != nil || len(all) != 1 {
				t.Fatal(all, err)
			}
			c := all[0]
			if c.Bucket != "test" || c.PathStyle {
				t.Fatal("bucket/addressing not persisted", c)
			}
			out = request("GET", "/connections", member, false, nil)
			if !strings.Contains(out.Body.String(), ">Use connection</a>") {
				t.Fatal("missing Use connection for personal/shared connection")
			}
			out = request("GET", "/directories/new?connection="+c.ID, member, false, nil)
			bucketInput := regexp.MustCompile(`<input[^>]*name="bucket"[^>]*>`).FindString(out.Body.String())
			if out.Code != 200 || !strings.Contains(bucketInput, `value="test"`) || !strings.Contains(bucketInput, "readonly") {
				t.Fatal("fixed bucket not prefilled/read-only", out.Code, bucketInput)
			}
			// The local S3 fixture needs explicit path-style addressing; existing saved
			// choices must stay checked while new connections default off.
			values.Set("revision", fmt.Sprint(c.Revision))
			values.Set("path_style", "on")
			update := "/connections/" + c.ID + "/update"
			if out := request("POST", update, actor, shared, values); out.Code != 303 {
				t.Fatal("path-style opt-in", out.Code, out.Body.String())
			}
			c, err = repo.Connection(ctx, member, c.ID)
			if err != nil || !c.PathStyle {
				t.Fatal(c, err)
			}
			form := url.Values{"name": {"Fixed area"}, "connection_id": {c.ID}, "prefix": {"projects"}, "encryption_mode": {"none"}, "bucket": {"other-bucket"}}
			if out := request("POST", "/directories", member, false, form); out.Code != 422 {
				t.Fatal("forged bucket accepted", out.Code, out.Body.String())
			}
			// An omitted bucket resolves to the configured bucket on the server too.
			form.Del("bucket")
			if out := request("POST", "/directories", member, false, form); out.Code != 303 {
				t.Fatal("fixed mapping create", out.Code, out.Body.String())
			}
			allMaps, err := repo.Directories(ctx, member)
			if err != nil || len(allMaps) != 1 || allMaps[0].Bucket != "test" {
				t.Fatal("fixed bucket not saved", allMaps, err)
			}
			mapping := allMaps[0]
			forged := mapping
			forged.ID = ""
			forged.Bucket = "other-bucket"
			forged.Name = "Forged repository"
			if err := repo.SaveDirectory(ctx, &forged); !errors.Is(err, store.ErrBucket) {
				t.Fatal("repository bucket bypass", err)
			}
			if _, err := repo.DB.ExecContext(ctx, `UPDATE directories SET bucket='other-bucket' WHERE id=$1`, mapping.ID); err == nil {
				t.Fatal("SQL bucket bypass")
			}
			if out := request("POST", "/connections/"+c.ID+"/test", member, false, url.Values{"bucket": {"other-bucket"}}); out.Code != 422 {
				t.Fatal("listing bucket bypass", out.Code, out.Body.String())
			}
			if out := request("POST", "/connections/"+c.ID+"/test", member, false, nil); out.Code != 200 {
				t.Fatal("fixed-bucket listing", out.Code, out.Body.String())
			}
			values.Set("revision", fmt.Sprint(c.Revision))
			values.Set("bucket", "other-bucket")
			if out := request("POST", update, actor, shared, values); out.Code != 409 {
				t.Fatal("incompatible restriction changed", out.Code, out.Body.String())
			}
			values.Set("bucket", "")
			if out := request("POST", update, actor, shared, values); out.Code != 303 {
				t.Fatal("clear restriction", out.Code, out.Body.String())
			}
			c, err = repo.Connection(ctx, member, c.ID)
			if err != nil || c.Bucket != "" {
				t.Fatal(c, err)
			}
			values.Set("revision", fmt.Sprint(c.Revision))
			values.Set("bucket", "test")
			if out := request("POST", update, actor, shared, values); out.Code != 303 {
				t.Fatal("compatible existing restriction", out.Code, out.Body.String())
			}
		})
	}
}
