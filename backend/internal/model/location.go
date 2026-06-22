package model

import "time"

const (
	DriverStatusOnline      = "online"
	DriverStatusOffline     = "offline"
	DriverStatusToPickup    = "to_pickup"
	DriverStatusInTrip      = "in_trip"
	DriverStatusUnavailable = "unavailable"
)

type DriverLocation struct {
	DriverID   string    `json:"driver_id"`
	OrderID    string    `json:"order_id,omitempty"`
	Lat        float64   `json:"lat"`
	Lng        float64   `json:"lng"`
	SpeedKPH   float64   `json:"speed_kph,omitempty"`
	Heading    float64   `json:"heading,omitempty"`
	AccuracyM  float64   `json:"accuracy_m,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
	SourceAddr string    `json:"source_addr,omitempty"`
}

type DriverHeartbeat struct {
	DriverID   string    `json:"driver_id"`
	OrderID    string    `json:"order_id,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
	SourceAddr string    `json:"source_addr,omitempty"`
}

type DriverStatus struct {
	DriverID   string    `json:"driver_id"`
	Status     string    `json:"status"`
	UpdatedAt  time.Time `json:"updated_at"`
	SourceAddr string    `json:"source_addr,omitempty"`
}

type NearbyQuery struct {
	Lat      float64
	Lng      float64
	RadiusM  float64
	Limit    int
	OnlyLive bool
}

type NearbyDriver struct {
	DriverID  string         `json:"driver_id"`
	Status    string         `json:"status"`
	DistanceM float64        `json:"distance_m"`
	Location  DriverLocation `json:"location"`
	UpdatedAt time.Time      `json:"updated_at"`
}

func IsDriverStatusAllowed(status string) bool {
	switch status {
	case DriverStatusOnline, DriverStatusOffline, DriverStatusToPickup, DriverStatusInTrip, DriverStatusUnavailable:
		return true
	default:
		return false
	}
}

func IsDriverStatusAvailableForNearby(status string) bool {
	return status == DriverStatusOnline
}
