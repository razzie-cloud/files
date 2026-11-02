package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/store"
)

type failingLogRepo struct{ store.Repository }

func (failingLogRepo) Directories(context.Context, string) ([]store.Directory, error) {
	return nil, errors.New("secret SQL and database password")
}
func (failingLogRepo) PublicShare(context.Context, string) (store.Share, error) {
	return store.Share{}, errors.New("secret share capability and storage endpoint")
}

func TestManagementAndPublicFailureLogsExcludePrivateData(t *testing.T) {
	for _, public := range []bool{false, true} {
		t.Run(map[bool]string{false: "management", true: "public"}[public], func(t *testing.T) {
			s, err := New(config.Config{Origin: "http://files.example", ShareOrigin: "http://shared.example"}, failingLogRepo{}, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			defer slog.SetDefault(previous)
			url, route, status := "http://files.example/?path=secret-object", "GET /{$}", 500
			capability := strings.Repeat("f", 64)
			if public {
				url, route, status = "http://shared.example/s/"+capability+"?path=secret-object", "GET /s/{share}", 503
			}
			r := httptest.NewRequest("GET", url, nil)
			r.Header.Set("X-Auth-Request-User", testOwner)
			r.Header.Set("X-Auth-Request-Name", "secret-display-name")
			r.Header.Set("Cookie", "secret-cookie")
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if strings.Contains(output.String(), "secret") || strings.Contains(output.String(), capability) {
				t.Fatalf("private data logged: %s", &output)
			}
			var event map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &event); err != nil {
				t.Fatal(err)
			}
			if event["event"] != "request.failed" || event["route"] != route || event["request_id"] != w.Header().Get("X-Request-ID") {
				t.Fatalf("missing failure context: %#v", event)
			}
			if public && event["owner_user_id"] != nil {
				t.Fatal("public request trusted proxy identity")
			}
		})
	}
}
