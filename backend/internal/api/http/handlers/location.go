package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"uber-test/backend/internal/api/http/middleware"
	"uber-test/backend/internal/location"
	"uber-test/backend/internal/model"
)

type LocationHandler struct {
	locationService *location.Service
}

type upsertDriverLocationRequest struct {
	OrderID   string  `json:"order_id"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	SpeedKPH  float64 `json:"speed_kph"`
	Heading   float64 `json:"heading"`
	AccuracyM float64 `json:"accuracy_m"`
}

type touchDriverHeartbeatRequest struct {
	OrderID string `json:"order_id"`
}

func NewLocationHandler(locationService *location.Service) *LocationHandler {
	return &LocationHandler{locationService: locationService}
}

func (h *LocationHandler) ListLatest(w http.ResponseWriter, r *http.Request) {
	items, err := h.locationService.ListLatest(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
	})
}

func (h *LocationHandler) GetLatestByDriver(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	driverID, ok := lastPathParam(r.URL.Path, "/api/v1/drivers/", "/location")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid driver location path"})
		return
	}
	if err := middleware.MustBeSelfOrAdmin(r, driverID); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
		return
	}

	item, err := h.locationService.GetLatestByDriverID(r.Context(), driverID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"item": item})
}

func (h *LocationHandler) UpsertByDriver(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	driverID, ok := lastPathParam(r.URL.Path, "/api/v1/drivers/", "/location")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid driver location path"})
		return
	}
	if err := middleware.MustBeSelfOrAdmin(r, driverID); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
		return
	}

	var req upsertDriverLocationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return
	}

	location := model.DriverLocation{
		DriverID:   driverID,
		OrderID:    req.OrderID,
		Lat:        req.Lat,
		Lng:        req.Lng,
		SpeedKPH:   req.SpeedKPH,
		Heading:    req.Heading,
		AccuracyM:  req.AccuracyM,
		Timestamp:  time.Now().UTC(),
		SourceAddr: r.RemoteAddr,
	}
	if err := h.locationService.UpsertDriverLocation(r.Context(), location); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"item": location})
}

func (h *LocationHandler) TouchHeartbeatByDriver(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	driverID, ok := lastPathParam(r.URL.Path, "/api/v1/drivers/", "/heartbeat")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid driver heartbeat path"})
		return
	}
	if err := middleware.MustBeSelfOrAdmin(r, driverID); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
		return
	}

	var req touchDriverHeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return
	}

	heartbeat := model.DriverHeartbeat{
		DriverID:   driverID,
		OrderID:    req.OrderID,
		Timestamp:  time.Now().UTC(),
		SourceAddr: r.RemoteAddr,
	}
	if err := h.locationService.TouchHeartbeat(r.Context(), heartbeat); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"item": heartbeat})
}
