package model

import "time"

const (
	DispatchStatusPending   = "pending"
	DispatchStatusAccepted  = "accepted"
	DispatchStatusRejected  = "rejected"
	DispatchStatusExpired   = "expired"
	DispatchStatusCancelled = "cancelled"
)

type DispatchRecord struct {
	ID            string    `json:"id"`
	OrderID       string    `json:"order_id"`
	DriverID      string    `json:"driver_id"`
	Status        string    `json:"status"`
	DistanceM     float64   `json:"distance_m"`
	DispatchRound int       `json:"dispatch_round"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	RespondedAt   time.Time `json:"responded_at,omitempty"`
}

type DispatchAssignment struct {
	Dispatch DispatchRecord `json:"dispatch"`
	Order    Order          `json:"order"`
}
