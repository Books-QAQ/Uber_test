package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"uber-test/backend/internal/app"
	"uber-test/backend/internal/config"
)

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.LogLevel),
	}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	application, err := app.New(cfg, logger)
	if err != nil {
		logger.Error("failed to build application", "error", err)
		os.Exit(1)
	}
	if err := application.Run(ctx); err != nil {
		logger.Error("application stopped with error", "error", err)
		os.Exit(1)
	}
}

func parseLogLevel(value string) slog.Level {
	switch value {
	case "debug", "DEBUG":
		return slog.LevelDebug
	case "warn", "WARN", "warning", "WARNING":
		return slog.LevelWarn
	case "error", "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
