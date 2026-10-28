// files-dev-s3 is an authenticated, disposable in-memory S3 development fixture.
// It is not a production storage service. All objects disappear on restart.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/razzie-cloud/files/internal/devs3"
)

func main() {
	password := os.Getenv("S3_DEV_PASSWORD")
	if len(password) < 8 {
		log.Fatal("set S3_DEV_PASSWORD to at least eight characters")
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
		log.Fatal("cannot initialize S3 development fixture")
	}
	server := &http.Server{Addr: ":9000", Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10}
	log.Printf("disposable S3 fixture on :9000, bucket %s; data is lost on restart", bucket)
	log.Fatal(server.ListenAndServe())
}
