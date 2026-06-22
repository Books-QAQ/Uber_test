package location

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestRedisStoreUpsertBatchAndList(t *testing.T) {
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
	}, 3)
	if err != nil {
		t.Fatalf("new redis store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 22, 22, 30, 0, 0, time.UTC)
	locations := []testLocationInput{
		{driverID: "driver-1", lat: 31.20, at: now},
		{driverID: "driver-2", lat: 31.30, at: now.Add(2 * time.Second)},
		{driverID: "driver-1", lat: 31.21, at: now.Add(3 * time.Second)},
	}

	for _, item := range locations {
		if err := store.Upsert(context.Background(), item.location()); err != nil {
			t.Fatalf("upsert location: %v", err)
		}
	}

	if err := store.TouchHeartbeat(context.Background(), testHeartbeat("driver-1", now.Add(4*time.Second))); err != nil {
		t.Fatalf("touch heartbeat: %v", err)
	}

	latest, err := store.ListLatest(context.Background())
	if err != nil {
		t.Fatalf("list latest: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("expected 2 latest locations, got %d", len(latest))
	}
	if latest[0].DriverID != "driver-1" {
		t.Fatalf("expected most recent driver to be driver-1, got %s", latest[0].DriverID)
	}

	recent, err := store.ListRecent(context.Background(), "driver-1")
	if err != nil {
		t.Fatalf("list recent: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent locations for driver-1, got %d", len(recent))
	}
	if recent[0].Lat != 31.20 || recent[1].Lat != 31.21 {
		t.Fatalf("unexpected recent location order: %+v", recent)
	}

	if !mini.Exists("test:driver:heartbeat:driver-1") {
		t.Fatalf("expected heartbeat key to exist")
	}
}
