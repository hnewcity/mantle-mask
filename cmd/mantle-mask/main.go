package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hnewcity/mantle-mask/internal/config"
	"github.com/hnewcity/mantle-mask/internal/gateway"
	"github.com/hnewcity/mantle-mask/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	gin.SetMode(gin.ReleaseMode)
	dir := env("MANTLE_DATA_DIR", "./data")
	store, err := config.Open(dir, os.Getenv("MANTLE_MASTER_KEY"))
	if err != nil {
		return err
	}
	admin, err := config.AdminToken(dir, os.Getenv("MANTLE_ADMIN_TOKEN"))
	if err != nil {
		return err
	}
	proxy, err := gateway.New(store, dir)
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
