package trip

import (
	"context"
	"testing"

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
