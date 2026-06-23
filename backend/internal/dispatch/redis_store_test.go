package dispatch

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"uber-test/backend/internal/model"
)

func TestRedisStoreCreateBatchAndListPendingByDriverID(t *testing.T) {
	t.Parallel()

	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mini.Close()

	store, err := NewRedisStore(context.Background(), RedisConfig{
		Addr:      mini.Addr(),
		KeyPrefix: "test",
		TTL:       time.Hour,
	})
	if err != nil {
		t.Fatalf("new redis dispatch store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 23, 1, 0, 0, 0, time.UTC)
	records := []model.DispatchRecord{
		{
			ID:            "dispatch-1",
			OrderID:       "order-1",
			DriverID:      "driver-1",
			Status:        model.DispatchStatusPending,
			DistanceM:     320,
			DispatchRound: 1,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		{
			ID:            "dispatch-2",
			OrderID:       "order-2",
			DriverID:      "driver-1",
			Status:        model.DispatchStatusPending,
			DistanceM:     120,
			DispatchRound: 1,
			CreatedAt:     now.Add(time.Second),
			UpdatedAt:     now.Add(time.Second),
		},
	}

	if err := store.CreateBatch(context.Background(), records); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	items, err := store.ListPendingByDriverID(context.Background(), "driver-1")
	if err != nil {
		t.Fatalf("list pending by driver: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 pending records, got %d", len(items))
	}
	if items[0].ID != "dispatch-2" || items[1].ID != "dispatch-1" {
		t.Fatalf("unexpected dispatch order: %+v", items)
	}
}

func TestRedisStoreMarkAccepted(t *testing.T) {
	t.Parallel()

	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mini.Close()

	store, err := NewRedisStore(context.Background(), RedisConfig{
		Addr:      mini.Addr(),
		KeyPrefix: "test",
		TTL:       time.Hour,
	})
	if err != nil {
		t.Fatalf("new redis dispatch store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 23, 1, 5, 0, 0, time.UTC)
	if err := store.CreateBatch(context.Background(), []model.DispatchRecord{
		{
			ID:            "dispatch-1",
			OrderID:       "order-1",
			DriverID:      "driver-1",
			Status:        model.DispatchStatusPending,
			DistanceM:     100,
			DispatchRound: 1,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		{
			ID:            "dispatch-2",
			OrderID:       "order-1",
			DriverID:      "driver-2",
			Status:        model.DispatchStatusPending,
			DistanceM:     200,
			DispatchRound: 1,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	}); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	if err := store.MarkAccepted(context.Background(), "order-1", "driver-1", now.Add(2*time.Second)); err != nil {
		t.Fatalf("mark accepted: %v", err)
	}

	items, err := store.ListPendingByDriverID(context.Background(), "driver-1")
	if err != nil {
		t.Fatalf("list pending after accept: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no pending records after accept, got %d", len(items))
	}

	if _, err := store.GetPendingByOrderAndDriver(context.Background(), "order-1", "driver-2"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for expired pending dispatch, got %v", err)
	}
}
