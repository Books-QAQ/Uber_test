package location

import (
	"context"
	"math"
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
	statuses   map[string]model.DriverStatus
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
		statuses:   make(map[string]model.DriverStatus),
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

func (s *MemoryStore) SetDriverStatus(_ context.Context, status model.DriverStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.statuses[status.DriverID] = status
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

func (s *MemoryStore) FindNearby(_ context.Context, query model.NearbyQuery) ([]model.NearbyDriver, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if query.RadiusM <= 0 {
		query.RadiusM = 3000
	}
	if query.Limit <= 0 {
		query.Limit = 20
	}

	items := make([]model.NearbyDriver, 0, len(s.latest))
	for driverID, location := range s.latest {
		status, ok := s.statuses[driverID]
		if query.OnlyLive {
			if !ok || !model.IsDriverStatusAvailableForNearby(status.Status) {
				continue
			}
		}

		distance := haversineMeters(query.Lat, query.Lng, location.Lat, location.Lng)
		if distance > query.RadiusM {
			continue
		}

		driverStatus := model.DriverStatusOffline
		updatedAt := location.Timestamp
		if ok {
			driverStatus = status.Status
			if !status.UpdatedAt.IsZero() {
				updatedAt = status.UpdatedAt
			}
		}

		items = append(items, model.NearbyDriver{
			DriverID:  driverID,
			Status:    driverStatus,
			DistanceM: distance,
			Location:  location,
			UpdatedAt: updatedAt,
		})
	}

	slices.SortFunc(items, func(a, b model.NearbyDriver) int {
		switch {
		case a.DistanceM < b.DistanceM:
			return -1
		case a.DistanceM > b.DistanceM:
			return 1
		default:
			return 0
		}
	})

	if len(items) > query.Limit {
		items = items[:query.Limit]
	}

	return items, nil
}

func (s *MemoryStore) Close() error {
	return nil
}

func haversineMeters(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadiusM = 6371000.0

	lat1Rad := lat1 * math.Pi / 180
	lat2Rad := lat2 * math.Pi / 180
	deltaLat := (lat2 - lat1) * math.Pi / 180
	deltaLng := (lng2 - lng1) * math.Pi / 180

	a := math.Sin(deltaLat/2)*math.Sin(deltaLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*
			math.Sin(deltaLng/2)*math.Sin(deltaLng/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadiusM * c
}
