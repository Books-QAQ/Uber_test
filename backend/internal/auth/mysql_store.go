package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"

	"uber-test/backend/internal/model"
)

type MySQLStore struct {
	db *sql.DB
}

func NewMySQLStore(db *sql.DB) *MySQLStore {
	return &MySQLStore{db: db}
}

func (s *MySQLStore) CreateUser(ctx context.Context, user model.User) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (id, phone, password_hash, role, display_name, driver_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, user.ID, user.Phone, user.PasswordHash, user.Role, user.DisplayName, user.DriverID, user.CreatedAt, user.UpdatedAt)
	if err != nil {
		var mysqlErr *drivermysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return ErrDuplicatePhone
		}
		return err
	}
	return nil
}

func (s *MySQLStore) UpsertVehicle(ctx context.Context, vehicle model.Vehicle) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO vehicles (id, driver_id, plate_no, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			plate_no = VALUES(plate_no),
			updated_at = VALUES(updated_at)
	`, vehicle.ID, vehicle.DriverID, vehicle.PlateNo, vehicle.CreatedAt, vehicle.UpdatedAt)
	return err
}

func (s *MySQLStore) GetUserByPhone(ctx context.Context, phone string) (model.User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, phone, password_hash, role, display_name, driver_id, created_at, updated_at
		FROM users
		WHERE phone = ?
	`, phone)

	user, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, ErrUserNotFound
		}
		return model.User{}, err
	}
	return user, nil
}

func (s *MySQLStore) GetUserByID(ctx context.Context, id string) (model.User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, phone, password_hash, role, display_name, driver_id, created_at, updated_at
		FROM users
		WHERE id = ?
	`, id)

	user, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, ErrUserNotFound
		}
		return model.User{}, err
	}
	return user, nil
}

func (s *MySQLStore) GetDriverProfileByDriverID(ctx context.Context, driverID string) (model.DriverProfile, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.driver_id, u.display_name, u.phone, COALESCE(v.plate_no, '')
		FROM users u
		LEFT JOIN vehicles v ON v.driver_id = u.driver_id
		WHERE u.driver_id = ?
		LIMIT 1
	`, driverID)

	var profile model.DriverProfile
	err := row.Scan(
		&profile.UserID,
		&profile.DriverID,
		&profile.DisplayName,
		&profile.Phone,
		&profile.PlateNo,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.DriverProfile{}, ErrUserNotFound
		}
		return model.DriverProfile{}, err
	}
	return profile, nil
}

func (s *MySQLStore) UpsertDriverSession(ctx context.Context, session model.DriverSession) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO driver_sessions (
			id, driver_id, login_token, device_type, status, online_at, offline_at, last_heartbeat_at, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			login_token = VALUES(login_token),
			device_type = VALUES(device_type),
			status = VALUES(status),
			online_at = VALUES(online_at),
			offline_at = VALUES(offline_at),
			last_heartbeat_at = VALUES(last_heartbeat_at),
			updated_at = VALUES(updated_at)
	`, session.ID, session.DriverID, session.LoginToken, session.DeviceType, session.Status, session.OnlineAt, nullableTime(session.OfflineAt), nullableTime(session.LastHeartbeatAt), session.CreatedAt, session.UpdatedAt)
	return err
}

type userScanner interface {
	Scan(dest ...any) error
}

func scanUser(scanner userScanner) (model.User, error) {
	var user model.User
	err := scanner.Scan(
		&user.ID,
		&user.Phone,
		&user.PasswordHash,
		&user.Role,
		&user.DisplayName,
		&user.DriverID,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	return user, err
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
