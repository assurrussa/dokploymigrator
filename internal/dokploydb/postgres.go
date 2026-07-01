// Package dokploydb contains the guarded Dokploy database adapter.
package dokploydb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	// Register pgx as a database/sql driver for the Dokploy PostgreSQL adapter.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/assurrussa/dokploymigrator/internal/model"
)

var retargetTables = []string{
	string(model.ResourceApplication),
	string(model.ResourceCompose),
	string(model.ResourcePostgres),
	string(model.ResourceMySQL),
	string(model.ResourceMariaDB),
	string(model.ResourceMongo),
	string(model.ResourceRedis),
	string(model.ResourceLibSQL),
	string(model.ResourceDomain),
}

const (
	applicationIDColumn = "applicationId"
	mongoIDColumn       = "mongoId"
	lastSeenAtColumn    = "lastSeenAt"
	serverTable         = "server"
	statusColumn        = "status"
)

// Adapter performs guarded reads and writes against Dokploy PostgreSQL.
type Adapter struct {
	db             *sql.DB
	allowedHashes  map[string]struct{}
	requiredTables []string
}

// OpenPostgres opens a Dokploy PostgreSQL adapter.
func OpenPostgres(dsn string, allowedHashes []string) (*Adapter, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open dokploy postgres: %w", err)
	}
	allowed := make(map[string]struct{}, len(allowedHashes))
	for _, hash := range allowedHashes {
		if trimmed := strings.TrimSpace(hash); trimmed != "" {
			allowed[trimmed] = struct{}{}
		}
	}
	return &Adapter{db: db, allowedHashes: allowed, requiredTables: retargetTables}, nil
}

// Close closes the adapter.
func (a *Adapter) Close() error {
	return a.db.Close()
}

