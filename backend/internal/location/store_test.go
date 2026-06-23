package location

import (
	"context"
	"testing"
	"time"

	"uber-test/backend/internal/model"
)

func TestMemoryStoreFindNearbyUsesRTreeLatestPosition(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore(5)
	now := time.Date(2026, 6, 23, 6, 0, 0, 0, time.UTC)

	if err := store.SetDriverStatus(context.Background(), model.DriverStatus{
		DriverID:  "driver-1",
		Status:    model.DriverStatusOnline,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("set driver status: %v", err)
	}

	if err := store.Upsert(context.Background(), model.DriverLocation{
		DriverID:  "driver-1",
		Lat:       31.2304,
		Lng:       121.4737,
		Timestamp: now,
	}); err != nil {
		t.Fatalf("upsert initial location: %v", err)
	}

	if err := store.Upsert(context.Background(), model.DriverLocation{
		DriverID:  "driver-1",
		Lat:       31.2804,
		Lng:       121.5237,
		Timestamp: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("upsert moved location: %v", err)
	}

	items, err := store.FindNearby(context.Background(), model.NearbyQuery{
		Lat:      31.2304,
		Lng:      121.4737,
		RadiusM:  800,
		Limit:    5,
		OnlyLive: true,
	})
	if err != nil {
		t.Fatalf("find nearby: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected moved driver to be absent from old search area, got %d items", len(items))
	}

	if got := store.index.Size(); got != 1 {
		t.Fatalf("expected exactly one indexed driver location, got %d", got)
	}
}

func TestMemoryStoreFindNearbySortsNearestCandidates(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore(5)
	now := time.Date(2026, 6, 23, 6, 5, 0, 0, time.UTC)

	drivers := []model.DriverLocation{
		{
			DriverID:  "driver-near",
			Lat:       31.2305,
			Lng:       121.4737,
			Timestamp: now,
		},
		{
			DriverID:  "driver-mid",
			Lat:       31.2330,
			Lng:       121.4737,
			Timestamp: now.Add(time.Second),
		},
		{
			DriverID:  "driver-far",
			Lat:       31.2380,
			Lng:       121.4737,
			Timestamp: now.Add(2 * time.Second),
		},
	}

	for _, driver := range drivers {
		if err := store.Upsert(context.Background(), driver); err != nil {
			t.Fatalf("upsert driver %s: %v", driver.DriverID, err)
		}
		if err := store.SetDriverStatus(context.Background(), model.DriverStatus{
			DriverID:  driver.DriverID,
			Status:    model.DriverStatusOnline,
			UpdatedAt: now,
		}); err != nil {
			t.Fatalf("set driver status %s: %v", driver.DriverID, err)
		}
	}

	items, err := store.FindNearby(context.Background(), model.NearbyQuery{
		Lat:      31.2304,
		Lng:      121.4737,
		RadiusM:  1200,
		Limit:    2,
		OnlyLive: true,
	})
	if err != nil {
		t.Fatalf("find nearby: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 nearby drivers, got %d", len(items))
	}
	if items[0].DriverID != "driver-near" || items[1].DriverID != "driver-mid" {
		t.Fatalf("unexpected driver ordering: %+v", items)
	}
}

func TestMemoryStoreListRecentUsesLRUEviction(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore(2)
	now := time.Date(2026, 6, 23, 6, 10, 0, 0, time.UTC)

	locations := []model.DriverLocation{
		{
			DriverID:  "driver-1",
			Lat:       31.2304,
			Lng:       121.4737,
			Timestamp: now,
		},
		{
			DriverID:  "driver-1",
			Lat:       31.2305,
			Lng:       121.4738,
			Timestamp: now.Add(time.Second),
		},
		{
			DriverID:  "driver-1",
			Lat:       31.2306,
			Lng:       121.4739,
			Timestamp: now.Add(2 * time.Second),
		},
	}

	for _, location := range locations {
		if err := store.Upsert(context.Background(), location); err != nil {
			t.Fatalf("upsert recent location: %v", err)
		}
	}

	recent, err := store.ListRecent(context.Background(), "driver-1")
	if err != nil {
		t.Fatalf("list recent: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent locations after LRU eviction, got %d", len(recent))
	}
	if recent[0].Timestamp != locations[1].Timestamp || recent[1].Timestamp != locations[2].Timestamp {
		t.Fatalf("unexpected recent LRU order: %+v", recent)
	}
}
