package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"uber-test/backend/internal/api/http/middleware"
	"uber-test/backend/internal/auth"
	"uber-test/backend/internal/dispatch"
	"uber-test/backend/internal/model"
	"uber-test/backend/internal/order"
)

type OrderHandler struct {
	orderService *order.Service
	tripHandler  *TripHandler
	routeHandler *RouteHandler
	authService  *auth.Service
}

type createOrderRequest struct {
	PassengerID        string  `json:"passenger_id"`
	PickupLat          float64 `json:"pickup_lat"`
	PickupLng          float64 `json:"pickup_lng"`
	PickupAddress      string  `json:"pickup_address"`
	DestinationLat     float64 `json:"destination_lat"`
	DestinationLng     float64 `json:"destination_lng"`
	DestinationAddress string  `json:"destination_address"`
	EstimatedPrice     float64 `json:"estimated_price"`
	Pickup             *struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Address   string  `json:"address"`
	} `json:"pickup"`
	Destination *struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Address   string  `json:"address"`
	} `json:"destination"`
}

func NewOrderHandler(orderService *order.Service, tripHandler *TripHandler, routeHandler *RouteHandler, authService *auth.Service) *OrderHandler {
	return &OrderHandler{
		orderService: orderService,
		tripHandler:  tripHandler,
		routeHandler: routeHandler,
		authService:  authService,
	}
}

func (h *OrderHandler) Create(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	var rawReq createOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&rawReq); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return
	}
	req := normalizeCreateOrderInput(rawReq)

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

	writeJSON(w, http.StatusCreated, map[string]any{"item": h.enrichOrder(r, item)})
}

func normalizeCreateOrderInput(raw createOrderRequest) model.CreateOrderInput {
	input := model.CreateOrderInput{
		PassengerID:        raw.PassengerID,
		PickupLat:          raw.PickupLat,
		PickupLng:          raw.PickupLng,
		PickupAddress:      raw.PickupAddress,
		DestinationLat:     raw.DestinationLat,
		DestinationLng:     raw.DestinationLng,
		DestinationAddress: raw.DestinationAddress,
		EstimatedPrice:     raw.EstimatedPrice,
	}

	if raw.Pickup != nil {
		if input.PickupLat == 0 {
			input.PickupLat = raw.Pickup.Latitude
		}
		if input.PickupLng == 0 {
			input.PickupLng = raw.Pickup.Longitude
		}
		if input.PickupAddress == "" {
			input.PickupAddress = raw.Pickup.Address
		}
	}

	if raw.Destination != nil {
		if input.DestinationLat == 0 {
			input.DestinationLat = raw.Destination.Latitude
		}
		if input.DestinationLng == 0 {
			input.DestinationLng = raw.Destination.Longitude
		}
		if input.DestinationAddress == "" {
			input.DestinationAddress = raw.Destination.Address
		}
	}

	return input
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

	writeJSON(w, http.StatusOK, map[string]any{"items": h.enrichOrders(r, items)})
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
	if strings.HasSuffix(path, "/route") {
		h.getRoute(w, r, strings.TrimSuffix(path, "/route"))
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

	writeJSON(w, http.StatusOK, map[string]any{"item": h.enrichOrder(r, item)})
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

	writeJSON(w, http.StatusOK, map[string]any{"item": h.enrichOrder(r, item)})
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
		if errors.Is(err, order.ErrOrderAlreadyAccepted) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
		if errors.Is(err, dispatch.ErrDriverNotDispatched) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"item": h.enrichOrder(r, item)})
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

func (h *OrderHandler) getRoute(w http.ResponseWriter, r *http.Request, orderID string) {
	if h.routeHandler == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "route service unavailable"})
		return
	}
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

	h.routeHandler.GetByOrderID(w, r, orderID)
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

func (h *OrderHandler) enrichOrders(r *http.Request, items []model.Order) []model.Order {
	if len(items) == 0 {
		return items
	}

	enriched := make([]model.Order, 0, len(items))
	for _, item := range items {
		enriched = append(enriched, h.enrichOrder(r, item))
	}
	return enriched
}

func (h *OrderHandler) enrichOrder(r *http.Request, item model.Order) model.Order {
	if h.authService == nil || item.DriverID == "" {
		return item
	}

	profile, err := h.authService.GetDriverProfileByDriverID(r.Context(), item.DriverID)
	if err != nil {
		return item
	}

	item.DriverPlateNo = profile.PlateNo
	return item
}
