package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Principal struct {
	UserID   string `json:"user_id"`
	Role     string `json:"role"`
	DriverID string `json:"driver_id,omitempty"`
	Phone    string `json:"phone"`
}

type TokenClaims struct {
	Role     string `json:"role"`
	DriverID string `json:"driver_id,omitempty"`
	Phone    string `json:"phone"`
	jwt.RegisteredClaims
}

type TokenManager struct {
	secret []byte
	ttl    time.Duration
}

func NewTokenManager(secret string, ttl time.Duration) *TokenManager {
	return &TokenManager{
		secret: []byte(secret),
		ttl:    ttl,
	}
}

func (m *TokenManager) Issue(principal Principal) (string, error) {
	now := time.Now().UTC()
	claims := TokenClaims{
		Role:     principal.Role,
		DriverID: principal.DriverID,
		Phone:    principal.Phone,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   principal.UserID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

func (m *TokenManager) Parse(tokenString string) (Principal, error) {
	token, err := jwt.ParseWithClaims(tokenString, &TokenClaims{}, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return m.secret, nil
	})
	if err != nil {
		return Principal{}, ErrUnauthorized
	}

	claims, ok := token.Claims.(*TokenClaims)
	if !ok || !token.Valid {
		return Principal{}, ErrUnauthorized
	}

	return Principal{
		UserID:   claims.Subject,
		Role:     claims.Role,
		DriverID: claims.DriverID,
		Phone:    claims.Phone,
	}, nil
}
