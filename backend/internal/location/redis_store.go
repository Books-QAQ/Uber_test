package location

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
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
	maxRecent int
	keyPrefix string
	ttl       time.Duration
}

func NewRedisStore(ctx context.Context, cfg RedisConfig, maxRecent int) (*RedisStore, error) {
	if maxRecent <= 0 {
		maxRecent = 20
	}

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
		maxRecent: maxRecent,
		keyPrefix: strings.TrimSuffix(cfg.KeyPrefix, ":"),
		ttl:       cfg.TTL,
	}, nil
}

func (s *RedisStore) Upsert(ctx context.Context, location model.DriverLocation) error {
	return s.UpsertBatch(ctx, []model.DriverLocation{location})
}

func (s *RedisStore) UpsertBatch(ctx context.Context, locations []model.DriverLocation) error {
	if len(locations) == 0 {
		return nil
	}

	pipe := s.client.Pipeline()
	for _, location := range locations {
		payload, err := json.Marshal(location)
		if err != nil {
			return fmt.Errorf("marshal location: %w", err)
		}

		driverSetKey := s.driverSetKey()
		latestKey := s.latestKey(location.DriverID)
		recentKey := s.recentKey(location.DriverID)
		statusKey := s.statusKey(location.DriverID)
		activityKey := s.activityKey(location.DriverID)

		pipe.SAdd(ctx, driverSetKey, location.DriverID)
		pipe.Set(ctx, latestKey, payload, s.ttl)
		pipe.LPush(ctx, recentKey, payload)
		pipe.LTrim(ctx, recentKey, 0, int64(s.maxRecent-1))
		pipe.Set(ctx, activityKey, strconv.FormatInt(location.Timestamp.UnixMilli(), 10), s.ttl)

		statusPayload, err := s.client.Get(ctx, statusKey).Result()
		statusAvailable := err == nil && strings.Contains(statusPayload, `"status":"online"`)
		if statusAvailable {
			pipe.GeoAdd(ctx, s.onlineGeoKey(), &redis.GeoLocation{
				Name:      location.DriverID,
				Longitude: location.Lng,
				Latitude:  location.Lat,
			})
		}

		if s.ttl > 0 {
			pipe.Expire(ctx, recentKey, s.ttl)
			pipe.Expire(ctx, driverSetKey, s.ttl)
			pipe.Expire(ctx, statusKey, s.ttl)
			pipe.Expire(ctx, activityKey, s.ttl)
			pipe.Expire(ctx, s.onlineGeoKey(), s.ttl)
		}
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("write redis locations: %w", err)
	}

	return nil
}

func (s *RedisStore) TouchHeartbeat(ctx context.Context, heartbeat model.DriverHeartbeat) error {
	payload, err := json.Marshal(heartbeat)
	if err != nil {
		return fmt.Errorf("marshal heartbeat: %w", err)
	}

	key := s.heartbeatKey(heartbeat.DriverID)
	activityKey := s.activityKey(heartbeat.DriverID)
	activityValue := strconv.FormatInt(heartbeat.Timestamp.UnixMilli(), 10)
	if s.ttl > 0 {
		pipe := s.client.Pipeline()
		pipe.Set(ctx, key, payload, s.ttl)
		pipe.Set(ctx, activityKey, activityValue, s.ttl)
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("set heartbeat: %w", err)
		}
		return nil
	}

	pipe := s.client.Pipeline()
	pipe.Set(ctx, key, payload, 0)
	pipe.Set(ctx, activityKey, activityValue, 0)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("set heartbeat: %w", err)
	}
	return nil
}

func (s *RedisStore) SetDriverStatus(ctx context.Context, status model.DriverStatus) error {
	payload, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}

	key := s.statusKey(status.DriverID)
	if err := s.client.Set(ctx, key, payload, s.ttl).Err(); err != nil {
		return fmt.Errorf("set driver status: %w", err)
	}

	if model.IsDriverStatusAvailableForNearby(status.Status) {
		latestPayload, err := s.client.Get(ctx, s.latestKey(status.DriverID)).Result()
		if err != nil && err != redis.Nil {
			return fmt.Errorf("get latest location for geo index: %w", err)
		}
		if err == nil {
			var location model.DriverLocation
			if err := json.Unmarshal([]byte(latestPayload), &location); err != nil {
				return fmt.Errorf("unmarshal latest location for geo index: %w", err)
			}
			if err := s.client.GeoAdd(ctx, s.onlineGeoKey(), &redis.GeoLocation{
				Name:      status.DriverID,
				Longitude: location.Lng,
				Latitude:  location.Lat,
			}).Err(); err != nil {
				return fmt.Errorf("geo add online driver: %w", err)
			}
		}
		return nil
	}

	if err := s.client.ZRem(ctx, s.onlineGeoKey(), status.DriverID).Err(); err != nil {
		return fmt.Errorf("remove driver from online geo index: %w", err)
	}

	return nil
}

