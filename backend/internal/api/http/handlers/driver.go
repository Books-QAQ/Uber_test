package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"uber-test/backend/internal/api/http/middleware"
	"uber-test/backend/internal/auth"
	"uber-test/backend/internal/location"
	"uber-test/backend/internal/model"
)

type DriverHandler struct {
	locationService *location.Service
	authService     *auth.Service
}

type setDriverStatusRequest struct {
	Status string `json:"status"`
}

type setDriverVehicleRequest struct {
	PlateNo string `json:"plate_no"`
}

func NewDriverHandler(locationService *location.Service, authService *auth.Service) *DriverHandler {
	return &DriverHandler{
		locationService: locationService,
		authService:     authService,
	}
}

func (h *DriverHandler) SetStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"error": "method not allowed",
		})
		return
	}

	driverID, ok := lastPathParam(r.URL.Path, "/api/v1/drivers/", "/status")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid driver status path",
		})
		return
	}
	if err := middleware.MustBeSelfOrAdmin(r, driverID); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": err.Error(),
		})
		return
	}

	var req setDriverStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid json body",
		})
		return
	}

	status := model.DriverStatus{
		DriverID:   driverID,
		Status:     strings.TrimSpace(req.Status),
		UpdatedAt:  time.Now().UTC(),
		SourceAddr: r.RemoteAddr,
	}
	if err := h.locationService.SetDriverStatus(r.Context(), status); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"item": status,
	})
}

func (h *DriverHandler) SetVehicle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"error": "method not allowed",
		})
		return
	}
	if h.authService == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{
			"error": "auth service unavailable",
		})
		return
	}

	driverID, ok := lastPathParam(r.URL.Path, "/api/v1/drivers/", "/vehicle")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid driver vehicle path",
		})
		return
	}
	if err := middleware.MustBeSelfOrAdmin(r, driverID); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": err.Error(),
		})
		return
	}

	var req setDriverVehicleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid json body",
		})
		return
	}
	if err := h.authService.UpsertDriverVehicle(r.Context(), driverID, req.PlateNo); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": err.Error(),
		})
		return
	}

	profile, err := h.authService.GetDriverProfileByDriverID(r.Context(), driverID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"item": profile,
	})
}

func (h *DriverHandler) ListNearby(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"error": "method not allowed",
		})
		return
	}

	lat, err := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid lat",
		})
		return
	}

	lng, err := strconv.ParseFloat(r.URL.Query().Get("lng"), 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid lng",
		})
		return
	}

	radiusM := 3000.0
	if raw := r.URL.Query().Get("radius_m"); raw != "" {
		parsed, parseErr := strconv.ParseFloat(raw, 64)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": "invalid radius_m",
			})
			return
		}
		radiusM = parsed
	}

	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": "invalid limit",
			})
			return
		}
		limit = parsed
	}

	items, err := h.locationService.FindNearby(r.Context(), model.NearbyQuery{
		Lat:     lat,
		Lng:     lng,
		RadiusM: radiusM,
		Limit:   limit,
	})
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

func lastPathParam(path, prefix, suffix string) (string, bool) {
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}

	value := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	value = strings.Trim(value, "/")
	if value == "" || strings.Contains(value, "/") {
		return "", false
	}

	return value, true
}
