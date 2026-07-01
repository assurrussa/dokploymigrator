package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/assurrussa/dokploymigrator/internal/model"
	"github.com/assurrussa/dokploymigrator/internal/state"
)

const (
	testSourceServerID = "source"
	testTargetServerID = "target"
	testSchemaHash     = "hash"
	testJobID          = "job-1"
	testPlanID         = "plan-1"
	testResourceID     = "app"
	testResourceTable  = "application"
	testResourceColumn = "applicationId"
	testComposeID      = "compose-1"
)

func TestBuildReport(t *testing.T) {
	plan := model.MigrationPlan{
		ID:             testPlanID,
		SourceServerID: testSourceServerID,
		TargetServerID: testTargetServerID,
		CreatedAt:      time.Now(),
		SchemaHash:     "abc",
		Rows:           []model.PlanRow{{Table: testResourceTable, ID: testResourceID}},
	}
	jsonReport, markdown, err := buildReport(testJobID, plan, "ok")
	if err != nil {
		t.Fatalf("buildReport() error = %v", err)
	}
	if jsonReport == "" || markdown == "" {
		t.Fatal("expected non-empty reports")
	}
}

func TestAPIClientHealth(t *testing.T) {
	ctx := context.Background()
	client := NewAPIClient("", "", "/health", "/deploy")
	if err := client.Health(ctx); err == nil {
		t.Fatal("expected unconfigured health error")
	}
}

func TestAPIClientNormalizesPaths(t *testing.T) {
	ctx := context.Background()
	seen := make(map[string]string)
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.Method] = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(testServer.Close)

	client := NewAPIClient(testServer.URL+"/", "", "health", "api/deploy")
	if err := client.Health(ctx); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if err := client.Deploy(ctx, "resource-1"); err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	if seen[http.MethodGet] != "/health" {
		t.Fatalf("health path = %q, want /health", seen[http.MethodGet])
	}
	if seen[http.MethodPost] != "/api/deploy" {
		t.Fatalf("deploy path = %q, want /api/deploy", seen[http.MethodPost])
	}
}

func TestAPIClientDeployRequiresConfiguredPath(t *testing.T) {
	client := NewAPIClient("https://dokploy.example", "", "/health", "")
	err := client.Deploy(context.Background(), "resource-1")
	if !errors.Is(err, errDokployDeployPathMissing) {
		t.Fatalf("Deploy() error = %v, want %v", err, errDokployDeployPathMissing)
	}
}

func TestApplyRequiresConfirmationText(t *testing.T) {
	for _, confirmationText := range []string{"", "CONFIRM"} {
		t.Run(confirmationText, func(t *testing.T) {
			manager := NewManager(nil, &fakeDBAdapter{}, nil)
			err := manager.Apply(
				context.Background(),
				"job-1",
				model.MigrationPlan{},
				ApplyOptions{ConfirmationText: confirmationText},
			)
			if !errors.Is(err, errApplyConfirmationMissing) {
				t.Fatalf("Apply() error = %v, want %v", err, errApplyConfirmationMissing)
			}
		})
	}
}

func TestApplyPassesSchemaApproval(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	fakeDB := &fakeDBAdapter{}
	manager := NewManager(store, fakeDB, nil)
	plan := testPlan()

	job := createTestJob(t, ctx, store, plan)
	opts := ApplyOptions{SchemaHashApproval: testSchemaHash, ConfirmationText: applyConfirmationText}
	if err := manager.Apply(ctx, job.ID, plan, opts); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if fakeDB.appliedApproval != testSchemaHash {
		t.Fatalf("applied approval = %q, want %s", fakeDB.appliedApproval, testSchemaHash)
	}
	if len(fakeDB.appliedPlan.Rows) != len(plan.Rows) {
		t.Fatalf("applied rows = %d, want %d", len(fakeDB.appliedPlan.Rows), len(plan.Rows))
	}
}

