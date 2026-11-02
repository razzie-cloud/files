package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/razzie-cloud/files/internal/app"
	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/db"
	"github.com/razzie-cloud/files/internal/logging"
	"github.com/razzie-cloud/files/internal/secrets"
	rclonestorage "github.com/razzie-cloud/files/internal/storage/rclone"
	"github.com/razzie-cloud/files/internal/store"
)

func run() error {
	cfg, e := config.Load(os.Args[1:], os.Getenv)
	if e != nil {
		return logging.Wrap("config", e)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	uri, e := cfg.DatabaseURI(startup)
	if e != nil {
		return logging.Wrap("database_discovery", e)
	}
	database, e := sql.Open("pgx", uri)
	if e != nil {
		return logging.Wrap("postgres_open", e)
	}
	defer database.Close()
	database.SetMaxOpenConns(cfg.MaxUploads + 16)
	database.SetMaxIdleConns(4)
	database.SetConnMaxLifetime(30 * time.Minute)
	if e = database.PingContext(startup); e != nil {
		return logging.Wrap("postgres_connect", e)
	}
	if e = db.Migrate(startup, database); e != nil {
		return logging.Wrap("migrate_postgres", e)
	}
	secretStore := secrets.New(database)
	if cfg.ImportSecretsDir != "" {
		if e = secretStore.Import(startup, cfg.ImportSecretsDir); e != nil {
			return logging.Wrap("import_secrets", e)
		}
	}
	if e = secretStore.Ready(startup); e != nil {
		return logging.Wrap("secrets_ready", e)
	}
	policy, e := rclonestorage.NewPolicy(cfg.PrivateEndpoints, cfg.AllowHTTP)
	if e != nil {
		return logging.Wrap("egress_policy", e)
	}
	if e = rclonestorage.Initialize(); e != nil {
		return logging.Wrap("rclone_init", e)
	}
	egress, e := rclonestorage.StartEgress(policy)
	if e != nil {
		return logging.Wrap("egress_init", e)
	}
	defer egress.Close()
	factory := &rclonestorage.Factory{ProxyURL: egress.URL, Policy: policy, MaxEntries: cfg.MaxEntries}
	server, e := app.New(cfg, &store.Store{DB: database}, secretStore, factory, policy)
	if e != nil {
		return logging.Wrap("template_init", e)
	}
	stopHealth := server.StartHealthChecks(ctx)
	defer stopHealth()
	httpServer := &http.Server{Addr: cfg.Address(), Handler: server.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10, ErrorLog: logging.ServerLogger()}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdown); err != nil {
			slog.Warn("Graceful shutdown failed", "event", "server.shutdown_failed", "error_kind", logging.Kind(err))
			httpServer.Close()
		}
	}()
	slog.Info("Server listening", "event", "server.listening", "address", cfg.Address())
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
	return logging.Wrap("serve", e)
}

func main() {
	if err := logging.Setup("files"); err != nil {
		slog.Error(err.Error(), "event", "config.invalid", "field", "LOG_LEVEL")
		os.Exit(1)
	}
	if err := run(); err != nil {
		slog.Error("Service failed", append([]any{"event", "server.failed"}, logging.ErrorAttrs(err)...)...)
		os.Exit(1)
	}
}
