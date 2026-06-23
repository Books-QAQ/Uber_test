package routeplan

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"uber-test/backend/internal/model"
)

type Broadcaster interface {
	BroadcastJSON(v any)
}

type Service struct {
	store       Store
	broadcaster Broadcaster
	logger      *slog.Logger
}

func NewService(store Store, broadcaster Broadcaster, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:       store,
		broadcaster: broadcaster,
		logger:      logger,
	}
}

func (s *Service) Upsert(ctx context.Context, route model.DriverRoute) (model.DriverRoute, error) {
	if route.DriverID == "" {
		return model.DriverRoute{}, fmt.Errorf("upsert route: missing driver_id")
	}
	if len(route.Points) == 0 {
		if err := s.ClearByDriverID(ctx, route.DriverID); err != nil {
			return model.DriverRoute{}, err
		}
		return model.DriverRoute{}, nil
	}
	if route.OrderID == "" {
		return model.DriverRoute{}, fmt.Errorf("upsert route: missing order_id")
	}

	route.Mode = strings.TrimSpace(route.Mode)
	if route.UpdatedAt.IsZero() {
		route.UpdatedAt = time.Now().UTC()
	}

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
