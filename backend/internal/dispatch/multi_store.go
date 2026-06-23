package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"uber-test/backend/internal/model"
)

type MultiStore struct {
	cache      Store
	persistent Store
	logger     *slog.Logger
}

func NewMultiStore(cache Store, persistent Store, logger *slog.Logger) *MultiStore {
	return &MultiStore{
		cache:      cache,
		persistent: persistent,
		logger:     logger,
	}
}

func (s *MultiStore) CreateBatch(ctx context.Context, records []model.DispatchRecord) error {
	if err := s.persistent.CreateBatch(ctx, records); err != nil {
		return fmt.Errorf("persistent create dispatch batch: %w", err)
	}
	if err := s.cache.CreateBatch(ctx, records); err != nil {
		return fmt.Errorf("cache create dispatch batch: %w", err)
	}
	return nil
}

func (s *MultiStore) ListPendingByDriverID(ctx context.Context, driverID string) ([]model.DispatchRecord, error) {
	items, err := s.cache.ListPendingByDriverID(ctx, driverID)
	if err == nil && len(items) > 0 {
		return items, nil
	}
	if err != nil {
		s.logger.Warn("dispatch cache read failed, falling back to persistent store", "driver_id", driverID, "error", err)
	}

	items, persistentErr := s.persistent.ListPendingByDriverID(ctx, driverID)
	if persistentErr != nil {
		return nil, fmt.Errorf("persistent dispatch list by driver: %w", persistentErr)
	}
	return items, nil
}

func (s *MultiStore) ListPendingByOrderID(ctx context.Context, orderID string) ([]model.DispatchRecord, error) {
	items, err := s.cache.ListPendingByOrderID(ctx, orderID)
	if err == nil && len(items) > 0 {
		return items, nil
	}
	if err != nil {
		s.logger.Warn("dispatch cache read by order failed, falling back to persistent store", "order_id", orderID, "error", err)
	}

	items, persistentErr := s.persistent.ListPendingByOrderID(ctx, orderID)
	if persistentErr != nil {
		return nil, fmt.Errorf("persistent dispatch list by order: %w", persistentErr)
	}
	return items, nil
}

func (s *MultiStore) ListByOrderID(ctx context.Context, orderID string) ([]model.DispatchRecord, error) {
	items, err := s.cache.ListByOrderID(ctx, orderID)
	if err == nil && len(items) > 0 {
		return items, nil
	}
	if err != nil {
		s.logger.Warn("dispatch cache history read by order failed, falling back to persistent store", "order_id", orderID, "error", err)
	}

	items, persistentErr := s.persistent.ListByOrderID(ctx, orderID)
	if persistentErr != nil {
		return nil, fmt.Errorf("persistent dispatch list history by order: %w", persistentErr)
	}
	return items, nil
}

func (s *MultiStore) GetPendingByOrderAndDriver(ctx context.Context, orderID, driverID string) (model.DispatchRecord, error) {
	item, err := s.cache.GetPendingByOrderAndDriver(ctx, orderID, driverID)
	if err == nil {
		return item, nil
	}
	if err != nil && err != ErrNotFound {
		s.logger.Warn("dispatch cache point read failed, falling back to persistent store", "order_id", orderID, "driver_id", driverID, "error", err)
	}

	item, persistentErr := s.persistent.GetPendingByOrderAndDriver(ctx, orderID, driverID)
	if persistentErr != nil {
		return model.DispatchRecord{}, persistentErr
	}
	return item, nil
}

func (s *MultiStore) MarkAccepted(ctx context.Context, orderID, driverID string, acceptedAt time.Time) error {
	if err := s.persistent.MarkAccepted(ctx, orderID, driverID, acceptedAt); err != nil {
		return fmt.Errorf("persistent mark dispatch accepted: %w", err)
	}
	if err := s.cache.MarkAccepted(ctx, orderID, driverID, acceptedAt); err != nil && err != ErrNotFound {
		return fmt.Errorf("cache mark dispatch accepted: %w", err)
	}
	return nil
}

func (s *MultiStore) UpdatePendingStatusByOrderID(ctx context.Context, orderID, status string, updatedAt time.Time) error {
	if err := s.persistent.UpdatePendingStatusByOrderID(ctx, orderID, status, updatedAt); err != nil {
		return fmt.Errorf("persistent update dispatch status by order: %w", err)
	}
	if err := s.cache.UpdatePendingStatusByOrderID(ctx, orderID, status, updatedAt); err != nil {
		return fmt.Errorf("cache update dispatch status by order: %w", err)
	}
	return nil
}

func (s *MultiStore) UpdatePendingStatusByDriverID(ctx context.Context, driverID, status string, updatedAt time.Time) error {
	if err := s.persistent.UpdatePendingStatusByDriverID(ctx, driverID, status, updatedAt); err != nil {
		return fmt.Errorf("persistent update dispatch status by driver: %w", err)
	}
	if err := s.cache.UpdatePendingStatusByDriverID(ctx, driverID, status, updatedAt); err != nil {
		return fmt.Errorf("cache update dispatch status by driver: %w", err)
	}
	return nil
}

func (s *MultiStore) Close() error {
	if err := s.cache.Close(); err != nil {
		return err
	}
	return s.persistent.Close()
}
