package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/assurrussa/dokploymigrator/internal/jobs"
	"github.com/assurrussa/dokploymigrator/internal/model"
	"github.com/assurrussa/dokploymigrator/internal/state"
)

func TestBasicAuthRejectsMissingCredentials(t *testing.T) {
	srv := New(Config{BasicUser: "u", BasicPassword: "p", AdminToken: "t"}, nil, nil)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestBasicAuthAllowsValidCredentials(t *testing.T) {
	srv := New(Config{BasicUser: "u", BasicPassword: "p", AdminToken: "t"}, nil, nil)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/health", nil)
	req.SetBasicAuth("u", "p")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestServersEndpointReturnsScannedServers(t *testing.T) {
	manager := jobs.NewManager(nil, &fakeServerDBAdapter{}, nil)
	srv := New(Config{BasicUser: "u", BasicPassword: "p", AdminToken: "t"}, nil, manager)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/servers", nil)
	req.SetBasicAuth("u", "p")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() == "null\n" || rec.Body.String() == "[]\n" {
		t.Fatalf("expected server list, got %s", rec.Body.String())
	}
}

func TestJobsEndpointReturnsLatestFiftyJobs(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	defer store.Close()
	seedJobs(t, ctx, store)

	srv := New(Config{BasicUser: "u", BasicPassword: "p", AdminToken: "t"}, store, nil)
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/jobs", nil)
	req.SetBasicAuth("u", "p")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got jobsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode jobs response: %v", err)
	}
	if len(got.Jobs) != jobHistoryPageSize {
		t.Fatalf("jobs length = %d, want %d", len(got.Jobs), jobHistoryPageSize)
	}
	if got.Total != 55 {
		t.Fatalf("total = %d, want 55", got.Total)
	}
	if got.Limit != jobHistoryPageSize {
		t.Fatalf("limit = %d, want %d", got.Limit, jobHistoryPageSize)
	}
	if got.Offset != 0 {
		t.Fatalf("offset = %d, want 0", got.Offset)
	}
	if got.ProtectedCount != protectedJobCount {
		t.Fatalf("protected count = %d, want %d", got.ProtectedCount, protectedJobCount)
	}
}

func TestJobsEndpointSupportsOffset(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	defer store.Close()
	seedJobs(t, ctx, store)

	srv := New(Config{BasicUser: "u", BasicPassword: "p", AdminToken: "t"}, store, nil)
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/jobs?limit=50&offset=50", nil)
	req.SetBasicAuth("u", "p")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got jobsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode jobs response: %v", err)
	}
	if len(got.Jobs) != 5 {
		t.Fatalf("jobs length = %d, want 5", len(got.Jobs))
	}
	if got.Total != 55 {
		t.Fatalf("total = %d, want 55", got.Total)
	}
	if got.Offset != 50 {
		t.Fatalf("offset = %d, want 50", got.Offset)
	}
}

func TestDeleteJobRejectsLatestFifty(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	defer store.Close()
	seedJobs(t, ctx, store)

	srv := New(Config{BasicUser: "u", BasicPassword: "p", AdminToken: "t"}, store, nil)
	req := httptest.NewRequestWithContext(ctx, http.MethodDelete, "/api/jobs/job-54", nil)
	req.SetBasicAuth("u", "p")
	req.Header.Set("X-Migrator-Admin-Token", "t")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if _, err := store.GetJob(ctx, "job-54"); err != nil {
		t.Fatalf("protected job was deleted: %v", err)
	}
}

func TestDeleteJobAllowsOlderThanLatestFifty(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	defer store.Close()
	seedJobs(t, ctx, store)
	if err := store.AppendEvent(ctx, "job-0", "info", "created", map[string]string{"job": "job-0"}); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	if err := store.SaveReport(ctx, "job-0", `{"ok":true}`, "# ok"); err != nil {
		t.Fatalf("SaveReport() error = %v", err)
	}

	srv := New(Config{BasicUser: "u", BasicPassword: "p", AdminToken: "t"}, store, nil)
	req := httptest.NewRequestWithContext(ctx, http.MethodDelete, "/api/jobs/job-0", nil)
	req.SetBasicAuth("u", "p")
	req.Header.Set("X-Migrator-Admin-Token", "t")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if _, err := store.GetJob(ctx, "job-0"); !errors.Is(err, state.ErrJobNotFound) {
		t.Fatalf("GetJob() error = %v, want ErrJobNotFound", err)
	}
	events, err := store.ListEvents(ctx, "job-0")
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events length = %d, want 0", len(events))
	}
}

func TestAdminTokenCannotBeEmpty(t *testing.T) {
	srv := New(Config{BasicUser: "u", BasicPassword: "p"}, nil, nil)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/plan", nil)
	req.SetBasicAuth("u", "p")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func openTestStore(t *testing.T, ctx context.Context) *state.Store {
	t.Helper()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatalf("state.Open() error = %v", err)
	}
	return store
}

func seedJobs(t *testing.T, ctx context.Context, store *state.Store) {
	t.Helper()
	for i := range 55 {
		job := state.Job{
			ID:             "job-" + strconv.Itoa(i),
			SourceServerID: "source",
			TargetServerID: "target",
			Mode:           string(model.ModeDeadRecovery),
			Status:         state.JobSucceeded,
		}
		if err := store.CreateJob(ctx, job); err != nil {
			t.Fatalf("CreateJob(%s) error = %v", job.ID, err)
		}
	}
}

type fakeServerDBAdapter struct{}

func (f *fakeServerDBAdapter) ListServers(
	_ context.Context,
	_ time.Duration,
) ([]model.Server, error) {
	return []model.Server{
		{ID: "server-1", Name: "Server 1", Status: model.ServerOnline, ResourceCount: 2},
	}, nil
}

func (f *fakeServerDBAdapter) BuildRetargetPlan(
	_ context.Context,
	_ string,
	_ string,
) (model.MigrationPlan, error) {
	return model.MigrationPlan{}, nil
}

func (f *fakeServerDBAdapter) ApplyRetargetPlan(
	_ context.Context,
	_ model.MigrationPlan,
	_ string,
) error {
	return nil
}

func (f *fakeServerDBAdapter) RollbackRetargetPlan(
	_ context.Context,
	_ model.MigrationPlan,
	_ string,
) error {
	return nil
}
