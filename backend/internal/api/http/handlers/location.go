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
