package routeplan

import (
	"context"
	"testing"
	"time"

	"uber-test/backend/internal/model"
	"uber-test/backend/internal/order"
)

type stubPlanner struct {
	points []model.RoutePoint
	err    error
}

func (s stubPlanner) Plan(_ context.Context, _ float64, _ float64, _ float64, _ float64) ([]model.RoutePoint, error) {
	return append([]model.RoutePoint(nil), s.points...), s.err
}

type stubOrderReader struct {
	activeByDriver map[string]model.Order
}

func (s stubOrderReader) GetByID(_ context.Context, id string) (model.Order, error) {
	for _, item := range s.activeByDriver {
		if item.ID == id {
			return item, nil
		}
	}
	return model.Order{}, order.ErrNotFound
}

func (s stubOrderReader) FindActiveByDriverID(_ context.Context, driverID string) (model.Order, error) {
	item, ok := s.activeByDriver[driverID]
	if !ok {
		return model.Order{}, order.ErrNotFound
	}
	return item, nil
}

func TestPlanPathUsesPlannerEvenNearDestination(t *testing.T) {
	t.Parallel()

	expected := []model.RoutePoint{
		{Lat: 31.230000, Lng: 121.470000},
		{Lat: 31.230030, Lng: 121.470140},
		{Lat: 31.230060, Lng: 121.470220},
	}

	service := NewService(NewMemoryStore(), nil, nil, stubPlanner{points: expected}, nil, nil)
	points, err := service.planPath(context.Background(), 31.230000, 121.470000, 31.230080, 121.470090)
	if err != nil {
		t.Fatalf("planPath returned error: %v", err)
	}
	if len(points) != len(expected) {
		t.Fatalf("expected planner path length %d, got %d", len(expected), len(points))
	}
	for i := range expected {
		if points[i] != expected[i] {
			t.Fatalf("expected planner point %d to be %+v, got %+v", i, expected[i], points[i])
		}
	}
}

func TestSyncRouteForNearDestinationKeepsPlannedRoadEndpoint(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	planned := []model.RoutePoint{
		{Lat: 31.230000, Lng: 121.470000},
		{Lat: 31.230020, Lng: 121.470150},
		{Lat: 31.230045, Lng: 121.470260},
	}
	service := NewService(store, nil, nil, stubPlanner{points: planned}, nil, nil)

	orderItem := model.Order{
		ID:        "order-1",
		DriverID:  "driver-1",
		Status:    model.OrderStatusAccepted,
		PickupLat: 31.230080,
		PickupLng: 121.470090,
		UpdatedAt: model.Order{}.UpdatedAt,
		CreatedAt: model.Order{}.CreatedAt,
	}
	locationUpdate := model.DriverLocation{
		DriverID: "driver-1",
		OrderID:  "order-1",
		Lat:      31.230000,
		Lng:      121.470000,
	}

	if err := service.syncRouteFor(context.Background(), locationUpdate, orderItem); err != nil {
		t.Fatalf("syncRouteFor returned error: %v", err)
	}

	route, err := store.GetByOrderID(context.Background(), orderItem.ID)
	if err != nil {
		t.Fatalf("GetByOrderID returned error: %v", err)
	}
	if len(route.Points) < 2 {
		t.Fatalf("expected persisted route path, got %+v", route.Points)
	}

	last := route.Points[len(route.Points)-1]
	if last != planned[len(planned)-1] {
		t.Fatalf("expected route to keep planned road endpoint %+v, got %+v", planned[len(planned)-1], last)
	}
	if last == (model.RoutePoint{Lat: orderItem.PickupLat, Lng: orderItem.PickupLng}) {
		t.Fatalf("expected route to avoid collapsing to raw pickup point %+v", last)
	}
}

func TestSyncRouteForSkipsReplanDuringCooldown(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	initial := model.DriverRoute{
		DriverID: "driver-1",
		OrderID:  "order-1",
		Mode:     "pickup",
		Points: []model.RoutePoint{
			{Lat: 31.2300, Lng: 121.4700},
			{Lat: 31.2305, Lng: 121.4705},
			{Lat: 31.2310, Lng: 121.4710},
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.Save(context.Background(), initial); err != nil {
		t.Fatalf("save initial route: %v", err)
	}

	replanned := []model.RoutePoint{
		{Lat: 31.2400, Lng: 121.4800},
		{Lat: 31.2500, Lng: 121.4900},
	}
	service := NewService(store, nil, nil, stubPlanner{points: replanned}, nil, nil)

	orderItem := model.Order{
		ID:        "order-1",
		DriverID:  "driver-1",
		Status:    model.OrderStatusAccepted,
		PickupLat: 31.2350,
		PickupLng: 121.4750,
	}
	// Far enough from the existing path to trigger a replan without cooldown.
	locationUpdate := model.DriverLocation{
		DriverID: "driver-1",
		OrderID:  "order-1",
		Lat:      31.2365,
		Lng:      121.4765,
	}

	if err := service.syncRouteFor(context.Background(), locationUpdate, orderItem); err != nil {
		t.Fatalf("syncRouteFor returned error: %v", err)
	}

	route, err := store.GetByOrderID(context.Background(), orderItem.ID)
	if err != nil {
		t.Fatalf("GetByOrderID returned error: %v", err)
	}
	if len(route.Points) != len(initial.Points) {
		t.Fatalf("expected cooldown to keep existing route length %d, got %d", len(initial.Points), len(route.Points))
	}
	for i := range initial.Points {
		if route.Points[i] != initial.Points[i] {
			t.Fatalf("expected route point %d to stay %+v, got %+v", i, initial.Points[i], route.Points[i])
		}
	}
}

func TestProjectVisibleLocationSnapsToProjectedRoadPoint(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	service := NewService(store, stubOrderReader{
		activeByDriver: map[string]model.Order{
			"driver-1": {
				ID:       "order-1",
				DriverID: "driver-1",
				Status:   model.OrderStatusAccepted,
			},
		},
	}, nil, nil, nil, nil)

	route := model.DriverRoute{
		DriverID: "driver-1",
		OrderID:  "order-1",
		Mode:     "pickup",
		Points: []model.RoutePoint{
			{Lat: 31.230000, Lng: 121.470000},
			{Lat: 31.230000, Lng: 121.470120},
			{Lat: 31.230000, Lng: 121.470240},
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.Save(context.Background(), route); err != nil {
		t.Fatalf("save route: %v", err)
	}

	raw := model.DriverLocation{
		DriverID: "driver-1",
		OrderID:  "order-1",
		Lat:      31.230004,
		Lng:      121.470002,
	}

	visible, err := service.ProjectVisibleLocation(context.Background(), raw)
	if err != nil {
		t.Fatalf("ProjectVisibleLocation returned error: %v", err)
	}
	if visible.Lat == raw.Lat && visible.Lng == raw.Lng {
		t.Fatalf("expected visible location to snap away from raw point, got %+v", visible)
	}
	if visible.Lng != route.Points[1].Lng {
		t.Fatalf("expected visible location to snap to road connector point lng %.6f, got %.6f", route.Points[1].Lng, visible.Lng)
	}
}
