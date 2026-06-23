package dispatch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"uber-test/backend/internal/model"
)

const (
	defaultDispatchRadiusM = 3000
	defaultDispatchLimit   = 5
	defaultDispatchRound   = 1
)

type NearbyFinder interface {
	FindNearby(ctx context.Context, query model.NearbyQuery) ([]model.NearbyDriver, error)
}

type OrderReader interface {
	GetByID(ctx context.Context, id string) (model.Order, error)
}

type Broadcaster interface {
	BroadcastJSON(v any)
}

type Service struct {
	store       Store
	orders      OrderReader
	nearby      NearbyFinder
	broadcaster Broadcaster
	logger      *slog.Logger
}

func NewService(store Store, orders OrderReader, nearby NearbyFinder, broadcaster Broadcaster, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:       store,
		orders:      orders,
		nearby:      nearby,
		broadcaster: broadcaster,
		logger:      logger,
	}
}

func (s *Service) DispatchOrder(ctx context.Context, order model.Order) error {
	if s == nil || s.store == nil || s.nearby == nil {
		return nil
	}
	if order.ID == "" {
		return fmt.Errorf("dispatch order: missing order id")
	}

	drivers, err := s.nearby.FindNearby(ctx, model.NearbyQuery{
		Lat:     order.PickupLat,
		Lng:     order.PickupLng,
		RadiusM: defaultDispatchRadiusM,
		Limit:   defaultDispatchLimit,
	})
	if err != nil {
		return err
	}
	if len(drivers) == 0 {
		s.logger.Info("no nearby drivers available for dispatch", "order_id", order.ID)
		return nil
	}

	now := time.Now().UTC()
	records := make([]model.DispatchRecord, 0, len(drivers))
	for _, driver := range drivers {
		records = append(records, model.DispatchRecord{
			ID:            newDispatchID(),
			OrderID:       order.ID,
			DriverID:      driver.DriverID,
			Status:        model.DispatchStatusPending,
			DistanceM:     driver.DistanceM,
			DispatchRound: defaultDispatchRound,
			CreatedAt:     now,
			UpdatedAt:     now,
		})
	}

	if err := s.store.CreateBatch(ctx, records); err != nil {
		return err
	}

	s.broadcast(map[string]any{
		"type":  "dispatch.created",
		"count": len(records),
		"data":  records,
	})
	s.logger.Info("created dispatch records", "order_id", order.ID, "candidate_count", len(records))
	return nil
}

func (s *Service) ListPendingAssignmentsByDriverID(ctx context.Context, driverID string) ([]model.DispatchAssignment, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	if driverID == "" {
		return nil, fmt.Errorf("list dispatch assignments: missing driver_id")
	}

	records, err := s.store.ListPendingByDriverID(ctx, driverID)
	if err != nil {
		return nil, err
	}

	assignments := make([]model.DispatchAssignment, 0, len(records))
	for _, record := range records {
		order := model.Order{ID: record.OrderID}
		if s.orders != nil {
			found, getErr := s.orders.GetByID(ctx, record.OrderID)
			if getErr != nil {
				s.logger.Warn("dispatch assignment references missing order", "order_id", record.OrderID, "driver_id", driverID, "error", getErr)
				continue
			}
			order = found
		}
		assignments = append(assignments, model.DispatchAssignment{
			Dispatch: record,
			Order:    order,
		})
	}

	return assignments, nil
}

func (s *Service) EnsureDriverCanAccept(ctx context.Context, orderID, driverID string) error {
	if s == nil || s.store == nil {
		return nil
	}
	if orderID == "" || driverID == "" {
		return ErrDriverNotDispatched
	}

	if _, err := s.store.GetPendingByOrderAndDriver(ctx, orderID, driverID); err != nil {
		if err == ErrNotFound {
			return ErrDriverNotDispatched
		}
		return err
	}

	return nil
}

func (s *Service) MarkAccepted(ctx context.Context, orderID, driverID string) error {
	if s == nil || s.store == nil {
		return nil
	}

	now := time.Now().UTC()
	if err := s.store.MarkAccepted(ctx, orderID, driverID, now); err != nil {
		if err == ErrNotFound {
			return ErrDriverNotDispatched
		}
		return err
	}

	s.broadcast(map[string]any{
		"type":      "dispatch.accepted",
		"order_id":  orderID,
		"driver_id": driverID,
		"at":        now,
	})
	return nil
}

func (s *Service) ClosePendingByOrderID(ctx context.Context, orderID, status string) error {
	if s == nil || s.store == nil {
		return nil
	}
	if orderID == "" {
		return fmt.Errorf("close pending dispatches: missing order_id")
	}
	if status == "" {
		status = model.DispatchStatusExpired
	}

	now := time.Now().UTC()
	if err := s.store.UpdatePendingStatusByOrderID(ctx, orderID, status, now); err != nil {
		return err
	}

	s.broadcast(map[string]any{
		"type":     "dispatch.closed",
		"order_id": orderID,
		"status":   status,
		"at":       now,
	})
	return nil
}

func (s *Service) broadcast(payload any) {
	if s.broadcaster != nil {
		s.broadcaster.BroadcastJSON(payload)
	}
}

func newDispatchID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("dispatch-%d", time.Now().UnixNano())
	}
	return "dispatch-" + hex.EncodeToString(buf[:])
}
