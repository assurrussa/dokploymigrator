package config

import (
	"testing"
	"time"
)

func TestDurationEnv(t *testing.T) {
	t.Setenv("DURATION_SECONDS", "30")
	t.Setenv("DURATION_TEXT", "2m")
	t.Setenv("DURATION_BAD", "wat")

	if got := durationEnv("DURATION_SECONDS", time.Second); got != 30*time.Second {
		t.Fatalf("seconds duration = %s", got)
	}
	if got := durationEnv("DURATION_TEXT", time.Second); got != 2*time.Minute {
		t.Fatalf("text duration = %s", got)
	}
	if got := durationEnv("DURATION_BAD", 5*time.Second); got != 5*time.Second {
		t.Fatalf("bad duration fallback = %s", got)
	}
}

func TestLoadRequiresAuth(t *testing.T) {
	t.Setenv("MIGRATOR_BASIC_USER", "admin")
	t.Setenv("MIGRATOR_BASIC_PASSWORD", "secret")
	t.Setenv("MIGRATOR_ADMIN_TOKEN", "token")
	t.Setenv("DOKPLOY_HEALTH_PATH", "healthz")
	t.Setenv("DOKPLOY_DEPLOY_PATH", "api/deploy")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Addr == "" || cfg.StatePath == "" {
		t.Fatalf("expected defaults, got %+v", cfg)
	}
	if cfg.HealthPath != "/healthz" {
		t.Fatalf("HealthPath = %q, want /healthz", cfg.HealthPath)
	}
	if cfg.DeployPath != "/api/deploy" {
		t.Fatalf("DeployPath = %q, want /api/deploy", cfg.DeployPath)
	}
}
