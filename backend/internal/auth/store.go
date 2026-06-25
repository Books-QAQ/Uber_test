package auth

import (
	"context"
	"sync"

	"uber-test/backend/internal/model"
)

type Store interface {
	CreateUser(ctx context.Context, user model.User) error
	GetUserByPhone(ctx context.Context, phone string) (model.User, error)
	GetUserByID(ctx context.Context, id string) (model.User, error)
	UpsertVehicle(ctx context.Context, vehicle model.Vehicle) error
	GetDriverProfileByDriverID(ctx context.Context, driverID string) (model.DriverProfile, error)
	UpsertDriverSession(ctx context.Context, session model.DriverSession) error
}

type MemoryStore struct {
	mu               sync.RWMutex
	usersByID        map[string]model.User
	phoneToUser      map[string]string
	driverToUser     map[string]string
	vehiclesByDriver map[string]model.Vehicle
	sessionsByDriver map[string]model.DriverSession
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		usersByID:        make(map[string]model.User),
		phoneToUser:      make(map[string]string),
		driverToUser:     make(map[string]string),
		vehiclesByDriver: make(map[string]model.Vehicle),
		sessionsByDriver: make(map[string]model.DriverSession),
	}
}

func (s *MemoryStore) CreateUser(_ context.Context, user model.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.phoneToUser[user.Phone]; exists {
		return ErrDuplicatePhone
	}

	s.usersByID[user.ID] = user
	s.phoneToUser[user.Phone] = user.ID
	if user.DriverID != "" {
		s.driverToUser[user.DriverID] = user.ID
	}
	return nil
}

func (s *MemoryStore) GetUserByPhone(_ context.Context, phone string) (model.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.phoneToUser[phone]
	if !ok {
		return model.User{}, ErrUserNotFound
	}

	user, exists := s.usersByID[id]
	if !exists {
		return model.User{}, ErrUserNotFound
	}

	return user, nil
}

func (s *MemoryStore) GetUserByID(_ context.Context, id string) (model.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, ok := s.usersByID[id]
	if !ok {
		return model.User{}, ErrUserNotFound
	}

	return user, nil
}

func (s *MemoryStore) UpsertVehicle(_ context.Context, vehicle model.Vehicle) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for driverID, existing := range s.vehiclesByDriver {
		if driverID != vehicle.DriverID && existing.PlateNo == vehicle.PlateNo {
			return ErrDuplicatePlateNo
		}
	}
	s.vehiclesByDriver[vehicle.DriverID] = vehicle
	return nil
}

func (s *MemoryStore) GetDriverProfileByDriverID(_ context.Context, driverID string) (model.DriverProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	userID, ok := s.driverToUser[driverID]
	if !ok {
		return model.DriverProfile{}, ErrUserNotFound
	}

	user, exists := s.usersByID[userID]
	if !exists {
		return model.DriverProfile{}, ErrUserNotFound
	}

	profile := model.DriverProfile{
		UserID:      user.ID,
		DriverID:    user.DriverID,
		DisplayName: user.DisplayName,
		Phone:       user.Phone,
	}
	if vehicle, ok := s.vehiclesByDriver[driverID]; ok {
		profile.PlateNo = vehicle.PlateNo
	}

	return profile, nil
}

func (s *MemoryStore) UpsertDriverSession(_ context.Context, session model.DriverSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessionsByDriver[session.DriverID] = session
	return nil
}
