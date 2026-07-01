// Package jobs orchestrates durable migration workflows.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/assurrussa/dokploymigrator/internal/model"
	"github.com/assurrussa/dokploymigrator/internal/state"
)

const (
	applyConfirmationText = "APPLY"
	dryRunPlanReadyEvent  = "dry-run plan ready"
	eventErrorKey         = "error"
	eventRowsKey          = "rows"
)

var (
	errDokployAPIBaseURLMissing = errors.New("dokploy API base URL is not configured")
	errDokployDeployPathMissing = errors.New("dokploy deploy path is not configured")
	errApplyConfirmationMissing = errors.New("apply requires confirmation text APPLY")
	errDokployDBMissing         = errors.New("dokploy DB adapter is not configured")
	errStateStoreMissing        = errors.New("state store is not configured")
)

// DBAdapter is the Dokploy database contract used by the job manager.
type DBAdapter interface {
	ListServers(ctx context.Context, deadAfter time.Duration) ([]model.Server, error)
	BuildRetargetPlan(ctx context.Context, sourceServerID string, targetServerID string) (model.MigrationPlan, error)
	ApplyRetargetPlan(ctx context.Context, plan model.MigrationPlan, schemaHashApproval string) error
	RollbackRetargetPlan(ctx context.Context, plan model.MigrationPlan, schemaHashApproval string) error
}

// APIClient calls Dokploy HTTP endpoints that vary across Dokploy versions.
type APIClient struct {
	baseURL    string
	token      string
	healthPath string
	deployPath string
	client     *http.Client
}

// NewAPIClient creates a Dokploy API client.
func NewAPIClient(baseURL string, token string, healthPath string, deployPath string) *APIClient {
	return &APIClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		healthPath: normalizeAPIPath(healthPath),
		deployPath: normalizeAPIPath(deployPath),
		client:     &http.Client{Timeout: 20 * time.Second},
	}
}

