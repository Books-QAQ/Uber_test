CREATE TABLE IF NOT EXISTS trip_points (
    id VARCHAR(64) PRIMARY KEY,
    trip_id VARCHAR(64) NOT NULL,
    order_id VARCHAR(64) NOT NULL,
    driver_id VARCHAR(64) NOT NULL,
    trip_status VARCHAR(32) NOT NULL,
    lat DOUBLE NOT NULL,
    lng DOUBLE NOT NULL,
    speed_kph DOUBLE NOT NULL DEFAULT 0,
    heading DOUBLE NOT NULL DEFAULT 0,
    accuracy_m DOUBLE NOT NULL DEFAULT 0,
    recorded_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    INDEX idx_trip_points_trip_recorded_at (trip_id, recorded_at),
    INDEX idx_trip_points_order_recorded_at (order_id, recorded_at),
    INDEX idx_trip_points_driver_recorded_at (driver_id, recorded_at)
);
