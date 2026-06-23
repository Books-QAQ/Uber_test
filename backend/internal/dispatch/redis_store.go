package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"uber-test/backend/internal/model"
)

type RedisConfig struct {
	Addr      string
	Password  string
	DB        int
	KeyPrefix string
	TTL       time.Duration
}

type RedisStore struct {
	client    *redis.Client
	keyPrefix string
	ttl       time.Duration
}

func NewRedisStore(ctx context.Context, cfg RedisConfig) (*RedisStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &RedisStore{
		client:    client,
		keyPrefix: strings.TrimSuffix(cfg.KeyPrefix, ":"),
		ttl:       cfg.TTL,
	}, nil
}

func (s *RedisStore) CreateBatch(ctx context.Context, records []model.DispatchRecord) error {
	if len(records) == 0 {
		return nil
	}

	pipe := s.client.Pipeline()
	for _, record := range records {
		if record.ID == "" || record.OrderID == "" || record.DriverID == "" {
			return fmt.Errorf("create dispatch batch: incomplete dispatch record")
		}

		exists, err := s.client.Exists(ctx, s.pendingOrderDriverKey(record.OrderID, record.DriverID)).Result()
		if err != nil {
			return fmt.Errorf("check pending dispatch index: %w", err)
		}
		if exists > 0 {
			continue
		}

		payload, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("marshal dispatch record: %w", err)
		}

		pipe.Set(ctx, s.recordKey(record.ID), payload, s.ttl)
		pipe.SAdd(ctx, s.pendingOrderKey(record.OrderID), record.ID)
		pipe.SAdd(ctx, s.pendingDriverKey(record.DriverID), record.ID)
		pipe.Set(ctx, s.pendingOrderDriverKey(record.OrderID, record.DriverID), record.ID, s.ttl)
		if s.ttl > 0 {
			pipe.Expire(ctx, s.pendingOrderKey(record.OrderID), s.ttl)
			pipe.Expire(ctx, s.pendingDriverKey(record.DriverID), s.ttl)
		}
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("write redis dispatch batch: %w", err)
	}
	return nil
}

func (s *RedisStore) ListPendingByDriverID(ctx context.Context, driverID string) ([]model.DispatchRecord, error) {
	recordIDs, err := s.client.SMembers(ctx, s.pendingDriverKey(driverID)).Result()
	if err != nil {
		return nil, fmt.Errorf("load pending dispatch ids by driver: %w", err)
	}
	if len(recordIDs) == 0 {
		return nil, nil
	}

	items, err := s.loadRecords(ctx, recordIDs)
	if err != nil {
		return nil, err
	}

	pending := make([]model.DispatchRecord, 0, len(items))
	for _, item := range items {
		if item.Status == model.DispatchStatusPending {
			pending = append(pending, item)
		}
	}
	sortPendingDispatches(pending)
	return pending, nil
}

func (s *RedisStore) GetPendingByOrderAndDriver(ctx context.Context, orderID, driverID string) (model.DispatchRecord, error) {
	recordID, err := s.client.Get(ctx, s.pendingOrderDriverKey(orderID, driverID)).Result()
	if err == redis.Nil {
		return model.DispatchRecord{}, ErrNotFound
	}
	if err != nil {
		return model.DispatchRecord{}, fmt.Errorf("load pending dispatch index: %w", err)
	}

	payload, err := s.client.Get(ctx, s.recordKey(recordID)).Result()
	if err == redis.Nil {
		return model.DispatchRecord{}, ErrNotFound
	}
	if err != nil {
		return model.DispatchRecord{}, fmt.Errorf("load dispatch record: %w", err)
	}

	var record model.DispatchRecord
	if err := json.Unmarshal([]byte(payload), &record); err != nil {
		return model.DispatchRecord{}, fmt.Errorf("unmarshal dispatch record: %w", err)
	}
	if record.Status != model.DispatchStatusPending {
		return model.DispatchRecord{}, ErrNotFound
	}
	return record, nil
}

