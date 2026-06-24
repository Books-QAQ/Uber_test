package routeplan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"uber-test/backend/internal/location"
	"uber-test/backend/internal/model"
	"uber-test/backend/internal/order"
)

const (
	defaultDeviationRefreshM = 20.0
	defaultArrivalThresholdM = 45.0
)

type Broadcaster interface {
	BroadcastJSON(v any)
}

type OrderReader interface {
	GetByID(ctx context.Context, id string) (model.Order, error)
	FindActiveByDriverID(ctx context.Context, driverID string) (model.Order, error)
}

type LocationReader interface {
	GetLatestByDriverID(ctx context.Context, driverID string) (model.DriverLocation, error)
}

type Service struct {
	store             Store
	orders            OrderReader
	locations         LocationReader
	planner           Planner
	deviationRefreshM float64
	arrivalThresholdM float64
	broadcaster       Broadcaster
	logger            *slog.Logger
}

func NewService(store Store, orders OrderReader, locations LocationReader, planner Planner, broadcaster Broadcaster, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}

	return &Service{
		store:             store,
		orders:            orders,
		locations:         locations,
		planner:           planner,
		deviationRefreshM: defaultDeviationRefreshM,
		arrivalThresholdM: defaultArrivalThresholdM,
		broadcaster:       broadcaster,
		logger:            logger,
	}
}

func (s *Service) Upsert(ctx context.Context, route model.DriverRoute) (model.DriverRoute, error) {
	if route.DriverID == "" && route.Mode != "preview" {
		return model.DriverRoute{}, fmt.Errorf("upsert route: missing driver_id")
	}
	if len(route.Points) == 0 {
		if route.DriverID == "" {
			return model.DriverRoute{}, nil
		}
		if err := s.ClearByDriverID(ctx, route.DriverID); err != nil {
			return model.DriverRoute{}, err
		}
		return model.DriverRoute{}, nil
	}
	if route.OrderID == "" && route.Mode != "preview" {
		return model.DriverRoute{}, fmt.Errorf("upsert route: missing order_id")
	}

	route.Mode = strings.TrimSpace(route.Mode)
	if route.UpdatedAt.IsZero() {
		route.UpdatedAt = time.Now().UTC()
	}
	route.Points = dedupePoints(route.Points)

	if err := s.store.Save(ctx, route); err != nil {
		return model.DriverRoute{}, err
	}

	if s.broadcaster != nil {
		s.broadcaster.BroadcastJSON(map[string]any{
			"type": "driver.route.updated",
			"data": route,
		})
	}
	s.logger.Debug("driver route updated", "driver_id", route.DriverID, "order_id", route.OrderID, "mode", route.Mode, "points", len(route.Points))
	return route, nil
}

func (s *Service) GetByOrderID(ctx context.Context, orderID string) (model.DriverRoute, error) {
	if orderID == "" {
		return model.DriverRoute{}, fmt.Errorf("get route: missing order_id")
	}
	return s.store.GetByOrderID(ctx, orderID)
}

func (s *Service) ClearByDriverID(ctx context.Context, driverID string) error {
	if driverID == "" {
		return fmt.Errorf("clear route: missing driver_id")
	}

	route, err := s.store.ClearByDriverID(ctx, driverID)
	if err != nil {
		if err == ErrNotFound {
			return nil
		}
		return err
	}

	if s.broadcaster != nil {
		s.broadcaster.BroadcastJSON(map[string]any{
			"type": "driver.route.cleared",
			"data": map[string]any{
				"driver_id": route.DriverID,
				"order_id":  route.OrderID,
			},
		})
	}
	s.logger.Debug("driver route cleared", "driver_id", route.DriverID, "order_id", route.OrderID)
	return nil
}

func (s *Service) PlanPreview(ctx context.Context, originLat, originLng, destinationLat, destinationLng float64) (model.DriverRoute, error) {
	points, err := s.planPath(ctx, originLat, originLng, destinationLat, destinationLng)
	if err != nil {
		return model.DriverRoute{}, err
	}
	return model.DriverRoute{
		Mode:      "preview",
		Points:    points,
		UpdatedAt: time.Now().UTC(),
	}, nil
}

func (s *Service) SyncDriverLocation(ctx context.Context, locationUpdate model.DriverLocation) error {
	if s == nil || s.store == nil || s.orders == nil {
		return nil
	}
	if locationUpdate.DriverID == "" {
		return nil
	}

	orderItem, err := s.resolveActiveOrder(ctx, locationUpdate)
	if err != nil {
		return err
	}
	if orderItem.ID == "" || !isRouteableOrderStatus(orderItem.Status) || orderItem.DriverID == "" {
		return s.ClearByDriverID(ctx, locationUpdate.DriverID)
	}

	return s.syncRouteFor(ctx, locationUpdate, orderItem)
}

