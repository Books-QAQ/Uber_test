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
