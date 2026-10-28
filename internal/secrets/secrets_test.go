package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/razzie-cloud/files/internal/db"
	"github.com/razzie-cloud/files/internal/testutil"
)

func TestDatabaseSecretOwnershipAndRecovery(t *testing.T) {
	database := testutil.Database(t)
	ctx := context.Background()
	s := New(database)
	owner := uuid.NewString()
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	value := Value{AccessKey: "test-key", SecretKey: "test-secret", SessionToken: "test-session", Password: "crypt password", Password2: "salt password"}
	ref, err := s.Put(ctx, owner, value)
	if err != nil {
		t.Fatal(err)
	}
	// A new store instance needs only the database; there is no file dependency.
	got, err := New(database).Get(ctx, owner, ref)
	if err != nil || got != value {
		t.Fatalf("secret round trip failed: %v", err)
	}
	for _, test := range []struct{ owner, ref string }{{uuid.NewString(), ref}, {owner, "../escape"}, {owner, ""}} {
		if _, err := s.Get(ctx, test.owner, test.ref); !errors.Is(err, ErrNotFound) {
			t.Fatal("secret escaped ownership boundary")
		}
	}
	connectionID, directoryID := uuid.NewString(), uuid.NewString()
	_, err = database.ExecContext(ctx, `INSERT INTO storage_connections(id,owner_user_id,name,provider,region,path_style,access_mode,secret_ref) VALUES($1,$2,'Storage','Other','region',true,'writable',$3)`, connectionID, owner, ref)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.ExecContext(ctx, `INSERT INTO directories(id,owner_user_id,connection_id,name,bucket,read_only,encryption_mode,crypt_secret_ref) VALUES($1,$2,$3,'Crypt','bucket',false,'rclone_crypt',$4)`, directoryID, owner, connectionID, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.ExecContext(ctx, `DELETE FROM service_secrets WHERE owner_user_id=$1 AND id=$2`, owner, ref); err == nil {
		t.Fatal("referenced secret was deleted")
	}
	if _, err = database.ExecContext(ctx, `DELETE FROM directories WHERE id=$1`, directoryID); err != nil {
		t.Fatal(err)
	}
	if _, err = database.ExecContext(ctx, `DELETE FROM storage_connections WHERE id=$1`, connectionID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, owner, ref); err != nil {
		t.Fatal("configuration deletion lost recovery secret")
	}
	otherRef, err := s.Put(ctx, uuid.NewString(), value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.ExecContext(ctx, `INSERT INTO storage_connections(id,owner_user_id,name,provider,region,path_style,access_mode,secret_ref) VALUES($1,$2,'Forbidden','Other','region',true,'read_only',$3)`, uuid.NewString(), owner, otherRef); err == nil {
		t.Fatal("cross-owner secret reference accepted")
	}
	if _, err = s.Put(ctx, "not-a-user", value); err == nil {
		t.Fatal("invalid owner accepted")
	}
}

func writeLegacy(t *testing.T, dir, ref string, value Value) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, ref), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeAndLegacyImport(t *testing.T) {
	database := testutil.EmptyDatabase(t)
	ctx := context.Background()
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("../db/migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	owner := uuid.NewString()
	credentialsRef, cryptRef, retainedRef := owner+"-"+uuid.NewString(), owner+"-"+uuid.NewString(), owner+"-"+uuid.NewString()
	connectionID := uuid.NewString()
	_, err = database.ExecContext(ctx, `INSERT INTO storage_connections(id,owner_user_id,name,provider,region,path_style,access_mode,secret_ref) VALUES($1,$2,'Legacy','Other','region',true,'writable',$3)`, connectionID, owner, credentialsRef)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.ExecContext(ctx, `INSERT INTO directories(id,owner_user_id,connection_id,name,bucket,read_only,encryption_mode,crypt_secret_ref) VALUES($1,$2,$3,'Legacy crypt','bucket',false,'rclone_crypt',$4)`, uuid.NewString(), owner, connectionID, cryptRef)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.ExecContext(ctx, `INSERT INTO directories(id,owner_user_id,connection_id,name,bucket,read_only,encryption_mode) VALUES($1,$2,$3,'Legacy plain','other-bucket',true,'none')`, uuid.NewString(), owner, connectionID)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	s := New(database)
	if !errors.Is(s.Ready(ctx), ErrLegacySecrets) {
		t.Fatal("upgraded installation served without importing secrets")
	}
	dir := t.TempDir()
	credentials := Value{AccessKey: "legacy-key", SecretKey: "legacy-secret"}
	keys := Value{Password: "legacy password", Password2: "legacy salt"}
	writeLegacy(t, dir, credentialsRef, credentials)
	if err = s.Import(ctx, dir); !errors.Is(err, ErrLegacySecrets) {
		t.Fatal("missing referenced crypt secret accepted")
	}
	var count int
	if err = database.QueryRowContext(ctx, "SELECT count(*) FROM service_secrets").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial import was committed")
	}
	writeLegacy(t, dir, cryptRef, keys)
	writeLegacy(t, dir, retainedRef, Value{Password: "retained recovery password"})
	if err = s.Import(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if err = s.Import(ctx, dir); err != nil {
		t.Fatal("repeat import failed", err)
	}
	if err = s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(ctx, owner, cryptRef); err != nil || got != keys {
		t.Fatal("crypt secret changed during import")
	}
	if _, err = s.Get(ctx, owner, retainedRef); err != nil {
		t.Fatal("unreferenced recovery secret was not imported")
	}
	writeLegacy(t, dir, cryptRef, Value{Password: "different password"})
	if err = s.Import(ctx, dir); err == nil {
		t.Fatal("import overwrote a conflicting database secret")
	}
	if got, err := s.Get(ctx, owner, cryptRef); err != nil || got != keys {
		t.Fatal("conflicting import changed the database secret")
	}
	if err = os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if got, err := New(database).Get(ctx, owner, credentialsRef); err != nil || got != credentials {
		t.Fatal("imported credentials still depended on the old directory")
	}
}

func TestImportRejectsInvalidRecordsWithoutLeakingContents(t *testing.T) {
	database := testutil.Database(t)
	ctx := context.Background()
	s := New(database)
	dir := t.TempDir()
	owner := uuid.NewString()
	ref := owner + "-" + uuid.NewString()
	if err := os.WriteFile(filepath.Join(dir, ref), []byte(`{"Password":"private secret",broken}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Import(ctx, dir); err == nil || err.Error() != "invalid legacy secret record" {
		t.Fatal("malformed record accepted or raw secret error returned")
	}
	if err := os.Remove(filepath.Join(dir, ref)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ref), []byte("null"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Import(ctx, dir); err == nil {
		t.Fatal("null record accepted")
	}
	if err := os.Remove(filepath.Join(dir, ref)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, ref)); err != nil {
		t.Fatal(err)
	}
	if err := s.Import(ctx, dir); err == nil {
		t.Fatal("symlink record accepted")
	}
}
