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
	defaultDispatchRadiusM = 10000
	defaultDispatchLimit   = 5
	defaultDispatchRound   = 1
)

type NearbyFinder interface {
	FindNearby(ctx context.Context, query model.NearbyQuery) ([]model.NearbyDriver, error)
}

type OrderReader interface {
	GetByID(ctx context.Context, id string) (model.Order, error)
	List(ctx context.Context) ([]model.Order, error)
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
	return s.dispatchOrderWithRound(ctx, order, defaultDispatchRound)
}

func (s *Service) dispatchOrderWithRound(ctx context.Context, order model.Order, dispatchRound int) error {
	if s == nil || s.store == nil || s.nearby == nil {
		return nil
	}
	if order.ID == "" {
		return fmt.Errorf("dispatch order: missing order id")
	}
	if dispatchRound <= 0 {
		dispatchRound = defaultDispatchRound
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
			DispatchRound: dispatchRound,
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

func (s *Service) ClosePendingByDriverID(ctx context.Context, driverID, status string) error {
	if s == nil || s.store == nil {
		return nil
	}
	if driverID == "" {
		return fmt.Errorf("close pending dispatches: missing driver_id")
	}
	if status == "" {
		status = model.DispatchStatusExpired
	}

	records, err := s.store.ListPendingByDriverID(ctx, driverID)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}

	now := time.Now().UTC()
	if err := s.store.UpdatePendingStatusByDriverID(ctx, driverID, status, now); err != nil {
		return err
	}

	orderIDs := make([]string, 0, len(records))
	for _, record := range records {
		orderIDs = append(orderIDs, record.OrderID)
	}

	s.broadcast(map[string]any{
		"type":      "dispatch.driver.closed",
		"driver_id": driverID,
		"status":    status,
		"count":     len(records),
		"order_ids": orderIDs,
		"at":        now,
	})
	return nil
}

func (s *Service) HandleDriverExpired(ctx context.Context, driverID string) error {
	if s == nil || s.store == nil {
		return nil
	}
	if driverID == "" {
		return fmt.Errorf("handle expired driver: missing driver_id")
	}

	records, err := s.store.ListPendingByDriverID(ctx, driverID)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}

	orderIDs := make(map[string]struct{}, len(records))
	for _, record := range records {
		orderIDs[record.OrderID] = struct{}{}
	}

	if err := s.ClosePendingByDriverID(ctx, driverID, model.DispatchStatusExpired); err != nil {
		return err
	}

	for orderID := range orderIDs {
		pending, err := s.store.ListPendingByOrderID(ctx, orderID)
		if err != nil {
			return err
		}
		if len(pending) > 0 {
			continue
		}
		if s.orders == nil {
			continue
		}

		order, err := s.orders.GetByID(ctx, orderID)
		if err != nil {
			s.logger.Warn("skip redispatch for missing order after driver expiration", "order_id", orderID, "driver_id", driverID, "error", err)
			continue
		}
		if order.Status != model.OrderStatusPendingDispatch {
			continue
		}
		nextRound, err := s.nextDispatchRound(ctx, orderID)
		if err != nil {
			return err
		}
		if err := s.dispatchOrderWithRound(ctx, order, nextRound); err != nil {
			return err
		}
		s.logger.Info("redispatched order after driver expiration", "order_id", orderID, "driver_id", driverID)
	}

	return nil
}

func (s *Service) RetryTimedOutOrders(ctx context.Context, pendingTimeout time.Duration, maxRounds int) error {
	if s == nil || s.store == nil || s.orders == nil {
		return nil
	}
	if pendingTimeout <= 0 {
		return nil
	}
	if maxRounds <= 0 {
		maxRounds = 1
	}

	seenOrders := make(map[string]struct{})
	allOrders, err := s.orders.List(ctx)
	if err != nil {
		return err
	}

	cutoff := time.Now().UTC().Add(-pendingTimeout)
	for _, order := range allOrders {
		if order.Status != model.OrderStatusPendingDispatch {
			continue
		}
		if _, seen := seenOrders[order.ID]; seen {
			continue
		}

		pending, err := s.store.ListPendingByOrderID(ctx, order.ID)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			continue
		}

		latestPendingAt := latestPendingUpdatedAt(pending)
		if latestPendingAt.After(cutoff) {
			continue
		}

		nextRound, err := s.nextDispatchRound(ctx, order.ID)
		if err != nil {
			return err
		}
		if nextRound > maxRounds {
			continue
		}

		if err := s.ClosePendingByOrderID(ctx, order.ID, model.DispatchStatusExpired); err != nil {
			return err
		}
		if err := s.dispatchOrderWithRound(ctx, order, nextRound); err != nil {
			return err
		}
		seenOrders[order.ID] = struct{}{}
		s.logger.Info("redispatched timed-out order", "order_id", order.ID, "next_round", nextRound)
	}

	return nil
}

func (s *Service) nextDispatchRound(ctx context.Context, orderID string) (int, error) {
	records, err := s.store.ListByOrderID(ctx, orderID)
	if err != nil {
		return 0, err
	}

	maxRound := 0
	for _, record := range records {
		if record.DispatchRound > maxRound {
			maxRound = record.DispatchRound
		}
	}
	if maxRound == 0 {
		return defaultDispatchRound, nil
	}
	return maxRound + 1, nil
}

func latestPendingUpdatedAt(records []model.DispatchRecord) time.Time {
	var latest time.Time
	for _, record := range records {
		if record.UpdatedAt.After(latest) {
			latest = record.UpdatedAt
		}
	}
	return latest
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
