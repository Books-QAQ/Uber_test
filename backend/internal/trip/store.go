package trip

import (
	"context"
	"slices"
	"sync"

	"uber-test/backend/internal/model"
)

type Store interface {
	Save(ctx context.Context, trip model.Trip) error
	GetByOrderID(ctx context.Context, orderID string) (model.Trip, error)
	List(ctx context.Context) ([]model.Trip, error)
	SavePoint(ctx context.Context, point model.TripPoint) error
	ListPointsByTripID(ctx context.Context, tripID string) ([]model.TripPoint, error)
	GetLastPointByTripID(ctx context.Context, tripID string) (model.TripPoint, error)
}

type MemoryStore struct {
	mu           sync.RWMutex
	trips        map[string]model.Trip
	pointsByTrip map[string][]model.TripPoint
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		trips:        make(map[string]model.Trip),
		pointsByTrip: make(map[string][]model.TripPoint),
	}
}

func (s *MemoryStore) Save(_ context.Context, trip model.Trip) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.trips[trip.OrderID] = trip
	return nil
}

func (s *MemoryStore) GetByOrderID(_ context.Context, orderID string) (model.Trip, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	trip, ok := s.trips[orderID]
	if !ok {
		return model.Trip{}, ErrNotFound
	}
	return trip, nil
}

func (s *MemoryStore) List(_ context.Context) ([]model.Trip, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]model.Trip, 0, len(s.trips))
	for _, trip := range s.trips {
		items = append(items, trip)
	}

	slices.SortFunc(items, func(a, b model.Trip) int {
		if a.CreatedAt.After(b.CreatedAt) {
			return -1
		}
		if a.CreatedAt.Before(b.CreatedAt) {
			return 1
		}
		return 0
	})

	return items, nil
}

func (s *MemoryStore) SavePoint(_ context.Context, point model.TripPoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pointsByTrip[point.TripID] = append(s.pointsByTrip[point.TripID], point)
	return nil
}

func (s *MemoryStore) ListPointsByTripID(_ context.Context, tripID string) ([]model.TripPoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	points := s.pointsByTrip[tripID]
	if len(points) == 0 {
		return nil, ErrNotFound
	}

	result := make([]model.TripPoint, len(points))
	copy(result, points)
	return result, nil
}

func (s *MemoryStore) GetLastPointByTripID(_ context.Context, tripID string) (model.TripPoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	points := s.pointsByTrip[tripID]
	if len(points) == 0 {
		return model.TripPoint{}, ErrNotFound
	}
	return points[len(points)-1], nil
}
