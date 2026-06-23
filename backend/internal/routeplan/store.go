package routeplan

import (
	"context"
	"sync"

	"uber-test/backend/internal/model"
)

type Store interface {
	Save(ctx context.Context, route model.DriverRoute) error
	GetByOrderID(ctx context.Context, orderID string) (model.DriverRoute, error)
	ClearByDriverID(ctx context.Context, driverID string) (model.DriverRoute, error)
}

type MemoryStore struct {
	mu            sync.RWMutex
	byOrderID     map[string]model.DriverRoute
	orderByDriver map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		byOrderID:     make(map[string]model.DriverRoute),
		orderByDriver: make(map[string]string),
	}
}

func (s *MemoryStore) Save(_ context.Context, route model.DriverRoute) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if previousOrderID, ok := s.orderByDriver[route.DriverID]; ok && previousOrderID != "" && previousOrderID != route.OrderID {
		delete(s.byOrderID, previousOrderID)
	}

	s.orderByDriver[route.DriverID] = route.OrderID
	s.byOrderID[route.OrderID] = cloneRoute(route)
	return nil
}

func (s *MemoryStore) GetByOrderID(_ context.Context, orderID string) (model.DriverRoute, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	route, ok := s.byOrderID[orderID]
	if !ok {
		return model.DriverRoute{}, ErrNotFound
	}
	return cloneRoute(route), nil
}

func (s *MemoryStore) ClearByDriverID(_ context.Context, driverID string) (model.DriverRoute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	orderID, ok := s.orderByDriver[driverID]
	if !ok || orderID == "" {
		return model.DriverRoute{}, ErrNotFound
	}

	route := s.byOrderID[orderID]
	delete(s.orderByDriver, driverID)
	delete(s.byOrderID, orderID)
	return cloneRoute(route), nil
}

func cloneRoute(route model.DriverRoute) model.DriverRoute {
	cloned := route
	cloned.Points = append([]model.RoutePoint(nil), route.Points...)
	return cloned
}
