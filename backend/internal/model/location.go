package model

import "time"

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
