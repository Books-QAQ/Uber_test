package handlers

import (
	"net/http"

	"uber-test/backend/internal/location"
)

type LocationHandler struct {
	locationService *location.Service
}

func NewLocationHandler(locationService *location.Service) *LocationHandler {
	return &LocationHandler{locationService: locationService}
}

func (h *LocationHandler) ListLatest(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"items": h.locationService.ListLatest(),
	})
}
