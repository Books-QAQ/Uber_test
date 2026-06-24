package location

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"uber-test/backend/internal/model"
)

type MultiStore struct {
	primary   Store
	secondary Store
	logger    *slog.Logger
}

func NewMultiStore(primary Store, secondary Store, logger *slog.Logger) *MultiStore {
	return &MultiStore{
		primary:   primary,
		secondary: secondary,
		logger:    logger,
	}
}

func (s *MultiStore) Upsert(ctx context.Context, location model.DriverLocation) error {
	if err := s.primary.Upsert(ctx, location); err != nil {
		return fmt.Errorf("primary upsert: %w", err)
	}
	if err := s.secondary.Upsert(ctx, location); err != nil {
		return fmt.Errorf("secondary upsert: %w", err)
	}
	return nil
}

func (s *MultiStore) UpsertBatch(ctx context.Context, locations []model.DriverLocation) error {
	if err := s.primary.UpsertBatch(ctx, locations); err != nil {
		return fmt.Errorf("primary batch upsert: %w", err)
	}
	if err := s.secondary.UpsertBatch(ctx, locations); err != nil {
		return fmt.Errorf("secondary batch upsert: %w", err)
	}
	return nil
}

func (s *MultiStore) TouchHeartbeat(ctx context.Context, heartbeat model.DriverHeartbeat) error {
	if err := s.primary.TouchHeartbeat(ctx, heartbeat); err != nil {
		return fmt.Errorf("primary heartbeat: %w", err)
	}
	if err := s.secondary.TouchHeartbeat(ctx, heartbeat); err != nil {
		return fmt.Errorf("secondary heartbeat: %w", err)
	}
	return nil
}

func (s *MultiStore) SetDriverStatus(ctx context.Context, status model.DriverStatus) error {
	if err := s.primary.SetDriverStatus(ctx, status); err != nil {
		return fmt.Errorf("primary set driver status: %w", err)
	}
	if err := s.secondary.SetDriverStatus(ctx, status); err != nil {
		return fmt.Errorf("secondary set driver status: %w", err)
	}
	return nil
}

func (s *MultiStore) ListLatest(ctx context.Context) ([]model.DriverLocation, error) {
	items, err := s.primary.ListLatest(ctx)
	if err == nil && len(items) > 0 {
		return items, nil
	}
	if err != nil {
		s.logger.Warn("primary location store read failed, falling back to secondary", "error", err)
	}

	items, secondaryErr := s.secondary.ListLatest(ctx)
	if secondaryErr != nil {
		return nil, fmt.Errorf("secondary list latest: %w", secondaryErr)
	}
	return items, nil
}

func (s *MultiStore) GetLatestByDriverID(ctx context.Context, driverID string) (model.DriverLocation, error) {
	item, err := s.primary.GetLatestByDriverID(ctx, driverID)
	if err == nil {
		return item, nil
	}
	s.logger.Warn("primary latest-by-driver read failed, falling back to secondary", "driver_id", driverID, "error", err)

	item, secondaryErr := s.secondary.GetLatestByDriverID(ctx, driverID)
	if secondaryErr != nil {
		return model.DriverLocation{}, fmt.Errorf("secondary latest-by-driver read: %w", secondaryErr)
	}
	return item, nil
}

func (s *MultiStore) FindNearby(ctx context.Context, query model.NearbyQuery) ([]model.NearbyDriver, error) {
	items, err := s.primary.FindNearby(ctx, query)
	if err == nil && len(items) > 0 {
		return items, nil
	}
	if err != nil {
		s.logger.Warn("primary nearby store read failed, falling back to secondary", "error", err)
	}

	items, secondaryErr := s.secondary.FindNearby(ctx, query)
	if secondaryErr != nil {
		return nil, fmt.Errorf("secondary nearby read: %w", secondaryErr)
	}
	return items, nil
}

func (s *MultiStore) ListRecent(ctx context.Context, driverID string) ([]model.DriverLocation, error) {
	items, err := s.primary.ListRecent(ctx, driverID)
	if err == nil && len(items) > 0 {
		return items, nil
	}
	if err != nil {
		s.logger.Warn("primary recent store read failed, falling back to secondary", "driver_id", driverID, "error", err)
	}

	items, secondaryErr := s.secondary.ListRecent(ctx, driverID)
	if secondaryErr != nil {
		return nil, fmt.Errorf("secondary list recent: %w", secondaryErr)
	}
	return items, nil
}

func (s *MultiStore) ExpireInactive(ctx context.Context, cutoff time.Time) ([]model.DriverStatus, error) {
	primaryExpired, err := s.primary.ExpireInactive(ctx, cutoff)
	if err != nil {
		return nil, fmt.Errorf("primary expire inactive: %w", err)
	}

	secondaryExpired, err := s.secondary.ExpireInactive(ctx, cutoff)
	if err != nil {
		return nil, fmt.Errorf("secondary expire inactive: %w", err)
	}

	if len(primaryExpired) == 0 && len(secondaryExpired) == 0 {
		return nil, nil
	}

	merged := make(map[string]model.DriverStatus, len(primaryExpired)+len(secondaryExpired))
	for _, status := range secondaryExpired {
		merged[status.DriverID] = status
	}
	for _, status := range primaryExpired {
		merged[status.DriverID] = status
	}

	items := make([]model.DriverStatus, 0, len(merged))
	for _, status := range merged {
		items = append(items, status)
	}
	return items, nil
}

func (s *MultiStore) Close() error {
	if err := s.secondary.Close(); err != nil {
		return err
	}
	return s.primary.Close()
}
