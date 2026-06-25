package mapmatch

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"uber-test/backend/internal/model"
)

type RecentReader interface {
	ListRecent(ctx context.Context, driverID string) ([]model.DriverLocation, error)
}

type Service struct {
	reader      RecentReader
	matcher     Matcher
	logger      *slog.Logger
	minPoints   int
	windowSize  int
	maxLookback time.Duration

	mu     sync.RWMutex
	latest map[string]model.DriverLocation
}

func NewService(reader RecentReader, matcher Matcher, logger *slog.Logger, minPoints, windowSize int, maxLookback time.Duration) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if minPoints <= 0 {
		minPoints = 4
	}
	if windowSize <= 0 {
		windowSize = 8
	}
	if maxLookback <= 0 {
		maxLookback = 45 * time.Second
	}

	return &Service{
		reader:      reader,
		matcher:     matcher,
		logger:      logger,
		minPoints:   minPoints,
		windowSize:  windowSize,
		maxLookback: maxLookback,
		latest:      make(map[string]model.DriverLocation),
	}
}

func (s *Service) Sync(ctx context.Context, raw model.DriverLocation) (model.DriverLocation, error) {
	visible := raw

	if s == nil || s.reader == nil || s.matcher == nil || raw.DriverID == "" {
		s.setLatest(visible)
		return visible, nil
	}
	if raw.OrderID != "" {
		// Simulated/active-trip locations already move on a routed path; re-matching them
		// can snap points onto neighboring lanes and make the marker "wander".
		s.setLatest(visible)
		return visible, nil
	}

	recent, err := s.reader.ListRecent(ctx, raw.DriverID)
	if err != nil || len(recent) == 0 {
		s.setLatest(visible)
		return visible, err
	}

	window := s.prepareWindow(recent)
	if len(window) < s.minPoints {
		s.setLatest(visible)
		return visible, nil
	}

	matched, err := s.matcher.Match(ctx, window)
	if err != nil {
		s.setLatest(visible)
		return visible, err
	}
	if len(matched) == 0 {
		s.setLatest(visible)
		return visible, nil
	}

	visible = matched[len(matched)-1]
	s.setLatest(visible)
	return visible, nil
}

func (s *Service) GetLatest(driverID string) (model.DriverLocation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	location, ok := s.latest[driverID]
	return location, ok
}

func (s *Service) prepareWindow(recent []model.DriverLocation) []model.DriverLocation {
	if len(recent) == 0 {
		return nil
	}

	latest := recent[len(recent)-1]
	threshold := latest.Timestamp.Add(-s.maxLookback)
	window := make([]model.DriverLocation, 0, minInt(len(recent), s.windowSize))
	for i := len(recent) - 1; i >= 0; i-- {
		point := recent[i]
		if !point.Timestamp.IsZero() && point.Timestamp.Before(threshold) {
			break
		}
		window = append(window, point)
		if len(window) >= s.windowSize {
			break
		}
	}

	// Reverse into chronological order.
	for i, j := 0, len(window)-1; i < j; i, j = i+1, j-1 {
		window[i], window[j] = window[j], window[i]
	}
	return window
}

func (s *Service) setLatest(location model.DriverLocation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest[location.DriverID] = location
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
