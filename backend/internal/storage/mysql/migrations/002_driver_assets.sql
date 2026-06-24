CREATE TABLE IF NOT EXISTS vehicles (
    id VARCHAR(64) PRIMARY KEY,
    driver_id VARCHAR(64) NOT NULL UNIQUE,
    plate_no VARCHAR(32) NOT NULL UNIQUE,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    INDEX idx_vehicles_plate_no (plate_no)
);

CREATE TABLE IF NOT EXISTS driver_sessions (
    id VARCHAR(64) PRIMARY KEY,
    driver_id VARCHAR(64) NOT NULL UNIQUE,
    login_token VARCHAR(512) NOT NULL,
    device_type VARCHAR(32) NOT NULL DEFAULT 'unknown',
    status VARCHAR(32) NOT NULL,
    online_at DATETIME NOT NULL,
    offline_at DATETIME NULL,
    last_heartbeat_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    INDEX idx_driver_sessions_status (status),
    INDEX idx_driver_sessions_updated_at (updated_at)
);
