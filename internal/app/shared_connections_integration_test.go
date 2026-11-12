package app

import (
	"context"
	"errors"
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

func TestSharedConnectionAuthorizationAndMappings(t *testing.T) {
	const origin = "http://example.com"
	const creator = "11111111-1111-4111-8111-111111111111"
	const member = "22222222-2222-4222-8222-222222222222"
	const another = "33333333-3333-4333-8333-333333333333"
	ctx := context.Background()
	gateway, sign := sharedGateway(t, origin)
	repo := &store.Store{DB: testutil.Database(t)}
	secretStore := secrets.New(repo.DB)
	policy, err := rclonestorage.NewPolicy("", false)
	if err != nil {
		t.Fatal(err)
	}
	app, err := New(config.Config{Origin: origin, AccessGatewayURL: gateway}, repo, secretStore, nil, policy)
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	request := func(method, path, actor string, admin bool, values url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		// Header spoofing never grants authority when the signed claim is false.
		r.Header.Set("X-Auth-Request-Admin", "true")
		sign(r, actor, admin)
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, r)
		return out
	}
	values := url.Values{"name": {"Shared storage"}, "provider": {"AWS"}, "region": {"us-east-1"}, "path_style": {"on"}, "access_mode": {"writable"}, "conditional_writes": {"on"}, "access_key": {"shared-access-value"}, "secret_key": {"shared-secret-value"}, "shared": {"on"}, "user_id_prefix": {"on"}}
	if out := request("POST", "/connections", member, false, values); out.Code != 403 {
		t.Fatal("member created shared connection", out.Code, out.Body.String())
	}
	// Personal connections remain owner-managed, without prefix controls.
	personalValues := url.Values{"name": {"Personal storage"}, "provider": {"AWS"}, "region": {"us-east-1"}, "access_mode": {"writable"}, "conditional_writes": {"on"}, "access_key": {"personal-access"}, "secret_key": {"personal-secret"}}
	if out := request("POST", "/connections", member, false, personalValues); out.Code != 303 {
		t.Fatal("personal create", out.Code, out.Body.String())
	}
	personalList, err := repo.Connections(ctx, member)
	if err != nil || len(personalList) != 1 {
		t.Fatal(personalList, err)
	}
	personal := personalList[0]
	if out := request("POST", "/connections", creator, true, values); out.Code != 303 {
		t.Fatal("shared create", out.Code, out.Body.String())
	}
	all, err := repo.Connections(ctx, member)
	if err != nil || len(all) != 2 {
		t.Fatal("available connections", all, err)
	}
	c := all[0]
	if !c.Shared || !c.UserIDPrefix || c.Owner != creator {
		t.Fatal("sharing fields", c)
	}
	editURL := "/connections/" + c.ID + "/edit"
	updateURL := "/connections/" + c.ID + "/update"
	removeURL := "/connections/" + c.ID + "/remove"
	for _, actor := range []string{member, creator} {
		for _, target := range []struct{ method, path string }{{"GET", editURL}, {"POST", updateURL}, {"POST", removeURL}} {
			out := request(target.method, target.path, actor, false, values)
			if out.Code != 403 {
				t.Fatalf("nonadmin %s %s: %d %s", actor, target.path, out.Code, out.Body.String())
			}
		}
	}
	out := request("GET", "/connections", member, false, nil)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "Managed by your administrator") {
		t.Fatal("member view", out.Code, out.Body.String())
	}
	for _, secret := range []string{"shared-access-value", "shared-secret-value", "personal-secret"} {
		if strings.Contains(out.Body.String(), secret) {
			t.Fatal("rendered secret")
		}
	}
	if out := request("GET", editURL, another, true, nil); out.Code != 200 || !strings.Contains(out.Body.String(), `id="connection-shared"`) {
		t.Fatal("other admin edit", out.Code, out.Body.String())
	}
	values.Set("revision", fmt.Sprint(c.Revision))
	values.Set("name", "Renamed shared")
	values.Set("access_key", "")
	values.Set("secret_key", "")
	values.Del("shared") // Disabled creation-only field is absent from native forms.
	if out := request("POST", updateURL, another, true, values); out.Code != 303 {
		t.Fatal("admin update", out.Code, out.Body.String())
	}
	c, err = repo.Connection(ctx, member, c.ID)
	if err != nil || c.Owner != creator || !c.Shared {
		t.Fatal(c, err)
	}
	keys, err := secretStore.Get(ctx, c.Owner, c.SecretRef)
	if err != nil || keys.SecretKey != "shared-secret-value" {
		t.Fatal("shared credentials did not remain owned by creator", err)
	}
	values.Set("revision", fmt.Sprint(c.Revision))
	values.Set("shared", "")
	if out := request("POST", updateURL, creator, true, values); out.Code != 409 {
		t.Fatal("shared status changed", out.Code, out.Body.String())
	}
	values.Del("shared")
	personal.Shared = true
	if err := repo.SaveConnection(store.WithAdmin(ctx, true), &personal); !errors.Is(err, store.ErrConflict) {
		t.Fatal("personal became shared", err)
	}
	if err := repo.SaveConnection(ctx, &c); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("nonadmin repository edit", err)
	}
	first := store.Directory{Owner: member, ConnectionID: c.ID, ConnectionRevision: c.Revision, Name: "Projects", Bucket: "bucket", Prefix: "projects", EncryptionMode: "none"}
	if err := repo.SaveDirectory(ctx, &first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID = ""
	second.Owner = another
	if err := repo.SaveDirectory(ctx, &second); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Directory(ctx, another, first.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("mapping scope", err)
	}
	share := store.Share{Owner: member, DirectoryID: first.ID, IsDirectory: true}
	if err := repo.CreateShare(ctx, &share); err != nil {
		t.Fatal(err)
	}
	snapshot, err := app.loadShare(ctx, share.ID)
	if err != nil || snapshot.Connection.Owner != creator || snapshot.Directory.Owner != member || snapshot.Credentials.SecretKey != "shared-secret-value" {
		t.Fatal("public share failed to resolve shared credentials", err)
	}
	inUse, err := repo.Connection(ctx, creator, c.ID)
	if err != nil || !inUse.InUse {
		t.Fatal("cross-owner attachment not detected", err)
	}
	out = request("GET", editURL, creator, true, nil)
	if out.Code != 200 || !strings.Contains(out.Body.String(), `data-prefix-locked="true"`) {
		t.Fatal("prefix not locked in form", out.Code, out.Body.String())
	}
	values.Del("user_id_prefix")
	if out := request("POST", updateURL, creator, true, values); out.Code != 409 {
		t.Fatal("redirected existing storage", out.Code, out.Body.String())
	}
	inUse.UserIDPrefix = false
	if err := repo.SaveConnection(store.WithAdmin(ctx, true), &inUse); !errors.Is(err, store.ErrConflict) {
		t.Fatal("redirected existing storage through repository", err)
	}
	if err := repo.RemoveConnection(store.WithAdmin(ctx, true), another, c.ID, c.Revision); !errors.Is(err, store.ErrConflict) {
		t.Fatal("removed connection in use", err)
	}
	if err := repo.RemoveDirectory(ctx, member, first.ID, first.Revision); err != nil {
		t.Fatal(err)
	}
	if err := repo.RemoveDirectory(ctx, another, second.ID, second.Revision); err != nil {
		t.Fatal(err)
	}
	if out := request("POST", removeURL, another, true, url.Values{"revision": {fmt.Sprint(c.Revision)}, "confirm": {"on"}}); out.Code != 303 {
		t.Fatal("remove unused shared", out.Code, out.Body.String())
	}
}
