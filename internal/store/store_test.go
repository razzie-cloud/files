package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/razzie-cloud/files/internal/store"
	"github.com/razzie-cloud/files/internal/testutil"
)

func TestOwnershipRevisionsAndPrefixes(t *testing.T) {
	repo := &store.Store{DB: testutil.Database(t)}
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	c := store.Connection{Owner: alice, Name: "Storage", Provider: "Other", AccessMode: "writable", SecretRef: "secret"}
	if e := repo.SaveConnection(ctx, &c); e != nil {
		t.Fatal(e)
	}
	if _, e := repo.Connection(ctx, bob, c.ID); !errors.Is(e, store.ErrNotFound) {
		t.Fatal("cross-owner connection lookup")
	}
	d := store.Directory{Owner: alice, ConnectionID: c.ID, Name: "Photos", Bucket: "bucket", Prefix: "photos", EncryptionMode: "none"}
	if e := repo.SaveDirectory(ctx, &d); e != nil {
		t.Fatal(e)
	}
	for _, prefix := range []string{"", "photos", "photos/nested"} {
		other := d
		other.ID = ""
		other.Name = uuid.NewString()
		other.Prefix = prefix
		if e := repo.SaveDirectory(ctx, &other); !errors.Is(e, store.ErrConflict) {
			t.Fatalf("overlap %q accepted: %v", prefix, e)
		}
	}
	other := d
	other.ID = ""
	other.Name = "Separate"
	other.Prefix = "photos-old"
	if e := repo.SaveDirectory(ctx, &other); e != nil {
		t.Fatal("prefix boundary", e)
	}
	other = d
	other.ID = ""
	other.Owner = bob
	other.Name = "Forbidden"
	if e := repo.SaveDirectory(ctx, &other); !errors.Is(e, store.ErrNotFound) {
		t.Fatal("cross-owner connection attached", e)
	}
	stale := c
	c.AccessMode = "read_only"
	if e := repo.SaveConnection(ctx, &c); e != nil {
		t.Fatal(e)
	}
	if e := repo.SaveConnection(ctx, &stale); !errors.Is(e, store.ErrConflict) {
		t.Fatal("stale update accepted")
	}
	if e := repo.RemoveConnection(ctx, alice, c.ID, c.Revision); !errors.Is(e, store.ErrConflict) {
		t.Fatal("removed referenced connection")
	}
	if _, e := repo.Directory(ctx, bob, d.ID); !errors.Is(e, store.ErrNotFound) {
		t.Fatal("cross-owner directory lookup")
	}
	unlock, e := repo.Lock(ctx, "shared-object")
	if e != nil {
		t.Fatal(e)
	}
	timeout, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if release, e := repo.Lock(timeout, "shared-object"); e == nil {
		release()
		t.Fatal("write lock did not serialize")
	}
	unlock()
	if release, e := repo.Lock(ctx, "shared-object"); e != nil {
		t.Fatal(e)
	} else {
		release()
	}
}
