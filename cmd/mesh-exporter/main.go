package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/caoer/mesh-exporter/internal/collector"
	"github.com/caoer/mesh-exporter/internal/config"
	"github.com/caoer/mesh-exporter/internal/push"
	"github.com/caoer/mesh-exporter/internal/server"
	"github.com/prometheus/client_golang/prometheus"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "/etc/mesh-exporter/config.yaml", "path to config file")
	showVersion := flag.Bool("version", false, "show version")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err, "path", *configPath)
		os.Exit(1)
	}

	reg := prometheus.NewRegistry()

	collectors, err := collector.BuildFromConfig(cfg, reg, logger)
	if err != nil {
		slog.Error("failed to build collectors", "error", err)
		os.Exit(1)
	}

	for _, c := range collectors {
		if err := c.Start(); err != nil {
			slog.Error("failed to start collector", "name", c.Name(), "error", err)
			os.Exit(1)
		}
		slog.Info("collector started", "name", c.Name())
	}

	var pushClient *push.PushClient
	if cfg.Push.Enabled {
		pc, err := push.NewPushClient(reg, cfg.Push, logger)
		if err != nil {
			slog.Error("failed to create push client", "error", err)
			os.Exit(1)
		}
		pc.Start()
		pushClient = pc
	}

	srv := server.New(cfg.Listen, reg)

	go func() {
		slog.Info("listening", "addr", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}
	if pushClient != nil {
		pushClient.Stop()
	}
	for _, c := range collectors {
		c.Stop()
	}
}
