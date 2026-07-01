package dokploydb

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	// Register SQLite for scanResourceCounts regression tests.
	_ "modernc.org/sqlite"

	"github.com/assurrussa/dokploymigrator/internal/model"
)

const (
	testAppID          = "app-1"
	testComposeID      = "compose-1"
	testPlanID         = "plan-1"
	testSchemaHash     = "hash"
	testSourceServerID = "source"
	testTargetServerID = "target"
)

func TestIsRetargetTable(t *testing.T) {
	tests := []struct {
		name  string
		table string
		want  bool
	}{
		{name: "application allowed", table: "application", want: true},
		{name: "domain allowed", table: "domain", want: true},
		{name: "server forbidden", table: "server", want: false},
		{name: "injection forbidden", table: "application;drop", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetargetTable(tt.table); got != tt.want {
				t.Fatalf("isRetargetTable(%q) = %v, want %v", tt.table, got, tt.want)
			}
		})
	}
}

func TestQuoteIdentifier(t *testing.T) {
	if got := quoteIdentifier(`weird"name`); got != `"weird""name"` {
		t.Fatalf("quoteIdentifier() = %s", got)
	}
}

func TestResourceIDColumnUsesDokploySpecificIDs(t *testing.T) {
	columns := map[string]struct{}{applicationIDColumn: {}, "serverId": {}}
	got, ok := resourceIDColumn("application", columns)
	if !ok {
		t.Fatalf("resourceIDColumn() did not find %s", applicationIDColumn)
	}
	if got != applicationIDColumn {
		t.Fatalf("resourceIDColumn() = %s, want %s", got, applicationIDColumn)
	}
}

func TestResourceIDColumnSupportsMongoTable(t *testing.T) {
	columns := map[string]struct{}{mongoIDColumn: {}, "serverId": {}}
	got, ok := resourceIDColumn("mongo", columns)
	if !ok {
		t.Fatalf("resourceIDColumn() did not find %s", mongoIDColumn)
	}
	if got != mongoIDColumn {
		t.Fatalf("resourceIDColumn() = %s, want %s", got, mongoIDColumn)
	}
}

func TestValidatedIDColumnRejectsWrongColumn(t *testing.T) {
	if _, err := validatedIDColumn("application", "serverId"); err == nil {
		t.Fatal("expected invalid id column error")
	}
}

func TestValidateWriteSchemaAllowsExplicitApproval(t *testing.T) {
	if err := validateWriteSchema(testSchemaHash, testSchemaHash, testSchemaHash, nil); err != nil {
		t.Fatalf("validateWriteSchema() error = %v", err)
	}
}

func TestValidateWriteSchemaRequiresPlanHash(t *testing.T) {
	if err := validateWriteSchema(testSchemaHash, "", testSchemaHash, nil); err == nil {
		t.Fatal("expected missing plan schema hash error")
	}
}

func TestValidateWriteSchemaRejectsWrongApproval(t *testing.T) {
	if err := validateWriteSchema(testSchemaHash, testSchemaHash, "other", nil); err == nil {
		t.Fatal("expected schema approval mismatch")
	}
}

func TestValidateWriteSchemaAllowsEnvAllowlistFallback(t *testing.T) {
	allowed := map[string]struct{}{testSchemaHash: {}}
	if err := validateWriteSchema(testSchemaHash, testSchemaHash, "", allowed); err != nil {
		t.Fatalf("validateWriteSchema() error = %v", err)
	}
}

func TestValidateWriteSchemaRequiresApprovalOrAllowlist(t *testing.T) {
	if err := validateWriteSchema(testSchemaHash, testSchemaHash, "", nil); err == nil {
		t.Fatal("expected missing schema approval error")
	}
}

func TestClassifyServerStatusFromRawStatus(t *testing.T) {
	now := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		raw  string
		want model.ServerStatus
	}{
		{raw: "online", want: model.ServerOnline},
		{raw: "connected", want: model.ServerOnline},
		{raw: "offline", want: model.ServerOffline},
		{raw: "unreachable", want: model.ServerOffline},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got := classifyServerStatus(tt.raw, nil, 10*time.Minute, now)
			if got != tt.want {
				t.Fatalf("classifyServerStatus(%q) = %s, want %s", tt.raw, got, tt.want)
			}
		})
	}
}

func TestClassifyServerStatusFromLastSeen(t *testing.T) {
	now := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	recent := now.Add(-2 * time.Minute)
	stale := now.Add(-20 * time.Minute)

	if got := classifyServerStatus("", &recent, 10*time.Minute, now); got != model.ServerOnline {
		t.Fatalf("recent last seen status = %s, want %s", got, model.ServerOnline)
	}
	if got := classifyServerStatus("", &stale, 10*time.Minute, now); got != model.ServerOffline {
		t.Fatalf("stale last seen status = %s, want %s", got, model.ServerOffline)
	}
	if got := classifyServerStatus("", nil, 10*time.Minute, now); got != model.ServerUnknown {
		t.Fatalf("missing last seen status = %s, want %s", got, model.ServerUnknown)
	}
}

