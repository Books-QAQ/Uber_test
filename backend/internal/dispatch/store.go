package dispatch

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"uber-test/backend/internal/model"
)

type Store interface {
	CreateBatch(ctx context.Context, records []model.DispatchRecord) error
	ListPendingByDriverID(ctx context.Context, driverID string) ([]model.DispatchRecord, error)
	GetPendingByOrderAndDriver(ctx context.Context, orderID, driverID string) (model.DispatchRecord, error)
	MarkAccepted(ctx context.Context, orderID, driverID string, acceptedAt time.Time) error
	UpdatePendingStatusByOrderID(ctx context.Context, orderID, status string, updatedAt time.Time) error
	Close() error
}

type MemoryStore struct {
	mu      sync.RWMutex
	records map[string]model.DispatchRecord
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		records: make(map[string]model.DispatchRecord),
	}
}

func (s *MemoryStore) CreateBatch(_ context.Context, records []model.DispatchRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, record := range records {
		if record.ID == "" || record.OrderID == "" || record.DriverID == "" {
			return fmt.Errorf("create dispatch batch: incomplete dispatch record")
		}
		if hasPendingRecordLocked(s.records, record.OrderID, record.DriverID) {
			continue
		}
		if _, exists := s.records[record.ID]; exists {
			return fmt.Errorf("dispatch record %s already exists", record.ID)
		}
		s.records[record.ID] = record
	}

	return nil
}

func (s *MemoryStore) ListPendingByDriverID(_ context.Context, driverID string) ([]model.DispatchRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]model.DispatchRecord, 0)
	for _, record := range s.records {
		if record.DriverID == driverID && record.Status == model.DispatchStatusPending {
			items = append(items, record)
		}
	}

	sortPendingDispatches(items)
	return items, nil
}

func (s *MemoryStore) GetPendingByOrderAndDriver(_ context.Context, orderID, driverID string) (model.DispatchRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, record := range s.records {
		if record.OrderID == orderID && record.DriverID == driverID && record.Status == model.DispatchStatusPending {
			return record, nil
		}
	}

	return model.DispatchRecord{}, ErrNotFound
}

func (s *MemoryStore) MarkAccepted(_ context.Context, orderID, driverID string, acceptedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	found := false
	for id, record := range s.records {
		if record.OrderID != orderID || record.Status != model.DispatchStatusPending {
			continue
		}

		record.UpdatedAt = acceptedAt
		record.RespondedAt = acceptedAt
		if record.DriverID == driverID {
			record.Status = model.DispatchStatusAccepted
			found = true
		} else {
			record.Status = model.DispatchStatusExpired
		}
		s.records[id] = record
	}

	if !found {
		return ErrNotFound
	}

	return nil
}

func (s *MemoryStore) UpdatePendingStatusByOrderID(_ context.Context, orderID, status string, updatedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, record := range s.records {
		if record.OrderID != orderID || record.Status != model.DispatchStatusPending {
			continue
		}
		record.Status = status
		record.UpdatedAt = updatedAt
		record.RespondedAt = updatedAt
		s.records[id] = record
	}

	return nil
}

func (s *MemoryStore) Close() error {
	return nil
}

func hasPendingRecordLocked(records map[string]model.DispatchRecord, orderID, driverID string) bool {
	for _, record := range records {
		if record.OrderID == orderID && record.DriverID == driverID && record.Status == model.DispatchStatusPending {
			return true
		}
	}
	return false
}

func sortPendingDispatches(items []model.DispatchRecord) {
	slices.SortFunc(items, func(a, b model.DispatchRecord) int {
		switch {
		case a.DistanceM < b.DistanceM:
			return -1
		case a.DistanceM > b.DistanceM:
			return 1
		case a.CreatedAt.After(b.CreatedAt):
			return -1
		case a.CreatedAt.Before(b.CreatedAt):
			return 1
		default:
			return 0
		}
	})
}
