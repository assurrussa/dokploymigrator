# AGENTS.md

This is a repo-local overlay for future agents working on Dokploy Migrator.

- Start from `README.md` for public project overview, `docs/operations.md` for operator behavior, and `implementation-notes.md` for decisions/tradeoffs.
- Use `.agents/skills/dokploy-migrator/SKILL.md` for project-specific workflow when changing migration logic, Dokploy Compose deployment, schema approval, or verification docs.
- For shared agent context, prefer `AGENT_CONTEXT_ROOT` and the project-context-router workflow when available. Do not commit machine-local absolute paths.
- Do not weaken schema approval. Production writes must stay gated by dry-run review plus explicit UI `schemaHashApproval` or `MIGRATOR_SCHEMA_ALLOWLIST`.
- Keep the app internal container port at `8080`. `MIGRATOR_HTTP_PORT` is only for optional host port publishing.
- Prefer current stable Go versions and Docker base images; keep `go.mod`, `Dockerfile`, and docs aligned when bumping them.
- Before handing off code changes, run `make test`, `make lint`, `make build`, and `docker compose config` when the environment supports them.
- Run `make e2e-db` when changing Dokploy schema detection, retarget planning, apply/rollback SQL, or supported resource tables.
- Never commit live DSNs, tokens, passwords, S3 keys, SSH keys, or host-specific production values.