func TestRollbackPassesSchemaApproval(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	fakeDB := &fakeDBAdapter{}
	manager := NewManager(store, fakeDB, nil)
	plan := testPlan()
	job := createTestJob(t, ctx, store, plan)

	if err := manager.Rollback(ctx, job.ID, plan, RollbackOptions{
		SchemaHashApproval: testSchemaHash,
	}); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if fakeDB.rolledBackApproval != testSchemaHash {
		t.Fatalf("rollback approval = %q, want %s", fakeDB.rolledBackApproval, testSchemaHash)
	}
}

func TestRollbackRecordsFailureEvent(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	fakeDB := &fakeDBAdapter{rollbackErr: errors.New("rollback boom")}
	manager := NewManager(store, fakeDB, nil)
	plan := testPlan()
	job := createTestJob(t, ctx, store, plan)

	err := manager.Rollback(ctx, job.ID, plan, RollbackOptions{
		SchemaHashApproval: testSchemaHash,
	})
	if err == nil {
		t.Fatal("expected rollback error")
	}
	gotJob, getErr := store.GetJob(ctx, job.ID)
	if getErr != nil {
		t.Fatalf("GetJob() error = %v", getErr)
	}
	if gotJob.Status != state.JobFailed || gotJob.Checkpoint != "rollback-retarget" {
		t.Fatalf("job state = %s/%s, want failed/rollback-retarget", gotJob.Status, gotJob.Checkpoint)
	}
	events, listErr := store.ListEvents(ctx, job.ID)
	if listErr != nil {
		t.Fatalf("ListEvents() error = %v", listErr)
	}
	var sawStart bool
	var sawFailure bool
	for _, event := range events {
		if event.Message == "applying metadata rollback" {
			sawStart = true
		}
		if event.Message == "metadata rollback failed" {
			sawFailure = true
			var payload map[string]string
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("decode failure payload: %v", err)
			}
			if payload[eventErrorKey] != "rollback boom" {
				t.Fatalf("failure payload = %+v, want rollback boom", payload)
			}
		}
	}
	if !sawStart || !sawFailure {
		t.Fatalf("rollback audit events sawStart=%v sawFailure=%v; events=%+v", sawStart, sawFailure, events)
	}
}

