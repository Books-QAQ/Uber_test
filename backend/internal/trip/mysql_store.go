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
