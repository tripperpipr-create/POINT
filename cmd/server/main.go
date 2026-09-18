package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/events"
	"local-agent-workbench/internal/httpapi"
	"local-agent-workbench/internal/observability"
)

func main() {
	logger := observability.ConfigureLogger()
	slog.SetDefault(logger)
	dataDir := env("DATA_DIR", filepath.Join(".", "data"))
	apiToken, err := httpapi.ResolveAPIToken(dataDir)
	if err != nil {
		logger.Error("resolve API token", "error", err)
		os.Exit(1)
	}
	eventHub := events.NewHub()
	application, err := app.New(dataDir, app.WithEventSink(eventHub.Publish))
	if err != nil {
		logger.Error("initialise application", "error", err)
		os.Exit(1)
	}
	defer application.Shutdown(context.Background())
	workspaceRoot := env("WORKSPACE_ROOT", filepath.Join(".", "workspace"))
	if err = application.SetWorkspaceBoundary(workspaceRoot); err != nil {
		logger.Error("configure workspace boundary", "error", err)
		os.Exit(1)
	}
	// The headless core is the process that actually runs in the product, so it
	// owns startup recovery too: quests abandoned by a stopped core are paused
	// instead of claiming progress, and sandbox retention runs.
	application.Startup(context.Background())
	// Listen before OpenWorkspace so the IDE health check does not time out on large trees.
	address := env("HTTP_ADDR", "127.0.0.1:8080")
	// Свой адрес ядро узнаёт здесь и только здесь: по нему исполнители на этой
	// машине забирают инструменты Point через MCP.
	application.SetSelfURL("http://" + address)
	server := &http.Server{Addr: address, Handler: httpapi.New(application, eventHub, logger, os.Getenv("CORS_ORIGIN"), apiToken).Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("headless workbench listening", "address", address, "workspace", workspaceRoot, "log_level", os.Getenv("POINT_LOG_LEVEL"), "log_format", os.Getenv("POINT_LOG_FORMAT"), "api_token_file", filepath.Join(dataDir, httpapi.APITokenFileName))
		serverErrors <- server.ListenAndServe()
	}()
	if _, err = application.OpenWorkspace(workspaceRoot); err != nil {
		logger.Error("open configured workspace", "error", err)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = server.Shutdown(ctx)
		cancel()
		os.Exit(1)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err = <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server failed", "error", err)
		}
	case sig := <-signals:
		logger.Info("shutdown requested", "signal", sig.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if shutdownErr := server.Shutdown(ctx); shutdownErr != nil {
		logger.Error("graceful shutdown failed", "error", shutdownErr)
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
