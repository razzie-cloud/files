package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/razzie-cloud/files/internal/app"
	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/db"
	"github.com/razzie-cloud/files/internal/secrets"
	rclonestorage "github.com/razzie-cloud/files/internal/storage/rclone"
	"github.com/razzie-cloud/files/internal/store"
)

func run() error {
	cfg, e := config.Load(os.Args[1:], os.Getenv)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	uri, e := cfg.DatabaseURI(startup)
	if e != nil {
		return e
	}
	database, e := sql.Open("pgx", uri)
	if e != nil {
		return e
	}
	defer database.Close()
	database.SetMaxOpenConns(cfg.MaxUploads + 16)
	database.SetMaxIdleConns(4)
	database.SetConnMaxLifetime(30 * time.Minute)
	if e = database.PingContext(startup); e != nil {
		return sanitized("database unavailable")
	}
	if e = db.Migrate(startup, database); e != nil {
		return sanitized("database migration failed")
	}
	secretStore, e := secrets.Open(cfg.SecretsDir)
	if e != nil {
		return sanitized("secret storage unavailable")
	}
	defer secretStore.Close()
	policy, e := rclonestorage.NewPolicy(cfg.PrivateEndpoints, cfg.AllowHTTP)
	if e != nil {
		return e
	}
	if e = rclonestorage.Initialize(); e != nil {
		return sanitized("rclone initialization failed")
	}
	egress, e := rclonestorage.StartEgress(policy)
	if e != nil {
		return sanitized("cannot start S3 egress proxy")
	}
	defer egress.Close()
	factory := &rclonestorage.Factory{ProxyURL: egress.URL, Policy: policy, MaxEntries: cfg.MaxEntries}
	server, e := app.New(cfg, &store.Store{DB: database}, secretStore, factory, policy)
	if e != nil {
		return sanitized("template initialization failed")
	}
	httpServer := &http.Server{Addr: cfg.Address(), Handler: server.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if httpServer.Shutdown(shutdown) != nil {
			httpServer.Close()
		}
	}()
	log.Printf("files listening on %s", cfg.Address())
	e = httpServer.ListenAndServe()
	if e != http.ErrServerClosed {
		stop()
	}
	// ListenAndServe returns when the listener closes, before Shutdown has
	// finished draining requests. Keep storage/database dependencies alive.
	<-shutdownDone
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}

type sanitized string

func (e sanitized) Error() string { return string(e) }
func main() {
	if e := run(); e != nil {
		log.Fatal(e)
	}
}
