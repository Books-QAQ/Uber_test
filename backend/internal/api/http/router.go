package http

import (
	"log/slog"
	"net/http"
	"strings"

	"uber-test/backend/internal/api/http/handlers"
	"uber-test/backend/internal/api/http/middleware"
	wsapi "uber-test/backend/internal/api/ws"
	"uber-test/backend/internal/auth"
	"uber-test/backend/internal/dispatch"
	"uber-test/backend/internal/location"
	"uber-test/backend/internal/model"
	"uber-test/backend/internal/order"
	"uber-test/backend/internal/routeplan"
	"uber-test/backend/internal/trip"
)

type RouterDeps struct {
	Logger          *slog.Logger
	AccessLogEnabled bool
	AuthService     *auth.Service
	Authenticator   *middleware.Authenticator
	LocationService *location.Service
	OrderService    *order.Service
	TripService     *trip.Service
	DispatchService *dispatch.Service
	RouteService    *routeplan.Service
	Hub             *wsapi.Hub
	WSReadBuffer    int
	WSWriteBuffer   int
}

func NewRouter(deps RouterDeps) http.Handler {
	mux := http.NewServeMux()

	healthHandler := handlers.NewHealthHandler()
	authHandler := handlers.NewAuthHandler(deps.AuthService)
	locationHandler := handlers.NewLocationHandler(deps.LocationService)
	driverHandler := handlers.NewDriverHandler(deps.LocationService, deps.AuthService)
	routeHandler := handlers.NewRouteHandler(deps.RouteService)
	tripHandler := handlers.NewTripHandler(deps.TripService)
	orderHandler := handlers.NewOrderHandler(deps.OrderService, tripHandler, routeHandler, deps.AuthService)
	dispatchHandler := handlers.NewDispatchHandler(deps.DispatchService)
	requireAuth := deps.Authenticator.RequireAuth

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"service":"uber-test-backend","status":"ok"}`))
	})
	mux.HandleFunc("/healthz", healthHandler.Get)
	mux.HandleFunc("/api/v1/healthz", healthHandler.Get)
	mux.HandleFunc("/api/v1/auth/register", authHandler.Register)
	mux.HandleFunc("/api/v1/auth/login", authHandler.Login)
	mux.HandleFunc("/api/v1/routes/preview", routeHandler.GetPreview)
	mux.Handle("/api/v1/auth/me", requireAuth(http.HandlerFunc(authHandler.Me)))
	mux.Handle("/api/v1/drivers/", requireAuth(middleware.RequireRoles(model.RoleDriver, model.RoleAdmin)(http.HandlerFunc(routeDriverSubresources(driverHandler, orderHandler, dispatchHandler, routeHandler)))))
	mux.Handle("/api/v1/drivers/nearby", requireAuth(middleware.RequireRoles(model.RolePassenger, model.RoleDriver, model.RoleAdmin)(http.HandlerFunc(driverHandler.ListNearby))))
	mux.Handle("/api/v1/drivers/locations", requireAuth(middleware.RequireRoles(model.RoleAdmin)(http.HandlerFunc(locationHandler.ListLatest))))
	mux.Handle("/api/v1/orders", requireAuth(middleware.RequireRoles(model.RolePassenger, model.RoleDriver, model.RoleAdmin)(http.HandlerFunc(routeOrderCollection(orderHandler)))))
	mux.Handle("/api/v1/orders/", requireAuth(middleware.RequireRoles(model.RolePassenger, model.RoleDriver, model.RoleAdmin)(http.HandlerFunc(orderHandler.GetOrUpdateStatus))))
	mux.Handle("/api/v1/trips", requireAuth(middleware.RequireRoles(model.RoleAdmin)(http.HandlerFunc(tripHandler.List))))
	mux.Handle("/ws/location", requireAuth(wsapi.NewHandler(deps.Hub, deps.WSReadBuffer, deps.WSWriteBuffer)))

	return withLogging(deps.Logger, deps.AccessLogEnabled, mux)
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

func routeDriverSubresources(driverHandler *handlers.DriverHandler, orderHandler *handlers.OrderHandler, dispatchHandler *handlers.DispatchHandler, routeHandler *handlers.RouteHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/status"):
			driverHandler.SetStatus(w, r)
		case strings.HasSuffix(r.URL.Path, "/vehicle"):
			driverHandler.SetVehicle(w, r)
		case strings.HasSuffix(r.URL.Path, "/current-order"):
			orderHandler.GetCurrentByDriver(w, r)
		case strings.HasSuffix(r.URL.Path, "/dispatches"):
			dispatchHandler.ListPendingByDriver(w, r)
		case strings.HasSuffix(r.URL.Path, "/route"):
			routeHandler.UpsertByDriver(w, r)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"route not found"}`))
		}
	}
}

func withLogging(logger *slog.Logger, enabled bool, next http.Handler) http.Handler {
	if !enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Info("http request", "method", r.Method, "path", r.URL.Path, "remote_addr", r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}
