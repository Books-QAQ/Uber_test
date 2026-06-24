package model

import "time"

const (
	OrderStatusCreated         = "created"
	OrderStatusPendingDispatch = "pending_dispatch"
	OrderStatusAccepted        = "accepted"
	OrderStatusDriverArrived   = "driver_arrived"
	OrderStatusInTrip          = "in_trip"
	OrderStatusCompleted       = "completed"
	OrderStatusCancelled       = "cancelled"
	OrderStatusToBePaid        = "to_be_paid"
	OrderStatusPaid            = "paid"
)

type Order struct {
	ID                 string    `json:"id"`
	PassengerID        string    `json:"passenger_id"`
	DriverID           string    `json:"driver_id,omitempty"`
	DriverPlateNo      string    `json:"driver_plate_no,omitempty"`
	Status             string    `json:"status"`
	PickupLat          float64   `json:"pickup_lat"`
	PickupLng          float64   `json:"pickup_lng"`
	PickupAddress      string    `json:"pickup_address,omitempty"`
	DestinationLat     float64   `json:"destination_lat"`
	DestinationLng     float64   `json:"destination_lng"`
	DestinationAddress string    `json:"destination_address,omitempty"`
	EstimatedPrice     float64   `json:"estimated_price,omitempty"`
	FinalPrice         float64   `json:"final_price,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type CreateOrderInput struct {
	PassengerID        string  `json:"passenger_id"`
	PickupLat          float64 `json:"pickup_lat"`
	PickupLng          float64 `json:"pickup_lng"`
	PickupAddress      string  `json:"pickup_address"`
	DestinationLat     float64 `json:"destination_lat"`
	DestinationLng     float64 `json:"destination_lng"`
	DestinationAddress string  `json:"destination_address"`
	EstimatedPrice     float64 `json:"estimated_price"`
}

type UpdateOrderStatusInput struct {
	Status           string  `json:"status"`
	DriverID         string  `json:"driver_id,omitempty"`
	ActualDistanceM  int     `json:"actual_distance_m,omitempty"`
	ActualDurationS  int     `json:"actual_duration_s,omitempty"`
	WaitingDurationS int     `json:"waiting_duration_s,omitempty"`
	FinalPrice       float64 `json:"final_price,omitempty"`
}
