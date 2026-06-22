package http

import (
	"log/slog"
	"net/http"

	"uber-test/backend/internal/api/http/handlers"
	wsapi "uber-test/backend/internal/api/ws"
	"uber-test/backend/internal/location"
	"uber-test/backend/internal/order"
)

type RouterDeps struct {
	Logger          *slog.Logger
	LocationService *location.Service
	OrderService    *order.Service
	Hub             *wsapi.Hub
	WSReadBuffer    int
	WSWriteBuffer   int
}

func NewRouter(deps RouterDeps) http.Handler {
	mux := http.NewServeMux()

	healthHandler := handlers.NewHealthHandler()
	locationHandler := handlers.NewLocationHandler(deps.LocationService)
	driverHandler := handlers.NewDriverHandler(deps.LocationService)
	orderHandler := handlers.NewOrderHandler(deps.OrderService)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"service":"uber-test-backend","status":"ok"}`))
	})
	mux.HandleFunc("/healthz", healthHandler.Get)
	mux.HandleFunc("/api/v1/healthz", healthHandler.Get)
	mux.HandleFunc("/api/v1/drivers/nearby", driverHandler.ListNearby)
	mux.HandleFunc("/api/v1/drivers/", driverHandler.SetStatus)
	mux.HandleFunc("/api/v1/drivers/locations", locationHandler.ListLatest)
	mux.HandleFunc("/api/v1/orders", routeOrderCollection(orderHandler))
	mux.HandleFunc("/api/v1/orders/", orderHandler.GetOrUpdateStatus)
	mux.Handle("/ws/location", wsapi.NewHandler(deps.Hub, deps.WSReadBuffer, deps.WSWriteBuffer))

	return withLogging(deps.Logger, mux)
}

func routeOrderCollection(handler *handlers.OrderHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			handler.Create(w, r)
		case http.MethodGet:
			handler.List(w, r)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte(`{"error":"method not allowed"}`))
		}
	}
}

func withLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Info("http request", "method", r.Method, "path", r.URL.Path, "remote_addr", r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}
