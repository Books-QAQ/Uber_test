CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(64) PRIMARY KEY,
    phone VARCHAR(32) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    role VARCHAR(32) NOT NULL,
    display_name VARCHAR(128) NOT NULL DEFAULT '',
    driver_id VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS orders (
    id VARCHAR(64) PRIMARY KEY,
    passenger_id VARCHAR(64) NOT NULL,
    driver_id VARCHAR(64) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL,
    pickup_lat DOUBLE NOT NULL,
    pickup_lng DOUBLE NOT NULL,
    pickup_address VARCHAR(255) NOT NULL DEFAULT '',
    destination_lat DOUBLE NOT NULL,
    destination_lng DOUBLE NOT NULL,
    destination_address VARCHAR(255) NOT NULL DEFAULT '',
    estimated_price DOUBLE NOT NULL DEFAULT 0,
    final_price DOUBLE NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    INDEX idx_orders_passenger_id (passenger_id),
    INDEX idx_orders_driver_id (driver_id),
    INDEX idx_orders_status (status)
);

CREATE TABLE IF NOT EXISTS trips (
    id VARCHAR(64) PRIMARY KEY,
    order_id VARCHAR(64) NOT NULL UNIQUE,
    passenger_id VARCHAR(64) NOT NULL,
    driver_id VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    started_at DATETIME NULL,
    ended_at DATETIME NULL,
    actual_distance_m INT NOT NULL DEFAULT 0,
    actual_duration_s INT NOT NULL DEFAULT 0,
    waiting_duration_s INT NOT NULL DEFAULT 0,
    estimated_price DOUBLE NOT NULL DEFAULT 0,
    final_price DOUBLE NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    INDEX idx_trips_driver_id (driver_id)
);
