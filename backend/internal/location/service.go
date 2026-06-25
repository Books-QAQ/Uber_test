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

type TripRecorder interface {
	RecordLocation(ctx context.Context, location model.DriverLocation) error
}

type LocationMatcher interface {
	Sync(ctx context.Context, raw model.DriverLocation) (model.DriverLocation, error)
	GetLatest(driverID string) (model.DriverLocation, bool)
}

type Service struct {
	store        Store
	routeSync    RouteCoordinator
	tripRecorder TripRecorder
	matcher      LocationMatcher
	broadcaster  Broadcaster
	logger       *slog.Logger
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

func (s *Service) SetTripRecorder(tripRecorder TripRecorder) {
	s.tripRecorder = tripRecorder
}

func (s *Service) SetLocationMatcher(matcher LocationMatcher) {
	s.matcher = matcher
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
		return s.processLocations(ctx, message.Locations, remoteAddr.String())
	case message.Heartbeat != nil:
		return s.processHeartbeat(ctx, *message.Heartbeat, remoteAddr.String())
	default:
		return fmt.Errorf("decoded location packet is empty")
	}
}

func (s *Service) UpsertDriverLocation(ctx context.Context, location model.DriverLocation) error {
	if location.DriverID == "" {
		return fmt.Errorf("upsert driver location: missing driver_id")
	}
	return s.processLocations(ctx, []model.DriverLocation{location}, "http-debug")
}

func (s *Service) TouchHeartbeat(ctx context.Context, heartbeat model.DriverHeartbeat) error {
	if heartbeat.DriverID == "" {
		return fmt.Errorf("touch driver heartbeat: missing driver_id")
	}
	return s.processHeartbeat(ctx, heartbeat, "http-debug")
}

func (s *Service) ListLatest(ctx context.Context) ([]model.DriverLocation, error) {
	items, err := s.store.ListLatest(ctx)
	if err != nil {
		return nil, err
	}
	if s.matcher == nil {
		return items, nil
	}

	visible := make([]model.DriverLocation, 0, len(items))
	for _, item := range items {
		visible = append(visible, s.overlayMatched(item))
	}
	return visible, nil
}

func (s *Service) GetLatestByDriverID(ctx context.Context, driverID string) (model.DriverLocation, error) {
	if driverID == "" {
		return model.DriverLocation{}, fmt.Errorf("get latest driver location: missing driver_id")
	}
	item, err := s.store.GetLatestByDriverID(ctx, driverID)
	if err != nil {
		return model.DriverLocation{}, err
	}
	return s.overlayMatched(item), nil
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

	items, err := s.store.FindNearby(ctx, query)
	if err != nil {
		return nil, err
	}
	if s.matcher == nil {
		return items, nil
	}

	visible := make([]model.NearbyDriver, 0, len(items))
	for _, item := range items {
		item.Location = s.overlayMatched(item.Location)
		visible = append(visible, item)
	}
	return visible, nil
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

func (s *Service) visibleLocation(ctx context.Context, raw model.DriverLocation) model.DriverLocation {
	if s.matcher == nil {
		return raw
	}

	matched, err := s.matcher.Sync(ctx, raw)
	if err != nil {
		s.logger.Warn("map match failed, falling back to raw location", "driver_id", raw.DriverID, "order_id", raw.OrderID, "error", err)
		return raw
	}
	return matched
}

func (s *Service) processLocations(ctx context.Context, locations []model.DriverLocation, sourceAddr string) error {
	for i := range locations {
		if locations[i].Timestamp.IsZero() {
			locations[i].Timestamp = time.Now().UTC()
		}
		if locations[i].SourceAddr == "" {
			locations[i].SourceAddr = sourceAddr
		}
	}

	if len(locations) == 1 {
		if err := s.store.Upsert(ctx, locations[0]); err != nil {
			return err
		}
		broadcastLocation := s.visibleLocation(ctx, locations[0])
		if s.routeSync != nil {
			if err := s.routeSync.SyncDriverLocation(ctx, broadcastLocation); err != nil {
				s.logger.Warn("sync route for driver location failed", "driver_id", broadcastLocation.DriverID, "error", err)
			}
		}
		if s.tripRecorder != nil {
			if err := s.tripRecorder.RecordLocation(ctx, broadcastLocation); err != nil {
				s.logger.Warn("record trip point failed", "driver_id", broadcastLocation.DriverID, "order_id", broadcastLocation.OrderID, "error", err)
			}
		}
		s.broadcaster.BroadcastJSON(map[string]any{
			"type": "driver.location.updated",
			"data": broadcastLocation,
		})
		s.logger.Debug("processed location update", "driver_id", broadcastLocation.DriverID, "source_addr", locations[0].SourceAddr)
		return nil
	}

	if err := s.store.UpsertBatch(ctx, locations); err != nil {
		return err
	}
	visibleBatch := make([]model.DriverLocation, 0, len(locations))
	for _, location := range locations {
		visibleBatch = append(visibleBatch, s.visibleLocation(ctx, location))
	}
	if s.routeSync != nil {
		for _, location := range visibleBatch {
			if err := s.routeSync.SyncDriverLocation(ctx, location); err != nil {
				s.logger.Warn("sync route for batch driver location failed", "driver_id", location.DriverID, "error", err)
			}
		}
	}
	if s.tripRecorder != nil {
		for _, location := range visibleBatch {
			if err := s.tripRecorder.RecordLocation(ctx, location); err != nil {
				s.logger.Warn("record batch trip point failed", "driver_id", location.DriverID, "order_id", location.OrderID, "error", err)
			}
		}
	}
	s.broadcaster.BroadcastJSON(map[string]any{
		"type":  "driver.location.batch.updated",
		"count": len(locations),
		"data":  visibleBatch,
	})
	s.logger.Debug("processed location batch update", "count", len(locations), "source_addr", sourceAddr)
	return nil
}

func (s *Service) processHeartbeat(ctx context.Context, heartbeat model.DriverHeartbeat, sourceAddr string) error {
	if heartbeat.Timestamp.IsZero() {
		heartbeat.Timestamp = time.Now().UTC()
	}
	if heartbeat.SourceAddr == "" {
		heartbeat.SourceAddr = sourceAddr
	}

	if err := s.store.TouchHeartbeat(ctx, heartbeat); err != nil {
		return err
	}
	s.broadcaster.BroadcastJSON(map[string]any{
		"type": "driver.heartbeat.received",
		"data": heartbeat,
	})
	s.logger.Debug("processed heartbeat update", "driver_id", heartbeat.DriverID, "source_addr", heartbeat.SourceAddr)
	return nil
}

func (s *Service) overlayMatched(raw model.DriverLocation) model.DriverLocation {
	if s.matcher == nil {
		return raw
	}
	if matched, ok := s.matcher.GetLatest(raw.DriverID); ok {
		return matched
	}
	return raw
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
