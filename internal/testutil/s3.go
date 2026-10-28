package testutil

import (
	"github.com/razzie-cloud/files/internal/devs3"
	"github.com/rclone/gofakes3/s3mem"
	"net/http/httptest"
	"testing"
)

func S3(t *testing.T, bucket string) (*httptest.Server, *s3mem.Backend) {
	t.Helper()
	handler, backend, err := devs3.New(bucket, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server, backend
}
