package location

import (
	"context"
	"slices"
	"sync"

	"uber-test/backend/internal/model"
)

type MemoryStore struct {
	mu         sync.RWMutex
	maxRecent  int
	latest     map[string]model.DriverLocation
	recentByID map[string][]model.DriverLocation
	heartbeats map[string]model.DriverHeartbeat
}

func NewMemoryStore(maxRecent int) *MemoryStore {
	if maxRecent <= 0 {
		maxRecent = 20
	}

	return &MemoryStore{
		maxRecent:  maxRecent,
		latest:     make(map[string]model.DriverLocation),
		recentByID: make(map[string][]model.DriverLocation),
		heartbeats: make(map[string]model.DriverHeartbeat),
	}
}

func (s *MemoryStore) Upsert(_ context.Context, location model.DriverLocation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.latest[location.DriverID] = location

	recent := append(s.recentByID[location.DriverID], location)
	if len(recent) > s.maxRecent {
		recent = recent[len(recent)-s.maxRecent:]
	}
	s.recentByID[location.DriverID] = recent
	return nil
}

func (s *MemoryStore) UpsertBatch(ctx context.Context, locations []model.DriverLocation) error {
	for _, location := range locations {
		if err := s.Upsert(ctx, location); err != nil {
			return err
		}
	}
	return nil
}

func (s *MemoryStore) TouchHeartbeat(_ context.Context, heartbeat model.DriverHeartbeat) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.heartbeats[heartbeat.DriverID] = heartbeat
	return nil
}

func (s *MemoryStore) ListLatest(_ context.Context) ([]model.DriverLocation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]model.DriverLocation, 0, len(s.latest))
	for _, location := range s.latest {
		items = append(items, location)
	}

	slices.SortFunc(items, func(a, b model.DriverLocation) int {
		if a.Timestamp.Before(b.Timestamp) {
			return 1
		}
		if a.Timestamp.After(b.Timestamp) {
			return -1
		}
		return 0
	})

	return items, nil
}

func (s *MemoryStore) ListRecent(_ context.Context, driverID string) ([]model.DriverLocation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.recentByID[driverID]
	out := make([]model.DriverLocation, len(items))
	copy(out, items)
	return out, nil
}

func (s *MemoryStore) Close() error {
	return nil
}
