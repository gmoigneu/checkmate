// Package config resolves runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds every knob the server reads at boot.
type Config struct {
	// Env is "development" or "production"; it only affects log formatting.
	Env string

	// Addr is the listen address, e.g. ":8080".
	Addr string

	// DatabasePath is the sqlite file. Its parent directory is created on boot.
	DatabasePath string

	// AutoMigrate applies pending migrations during boot.
	AutoMigrate bool

	// ShutdownTimeout bounds how long in-flight requests get to finish.
	ShutdownTimeout time.Duration
}

// Load reads configuration from CHECKMATE_* environment variables, falling back
// to defaults suitable for local development.
func Load() (Config, error) {
	cfg := Config{
		Env:             env("CHECKMATE_ENV", "development"),
		Addr:            env("CHECKMATE_ADDR", ":8080"),
		DatabasePath:    env("CHECKMATE_DB_PATH", "checkmate.db"),
		AutoMigrate:     true,
		ShutdownTimeout: 15 * time.Second,
	}

	var err error

	if cfg.AutoMigrate, err = envBool("CHECKMATE_AUTO_MIGRATE", true); err != nil {
		return Config{}, err
	}

	if cfg.ShutdownTimeout, err = envDuration("CHECKMATE_SHUTDOWN_TIMEOUT", 15*time.Second); err != nil {
		return Config{}, err
	}

	switch cfg.Env {
	case "development", "production":
	default:
		return Config{}, fmt.Errorf("config: CHECKMATE_ENV must be development or production, got %q", cfg.Env)
	}

	return cfg, nil
}

// Development reports whether the server is running in development mode.
func (c Config) Development() bool { return c.Env == "development" }

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}

	return fallback
}

func envBool(key string, fallback bool) (bool, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}

	v, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("config: %s: %w", key, err)
	}

	return v, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}

	v, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("config: %s: %w", key, err)
	}

	return v, nil
}
