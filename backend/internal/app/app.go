package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	httpapi "uber-test/backend/internal/api/http"
	httpmiddleware "uber-test/backend/internal/api/http/middleware"
	wsapi "uber-test/backend/internal/api/ws"
	"uber-test/backend/internal/auth"
	"uber-test/backend/internal/config"
	"uber-test/backend/internal/location"
	"uber-test/backend/internal/order"
	mysqlstorage "uber-test/backend/internal/storage/mysql"
	udptransport "uber-test/backend/internal/transport/udp"
	"uber-test/backend/internal/trip"
)

type App struct {
	cfg        config.Config
	logger     *slog.Logger
	httpServer *http.Server
	udpServer  *udptransport.Server
	hub        *wsapi.Hub
	store      location.Store
	db         *sql.DB
}

func New(cfg config.Config, logger *slog.Logger) (*App, error) {
	hub := wsapi.NewHub(logger)
	tokenManager := auth.NewTokenManager(cfg.AuthJWTSecret, cfg.AuthTokenTTL)
	authStore := auth.Store(auth.NewMemoryStore())
	authenticator := httpmiddleware.NewAuthenticator(tokenManager)
	locationStore := location.Store(location.NewMemoryStore(cfg.RecentLocationLimit))
	orderStore := order.Store(order.NewMemoryStore())
	tripStore := trip.Store(trip.NewMemoryStore())
	var db *sql.DB

	if cfg.MySQLEnabled {
		openedDB, err := mysqlstorage.Open(context.Background(), mysqlstorage.Config{
			DSN:             cfg.MySQLDSN,
			MaxOpenConns:    cfg.MySQLMaxOpenConns,
			MaxIdleConns:    cfg.MySQLMaxIdleConns,
			ConnMaxLifetime: cfg.MySQLConnMaxLifetime,
		})
		if err != nil {
			return nil, fmt.Errorf("open mysql storage: %w", err)
		}
		if err := mysqlstorage.Migrate(context.Background(), openedDB); err != nil {
			_ = openedDB.Close()
			return nil, fmt.Errorf("migrate mysql storage: %w", err)
		}

		db = openedDB
		authStore = auth.NewMySQLStore(openedDB)
		orderStore = order.NewMySQLStore(openedDB)
		tripStore = trip.NewMySQLStore(openedDB)
		logger.Info("mysql-backed auth/order/trip stores enabled")
	}

	authService := auth.NewService(authStore, tokenManager)
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
	tripService := trip.NewService(tripStore)
	orderService := order.NewService(orderStore, locationService, tripService)

	router := httpapi.NewRouter(httpapi.RouterDeps{
		Logger:          logger,
		AuthService:     authService,
		Authenticator:   authenticator,
		LocationService: locationService,
		OrderService:    orderService,
		TripService:     tripService,
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
		db:        db,
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

	if a.db != nil {
		if err := a.db.Close(); err != nil {
			return err
		}
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
