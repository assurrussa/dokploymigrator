# Dokploy Template Draft

This project is not published as an official Dokploy template yet. The files in `templates/dokploy/` are a draft contract for that future template.

## Template Goals

- One-click Dokploy app install.
- Fixed internal container port `8080`.
- Persistent `/data` volume for Migrator job history.
- Required Basic Auth and admin token variables.
- Optional Dokploy API settings.
- Clear `DOKPLOY_POSTGRES_DSN` input.
- No default production secrets.

## Template Fit

The template should deploy Migrator itself as a normal Dokploy Compose app. It must not present the project as a Docker Swarm migration tool. Migrator retargets Dokploy metadata only; Swarm services, tasks, networks, routing, and volumes still require operator verification and redeploy/repair steps outside Migrator.

## Required Operator Inputs

- `MIGRATOR_BASIC_USER`
- `MIGRATOR_BASIC_PASSWORD`
- `MIGRATOR_ADMIN_TOKEN`
- `DOKPLOY_POSTGRES_DSN`

## Optional Inputs

- `MIGRATOR_HTTP_PORT`
- `MIGRATOR_SCHEMA_ALLOWLIST`
- `MIGRATOR_DEAD_AFTER`
- `DOKPLOY_API_BASE_URL`
- `DOKPLOY_API_TOKEN`
- `DOKPLOY_HEALTH_PATH`
- `DOKPLOY_DEPLOY_PATH`

## Expected Dokploy Domain Settings

- Path: `/`
- Internal path: `/`
- Container port: `8080`
- HTTPS: enabled when the operator exposes the app publicly

## Future Publishing Checklist

1. Publish a container image.
2. Replace `MIGRATOR_IMAGE` placeholder with the published image.
3. Verify Dokploy template syntax against the target Dokploy version.
4. Add screenshots for scan, dry-run, and apply.
5. Document supported Dokploy versions and known schema hashes.
6. Keep warnings about credentials, metadata-only scope, and Docker Swarm caveats visible.

Validate the draft with:

```sh
docker compose --env-file templates/dokploy/env.example -f templates/dokploy/docker-compose.yml config
```
