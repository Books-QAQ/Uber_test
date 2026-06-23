package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"uber-test/backend/internal/model"
)

type LoginResult struct {
	Token string     `json:"token"`
	User  model.User `json:"user"`
}

type Service struct {
	store  Store
	tokens *TokenManager
}

func NewService(store Store, tokens *TokenManager) *Service {
	return &Service{
		store:  store,
		tokens: tokens,
	}
}

func (s *Service) Register(ctx context.Context, input model.RegisterInput) (model.User, error) {
	input.Phone = strings.TrimSpace(input.Phone)
	input.Role = strings.TrimSpace(input.Role)
	if input.Phone == "" {
		return model.User{}, fmt.Errorf("register: missing phone")
	}
	if input.Password == "" {
		return model.User{}, fmt.Errorf("register: missing password")
	}
	if !model.IsValidRole(input.Role) {
		return model.User{}, fmt.Errorf("register: invalid role")
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return model.User{}, fmt.Errorf("register: hash password: %w", err)
	}

	now := time.Now().UTC()
	user := model.User{
		ID:           newUserID(),
		Phone:        input.Phone,
		PasswordHash: string(passwordHash),
		Role:         input.Role,
		DisplayName:  strings.TrimSpace(input.DisplayName),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if user.Role == model.RoleDriver {
		user.DriverID = newDriverID()
	}

	if err := s.store.CreateUser(ctx, user); err != nil {
		return model.User{}, err
	}

	return user, nil
}

func (s *Service) Login(ctx context.Context, input model.LoginInput) (LoginResult, error) {
	user, err := s.store.GetUserByPhone(ctx, strings.TrimSpace(input.Phone))
	if err != nil {
		return LoginResult{}, ErrInvalidCredentials
	}

	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)) != nil {
		return LoginResult{}, ErrInvalidCredentials
	}

	token, err := s.tokens.Issue(Principal{
		UserID:   user.ID,
		Role:     user.Role,
		DriverID: user.DriverID,
		Phone:    user.Phone,
	})
	if err != nil {
		return LoginResult{}, err
	}

	return LoginResult{
		Token: token,
		User:  user,
	}, nil
}

func (s *Service) Me(ctx context.Context, principal Principal) (model.User, error) {
	return s.store.GetUserByID(ctx, principal.UserID)
}

func newUserID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("user-%d", time.Now().UnixNano())
	}
	return "user-" + hex.EncodeToString(buf[:])
}

func newDriverID() string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("driver-%d", time.Now().UnixNano())
	}
	return "driver-" + hex.EncodeToString(buf[:])
}
