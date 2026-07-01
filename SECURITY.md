# Security Policy

Dokploy Migrator can update Dokploy database metadata. Treat it as an administrative tool.

## Supported Versions

Security fixes are handled on the default branch until stable releases are introduced.

## Reporting a Vulnerability

Please do not open a public issue for a vulnerability that could expose credentials, bypass apply confirmation, or modify Dokploy metadata unexpectedly.

Use a private reporting channel for the repository owner. If none is published yet, open a minimal public issue asking for a security contact without including exploit details.

## Operator Guidance

- Use strong Basic Auth credentials.
- Keep `MIGRATOR_ADMIN_TOKEN` private.
- Do not expose the app on a public domain without HTTPS and access control.
- Prefer explicit UI `schemaHash` approval for one-off recovery.
- Store DSNs, S3 keys, and SSH keys only as runtime secrets or environment variables.