func TestFirstTemporalColumnSkipsNonTemporalColumns(t *testing.T) {
	columns := map[string]columnInfo{
		lastSeenAtColumn: {Name: lastSeenAtColumn, DataType: "timestamp with time zone"},
		statusColumn:     {Name: statusColumn, DataType: "text"},
	}
	got := firstTemporalColumn(columns, []string{statusColumn, lastSeenAtColumn})
	if got != lastSeenAtColumn {
		t.Fatalf("firstTemporalColumn() = %q, want %s", got, lastSeenAtColumn)
	}
}

func TestFirstTemporalColumnDoesNotTreatUpdatedAtAsHealthSignal(t *testing.T) {
	columns := map[string]columnInfo{
		"updatedAt": {Name: "updatedAt", DataType: "timestamp with time zone"},
	}
	got := firstTemporalColumn(columns, []string{lastSeenAtColumn, "lastHeartbeatAt"})
	if got != "" {
		t.Fatalf("firstTemporalColumn() = %q, want empty", got)
	}
}

func TestScanResourceCountsMapsNullServerIDToLocalServer(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT NULL AS serverId, 2 AS count
		UNION ALL SELECT 'server-1', 3
		UNION ALL SELECT '', 4
	`)
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}

	counts := make(map[string]int)
	if err := scanResourceCounts(rows, "compose", counts); err != nil {
		t.Fatalf("scanResourceCounts() error = %v", err)
	}
	if got := counts[model.LocalServerID]; got != 2 {
		t.Fatalf("local server count = %d, want 2", got)
	}
	if got := counts["server-1"]; got != 3 {
		t.Fatalf("server-1 count = %d, want 3", got)
	}
	if _, ok := counts[""]; ok {
		t.Fatal("empty server ID should not be counted")
	}
}

func TestValidateRetargetPlanForWriteRequiresConsistentRows(t *testing.T) {
	plan := model.MigrationPlan{
		ID:             testPlanID,
		SourceServerID: testSourceServerID,
		TargetServerID: testTargetServerID,
		SchemaHash:     testSchemaHash,
		Rows: []model.PlanRow{{
			Table:       string(model.ResourceApplication),
			ID:          testAppID,
			OldServerID: testSourceServerID,
			NewServerID: "other-target",
		}},
	}

	if err := validateRetargetPlanForWrite(plan); err == nil {
		t.Fatal("expected inconsistent row error")
	}
}

func TestValidateRetargetPlanForWriteAcceptsRollbackDirection(t *testing.T) {
	plan := model.MigrationPlan{
		ID:             testPlanID,
		SourceServerID: testTargetServerID,
		TargetServerID: testSourceServerID,
		SchemaHash:     testSchemaHash,
		Rows: []model.PlanRow{{
			Table:       string(model.ResourceApplication),
			ID:          testAppID,
			OldServerID: testTargetServerID,
			NewServerID: testSourceServerID,
		}},
	}

	if err := validateRetargetPlanForWrite(plan); err != nil {
		t.Fatalf("validateRetargetPlanForWrite() error = %v", err)
	}
}

func TestValidateRetargetPlanForWriteAcceptsLocalServerSource(t *testing.T) {
	plan := model.MigrationPlan{
		ID:             testPlanID,
		SourceServerID: model.LocalServerID,
		TargetServerID: testTargetServerID,
		SchemaHash:     testSchemaHash,
		Rows: []model.PlanRow{{
			Table:       string(model.ResourceCompose),
			IDColumn:    "composeId",
			ID:          testComposeID,
			OldServerID: model.LocalServerID,
			NewServerID: testTargetServerID,
		}},
	}

	if err := validateRetargetPlanForWrite(plan); err != nil {
		t.Fatalf("validateRetargetPlanForWrite() error = %v", err)
	}
}

func TestRetargetSQLFromLocalServerUsesNullPredicate(t *testing.T) {
	query, args := retargetSQL(model.PlanRow{
		Table:       string(model.ResourceCompose),
		ID:          testComposeID,
		OldServerID: model.LocalServerID,
		NewServerID: testTargetServerID,
	}, "composeId")

	if !strings.Contains(query, `SET "serverId" = $1`) || !strings.Contains(query, `"serverId" IS NULL`) {
		t.Fatalf("retargetSQL() query = %s, want NULL old-server predicate", query)
	}
	if len(args) != 2 || args[0] != testTargetServerID || args[1] != testComposeID {
		t.Fatalf("retargetSQL() args = %#v, want target server and resource ID", args)
	}
}

func TestRetargetSQLToLocalServerSetsServerIDNull(t *testing.T) {
	query, args := retargetSQL(model.PlanRow{
		Table:       string(model.ResourceCompose),
		ID:          testComposeID,
		OldServerID: testTargetServerID,
		NewServerID: model.LocalServerID,
	}, "composeId")

	if !strings.Contains(query, `SET "serverId" = NULL`) || !strings.Contains(query, `"serverId" = $2`) {
		t.Fatalf("retargetSQL() query = %s, want NULL assignment and old-server guard", query)
	}
	if len(args) != 2 || args[0] != testComposeID || args[1] != testTargetServerID {
		t.Fatalf("retargetSQL() args = %#v, want resource ID and old server", args)
	}
}
