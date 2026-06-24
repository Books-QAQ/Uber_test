package location

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"google.golang.org/protobuf/proto"

	locationpb "uber-test/backend/internal/gen/location/v1"
	"uber-test/backend/internal/model"
)

type Broadcaster interface {
	BroadcastJSON(v any)
}

type RouteCoordinator interface {
	SyncDriverLocation(ctx context.Context, location model.DriverLocation) error
	ClearByDriverID(ctx context.Context, driverID string) error
}

type Service struct {
	store       Store
	routeSync   RouteCoordinator
	broadcaster Broadcaster
	logger      *slog.Logger
}

func NewService(store Store, broadcaster Broadcaster, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:       store,
		broadcaster: broadcaster,
		logger:      logger,
	}
}

func (s *Service) SetRouteCoordinator(routeSync RouteCoordinator) {
	s.routeSync = routeSync
}

func (s *Service) HandlePacket(ctx context.Context, remoteAddr netip.AddrPort, payload []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	message, err := decodeLocationPacket(payload)
	if err != nil {
		return err
	}

	switch {
	case len(message.Locations) > 0:
		for i := range message.Locations {
			if message.Locations[i].Timestamp.IsZero() {
				message.Locations[i].Timestamp = time.Now().UTC()
			}
			message.Locations[i].SourceAddr = remoteAddr.String()
		}

		if len(message.Locations) == 1 {
			if err := s.store.Upsert(ctx, message.Locations[0]); err != nil {
				return err
			}
			if s.routeSync != nil {
				if err := s.routeSync.SyncDriverLocation(ctx, message.Locations[0]); err != nil {
					s.logger.Warn("sync route for driver location failed", "driver_id", message.Locations[0].DriverID, "error", err)
				}
			}
			s.broadcaster.BroadcastJSON(map[string]any{
				"type": "driver.location.updated",
				"data": message.Locations[0],
			})
			s.logger.Debug("processed location update packet", "driver_id", message.Locations[0].DriverID, "remote_addr", remoteAddr.String())
			return nil
		}

		if err := s.store.UpsertBatch(ctx, message.Locations); err != nil {
			return err
		}
		if s.routeSync != nil {
			for _, location := range message.Locations {
				if err := s.routeSync.SyncDriverLocation(ctx, location); err != nil {
					s.logger.Warn("sync route for batch driver location failed", "driver_id", location.DriverID, "error", err)
				}
			}
		}
		s.broadcaster.BroadcastJSON(map[string]any{
			"type":  "driver.location.batch.updated",
			"count": len(message.Locations),
			"data":  message.Locations,
		})
		s.logger.Debug("processed location batch packet", "count", len(message.Locations), "remote_addr", remoteAddr.String())
		return nil
	case message.Heartbeat != nil:
		if message.Heartbeat.Timestamp.IsZero() {
			message.Heartbeat.Timestamp = time.Now().UTC()
		}
		message.Heartbeat.SourceAddr = remoteAddr.String()

		if err := s.store.TouchHeartbeat(ctx, *message.Heartbeat); err != nil {
			return err
		}
		s.broadcaster.BroadcastJSON(map[string]any{
			"type": "driver.heartbeat.received",
			"data": message.Heartbeat,
		})
		s.logger.Debug("processed heartbeat packet", "driver_id", message.Heartbeat.DriverID, "remote_addr", remoteAddr.String())
		return nil
	default:
		return fmt.Errorf("decoded location packet is empty")
	}
}

func (s *Service) ListLatest(ctx context.Context) ([]model.DriverLocation, error) {
	return s.store.ListLatest(ctx)
}

func (s *Service) GetLatestByDriverID(ctx context.Context, driverID string) (model.DriverLocation, error) {
	if driverID == "" {
		return model.DriverLocation{}, fmt.Errorf("get latest driver location: missing driver_id")
	}
	return s.store.GetLatestByDriverID(ctx, driverID)
}

func (s *Service) SetDriverStatus(ctx context.Context, status model.DriverStatus) error {
	if status.DriverID == "" {
		return fmt.Errorf("set driver status: missing driver_id")
	}
	if !model.IsDriverStatusAllowed(status.Status) {
		return fmt.Errorf("set driver status: unsupported status %q", status.Status)
	}
	if status.UpdatedAt.IsZero() {
		status.UpdatedAt = time.Now().UTC()
	}

	return s.store.SetDriverStatus(ctx, status)
}

