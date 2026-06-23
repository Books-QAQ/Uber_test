package location

import (
	"context"
	"time"

	"uber-test/backend/internal/model"
)

type Store interface {
	Upsert(ctx context.Context, location model.DriverLocation) error
	UpsertBatch(ctx context.Context, locations []model.DriverLocation) error
	TouchHeartbeat(ctx context.Context, heartbeat model.DriverHeartbeat) error
	SetDriverStatus(ctx context.Context, status model.DriverStatus) error
	ListLatest(ctx context.Context) ([]model.DriverLocation, error)
	ListRecent(ctx context.Context, driverID string) ([]model.DriverLocation, error)
	FindNearby(ctx context.Context, query model.NearbyQuery) ([]model.NearbyDriver, error)
	ExpireInactive(ctx context.Context, cutoff time.Time) ([]model.DriverStatus, error)
	Close() error
}
