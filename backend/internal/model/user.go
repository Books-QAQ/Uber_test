package model

import "time"

const (
	RolePassenger = "passenger"
	RoleDriver    = "driver"
	RoleAdmin     = "admin"
)

type User struct {
	ID           string    `json:"id"`
	Phone        string    `json:"phone"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	DisplayName  string    `json:"display_name,omitempty"`
	DriverID     string    `json:"driver_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type RegisterInput struct {
	Phone       string `json:"phone"`
	Password    string `json:"password"`
	Role        string `json:"role"`
	DisplayName string `json:"display_name"`
	PlateNo     string `json:"plate_no,omitempty"`
	DeviceType  string `json:"device_type,omitempty"`
}

type LoginInput struct {
	Phone      string `json:"phone"`
	Password   string `json:"password"`
	DeviceType string `json:"device_type,omitempty"`
}

func IsValidRole(role string) bool {
	switch role {
	case RolePassenger, RoleDriver, RoleAdmin:
		return true
	default:
		return false
	}
}
