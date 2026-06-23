package auth

import (
	"context"
	"database/sql"
	"errors"

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