func TestApplyAllowsStoredDryRunPlanSubset(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	fakeDB := &fakeDBAdapter{}
	manager := NewManager(store, fakeDB, nil)
	plan := testPlan()
	plan.Rows = append(plan.Rows, model.PlanRow{
		Table:       "compose",
		IDColumn:    "composeId",
		ID:          testComposeID,
		Name:        "Compose 1",
		OldServerID: testSourceServerID,
		NewServerID: testTargetServerID,
	})
	job := createTestJob(t, ctx, store, plan)

	subsetPlan := plan
	subsetPlan.Rows = []model.PlanRow{plan.Rows[1]}
	if err := manager.Apply(ctx, job.ID, subsetPlan, ApplyOptions{
		SchemaHashApproval: testSchemaHash,
		ConfirmationText:   applyConfirmationText,
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(fakeDB.appliedPlan.Rows) != 1 || fakeDB.appliedPlan.Rows[0].ID != testComposeID {
		t.Fatalf("applied rows = %+v, want only %s", fakeDB.appliedPlan.Rows, testComposeID)
	}
}

func TestApplyRequiresExistingJob(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	fakeDB := &fakeDBAdapter{}
	manager := NewManager(store, fakeDB, nil)

	plan := testPlan()
	err := manager.Apply(ctx, "missing-job", plan, ApplyOptions{
		SchemaHashApproval: testSchemaHash,
		ConfirmationText:   applyConfirmationText,
	})
	if !errors.Is(err, state.ErrJobNotFound) {
		t.Fatalf("Apply() error = %v, want %v", err, state.ErrJobNotFound)
	}
	if fakeDB.appliedApproval != "" {
		t.Fatalf("ApplyRetargetPlan was called with approval %q", fakeDB.appliedApproval)
	}
}

func TestApplyRequiresJobPlanSourceTargetMatch(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	fakeDB := &fakeDBAdapter{}
	manager := NewManager(store, fakeDB, nil)

	plan := testPlan()
	job := createTestJob(t, ctx, store, plan)
	plan.TargetServerID = "other-target"
	err := manager.Apply(ctx, job.ID, plan, ApplyOptions{
		SchemaHashApproval: testSchemaHash,
		ConfirmationText:   applyConfirmationText,
	})
	if err == nil {
		t.Fatal("expected plan/job mismatch error")
	}
	if fakeDB.appliedApproval != "" {
		t.Fatalf("ApplyRetargetPlan was called with approval %q", fakeDB.appliedApproval)
	}
}

func TestApplyRequiresPlanSchemaHash(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	manager := NewManager(store, &fakeDBAdapter{}, nil)

	plan := testPlan()
	job := createTestJob(t, ctx, store, plan)
	plan.SchemaHash = ""
	err := manager.Apply(ctx, job.ID, plan, ApplyOptions{
		SchemaHashApproval: testSchemaHash,
		ConfirmationText:   applyConfirmationText,
	})
	if err == nil {
		t.Fatal("expected missing plan schemaHash error")
	}
}

func TestApplyRequiresStoredDryRunPlan(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	fakeDB := &fakeDBAdapter{}
	manager := NewManager(store, fakeDB, nil)
	plan := testPlan()

	job := state.Job{
		ID:             testJobID,
		SourceServerID: testSourceServerID,
		TargetServerID: testTargetServerID,
		Mode:           string(model.ModeDeadRecovery),
		Status:         state.JobPaused,
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}

	err := manager.Apply(ctx, job.ID, plan, ApplyOptions{
		SchemaHashApproval: testSchemaHash,
		ConfirmationText:   applyConfirmationText,
	})
	if err == nil {
		t.Fatal("expected missing stored dry-run plan error")
	}
	if fakeDB.appliedApproval != "" {
		t.Fatalf("ApplyRetargetPlan was called with approval %q", fakeDB.appliedApproval)
	}
}

func TestApplyRejectsMutatedStoredDryRunPlan(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	fakeDB := &fakeDBAdapter{}
	manager := NewManager(store, fakeDB, nil)
	plan := testPlan()
	job := createTestJob(t, ctx, store, plan)

	mutatedPlan := plan
	mutatedPlan.Rows[0].ID = "other-app"
	err := manager.Apply(ctx, job.ID, mutatedPlan, ApplyOptions{
		SchemaHashApproval: testSchemaHash,
		ConfirmationText:   applyConfirmationText,
	})
	if err == nil {
		t.Fatal("expected stored dry-run plan mismatch error")
	}
	if fakeDB.appliedApproval != "" {
		t.Fatalf("ApplyRetargetPlan was called with approval %q", fakeDB.appliedApproval)
	}
}

func TestApplyRejectsExtraDryRunPlanRow(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	fakeDB := &fakeDBAdapter{}
	manager := NewManager(store, fakeDB, nil)
	plan := testPlan()
	job := createTestJob(t, ctx, store, plan)

	extraPlan := plan
	extraPlan.Rows = append(extraPlan.Rows, model.PlanRow{
		Table:       "compose",
		IDColumn:    "composeId",
		ID:          testComposeID,
		OldServerID: testSourceServerID,
		NewServerID: testTargetServerID,
	})
	err := manager.Apply(ctx, job.ID, extraPlan, ApplyOptions{
		SchemaHashApproval: testSchemaHash,
		ConfirmationText:   applyConfirmationText,
	})
	if err == nil {
		t.Fatal("expected extra row mismatch error")
	}
	if fakeDB.appliedApproval != "" {
		t.Fatalf("ApplyRetargetPlan was called with approval %q", fakeDB.appliedApproval)
	}
}

func TestPlanRejectsUnsupportedModeBeforeCreatingJob(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	manager := NewManager(store, &fakeDBAdapter{}, nil)

	_, _, err := manager.Plan(ctx, testSourceServerID, testTargetServerID, model.MigrationMode("surprise"))
	if err == nil {
		t.Fatal("expected unsupported mode error")
	}
	jobs, listErr := store.ListJobs(ctx, 50)
	if listErr != nil {
		t.Fatalf("ListJobs() error = %v", listErr)
	}
	if len(jobs) != 0 {
		t.Fatalf("jobs created for invalid mode: %+v", jobs)
	}
}

func TestPlanDefaultsEmptyModeToDeadRecovery(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	manager := NewManager(store, &fakeDBAdapter{plan: testPlan()}, nil)

	job, _, err := manager.Plan(ctx, testSourceServerID, testTargetServerID, "")
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if job.Mode != string(model.ModeDeadRecovery) {
		t.Fatalf("job mode = %q, want %q", job.Mode, model.ModeDeadRecovery)
	}
}

func TestPlanTrimsSourceAndTargetBeforeCreatingJob(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	fakeDB := &fakeDBAdapter{plan: testPlan()}
	manager := NewManager(store, fakeDB, nil)

	job, plan, err := manager.Plan(ctx, " "+testSourceServerID+" ", "\t"+testTargetServerID+"\n", "")
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if job.SourceServerID != testSourceServerID || job.TargetServerID != testTargetServerID {
		t.Fatalf("job source/target = %q -> %q, want %q -> %q",
			job.SourceServerID, job.TargetServerID, testSourceServerID, testTargetServerID)
	}
	if plan.SourceServerID != testSourceServerID || plan.TargetServerID != testTargetServerID {
		t.Fatalf("plan source/target = %q -> %q, want %q -> %q",
			plan.SourceServerID, plan.TargetServerID, testSourceServerID, testTargetServerID)
	}
	if fakeDB.planSourceServerID != testSourceServerID || fakeDB.planTargetServerID != testTargetServerID {
		t.Fatalf("adapter source/target = %q -> %q, want %q -> %q",
			fakeDB.planSourceServerID,
			fakeDB.planTargetServerID,
			testSourceServerID,
			testTargetServerID,
		)
	}
}

func testPlan() model.MigrationPlan {
	return model.MigrationPlan{
		ID:             testPlanID,
		SourceServerID: testSourceServerID,
		TargetServerID: testTargetServerID,
		SchemaHash:     testSchemaHash,
		Rows: []model.PlanRow{{
			Table:       testResourceTable,
			IDColumn:    testResourceColumn,
			ID:          testResourceID,
			Name:        "App",
			OldServerID: testSourceServerID,
			NewServerID: testTargetServerID,
		}},
	}
}

func createTestJob(t *testing.T, ctx context.Context, store *state.Store, plan model.MigrationPlan) state.Job {
	t.Helper()
	job := state.Job{
		ID:             testJobID,
		SourceServerID: plan.SourceServerID,
		TargetServerID: plan.TargetServerID,
		Mode:           string(model.ModeDeadRecovery),
		Status:         state.JobPaused,
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if err := store.AppendEvent(ctx, job.ID, "info", dryRunPlanReadyEvent, plan); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	return job
}

func openTestStore(t *testing.T, ctx context.Context) *state.Store {
	t.Helper()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatalf("state.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("store.Close() error = %v", err)
		}
	})
	return store
}

type fakeDBAdapter struct {
	appliedApproval    string
	appliedPlan        model.MigrationPlan
	applyErr           error
	plan               model.MigrationPlan
	planSourceServerID string
	planTargetServerID string
	rolledBackApproval string
	rollbackErr        error
}

func (f *fakeDBAdapter) ListServers(
	_ context.Context,
	_ time.Duration,
) ([]model.Server, error) {
	return []model.Server{{ID: testSourceServerID, Name: "Source", Status: model.ServerUnknown, ResourceCount: 1}}, nil
}

func (f *fakeDBAdapter) BuildRetargetPlan(
	_ context.Context,
	sourceServerID string,
	targetServerID string,
) (model.MigrationPlan, error) {
	f.planSourceServerID = sourceServerID
	f.planTargetServerID = targetServerID
	if f.plan.ID != "" {
		return f.plan, nil
	}
	return model.MigrationPlan{}, nil
}

func (f *fakeDBAdapter) ApplyRetargetPlan(
	_ context.Context,
	plan model.MigrationPlan,
	schemaHashApproval string,
) error {
	f.appliedApproval = schemaHashApproval
	f.appliedPlan = plan
	return f.applyErr
}

func (f *fakeDBAdapter) RollbackRetargetPlan(
	_ context.Context,
	_ model.MigrationPlan,
	schemaHashApproval string,
) error {
	f.rolledBackApproval = schemaHashApproval
	return f.rollbackErr
}
