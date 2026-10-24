package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hnewcity/mantle-mask/internal/config"
	"github.com/hnewcity/mantle-mask/internal/gateway"
	"github.com/hnewcity/mantle-mask/internal/server"
	"github.com/hnewcity/mantle-mask/internal/usage"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	gin.SetMode(gin.ReleaseMode)
	applyMemoryLimit()
	dir := env("MANTLE_DATA_DIR", "./data")
	store, err := config.Open(dir, os.Getenv("MANTLE_MASTER_KEY"))
	if err != nil {
		return err
	}
	admin, err := config.AdminToken(dir, os.Getenv("MANTLE_ADMIN_TOKEN"))
	if err != nil {
		return err
	}
	storage := usage.Options{MySQLDSN: os.Getenv("MANTLE_MYSQL_DSN")}
	if raw := os.Getenv("MANTLE_MYSQL_IMPORT_SQLITE"); raw != "" {
		if storage.ImportSQLite, err = strconv.ParseBool(raw); err != nil {
			return errors.New("MANTLE_MYSQL_IMPORT_SQLITE must be true or false")
		}
		if storage.ImportSQLite && storage.MySQLDSN == "" {
			return errors.New("MANTLE_MYSQL_IMPORT_SQLITE requires MANTLE_MYSQL_DSN")
		}
	}
	proxy, err := gateway.New(store, dir, storage)
	if err != nil {
		return err
	}
	defer proxy.Close()
	addr := env("MANTLE_ADDR", "127.0.0.1:8080")
	srv := &http.Server{
		Addr: addr, Handler: server.New(store, proxy, admin, os.Getenv("MANTLE_CORS_ORIGINS")),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		IdleTimeout: 120 * time.Second, MaxHeaderBytes: 64 << 10,
		// Stream writes are individually bounded inside the proxy.
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	slog.Info("Mantle Mask listening", "address", addr, "data", dir)
	if os.Getenv("MANTLE_ADMIN_TOKEN") == "" {
		slog.Info("admin token available in file", "path", filepath.Join(dir, "admin-token"))
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			_ = srv.Close()
			return err
		}
		err := <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
