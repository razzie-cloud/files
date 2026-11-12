package app

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/razzie-cloud/files/internal/secrets"
	"github.com/razzie-cloud/files/internal/store"
	"github.com/razzie-cloud/files/internal/testutil"
	"testing"
	"time"
)

func TestFixedBucketChangeWaitsForMapping(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const creator = "11111111-1111-4111-8111-111111111111"
	const member = "22222222-2222-4222-8222-222222222222"
	repo := &store.Store{DB: testutil.Database(t)}
	ref, err := secrets.New(repo.DB).Put(ctx, creator, secrets.Value{AccessKey: "test", SecretKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	c := store.Connection{Owner: creator, Name: "Concurrent", Provider: "AWS", Region: "us-east-1", AccessMode: "writable", SecretRef: ref, Shared: true, UserIDPrefix: true}
	admin := store.WithAdmin(ctx, true)
	if err := repo.SaveConnection(admin, &c); err != nil {
		t.Fatal(err)
	}
	tx, err := repo.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT id FROM storage_connections WHERE id=$1 FOR SHARE`, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO directories(id,owner_user_id,connection_id,name,bucket,prefix,read_only,encryption_mode) VALUES($1,$2,$3,'Pending','bucket','projects',false,'none')`, uuid.NewString(), member, c.ID); err != nil {
		t.Fatal(err)
	}
	c.Bucket = "other-bucket"
	result := make(chan error, 1)
	go func() { result <- repo.SaveConnection(admin, &c) }()
	select {
	case err := <-result:
		t.Fatal("bucket restriction did not wait for mapping", err)
	case <-time.After(40 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, store.ErrConflict) {
			t.Fatal("incompatible bucket restriction accepted after concurrent mapping commit", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// The same invariant also holds for a direct SQL update.
	if _, err := repo.DB.ExecContext(ctx, `UPDATE storage_connections SET bucket='other-bucket' WHERE id=$1`, c.ID); err == nil {
		t.Fatal("SQL fixed-bucket policy bypass")
	}
}
