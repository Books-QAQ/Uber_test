package location

import (
	"slices"
	"sync"

	"uber-test/backend/internal/model"
)

type MemoryStore struct {
	mu         sync.RWMutex
	maxRecent  int
	latest     map[string]model.DriverLocation
	recentByID map[string][]model.DriverLocation
}

func NewMemoryStore(maxRecent int) *MemoryStore {
	if maxRecent <= 0 {
		maxRecent = 20
	}

	return &MemoryStore{
		maxRecent:  maxRecent,
		latest:     make(map[string]model.DriverLocation),
		recentByID: make(map[string][]model.DriverLocation),
	}
}

func (s *MemoryStore) Upsert(location model.DriverLocation) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.latest[location.DriverID] = location

	recent := append(s.recentByID[location.DriverID], location)
	if len(recent) > s.maxRecent {
		recent = recent[len(recent)-s.maxRecent:]
	}
	s.recentByID[location.DriverID] = recent
}

func (s *MemoryStore) ListLatest() []model.DriverLocation {
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

	return items
}

func (s *MemoryStore) ListRecent(driverID string) []model.DriverLocation {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.recentByID[driverID]
	out := make([]model.DriverLocation, len(items))
	copy(out, items)
	return out
}
