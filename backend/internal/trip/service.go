package trip

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"time"

	"uber-test/backend/internal/model"
)

type Service struct {
	store       Store
	broadcaster Broadcaster
}

type Broadcaster interface {
	BroadcastJSON(v any)
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) SetBroadcaster(broadcaster Broadcaster) {
	s.broadcaster = broadcaster
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
		trip.FinalPrice = calculateCurrentFare(
			trip.ActualDistanceM,
			trip.ActualDurationS,
			trip.WaitingDurationS,
		)
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
	if trip.Status == model.TripStatusInTrip || trip.Status == model.TripStatusCompleted || trip.Status == model.TripStatusPaid {
		s.broadcastFareUpdate(trip)
	}

	return trip, nil
}

func (s *Service) GetByOrderID(ctx context.Context, orderID string) (model.Trip, error) {
	if orderID == "" {
		return model.Trip{}, fmt.Errorf("get trip: missing order_id")
	}
	trip, err := s.store.GetByOrderID(ctx, orderID)
	if err != nil {
		return model.Trip{}, err
	}
	points, err := s.store.ListPointsByTripID(ctx, trip.ID)
	if err == nil {
		trip.Points = points
	} else if err != ErrNotFound {
		return model.Trip{}, err
	}
	return trip, nil
}

func (s *Service) List(ctx context.Context) ([]model.Trip, error) {
	return s.store.List(ctx)
}

func (s *Service) RecordLocation(ctx context.Context, location model.DriverLocation) error {
	if location.OrderID == "" {
		return nil
	}

	trip, err := s.store.GetByOrderID(ctx, location.OrderID)
	if err != nil {
		if err == ErrNotFound {
			return nil
		}
		return err
	}
	if trip.Status != model.TripStatusPending && trip.Status != model.TripStatusInTrip {
		return nil
	}

	recordedAt := location.Timestamp
	if recordedAt.IsZero() {
		recordedAt = time.Now().UTC()
	}
	now := time.Now().UTC()
	point := model.TripPoint{
		ID:         newTripPointID(),
		TripID:     trip.ID,
		OrderID:    trip.OrderID,
		DriverID:   location.DriverID,
		TripStatus: trip.Status,
		Lat:        location.Lat,
		Lng:        location.Lng,
		SpeedKPH:   location.SpeedKPH,
		Heading:    location.Heading,
		AccuracyM:  location.AccuracyM,
		RecordedAt: recordedAt,
		CreatedAt:  now,
	}

	lastPoint, err := s.store.GetLastPointByTripID(ctx, trip.ID)
	hasLastPoint := err == nil
	if err != nil && err != ErrNotFound {
		return err
	}
	if hasLastPoint && isDuplicateTripPoint(lastPoint, point) {
		return nil
	}

	if err := s.store.SavePoint(ctx, point); err != nil {
		return err
	}

	if trip.Status != model.TripStatusInTrip {
		return nil
	}

	if hasLastPoint && lastPoint.TripStatus == model.TripStatusInTrip {
		trip.ActualDistanceM += int(math.Round(haversineMeters(lastPoint.Lat, lastPoint.Lng, point.Lat, point.Lng)))
	}
	if !trip.StartedAt.IsZero() {
		trip.ActualDurationS = maxInt(trip.ActualDurationS, int(recordedAt.Sub(trip.StartedAt).Seconds()))
	}
	trip.FinalPrice = calculateCurrentFare(
		trip.ActualDistanceM,
		trip.ActualDurationS,
		trip.WaitingDurationS,
	)
	trip.UpdatedAt = now
	if err := s.store.Save(ctx, trip); err != nil {
		return err
	}
	s.broadcastFareUpdate(trip)
	return nil
}

func calculateFare(order model.Order, input model.UpdateOrderStatusInput, trip model.Trip) float64 {
	if input.FinalPrice > 0 {
		return input.FinalPrice
	}
	if order.FinalPrice > 0 {
		return order.FinalPrice
	}

	return calculateCurrentFare(
		maxInt(input.ActualDistanceM, trip.ActualDistanceM),
		maxInt(input.ActualDurationS, trip.ActualDurationS),
		maxInt(input.WaitingDurationS, trip.WaitingDurationS),
	)
}

func calculateCurrentFare(actualDistanceM, actualDurationS, waitingDurationS int) float64 {
	distanceKM := float64(actualDistanceM) / 1000.0
	durationMin := float64(actualDurationS) / 60.0
	waitingMin := float64(waitingDurationS) / 60.0

	if distanceKM == 0 && durationMin == 0 && waitingMin == 0 {
		return 0
	}

	total := distanceKM*10.2 + durationMin*0.5 + waitingMin*0.8
	return roundTo2(total)
}

func (s *Service) broadcastFareUpdate(trip model.Trip) {
	if s.broadcaster == nil {
		return
	}

	s.broadcaster.BroadcastJSON(map[string]any{
		"type": "trip.fare.updated",
		"data": map[string]any{
			"trip_id":            trip.ID,
			"order_id":           trip.OrderID,
			"driver_id":          trip.DriverID,
			"status":             trip.Status,
			"actual_distance_m":  trip.ActualDistanceM,
			"actual_duration_s":  trip.ActualDurationS,
			"waiting_duration_s": trip.WaitingDurationS,
			"current_price":      trip.FinalPrice,
			"updated_at":         trip.UpdatedAt,
		},
	})
}

func newTripID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("trip-%d", time.Now().UnixNano())
	}
	return "trip-" + hex.EncodeToString(buf[:])
}

func newTripPointID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("trip-point-%d", time.Now().UnixNano())
	}
	return "trip-point-" + hex.EncodeToString(buf[:])
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

func isDuplicateTripPoint(a, b model.TripPoint) bool {
	if a.TripStatus != b.TripStatus {
		return false
	}
	if math.Abs(a.Lat-b.Lat) > 0.000001 || math.Abs(a.Lng-b.Lng) > 0.000001 {
		return false
	}
	return a.RecordedAt.Equal(b.RecordedAt)
}

func haversineMeters(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadiusM = 6371000.0

	lat1Rad := lat1 * math.Pi / 180
	lat2Rad := lat2 * math.Pi / 180
	deltaLat := (lat2 - lat1) * math.Pi / 180
	deltaLng := (lng2 - lng1) * math.Pi / 180

	a := math.Sin(deltaLat/2)*math.Sin(deltaLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*
			math.Sin(deltaLng/2)*math.Sin(deltaLng/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadiusM * c
}
