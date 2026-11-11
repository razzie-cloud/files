package app

import (
	"context"
	"fmt"
	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/store"
	"github.com/razzie-cloud/files/internal/testutil"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/razzie-cloud/files/internal/secrets"
	rclonestorage "github.com/razzie-cloud/files/internal/storage/rclone"
)

func TestConnectionDialogWorkflow(t *testing.T) {
	database := testutil.Database(t)
	repo := &store.Store{DB: database}
	secretStore := secrets.New(database)
	policy, err := rclonestorage.NewPolicy("", false)
	if err != nil {
		t.Fatal(err)
	}
	app, err := New(config.Config{Origin: "http://example.com"}, repo, secretStore, nil, policy)
	if err != nil {
		t.Fatal(err)
	}

	handler := app.Handler()
	owner := "11111111-1111-4111-8111-111111111111"
	other := "22222222-2222-4222-8222-222222222222"
	request := func(method, target, user, hxTarget string, values url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(values.Encode()))
		req.Header.Set("X-Auth-Request-User", user)
		req.Header.Set("Origin", "http://example.com")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if hxTarget != "" {
			req.Header.Set("HX-Request", "true")
			req.Header.Set("HX-Target", hxTarget)
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	out := request("GET", "/connections", owner, "", nil)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "data-connection-form") {
		t.Fatalf("first use: %d %s", out.Code, out.Body.String())
	}
	values := url.Values{"name": {"Browser storage"}, "provider": {"AWS"}, "region": {"us-east-1"}, "path_style": {"on"}, "conditional_writes": {"on"}, "access_mode": {"writable"}, "access_key": {"stored-access"}, "secret_key": {"stored-secret"}, "session_token": {"stored-token"}}
	out = request("POST", "/connections", owner, "connection-feedback", values)
	if out.Code != 200 || !strings.Contains(out.Header().Get("HX-Location"), "/connections") {
		t.Fatalf("create: %d %s", out.Code, out.Body.String())
	}
	all, err := repo.Connections(context.Background(), owner)
	if err != nil || len(all) != 1 {
		t.Fatal(all, err)
	}
	c := all[0]
	editURL := "/connections/" + c.ID + "/edit"
	updateURL := "/connections/" + c.ID + "/update"
	out = request("GET", "/connections", owner, "", nil)
	if strings.Contains(out.Body.String(), "data-connection-form") || !strings.Contains(out.Body.String(), `id="add-connection"`) {
		t.Fatal("populated list still shows the form")
	}
	out = request("GET", editURL, owner, "connection-dialog-content", nil)
	if out.Code != 200 || strings.Contains(out.Body.String(), "<html") || !strings.Contains(out.Body.String(), `value="Browser storage"`) || !strings.Contains(out.Body.String(), `data-close-connection`) {
		t.Fatalf("edit: %d %s", out.Code, out.Body.String())
	}
	for _, secret := range []string{"stored-access", "stored-secret", "stored-token"} {
		if strings.Contains(out.Body.String(), secret) {
			t.Fatal("saved credential leaked")
		}
	}
	out = request("GET", editURL, other, "connection-dialog-content", nil)
	if out.Code != 404 || out.Header().Get("HX-Retarget") != "#connection-list-feedback" || strings.Contains(out.Body.String(), "Browser storage") {
		t.Fatalf("owner scope: %d %s", out.Code, out.Body.String())
	}
	values.Set("revision", fmt.Sprint(c.Revision))
	values.Set("name", "")
	values.Set("secret_key", "submitted-secret-sentinel")
	out = request("POST", updateURL, owner, "connection-feedback", values)
	if out.Code != 422 || out.Header().Get("HX-Retarget") != "#connection-feedback" || strings.Contains(out.Body.String(), "submitted-secret-sentinel") || strings.Contains(out.Body.String(), "<form") {
		t.Fatalf("validation: %d %s", out.Code, out.Body.String())
	}
	values.Set("name", "Renamed storage")
	values.Set("access_key", "")
	values.Set("secret_key", "")
	values.Set("session_token", "")
	out = request("POST", updateURL, owner, "connection-feedback", values)
	if out.Code != 200 || out.Header().Get("HX-Location") == "" {
		t.Fatalf("update: %d %s", out.Code, out.Body.String())
	}
	updated, err := repo.Connection(context.Background(), owner, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := secretStore.Get(context.Background(), owner, updated.SecretRef)
	if err != nil || credentials.AccessKey != "stored-access" || credentials.SecretKey != "stored-secret" || credentials.SessionToken != "stored-token" {
		t.Fatal("blank credentials were not retained", err)
	}
	out = request("POST", updateURL, owner, "connection-feedback", values)
	if out.Code != 409 || out.Header().Get("HX-Retarget") != "#connection-feedback" {
		t.Fatalf("stale revision: %d %s", out.Code, out.Body.String())
	}
	out = request("GET", editURL, owner, "", nil)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "<html") || !strings.Contains(out.Body.String(), "data-connection-form") {
		t.Fatal("full-page edit fallback missing")
	}

}
