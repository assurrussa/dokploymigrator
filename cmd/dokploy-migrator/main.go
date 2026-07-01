package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/assurrussa/dokploymigrator/internal/config"
	"github.com/assurrussa/dokploymigrator/internal/dokploydb"
	"github.com/assurrussa/dokploymigrator/internal/jobs"
	"github.com/assurrussa/dokploymigrator/internal/model"
	"github.com/assurrussa/dokploymigrator/internal/server"
	"github.com/assurrussa/dokploymigrator/internal/state"
)

var errApplyConfirmationRequired = errors.New("apply requires -confirm APPLY")

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case "serve":
		return serve(ctx, os.Args[2:])
	case "plan":
		return plan(ctx, os.Args[2:])
	case "apply":
		return apply(ctx, os.Args[2:])
	case "rollback":
		return rollback(ctx, os.Args[2:])
	case "jobs":
		return listJobs(ctx, os.Args[2:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func serve(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := flags.String("addr", "", "HTTP listen address")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *addr != "" {
		cfg.Addr = *addr
	}
	store, manager, cleanup, err := buildRuntime(ctx, cfg, false)
	if err != nil {
		return err
	}
	defer cleanup()

	srv := server.New(server.Config{
		BasicUser:     cfg.BasicAuthUser,
		BasicPassword: cfg.BasicAuthPassword,
		AdminToken:    cfg.AdminToken,
		DeadAfter:     cfg.DeadAfter,
	}, store, manager)
	log.Printf("Dokploy Migrator listening on %s", cfg.Addr)
	return server.ListenAndServe(ctx, cfg.Addr, srv.Handler())
}

func plan(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("plan", flag.ExitOnError)
	source := flags.String("source", "", "source server ID")
	target := flags.String("target", "", "target server ID")
	mode := flags.String("mode", string(model.ModeDeadRecovery), "migration mode")
	out := flags.String("out", "", "optional output file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	_, manager, cleanup, err := buildRuntime(ctx, cfg, true)
	if err != nil {
		return err
	}
	defer cleanup()

	job, migrationPlan, err := manager.Plan(ctx, *source, *target, model.MigrationMode(*mode))
	if err != nil {
		return err
	}
	payload := planOutput{Job: job, Plan: migrationPlan}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal plan: %w", err)
	}
	if *out != "" {
		if err := os.WriteFile(*out, encoded, 0o600); err != nil {
			return fmt.Errorf("write plan file: %w", err)
		}
		return nil
	}
	_, _ = os.Stdout.Write(append(encoded, '\n'))
	return nil
}

func apply(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("apply", flag.ExitOnError)
	jobID := flags.String("job", "", "job ID")
	planPath := flags.String("plan", "", "plan JSON file")
	schemaHashApproval := flags.String(
		"schema-hash-approval",
		"",
		"explicit schema hash approval; omit only when MIGRATOR_SCHEMA_ALLOWLIST contains the plan hash",
	)
	confirmation := flags.String("confirm", "", "must be exactly APPLY")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *jobID == "" || *planPath == "" {
		return errors.New("apply requires -job and -plan")
	}
	if *confirmation != "APPLY" {
		return errApplyConfirmationRequired
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	_, manager, cleanup, err := buildRuntime(ctx, cfg, true)
	if err != nil {
		return err
	}
	defer cleanup()
	migrationPlan, err := readPlan(*planPath)
	if err != nil {
		return err
	}
	return manager.Apply(ctx, *jobID, migrationPlan, jobs.ApplyOptions{
		SchemaHashApproval: *schemaHashApproval,
		ConfirmationText:   *confirmation,
	})
}

func rollback(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("rollback", flag.ExitOnError)
	jobID := flags.String("job", "", "job ID")
	planPath := flags.String("plan", "", "plan JSON file")
	schemaHashApproval := flags.String(
		"schema-hash-approval",
		"",
		"explicit schema hash approval; omit only when MIGRATOR_SCHEMA_ALLOWLIST contains the plan hash",
	)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *jobID == "" || *planPath == "" {
		return errors.New("rollback requires -job and -plan")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	_, manager, cleanup, err := buildRuntime(ctx, cfg, true)
	if err != nil {
		return err
	}
	defer cleanup()
	migrationPlan, err := readPlan(*planPath)
	if err != nil {
		return err
	}
	return manager.Rollback(ctx, *jobID, migrationPlan, jobs.RollbackOptions{
		SchemaHashApproval: *schemaHashApproval,
	})
}

func listJobs(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("jobs", flag.ExitOnError)
	statePath := flags.String("state", "", "state database path")
	limit := flags.Int("limit", 50, "maximum jobs to list")
	offset := flags.Int("offset", 0, "jobs to skip in newest-first order")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *statePath != "" {
		cfg.StatePath = *statePath
	}
	store, err := state.Open(ctx, cfg.StatePath)
	if err != nil {
		return err
	}
	defer store.Close()
	jobs, err := store.ListJobsPage(ctx, *limit, *offset)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	_, _ = os.Stdout.Write(append(encoded, '\n'))
	return nil
}

type planOutput struct {
	Job  state.Job           `json:"job"`
	Plan model.MigrationPlan `json:"plan"`
}

func buildRuntime(ctx context.Context, cfg config.Config, requireDB bool) (*state.Store, *jobs.Manager, func(), error) {
	store, err := state.Open(ctx, cfg.StatePath)
	if err != nil {
		return nil, nil, nil, err
	}
	cleanup := func() {
		_ = store.Close()
	}
	if err := cfg.RequireDokployDB(); err != nil {
		if requireDB {
			cleanup()
			return nil, nil, nil, err
		}
		api := jobs.NewAPIClient(cfg.DokployAPIBaseURL, cfg.DokployAPIToken, cfg.HealthPath, cfg.DeployPath)
		manager := jobs.NewManager(store, nil, api)
		log.Printf("Dokploy DB is not configured: %v; UI will start, migration actions are disabled", err)
		return store, manager, cleanup, nil
	}
	db, err := dokploydb.OpenPostgres(cfg.DokployPostgresDSN, cfg.SchemaAllowlist)
	if err != nil {
		cleanup()
		return nil, nil, nil, err
	}
	cleanup = func() {
		_ = db.Close()
		_ = store.Close()
	}
	api := jobs.NewAPIClient(cfg.DokployAPIBaseURL, cfg.DokployAPIToken, cfg.HealthPath, cfg.DeployPath)
	manager := jobs.NewManager(store, db, api)
	return store, manager, cleanup, nil
}

func readPlan(path string) (model.MigrationPlan, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return model.MigrationPlan{}, fmt.Errorf("read plan file: %w", err)
	}
	var direct model.MigrationPlan
	if err := json.Unmarshal(body, &direct); err == nil && direct.ID != "" {
		return direct, nil
	}
	var wrapped struct {
		Plan model.MigrationPlan `json:"plan"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return model.MigrationPlan{}, fmt.Errorf("decode plan file: %w", err)
	}
	if wrapped.Plan.ID == "" {
		return model.MigrationPlan{}, errors.New("plan file does not contain a plan")
	}
	return wrapped.Plan, nil
}

func printUsage() {
	_, _ = io.WriteString(os.Stdout, `Usage:
  dokploy-migrator serve [-addr :8080]
  dokploy-migrator plan -source SOURCE_ID -target TARGET_ID [-mode dead_recovery] [-out plan.json]
  dokploy-migrator apply -job JOB_ID -plan plan.json -schema-hash-approval HASH -confirm APPLY
  dokploy-migrator rollback -job JOB_ID -plan plan.json -schema-hash-approval HASH
  dokploy-migrator jobs [-limit 50] [-offset 0]

Required environment:
  MIGRATOR_BASIC_USER
  MIGRATOR_BASIC_PASSWORD
  MIGRATOR_ADMIN_TOKEN
  DOKPLOY_POSTGRES_DSN or DATABASE_URL

Optional environment:
  MIGRATOR_ADDR
  MIGRATOR_STATE_PATH
  MIGRATOR_SCHEMA_ALLOWLIST
  DOKPLOY_API_BASE_URL
  DOKPLOY_API_TOKEN
  DOKPLOY_HEALTH_PATH
  DOKPLOY_DEPLOY_PATH
`)
}
