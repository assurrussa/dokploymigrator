# Contributing

Thanks for improving Dokploy Migrator.

## Development Setup

```sh
go version
make test
make lint
make build
```

The project intentionally keeps runtime dependencies small:

- Go service and CLI in `cmd/` and `internal/`
- Embedded Web UI in `internal/server/static/`
- Docker Compose deployment in `docker-compose.yml`
- Future Dokploy template draft in `templates/dokploy/`

## Safety Rules

- Do not weaken apply confirmation, schema hash approval, or transaction checks.
- Do not add destructive SQL outside `internal/dokploydb/`.
- Do not commit credentials, DSNs, tokens, S3 keys, SSH keys, or production host values.
- Keep the internal container port at `8080`.
- Add or update tests for changes to migration planning, apply, rollback, or schema detection.

## Pull Request Checklist

- `make test`
- `make lint`
- `make build`
- `docker compose config`
- Documentation updated when config, API, UI flow, or operator behavior changes

## Scope Notes

Metadata retargeting is implemented. Full S3 volume/database restore orchestration is planned but not complete.
