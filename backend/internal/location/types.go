package location

import (
	"context"

	"uber-test/backend/internal/model"
)

type Store interface {
	Upsert(ctx context.Context, location model.DriverLocation) error
	UpsertBatch(ctx context.Context, locations []model.DriverLocation) error
	TouchHeartbeat(ctx context.Context, heartbeat model.DriverHeartbeat) error
	ListLatest(ctx context.Context) ([]model.DriverLocation, error)
	ListRecent(ctx context.Context, driverID string) ([]model.DriverLocation, error)
	Close() error
}
