package state

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestStoreJobLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	job := Job{
		ID:             "job-1",
		SourceServerID: "dead",
		TargetServerID: "live",
		Mode:           "dead_recovery",
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if err := store.UpdateJob(ctx, "job-1", JobRunning, "preflight"); err != nil {
		t.Fatalf("UpdateJob() error = %v", err)
	}
	if err := store.AppendEvent(ctx, "job-1", "info", "preflight passed", map[string]string{"step": "preflight"}); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	got, err := store.GetJob(ctx, "job-1")
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.Status != JobRunning || got.Checkpoint != "preflight" {
		t.Fatalf("unexpected job: %+v", got)
	}

	events, err := store.ListEvents(ctx, "job-1")
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 1 || events[0].Message != "preflight passed" {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestUpdateJobRequiresExistingJob(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	err = store.UpdateJob(ctx, "missing-job", JobRunning, "preflight")
	if !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("UpdateJob() error = %v, want %v", err, ErrJobNotFound)
	}
}

func TestAppendEventRequiresExistingJob(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	if err := store.AppendEvent(ctx, "missing-job", "info", "orphan", map[string]string{}); err == nil {
		t.Fatal("expected foreign key error for missing job")
	}
}
