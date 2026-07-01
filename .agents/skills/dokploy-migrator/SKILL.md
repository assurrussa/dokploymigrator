---
name: dokploy-migrator
description: Use when working in this repository on Dokploy Migrator, including Dokploy schema retargeting, Web UI apply flow, Dokploy Compose deployment, DSN and serverId discovery, schemaHash approval, Go or Docker version updates, tests, README, and AGENTS maintenance.
---

# Dokploy Migrator

## Overview

This skill keeps work on Dokploy Migrator grounded in the repo's safety model and the real Dokploy deployment workflow. Use it before changing migration logic, operator docs, Compose deployment, schema approval behavior, or live verification commands.

## Grounding

Start with these repo files:

- `README.md` for the public project overview and quick-start.
- `docs/operations.md` for the operator runbook and production flow.
- `docs/e2e.md` for the real Dokploy database end-to-end test.
- `docs/dokploy-template.md` plus `templates/dokploy/` for future Dokploy template work.
- `implementation-notes.md` for decisions, tradeoffs, and current limitations.
- `AGENTS.md` for repo-local rules.
- `internal/dokploydb/postgres.go` for schema fingerprinting and retarget writes.
- `internal/jobs/jobs.go` for job orchestration and apply/rollback state.
- `internal/server/server.go` plus `internal/server/static/` for API and Web UI behavior.

For live Dokploy checks, load `references/operations.md`.

## Non-Negotiable Safety Rules

- Never weaken schema approval. Apply must require a dry-run plan plus either explicit `schemaHashApproval` or `MIGRATOR_SCHEMA_ALLOWLIST`.
- UI/API apply must require confirmation text exactly `APPLY`.
- Keep transaction behavior, schema drift checks, `idColumn` validation, and row-count validation intact.
- Do not add destructive SQL outside the Dokploy DB adapter.
- Do not commit live DSNs, tokens, passwords, S3 credentials, SSH keys, or host-specific production values.

## Project Contracts

- Internal container port stays `8080`.
- `MIGRATOR_HTTP_PORT` is only for optional host port publishing in Docker Compose.
- Dokploy Domains should target container port `8080`.
- Go and Docker versions should track current stable releases; keep `go.mod`, `Dockerfile`, README, and implementation notes aligned.
- Current production scope is metadata retargeting. S3 backup discovery and SSH restore primitives exist, but full restore orchestration is not complete.
- The Web UI is Russian by default and has an English switch.
- Server scan is read-only and exposed as `GET /api/servers` under Basic Auth.
- `planned_relocation` is a reserved mode marker; it should stay hidden from the primary UI until it has distinct consistency behavior.

## Dokploy Schema Notes

- Server table uses `"serverId"`.
- Resource tables use table-specific ID columns such as `"applicationId"`, `"composeId"`, `"postgresId"`, and `"redisId"`.
- Mongo resources use table `"mongo"`, not `"mongodb"`.
- Database resources may include `"postgres"`, `"mysql"`, `"mongo"`, `"redis"`, `"mariadb"`, and `"libsql"` depending on Dokploy version.

## Verification

Run the tight checks after code changes:

```sh
make test
make lint
make build
docker compose config
MIGRATOR_HTTP_PORT=8888 docker compose config
```

Run the heavy real-schema check when touching Dokploy schema assumptions:

```sh
make e2e-db
```

Run skill validation after editing this skill:

```sh
python3 <skill-creator>/scripts/quick_validate.py .agents/skills/dokploy-migrator
```

Before finishing docs or onboarding changes, confirm there are no machine-local production paths or secrets in committed files.
