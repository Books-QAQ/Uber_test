package trip

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"uber-test/backend/internal/model"
)

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) SyncWithOrder(ctx context.Context, order model.Order, input model.UpdateOrderStatusInput) (model.Trip, error) {
	trip, err := s.store.GetByOrderID(ctx, order.ID)
	if err != nil && err != ErrNotFound {
		return model.Trip{}, err
	}

	if err == ErrNotFound {
		trip = model.Trip{
			ID:             newTripID(),
			OrderID:        order.ID,
			PassengerID:    order.PassengerID,
			DriverID:       order.DriverID,
			Status:         model.TripStatusPending,
			EstimatedPrice: order.EstimatedPrice,
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		}
	}

	trip.DriverID = order.DriverID
	trip.UpdatedAt = time.Now().UTC()

	switch order.Status {
	case model.OrderStatusAccepted, model.OrderStatusDriverArrived:
		trip.Status = model.TripStatusPending
	case model.OrderStatusInTrip:
		trip.Status = model.TripStatusInTrip
		if trip.StartedAt.IsZero() {
			trip.StartedAt = time.Now().UTC()
		}
	case model.OrderStatusCompleted, model.OrderStatusToBePaid, model.OrderStatusPaid:
		if trip.StartedAt.IsZero() {
			trip.StartedAt = order.UpdatedAt
		}
		trip.Status = model.TripStatusCompleted
		if order.Status == model.OrderStatusPaid {
			trip.Status = model.TripStatusPaid
		}
		trip.EndedAt = time.Now().UTC()
		trip.ActualDistanceM = maxInt(input.ActualDistanceM, trip.ActualDistanceM)
		trip.ActualDurationS = maxInt(input.ActualDurationS, trip.ActualDurationS)
		trip.WaitingDurationS = maxInt(input.WaitingDurationS, trip.WaitingDurationS)
		trip.FinalPrice = calculateFare(order, input, trip)
	}

	if err := s.store.Save(ctx, trip); err != nil {
		return model.Trip{}, err
	}

	return trip, nil
}

func (s *Service) GetByOrderID(ctx context.Context, orderID string) (model.Trip, error) {
	if orderID == "" {
		return model.Trip{}, fmt.Errorf("get trip: missing order_id")
	}
	return s.store.GetByOrderID(ctx, orderID)
}

func (s *Service) List(ctx context.Context) ([]model.Trip, error) {
	return s.store.List(ctx)
}

func calculateFare(order model.Order, input model.UpdateOrderStatusInput, trip model.Trip) float64 {
	if input.FinalPrice > 0 {
		return input.FinalPrice
	}
	if order.FinalPrice > 0 {
		return order.FinalPrice
	}

	distanceKM := float64(maxInt(input.ActualDistanceM, trip.ActualDistanceM)) / 1000.0
	durationMin := float64(maxInt(input.ActualDurationS, trip.ActualDurationS)) / 60.0
	waitingMin := float64(maxInt(input.WaitingDurationS, trip.WaitingDurationS)) / 60.0

	if distanceKM == 0 && durationMin == 0 && waitingMin == 0 {
		if order.EstimatedPrice > 0 {
			return order.EstimatedPrice
		}
		return 14
	}

	total := 14.0 + distanceKM*2.8 + durationMin*0.5 + waitingMin*0.8
	if total < order.EstimatedPrice {
		return order.EstimatedPrice
	}
	return roundTo2(total)
}

func newTripID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("trip-%d", time.Now().UnixNano())
	}
	return "trip-" + hex.EncodeToString(buf[:])
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func roundTo2(value float64) float64 {
	return float64(int(value*100+0.5)) / 100
}
