# End-to-End Tests

Dokploy Migrator has a heavy e2e path that checks the Migrator against a PostgreSQL database migrated by the official Dokploy container.

## Why This Exists

Unit tests protect our code paths, but they do not prove that current Dokploy database schema still matches our adapter assumptions. The e2e test starts Dokploy, lets it create/migrate its database, seeds a recovery scenario into that real schema, and runs:

```text
plan -> apply -> verify -> rollback -> verify
```

## Requirements

- Docker with Compose v2
- Go
- `jq`
- Internet access to pull `dokploy/dokploy`, `postgres:16`, and `redis:7`

This test is intentionally not part of `make test`. It is slower and requires Docker.

## Run

```sh
make e2e-db
```

Useful overrides:

```sh
DOKPLOY_VERSION=v0.29.8 make e2e-db
E2E_POSTGRES_PORT=55433 make e2e-db
E2E_KEEP=1 make e2e-db
```

By default the test uses `dokploy/dokploy:latest` so schema drift is caught early. Set `DOKPLOY_VERSION` only when you need to reproduce a failure against a specific Dokploy release.

`E2E_KEEP=1` leaves the Compose project running for inspection.

## What It Starts

The Compose stack in `e2e/dokploy/docker-compose.yml` starts:

- `postgres:16`
- `redis:7`
- `dokploy/dokploy:${DOKPLOY_VERSION:-latest}`

The Dokploy container is used to create/migrate the real Dokploy PostgreSQL schema. The test then inserts two servers and four resources into that schema using dynamic SQL that respects current required columns.

## What It Verifies

- Our adapter can build a plan from the real migrated schema.
- At least application, compose, postgres, and redis resources are discovered.
- Apply moves those rows to the target server.
- Rollback moves those rows back to the source server.
- CLI apply still requires explicit `-confirm APPLY`.
- CLI apply and rollback accept explicit `-schema-hash-approval`; the env allowlist remains only a fallback path.

## Full Dokploy UI/API E2E

A future deeper test can automate Dokploy onboarding and create resources through Dokploy API/UI. That requires more host assumptions: Linux, Docker Swarm, Docker socket access, free ports, and stable Dokploy auth bootstrap. The current `e2e-db` layer gives the highest value for this project today: it catches real schema drift without making normal CI depend on a full Dokploy installation.