// SchemaFingerprint returns a stable hash of supported Dokploy table columns.
func (a *Adapter) SchemaFingerprint(ctx context.Context) (string, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT table_name, column_name, data_type
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = ANY($1)
		ORDER BY table_name, ordinal_position
	`, a.requiredTables)
	if err != nil {
		return "", fmt.Errorf("query schema fingerprint: %w", err)
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var tableName, columnName, dataType string
		if err := rows.Scan(&tableName, &columnName, &dataType); err != nil {
			return "", fmt.Errorf("scan schema fingerprint: %w", err)
		}
		parts = append(parts, tableName+"."+columnName+":"+dataType)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate schema fingerprint: %w", err)
	}
	if len(parts) == 0 {
		return "", errors.New("no supported Dokploy tables found")
	}

	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

// ValidateSchema refuses writes when schema allowlist is configured and current
// fingerprint is not in that allowlist.
func (a *Adapter) ValidateSchema(ctx context.Context) (string, error) {
	hash, err := a.SchemaFingerprint(ctx)
	if err != nil {
		return "", err
	}
	if len(a.allowedHashes) == 0 {
		return hash, nil
	}
	if _, ok := a.allowedHashes[hash]; !ok {
		return hash, fmt.Errorf("schema fingerprint %s is not allowlisted", hash)
	}
	return hash, nil
}

// ListServers returns Dokploy servers with best-effort status and resource counts.
func (a *Adapter) ListServers(ctx context.Context, deadAfter time.Duration) ([]model.Server, error) {
	columns, err := a.tableColumns(ctx, serverTable)
	if err != nil {
		return nil, err
	}
	if _, ok := columns["serverId"]; !ok {
		return nil, errors.New(`dokploy "server" table does not have "serverId" column`)
	}

	nameExpr := `''::text`
	if _, ok := columns["name"]; ok {
		nameExpr = `COALESCE("name"::text, '')`
	}
	statusExpr := `NULL::text`
	if selectedStatusColumn := firstExistingColumn(columns, []string{statusColumn, "state"}); selectedStatusColumn != "" {
		statusExpr = quoteIdentifier(selectedStatusColumn) + `::text`
	}
	lastSeenExpr := `NULL::timestamptz`
	if lastSeenColumn := firstTemporalColumn(columns, []string{
		lastSeenAtColumn,
		"lastSeen",
		"lastOnlineAt",
		"lastPingAt",
		"lastHeartbeatAt",
		"lastHealthCheckAt",
		"lastHealthAt",
	}); lastSeenColumn != "" {
		lastSeenExpr = quoteIdentifier(lastSeenColumn)
	}

	resourceCounts, err := a.resourceCountsByServer(ctx)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(
		`SELECT "serverId"::text, %s, %s, %s FROM "server" ORDER BY 2, 1`,
		nameExpr,
		statusExpr,
		lastSeenExpr,
	)
	rows, err := a.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query Dokploy servers: %w", err)
	}
	defer rows.Close()

	now := time.Now().UTC()
	servers := make([]model.Server, 0)
	for rows.Next() {
		var server model.Server
		var rawStatus sql.NullString
		var lastSeen sql.NullTime
		if err := rows.Scan(&server.ID, &server.Name, &rawStatus, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan Dokploy server: %w", err)
		}
		if server.Name == "" {
			server.Name = server.ID
		}
		if lastSeen.Valid {
			seen := lastSeen.Time.UTC()
			server.LastSeenAt = &seen
		}
		server.Status = classifyServerStatus(rawStatus.String, server.LastSeenAt, deadAfter, now)
		server.ResourceCount = resourceCounts[server.ID]
		servers = append(servers, server)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Dokploy servers: %w", err)
	}
	if localCount := resourceCounts[model.LocalServerID]; localCount > 0 && !serverListContains(servers, model.LocalServerID) {
		servers = append(servers, model.Server{
			ID:            model.LocalServerID,
			Name:          model.LocalServerName,
			Status:        model.ServerUnknown,
			ResourceCount: localCount,
		})
	}
	sort.SliceStable(servers, func(i, j int) bool {
		if servers[i].Status != servers[j].Status {
			return serverStatusRank(servers[i].Status) < serverStatusRank(servers[j].Status)
		}
		if servers[i].ResourceCount != servers[j].ResourceCount {
			return servers[i].ResourceCount > servers[j].ResourceCount
		}
		return strings.ToLower(servers[i].Name) < strings.ToLower(servers[j].Name)
	})
	return servers, nil
}

// ListServerResources returns all supported resources attached to one server.
func (a *Adapter) ListServerResources(ctx context.Context, serverID string) ([]model.Resource, error) {
	tables, err := a.supportedTables(ctx)
	if err != nil {
		return nil, err
	}

	resources := make([]model.Resource, 0)
	for _, table := range tables {
		nameExpr := `''`
		if table.HasName {
			nameExpr = `"name"`
		}
		whereClause := `"serverId" = $1`
		args := []any{serverID}
		if serverID == model.LocalServerID {
			whereClause = `"serverId" IS NULL`
			args = nil
		}
		//nolint:gosec // Table and ID column names come only from supportedTables, which requires expected schema columns first.
		query := fmt.Sprintf(
			`SELECT %s::text, COALESCE(%s::text, '') FROM %s WHERE %s ORDER BY %s::text`,
			quoteIdentifier(table.IDColumn),
			nameExpr,
			quoteIdentifier(table.Name),
			whereClause,
			quoteIdentifier(table.IDColumn),
		)
		rows, err := a.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("query %s resources: %w", table.Name, err)
		}
		if err := scanResources(rows, table, serverID, &resources); err != nil {
			return nil, err
		}
	}
	return resources, nil
}

func (a *Adapter) resourceCountsByServer(ctx context.Context) (map[string]int, error) {
	tables, err := a.supportedTables(ctx)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	for _, table := range tables {
		//nolint:gosec // Table name comes from supportedTables after information_schema verification.
		query := fmt.Sprintf(
			`SELECT "serverId"::text, count(*) FROM %s GROUP BY "serverId"`,
			quoteIdentifier(table.Name),
		)
		rows, err := a.db.QueryContext(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("query %s resource counts: %w", table.Name, err)
		}
		if err := scanResourceCounts(rows, table.Name, counts); err != nil {
			return nil, err
		}
	}
	return counts, nil
}

func scanResourceCounts(rows *sql.Rows, table string, counts map[string]int) error {
	defer rows.Close()
	for rows.Next() {
		var serverID sql.NullString
		var count int
		if err := rows.Scan(&serverID, &count); err != nil {
			return fmt.Errorf("scan %s resource count: %w", table, err)
		}
		if !serverID.Valid {
			counts[model.LocalServerID] += count
			continue
		}
		trimmedServerID := strings.TrimSpace(serverID.String)
		if trimmedServerID == "" {
			continue
		}
		counts[trimmedServerID] += count
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s resource counts: %w", table, err)
	}
	return nil
}

func scanResources(
	rows *sql.Rows,
	table supportedTable,
	serverID string,
	resources *[]model.Resource,
) error {
	defer rows.Close()

	for rows.Next() {
		var resource model.Resource
		if err := rows.Scan(&resource.ID, &resource.Name); err != nil {
			return fmt.Errorf("scan %s resource: %w", table.Name, err)
		}
		resource.Type = model.ResourceType(table.Name)
		resource.IDColumn = table.IDColumn
		resource.ServerID = serverID
		*resources = append(*resources, resource)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s resources: %w", table.Name, err)
	}
	return nil
}

// BuildRetargetPlan creates a dry-run plan without writing to Dokploy.
func (a *Adapter) BuildRetargetPlan(
	ctx context.Context,
	sourceServerID string,
	targetServerID string,
) (model.MigrationPlan, error) {
	sourceServerID = strings.TrimSpace(sourceServerID)
	targetServerID = strings.TrimSpace(targetServerID)
	if sourceServerID == "" || targetServerID == "" {
		return model.MigrationPlan{}, errors.New("source and target server IDs are required")
	}
	if sourceServerID == targetServerID {
		return model.MigrationPlan{}, errors.New("source and target server IDs must differ")
	}
	hash, err := a.ValidateSchema(ctx)
	if err != nil {
		return model.MigrationPlan{}, err
	}
	resources, err := a.ListServerResources(ctx, sourceServerID)
	if err != nil {
		return model.MigrationPlan{}, err
	}
	plan := model.MigrationPlan{
		ID:             fmt.Sprintf("plan-%d", time.Now().UTC().UnixNano()),
		SourceServerID: sourceServerID,
		TargetServerID: targetServerID,
		CreatedAt:      time.Now().UTC(),
		SchemaHash:     hash,
		Rows:           make([]model.PlanRow, 0, len(resources)),
	}
	for _, resource := range resources {
		plan.Rows = append(plan.Rows, model.PlanRow{
			Table:       string(resource.Type),
			IDColumn:    resource.IDColumn,
			ID:          resource.ID,
			Name:        resource.Name,
			OldServerID: sourceServerID,
			NewServerID: targetServerID,
		})
	}
	return plan, nil
}

// ApplyRetargetPlan applies a previously reviewed retarget plan in one transaction.
func (a *Adapter) ApplyRetargetPlan(
	ctx context.Context,
	plan model.MigrationPlan,
	schemaHashApproval string,
) error {
	if err := validateRetargetPlanForWrite(plan); err != nil {
		return err
	}
	hash, err := a.SchemaFingerprint(ctx)
	if err != nil {
		return err
	}
	if err := validateWriteSchema(hash, plan.SchemaHash, schemaHashApproval, a.allowedHashes); err != nil {
		return err
	}

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin retarget transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	for _, row := range plan.Rows {
		if !isRetargetTable(row.Table) {
			return fmt.Errorf("table %q is not in retarget allowlist", row.Table)
		}
		idColumn, err := validatedIDColumn(row.Table, row.IDColumn)
		if err != nil {
			return err
		}
		query, args := retargetSQL(row, idColumn)
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("retarget %s/%s: %w", row.Table, row.ID, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("retarget %s/%s rows affected: %w", row.Table, row.ID, err)
		}
		if affected != 1 {
			return fmt.Errorf("retarget %s/%s affected %d rows", row.Table, row.ID, affected)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit retarget transaction: %w", err)
	}
	return nil
}

func serverListContains(servers []model.Server, id string) bool {
	for _, server := range servers {
		if server.ID == id {
			return true
		}
	}
	return false
}

func retargetSQL(row model.PlanRow, idColumn string) (string, []any) {
	switch {
	case row.NewServerID == model.LocalServerID:
		return fmt.Sprintf(
			`UPDATE %s SET "serverId" = NULL WHERE %s = $1 AND "serverId" = $2`,
			quoteIdentifier(row.Table),
			quoteIdentifier(idColumn),
		), []any{row.ID, row.OldServerID}
	case row.OldServerID == model.LocalServerID:
		return fmt.Sprintf(
			`UPDATE %s SET "serverId" = $1 WHERE %s = $2 AND "serverId" IS NULL`,
			quoteIdentifier(row.Table),
			quoteIdentifier(idColumn),
		), []any{row.NewServerID, row.ID}
	default:
		return fmt.Sprintf(
			`UPDATE %s SET "serverId" = $1 WHERE %s = $2 AND "serverId" = $3`,
			quoteIdentifier(row.Table),
			quoteIdentifier(idColumn),
		), []any{row.NewServerID, row.ID, row.OldServerID}
	}
}

// RollbackRetargetPlan reverses metadata changes from a retarget plan.
func (a *Adapter) RollbackRetargetPlan(
	ctx context.Context,
	plan model.MigrationPlan,
	schemaHashApproval string,
) error {
	reversed := plan
	reversed.SourceServerID, reversed.TargetServerID = plan.TargetServerID, plan.SourceServerID
	reversed.Rows = make([]model.PlanRow, 0, len(plan.Rows))
	for i := len(plan.Rows) - 1; i >= 0; i-- {
		row := plan.Rows[i]
		row.OldServerID, row.NewServerID = row.NewServerID, row.OldServerID
		reversed.Rows = append(reversed.Rows, row)
	}
	return a.ApplyRetargetPlan(ctx, reversed, schemaHashApproval)
}

func validateRetargetPlanForWrite(plan model.MigrationPlan) error {
	if strings.TrimSpace(plan.ID) == "" {
		return errors.New("plan ID is required")
	}
	if strings.TrimSpace(plan.SourceServerID) == "" || strings.TrimSpace(plan.TargetServerID) == "" {
		return errors.New("plan source and target server IDs are required")
	}
	if plan.SourceServerID == plan.TargetServerID {
		return errors.New("plan source and target server IDs must differ")
	}
	if strings.TrimSpace(plan.SchemaHash) == "" {
		return errors.New("plan schema hash is required")
	}
	if len(plan.Rows) == 0 {
		return errors.New("plan has no rows")
	}
	for _, row := range plan.Rows {
		if strings.TrimSpace(row.Table) == "" || strings.TrimSpace(row.ID) == "" {
			return errors.New("plan rows require table and ID")
		}
		if row.OldServerID != plan.SourceServerID || row.NewServerID != plan.TargetServerID {
			return fmt.Errorf(
				"plan row %s/%s changes %s -> %s, want %s -> %s",
				row.Table,
				row.ID,
				row.OldServerID,
				row.NewServerID,
				plan.SourceServerID,
				plan.TargetServerID,
			)
		}
	}
	return nil
}

type supportedTable struct {
	Name     string
	IDColumn string
	HasName  bool
}

type columnInfo struct {
	Name     string
	DataType string
}

func (a *Adapter) tableColumns(ctx context.Context, table string) (map[string]columnInfo, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT column_name, data_type
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1
	`, table)
	if err != nil {
		return nil, fmt.Errorf("query %s columns: %w", table, err)
	}
	defer rows.Close()

	columns := make(map[string]columnInfo)
	for rows.Next() {
		var column columnInfo
		if err := rows.Scan(&column.Name, &column.DataType); err != nil {
			return nil, fmt.Errorf("scan %s column: %w", table, err)
		}
		columns[column.Name] = column
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s columns: %w", table, err)
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("dokploy %q table was not found", table)
	}
	return columns, nil
}

