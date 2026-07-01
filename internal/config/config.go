// Package config loads runtime configuration for Dokploy Migrator.
package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains process-level settings. Secret values are read at runtime and
// must not be written to audit reports.
type Config struct {
	Addr               string
	StatePath          string
	DokployPostgresDSN string
	DokployAPIBaseURL  string
	DokployAPIToken    string
	BasicAuthUser      string
	BasicAuthPassword  string
	AdminToken         string
	DeadAfter          time.Duration
	SchemaAllowlist    []string
	DeployPath         string
	HealthPath         string
}

// Load reads environment variables into Config.
func Load() (Config, error) {
	cfg := Config{
		Addr:              getenv("MIGRATOR_ADDR", ":8080"),
		StatePath:         getenv("MIGRATOR_STATE_PATH", "dokploy-migrator.sqlite"),
		DokployAPIBaseURL: strings.TrimRight(os.Getenv("DOKPLOY_API_BASE_URL"), "/"),
		DokployAPIToken:   os.Getenv("DOKPLOY_API_TOKEN"),
		BasicAuthUser:     os.Getenv("MIGRATOR_BASIC_USER"),
		BasicAuthPassword: os.Getenv("MIGRATOR_BASIC_PASSWORD"),
		AdminToken:        os.Getenv("MIGRATOR_ADMIN_TOKEN"),
		DeadAfter:         durationEnv("MIGRATOR_DEAD_AFTER", 10*time.Minute),
		SchemaAllowlist:   splitList(os.Getenv("MIGRATOR_SCHEMA_ALLOWLIST")),
		DeployPath:        normalizeURLPath(os.Getenv("DOKPLOY_DEPLOY_PATH")),
		HealthPath:        normalizeURLPath(getenv("DOKPLOY_HEALTH_PATH", "/health")),
	}

	cfg.DokployPostgresDSN = os.Getenv("DOKPLOY_POSTGRES_DSN")
	if cfg.DokployPostgresDSN == "" {
		cfg.DokployPostgresDSN = os.Getenv("DATABASE_URL")
	}

	if cfg.BasicAuthUser == "" || cfg.BasicAuthPassword == "" {
		return Config{}, errors.New("MIGRATOR_BASIC_USER and MIGRATOR_BASIC_PASSWORD are required")
	}
	if cfg.AdminToken == "" {
		return Config{}, errors.New("MIGRATOR_ADMIN_TOKEN is required")
	}
	return cfg, nil
}

// RequireDokployDB returns an error when DB operations are requested without a DSN.
func (c Config) RequireDokployDB() error {
	if c.DokployPostgresDSN == "" {
		return errors.New("DOKPLOY_POSTGRES_DSN or DATABASE_URL is required")
	}
	return nil
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func splitList(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func normalizeURLPath(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "/") {
		return trimmed
	}
	return "/" + trimmed
}
