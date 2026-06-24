package model

import "time"

const (
	DriverSessionStatusOnline  = "online"
	DriverSessionStatusOffline = "offline"
	DriverSessionStatusExpired = "expired"
)

type Vehicle struct {
	ID        string    `json:"id"`
	DriverID  string    `json:"driver_id"`
	PlateNo   string    `json:"plate_no"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type DriverSession struct {
	ID              string    `json:"id"`
	DriverID        string    `json:"driver_id"`
	LoginToken      string    `json:"login_token"`
	DeviceType      string    `json:"device_type,omitempty"`
	Status          string    `json:"status"`
	OnlineAt        time.Time `json:"online_at"`
	OfflineAt       time.Time `json:"offline_at,omitempty"`
	LastHeartbeatAt time.Time `json:"last_heartbeat_at,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type DriverProfile struct {
	UserID      string `json:"user_id"`
	DriverID    string `json:"driver_id"`
	DisplayName string `json:"display_name,omitempty"`
	Phone       string `json:"phone"`
	PlateNo     string `json:"plate_no,omitempty"`
}