func (s *RedisStore) MarkAccepted(ctx context.Context, orderID, driverID string, acceptedAt time.Time) error {
	return s.updatePendingRecordsByOrderID(ctx, orderID, acceptedAt, func(record *model.DispatchRecord) {
		if record.DriverID == driverID {
			record.Status = model.DispatchStatusAccepted
		} else {
			record.Status = model.DispatchStatusExpired
		}
		record.UpdatedAt = acceptedAt
		record.RespondedAt = acceptedAt
	}, true, driverID)
}

func (s *RedisStore) UpdatePendingStatusByOrderID(ctx context.Context, orderID, status string, updatedAt time.Time) error {
	return s.updatePendingRecordsByOrderID(ctx, orderID, updatedAt, func(record *model.DispatchRecord) {
		record.Status = status
		record.UpdatedAt = updatedAt
		record.RespondedAt = updatedAt
	}, false, "")
}

func (s *RedisStore) Close() error {
	return s.client.Close()
}

func (s *RedisStore) updatePendingRecordsByOrderID(ctx context.Context, orderID string, updatedAt time.Time, mutate func(record *model.DispatchRecord), requireMatchedDriver bool, matchedDriverID string) error {
	recordIDs, err := s.client.SMembers(ctx, s.pendingOrderKey(orderID)).Result()
	if err != nil {
		return fmt.Errorf("load pending order dispatch ids: %w", err)
	}
	if len(recordIDs) == 0 {
		if requireMatchedDriver {
			return ErrNotFound
		}
		return nil
	}

	items, err := s.loadRecords(ctx, recordIDs)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		if requireMatchedDriver {
			return ErrNotFound
		}
		return nil
	}

	foundDriver := false
	if requireMatchedDriver {
		for _, item := range items {
			if item.Status == model.DispatchStatusPending && item.DriverID == matchedDriverID {
				foundDriver = true
				break
			}
		}
		if !foundDriver {
			return ErrNotFound
		}
	}

	pipe := s.client.Pipeline()
	for _, item := range items {
		if item.Status != model.DispatchStatusPending {
			continue
		}
		mutate(&item)

		payload, marshalErr := json.Marshal(item)
		if marshalErr != nil {
			return fmt.Errorf("marshal updated dispatch record: %w", marshalErr)
		}

		pipe.Set(ctx, s.recordKey(item.ID), payload, s.ttl)
		pipe.SRem(ctx, s.pendingDriverKey(item.DriverID), item.ID)
		pipe.Del(ctx, s.pendingOrderDriverKey(item.OrderID, item.DriverID))
		if s.ttl > 0 {
			pipe.Expire(ctx, s.pendingDriverKey(item.DriverID), s.ttl)
		}
	}
	pipe.Del(ctx, s.pendingOrderKey(orderID))

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("update redis dispatch records: %w", err)
	}

	return nil
}

func (s *RedisStore) loadRecords(ctx context.Context, recordIDs []string) ([]model.DispatchRecord, error) {
	pipe := s.client.Pipeline()
	cmds := make([]*redis.StringCmd, 0, len(recordIDs))
	for _, recordID := range recordIDs {
		cmds = append(cmds, pipe.Get(ctx, s.recordKey(recordID)))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("load dispatch records: %w", err)
	}

	items := make([]model.DispatchRecord, 0, len(cmds))
	for _, cmd := range cmds {
		payload, err := cmd.Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read dispatch record: %w", err)
		}

		var item model.DispatchRecord
		if err := json.Unmarshal([]byte(payload), &item); err != nil {
			return nil, fmt.Errorf("unmarshal dispatch record: %w", err)
		}
		items = append(items, item)
	}

	return items, nil
}

func (s *RedisStore) recordKey(recordID string) string {
	return s.key("dispatch:record:" + recordID)
}

func (s *RedisStore) pendingOrderKey(orderID string) string {
	return s.key("dispatch:pending:order:" + orderID)
}

func (s *RedisStore) pendingDriverKey(driverID string) string {
	return s.key("dispatch:pending:driver:" + driverID)
}

func (s *RedisStore) pendingOrderDriverKey(orderID, driverID string) string {
	return s.key("dispatch:pending:order-driver:" + orderID + ":" + driverID)
}

func (s *RedisStore) key(suffix string) string {
	if s.keyPrefix == "" {
		return suffix
	}
	return s.keyPrefix + ":" + suffix
}
