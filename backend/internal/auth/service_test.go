package auth

import (
	"context"
	"testing"
	"time"

	"uber-test/backend/internal/model"
)

func TestServiceRegisterAndLogin(t *testing.T) {
	t.Parallel()

	service := NewService(NewMemoryStore(), NewTokenManager("test-secret", time.Hour))

	user, err := service.Register(context.Background(), model.RegisterInput{
		Phone:    "13800000000",
		Password: "secret123",
		Role:     model.RoleDriver,
	})
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	if user.DriverID == "" {
		t.Fatalf("expected driver_id for driver role")
	}

	result, err := service.Login(context.Background(), model.LoginInput{
		Phone:    "13800000000",
		Password: "secret123",
	})
	if err != nil {
		t.Fatalf("login user: %v", err)
	}
	if result.Token == "" {
		t.Fatalf("expected token")
	}

	principal, err := service.tokens.Parse(result.Token)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if principal.UserID != user.ID || principal.DriverID != user.DriverID {
		t.Fatalf("unexpected principal: %+v", principal)
	}
}
