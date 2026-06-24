package location

import (
	"context"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/dhconnelly/rtreego"

	"uber-test/backend/internal/model"
)

const (
	rtreeDimensions   = 2
	rtreeMinChildren  = 8
	rtreeMaxChildren  = 16
	rtreePointEpsilon = 0.0000001
)

type MemoryStore struct {
	mu           sync.RWMutex
	maxRecent    int
	latest       map[string]model.DriverLocation
	recentByID   map[string]*recentLocationLRU
	heartbeats   map[string]model.DriverHeartbeat
	activityByID map[string]time.Time
	statuses     map[string]model.DriverStatus
	index        *rtreego.Rtree
	spatialByID  map[string]*driverSpatial
}

func NewMemoryStore(maxRecent int) *MemoryStore {
	if maxRecent <= 0 {
		maxRecent = 20
	}

	return &MemoryStore{
		maxRecent:    maxRecent,
		latest:       make(map[string]model.DriverLocation),
		recentByID:   make(map[string]*recentLocationLRU),
		heartbeats:   make(map[string]model.DriverHeartbeat),
		activityByID: make(map[string]time.Time),
		statuses:     make(map[string]model.DriverStatus),
		index:        rtreego.NewTree(rtreeDimensions, rtreeMinChildren, rtreeMaxChildren),
		spatialByID:  make(map[string]*driverSpatial),
	}
}

type driverSpatial struct {
	driverID string
	rect     rtreego.Rect
}

func newDriverSpatial(location model.DriverLocation) (*driverSpatial, error) {
	rect, err := rtreego.NewRect(
		rtreego.Point{location.Lat, location.Lng},
		[]float64{rtreePointEpsilon, rtreePointEpsilon},
	)
	if err != nil {
		return nil, err
	}

	return &driverSpatial{
		driverID: location.DriverID,
		rect:     rect,
	}, nil
}

func (d *driverSpatial) Bounds() rtreego.Rect {
	return d.rect
}

func (s *MemoryStore) Upsert(_ context.Context, location model.DriverLocation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.upsertLocked(location)
}

func (s *MemoryStore) UpsertBatch(ctx context.Context, locations []model.DriverLocation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, location := range locations {
		if err := s.upsertLocked(location); err != nil {
			return err
		}
	}
	return nil
}

func (s *MemoryStore) upsertLocked(location model.DriverLocation) error {
	s.latest[location.DriverID] = location
	if !location.Timestamp.IsZero() {
		s.activityByID[location.DriverID] = location.Timestamp
	}

	recent := s.recentByID[location.DriverID]
	if recent == nil {
		recent = newRecentLocationLRU(s.maxRecent)
		s.recentByID[location.DriverID] = recent
	}
	recent.Add(location)

	if existing := s.spatialByID[location.DriverID]; existing != nil {
		s.index.Delete(existing)
	}

	spatial, err := newDriverSpatial(location)
	if err != nil {
		return err
	}
	s.index.Insert(spatial)
	s.spatialByID[location.DriverID] = spatial
	return nil
}

func (s *MemoryStore) TouchHeartbeat(_ context.Context, heartbeat model.DriverHeartbeat) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.heartbeats[heartbeat.DriverID] = heartbeat
	if !heartbeat.Timestamp.IsZero() {
		s.activityByID[heartbeat.DriverID] = heartbeat.Timestamp
	}
	return nil
}

func (s *MemoryStore) SetDriverStatus(_ context.Context, status model.DriverStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.statuses[status.DriverID] = status
	if status.Status != model.DriverStatusOffline && !status.UpdatedAt.IsZero() {
		if lastActive, ok := s.activityByID[status.DriverID]; !ok || status.UpdatedAt.After(lastActive) {
			s.activityByID[status.DriverID] = status.UpdatedAt
		}
	}
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

func (s *MemoryStore) GetLatestByDriverID(_ context.Context, driverID string) (model.DriverLocation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	location, ok := s.latest[driverID]
	if !ok {
		return model.DriverLocation{}, ErrNotFound
	}
	return location, nil
}

func (s *MemoryStore) ListRecent(_ context.Context, driverID string) ([]model.DriverLocation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cache := s.recentByID[driverID]
	if cache == nil {
		return nil, nil
	}
	return cache.List(), nil
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

	searchRect, err := nearbySearchRect(query.Lat, query.Lng, query.RadiusM)
	if err != nil {
		return nil, err
	}

	candidates := s.index.SearchIntersect(searchRect)
	items := make([]model.NearbyDriver, 0, len(candidates))
	for _, candidate := range candidates {
		spatial, ok := candidate.(*driverSpatial)
		if !ok {
			continue
		}

		driverID := spatial.driverID
		location, exists := s.latest[driverID]
		if !exists {
			continue
		}

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

func (s *MemoryStore) ExpireInactive(_ context.Context, cutoff time.Time) ([]model.DriverStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	expired := make([]model.DriverStatus, 0)
	for driverID, status := range s.statuses {
		if status.Status == model.DriverStatusOffline {
			continue
		}

		lastActive := s.lastActiveAtLocked(driverID)
		if lastActive.IsZero() || lastActive.After(cutoff) {
			continue
		}

		status.Status = model.DriverStatusOffline
		status.UpdatedAt = cutoff
		s.statuses[driverID] = status
		expired = append(expired, status)
	}

	return expired, nil
}

func nearbySearchRect(lat, lng, radiusM float64) (rtreego.Rect, error) {
	latDelta := metersToLatDegrees(radiusM)
	lngDelta := metersToLngDegrees(radiusM, lat)
	if latDelta <= 0 {
		latDelta = rtreePointEpsilon
	}
	if lngDelta <= 0 {
		lngDelta = rtreePointEpsilon
	}

	return rtreego.NewRect(
		rtreego.Point{lat - latDelta, lng - lngDelta},
		[]float64{latDelta * 2, lngDelta * 2},
	)
}

func (s *MemoryStore) Close() error {
	return nil
}

func metersToLatDegrees(meters float64) float64 {
	return meters / 111320.0
}

func metersToLngDegrees(meters float64, lat float64) float64 {
	cosLat := math.Cos(lat * math.Pi / 180)
	if math.Abs(cosLat) < 0.000001 {
		return meters / 111320.0
	}
	return meters / (111320.0 * cosLat)
}

func (s *MemoryStore) lastActiveAtLocked(driverID string) time.Time {
	lastActive := s.activityByID[driverID]

	if heartbeat, ok := s.heartbeats[driverID]; ok && heartbeat.Timestamp.After(lastActive) {
		lastActive = heartbeat.Timestamp
	}
	if location, ok := s.latest[driverID]; ok && location.Timestamp.After(lastActive) {
		lastActive = location.Timestamp
	}
	if status, ok := s.statuses[driverID]; ok && status.UpdatedAt.After(lastActive) {
		lastActive = status.UpdatedAt
	}

	return lastActive
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