func (s *RedisStore) ListLatest(ctx context.Context) ([]model.DriverLocation, error) {
	driverIDs, err := s.client.SMembers(ctx, s.driverSetKey()).Result()
	if err != nil {
		return nil, fmt.Errorf("load driver ids: %w", err)
	}
	if len(driverIDs) == 0 {
		return nil, nil
	}

	pipe := s.client.Pipeline()
	cmds := make([]*redis.StringCmd, 0, len(driverIDs))
	for _, driverID := range driverIDs {
		cmds = append(cmds, pipe.Get(ctx, s.latestKey(driverID)))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("load latest locations: %w", err)
	}

	items := make([]model.DriverLocation, 0, len(cmds))
	for _, cmd := range cmds {
		payload, err := cmd.Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read latest location: %w", err)
		}

		var location model.DriverLocation
		if err := json.Unmarshal([]byte(payload), &location); err != nil {
			return nil, fmt.Errorf("unmarshal latest location: %w", err)
		}
		items = append(items, location)
	}

	slices.SortFunc(items, func(a, b model.DriverLocation) int {
		if a.Timestamp.Before(b.Timestamp) {
			return 1
		}
		if a.Timestamp.After(b.Timestamp) {
			return -1
		}
		return 0
	})

	return items, nil
}

func (s *RedisStore) GetLatestByDriverID(ctx context.Context, driverID string) (model.DriverLocation, error) {
	payload, err := s.client.Get(ctx, s.latestKey(driverID)).Result()
	if err == redis.Nil {
		return model.DriverLocation{}, ErrNotFound
	}
	if err != nil {
		return model.DriverLocation{}, fmt.Errorf("read latest location by driver: %w", err)
	}

	var location model.DriverLocation
	if err := json.Unmarshal([]byte(payload), &location); err != nil {
		return model.DriverLocation{}, fmt.Errorf("unmarshal latest location by driver: %w", err)
	}
	return location, nil
}

func (s *RedisStore) FindNearby(ctx context.Context, query model.NearbyQuery) ([]model.NearbyDriver, error) {
	if query.RadiusM <= 0 {
		query.RadiusM = 3000
	}
	if query.Limit <= 0 {
		query.Limit = 20
	}

	results, err := s.client.GeoSearchLocation(ctx, s.onlineGeoKey(), &redis.GeoSearchLocationQuery{
		GeoSearchQuery: redis.GeoSearchQuery{
			Longitude:  query.Lng,
			Latitude:   query.Lat,
			Radius:     query.RadiusM,
			RadiusUnit: "m",
			Count:      query.Limit,
			Sort:       "ASC",
		},
		WithCoord: true,
		WithDist:  true,
	}).Result()
	if err != nil && strings.Contains(err.Error(), "unknown command `geosearch`") {
		results, err = s.client.GeoRadius(ctx, s.onlineGeoKey(), query.Lng, query.Lat, &redis.GeoRadiusQuery{
			Radius:    query.RadiusM,
			Unit:      "m",
			WithCoord: true,
			WithDist:  true,
			Count:     query.Limit,
			Sort:      "ASC",
		}).Result()
	}
	if err != nil {
		return nil, fmt.Errorf("geo search nearby drivers: %w", err)
	}
	if len(results) == 0 {
		return nil, nil
	}

	items := make([]model.NearbyDriver, 0, len(results))
	for _, result := range results {
		locationPayload, err := s.client.Get(ctx, s.latestKey(result.Name)).Result()
		if err != nil {
			if err == redis.Nil {
				continue
			}
			return nil, fmt.Errorf("get latest location for nearby driver: %w", err)
		}

		statusPayload, err := s.client.Get(ctx, s.statusKey(result.Name)).Result()
		if err != nil && err != redis.Nil {
			return nil, fmt.Errorf("get status for nearby driver: %w", err)
		}

		var location model.DriverLocation
		if err := json.Unmarshal([]byte(locationPayload), &location); err != nil {
			return nil, fmt.Errorf("unmarshal nearby location: %w", err)
		}

		status := model.DriverStatus{DriverID: result.Name, Status: model.DriverStatusOffline}
		if err == nil {
			if unmarshalErr := json.Unmarshal([]byte(statusPayload), &status); unmarshalErr != nil {
				return nil, fmt.Errorf("unmarshal nearby status: %w", unmarshalErr)
			}
		}

		if query.OnlyLive && !model.IsDriverStatusAvailableForNearby(status.Status) {
			continue
		}

		items = append(items, model.NearbyDriver{
			DriverID:  result.Name,
			Status:    status.Status,
			DistanceM: result.Dist,
			Location:  location,
			UpdatedAt: location.Timestamp,
		})
	}

	return items, nil
}

