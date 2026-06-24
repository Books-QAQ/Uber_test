package trip

import (
	"context"
	"testing"
	"time"

	"uber-test/backend/internal/model"
)

func TestServiceSyncWithOrderLifecycle(t *testing.T) {
	t.Parallel()

	service := NewService(NewMemoryStore())

	order := model.Order{
		ID:             "order-trip-1",
		PassengerID:    "passenger-1",
		DriverID:       "driver-1",
		Status:         model.OrderStatusAccepted,
		EstimatedPrice: 25,
	}

	trip, err := service.SyncWithOrder(context.Background(), order, model.UpdateOrderStatusInput{
		Status: model.OrderStatusAccepted,
	})
	if err != nil {
		t.Fatalf("sync accepted: %v", err)
	}
	if trip.Status != model.TripStatusPending {
		t.Fatalf("expected pending trip, got %s", trip.Status)
	}

	order.Status = model.OrderStatusInTrip
	trip, err = service.SyncWithOrder(context.Background(), order, model.UpdateOrderStatusInput{
		Status: model.OrderStatusInTrip,
	})
	if err != nil {
		t.Fatalf("sync in_trip: %v", err)
	}
	if trip.Status != model.TripStatusInTrip || trip.StartedAt.IsZero() {
		t.Fatalf("expected started in-trip record, got %+v", trip)
	}

	order.Status = model.OrderStatusCompleted
	trip, err = service.SyncWithOrder(context.Background(), order, model.UpdateOrderStatusInput{
		Status:           model.OrderStatusCompleted,
		ActualDistanceM:  5200,
		ActualDurationS:  900,
		WaitingDurationS: 120,
	})
	if err != nil {
		t.Fatalf("sync completed: %v", err)
	}
	if trip.Status != model.TripStatusCompleted {
		t.Fatalf("expected completed trip, got %s", trip.Status)
	}
	if trip.FinalPrice <= 0 {
		t.Fatalf("expected positive final price, got %f", trip.FinalPrice)
	}
}

func TestServiceRecordLocationPersistsTripPoints(t *testing.T) {
	t.Parallel()

	service := NewService(NewMemoryStore())
	ctx := context.Background()

	order := model.Order{
		ID:             "order-trip-points-1",
		PassengerID:    "passenger-1",
		DriverID:       "driver-1",
		Status:         model.OrderStatusAccepted,
		EstimatedPrice: 25,
	}

	if _, err := service.SyncWithOrder(ctx, order, model.UpdateOrderStatusInput{Status: model.OrderStatusAccepted}); err != nil {
		t.Fatalf("sync accepted: %v", err)
	}

	if err := service.RecordLocation(ctx, model.DriverLocation{
		DriverID:  "driver-1",
		OrderID:   order.ID,
		Lat:       31.2304,
		Lng:       121.4737,
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record pending trip location: %v", err)
	}

	order.Status = model.OrderStatusInTrip
	if _, err := service.SyncWithOrder(ctx, order, model.UpdateOrderStatusInput{Status: model.OrderStatusInTrip}); err != nil {
		t.Fatalf("sync in_trip: %v", err)
	}

	baseTime := time.Now().UTC()
	points := []model.DriverLocation{
		{DriverID: "driver-1", OrderID: order.ID, Lat: 31.2305, Lng: 121.4738, Timestamp: baseTime},
		{DriverID: "driver-1", OrderID: order.ID, Lat: 31.2310, Lng: 121.4743, Timestamp: baseTime.Add(3 * time.Second)},
	}
	for _, point := range points {
		if err := service.RecordLocation(ctx, point); err != nil {
			t.Fatalf("record in-trip location: %v", err)
		}
	}

	trip, err := service.GetByOrderID(ctx, order.ID)
	if err != nil {
		t.Fatalf("get trip: %v", err)
	}
	if len(trip.Points) != 3 {
		t.Fatalf("expected 3 persisted trip points, got %d", len(trip.Points))
	}
	if trip.ActualDistanceM <= 0 {
		t.Fatalf("expected accumulated actual distance, got %d", trip.ActualDistanceM)
	}
	if trip.ActualDurationS < 3 {
		t.Fatalf("expected actual duration to be updated, got %d", trip.ActualDurationS)
	}
}
