package rclone

import (
	"context"
	"errors"
	"github.com/razzie-cloud/files/internal/secrets"
	"github.com/razzie-cloud/files/internal/store"
	"testing"
)

func TestFixedBucketCannotBeOverriddenAtStorageBoundary(t *testing.T) {
	factory := &Factory{}
	_, err := factory.Open(context.Background(), store.Connection{Bucket: "fixed-bucket"}, store.Directory{Bucket: "other-bucket"}, secrets.Value{}, secrets.Value{})
	if !errors.Is(err, store.ErrBucket) {
		t.Fatal("storage constructor allowed bucket override", err)
	}
}
