package location

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	locationpb "uber-test/backend/internal/gen/location/v1"
	"uber-test/backend/internal/model"
)

type stubBroadcaster struct {
	payloads []map[string]any
}

func (b *stubBroadcaster) BroadcastJSON(v any) {
	if payload, ok := v.(map[string]any); ok {
		b.payloads = append(b.payloads, payload)
	}
}

type stubMatcher struct {
	latest map[string]model.DriverLocation
}

func (s *stubMatcher) Sync(_ context.Context, raw model.DriverLocation) (model.DriverLocation, error) {
	if s.latest == nil {
		s.latest = make(map[string]model.DriverLocation)
	}
	matched := raw
	matched.Lat += 0.001
	matched.Lng += 0.001
	s.latest[raw.DriverID] = matched
	return matched, nil
}

func (s *stubMatcher) GetLatest(driverID string) (model.DriverLocation, bool) {
	if s.latest == nil {
		return model.DriverLocation{}, false
	}
	item, ok := s.latest[driverID]
	return item, ok
}

type stubRouteCoordinator struct {
	synced    []model.DriverLocation
	projected map[string]model.DriverLocation
}

func (s *stubRouteCoordinator) SyncDriverLocation(_ context.Context, location model.DriverLocation) error {
	s.synced = append(s.synced, location)
	return nil
}

func (s *stubRouteCoordinator) ClearByDriverID(_ context.Context, _ string) error {
	return nil
}

func (s *stubRouteCoordinator) ProjectVisibleLocation(_ context.Context, location model.DriverLocation) (model.DriverLocation, error) {
	if s.projected == nil {
		return location, nil
	}
	if item, ok := s.projected[location.DriverID]; ok {
		projected := location
		projected.Lat = item.Lat
		projected.Lng = item.Lng
		return projected, nil
	}
	return location, nil
}

