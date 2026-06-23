package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"uber-test/backend/internal/api/http/middleware"
	"uber-test/backend/internal/model"
	"uber-test/backend/internal/routeplan"
)

type RouteHandler struct {
	routeService *routeplan.Service
}

type upsertDriverRouteRequest struct {
	OrderID string             `json:"order_id"`
	Mode    string             `json:"mode"`
	Points  []model.RoutePoint `json:"points"`
}

func NewRouteHandler(routeService *routeplan.Service) *RouteHandler {
	return &RouteHandler{routeService: routeService}
}

func (h *RouteHandler) UpsertByDriver(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	driverID, ok := lastPathParam(r.URL.Path, "/api/v1/drivers/", "/route")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid driver route path"})
		return
	}
	if err := middleware.MustBeSelfOrAdmin(r, driverID); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
		return
	}

	var req upsertDriverRouteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return
	}

	route, err := h.routeService.Upsert(r.Context(), model.DriverRoute{
		DriverID:  driverID,
		OrderID:   strings.TrimSpace(req.OrderID),
		Mode:      strings.TrimSpace(req.Mode),
		Points:    req.Points,
		UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"item": route})
}

func (h *RouteHandler) GetByOrderID(w http.ResponseWriter, r *http.Request, orderID string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	item, err := h.routeService.GetByOrderID(r.Context(), orderID)
	if err != nil {
		if errors.Is(err, routeplan.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"item": item})
}
