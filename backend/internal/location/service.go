package location

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"uber-test/backend/internal/model"
)

type Broadcaster interface {
	BroadcastJSON(v any)
}

type Service struct {
	store       *MemoryStore
	broadcaster Broadcaster
	logger      *slog.Logger
}

func NewService(store *MemoryStore, broadcaster Broadcaster, logger *slog.Logger) *Service {
	return &Service{
		store:       store,
		broadcaster: broadcaster,
		logger:      logger,
	}
}

func (s *Service) HandlePacket(ctx context.Context, remoteAddr netip.AddrPort, payload []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	update, err := decodeLocationPacket(payload)
	if err != nil {
		return err
	}

	if update.Timestamp.IsZero() {
		update.Timestamp = time.Now().UTC()
	}
	update.SourceAddr = remoteAddr.String()

	s.store.Upsert(update)
	s.broadcaster.BroadcastJSON(map[string]any{
		"type": "driver.location.updated",
		"data": update,
	})

	s.logger.Debug("processed location packet", "driver_id", update.DriverID, "remote_addr", update.SourceAddr)
	return nil
}

func (s *Service) ListLatest() []model.DriverLocation {
	return s.store.ListLatest()
}

// decodeLocationPacket uses JSON temporarily so the scaffold is runnable
// before the Protobuf schema and generated code are added.
func decodeLocationPacket(payload []byte) (model.DriverLocation, error) {
	var update model.DriverLocation
	if err := json.Unmarshal(payload, &update); err != nil {
		return model.DriverLocation{}, fmt.Errorf("decode location packet: %w", err)
	}
	return update, nil
}
