package trip

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"uber-test/backend/internal/model"
)

type MySQLStore struct {
	db *sql.DB
}

func NewMySQLStore(db *sql.DB) *MySQLStore {
	return &MySQLStore{db: db}
}

func (s *MySQLStore) Save(ctx context.Context, trip model.Trip) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO trips (
			id, order_id, passenger_id, driver_id, status, started_at, ended_at,
			actual_distance_m, actual_duration_s, waiting_duration_s, estimated_price, final_price, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			driver_id = VALUES(driver_id),
			status = VALUES(status),
			started_at = VALUES(started_at),
			ended_at = VALUES(ended_at),
			actual_distance_m = VALUES(actual_distance_m),
			actual_duration_s = VALUES(actual_duration_s),
			waiting_duration_s = VALUES(waiting_duration_s),
			estimated_price = VALUES(estimated_price),
			final_price = VALUES(final_price),
			updated_at = VALUES(updated_at)
	`, trip.ID, trip.OrderID, trip.PassengerID, trip.DriverID, trip.Status, nullableTime(trip.StartedAt), nullableTime(trip.EndedAt),
		trip.ActualDistanceM, trip.ActualDurationS, trip.WaitingDurationS, trip.EstimatedPrice, trip.FinalPrice, trip.CreatedAt, trip.UpdatedAt)
	return err
}

func (s *MySQLStore) GetByOrderID(ctx context.Context, orderID string) (model.Trip, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, order_id, passenger_id, driver_id, status, started_at, ended_at,
		       actual_distance_m, actual_duration_s, waiting_duration_s, estimated_price, final_price, created_at, updated_at
		FROM trips
		WHERE order_id = ?
	`, orderID)

	trip, err := scanTrip(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Trip{}, ErrNotFound
		}
		return model.Trip{}, err
	}
	return trip, nil
}

func (s *MySQLStore) List(ctx context.Context) ([]model.Trip, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, order_id, passenger_id, driver_id, status, started_at, ended_at,
		       actual_distance_m, actual_duration_s, waiting_duration_s, estimated_price, final_price, created_at, updated_at
		FROM trips
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]model.Trip, 0)
	for rows.Next() {
		trip, err := scanTrip(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, trip)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *MySQLStore) SavePoint(ctx context.Context, point model.TripPoint) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO trip_points (
			id, trip_id, order_id, driver_id, trip_status, lat, lng, speed_kph, heading, accuracy_m, recorded_at, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, point.ID, point.TripID, point.OrderID, point.DriverID, point.TripStatus, point.Lat, point.Lng, point.SpeedKPH, point.Heading, point.AccuracyM, point.RecordedAt, point.CreatedAt)
	return err
}

func (s *MySQLStore) ListPointsByTripID(ctx context.Context, tripID string) ([]model.TripPoint, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, trip_id, order_id, driver_id, trip_status, lat, lng, speed_kph, heading, accuracy_m, recorded_at, created_at
		FROM trip_points
		WHERE trip_id = ?
		ORDER BY recorded_at ASC, created_at ASC
	`, tripID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]model.TripPoint, 0)
	for rows.Next() {
		point, err := scanTripPoint(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, point)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	return items, nil
}

func (s *MySQLStore) GetLastPointByTripID(ctx context.Context, tripID string) (model.TripPoint, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, trip_id, order_id, driver_id, trip_status, lat, lng, speed_kph, heading, accuracy_m, recorded_at, created_at
		FROM trip_points
		WHERE trip_id = ?
		ORDER BY recorded_at DESC, created_at DESC
		LIMIT 1
	`, tripID)

	point, err := scanTripPoint(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.TripPoint{}, ErrNotFound
		}
		return model.TripPoint{}, err
	}
	return point, nil
}

type tripScanner interface {
	Scan(dest ...any) error
}

func scanTrip(scanner tripScanner) (model.Trip, error) {
	var trip model.Trip
	var startedAt sql.NullTime
	var endedAt sql.NullTime
	err := scanner.Scan(
		&trip.ID,
		&trip.OrderID,
		&trip.PassengerID,
		&trip.DriverID,
		&trip.Status,
		&startedAt,
		&endedAt,
		&trip.ActualDistanceM,
		&trip.ActualDurationS,
		&trip.WaitingDurationS,
		&trip.EstimatedPrice,
		&trip.FinalPrice,
		&trip.CreatedAt,
		&trip.UpdatedAt,
	)
	if startedAt.Valid {
		trip.StartedAt = startedAt.Time
	}
	if endedAt.Valid {
		trip.EndedAt = endedAt.Time
	}
	return trip, err
}

func nullableTime(value time.Time) sql.NullTime {
	if value.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: value, Valid: true}
}

type tripPointScanner interface {
	Scan(dest ...any) error
}

func scanTripPoint(scanner tripPointScanner) (model.TripPoint, error) {
	var point model.TripPoint
	err := scanner.Scan(
		&point.ID,
		&point.TripID,
		&point.OrderID,
		&point.DriverID,
		&point.TripStatus,
		&point.Lat,
		&point.Lng,
		&point.SpeedKPH,
		&point.Heading,
		&point.AccuracyM,
		&point.RecordedAt,
		&point.CreatedAt,
	)
	return point, err
}
