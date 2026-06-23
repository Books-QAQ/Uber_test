package handlers

import (
	"context"
	"net/http"

	"uber-test/backend/internal/api/http/middleware"
	"uber-test/backend/internal/model"
)

type DispatchReader interface {
	ListPendingAssignmentsByDriverID(ctx context.Context, driverID string) ([]model.DispatchAssignment, error)
}

type DispatchHandler struct {
	dispatchService DispatchReader
}

func NewDispatchHandler(dispatchService DispatchReader) *DispatchHandler {
	return &DispatchHandler{dispatchService: dispatchService}
}

func (h *DispatchHandler) ListPendingByDriver(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	driverID, ok := lastPathParam(r.URL.Path, "/api/v1/drivers/", "/dispatches")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid dispatch path"})
		return
	}
	if err := middleware.MustBeSelfOrAdmin(r, driverID); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
		return
	}

	items, err := h.dispatchService.ListPendingAssignmentsByDriverID(r.Context(), driverID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