func (a *Adapter) supportedTables(ctx context.Context) ([]supportedTable, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT table_name, column_name
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = ANY($1)
	`, a.requiredTables)
	if err != nil {
		return nil, fmt.Errorf("query supported tables: %w", err)
	}
	defer rows.Close()

	columns := make(map[string]map[string]struct{})
	for rows.Next() {
		var tableName, columnName string
		if err := rows.Scan(&tableName, &columnName); err != nil {
			return nil, fmt.Errorf("scan supported tables: %w", err)
		}
		if columns[tableName] == nil {
			columns[tableName] = make(map[string]struct{})
		}
		columns[tableName][columnName] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate supported tables: %w", err)
	}

	out := make([]supportedTable, 0, len(columns))
	for _, table := range a.requiredTables {
		tableColumns := columns[table]
		if tableColumns == nil {
			continue
		}
		idColumn, ok := resourceIDColumn(table, tableColumns)
		if !ok {
			continue
		}
		if _, ok := tableColumns["serverId"]; !ok {
			continue
		}
		_, hasName := tableColumns["name"]
		out = append(out, supportedTable{Name: table, IDColumn: idColumn, HasName: hasName})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func firstExistingColumn(columns map[string]columnInfo, candidates []string) string {
	for _, candidate := range candidates {
		if _, ok := columns[candidate]; ok {
			return candidate
		}
	}
	return ""
}

func firstTemporalColumn(columns map[string]columnInfo, candidates []string) string {
	for _, candidate := range candidates {
		column, ok := columns[candidate]
		if !ok {
			continue
		}
		dataType := strings.ToLower(column.DataType)
		if strings.Contains(dataType, "timestamp") || strings.Contains(dataType, "date") {
			return candidate
		}
	}
	return ""
}

func classifyServerStatus(
	rawStatus string,
	lastSeenAt *time.Time,
	deadAfter time.Duration,
	now time.Time,
) model.ServerStatus {
	normalized := strings.ToLower(strings.TrimSpace(rawStatus))
	switch normalized {
	case "online",
		"healthy",
		"active",
		"connected",
		"running",
		"ready":
		return model.ServerOnline
	case "offline",
		"dead",
		"failed",
		"inactive",
		"disconnected",
		"error",
		"unreachable":
		return model.ServerOffline
	}
	if lastSeenAt == nil || deadAfter <= 0 {
		return model.ServerUnknown
	}
	if now.Sub(*lastSeenAt) > deadAfter {
		return model.ServerOffline
	}
	return model.ServerOnline
}

func serverStatusRank(status model.ServerStatus) int {
	switch status {
	case model.ServerOffline:
		return 0
	case model.ServerUnknown:
		return 1
	case model.ServerOnline:
		return 2
	default:
		return 3
	}
}

func resourceIDColumn(table string, columns map[string]struct{}) (string, bool) {
	for _, candidate := range idColumnCandidates(table) {
		if _, ok := columns[candidate]; ok {
			return candidate, true
		}
	}
	return "", false
}

func validateWriteSchema(
	currentHash string,
	planHash string,
	schemaHashApproval string,
	allowedHashes map[string]struct{},
) error {
	if planHash == "" {
		return errors.New("plan schema hash is required")
	}
	if planHash != currentHash {
		return fmt.Errorf("plan schema hash %s does not match current hash %s", planHash, currentHash)
	}
	if schemaHashApproval != "" {
		if schemaHashApproval != currentHash {
			return fmt.Errorf("schema approval %s does not match current hash %s", schemaHashApproval, currentHash)
		}
		return nil
	}
	if len(allowedHashes) == 0 {
		return errors.New("schema approval is required: provide schemaHashApproval or MIGRATOR_SCHEMA_ALLOWLIST")
	}
	if _, ok := allowedHashes[currentHash]; !ok {
		return fmt.Errorf("schema fingerprint %s is not allowlisted", currentHash)
	}
	return nil
}

func validatedIDColumn(table string, candidate string) (string, error) {
	for _, allowed := range idColumnCandidates(table) {
		if candidate == "" && allowed == defaultIDColumn(table) {
			return allowed, nil
		}
		if candidate == allowed {
			return allowed, nil
		}
	}
	return "", fmt.Errorf("id column %q is not valid for table %q", candidate, table)
}

func idColumnCandidates(table string) []string {
	candidates := []string{defaultIDColumn(table), "id"}
	switch table {
	case string(model.ResourceApplication):
		candidates = append([]string{applicationIDColumn}, candidates...)
	case string(model.ResourceCompose):
		candidates = append([]string{"composeId"}, candidates...)
	case string(model.ResourcePostgres):
		candidates = append([]string{"postgresId"}, candidates...)
	case string(model.ResourceMySQL):
		candidates = append([]string{"mysqlId"}, candidates...)
	case string(model.ResourceMariaDB):
		candidates = append([]string{"mariadbId"}, candidates...)
	case string(model.ResourceMongo):
		candidates = append([]string{mongoIDColumn}, candidates...)
	case string(model.ResourceRedis):
		candidates = append([]string{"redisId"}, candidates...)
	case string(model.ResourceLibSQL):
		candidates = append([]string{"libsqlId"}, candidates...)
	case string(model.ResourceDomain):
		candidates = append([]string{"domainId"}, candidates...)
	}
	return uniqueStrings(candidates)
}

func defaultIDColumn(table string) string {
	return table + "Id"
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func isRetargetTable(table string) bool {
	for _, allowed := range retargetTables {
		if table == allowed {
			return true
		}
	}
	return false
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
