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
	input.PlateNo = normalizePlateNo(input.PlateNo)
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
	if user.Role == model.RoleDriver && input.PlateNo != "" {
		now := time.Now().UTC()
		if err := s.store.UpsertVehicle(ctx, model.Vehicle{
			ID:        newVehicleID(),
			DriverID:  user.DriverID,
			PlateNo:   input.PlateNo,
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			return model.User{}, err
		}
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
	if user.Role == model.RoleDriver && user.DriverID != "" {
		now := time.Now().UTC()
		if err := s.store.UpsertDriverSession(ctx, model.DriverSession{
			ID:              newDriverSessionID(),
			DriverID:        user.DriverID,
			LoginToken:      token,
			DeviceType:      normalizeDeviceType(input.DeviceType),
			Status:          model.DriverSessionStatusOnline,
			OnlineAt:        now,
			LastHeartbeatAt: now,
			CreatedAt:       now,
			UpdatedAt:       now,
		}); err != nil {
			return LoginResult{}, err
		}
	}

	return LoginResult{
		Token: token,
		User:  user,
	}, nil
}

func (s *Service) Me(ctx context.Context, principal Principal) (model.User, error) {
	return s.store.GetUserByID(ctx, principal.UserID)
}

func (s *Service) GetDriverProfileByDriverID(ctx context.Context, driverID string) (model.DriverProfile, error) {
	if strings.TrimSpace(driverID) == "" {
		return model.DriverProfile{}, fmt.Errorf("get driver profile: missing driver_id")
	}
	return s.store.GetDriverProfileByDriverID(ctx, strings.TrimSpace(driverID))
}

func (s *Service) UpsertDriverVehicle(ctx context.Context, driverID, plateNo string) error {
	driverID = strings.TrimSpace(driverID)
	plateNo = normalizePlateNo(plateNo)
	if driverID == "" {
		return fmt.Errorf("upsert driver vehicle: missing driver_id")
	}
	if plateNo == "" {
		return fmt.Errorf("upsert driver vehicle: missing plate_no")
	}

	now := time.Now().UTC()
	return s.store.UpsertVehicle(ctx, model.Vehicle{
		ID:        newVehicleID(),
		DriverID:  driverID,
		PlateNo:   plateNo,
		CreatedAt: now,
		UpdatedAt: now,
	})
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

func newVehicleID() string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("vehicle-%d", time.Now().UnixNano())
	}
	return "vehicle-" + hex.EncodeToString(buf[:])
}

func newDriverSessionID() string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("session-%d", time.Now().UnixNano())
	}
	return "session-" + hex.EncodeToString(buf[:])
}

func normalizePlateNo(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "")
	return value
}

func normalizeDeviceType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}