func (s *Service) SyncOrder(ctx context.Context, orderItem model.Order) error {
	if s == nil || s.store == nil {
		return nil
	}
	if !isRouteableOrderStatus(orderItem.Status) || orderItem.DriverID == "" {
		if orderItem.DriverID == "" {
			return nil
		}
		return s.ClearByDriverID(ctx, orderItem.DriverID)
	}
	if s.locations == nil {
		return nil
	}

	locationUpdate, err := s.locations.GetLatestByDriverID(ctx, orderItem.DriverID)
	if err != nil {
		if errors.Is(err, location.ErrNotFound) {
			return nil
		}
		return err
	}
	return s.syncRouteFor(ctx, locationUpdate, orderItem)
}

func (s *Service) resolveActiveOrder(ctx context.Context, locationUpdate model.DriverLocation) (model.Order, error) {
	if locationUpdate.OrderID != "" && s.orders != nil {
		orderItem, err := s.orders.GetByID(ctx, locationUpdate.OrderID)
		if err == nil {
			return orderItem, nil
		}
		if !errors.Is(err, order.ErrNotFound) {
			return model.Order{}, err
		}
	}
	if s.orders == nil {
		return model.Order{}, nil
	}
	orderItem, err := s.orders.FindActiveByDriverID(ctx, locationUpdate.DriverID)
	if err != nil {
		if errors.Is(err, order.ErrNotFound) {
			return model.Order{}, nil
		}
		return model.Order{}, err
	}
	return orderItem, nil
}

