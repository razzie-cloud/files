// files-dev-s3 is an authenticated, disposable in-memory S3 development fixture.
// It is not a production storage service. All objects disappear on restart.
package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/razzie-cloud/files/internal/devs3"
	"github.com/razzie-cloud/files/internal/logging"
)

func main() {
	if err := logging.Setup("files-dev-s3"); err != nil {
		slog.Error(err.Error(), "event", "config.invalid", "field", "LOG_LEVEL")
		os.Exit(1)
	}
	password := os.Getenv("S3_DEV_PASSWORD")
	if len(password) < 8 {
		slog.Error("Set S3_DEV_PASSWORD to at least eight characters", "event", "config.invalid", "field", "S3_DEV_PASSWORD")
		os.Exit(1)
	}
	key := os.Getenv("S3_DEV_ACCESS_KEY")
	if key == "" {
		key = "files-dev"
	}
	bucket := os.Getenv("S3_DEV_BUCKET")
	if bucket == "" {
		bucket = "files-demo"
	}
	handler, _, err := devs3.New(bucket, map[string]string{key: password})
	if err != nil {
		slog.Error("Cannot initialize S3 development fixture", "event", "server.failed", "stage", "fixture_init", "error_kind", logging.Kind(err))
		os.Exit(1)
	}
	server := &http.Server{Addr: ":9000", Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10, ErrorLog: logging.ServerLogger()}
	slog.Info("Disposable S3 fixture; data is lost on restart", "event", "server.listening", "address", ":9000")
	if err := server.ListenAndServe(); err != nil {
		slog.Error("Service failed", "event", "server.failed", "stage", "serve", "error_kind", logging.Kind(err))
		os.Exit(1)
	}
}
