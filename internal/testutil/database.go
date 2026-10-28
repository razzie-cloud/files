// Package testutil provides disposable-schema fixtures for integration tests.
package testutil

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/razzie-cloud/files/internal/db"
)

func Database(t *testing.T) *sql.DB {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("set TEST_DATABASE_URL to a disposable local PostgreSQL backend")
	}
	u, e := url.Parse(raw)
	if e != nil {
		t.Fatal(e)
	}
	if u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1" {
		t.Fatal("integration fixture requires a loopback PostgreSQL backend")
	}
	admin, e := sql.Open("pgx", raw)
	if e != nil {
		t.Fatal(e)
	}
	schema := "files_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = admin.Exec("CREATE SCHEMA " + schema); e != nil {
		admin.Close()
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	database, e := sql.Open("pgx", u.String())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { database.Close(); admin.Exec("DROP SCHEMA " + schema + " CASCADE"); admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if e = db.Migrate(ctx, database); e != nil {
		t.Fatal(e)
	}
	return database
}