// Health checks Dokploy API reachability.
func (c *APIClient) Health(ctx context.Context) error {
	if c.baseURL == "" {
		return errDokployAPIBaseURLMissing
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+c.healthPath, nil)
	if err != nil {
		return fmt.Errorf("build health request: %w", err)
	}
	c.authorize(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("call Dokploy health: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("dokploy health returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// Deploy triggers a deploy for a Dokploy resource through a configurable path.
func (c *APIClient) Deploy(ctx context.Context, resourceID string) error {
	if c.baseURL == "" {
		return errDokployAPIBaseURLMissing
	}
	if c.deployPath == "" {
		return errDokployDeployPathMissing
	}
	body := strings.NewReader(fmt.Sprintf(`{"id":%q}`, resourceID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+c.deployPath, body)
	if err != nil {
		return fmt.Errorf("build deploy request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("call Dokploy deploy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("dokploy deploy returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *APIClient) authorize(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

func normalizeAPIPath(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "/") {
		return trimmed
	}
	return "/" + trimmed
}

// Manager coordinates dry-run, apply, rollback, and reports.
type Manager struct {
	store *state.Store
	db    DBAdapter
	api   *APIClient
}

// NewManager creates a job manager.
func NewManager(store *state.Store, db DBAdapter, api *APIClient) *Manager {
	return &Manager{store: store, db: db, api: api}
}

// ListServers returns operator-facing Dokploy servers and attached resource counts.
func (m *Manager) ListServers(ctx context.Context, deadAfter time.Duration) ([]model.Server, error) {
	if m.db == nil {
		return nil, errDokployDBMissing
	}
	return m.db.ListServers(ctx, deadAfter)
}

// ApplyOptions contains operator confirmations for destructive writes.
type ApplyOptions struct {
	SchemaHashApproval string
	ConfirmationText   string
}

// RollbackOptions contains operator confirmations for rollback writes.
type RollbackOptions struct {
	SchemaHashApproval string
}

// Plan creates a durable dry-run job and returns its plan.
func (m *Manager) Plan(
	ctx context.Context,
	sourceServerID string,
	targetServerID string,
	mode model.MigrationMode,
) (state.Job, model.MigrationPlan, error) {
	sourceServerID = strings.TrimSpace(sourceServerID)
	targetServerID = strings.TrimSpace(targetServerID)
	if m.db == nil {
		return state.Job{}, model.MigrationPlan{}, errDokployDBMissing
	}
	if m.store == nil {
		return state.Job{}, model.MigrationPlan{}, errStateStoreMissing
	}
	normalizedMode, ok := model.NormalizeMigrationMode(mode)
	if !ok {
		return state.Job{}, model.MigrationPlan{}, fmt.Errorf("unsupported migration mode %q", mode)
	}
	job := state.Job{
		ID:             fmt.Sprintf("job-%d", time.Now().UTC().UnixNano()),
		SourceServerID: sourceServerID,
		TargetServerID: targetServerID,
		Mode:           string(normalizedMode),
		Status:         state.JobPending,
	}
	if err := m.store.CreateJob(ctx, job); err != nil {
		return state.Job{}, model.MigrationPlan{}, err
	}
	if err := m.store.AppendEvent(
		ctx,
		job.ID,
		"info",
		"job created",
		map[string]string{"mode": string(normalizedMode)},
	); err != nil {
		return state.Job{}, model.MigrationPlan{}, err
	}
	if m.api != nil && m.api.baseURL != "" {
		if err := m.api.Health(ctx); err != nil {
			_ = m.store.AppendEvent(
				ctx,
				job.ID,
				"warn",
				"Dokploy API health failed",
				map[string]string{eventErrorKey: err.Error()},
			)
		} else {
			_ = m.store.AppendEvent(ctx, job.ID, "info", "Dokploy API health passed", map[string]string{})
		}
	}
	plan, err := m.db.BuildRetargetPlan(ctx, sourceServerID, targetServerID)
	if err != nil {
		_ = m.store.UpdateJob(ctx, job.ID, state.JobFailed, "plan")
		_ = m.store.AppendEvent(ctx, job.ID, "error", "plan failed", map[string]string{eventErrorKey: err.Error()})
		return state.Job{}, model.MigrationPlan{}, err
	}
	if err := m.store.UpdateJob(ctx, job.ID, state.JobPaused, "dry-run-ready"); err != nil {
		return state.Job{}, model.MigrationPlan{}, err
	}
	if err := m.store.AppendEvent(ctx, job.ID, "info", dryRunPlanReadyEvent, plan); err != nil {
		return state.Job{}, model.MigrationPlan{}, err
	}
	job.Status = state.JobPaused
	job.Checkpoint = "dry-run-ready"
	return job, plan, nil
}

// Apply applies a reviewed plan and stores a report.
func (m *Manager) Apply(ctx context.Context, jobID string, plan model.MigrationPlan, opts ApplyOptions) error {
	if m.db == nil {
		return errDokployDBMissing
	}
	if opts.ConfirmationText != applyConfirmationText {
		return errApplyConfirmationMissing
	}
	if err := m.validateJobPlan(ctx, jobID, plan); err != nil {
		return err
	}
	if err := m.store.UpdateJob(ctx, jobID, state.JobRunning, "apply-retarget"); err != nil {
		return err
	}
	if err := m.store.AppendEvent(
		ctx,
		jobID,
		"info",
		"applying metadata retarget",
		map[string]int{eventRowsKey: len(plan.Rows)},
	); err != nil {
		return err
	}
	if err := m.db.ApplyRetargetPlan(ctx, plan, opts.SchemaHashApproval); err != nil {
		_ = m.store.UpdateJob(ctx, jobID, state.JobFailed, "apply-retarget")
		_ = m.store.AppendEvent(
			ctx,
			jobID,
			"error",
			"metadata retarget failed",
			map[string]string{eventErrorKey: err.Error()},
		)
		return err
	}
	reportJSON, reportMarkdown, err := buildReport(jobID, plan, "metadata retarget applied")
	if err != nil {
		return err
	}
	if err := m.store.SaveReport(ctx, jobID, reportJSON, reportMarkdown); err != nil {
		return err
	}
	if err := m.store.AppendEvent(ctx, jobID, "info", "report saved", map[string]string{}); err != nil {
		return err
	}
	return m.store.UpdateJob(ctx, jobID, state.JobSucceeded, "metadata-retarget-complete")
}

// Rollback reverses metadata changes from a reviewed plan.
func (m *Manager) Rollback(
	ctx context.Context,
	jobID string,
	plan model.MigrationPlan,
	opts RollbackOptions,
) error {
	if m.db == nil {
		return errDokployDBMissing
	}
	if err := m.validateJobPlan(ctx, jobID, plan); err != nil {
		return err
	}
	if err := m.store.UpdateJob(ctx, jobID, state.JobRunning, "rollback-retarget"); err != nil {
		return err
	}
	if err := m.store.AppendEvent(
		ctx,
		jobID,
		"info",
		"applying metadata rollback",
		map[string]int{eventRowsKey: len(plan.Rows)},
	); err != nil {
		return err
	}
	if err := m.db.RollbackRetargetPlan(ctx, plan, opts.SchemaHashApproval); err != nil {
		_ = m.store.UpdateJob(ctx, jobID, state.JobFailed, "rollback-retarget")
		_ = m.store.AppendEvent(
			ctx,
			jobID,
			"error",
			"metadata rollback failed",
			map[string]string{eventErrorKey: err.Error()},
		)
		return err
	}
	if err := m.store.AppendEvent(
		ctx,
		jobID,
		"info",
		"metadata rollback applied",
		map[string]int{eventRowsKey: len(plan.Rows)},
	); err != nil {
		return err
	}
	return m.store.UpdateJob(ctx, jobID, state.JobRolledBack, "metadata-rollback-complete")
}

func (m *Manager) validateJobPlan(ctx context.Context, jobID string, plan model.MigrationPlan) error {
	if m.store == nil {
		return errStateStoreMissing
	}
	if strings.TrimSpace(jobID) == "" {
		return errors.New("job ID is required")
	}
	if strings.TrimSpace(plan.ID) == "" {
		return errors.New("dry-run plan ID is required")
	}
	if strings.TrimSpace(plan.SchemaHash) == "" {
		return errors.New("dry-run plan schemaHash is required")
	}
	job, err := m.store.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job.SourceServerID != plan.SourceServerID || job.TargetServerID != plan.TargetServerID {
		return fmt.Errorf(
			"plan source/target does not match job %s: plan %s -> %s, job %s -> %s",
			jobID,
			plan.SourceServerID,
			plan.TargetServerID,
			job.SourceServerID,
			job.TargetServerID,
		)
	}
	return m.validateStoredDryRunPlan(ctx, jobID, plan)
}

func (m *Manager) validateStoredDryRunPlan(ctx context.Context, jobID string, plan model.MigrationPlan) error {
	events, err := m.store.ListEvents(ctx, jobID)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.Message != dryRunPlanReadyEvent {
			continue
		}
		var storedPlan model.MigrationPlan
		if err := json.Unmarshal(event.Payload, &storedPlan); err != nil {
			return fmt.Errorf("decode stored dry-run plan for job %s: %w", jobID, err)
		}
		if storedPlan.ID != plan.ID {
			continue
		}
		if err := validatePlanSubset(storedPlan, plan); err != nil {
			return fmt.Errorf("submitted plan %s does not match stored dry-run plan for job %s: %w", plan.ID, jobID, err)
		}
		return nil
	}
	return fmt.Errorf("stored dry-run plan %s was not found for job %s", plan.ID, jobID)
}

func validatePlanSubset(storedPlan model.MigrationPlan, submittedPlan model.MigrationPlan) error {
	if storedPlan.SourceServerID != submittedPlan.SourceServerID ||
		storedPlan.TargetServerID != submittedPlan.TargetServerID ||
		storedPlan.SchemaHash != submittedPlan.SchemaHash {
		return errors.New("plan source, target, or schemaHash changed")
	}
	if len(submittedPlan.Rows) == 0 {
		return errors.New("selected plan rows are required")
	}
	storedRows := make(map[planRowIdentity]model.PlanRow, len(storedPlan.Rows))
	for _, row := range storedPlan.Rows {
		identity := planRowIdentityFor(row)
		if identity.empty() {
			return errors.New("stored dry-run plan contains a row without table, idColumn, id, oldServerId, or newServerId")
		}
		if _, exists := storedRows[identity]; exists {
			return fmt.Errorf("stored dry-run plan contains duplicate row %s/%s", row.Table, row.ID)
		}
		storedRows[identity] = row
	}

	submittedRows := make(map[planRowIdentity]struct{}, len(submittedPlan.Rows))
	for _, row := range submittedPlan.Rows {
		identity := planRowIdentityFor(row)
		if identity.empty() {
			return errors.New("submitted plan contains a row without table, idColumn, id, oldServerId, or newServerId")
		}
		if _, exists := submittedRows[identity]; exists {
			return fmt.Errorf("submitted plan contains duplicate row %s/%s", row.Table, row.ID)
		}
		storedRow, exists := storedRows[identity]
		if !exists || storedRow != row {
			return fmt.Errorf("submitted row %s/%s is not part of the stored dry-run plan", row.Table, row.ID)
		}
		submittedRows[identity] = struct{}{}
	}
	return nil
}

type planRowIdentity struct {
	table       string
	idColumn    string
	id          string
	oldServerID string
	newServerID string
}

func planRowIdentityFor(row model.PlanRow) planRowIdentity {
	return planRowIdentity{
		table:       strings.TrimSpace(row.Table),
		idColumn:    strings.TrimSpace(row.IDColumn),
		id:          strings.TrimSpace(row.ID),
		oldServerID: strings.TrimSpace(row.OldServerID),
		newServerID: strings.TrimSpace(row.NewServerID),
	}
}

func (identity planRowIdentity) empty() bool {
	return identity.table == "" ||
		identity.idColumn == "" ||
		identity.id == "" ||
		identity.oldServerID == "" ||
		identity.newServerID == ""
}

func buildReport(jobID string, plan model.MigrationPlan, status string) (jsonReport string, markdownReport string, err error) {
	body := map[string]any{
		"jobId":  jobID,
		"status": status,
		"plan":   plan,
	}
	encoded, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("marshal report: %w", err)
	}
	markdown := fmt.Sprintf(
		"# Dokploy Migrator Report\n\n"+
			"- Job: `%s`\n"+
			"- Status: %s\n"+
			"- Source: `%s`\n"+
			"- Target: `%s`\n"+
			"- Rows changed: %d\n"+
			"- Schema: `%s`\n",
		jobID, status, plan.SourceServerID, plan.TargetServerID, len(plan.Rows), plan.SchemaHash)
	return string(encoded), markdown, nil
}
