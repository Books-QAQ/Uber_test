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
}

type MemoryStore struct {
	mu          sync.RWMutex
	usersByID   map[string]model.User
	phoneToUser map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		usersByID:   make(map[string]model.User),
		phoneToUser: make(map[string]string),
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
