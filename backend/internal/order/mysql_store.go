package order

import (
	"context"
	"database/sql"
	"errors"

	"uber-test/backend/internal/model"
)

type MySQLStore struct {
	db *sql.DB
}

func NewMySQLStore(db *sql.DB) *MySQLStore {
	return &MySQLStore{db: db}
}

func (s *MySQLStore) Create(ctx context.Context, order model.Order) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO orders (
			id, passenger_id, driver_id, status, pickup_lat, pickup_lng, pickup_address,
			destination_lat, destination_lng, destination_address, estimated_price, final_price, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, order.ID, order.PassengerID, order.DriverID, order.Status, order.PickupLat, order.PickupLng, order.PickupAddress,
		order.DestinationLat, order.DestinationLng, order.DestinationAddress, order.EstimatedPrice, order.FinalPrice, order.CreatedAt, order.UpdatedAt)
	return err
}

func (s *MySQLStore) GetByID(ctx context.Context, id string) (model.Order, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, passenger_id, driver_id, status, pickup_lat, pickup_lng, pickup_address,
		       destination_lat, destination_lng, destination_address, estimated_price, final_price, created_at, updated_at
		FROM orders
		WHERE id = ?
	`, id)
	order, err := scanOrder(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Order{}, ErrNotFound
		}
		return model.Order{}, err
	}
	return order, nil
}

func (s *MySQLStore) List(ctx context.Context) ([]model.Order, error) {
	return s.queryOrders(ctx, `
		SELECT id, passenger_id, driver_id, status, pickup_lat, pickup_lng, pickup_address,
		       destination_lat, destination_lng, destination_address, estimated_price, final_price, created_at, updated_at
		FROM orders
		ORDER BY created_at DESC
	`)
}

func (s *MySQLStore) ListByPassengerID(ctx context.Context, passengerID string) ([]model.Order, error) {
	return s.queryOrders(ctx, `
		SELECT id, passenger_id, driver_id, status, pickup_lat, pickup_lng, pickup_address,
		       destination_lat, destination_lng, destination_address, estimated_price, final_price, created_at, updated_at
		FROM orders
		WHERE passenger_id = ?
		ORDER BY created_at DESC
	`, passengerID)
}

func (s *MySQLStore) ListByDriverID(ctx context.Context, driverID string) ([]model.Order, error) {
	return s.queryOrders(ctx, `
		SELECT id, passenger_id, driver_id, status, pickup_lat, pickup_lng, pickup_address,
		       destination_lat, destination_lng, destination_address, estimated_price, final_price, created_at, updated_at
		FROM orders
		WHERE driver_id = ?
		ORDER BY created_at DESC
	`, driverID)
}

func (s *MySQLStore) FindActiveByDriverID(ctx context.Context, driverID string) (model.Order, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, passenger_id, driver_id, status, pickup_lat, pickup_lng, pickup_address,
		       destination_lat, destination_lng, destination_address, estimated_price, final_price, created_at, updated_at
		FROM orders
		WHERE driver_id = ? AND status IN (?, ?, ?)
		ORDER BY created_at DESC
		LIMIT 1
	`, driverID, model.OrderStatusAccepted, model.OrderStatusDriverArrived, model.OrderStatusInTrip)

	order, err := scanOrder(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Order{}, ErrNotFound
		}
		return model.Order{}, err
	}
	return order, nil
}

func (s *MySQLStore) Update(ctx context.Context, order model.Order) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE orders
		SET passenger_id = ?, driver_id = ?, status = ?, pickup_lat = ?, pickup_lng = ?, pickup_address = ?,
		    destination_lat = ?, destination_lng = ?, destination_address = ?, estimated_price = ?, final_price = ?, updated_at = ?
		WHERE id = ?
	`, order.PassengerID, order.DriverID, order.Status, order.PickupLat, order.PickupLng, order.PickupAddress,
		order.DestinationLat, order.DestinationLng, order.DestinationAddress, order.EstimatedPrice, order.FinalPrice, order.UpdatedAt, order.ID)
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
	return nil
}

func (s *MySQLStore) queryOrders(ctx context.Context, query string, args ...any) ([]model.Order, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]model.Order, 0)
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, order)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

type orderScanner interface {
	Scan(dest ...any) error
}

func scanOrder(scanner orderScanner) (model.Order, error) {
	var order model.Order
	err := scanner.Scan(
		&order.ID,
		&order.PassengerID,
		&order.DriverID,
		&order.Status,
		&order.PickupLat,
		&order.PickupLng,
		&order.PickupAddress,
		&order.DestinationLat,
		&order.DestinationLng,
		&order.DestinationAddress,
		&order.EstimatedPrice,
		&order.FinalPrice,
		&order.CreatedAt,
		&order.UpdatedAt,
	)
	return order, err
}