func (s *Service) FindNearby(ctx context.Context, query model.NearbyQuery) ([]model.NearbyDriver, error) {
	if query.Limit <= 0 {
		query.Limit = 20
	}
	if query.RadiusM <= 0 {
		query.RadiusM = 10000
	}
	query.OnlyLive = true

	return s.store.FindNearby(ctx, query)
}

func (s *Service) ExpireInactiveDrivers(ctx context.Context, cutoff time.Time) ([]model.DriverStatus, error) {
	items, err := s.store.ExpireInactive(ctx, cutoff)
	if err != nil {
		return nil, err
	}

	for _, item := range items {
		s.broadcaster.BroadcastJSON(map[string]any{
			"type": "driver.status.expired",
			"data": item,
		})
		s.logger.Info("driver marked offline due to inactivity", "driver_id", item.DriverID, "updated_at", item.UpdatedAt)
	}

	return items, nil
}

func (s *Service) RunExpirationLoop(ctx context.Context, interval, inactiveTimeout time.Duration) {
	if interval <= 0 || inactiveTimeout <= 0 {
		s.logger.Info("driver expiration loop disabled", "interval", interval, "inactive_timeout", inactiveTimeout)
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.logger.Info("starting driver expiration loop", "interval", interval, "inactive_timeout", inactiveTimeout)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().UTC().Add(-inactiveTimeout)
			if _, err := s.ExpireInactiveDrivers(ctx, cutoff); err != nil {
				s.logger.Error("driver expiration scan failed", "error", err)
			}
		}
	}
}

type decodedIngress struct {
	Locations []model.DriverLocation
	Heartbeat *model.DriverHeartbeat
}

func decodeLocationPacket(payload []byte) (decodedIngress, error) {
	packet := &locationpb.LocationIngressPacket{}
	if err := proto.Unmarshal(payload, packet); err != nil {
		return decodedIngress{}, fmt.Errorf("decode protobuf location packet: %w", err)
	}

	switch payload := packet.Payload.(type) {
	case *locationpb.LocationIngressPacket_LocationUpdate:
		location, err := mapProtoLocation(payload.LocationUpdate)
		if err != nil {
			return decodedIngress{}, err
		}
		return decodedIngress{Locations: []model.DriverLocation{location}}, nil
	case *locationpb.LocationIngressPacket_Heartbeat:
		heartbeat, err := mapProtoHeartbeat(payload.Heartbeat)
		if err != nil {
			return decodedIngress{}, err
		}
		return decodedIngress{Heartbeat: &heartbeat}, nil
	case *locationpb.LocationIngressPacket_LocationBatch:
		locations := make([]model.DriverLocation, 0, len(payload.LocationBatch.GetLocations()))
		for _, item := range payload.LocationBatch.GetLocations() {
			location, err := mapProtoLocation(item)
			if err != nil {
				return decodedIngress{}, err
			}
			locations = append(locations, location)
		}
		return decodedIngress{Locations: locations}, nil
	default:
		return decodedIngress{}, fmt.Errorf("decode protobuf location packet: unsupported payload")
	}
}

func mapProtoLocation(packet *locationpb.DriverLocationUpdate) (model.DriverLocation, error) {
	update := model.DriverLocation{
		DriverID:  packet.GetDriverId(),
		OrderID:   packet.GetOrderId(),
		Lat:       packet.GetLat(),
		Lng:       packet.GetLng(),
		SpeedKPH:  packet.GetSpeedKph(),
		Heading:   packet.GetHeading(),
		AccuracyM: packet.GetAccuracyM(),
	}
	if reportedAt := packet.GetReportedAtUnixMs(); reportedAt > 0 {
		update.Timestamp = time.UnixMilli(reportedAt).UTC()
	}
	if update.DriverID == "" {
		return model.DriverLocation{}, fmt.Errorf("decode protobuf location packet: missing driver_id")
	}
	return update, nil
}

func mapProtoHeartbeat(packet *locationpb.DriverHeartbeat) (model.DriverHeartbeat, error) {
	heartbeat := model.DriverHeartbeat{
		DriverID: packet.GetDriverId(),
		OrderID:  packet.GetOrderId(),
	}
	if reportedAt := packet.GetReportedAtUnixMs(); reportedAt > 0 {
		heartbeat.Timestamp = time.UnixMilli(reportedAt).UTC()
	}
	if heartbeat.DriverID == "" {
		return model.DriverHeartbeat{}, fmt.Errorf("decode protobuf heartbeat packet: missing driver_id")
	}
	return heartbeat, nil
}
