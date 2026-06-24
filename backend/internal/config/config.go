package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	AppEnv                 string
	HTTPAddr               string
	UDPAddr                string
	AuthJWTSecret          string
	AuthTokenTTL           time.Duration
	MySQLEnabled           bool
	MySQLDSN               string
	MySQLMaxOpenConns      int
	MySQLMaxIdleConns      int
	MySQLConnMaxLifetime   time.Duration
	WSReadBuffer           int
	WSWriteBuffer          int
	RecentLocationLimit    int
	DriverInactiveTimeout  time.Duration
	DriverSweepInterval    time.Duration
	DispatchPendingTimeout time.Duration
	DispatchMaxRounds      int
	RedisEnabled           bool
	RedisAddr              string
	RedisPassword          string
	RedisDB                int
	RedisKeyPrefix         string
	RedisLocationTTL       time.Duration
	RedisDispatchTTL       time.Duration
	RouteAMapWebKey        string
	RouteOSRMBaseURL       string
	RouteRequestTimeout    time.Duration
	MapMatchEnabled        bool
	MapMatchMinPoints      int
	MapMatchWindowSize     int
	MapMatchMaxLookback    time.Duration
	ShutdownTimeout        time.Duration
}

func Load() Config {
	return Config{
		AppEnv:                 getEnv("APP_ENV", "local"),
		HTTPAddr:               getEnv("HTTP_ADDR", ":8080"),
		UDPAddr:                getEnv("UDP_ADDR", ":9000"),
		AuthJWTSecret:          getEnv("AUTH_JWT_SECRET", "dev-secret-change-me"),
		AuthTokenTTL:           getEnvDuration("AUTH_TOKEN_TTL", 24*time.Hour),
		MySQLEnabled:           getEnvBool("MYSQL_ENABLED", false),
		MySQLDSN:               getEnv("MYSQL_DSN", "root:root@tcp(127.0.0.1:3306)/uber_test?parseTime=true"),
		MySQLMaxOpenConns:      getEnvInt("MYSQL_MAX_OPEN_CONNS", 10),
		MySQLMaxIdleConns:      getEnvInt("MYSQL_MAX_IDLE_CONNS", 5),
		MySQLConnMaxLifetime:   getEnvDuration("MYSQL_CONN_MAX_LIFETIME", time.Hour),
		WSReadBuffer:           getEnvInt("WS_READ_BUFFER", 1024),
		WSWriteBuffer:          getEnvInt("WS_WRITE_BUFFER", 1024),
		RecentLocationLimit:    getEnvInt("RECENT_LOCATION_LIMIT", 20),
		DriverInactiveTimeout:  getEnvDuration("DRIVER_INACTIVE_TIMEOUT", 15*time.Minute),
		DriverSweepInterval:    getEnvDuration("DRIVER_SWEEP_INTERVAL", 30*time.Second),
		DispatchPendingTimeout: getEnvDuration("DISPATCH_PENDING_TIMEOUT", 15*time.Second),
		DispatchMaxRounds:      getEnvInt("DISPATCH_MAX_ROUNDS", 3),
		RedisEnabled:           getEnvBool("REDIS_ENABLED", false),
		RedisAddr:              getEnv("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword:          getEnv("REDIS_PASSWORD", ""),
		RedisDB:                getEnvInt("REDIS_DB", 0),
		RedisKeyPrefix:         getEnv("REDIS_KEY_PREFIX", "uber-test"),
		RedisLocationTTL:       getEnvDuration("REDIS_LOCATION_TTL", 24*time.Hour),
		RedisDispatchTTL:       getEnvDuration("REDIS_DISPATCH_TTL", 30*time.Minute),
		RouteAMapWebKey:        getEnv("ROUTE_AMAP_WEB_KEY", ""),
		RouteOSRMBaseURL:       getEnv("ROUTE_OSRM_BASE_URL", "http://router.project-osrm.org"),
		RouteRequestTimeout:    getEnvDuration("ROUTE_REQUEST_TIMEOUT", 5*time.Second),
		MapMatchEnabled:        getEnvBool("MAPMATCH_ENABLED", true),
		MapMatchMinPoints:      getEnvInt("MAPMATCH_MIN_POINTS", 4),
		MapMatchWindowSize:     getEnvInt("MAPMATCH_WINDOW_SIZE", 8),
		MapMatchMaxLookback:    getEnvDuration("MAPMATCH_MAX_LOOKBACK", 45*time.Second),
		ShutdownTimeout:        getEnvDuration("SHUTDOWN_TIMEOUT", 10*time.Second),
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}

	return parsed
}

func getEnvBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}

	return parsed
}
