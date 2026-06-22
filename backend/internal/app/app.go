package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	httpapi "uber-test/backend/internal/api/http"
	wsapi "uber-test/backend/internal/api/ws"
	"uber-test/backend/internal/config"
	"uber-test/backend/internal/location"
	udptransport "uber-test/backend/internal/transport/udp"
)

type App struct {
	cfg        config.Config
	logger     *slog.Logger
	httpServer *http.Server
	udpServer  *udptransport.Server
	hub        *wsapi.Hub
	store      location.Store
}

func New(cfg config.Config, logger *slog.Logger) (*App, error) {
	hub := wsapi.NewHub(logger)
	locationStore := location.Store(location.NewMemoryStore(cfg.RecentLocationLimit))
	if cfg.RedisEnabled {
		redisStore, err := location.NewRedisStore(context.Background(), location.RedisConfig{
			Addr:      cfg.RedisAddr,
			Password:  cfg.RedisPassword,
			DB:        cfg.RedisDB,
			KeyPrefix: cfg.RedisKeyPrefix,
			TTL:       cfg.RedisLocationTTL,
		}, cfg.RecentLocationLimit)
		if err != nil {
			return nil, fmt.Errorf("create redis location store: %w", err)
		}
		locationStore = location.NewMultiStore(locationStore, redisStore, logger)
		logger.Info("redis-backed location store enabled", "addr", cfg.RedisAddr, "db", cfg.RedisDB, "key_prefix", cfg.RedisKeyPrefix)
	}
	locationService := location.NewService(locationStore, hub, logger)

	router := httpapi.NewRouter(httpapi.RouterDeps{
		Logger:          logger,
		LocationService: locationService,
		Hub:             hub,
		WSReadBuffer:    cfg.WSReadBuffer,
		WSWriteBuffer:   cfg.WSWriteBuffer,
	})

	return &App{
		cfg:    cfg,
		logger: logger,
		httpServer: &http.Server{
			Addr:    cfg.HTTPAddr,
			Handler: router,
		},
		udpServer: udptransport.NewServer(cfg.UDPAddr, locationService, logger),
		hub:       hub,
		store:     locationStore,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	errCh := make(chan error, 2)

	go a.hub.Run(ctx)

	go func() {
		a.logger.Info("starting HTTP server", "addr", a.cfg.HTTPAddr)
		if err := a.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()

	go func() {
		a.logger.Info("starting UDP location server", "addr", a.cfg.UDPAddr)
		if err := a.udpServer.ListenAndServe(ctx); err != nil {
			errCh <- fmt.Errorf("udp server: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
		defer cancel()
		_ = a.shutdown(shutdownCtx)
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
	defer cancel()

	return a.shutdown(shutdownCtx)
}

func (a *App) shutdown(ctx context.Context) error {
	a.logger.Info("shutting down application")

	if err := a.udpServer.Shutdown(); err != nil {
		return err
	}

	if err := a.httpServer.Shutdown(ctx); err != nil {
		return err
	}

	if err := a.store.Close(); err != nil {
		return err
	}

	done := make(chan struct{})
	go func() {
		a.hub.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}

	a.logger.Info("application shutdown complete")
	return nil
}
