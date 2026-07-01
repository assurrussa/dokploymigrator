# Dokploy Migrator Implementation Notes

## Decisions

- Project scaffold starts from an empty directory; no existing repo conventions were present.
- Go projects should use the current stable Go toolchain and current Docker base images at scaffold/update time; this repo now targets Go `1.26` with toolchain `go1.26.4`.
- Docker Compose deployment is a first-class path: the Web UI starts without a Dokploy DB DSN, while migration actions stay disabled until the DB DSN is configured.
- v1 is implemented as a Go binary that can serve an embedded Web UI and expose CLI commands from the same code path.
- Migrator state is kept separate from Dokploy in a local SQLite database.
- Dokploy PostgreSQL write support is guarded by schema fingerprint checks and dry-run plans before updates.
- Direct restore is modeled through SSH plus Docker restore containers rather than direct writes into Docker internals.
- S3 backup matching is manifest-first, with deterministic fallback candidates by resource name/type/time.

## Tradeoffs

- Exact Dokploy API endpoint names were not hardcoded beyond configurable health/deploy paths because current upstream docs could not be fetched through the local Context7 CLI runtime during planning.
- SQLite as a Dokploy backend is intentionally not implemented for destructive writes in v1.
- Metadata rollback is implemented for server/domain binding plans; restored volume/container cleanup is reported, not automatically removed.

## Validation Notes

- Keep adding fixtures for each supported Dokploy schema version before enabling writes against it.
- Use `go test ./...` as the baseline verification command.
- Verified locally with `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./cmd/dokploy-migrator`, and an authenticated `/api/health` request against the built binary.
