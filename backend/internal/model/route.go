package model

import "time"

type RoutePoint struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type DriverRoute struct {
	DriverID  string       `json:"driver_id"`
	OrderID   string       `json:"order_id"`
	Mode      string       `json:"mode"`
	Points    []RoutePoint `json:"points"`
	UpdatedAt time.Time    `json:"updated_at"`
}
