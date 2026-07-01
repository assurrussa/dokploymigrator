// Package model contains shared domain types for Dokploy Migrator.
package model

import "time"

const (
	// LocalServerID is the operator-facing marker for resources that Dokploy
	// stores on its built-in local server with serverId = NULL.
	LocalServerID   = "__dokploy_local__"
	LocalServerName = "Main local Dokploy"
)

// ResourceType identifies a Dokploy resource kind that can be migrated.
type ResourceType string

const (
	ResourceApplication ResourceType = "application"
	ResourceCompose     ResourceType = "compose"
	ResourcePostgres    ResourceType = "postgres"
	ResourceMySQL       ResourceType = "mysql"
	ResourceMariaDB     ResourceType = "mariadb"
	ResourceMongo       ResourceType = "mongo"
	ResourceRedis       ResourceType = "redis"
	ResourceLibSQL      ResourceType = "libsql"
	ResourceDomain      ResourceType = "domain"
)

// ServerStatus is the operator-facing server health classification.
type ServerStatus string

const (
	ServerOnline  ServerStatus = "online"
	ServerOffline ServerStatus = "offline"
	ServerUnknown ServerStatus = "unknown"
)

// Server describes a Dokploy server node.
type Server struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Status        ServerStatus `json:"status"`
	LastSeenAt    *time.Time   `json:"lastSeenAt,omitempty"`
	ResourceCount int          `json:"resourceCount"`
}

// Resource describes an entity currently bound to a Dokploy server.
type Resource struct {
	ID       string       `json:"id"`
	IDColumn string       `json:"idColumn"`
	Name     string       `json:"name"`
	Type     ResourceType `json:"type"`
	ServerID string       `json:"serverId"`
}

// MigrationMode controls source consistency behavior.
type MigrationMode string

const (
	ModeDeadRecovery      MigrationMode = "dead_recovery"
	ModePlannedRelocation MigrationMode = "planned_relocation"
)

// NormalizeMigrationMode applies the production default and rejects unknown
// mode strings before they are stored in durable job history.
func NormalizeMigrationMode(mode MigrationMode) (MigrationMode, bool) {
	if mode == "" {
		return ModeDeadRecovery, true
	}
	switch mode {
	case ModeDeadRecovery, ModePlannedRelocation:
		return mode, true
	default:
		return "", false
	}
}

// BackupCandidate describes an S3 backup object that may restore a resource.
type BackupCandidate struct {
	ResourceID   string       `json:"resourceId,omitempty"`
	ResourceName string       `json:"resourceName,omitempty"`
	ResourceType ResourceType `json:"resourceType,omitempty"`
	Key          string       `json:"key"`
	ETag         string       `json:"etag,omitempty"`
	Checksum     string       `json:"checksum,omitempty"`
	Size         int64        `json:"size"`
	LastModified time.Time    `json:"lastModified"`
	Confidence   string       `json:"confidence"`
}

// PlanRow describes one DB change proposed by a dry-run plan.
type PlanRow struct {
	Table       string `json:"table"`
	IDColumn    string `json:"idColumn"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	OldServerID string `json:"oldServerId"`
	NewServerID string `json:"newServerId"`
}

// MigrationPlan is the complete dry-run result for a source-target pair.
type MigrationPlan struct {
	ID             string    `json:"id"`
	SourceServerID string    `json:"sourceServerId"`
	TargetServerID string    `json:"targetServerId"`
	CreatedAt      time.Time `json:"createdAt"`
	SchemaHash     string    `json:"schemaHash"`
	Rows           []PlanRow `json:"rows"`
}
