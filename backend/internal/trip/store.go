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
}

type MemoryStore struct {
	mu    sync.RWMutex
	trips map[string]model.Trip
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		trips: make(map[string]model.Trip),
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
