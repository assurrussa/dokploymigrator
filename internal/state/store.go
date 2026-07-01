// Package state stores durable Migrator jobs and audit events.
package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	// Register modernc SQLite as a database/sql driver for the Migrator-owned state store.
	_ "modernc.org/sqlite"
)

// Store wraps the Migrator-owned SQLite database.
type Store struct {
	db *sql.DB
}

// ErrJobNotFound is returned when a durable job ID does not exist.
var ErrJobNotFound = errors.New("job not found")

// JobStatus is a durable migration lifecycle state.
type JobStatus string

const (
	JobPending    JobStatus = "pending"
	JobRunning    JobStatus = "running"
	JobPaused     JobStatus = "paused"
	JobSucceeded  JobStatus = "succeeded"
	JobFailed     JobStatus = "failed"
	JobRolledBack JobStatus = "rolled_back"
)

const (
	selectJobColumns = `SELECT id, source_server_id, target_server_id, mode, status, checkpoint, created_at, updated_at`
	defaultJobLimit  = 50
	selectEventsSQL  = `SELECT id, job_id, level, message, payload, created_at
		FROM events
		WHERE job_id = ?
		ORDER BY id ASC`
	upsertReportSQL = `INSERT INTO reports (job_id, json_report, markdown_report, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(job_id) DO UPDATE SET
			json_report = excluded.json_report,
			markdown_report = excluded.markdown_report,
			created_at = excluded.created_at`
)

// Job is a durable migration job.
type Job struct {
	ID             string    `json:"id"`
	SourceServerID string    `json:"sourceServerId"`
	TargetServerID string    `json:"targetServerId"`
	Mode           string    `json:"mode"`
	Status         JobStatus `json:"status"`
	Checkpoint     string    `json:"checkpoint"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// Event is an append-only job event.
type Event struct {
	ID        int64           `json:"id"`
	JobID     string          `json:"jobId"`
	Level     string          `json:"level"`
	Message   string          `json:"message"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

// Open opens and migrates the state database.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite state: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			source_server_id TEXT NOT NULL,
			target_server_id TEXT NOT NULL,
			mode TEXT NOT NULL,
			status TEXT NOT NULL,
			checkpoint TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id TEXT NOT NULL,
			level TEXT NOT NULL,
			message TEXT NOT NULL,
			payload TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			FOREIGN KEY(job_id) REFERENCES jobs(id)
		)`,
		`CREATE TABLE IF NOT EXISTS reports (
			job_id TEXT PRIMARY KEY,
			json_report TEXT NOT NULL,
			markdown_report TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(job_id) REFERENCES jobs(id)
		)`,
	}
	for _, stmt := range statements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate state: %w", err)
		}
	}
	return nil
}

// CreateJob inserts a new migration job.
func (s *Store) CreateJob(ctx context.Context, job Job) error {
	now := time.Now().UTC()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	job.UpdatedAt = now
	if job.Status == "" {
		job.Status = JobPending
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO jobs
		(id, source_server_id, target_server_id, mode, status, checkpoint, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.SourceServerID, job.TargetServerID, job.Mode, job.Status, job.Checkpoint,
		formatTime(job.CreatedAt), formatTime(job.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("create job %s: %w", job.ID, err)
	}
	return nil
}

// UpdateJob sets job status and checkpoint.
func (s *Store) UpdateJob(ctx context.Context, id string, status JobStatus, checkpoint string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = ?, checkpoint = ?, updated_at = ? WHERE id = ?`,
		status, checkpoint, formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("update job %s: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update job %s rows affected: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: %s", ErrJobNotFound, id)
	}
	return nil
}

// GetJob returns one job by ID.
func (s *Store) GetJob(ctx context.Context, id string) (Job, error) {
	row := s.db.QueryRowContext(ctx, selectJobColumns+` FROM jobs WHERE id = ?`, id)
	var job Job
	var created, updated string
	if err := scanJob(row, &job, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Job{}, fmt.Errorf("%w: %s", ErrJobNotFound, id)
		}
		return Job{}, fmt.Errorf("get job %s: %w", id, err)
	}
	job.CreatedAt = parseTime(created)
	job.UpdatedAt = parseTime(updated)
	return job, nil
}

// ListJobs returns recent jobs.
func (s *Store) ListJobs(ctx context.Context, limit int) ([]Job, error) {
	return s.ListJobsPage(ctx, limit, 0)
}

// ListJobsPage returns jobs in deterministic newest-first order.
func (s *Store) ListJobsPage(ctx context.Context, limit int, offset int) ([]Job, error) {
	if limit <= 0 {
		limit = defaultJobLimit
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.QueryContext(
		ctx,
		selectJobColumns+` FROM jobs ORDER BY updated_at DESC, id DESC LIMIT ? OFFSET ?`,
		limit,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	jobs := make([]Job, 0)
	for rows.Next() {
		var job Job
		var created, updated string
		if err := scanJob(rows, &job, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		job.CreatedAt = parseTime(created)
		job.UpdatedAt = parseTime(updated)
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate jobs: %w", err)
	}
	return jobs, nil
}

// CountJobs returns the number of durable jobs.
func (s *Store) CountJobs(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count jobs: %w", err)
	}
	return count, nil
}

// ProtectedJobIDs returns the newest job IDs that should be retained.
func (s *Store) ProtectedJobIDs(ctx context.Context, limit int) (map[string]struct{}, error) {
	jobs, err := s.ListJobs(ctx, limit)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(jobs))
	for _, job := range jobs {
		ids[job.ID] = struct{}{}
	}
	return ids, nil
}

// DeleteJob deletes a job and its Migrator-owned audit artifacts.
func (s *Store) DeleteJob(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete job %s: %w", id, err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `DELETE FROM reports WHERE job_id = ?`, id); err != nil {
		return fmt.Errorf("delete job %s report: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE job_id = ?`, id); err != nil {
		return fmt.Errorf("delete job %s events: %w", id, err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete job %s: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete job %s rows affected: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: %s", ErrJobNotFound, id)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete job %s: %w", id, err)
	}
	return nil
}

// AppendEvent writes an audit event.
func (s *Store) AppendEvent(ctx context.Context, jobID string, level string, message string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal event payload: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO events (job_id, level, message, payload, created_at) VALUES (?, ?, ?, ?, ?)`,
		jobID, level, message, string(body), formatTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}

// ListEvents returns events for one job.
func (s *Store) ListEvents(ctx context.Context, jobID string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, selectEventsSQL, jobID)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var event Event
		var payload string
		var created string
		if err := rows.Scan(&event.ID, &event.JobID, &event.Level, &event.Message, &payload, &created); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		event.Payload = json.RawMessage(payload)
		event.CreatedAt = parseTime(created)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return events, nil
}

// SaveReport stores final migration reports.
func (s *Store) SaveReport(ctx context.Context, jobID string, jsonReport string, markdownReport string) error {
	_, err := s.db.ExecContext(ctx, upsertReportSQL, jobID, jsonReport, markdownReport, formatTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("save report: %w", err)
	}
	return nil
}

type jobScanner interface {
	Scan(dest ...any) error
}

func scanJob(scanner jobScanner, job *Job, created *string, updated *string) error {
	return scanner.Scan(
		&job.ID,
		&job.SourceServerID,
		&job.TargetServerID,
		&job.Mode,
		&job.Status,
		&job.Checkpoint,
		created,
		updated,
	)
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
