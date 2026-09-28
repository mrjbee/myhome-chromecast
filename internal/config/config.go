package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	HTTPAddr           string
	DiscoveryInterface string
	DeviceTTL          time.Duration
	CastTimeout        time.Duration
	YouTubeTimeout     time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:           envOrDefault("HTTP_ADDR", ":8080"),
		DiscoveryInterface: os.Getenv("DISCOVERY_INTERFACE"),
		DeviceTTL:          3 * time.Hour,
		CastTimeout:        5 * time.Second,
		YouTubeTimeout:     15 * time.Second,
	}

	var err error
	if cfg.DeviceTTL, err = durationEnv("DEVICE_TTL", cfg.DeviceTTL); err != nil {
		return Config{}, err
	}
	if cfg.CastTimeout, err = durationEnv("CAST_TIMEOUT", cfg.CastTimeout); err != nil {
		return Config{}, err
	}
	if cfg.YouTubeTimeout, err = durationEnv("YOUTUBE_TIMEOUT", cfg.YouTubeTimeout); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration: %w", name, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", name)
	}
	return duration, nil
}
