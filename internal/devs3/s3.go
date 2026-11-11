// Package devs3 supplies a disposable in-memory S3 development backend.
package devs3

import (
	"context"
	"encoding/hex"
	"github.com/rclone/gofakes3"
	"github.com/rclone/gofakes3/s3mem"
	"io"
	"net/http"
	"strings"
	"sync"
)

type markerKey struct{}

// gofakes3 v0.0.10 strips trailing slashes in its HTTP router. Preserve the
// original marker key in the test backend so it models actual S3 directory
// markers instead of creating an unencrypted zero-byte file called "folder".
type markerBackend struct{ gofakes3.Backend }

func marker(ctx context.Context, key string) string {
	if ctx.Value(markerKey{}) == true {
		return key + "/"
	}
	return key
}
func (b markerBackend) PutObject(ctx context.Context, bucket, key string, meta map[string]string, in io.Reader, size int64) (gofakes3.PutObjectResult, error) {
	return b.Backend.PutObject(ctx, bucket, marker(ctx, key), meta, in, size)
}
func (b markerBackend) HeadObject(ctx context.Context, bucket, key string) (*gofakes3.Object, error) {
	return b.Backend.HeadObject(ctx, bucket, marker(ctx, key))
}
func (b markerBackend) GetObject(ctx context.Context, bucket, key string, span *gofakes3.ObjectRangeRequest) (*gofakes3.Object, error) {
	return b.Backend.GetObject(ctx, bucket, marker(ctx, key), span)
}
func (b markerBackend) DeleteObject(ctx context.Context, bucket, key string) (gofakes3.ObjectDeleteResult, error) {
	return b.Backend.DeleteObject(ctx, bucket, marker(ctx, key))
}
func New(bucket string, credentials map[string]string) (http.Handler, *s3mem.Backend, error) {
	backend := s3mem.New()
	if err := backend.CreateBucket(context.Background(), bucket); err != nil {
		return nil, nil, err
	}
	var options []gofakes3.Option
	if len(credentials) > 0 {
		options = append(options, gofakes3.WithV4Auth(credentials))
	}
	handler := gofakes3.New(markerBackend{backend}, options...).Server()
	// The fixture library does not enforce S3 conditional writes. Serialize the
	// check with the operation so Notes can exercise optimistic concurrency here.
	var requests sync.Mutex
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Lock()
		defer requests.Unlock()
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
		if len(parts) == 2 && (r.Method == "PUT" || r.Method == "DELETE" || r.Method == "GET") {
			current, headErr := backend.HeadObject(r.Context(), parts[0], parts[1])
			match, none := r.Header.Get("If-Match"), r.Header.Get("If-None-Match")
			etag := ""
			if headErr == nil {
				etag = `"` + hex.EncodeToString(current.Hash) + `"`
			}
			if (match != "" && (headErr != nil || match != etag)) || (none == "*" && headErr == nil) {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusPreconditionFailed)
				io.WriteString(w, "<Error><Code>PreconditionFailed</Code><Message>Object validator changed</Message></Error>")
				return
			}
		}
		isMarker := strings.HasSuffix(r.URL.Path, "/") && strings.Count(strings.TrimPrefix(r.URL.Path, "/"), "/") > 1
		handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), markerKey{}, isMarker)))
	}), backend, nil
}
