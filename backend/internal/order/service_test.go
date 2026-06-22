package order

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	locationpb "uber-test/backend/internal/gen/location/v1"
	"uber-test/backend/internal/location"
	"uber-test/backend/internal/model"
)

type stubDriverStatusWriter struct {
	statuses []model.DriverStatus
}

func (s *stubDriverStatusWriter) SetDriverStatus(_ context.Context, status model.DriverStatus) error {
	s.statuses = append(s.statuses, status)
	return nil
}

func TestServiceCreateAndUpdateStatus(t *testing.T) {
	t.Parallel()

	driverWriter := &stubDriverStatusWriter{}
	service := NewService(NewMemoryStore(), driverWriter)

	order, err := service.Create(context.Background(), model.CreateOrderInput{
		PassengerID:        "passenger-1",
		PickupLat:          31.2304,
		PickupLng:          121.4737,
		DestinationLat:     31.2204,
		DestinationLng:     121.4637,
		DestinationAddress: "destination",
		EstimatedPrice:     32.5,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	if order.Status != model.OrderStatusPendingDispatch {
		t.Fatalf("expected pending_dispatch, got %s", order.Status)
	}

	order, err = service.UpdateStatus(context.Background(), order.ID, model.UpdateOrderStatusInput{
		Status:   model.OrderStatusAccepted,
		DriverID: "driver-1",
	})
	if err != nil {
		t.Fatalf("accept order: %v", err)
	}
	if order.DriverID != "driver-1" {
		t.Fatalf("expected driver-1, got %s", order.DriverID)
	}
	if len(driverWriter.statuses) != 1 || driverWriter.statuses[0].Status != model.DriverStatusToPickup {
		t.Fatalf("expected to_pickup driver status update, got %+v", driverWriter.statuses)
	}

	if _, err := service.UpdateStatus(context.Background(), order.ID, model.UpdateOrderStatusInput{
		Status: model.OrderStatusPaid,
	}); err == nil {
		t.Fatalf("expected invalid direct transition to paid")
	}
}

func TestServiceUpdateStatusLifecycleSyncsDriverStatus(t *testing.T) {
	t.Parallel()

	driverWriter := &stubDriverStatusWriter{}
	service := NewService(NewMemoryStore(), driverWriter)

	order, err := service.Create(context.Background(), model.CreateOrderInput{
		PassengerID:    "passenger-2",
		PickupLat:      31.20,
		PickupLng:      121.47,
		DestinationLat: 31.21,
		DestinationLng: 121.48,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	steps := []model.UpdateOrderStatusInput{
		{Status: model.OrderStatusAccepted, DriverID: "driver-2"},
		{Status: model.OrderStatusDriverArrived},
		{Status: model.OrderStatusInTrip},
		{Status: model.OrderStatusCompleted},
	}

	for _, step := range steps {
		order, err = service.UpdateStatus(context.Background(), order.ID, step)
		if err != nil {
			t.Fatalf("update status to %s: %v", step.Status, err)
		}
	}

	if order.Status != model.OrderStatusCompleted {
		t.Fatalf("expected completed order, got %s", order.Status)
	}
	if len(driverWriter.statuses) != 4 {
		t.Fatalf("expected 4 driver status syncs, got %d", len(driverWriter.statuses))
	}

	got := []string{
		driverWriter.statuses[0].Status,
		driverWriter.statuses[1].Status,
		driverWriter.statuses[2].Status,
		driverWriter.statuses[3].Status,
	}
	want := []string{
		model.DriverStatusToPickup,
		model.DriverStatusToPickup,
		model.DriverStatusInTrip,
		model.DriverStatusOnline,
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected driver status at step %d: got %s want %s", i, got[i], want[i])
		}
		if driverWriter.statuses[i].UpdatedAt.IsZero() || driverWriter.statuses[i].UpdatedAt.Before(time.Now().Add(-time.Minute)) {
			t.Fatalf("unexpected updated_at at step %d: %+v", i, driverWriter.statuses[i].UpdatedAt)
		}
	}
}

func TestServiceAcceptedOrderRemovesDriverFromNearbyAvailability(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	locationService := location.NewService(location.NewMemoryStore(10), nilBroadcaster{}, logger)
	service := NewService(NewMemoryStore(), locationService)

	if err := locationService.SetDriverStatus(context.Background(), model.DriverStatus{
		DriverID:  "driver-3",
		Status:    model.DriverStatusOnline,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("set driver online: %v", err)
	}
	if err := locationService.HandlePacket(context.Background(), mustAddrPort("127.0.0.1:19000"), mustSingleLocationPayload("driver-3", 31.2304, 121.4737)); err != nil {
		t.Fatalf("seed driver location: %v", err)
	}

	order, err := service.Create(context.Background(), model.CreateOrderInput{
		PassengerID:    "passenger-3",
		PickupLat:      31.2304,
		PickupLng:      121.4737,
		DestinationLat: 31.2204,
		DestinationLng: 121.4637,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	if _, err := service.UpdateStatus(context.Background(), order.ID, model.UpdateOrderStatusInput{
		Status:   model.OrderStatusAccepted,
		DriverID: "driver-3",
	}); err != nil {
		t.Fatalf("accept order: %v", err)
	}

	nearby, err := locationService.FindNearby(context.Background(), model.NearbyQuery{
		Lat:     31.2304,
		Lng:     121.4737,
		RadiusM: 500,
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("find nearby: %v", err)
	}
	if len(nearby) != 0 {
		t.Fatalf("expected accepted driver to be unavailable for nearby, got %d items", len(nearby))
	}
}

type nilBroadcaster struct{}

func (nilBroadcaster) BroadcastJSON(any) {}

func mustAddrPort(value string) netip.AddrPort {
	return netip.MustParseAddrPort(value)
}

func mustSingleLocationPayload(driverID string, lat, lng float64) []byte {
	payload, err := proto.Marshal(&locationpb.LocationIngressPacket{
		Payload: &locationpb.LocationIngressPacket_LocationUpdate{
			LocationUpdate: &locationpb.DriverLocationUpdate{
				DriverId:         driverID,
				Lat:              lat,
				Lng:              lng,
				ReportedAtUnixMs: time.Now().UTC().UnixMilli(),
			},
		},
	})
	if err != nil {
		panic(err)
	}
	return payload
}
