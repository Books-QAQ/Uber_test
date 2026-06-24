package model

import "time"

const (
	TripStatusPending   = "pending"
	TripStatusInTrip    = "in_trip"
	TripStatusCompleted = "completed"
	TripStatusPaid      = "paid"
)

type Trip struct {
	ID               string      `json:"id"`
	OrderID          string      `json:"order_id"`
	PassengerID      string      `json:"passenger_id"`
	DriverID         string      `json:"driver_id"`
	Status           string      `json:"status"`
	StartedAt        time.Time   `json:"started_at,omitempty"`
	EndedAt          time.Time   `json:"ended_at,omitempty"`
	ActualDistanceM  int         `json:"actual_distance_m,omitempty"`
	ActualDurationS  int         `json:"actual_duration_s,omitempty"`
	WaitingDurationS int         `json:"waiting_duration_s,omitempty"`
	EstimatedPrice   float64     `json:"estimated_price,omitempty"`
	FinalPrice       float64     `json:"final_price,omitempty"`
	Points           []TripPoint `json:"points,omitempty"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
}

type TripPoint struct {
	ID         string    `json:"id"`
	TripID     string    `json:"trip_id"`
	OrderID    string    `json:"order_id"`
	DriverID   string    `json:"driver_id"`
	TripStatus string    `json:"trip_status"`
	Lat        float64   `json:"lat"`
	Lng        float64   `json:"lng"`
	SpeedKPH   float64   `json:"speed_kph,omitempty"`
	Heading    float64   `json:"heading,omitempty"`
	AccuracyM  float64   `json:"accuracy_m,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
	CreatedAt  time.Time `json:"created_at"`
}
