package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/assurrussa/dokploymigrator/internal/config"
)

func TestBuildRuntimeAllowsServeWithoutDokployDB(t *testing.T) {
	cfg := config.Config{
		StatePath: filepath.Join(t.TempDir(), "state.sqlite"),
	}
	store, manager, cleanup, err := buildRuntime(context.Background(), cfg, false)
	if err != nil {
		t.Fatalf("buildRuntime() error = %v", err)
	}
	defer cleanup()
	if store == nil || manager == nil {
		t.Fatal("expected store and manager")
	}
}

func TestBuildRuntimeRequiresDokployDBForCommands(t *testing.T) {
	cfg := config.Config{
		StatePath: filepath.Join(t.TempDir(), "state.sqlite"),
	}
	_, _, cleanup, err := buildRuntime(context.Background(), cfg, true)
	if cleanup != nil {
		cleanup()
	}
	if err == nil {
		t.Fatal("expected missing DB error")
	}
}

func TestApplyCLIRequiresConfirmation(t *testing.T) {
	err := apply(context.Background(), []string{
		"-job", "job-1",
		"-plan", "plan.json",
	})
	if err == nil {
		t.Fatal("expected missing confirmation error")
	}
	if !errors.Is(err, errApplyConfirmationRequired) {
		t.Fatalf("apply() error = %v, want %v", err, errApplyConfirmationRequired)
	}
}