func (s *RedisStore) ListRecent(ctx context.Context, driverID string) ([]model.DriverLocation, error) {
	values, err := s.client.LRange(ctx, s.recentKey(driverID), 0, int64(s.maxRecent-1)).Result()
	if err != nil {
		return nil, fmt.Errorf("load recent locations: %w", err)
	}

	items := make([]model.DriverLocation, 0, len(values))
	for _, value := range values {
		var location model.DriverLocation
		if err := json.Unmarshal([]byte(value), &location); err != nil {
			return nil, fmt.Errorf("unmarshal recent location: %w", err)
		}
		items = append(items, location)
	}

	slices.Reverse(items)
	return items, nil
}

func (s *RedisStore) ExpireInactive(ctx context.Context, cutoff time.Time) ([]model.DriverStatus, error) {
	driverIDs, err := s.client.SMembers(ctx, s.driverSetKey()).Result()
	if err != nil {
		return nil, fmt.Errorf("load driver ids for expiration: %w", err)
	}
	if len(driverIDs) == 0 {
		return nil, nil
	}

	pipe := s.client.Pipeline()
	activityCmds := make(map[string]*redis.StringCmd, len(driverIDs))
	statusCmds := make(map[string]*redis.StringCmd, len(driverIDs))
	for _, driverID := range driverIDs {
		activityCmds[driverID] = pipe.Get(ctx, s.activityKey(driverID))
		statusCmds[driverID] = pipe.Get(ctx, s.statusKey(driverID))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("load driver activity for expiration: %w", err)
	}

	expired := make([]model.DriverStatus, 0)
	for _, driverID := range driverIDs {
		statusPayload, err := statusCmds[driverID].Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read driver status for expiration: %w", err)
		}

		var status model.DriverStatus
		if err := json.Unmarshal([]byte(statusPayload), &status); err != nil {
			return nil, fmt.Errorf("unmarshal driver status for expiration: %w", err)
		}
		if status.Status == model.DriverStatusOffline {
			continue
		}

		activityPayload, err := activityCmds[driverID].Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read driver activity for expiration: %w", err)
		}

		unixMS, err := strconv.ParseInt(activityPayload, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse driver activity for expiration: %w", err)
		}
		if time.UnixMilli(unixMS).UTC().After(cutoff) {
			continue
		}

		status.Status = model.DriverStatusOffline
		status.UpdatedAt = cutoff
		if err := s.SetDriverStatus(ctx, status); err != nil {
			return nil, err
		}
		expired = append(expired, status)
	}

	return expired, nil
}

func (s *RedisStore) Close() error {
	return s.client.Close()
}

func (s *RedisStore) driverSetKey() string {
	return s.key("driver:location:drivers")
}

func (s *RedisStore) latestKey(driverID string) string {
	return s.key("driver:location:latest:" + driverID)
}

func (s *RedisStore) recentKey(driverID string) string {
	return s.key("driver:location:recent:" + driverID)
}

func (s *RedisStore) heartbeatKey(driverID string) string {
	return s.key("driver:heartbeat:" + driverID)
}

func (s *RedisStore) activityKey(driverID string) string {
	return s.key("driver:activity:" + driverID)
}

func (s *RedisStore) statusKey(driverID string) string {
	return s.key("driver:status:" + driverID)
}

func (s *RedisStore) onlineGeoKey() string {
	return s.key("driver:geo:online")
}

func (s *RedisStore) key(suffix string) string {
	if s.keyPrefix == "" {
		return suffix
	}
	return s.keyPrefix + ":" + suffix
}
