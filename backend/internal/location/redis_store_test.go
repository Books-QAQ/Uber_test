package location

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"uber-test/backend/internal/model"
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

func TestRedisStoreFindNearbyReturnsOnlineDrivers(t *testing.T) {
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
	}, 5)
	if err != nil {
		t.Fatalf("new redis store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 22, 22, 40, 0, 0, time.UTC)
	for _, item := range []testLocationInput{
		{driverID: "driver-1", lat: 31.2304, at: now},
		{driverID: "driver-2", lat: 31.2404, at: now.Add(time.Second)},
	} {
		location := item.location()
		location.Lng = 121.4737
		if err := store.Upsert(context.Background(), location); err != nil {
			t.Fatalf("upsert location: %v", err)
		}
	}

	if err := store.SetDriverStatus(context.Background(), model.DriverStatus{
		DriverID:  "driver-1",
		Status:    model.DriverStatusOnline,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("set online status: %v", err)
	}
	if err := store.SetDriverStatus(context.Background(), model.DriverStatus{
		DriverID:  "driver-2",
		Status:    model.DriverStatusOffline,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("set offline status: %v", err)
	}

	items, err := store.FindNearby(context.Background(), model.NearbyQuery{
		Lat:      31.2304,
		Lng:      121.4737,
		RadiusM:  2000,
		Limit:    10,
		OnlyLive: true,
	})
	if err != nil {
		t.Fatalf("find nearby: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 nearby online driver, got %d", len(items))
	}
	if items[0].DriverID != "driver-1" {
		t.Fatalf("expected driver-1, got %s", items[0].DriverID)
	}
}

func TestRedisStoreExpireInactiveMarksOffline(t *testing.T) {
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
	}, 5)
	if err != nil {
		t.Fatalf("new redis store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 23, 0, 10, 0, 0, time.UTC)
	stale := model.DriverLocation{
		DriverID:  "driver-stale",
		Lat:       31.2304,
		Lng:       121.4737,
		Timestamp: now.Add(-20 * time.Minute),
	}
	fresh := model.DriverLocation{
		DriverID:  "driver-fresh",
		Lat:       31.2305,
		Lng:       121.4738,
		Timestamp: now.Add(-2 * time.Minute),
	}

	for _, location := range []model.DriverLocation{stale, fresh} {
		if err := store.Upsert(context.Background(), location); err != nil {
			t.Fatalf("upsert driver %s: %v", location.DriverID, err)
		}
		if err := store.SetDriverStatus(context.Background(), model.DriverStatus{
			DriverID:  location.DriverID,
			Status:    model.DriverStatusOnline,
			UpdatedAt: now.Add(-25 * time.Minute),
		}); err != nil {
			t.Fatalf("set driver status %s: %v", location.DriverID, err)
		}
	}

	expired, err := store.ExpireInactive(context.Background(), now.Add(-15*time.Minute))
	if err != nil {
		t.Fatalf("expire inactive: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired driver, got %d", len(expired))
	}
	if expired[0].DriverID != "driver-stale" || expired[0].Status != model.DriverStatusOffline {
		t.Fatalf("unexpected expired driver payload: %+v", expired[0])
	}

	items, err := store.FindNearby(context.Background(), model.NearbyQuery{
		Lat:      31.2304,
		Lng:      121.4737,
		RadiusM:  1000,
		Limit:    10,
		OnlyLive: true,
	})
	if err != nil {
		t.Fatalf("find nearby after expiration: %v", err)
	}
	if len(items) != 1 || items[0].DriverID != "driver-fresh" {
		t.Fatalf("expected only fresh online driver after expiration, got %+v", items)
	}
}
