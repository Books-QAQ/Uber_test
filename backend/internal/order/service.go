package order

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"uber-test/backend/internal/model"
)

type Service struct {
	store              Store
	driverStatusWriter DriverStatusWriter
	tripLifecycle      TripLifecycleWriter
}

type DriverStatusWriter interface {
	SetDriverStatus(ctx context.Context, status model.DriverStatus) error
}

type TripLifecycleWriter interface {
	SyncWithOrder(ctx context.Context, order model.Order, input model.UpdateOrderStatusInput) (model.Trip, error)
}

func NewService(store Store, driverStatusWriter DriverStatusWriter, tripLifecycle TripLifecycleWriter) *Service {
	return &Service{
		store:              store,
		driverStatusWriter: driverStatusWriter,
		tripLifecycle:      tripLifecycle,
	}
}

func (s *Service) Create(ctx context.Context, input model.CreateOrderInput) (model.Order, error) {
	if input.PassengerID == "" {
		return model.Order{}, fmt.Errorf("create order: missing passenger_id")
	}

	now := time.Now().UTC()
	order := model.Order{
		ID:                 newOrderID(),
		PassengerID:        input.PassengerID,
		Status:             model.OrderStatusPendingDispatch,
		PickupLat:          input.PickupLat,
		PickupLng:          input.PickupLng,
		PickupAddress:      input.PickupAddress,
		DestinationLat:     input.DestinationLat,
		DestinationLng:     input.DestinationLng,
		DestinationAddress: input.DestinationAddress,
		EstimatedPrice:     input.EstimatedPrice,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	if err := s.store.Create(ctx, order); err != nil {
		return model.Order{}, err
	}

	return order, nil
}

func (s *Service) GetByID(ctx context.Context, id string) (model.Order, error) {
	return s.store.GetByID(ctx, id)
}

func (s *Service) List(ctx context.Context) ([]model.Order, error) {
	return s.store.List(ctx)
}

func (s *Service) ListByPassengerID(ctx context.Context, passengerID string) ([]model.Order, error) {
	if passengerID == "" {
		return nil, fmt.Errorf("list orders: missing passenger_id")
	}
	return s.store.ListByPassengerID(ctx, passengerID)
}

func (s *Service) ListByDriverID(ctx context.Context, driverID string) ([]model.Order, error) {
	if driverID == "" {
		return nil, fmt.Errorf("list orders: missing driver_id")
	}
	return s.store.ListByDriverID(ctx, driverID)
}

func (s *Service) GetCurrentByDriverID(ctx context.Context, driverID string) (model.Order, error) {
	if driverID == "" {
		return model.Order{}, fmt.Errorf("get current order: missing driver_id")
	}
	return s.store.FindActiveByDriverID(ctx, driverID)
}

func (s *Service) UpdateStatus(ctx context.Context, id string, input model.UpdateOrderStatusInput) (model.Order, error) {
	order, err := s.store.GetByID(ctx, id)
	if err != nil {
		return model.Order{}, err
	}

	if err := validateStatusUpdate(order, input); err != nil {
		return model.Order{}, err
	}
	if err := s.ensureDriverAvailableForAcceptance(ctx, order, input); err != nil {
		return model.Order{}, err
	}

	if !isValidOrderTransition(order.Status, input.Status) {
		return model.Order{}, fmt.Errorf("invalid order status transition: %s -> %s", order.Status, input.Status)
	}

	order.Status = input.Status
	if input.DriverID != "" {
		order.DriverID = input.DriverID
	}
	if input.Status == model.OrderStatusPaid && order.FinalPrice == 0 {
		order.FinalPrice = order.EstimatedPrice
	}
	order.UpdatedAt = time.Now().UTC()

	if err := s.syncTrip(ctx, &order, input); err != nil {
		return model.Order{}, err
	}

	if err := s.store.Update(ctx, order); err != nil {
		return model.Order{}, err
	}

	if err := s.syncDriverStatus(ctx, order, input.Status); err != nil {
		return model.Order{}, err
	}

	return order, nil
}

func (s *Service) ensureDriverAvailableForAcceptance(ctx context.Context, order model.Order, input model.UpdateOrderStatusInput) error {
	if input.Status != model.OrderStatusAccepted {
		return nil
	}

	driverID := input.DriverID
	if driverID == "" {
		driverID = order.DriverID
	}
	if driverID == "" {
		return nil
	}

	active, err := s.store.FindActiveByDriverID(ctx, driverID)
	if err == nil && active.ID != order.ID {
		return ErrDriverBusy
	}
	if err != nil && err != ErrNotFound {
		return err
	}

	return nil
}

func validateStatusUpdate(order model.Order, input model.UpdateOrderStatusInput) error {
	if input.Status == "" {
		return fmt.Errorf("update order status: missing status")
	}

	if input.Status == model.OrderStatusAccepted && input.DriverID == "" && order.DriverID == "" {
		return fmt.Errorf("update order status: accepted order requires driver_id")
	}

	if requiresAssignedDriver(input.Status) && order.DriverID == "" && input.DriverID == "" {
		return fmt.Errorf("update order status: status %s requires an assigned driver", input.Status)
	}

	return nil
}

func requiresAssignedDriver(status string) bool {
	switch status {
	case model.OrderStatusAccepted, model.OrderStatusDriverArrived, model.OrderStatusInTrip, model.OrderStatusCompleted, model.OrderStatusToBePaid, model.OrderStatusPaid:
		return true
	default:
		return false
	}
}

func (s *Service) syncDriverStatus(ctx context.Context, order model.Order, orderStatus string) error {
	if s.driverStatusWriter == nil || order.DriverID == "" {
		return nil
	}

	driverStatus := ""
	switch orderStatus {
	case model.OrderStatusAccepted, model.OrderStatusDriverArrived:
		driverStatus = model.DriverStatusToPickup
	case model.OrderStatusInTrip:
		driverStatus = model.DriverStatusInTrip
	case model.OrderStatusCompleted, model.OrderStatusToBePaid, model.OrderStatusPaid, model.OrderStatusCancelled:
		driverStatus = model.DriverStatusOnline
	default:
		return nil
	}

	return s.driverStatusWriter.SetDriverStatus(ctx, model.DriverStatus{
		DriverID:  order.DriverID,
		Status:    driverStatus,
		UpdatedAt: time.Now().UTC(),
	})
}

func (s *Service) syncTrip(ctx context.Context, order *model.Order, input model.UpdateOrderStatusInput) error {
	if s.tripLifecycle == nil {
		return nil
	}

	switch order.Status {
	case model.OrderStatusAccepted, model.OrderStatusDriverArrived, model.OrderStatusInTrip, model.OrderStatusCompleted, model.OrderStatusToBePaid, model.OrderStatusPaid:
	default:
		return nil
	}

	trip, err := s.tripLifecycle.SyncWithOrder(ctx, *order, input)
	if err != nil {
		return err
	}
	if trip.FinalPrice > 0 {
		order.FinalPrice = trip.FinalPrice
	}

	return nil
}

func isValidOrderTransition(from, to string) bool {
	if from == to {
		return true
	}

	allowed := map[string]map[string]bool{
		model.OrderStatusCreated: {
			model.OrderStatusPendingDispatch: true,
			model.OrderStatusCancelled:       true,
		},
		model.OrderStatusPendingDispatch: {
			model.OrderStatusAccepted:  true,
			model.OrderStatusCancelled: true,
		},
		model.OrderStatusAccepted: {
			model.OrderStatusDriverArrived: true,
			model.OrderStatusCancelled:     true,
		},
		model.OrderStatusDriverArrived: {
			model.OrderStatusInTrip:    true,
			model.OrderStatusCancelled: true,
		},
		model.OrderStatusInTrip: {
			model.OrderStatusCompleted: true,
		},
		model.OrderStatusCompleted: {
			model.OrderStatusToBePaid: true,
			model.OrderStatusPaid:     true,
		},
		model.OrderStatusToBePaid: {
			model.OrderStatusPaid: true,
		},
	}

	return allowed[from][to]
}

func isOrderActive(status string) bool {
	switch status {
	case model.OrderStatusAccepted, model.OrderStatusDriverArrived, model.OrderStatusInTrip:
		return true
	default:
		return false
	}
}

func newOrderID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("order-%d", time.Now().UnixNano())
	}
	return "order-" + hex.EncodeToString(buf[:])
}
