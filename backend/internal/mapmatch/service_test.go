package mapmatch

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"uber-test/backend/internal/model"
)

type stubRecentReader struct {
	items []model.DriverLocation
}

func (s stubRecentReader) ListRecent(context.Context, string) ([]model.DriverLocation, error) {
	return s.items, nil
}

type stubMatcher struct{}

func (stubMatcher) Match(_ context.Context, points []model.DriverLocation) ([]model.DriverLocation, error) {
	matched := make([]model.DriverLocation, 0, len(points))
	for _, point := range points {
		next := point
		next.Lat += 0.001
		next.Lng += 0.001
		matched = append(matched, next)
	}
	return matched, nil
}

func TestServiceSyncStoresMatchedLatest(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	reader := stubRecentReader{
		items: []model.DriverLocation{
			{DriverID: "driver-1", Lat: 31.2301, Lng: 121.4731, Timestamp: now.Add(-3 * time.Second)},
			{DriverID: "driver-1", Lat: 31.2302, Lng: 121.4732, Timestamp: now.Add(-2 * time.Second)},
			{DriverID: "driver-1", Lat: 31.2303, Lng: 121.4733, Timestamp: now.Add(-time.Second)},
			{DriverID: "driver-1", Lat: 31.2304, Lng: 121.4734, Timestamp: now},
		},
	}
	service := NewService(reader, stubMatcher{}, slog.New(slog.NewTextHandler(io.Discard, nil)), 4, 8, 45*time.Second)

	visible, err := service.Sync(context.Background(), reader.items[len(reader.items)-1])
	if err != nil {
		t.Fatalf("sync map match: %v", err)
	}
	if visible.Lat <= reader.items[len(reader.items)-1].Lat {
		t.Fatalf("expected matched latitude to differ, got %+v", visible)
	}

	stored, ok := service.GetLatest("driver-1")
	if !ok {
		t.Fatalf("expected latest matched location")
	}
	if stored.Lat != visible.Lat || stored.Lng != visible.Lng {
		t.Fatalf("expected latest matched location to be cached")
	}
}
