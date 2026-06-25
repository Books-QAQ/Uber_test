package main

import (
	"math"
	"testing"

	"uber-test/backend/internal/model"
)

func TestRemainingRouteDistanceLockedUsesPathLength(t *testing.T) {
	agent := &driverAgent{
		current: &trackedOrder{
			Order: model.Order{ID: "order-1", Status: model.OrderStatusAccepted},
		},
		routeMode:    "pickup",
		routeOrderID: "order-1",
		routeTarget:  routePoint{Lat: 31.0010, Lng: 121.0000},
		routePath: []routePoint{
			{Lat: 31.0000, Lng: 121.0010},
			{Lat: 31.0010, Lng: 121.0010},
			{Lat: 31.0010, Lng: 121.0000},
		},
	}

	got := agent.remainingRouteDistanceLocked(31.0000, 121.0000, 31.0010, 121.0000, "pickup")
	direct := linearDistanceMeters(31.0000, 121.0000, 31.0010, 121.0000)
	if got <= direct {
		t.Fatalf("expected routed distance %.2f to exceed direct distance %.2f", got, direct)
	}
}

func TestCanAdvanceAlongRouteRequiresShortRemainingRoute(t *testing.T) {
	agent := &driverAgent{
		current: &trackedOrder{
			Order: model.Order{ID: "order-1", Status: model.OrderStatusAccepted, PickupLat: 31.0000, PickupLng: 121.00005},
		},
		currentLat:    31.0000,
		currentLng:    121.0000,
		arriveWithinM: 45,
		routeMode:     "pickup",
		routeOrderID:  "order-1",
		routeTarget:   routePoint{Lat: 31.0010, Lng: 121.0000},
		activeStepM:   35,
		routePath: []routePoint{
			{Lat: 31.0000, Lng: 121.0010},
			{Lat: 31.0010, Lng: 121.0010},
			{Lat: 31.0010, Lng: 121.0000},
		},
	}

	if agent.canAdvanceAlongRoute(31.0000, 121.00005, "pickup") {
		t.Fatal("expected advance check to fail while a long remaining routed path still exists")
	}

	agent.routePath = []routePoint{{Lat: 31.0000, Lng: 121.00005}}
	agent.routeTarget = routePoint{Lat: 31.0000, Lng: 121.00005}
	if !agent.canAdvanceAlongRoute(31.0000, 121.00005, "pickup") {
		t.Fatal("expected advance check to succeed when only a few meters remain on the routed path")
	}
}

func TestCanAdvanceAlongRouteFallsBackToActualTargetWithoutRouteTarget(t *testing.T) {
	agent := &driverAgent{
		current: &trackedOrder{
			Order: model.Order{ID: "order-1", Status: model.OrderStatusAccepted},
		},
		currentLat:    31.0000,
		currentLng:    121.0000,
		arriveWithinM: 45,
		routeMode:     "pickup",
		routeOrderID:  "order-1",
		routeTarget:   routePoint{},
		routePath:     nil,
	}

	if agent.canAdvanceAlongRoute(31.0010, 121.0000, "pickup") {
		t.Fatal("expected advance check to fail when no routed endpoint is available and the raw pickup is still far away")
	}
}

func TestCanAdvanceAlongRouteUsesRoutedEndpointWhenAvailable(t *testing.T) {
	agent := &driverAgent{
		current: &trackedOrder{
			Order: model.Order{ID: "order-1", Status: model.OrderStatusInTrip, DestinationLat: 31.0000, DestinationLng: 121.0000},
		},
		currentLat:    31.0000,
		currentLng:    121.0000,
		arriveWithinM: 45,
		routeMode:     "trip",
		routeOrderID:  "order-1",
		routeTarget:   routePoint{Lat: 31.0000, Lng: 121.00005},
		routePath:     []routePoint{{Lat: 31.0000, Lng: 121.00005}},
	}

	if !agent.canAdvanceAlongRoute(31.0010, 121.0010, "trip") {
		t.Fatal("expected advance check to succeed once the routed endpoint is reached, even if the raw destination is farther away")
	}
}

func TestFinalSnapThresholdMetersStaysTight(t *testing.T) {
	got := finalSnapThresholdMeters(35)
	if math.Abs(got-10) > 0.001 {
		t.Fatalf("expected snap threshold to clamp to 10m, got %.2f", got)
	}
}

func TestDropNearCurrentPrefixSkipsConsumedLeadingPoints(t *testing.T) {
	path := []routePoint{
		{Lat: 31.0000, Lng: 121.0000},
		{Lat: 31.0000, Lng: 121.00005},
		{Lat: 31.0000, Lng: 121.00030},
	}

	got := dropNearCurrentPrefix(path, routePoint{Lat: 31.0000, Lng: 121.00004}, 20)
	if len(got) != 1 {
		t.Fatalf("expected one remaining route point after dropping stale prefix, got %d", len(got))
	}
	if got[0] != (routePoint{Lat: 31.0000, Lng: 121.00030}) {
		t.Fatalf("expected remaining route to start at the first not-yet-consumed point, got %+v", got[0])
	}
}

func TestDropNearCurrentPrefixKeepsFinalPointForLastHop(t *testing.T) {
	path := []routePoint{
		{Lat: 31.00000, Lng: 121.00000},
		{Lat: 31.00000, Lng: 121.00015},
	}

	got := dropNearCurrentPrefix(path, routePoint{Lat: 31.00000, Lng: 121.00014}, 20)
	if len(got) != 1 {
		t.Fatalf("expected the final hop to be preserved, got %d points", len(got))
	}
	if got[0] != path[len(path)-1] {
		t.Fatalf("expected to keep the final route point %+v, got %+v", path[len(path)-1], got[0])
	}
}
