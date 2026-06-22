package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	AppEnv              string
	HTTPAddr            string
	UDPAddr             string
	WSReadBuffer        int
	WSWriteBuffer       int
	RecentLocationLimit int
	ShutdownTimeout     time.Duration
}

func Load() Config {
	return Config{
		AppEnv:              getEnv("APP_ENV", "local"),
		HTTPAddr:            getEnv("HTTP_ADDR", ":8080"),
		UDPAddr:             getEnv("UDP_ADDR", ":9000"),
		WSReadBuffer:        getEnvInt("WS_READ_BUFFER", 1024),
		WSWriteBuffer:       getEnvInt("WS_WRITE_BUFFER", 1024),
		RecentLocationLimit: getEnvInt("RECENT_LOCATION_LIMIT", 20),
		ShutdownTimeout:     getEnvDuration("SHUTDOWN_TIMEOUT", 10*time.Second),
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
