package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"myhome-chromecast/internal/castclient"
	"myhome-chromecast/internal/config"
	"myhome-chromecast/internal/discovery"
	"myhome-chromecast/internal/httpapi"
	"myhome-chromecast/internal/youtube"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	registry := discovery.NewRegistry(cfg.DeviceTTL)
	if err := registry.Start(ctx, cfg.DiscoveryInterface, logger); err != nil {
		logger.Error("cannot start device discovery", "error", err)
		os.Exit(1)
	}

	youtubeClient := youtube.NewClient(cfg.YouTubeTimeout)
	connections := castclient.NewManager(registry, youtubeClient, cfg.CastTimeout, logger)
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.NewHandler(registry, connections, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("HTTP server started", "address", cfg.HTTPAddr, "deviceTTL", cfg.DeviceTTL)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown requested")
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server stopped", "error", err)
		}
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP shutdown failed", "error", err)
	}
	if err := connections.Close(); err != nil {
		logger.Warn("Cast connection shutdown failed", "error", err)
	}
}
