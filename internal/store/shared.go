package store

import (
	"context"
	"errors"
)

type adminKey struct{}

// WithAdmin is set only by the HTTP boundary after signed gateway verification.
// Background reads do not need administrative authority.
func WithAdmin(ctx context.Context, admin bool) context.Context {
	return context.WithValue(ctx, adminKey{}, admin)
}
func IsAdmin(ctx context.Context) bool {
	admin, _ := ctx.Value(adminKey{}).(bool)
	return admin
}

// StoragePrefix keeps the per-user root outside optional rclone encryption.
// Mapping metadata retains the logical prefix entered by its owner.
func (c Connection) StoragePrefix(owner, prefix string) string {
	if c.Shared && c.UserIDPrefix {
		if prefix == "" {
			return owner
		}
		return owner + "/" + prefix
	}
	return prefix
}

// ErrBucket reports an attempt to override a connection's fixed bucket.
var ErrBucket = errors.New("bucket is fixed by the connection")

func (c Connection) BucketFor(requested string) (string, error) {
	if c.Bucket == "" {
		return requested, nil
	}
	if requested != "" && requested != c.Bucket {
		return "", ErrBucket
	}
	return c.Bucket, nil
}