func TestServiceHandlePacketSupportsBatchAndHeartbeat(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore(10)
	broadcaster := &stubBroadcaster{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(store, broadcaster, logger)

	batchPayload, err := proto.Marshal(&locationpb.LocationIngressPacket{
		Payload: &locationpb.LocationIngressPacket_LocationBatch{
			LocationBatch: &locationpb.DriverLocationBatch{
				Locations: []*locationpb.DriverLocationUpdate{
					{
						DriverId:         "driver-1",
						Lat:              31.2304,
						Lng:              121.4737,
						ReportedAtUnixMs: time.Date(2026, 6, 22, 23, 0, 0, 0, time.UTC).UnixMilli(),
					},
					{
						DriverId:         "driver-2",
						Lat:              31.2305,
						Lng:              121.4738,
						ReportedAtUnixMs: time.Date(2026, 6, 22, 23, 0, 1, 0, time.UTC).UnixMilli(),
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal batch payload: %v", err)
	}

	if err := service.HandlePacket(context.Background(), netip.MustParseAddrPort("127.0.0.1:9000"), batchPayload); err != nil {
		t.Fatalf("handle batch payload: %v", err)
	}

	heartbeatPayload, err := proto.Marshal(&locationpb.LocationIngressPacket{
		Payload: &locationpb.LocationIngressPacket_Heartbeat{
			Heartbeat: &locationpb.DriverHeartbeat{
				DriverId:         "driver-1",
				ReportedAtUnixMs: time.Date(2026, 6, 22, 23, 0, 2, 0, time.UTC).UnixMilli(),
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal heartbeat payload: %v", err)
	}

	if err := service.HandlePacket(context.Background(), netip.MustParseAddrPort("127.0.0.1:9000"), heartbeatPayload); err != nil {
		t.Fatalf("handle heartbeat payload: %v", err)
	}

	latest, err := service.ListLatest(context.Background())
	if err != nil {
		t.Fatalf("list latest: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("expected 2 locations after batch packet, got %d", len(latest))
	}
	if len(broadcaster.payloads) != 2 {
		t.Fatalf("expected 2 broadcast events, got %d", len(broadcaster.payloads))
	}
	if eventType, _ := broadcaster.payloads[0]["type"].(string); eventType != "driver.location.batch.updated" {
		t.Fatalf("unexpected first event type: %v", broadcaster.payloads[0]["type"])
	}
	if eventType, _ := broadcaster.payloads[1]["type"].(string); eventType != "driver.heartbeat.received" {
		t.Fatalf("unexpected second event type: %v", broadcaster.payloads[1]["type"])
	}
}

func TestServiceFindNearbyReturnsOnlyOnlineDrivers(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore(10)
	broadcaster := &stubBroadcaster{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(store, broadcaster, logger)

	now := time.Date(2026, 6, 22, 23, 10, 0, 0, time.UTC)
	if err := store.Upsert(context.Background(), model.DriverLocation{
		DriverID:  "driver-online",
		Lat:       31.2304,
		Lng:       121.4737,
		Timestamp: now,
	}); err != nil {
		t.Fatalf("upsert online driver location: %v", err)
	}
	if err := store.Upsert(context.Background(), model.DriverLocation{
		DriverID:  "driver-offline",
		Lat:       31.2305,
		Lng:       121.4738,
		Timestamp: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("upsert offline driver location: %v", err)
	}

	if err := service.SetDriverStatus(context.Background(), model.DriverStatus{
		DriverID:  "driver-online",
		Status:    model.DriverStatusOnline,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("set online status: %v", err)
	}
	if err := service.SetDriverStatus(context.Background(), model.DriverStatus{
		DriverID:  "driver-offline",
		Status:    model.DriverStatusOffline,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("set offline status: %v", err)
	}

	items, err := service.FindNearby(context.Background(), model.NearbyQuery{
		Lat:     31.2304,
		Lng:     121.4737,
		RadiusM: 500,
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("find nearby: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 online nearby driver, got %d", len(items))
	}
	if items[0].DriverID != "driver-online" {
		t.Fatalf("expected driver-online, got %s", items[0].DriverID)
	}
}

func TestServiceBroadcastsAndReadsMatchedLocations(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore(10)
	broadcaster := &stubBroadcaster{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(store, broadcaster, logger)
	service.SetLocationMatcher(&stubMatcher{})

	now := time.Date(2026, 6, 24, 1, 0, 0, 0, time.UTC)
	if err := service.SetDriverStatus(context.Background(), model.DriverStatus{
		DriverID:  "driver-match",
		Status:    model.DriverStatusOnline,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("set driver online: %v", err)
	}

	payload, err := proto.Marshal(&locationpb.LocationIngressPacket{
		Payload: &locationpb.LocationIngressPacket_LocationUpdate{
			LocationUpdate: &locationpb.DriverLocationUpdate{
				DriverId:         "driver-match",
				Lat:              31.2304,
				Lng:              121.4737,
				ReportedAtUnixMs: now.UnixMilli(),
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal single payload: %v", err)
	}

	if err := service.HandlePacket(context.Background(), netip.MustParseAddrPort("127.0.0.1:9000"), payload); err != nil {
		t.Fatalf("handle payload: %v", err)
	}

	latest, err := service.GetLatestByDriverID(context.Background(), "driver-match")
	if err != nil {
		t.Fatalf("get latest by driver: %v", err)
	}
	if latest.Lat <= 31.2304 || latest.Lng <= 121.4737 {
		t.Fatalf("expected matched coordinates, got %+v", latest)
	}

	items, err := service.FindNearby(context.Background(), model.NearbyQuery{
		Lat:     31.2304,
		Lng:     121.4737,
		RadiusM: 500,
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("find nearby: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 nearby driver, got %d", len(items))
	}
	if items[0].Location.Lat <= 31.2304 || items[0].Location.Lng <= 121.4737 {
		t.Fatalf("expected nearby response to expose matched coordinates, got %+v", items[0].Location)
	}

	data, ok := broadcaster.payloads[len(broadcaster.payloads)-1]["data"].(model.DriverLocation)
	if !ok {
		t.Fatalf("expected broadcast payload data to be model.DriverLocation")
	}
	if data.Lat <= 31.2304 || data.Lng <= 121.4737 {
		t.Fatalf("expected broadcast to use matched coordinates, got %+v", data)
	}
}

func TestServiceSyncsRawLocationButBroadcastsProjectedVisibleLocation(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore(10)
	broadcaster := &stubBroadcaster{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(store, broadcaster, logger)

	routeCoordinator := &stubRouteCoordinator{
		projected: map[string]model.DriverLocation{
			"driver-route": {
				Lat: 31.2309,
				Lng: 121.4742,
			},
		},
	}
	service.SetRouteCoordinator(routeCoordinator)

	raw := model.DriverLocation{
		DriverID:  "driver-route",
		OrderID:   "order-1",
		Lat:       31.2304,
		Lng:       121.4737,
		Timestamp: time.Date(2026, 6, 25, 1, 0, 0, 0, time.UTC),
	}
	if err := service.UpsertDriverLocation(context.Background(), raw); err != nil {
		t.Fatalf("UpsertDriverLocation returned error: %v", err)
	}

	if len(routeCoordinator.synced) != 1 {
		t.Fatalf("expected 1 raw sync call, got %d", len(routeCoordinator.synced))
	}
	if routeCoordinator.synced[0].Lat != raw.Lat || routeCoordinator.synced[0].Lng != raw.Lng {
		t.Fatalf("expected route sync to use raw location %+v, got %+v", raw, routeCoordinator.synced[0])
	}

	data, ok := broadcaster.payloads[len(broadcaster.payloads)-1]["data"].(model.DriverLocation)
	if !ok {
		t.Fatalf("expected broadcast payload data to be model.DriverLocation")
	}
	if data.Lat != 31.2309 || data.Lng != 121.4742 {
		t.Fatalf("expected broadcast to use projected visible location, got %+v", data)
	}
}

func TestServiceExpireInactiveDriversMarksOffline(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore(10)
	broadcaster := &stubBroadcaster{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(store, broadcaster, logger)

	now := time.Date(2026, 6, 23, 0, 0, 0, 0, time.UTC)
	for _, driverID := range []string{"driver-stale", "driver-fresh"} {
		if err := service.SetDriverStatus(context.Background(), model.DriverStatus{
			DriverID:  driverID,
			Status:    model.DriverStatusOnline,
			UpdatedAt: now.Add(-20 * time.Minute),
		}); err != nil {
			t.Fatalf("set status for %s: %v", driverID, err)
		}
	}

	if err := store.Upsert(context.Background(), model.DriverLocation{
		DriverID:  "driver-stale",
		Lat:       31.2304,
		Lng:       121.4737,
		Timestamp: now.Add(-16 * time.Minute),
	}); err != nil {
		t.Fatalf("upsert stale driver: %v", err)
	}
	if err := store.Upsert(context.Background(), model.DriverLocation{
		DriverID:  "driver-fresh",
		Lat:       31.2305,
		Lng:       121.4738,
		Timestamp: now.Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("upsert fresh driver: %v", err)
	}

	expired, err := service.ExpireInactiveDrivers(context.Background(), now.Add(-15*time.Minute))
	if err != nil {
		t.Fatalf("expire inactive drivers: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired driver, got %d", len(expired))
	}
	if expired[0].DriverID != "driver-stale" || expired[0].Status != model.DriverStatusOffline {
		t.Fatalf("unexpected expired driver payload: %+v", expired[0])
	}

	items, err := service.FindNearby(context.Background(), model.NearbyQuery{
		Lat:     31.2304,
		Lng:     121.4737,
		RadiusM: 500,
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("find nearby after expiration: %v", err)
	}
	if len(items) != 1 || items[0].DriverID != "driver-fresh" {
		t.Fatalf("expected only fresh driver nearby after expiration, got %+v", items)
	}

	if len(broadcaster.payloads) == 0 {
		t.Fatalf("expected expiration broadcast event")
	}
	lastPayload := broadcaster.payloads[len(broadcaster.payloads)-1]
	if eventType, _ := lastPayload["type"].(string); eventType != "driver.status.expired" {
		t.Fatalf("unexpected expiration event type: %v", lastPayload["type"])
	}
}

type testLocationInput struct {
	driverID string
	lat      float64
	at       time.Time
}

func (i testLocationInput) location() model.DriverLocation {
	return model.DriverLocation{
		DriverID:  i.driverID,
		Lat:       i.lat,
		Lng:       121.47,
		Timestamp: i.at,
	}
}

func testHeartbeat(driverID string, at time.Time) model.DriverHeartbeat {
	return model.DriverHeartbeat{
		DriverID:  driverID,
		Timestamp: at,
	}
}
