package order

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"uber-test/backend/internal/model"
)

type Store interface {
	Create(ctx context.Context, order model.Order) error
	GetByID(ctx context.Context, id string) (model.Order, error)
	List(ctx context.Context) ([]model.Order, error)
	Update(ctx context.Context, order model.Order) error
}

type MemoryStore struct {
	mu     sync.RWMutex
	orders map[string]model.Order
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		orders: make(map[string]model.Order),
	}
}

func (s *MemoryStore) Create(_ context.Context, order model.Order) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.orders[order.ID]; exists {
		return fmt.Errorf("order %s already exists", order.ID)
	}
	s.orders[order.ID] = order
	return nil
}

func (s *MemoryStore) GetByID(_ context.Context, id string) (model.Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	order, ok := s.orders[id]
	if !ok {
		return model.Order{}, ErrNotFound
	}
	return order, nil
}

func (s *MemoryStore) List(_ context.Context) ([]model.Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]model.Order, 0, len(s.orders))
	for _, order := range s.orders {
		items = append(items, order)
	}

	slices.SortFunc(items, func(a, b model.Order) int {
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

func (s *MemoryStore) Update(_ context.Context, order model.Order) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.orders[order.ID]; !exists {
		return ErrNotFound
	}
	s.orders[order.ID] = order
	return nil
}
