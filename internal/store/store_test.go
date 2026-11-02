package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/razzie-cloud/files/internal/secrets"
	"github.com/razzie-cloud/files/internal/store"
	"github.com/razzie-cloud/files/internal/testutil"
)

func TestOwnershipRevisionsAndPrefixes(t *testing.T) {
	repo := &store.Store{DB: testutil.Database(t)}
	ctx := context.Background()
	alice, bob := uuid.NewString(), uuid.NewString()
	ref, err := secrets.New(repo.DB).Put(ctx, alice, secrets.Value{AccessKey: "key", SecretKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	c := store.Connection{Owner: alice, Name: "Storage", Provider: "Other", AccessMode: "writable", SecretRef: ref}
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
	share := store.Share{Owner: bob, DirectoryID: d.ID, IsDirectory: true}
	if err := repo.CreateShare(ctx, &share); !errors.Is(err, store.ErrConflict) {
		t.Fatal("cross-owner share persisted", err)
	}
	share.Owner = alice
	if err := repo.CreateShare(ctx, &share); err != nil {
		t.Fatal(err)
	}
	if list, err := repo.Shares(ctx, bob, d.ID); err != nil || len(list) != 0 {
		t.Fatal("cross-owner share list", list, err)
	}
	if err := repo.RemoveShare(ctx, bob, d.ID, share.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("cross-owner share deletion", err)
	}
	if err := repo.RemoveShare(ctx, alice, uuid.NewString(), share.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("share deleted through another directory", err)
	}
	if err := repo.RemoveShare(ctx, alice, d.ID, share.ID); err != nil {
		t.Fatal(err)
	}
	// Optional deadlines round-trip; expired links remain manageable by owners
	// while public discovery rejects them, including on an uncached replica.
	for _, deadline := range []time.Time{time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond), time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)} {
		expiring := store.Share{Owner: alice, DirectoryID: d.ID, IsDirectory: true, ExpiresAt: &deadline}
		if err := repo.CreateShare(ctx, &expiring); err != nil {
			t.Fatal(err)
		}
		list, err := repo.Shares(ctx, alice, d.ID)
		if err != nil || len(list) != 1 || list[0].ExpiresAt == nil || !list[0].ExpiresAt.Equal(deadline) {
			t.Fatal("expiration did not round-trip", list, err)
		}
		found, err := repo.PublicShare(ctx, expiring.ID)
		if deadline.Before(time.Now()) {
			if !errors.Is(err, store.ErrNotFound) {
				t.Fatal("expired link discovered publicly", err)
			}
		} else if err != nil || found.ExpiresAt == nil || !found.ExpiresAt.Equal(deadline) {
			t.Fatal("active expiring link unavailable", err)
		}
		if err := repo.RemoveShare(ctx, alice, d.ID, expiring.ID); err != nil {
			t.Fatal("owner cannot remove expiring share", err)
		}
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
