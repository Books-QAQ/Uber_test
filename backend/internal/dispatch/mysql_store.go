package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"uber-test/backend/internal/model"
)

type MySQLStore struct {
	db *sql.DB
}

func NewMySQLStore(db *sql.DB) *MySQLStore {
	return &MySQLStore{db: db}
}

func (s *MySQLStore) CreateBatch(ctx context.Context, records []model.DispatchRecord) error {
	for _, record := range records {
		if record.ID == "" || record.OrderID == "" || record.DriverID == "" {
			return fmt.Errorf("create dispatch batch: incomplete dispatch record")
		}

		pending, err := s.GetPendingByOrderAndDriver(ctx, record.OrderID, record.DriverID)
		if err == nil && pending.ID != "" {
			continue
		}
		if err != nil && err != ErrNotFound {
			return err
		}

		_, err = s.db.ExecContext(ctx, `
			INSERT INTO dispatches (
				id, order_id, driver_id, status, distance_m, dispatch_round, created_at, updated_at, responded_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, record.ID, record.OrderID, record.DriverID, record.Status, record.DistanceM, record.DispatchRound, record.CreatedAt, record.UpdatedAt, nullableDispatchTime(record.RespondedAt))
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *MySQLStore) ListPendingByDriverID(ctx context.Context, driverID string) ([]model.DispatchRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, order_id, driver_id, status, distance_m, dispatch_round, created_at, updated_at, responded_at
		FROM dispatches
		WHERE driver_id = ? AND status = ?
		ORDER BY distance_m ASC, created_at DESC
	`, driverID, model.DispatchStatusPending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]model.DispatchRecord, 0)
	for rows.Next() {
		record, scanErr := scanDispatchRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sortPendingDispatches(items)
	return items, nil
}

func (s *MySQLStore) ListPendingByOrderID(ctx context.Context, orderID string) ([]model.DispatchRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, order_id, driver_id, status, distance_m, dispatch_round, created_at, updated_at, responded_at
		FROM dispatches
		WHERE order_id = ? AND status = ?
		ORDER BY distance_m ASC, created_at DESC
	`, orderID, model.DispatchStatusPending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]model.DispatchRecord, 0)
	for rows.Next() {
		record, scanErr := scanDispatchRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sortPendingDispatches(items)
	return items, nil
}

func (s *MySQLStore) ListByOrderID(ctx context.Context, orderID string) ([]model.DispatchRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, order_id, driver_id, status, distance_m, dispatch_round, created_at, updated_at, responded_at
		FROM dispatches
		WHERE order_id = ?
		ORDER BY dispatch_round DESC, created_at DESC
	`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]model.DispatchRecord, 0)
	for rows.Next() {
		record, scanErr := scanDispatchRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *MySQLStore) GetPendingByOrderAndDriver(ctx context.Context, orderID, driverID string) (model.DispatchRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, order_id, driver_id, status, distance_m, dispatch_round, created_at, updated_at, responded_at
		FROM dispatches
		WHERE order_id = ? AND driver_id = ? AND status = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, orderID, driverID, model.DispatchStatusPending)

	record, err := scanDispatchRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.DispatchRecord{}, ErrNotFound
		}
		return model.DispatchRecord{}, err
	}
	return record, nil
}

func (s *MySQLStore) MarkAccepted(ctx context.Context, orderID, driverID string, acceptedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE dispatches
		SET status = ?,
		    updated_at = ?,
		    responded_at = ?
		WHERE order_id = ? AND driver_id = ? AND status = ?
	`, model.DispatchStatusAccepted, acceptedAt, acceptedAt, orderID, driverID, model.DispatchStatusPending)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE dispatches
		SET status = ?,
		    updated_at = ?,
		    responded_at = ?
		WHERE order_id = ? AND driver_id <> ? AND status = ?
	`, model.DispatchStatusExpired, acceptedAt, acceptedAt, orderID, driverID, model.DispatchStatusPending)
	return err
}

func (s *MySQLStore) UpdatePendingStatusByOrderID(ctx context.Context, orderID, status string, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE dispatches
		SET status = ?,
		    updated_at = ?,
		    responded_at = ?
		WHERE order_id = ? AND status = ?
	`, status, updatedAt, updatedAt, orderID, model.DispatchStatusPending)
	return err
}

func (s *MySQLStore) UpdatePendingStatusByDriverID(ctx context.Context, driverID, status string, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE dispatches
		SET status = ?,
		    updated_at = ?,
		    responded_at = ?
		WHERE driver_id = ? AND status = ?
	`, status, updatedAt, updatedAt, driverID, model.DispatchStatusPending)
	return err
}

func (s *MySQLStore) Close() error {
	return nil
}

type dispatchScanner interface {
	Scan(dest ...any) error
}

func scanDispatchRecord(scanner dispatchScanner) (model.DispatchRecord, error) {
	var record model.DispatchRecord
	var respondedAt sql.NullTime
	err := scanner.Scan(
		&record.ID,
		&record.OrderID,
		&record.DriverID,
		&record.Status,
		&record.DistanceM,
		&record.DispatchRound,
		&record.CreatedAt,
		&record.UpdatedAt,
		&respondedAt,
	)
	if respondedAt.Valid {
		record.RespondedAt = respondedAt.Time
	}
	return record, err
}

func nullableDispatchTime(value time.Time) sql.NullTime {
	if value.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: value, Valid: true}
}
