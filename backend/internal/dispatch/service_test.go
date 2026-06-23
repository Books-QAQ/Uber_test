package dispatch

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
	"uber-test/backend/internal/order"
)

type nilBroadcaster struct{}

func (nilBroadcaster) BroadcastJSON(any) {}

func TestServiceDispatchOrderCreatesPendingAssignments(t *testing.T) {
	t.Parallel()

	service, orderStore := newTestService(t)
	orderItem := model.Order{
		ID:             "order-dispatch-1",
		PassengerID:    "passenger-1",
		Status:         model.OrderStatusPendingDispatch,
		PickupLat:      31.2304,
		PickupLng:      121.4737,
		DestinationLat: 31.2204,
		DestinationLng: 121.4637,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := orderStore.Create(context.Background(), orderItem); err != nil {
		t.Fatalf("seed order: %v", err)
	}

	if err := service.DispatchOrder(context.Background(), orderItem); err != nil {
		t.Fatalf("dispatch order: %v", err)
	}

	assignments, err := service.ListPendingAssignmentsByDriverID(context.Background(), "driver-near-1")
	if err != nil {
		t.Fatalf("list pending assignments: %v", err)
	}
	if len(assignments) != 1 {
		t.Fatalf("expected 1 assignment for driver-near-1, got %d", len(assignments))
	}
	if assignments[0].Order.ID != orderItem.ID {
		t.Fatalf("expected order %s, got %s", orderItem.ID, assignments[0].Order.ID)
	}
	if assignments[0].Dispatch.Status != model.DispatchStatusPending {
		t.Fatalf("expected pending dispatch, got %s", assignments[0].Dispatch.Status)
	}

	secondAssignments, err := service.ListPendingAssignmentsByDriverID(context.Background(), "driver-near-2")
	if err != nil {
		t.Fatalf("list pending assignments for second driver: %v", err)
	}
	if len(secondAssignments) != 1 {
		t.Fatalf("expected 1 assignment for driver-near-2, got %d", len(secondAssignments))
	}
	if assignments[0].Dispatch.DistanceM > secondAssignments[0].Dispatch.DistanceM {
		t.Fatalf("expected closer driver to have shorter distance")
	}
}

func TestServiceEnsureDriverCanAcceptAndMarkAccepted(t *testing.T) {
	t.Parallel()

	service, orderStore := newTestService(t)
	orderItem := model.Order{
		ID:             "order-dispatch-2",
		PassengerID:    "passenger-2",
		Status:         model.OrderStatusPendingDispatch,
		PickupLat:      31.2304,
		PickupLng:      121.4737,
		DestinationLat: 31.2204,
		DestinationLng: 121.4637,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := orderStore.Create(context.Background(), orderItem); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if err := service.DispatchOrder(context.Background(), orderItem); err != nil {
		t.Fatalf("dispatch order: %v", err)
	}

	if err := service.EnsureDriverCanAccept(context.Background(), orderItem.ID, "driver-near-1"); err != nil {
		t.Fatalf("expected dispatched driver to be able to accept: %v", err)
	}
	if err := service.EnsureDriverCanAccept(context.Background(), orderItem.ID, "driver-far"); err != ErrDriverNotDispatched {
		t.Fatalf("expected ErrDriverNotDispatched for undispatched driver, got %v", err)
	}

	if err := service.MarkAccepted(context.Background(), orderItem.ID, "driver-near-1"); err != nil {
		t.Fatalf("mark accepted: %v", err)
	}

	assignments, err := service.ListPendingAssignmentsByDriverID(context.Background(), "driver-near-1")
	if err != nil {
		t.Fatalf("list pending after accept: %v", err)
	}
	if len(assignments) != 0 {
		t.Fatalf("expected accepted driver to have no pending assignments, got %d", len(assignments))
	}

	secondAssignments, err := service.ListPendingAssignmentsByDriverID(context.Background(), "driver-near-2")
	if err != nil {
		t.Fatalf("list other pending after accept: %v", err)
	}
	if len(secondAssignments) != 0 {
		t.Fatalf("expected other drivers to have no pending assignments, got %d", len(secondAssignments))
	}
}

func newTestService(t *testing.T) (*Service, *order.MemoryStore) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	locationService := location.NewService(location.NewMemoryStore(10), nilBroadcaster{}, logger)
	orderStore := order.NewMemoryStore()

	if err := locationService.SetDriverStatus(context.Background(), model.DriverStatus{
		DriverID:  "driver-near-1",
		Status:    model.DriverStatusOnline,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("set driver-near-1 online: %v", err)
	}
	if err := locationService.SetDriverStatus(context.Background(), model.DriverStatus{
		DriverID:  "driver-near-2",
		Status:    model.DriverStatusOnline,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("set driver-near-2 online: %v", err)
	}
	if err := locationService.SetDriverStatus(context.Background(), model.DriverStatus{
		DriverID:  "driver-far",
		Status:    model.DriverStatusOffline,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("set driver-far offline: %v", err)
	}

	if err := locationService.HandlePacket(context.Background(), netip.MustParseAddrPort("127.0.0.1:19001"), mustSingleLocationPayload("driver-near-1", 31.2305, 121.4738)); err != nil {
		t.Fatalf("seed location for driver-near-1: %v", err)
	}
	if err := locationService.HandlePacket(context.Background(), netip.MustParseAddrPort("127.0.0.1:19002"), mustSingleLocationPayload("driver-near-2", 31.2310, 121.4742)); err != nil {
		t.Fatalf("seed location for driver-near-2: %v", err)
	}
	if err := locationService.HandlePacket(context.Background(), netip.MustParseAddrPort("127.0.0.1:19003"), mustSingleLocationPayload("driver-far", 31.3300, 121.5700)); err != nil {
		t.Fatalf("seed location for driver-far: %v", err)
	}

	service := NewService(NewMemoryStore(), orderStore, locationService, nilBroadcaster{}, logger)
	return service, orderStore
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
