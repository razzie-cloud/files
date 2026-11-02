package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/razzie-cloud/files/internal/secrets"
	"github.com/razzie-cloud/files/internal/store"
)

// Run against both plain S3 and rclone crypt from the existing HTTP fixture.
func exerciseShareWorkflow(t *testing.T, srv *Server, repo *store.Store, d store.Directory) {
	t.Helper()
	ctx := context.Background()
	c, err := repo.Connection(ctx, d.Owner, d.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := srv.Secrets.Get(ctx, d.Owner, c.SecretRef)
	if err != nil {
		t.Fatal(err)
	}
	var crypt secrets.Value
	if d.EncryptionMode == "rclone_crypt" {
		crypt, err = srv.Secrets.Get(ctx, d.Owner, d.CryptSecretRef)
		if err != nil {
			t.Fatal(err)
		}
	}
	fs, err := srv.Storage.Open(ctx, c, d, credentials, crypt)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	filename := "hello + %.txt"
	for _, key := range []string{"folder/" + filename, "folder/nested/child.txt", "folderish/private.txt", "private.txt"} {
		if err := fs.Put(ctx, key, strings.NewReader("shared content")); err != nil {
			t.Fatal(err)
		}
	}
	handler := srv.Handler()
	request := func(method, target, owner string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if owner != "" {
			req.Header.Set("X-Auth-Request-User", owner)
			req.Header.Set("Origin", srv.Config.Origin)
		} else {
			req.Header.Set("Origin", srv.Config.ShareOrigin)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	admin := srv.Config.Origin + "/directories/" + d.ID
	values := url.Values{"path": {"folder"}, "kind": {"directory"}, "password": {"share password"}, "expires_in_minutes": {"60"}}
	for _, invalid := range []string{"0", "-1", "1.5", "invalid", "153722868"} {
		if out := request("POST", admin+"/shares", d.Owner, url.Values{"path": {"folder"}, "kind": {"directory"}, "expires_in_minutes": {invalid}}, nil); out.Code != 422 {
			t.Fatal("invalid expiration accepted", invalid, out.Code)
		}
	}
	if out := request("POST", admin+"/shares", uuid.NewString(), values, nil); out.Code != 404 {
		t.Fatal("cross-owner share creation", out.Code)
	}
	if out := request("POST", admin+"/shares", d.Owner, values, nil); out.Code != 303 {
		t.Fatal("share creation", out.Code, out.Body.String())
	}
	shares, err := repo.Shares(ctx, d.Owner, d.ID)
	if err != nil || len(shares) != 1 {
		t.Fatal("shares", shares, err)
	}
	share := shares[0]
	if share.ExpiresAt == nil || !share.ExpiresAt.After(time.Now().Add(59*time.Minute)) || !share.ExpiresAt.Before(time.Now().Add(61*time.Minute)) {
		t.Fatal("share expiration not persisted", share.ExpiresAt)
	}
	if out := request("GET", admin+"/shares", d.Owner, nil, nil); out.Code != 200 || !strings.Contains(out.Body.String(), "Expires <time") {
		t.Fatal("expiration missing from management page", out.Code, out.Body.String())
	}
	if share.DirectoryID != d.ID || share.PasswordHash == "share password" || share.PasswordHash == "" {
		t.Fatal("share binding/password storage")
	}
	public := srv.Config.ShareOrigin + "/s/" + share.ID
	if out := request("GET", public, "", nil, nil); out.Code != 200 || !strings.Contains(out.Body.String(), "requires a password") {
		t.Fatal("public password page", out.Code, out.Body.String())
	}
	out := request("POST", public+"/unlock", "", url.Values{"password": {"share password"}}, nil)
	if out.Code != 303 || len(out.Result().Cookies()) != 1 {
		t.Fatal("password unlock", out.Code, out.Body.String())
	}
	cookie := out.Result().Cookies()[0]
	out = request("GET", public, "", nil, cookie)
	if out.Code != 200 || strings.Contains(out.Body.String(), "private.txt") {
		t.Fatal("directory scope", out.Code, out.Body.String())
	}
	var download string
	for _, u := range pageURLs(t, out.Body.String(), "a", "href") {
		if strings.HasSuffix(u.Path, "/download") {
			download = srv.Config.ShareOrigin + u.String()
		}
	}
	if download == "" {
		t.Fatal("missing public download")
	}
	out = request("GET", download, "", nil, cookie)
	if out.Code != 200 || out.Body.String() != "shared content" {
		t.Fatal("public download/decryption", out.Code, out.Body.String())
	}
	out = request("GET", public+"?path=nested", "", nil, cookie)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "child.txt") {
		t.Fatal("nested browse", out.Code, out.Body.String())
	}
	req := httptest.NewRequest("GET", download, nil)
	req.AddCookie(cookie)
	req.Header.Set("Range", "bytes=2-6")
	ranged := httptest.NewRecorder()
	handler.ServeHTTP(ranged, req)
	if ranged.Code != 206 || ranged.Body.String() != "ared " {
		t.Fatal("public range", ranged.Code, ranged.Body.String())
	}
	// A second replica can validate the signed cookie using persisted share data.
	replica, err := New(srv.Config, repo, srv.Secrets, srv.Storage, srv.Policy)
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest("GET", download, nil)
	req.AddCookie(cookie)
	out = httptest.NewRecorder()
	replica.Handler().ServeHTTP(out, req)
	if out.Code != 200 || out.Body.String() != "shared content" {
		t.Fatal("replica session", out.Code, out.Body.String())
	}
	// Rename through management: sharing stays attached to the ID.
	out = request("POST", admin+"/update", d.Owner, url.Values{"name": {"Renamed " + d.Name}, "revision": {strconv.FormatInt(d.Revision, 10)}}, nil)
	if out.Code != 303 {
		t.Fatal("rename", out.Code, out.Body.String())
	}
	if out = request("GET", download, "", nil, cookie); out.Code != 200 || out.Body.String() != "shared content" {
		t.Fatal("rename broke share", out.Code, out.Body.String())
	}
	// Single-file links cannot be widened by query-string paths or share cookies.
	out = request("POST", admin+"/shares", d.Owner, url.Values{"path": {"folder/" + filename}, "kind": {"file"}}, nil)
	if out.Code != 303 {
		t.Fatal("file share", out.Code, out.Body.String())
	}
	shares, _ = repo.Shares(ctx, d.Owner, d.ID)
	var file store.Share
	for _, v := range shares {
		if !v.IsDirectory {
			file = v
		}
	}
	fileBase := srv.Config.ShareOrigin + "/s/" + file.ID
	if out = request("GET", fileBase+"/download", "", nil, nil); out.Code != 200 || !bytes.Equal(out.Body.Bytes(), []byte("shared content")) {
		t.Fatal("file download", out.Code, out.Body.String())
	}
	if out = request("GET", fileBase+"/download?path=private.txt", "", nil, nil); out.Code != 404 {
		t.Fatal("file scope expanded", out.Code)
	}
	// The empty path shares the complete mapped directory, without a password.
	out = request("POST", admin+"/shares", d.Owner, url.Values{"path": {""}, "kind": {"directory"}}, nil)
	if out.Code != 303 {
		t.Fatal("whole directory share", out.Code, out.Body.String())
	}
	all, _ := repo.Shares(ctx, d.Owner, d.ID)
	for _, v := range all {
		if v.IsDirectory && v.Path == "" {
			out = request("GET", srv.Config.ShareOrigin+"/s/"+v.ID, "", nil, nil)
			if out.Code != 200 || !strings.Contains(out.Body.String(), "private.txt") || strings.Contains(out.Body.String(), "requires a password") {
				t.Fatal("whole directory browse", out.Code, out.Body.String())
			}
		}
	}
	if out = request("POST", admin+"/shares/"+share.ID+"/remove", uuid.NewString(), nil, nil); out.Code != 404 {
		t.Fatal("cross-owner revoke", out.Code)
	}
	if out = request("POST", admin+"/shares/"+share.ID+"/remove", d.Owner, nil, nil); out.Code != 303 {
		t.Fatal("revoke", out.Code, out.Body.String())
	}
	if out = request("GET", download, "", nil, cookie); out.Code != 404 {
		t.Fatal("revoked share still cached locally", out.Code)
	}
	// Simulate the other replica's non-sliding cache deadline without sleeping.
	replica.shares.mu.Lock()
	entry := replica.shares.entries[share.ID]
	entry.until = time.Now().Add(-time.Second)
	replica.shares.entries[share.ID] = entry
	replica.shares.mu.Unlock()
	req = httptest.NewRequest("GET", download, nil)
	req.AddCookie(cookie)
	out = httptest.NewRecorder()
	replica.Handler().ServeHTTP(out, req)
	if out.Code != 404 {
		t.Fatal("replica did not observe revocation at expiry", out.Code)
	}
	// Deleting and recreating a named directory must never resurrect a share.
	current, _ := repo.Directory(ctx, d.Owner, d.ID)
	out = request("POST", admin+"/remove", d.Owner, url.Values{"revision": {strconv.FormatInt(current.Revision, 10)}}, nil)
	if out.Code != 303 {
		t.Fatal("remove directory", out.Code, out.Body.String())
	}
	if _, err := repo.PublicShare(ctx, file.ID); err != store.ErrNotFound {
		t.Fatal("directory deletion did not cascade", err)
	}
	if out = request("GET", fileBase+"/download", "", nil, nil); out.Code != 404 {
		t.Fatal("removed directory still shared", out.Code)
	}
	current.ID = ""
	if err := repo.SaveDirectory(ctx, &current); err != nil {
		t.Fatal("recreate", err)
	}
	if out = request("GET", fileBase+"/download", "", nil, nil); out.Code != 404 {
		t.Fatal("recreated directory inherited old share", out.Code)
	}
}
