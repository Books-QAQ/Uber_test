package handlers

import (
	"errors"
	"net/http"

	"uber-test/backend/internal/api/http/middleware"
	"uber-test/backend/internal/trip"
)

type TripHandler struct {
	tripService *trip.Service
}

func NewTripHandler(tripService *trip.Service) *TripHandler {
	return &TripHandler{tripService: tripService}
}

func (h *TripHandler) List(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	items, err := h.tripService.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *TripHandler) GetByOrderID(w http.ResponseWriter, r *http.Request, orderID string) {
	if _, err := middleware.CurrentPrincipal(r); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": err.Error()})
		return
	}

	item, err := h.tripService.GetByOrderID(r.Context(), orderID)
	if err != nil {
		if errors.Is(err, trip.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"item": item})
}
