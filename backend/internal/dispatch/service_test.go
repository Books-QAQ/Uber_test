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

type captureBroadcaster struct {
	payloads []map[string]any
}

func (b *captureBroadcaster) BroadcastJSON(v any) {
	if payload, ok := v.(map[string]any); ok {
		b.payloads = append(b.payloads, payload)
	}
}

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

func TestServiceClosePendingByDriverID(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	locationService := location.NewService(location.NewMemoryStore(10), nilBroadcaster{}, logger)
	orderStore := order.NewMemoryStore()
	broadcaster := &captureBroadcaster{}
	service := NewService(NewMemoryStore(), orderStore, locationService, broadcaster, logger)

	now := time.Now().UTC()
	for _, orderID := range []string{"order-a", "order-b", "order-c"} {
		if err := orderStore.Create(context.Background(), model.Order{
			ID:             orderID,
			PassengerID:    "passenger-1",
			Status:         model.OrderStatusPendingDispatch,
			PickupLat:      31.2304,
			PickupLng:      121.4737,
			DestinationLat: 31.2204,
			DestinationLng: 121.4637,
			CreatedAt:      now,
			UpdatedAt:      now,
		}); err != nil {
			t.Fatalf("seed order %s: %v", orderID, err)
		}
	}

	records := []model.DispatchRecord{
		{
			ID:            "dispatch-a",
			OrderID:       "order-a",
			DriverID:      "driver-1",
			Status:        model.DispatchStatusPending,
			DistanceM:     100,
			DispatchRound: 1,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		{
			ID:            "dispatch-b",
			OrderID:       "order-b",
			DriverID:      "driver-1",
			Status:        model.DispatchStatusPending,
			DistanceM:     120,
			DispatchRound: 1,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		{
			ID:            "dispatch-c",
			OrderID:       "order-c",
			DriverID:      "driver-2",
			Status:        model.DispatchStatusPending,
			DistanceM:     140,
			DispatchRound: 1,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	}

	if err := service.store.CreateBatch(context.Background(), records); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	if err := service.ClosePendingByDriverID(context.Background(), "driver-1", model.DispatchStatusExpired); err != nil {
		t.Fatalf("close pending by driver: %v", err)
	}

	items, err := service.ListPendingAssignmentsByDriverID(context.Background(), "driver-1")
	if err != nil {
		t.Fatalf("list pending for closed driver: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no pending assignments for expired driver, got %d", len(items))
	}

	otherItems, err := service.ListPendingAssignmentsByDriverID(context.Background(), "driver-2")
	if err != nil {
		t.Fatalf("list pending for other driver: %v", err)
	}
	if len(otherItems) != 1 || otherItems[0].Dispatch.ID != "dispatch-c" {
		t.Fatalf("expected other driver pending assignment to remain, got %+v", otherItems)
	}

	if len(broadcaster.payloads) == 0 {
		t.Fatalf("expected broadcast payload for driver closure")
	}
	last := broadcaster.payloads[len(broadcaster.payloads)-1]
	if eventType, _ := last["type"].(string); eventType != "dispatch.driver.closed" {
		t.Fatalf("unexpected event type: %v", last["type"])
	}
}

func TestServiceHandleDriverExpiredRedispatchesOrdersWithoutCandidates(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	locationService := location.NewService(location.NewMemoryStore(10), nilBroadcaster{}, logger)
	orderStore := order.NewMemoryStore()
	service := NewService(NewMemoryStore(), orderStore, locationService, &captureBroadcaster{}, logger)

	now := time.Now().UTC()
	orderItem := model.Order{
		ID:             "order-redispatch",
		PassengerID:    "passenger-1",
		Status:         model.OrderStatusPendingDispatch,
		PickupLat:      31.2304,
		PickupLng:      121.4737,
		DestinationLat: 31.2204,
		DestinationLng: 121.4637,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := orderStore.Create(context.Background(), orderItem); err != nil {
		t.Fatalf("seed order: %v", err)
	}

	for _, status := range []model.DriverStatus{
		{
			DriverID:  "driver-expired",
			Status:    model.DriverStatusOffline,
			UpdatedAt: now,
		},
		{
			DriverID:  "driver-replacement",
			Status:    model.DriverStatusOnline,
			UpdatedAt: now,
		},
	} {
		if err := locationService.SetDriverStatus(context.Background(), status); err != nil {
			t.Fatalf("set driver status %s: %v", status.DriverID, err)
		}
	}

	if err := locationService.HandlePacket(context.Background(), netip.MustParseAddrPort("127.0.0.1:19011"), mustSingleLocationPayload("driver-expired", 31.2305, 121.4738)); err != nil {
		t.Fatalf("seed expired driver location: %v", err)
	}
	if err := locationService.HandlePacket(context.Background(), netip.MustParseAddrPort("127.0.0.1:19012"), mustSingleLocationPayload("driver-replacement", 31.2306, 121.4739)); err != nil {
		t.Fatalf("seed replacement driver location: %v", err)
	}

	if err := service.store.CreateBatch(context.Background(), []model.DispatchRecord{
		{
			ID:            "dispatch-expired",
			OrderID:       orderItem.ID,
			DriverID:      "driver-expired",
			Status:        model.DispatchStatusPending,
			DistanceM:     100,
			DispatchRound: 1,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	}); err != nil {
		t.Fatalf("seed pending dispatch: %v", err)
	}

	if err := service.HandleDriverExpired(context.Background(), "driver-expired"); err != nil {
		t.Fatalf("handle driver expired: %v", err)
	}

	expiredItems, err := service.ListPendingAssignmentsByDriverID(context.Background(), "driver-expired")
	if err != nil {
		t.Fatalf("list expired driver assignments: %v", err)
	}
	if len(expiredItems) != 0 {
		t.Fatalf("expected expired driver to have no pending assignments, got %d", len(expiredItems))
	}

	replacementItems, err := service.ListPendingAssignmentsByDriverID(context.Background(), "driver-replacement")
	if err != nil {
		t.Fatalf("list replacement driver assignments: %v", err)
	}
	if len(replacementItems) != 1 {
		t.Fatalf("expected replacement driver to receive redispatch, got %d assignments", len(replacementItems))
	}
	if replacementItems[0].Order.ID != orderItem.ID {
		t.Fatalf("expected redispatched order %s, got %s", orderItem.ID, replacementItems[0].Order.ID)
	}
}

func TestServiceRetryTimedOutOrdersCreatesNextDispatchRound(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	locationService := location.NewService(location.NewMemoryStore(10), nilBroadcaster{}, logger)
	orderStore := order.NewMemoryStore()
	service := NewService(NewMemoryStore(), orderStore, locationService, &captureBroadcaster{}, logger)

	now := time.Now().UTC()
	orderItem := model.Order{
		ID:             "order-timeout-retry",
		PassengerID:    "passenger-1",
		Status:         model.OrderStatusPendingDispatch,
		PickupLat:      31.2304,
		PickupLng:      121.4737,
		DestinationLat: 31.2204,
		DestinationLng: 121.4637,
		CreatedAt:      now.Add(-time.Minute),
		UpdatedAt:      now.Add(-time.Minute),
	}
	if err := orderStore.Create(context.Background(), orderItem); err != nil {
		t.Fatalf("seed order: %v", err)
	}

	if err := locationService.SetDriverStatus(context.Background(), model.DriverStatus{
		DriverID:  "driver-round-2",
		Status:    model.DriverStatusOnline,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("set driver online: %v", err)
	}
	if err := locationService.HandlePacket(context.Background(), netip.MustParseAddrPort("127.0.0.1:19021"), mustSingleLocationPayload("driver-round-2", 31.2305, 121.4738)); err != nil {
		t.Fatalf("seed driver location: %v", err)
	}

	if err := service.store.CreateBatch(context.Background(), []model.DispatchRecord{
		{
			ID:            "dispatch-round-1",
			OrderID:       orderItem.ID,
			DriverID:      "driver-round-1",
			Status:        model.DispatchStatusPending,
			DistanceM:     100,
			DispatchRound: 1,
			CreatedAt:     now.Add(-time.Minute),
			UpdatedAt:     now.Add(-time.Minute),
		},
	}); err != nil {
		t.Fatalf("seed timed-out dispatch: %v", err)
	}

	if err := service.RetryTimedOutOrders(context.Background(), 15*time.Second, 3); err != nil {
		t.Fatalf("retry timed-out orders: %v", err)
	}

	records, err := service.store.ListByOrderID(context.Background(), orderItem.ID)
	if err != nil {
		t.Fatalf("list order dispatch history: %v", err)
	}
	if len(records) < 2 {
		t.Fatalf("expected at least 2 dispatch records after retry, got %d", len(records))
	}

	foundRound2Pending := false
	for _, record := range records {
		if record.DispatchRound == 2 && record.Status == model.DispatchStatusPending {
			foundRound2Pending = true
			break
		}
	}
	if !foundRound2Pending {
		t.Fatalf("expected a round-2 pending dispatch record, got %+v", records)
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
