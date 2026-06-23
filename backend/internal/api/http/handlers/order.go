package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"uber-test/backend/internal/api/http/middleware"
	"uber-test/backend/internal/auth"
	"uber-test/backend/internal/model"
	"uber-test/backend/internal/order"
)

type OrderHandler struct {
	orderService *order.Service
	tripHandler  *TripHandler
}

func NewOrderHandler(orderService *order.Service, tripHandler *TripHandler) *OrderHandler {
	return &OrderHandler{
		orderService: orderService,
		tripHandler:  tripHandler,
	}
}

func (h *OrderHandler) Create(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	var req model.CreateOrderInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return
	}

	principal, err := middleware.CurrentPrincipal(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": err.Error()})
		return
	}
	switch principal.Role {
	case model.RolePassenger:
		req.PassengerID = principal.UserID
	case model.RoleAdmin:
		if req.PassengerID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "admin create order requires passenger_id"})
			return
		}
	default:
		writeJSON(w, http.StatusForbidden, map[string]any{"error": auth.ErrForbidden.Error()})
		return
	}

	item, err := h.orderService.Create(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"item": item})
}

func (h *OrderHandler) List(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	principal, err := middleware.CurrentPrincipal(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": err.Error()})
		return
	}

	var items []model.Order
	switch principal.Role {
	case model.RolePassenger:
		items, err = h.orderService.ListByPassengerID(r.Context(), principal.UserID)
	case model.RoleDriver:
		items, err = h.orderService.ListByDriverID(r.Context(), principal.DriverID)
	case model.RoleAdmin:
		items, err = h.orderService.List(r.Context())
	default:
		writeJSON(w, http.StatusForbidden, map[string]any{"error": auth.ErrForbidden.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *OrderHandler) GetOrUpdateStatus(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/orders/")
	path = strings.Trim(path, "/")
	if path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing order id"})
		return
	}

	if strings.HasSuffix(path, "/status") {
		h.updateStatus(w, r, strings.TrimSuffix(path, "/status"))
		return
	}
	if strings.HasSuffix(path, "/trip") {
		h.getTrip(w, r, strings.TrimSuffix(path, "/trip"))
		return
	}

	h.getByID(w, r, path)
}

func (h *OrderHandler) GetCurrentByDriver(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	driverID, ok := lastPathParam(r.URL.Path, "/api/v1/drivers/", "/current-order")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid current-order path"})
		return
	}
	if err := middleware.MustBeSelfOrAdmin(r, driverID); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
		return
	}

	item, err := h.orderService.GetCurrentByDriverID(r.Context(), driverID)
	if err != nil {
		if errors.Is(err, order.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"item": item})
}

func (h *OrderHandler) getByID(w http.ResponseWriter, r *http.Request, orderID string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	item, err := h.orderService.GetByID(r.Context(), orderID)
	if err != nil {
		if errors.Is(err, order.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if err := authorizeOrderAccess(r, item); err != nil {
		status := http.StatusForbidden
		if errors.Is(err, auth.ErrUnauthorized) {
			status = http.StatusUnauthorized
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"item": item})
}

func (h *OrderHandler) updateStatus(w http.ResponseWriter, r *http.Request, orderID string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	var req model.UpdateOrderStatusInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return
	}

	currentOrder, err := h.orderService.GetByID(r.Context(), orderID)
	if err != nil {
		if errors.Is(err, order.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if err := authorizeOrderStatusUpdate(r, currentOrder, req); err != nil {
		status := http.StatusForbidden
		if errors.Is(err, auth.ErrUnauthorized) {
			status = http.StatusUnauthorized
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}

	item, err := h.orderService.UpdateStatus(r.Context(), orderID, req)
	if err != nil {
		if errors.Is(err, order.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		if errors.Is(err, order.ErrDriverBusy) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"item": item})
}

func (h *OrderHandler) getTrip(w http.ResponseWriter, r *http.Request, orderID string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	orderItem, err := h.orderService.GetByID(r.Context(), orderID)
	if err != nil {
		if errors.Is(err, order.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if err := authorizeOrderAccess(r, orderItem); err != nil {
		status := http.StatusForbidden
		if errors.Is(err, auth.ErrUnauthorized) {
			status = http.StatusUnauthorized
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}

	h.tripHandler.GetByOrderID(w, r, orderID)
}

func authorizeOrderAccess(r *http.Request, orderItem model.Order) error {
	principal, err := middleware.CurrentPrincipal(r)
	if err != nil {
		return err
	}

	switch principal.Role {
	case model.RoleAdmin:
		return nil
	case model.RolePassenger:
		if orderItem.PassengerID == principal.UserID {
			return nil
		}
	case model.RoleDriver:
		if orderItem.DriverID == principal.DriverID {
			return nil
		}
	}

	return auth.ErrForbidden
}

func authorizeOrderStatusUpdate(r *http.Request, orderItem model.Order, req model.UpdateOrderStatusInput) error {
	principal, err := middleware.CurrentPrincipal(r)
	if err != nil {
		return err
	}

	switch principal.Role {
	case model.RoleAdmin:
		return nil
	case model.RolePassenger:
		if orderItem.PassengerID != principal.UserID {
			return auth.ErrForbidden
		}
		if req.Status != model.OrderStatusCancelled && req.Status != model.OrderStatusPaid {
			return auth.ErrForbidden
		}
		return nil
	case model.RoleDriver:
		driverID := req.DriverID
		if driverID == "" {
			driverID = orderItem.DriverID
		}
		if driverID != principal.DriverID {
			return auth.ErrForbidden
		}
		switch req.Status {
		case model.OrderStatusAccepted, model.OrderStatusDriverArrived, model.OrderStatusInTrip, model.OrderStatusCompleted:
			return nil
		default:
			return auth.ErrForbidden
		}
	default:
		return auth.ErrForbidden
	}
}
