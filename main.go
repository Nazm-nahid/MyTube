package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

//go:embed web
var web embed.FS

func webFS() fs.FS {
	sub, err := fs.Sub(web, "web")
	if err != nil {
		panic(err)
	}
	return sub
}

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	bucket := os.Getenv("GCS_BUCKET")
	if bucket == "" {
		return errors.New("GCS_BUCKET is required")
	}
	ttl, err := time.ParseDuration(env("UPLOAD_URL_TTL", "15m"))
	if err != nil {
		return err
	}

	pool, err := pgxpool.New(ctx, env("DATABASE_URL", "postgres://videos:videos@localhost:5433/videos"))
	if err != nil {
		return err
	}
	defer pool.Close()

	store := &Store{db: pool}
	if err := store.Migrate(ctx, schema); err != nil {
		return err
	}

	storage, err := NewStorage(ctx, bucket, ttl)
	if err != nil {
		return err
	}
	defer storage.Close()

	srv := &http.Server{
		Addr:              env("ADDR", ":8080"),
		Handler:           (&Server{store: store, storage: storage}).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", srv.Addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