func (s *Service) syncRouteFor(ctx context.Context, locationUpdate model.DriverLocation, orderItem model.Order) error {
	mode, destination, ok := routeTargetFor(orderItem)
	if !ok {
		return s.ClearByDriverID(ctx, locationUpdate.DriverID)
	}

	if linearDistanceMeters(locationUpdate.Lat, locationUpdate.Lng, destination.Lat, destination.Lng) <= s.arrivalThresholdM {
		points := dedupePoints([]model.RoutePoint{
			{Lat: locationUpdate.Lat, Lng: locationUpdate.Lng},
			destination,
		})
		_, err := s.Upsert(ctx, model.DriverRoute{
			DriverID:  orderItem.DriverID,
			OrderID:   orderItem.ID,
			Mode:      mode,
			Points:    points,
			UpdatedAt: time.Now().UTC(),
		})
		return err
	}

	existing, err := s.store.GetByOrderID(ctx, orderItem.ID)
	if err == nil && existing.DriverID == orderItem.DriverID && existing.Mode == mode {
		if remaining := trimDrivenPath(existing.Points, model.RoutePoint{Lat: locationUpdate.Lat, Lng: locationUpdate.Lng}); len(remaining) > 0 {
			if distanceToPathMeters(model.RoutePoint{Lat: locationUpdate.Lat, Lng: locationUpdate.Lng}, existing.Points) <= s.deviationRefreshM {
				_, err := s.Upsert(ctx, model.DriverRoute{
					DriverID:  orderItem.DriverID,
					OrderID:   orderItem.ID,
					Mode:      mode,
					Points:    remaining,
					UpdatedAt: time.Now().UTC(),
				})
				return err
			}
		}
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	points, err := s.planPath(ctx, locationUpdate.Lat, locationUpdate.Lng, destination.Lat, destination.Lng)
	if err != nil {
		s.logger.Warn("route planning failed, falling back to straight line", "driver_id", orderItem.DriverID, "order_id", orderItem.ID, "mode", mode, "error", err)
	}
	points = trimDrivenPath(points, model.RoutePoint{Lat: locationUpdate.Lat, Lng: locationUpdate.Lng})
	if len(points) == 0 {
		points = dedupePoints([]model.RoutePoint{
			{Lat: locationUpdate.Lat, Lng: locationUpdate.Lng},
			destination,
		})
	}

	_, err = s.Upsert(ctx, model.DriverRoute{
		DriverID:  orderItem.DriverID,
		OrderID:   orderItem.ID,
		Mode:      mode,
		Points:    points,
		UpdatedAt: time.Now().UTC(),
	})
	return err
}

func (s *Service) planPath(ctx context.Context, originLat, originLng, destinationLat, destinationLng float64) ([]model.RoutePoint, error) {
	if linearDistanceMeters(originLat, originLng, destinationLat, destinationLng) <= s.arrivalThresholdM {
		return dedupePoints([]model.RoutePoint{
			{Lat: originLat, Lng: originLng},
			{Lat: destinationLat, Lng: destinationLng},
		}), nil
	}

	if s.planner == nil {
		return dedupePoints([]model.RoutePoint{
			{Lat: originLat, Lng: originLng},
			{Lat: destinationLat, Lng: destinationLng},
		}), nil
	}

	points, err := s.planner.Plan(ctx, originLat, originLng, destinationLat, destinationLng)
	if err != nil {
		return dedupePoints([]model.RoutePoint{
			{Lat: originLat, Lng: originLng},
			{Lat: destinationLat, Lng: destinationLng},
		}), err
	}
	return dedupePoints(points), nil
}

func routeTargetFor(orderItem model.Order) (string, model.RoutePoint, bool) {
	switch orderItem.Status {
	case model.OrderStatusAccepted, model.OrderStatusDriverArrived:
		return "pickup", model.RoutePoint{Lat: orderItem.PickupLat, Lng: orderItem.PickupLng}, true
	case model.OrderStatusInTrip:
		return "trip", model.RoutePoint{Lat: orderItem.DestinationLat, Lng: orderItem.DestinationLng}, true
	default:
		return "", model.RoutePoint{}, false
	}
}

func isRouteableOrderStatus(status string) bool {
	switch status {
	case model.OrderStatusAccepted, model.OrderStatusDriverArrived, model.OrderStatusInTrip:
		return true
	default:
		return false
	}
}

func trimDrivenPath(path []model.RoutePoint, driverPosition model.RoutePoint) []model.RoutePoint {
	normalized := dedupePoints(path)
	if len(normalized) == 0 {
		return nil
	}
	if len(normalized) == 1 {
		return dedupePoints([]model.RoutePoint{driverPosition, normalized[0]})
	}

	nearestSegmentIndex := 0
	nearestProjection := normalized[0]
	nearestDistance := math.MaxFloat64
	for index := 0; index < len(normalized)-1; index++ {
		projection := projectPointToSegment(driverPosition, normalized[index], normalized[index+1])
		if projection.distance < nearestDistance {
			nearestDistance = projection.distance
			nearestSegmentIndex = index
			nearestProjection = projection.point
		}
	}

	visiblePath := []model.RoutePoint{driverPosition}
	if !samePoint(visiblePath[len(visiblePath)-1], nearestProjection) {
		visiblePath = append(visiblePath, nearestProjection)
	}
	for _, point := range normalized[nearestSegmentIndex+1:] {
		if !samePoint(visiblePath[len(visiblePath)-1], point) {
			visiblePath = append(visiblePath, point)
		}
	}
	return dedupePoints(visiblePath)
}

func distanceToPathMeters(point model.RoutePoint, path []model.RoutePoint) float64 {
	if len(path) == 0 {
		return math.MaxFloat64
	}
	if len(path) == 1 {
		return linearDistanceMeters(point.Lat, point.Lng, path[0].Lat, path[0].Lng)
	}

	best := math.MaxFloat64
	for index := 0; index < len(path)-1; index++ {
		projection := projectPointToSegment(point, path[index], path[index+1])
		if projection.distance < best {
			best = projection.distance
		}
	}
	return best
}

type projectedPoint struct {
	point    model.RoutePoint
	distance float64
}

func projectPointToSegment(point, start, end model.RoutePoint) projectedPoint {
	midLatRad := ((start.Lat + end.Lat + point.Lat) / 3) * math.Pi / 180
	scaleX := 111320 * math.Cos(midLatRad)
	scaleY := 111320.0

	sx := start.Lng * scaleX
	sy := start.Lat * scaleY
	ex := end.Lng * scaleX
	ey := end.Lat * scaleY
	px := point.Lng * scaleX
	py := point.Lat * scaleY

	dx := ex - sx
	dy := ey - sy
	lengthSquared := dx*dx + dy*dy
	if lengthSquared == 0 {
		return projectedPoint{
			point:    start,
			distance: linearDistanceMeters(point.Lat, point.Lng, start.Lat, start.Lng),
		}
	}

	ratio := math.Max(0, math.Min(1, ((px-sx)*dx+(py-sy)*dy)/lengthSquared))
	projected := model.RoutePoint{
		Lat: start.Lat + (end.Lat-start.Lat)*ratio,
		Lng: start.Lng + (end.Lng-start.Lng)*ratio,
	}
	return projectedPoint{
		point:    projected,
		distance: linearDistanceMeters(point.Lat, point.Lng, projected.Lat, projected.Lng),
	}
}

func linearDistanceMeters(fromLat, fromLng, toLat, toLng float64) float64 {
	latMeters := (toLat - fromLat) * 111320.0
	midLatRad := ((fromLat + toLat) / 2) * math.Pi / 180
	lngMeters := (toLng - fromLng) * 111320.0 * math.Cos(midLatRad)
	return math.Hypot(latMeters, lngMeters)
}
