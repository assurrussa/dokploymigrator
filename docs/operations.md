# Operations Guide

This guide is for operators running Dokploy Migrator against a real Dokploy installation.

## Scope and Deployment Fit

Dokploy Migrator changes Dokploy PostgreSQL metadata. It does not move Docker runtime state. Use it when a dead or unavailable server still owns Dokploy resources in the database and the operator can redeploy the affected resources after retargeting.

Best fit:

- Single-host recovery paths and resources that do not depend on preserving existing Swarm service/task state.
- Applications, Compose stacks, Dokploy-managed databases, and domains whose `serverId` needs to move to a healthy remote server or back to the main local Dokploy server.
- Emergency recovery where a reviewed dry-run, explicit `schemaHash` approval, and post-apply verification are acceptable.

Use extra caution with Docker Swarm. Swarm keeps its own manager/service/task desired state, routing, networks, and volumes. Migrator does not update that state, so a Swarm-backed installation needs manual checks such as `docker service ls`, `docker service ps`, volume/network inspection, and Dokploy redeploy verification after the metadata change.

## Important Rules

- Back up Dokploy PostgreSQL before applying a plan.
- Review the dry-run rows and move only the resources that should leave the source server.
- Keep Basic Auth, `MIGRATOR_ADMIN_TOKEN`, and `DOKPLOY_POSTGRES_DSN` private.
- Do not expose the app publicly without HTTPS and access control.
- For production, prefer a versioned container image once releases exist.

## Discover Dokploy PostgreSQL DSN

Dokploy commonly runs PostgreSQL as service `dokploy-postgres` with database/user `dokploy`.

Read the current password from the Postgres container secret:

```sh
PASS="$(docker exec $(docker ps -q --filter label=com.docker.swarm.service.name=dokploy-postgres | head -n1) sh -lc 'cat /run/secrets/postgres_password')"
```

Build the DSN:

```sh
printf 'postgres://dokploy:%s@dokploy-postgres:5432/dokploy?sslmode=disable\n' "$PASS"
```

Verify connectivity:

```sh
docker run --rm --network dokploy-network \
  -e PGPASSWORD="$PASS" \
  postgres:18 \
  psql -h dokploy-postgres -U dokploy -d dokploy \
  -v ON_ERROR_STOP=1 -P pager=off \
  -c 'select 1 as ok;'
```

## List Server IDs

The Web UI can scan servers automatically. If you need a shell check:

```sh
docker run --rm --network dokploy-network \
  -e PGPASSWORD="$PASS" \
  postgres:18 \
  psql -h dokploy-postgres -U dokploy -d dokploy \
  -v ON_ERROR_STOP=1 -P pager=off \
  -c 'select "serverId", "name" from "server" order by "name";'
```

The main local Dokploy server is not always stored as a row in `server`. The Web
UI always shows it through the synthetic ID `__dokploy_local__`, including when
it owns zero resources. As a source this marker selects rows where `serverId IS
NULL`; as a target it makes apply set `serverId = NULL`.

Count movable resources by explicit server or local source:

```sh
docker run --rm --network dokploy-network \
  -e PGPASSWORD="$PASS" \
  postgres:18 \
  psql -h dokploy-postgres -U dokploy -d dokploy \
  -v ON_ERROR_STOP=1 -P pager=off \
  -c "
select coalesce(\"serverId\"::text, '__dokploy_local__') as source_id, count(*) from (
  select \"serverId\" from \"application\"
  union all select \"serverId\" from \"compose\"
  union all select \"serverId\" from \"postgres\"
  union all select \"serverId\" from \"mysql\"
  union all select \"serverId\" from \"mongo\"
  union all select \"serverId\" from \"redis\"
) x
group by source_id
order by source_id;
"
```

If tables such as `mysql` or `mongo` do not exist in your Dokploy version,
remove those lines from the manual query.

## Production Flow

1. Deploy Dokploy Migrator with Basic Auth, admin token, and `DOKPLOY_POSTGRES_DSN`.
2. Open the UI.
3. Scan servers.
4. Select source and target server IDs. Use `__dokploy_local__` as the source for resources whose current `serverId` is `NULL`, or as the target to move resources onto the main local Dokploy server.
5. Build dry-run.
6. Review rows, uncheck resources that should stay on the source server for this apply, and verify `schemaHash`.
7. Paste `schemaHash` or configure `MIGRATOR_SCHEMA_ALLOWLIST`.
8. Type `APPLY`.
9. Apply metadata retargeting for the selected resources.
10. Verify PostgreSQL rows.
11. Redeploy affected resources in Dokploy.

## Verify Apply

Check named resources from the dry-run:

```sh
docker run --rm --network dokploy-network \
  -e PGPASSWORD="$PASS" \
  postgres:18 \
  psql -h dokploy-postgres -U dokploy -d dokploy \
  -v ON_ERROR_STOP=1 -P pager=off \
  -c "
select 'application' as type, \"name\", \"serverId\" from \"application\" where \"name\" = 'frontend'
union all
select 'compose', \"name\", \"serverId\" from \"compose\" where \"name\" = 'backend'
union all
select 'postgres', \"name\", \"serverId\" from \"postgres\" where \"name\" = 'pgsql'
union all
select 'redis', \"name\", \"serverId\" from \"redis\" where \"name\" = 'redis';
"
```

Count resources left on the old server:

```sh
OLD_SERVER_ID='<source-server-id>'

docker run --rm --network dokploy-network \
  -e PGPASSWORD="$PASS" \
  postgres:18 \
  psql -h dokploy-postgres -U dokploy -d dokploy \
  -v ON_ERROR_STOP=1 -P pager=off \
  -c "
select count(*) from (
  select \"serverId\" from \"application\" where \"serverId\" = '$OLD_SERVER_ID'
  union all select \"serverId\" from \"compose\" where \"serverId\" = '$OLD_SERVER_ID'
  union all select \"serverId\" from \"postgres\" where \"serverId\" = '$OLD_SERVER_ID'
  union all select \"serverId\" from \"mysql\" where \"serverId\" = '$OLD_SERVER_ID'
  union all select \"serverId\" from \"mongo\" where \"serverId\" = '$OLD_SERVER_ID'
  union all select \"serverId\" from \"redis\" where \"serverId\" = '$OLD_SERVER_ID'
) x;
"
```

If the old source was the main local Dokploy server, check `serverId IS NULL`
instead:

```sh
docker run --rm --network dokploy-network \
  -e PGPASSWORD="$PASS" \
  postgres:18 \
  psql -h dokploy-postgres -U dokploy -d dokploy \
  -v ON_ERROR_STOP=1 -P pager=off \
  -c "
select count(*) from (
  select \"serverId\" from \"application\" where \"serverId\" is null
  union all select \"serverId\" from \"compose\" where \"serverId\" is null
  union all select \"serverId\" from \"postgres\" where \"serverId\" is null
  union all select \"serverId\" from \"mysql\" where \"serverId\" is null
  union all select \"serverId\" from \"mongo\" where \"serverId\" is null
  union all select \"serverId\" from \"redis\" where \"serverId\" is null
) x;
"
```

If tables such as `mysql` or `mongo` do not exist in your Dokploy version, remove those lines from the manual query. The Migrator adapter automatically skips missing supported tables.

If the target was the main local Dokploy server, verify the selected resource
rows with `serverId IS NULL`. The dry-run and stored report retain
`targetServerId: "__dokploy_local__"` as the operator-facing representation;
the PostgreSQL write uses `NULL`.

## Job History

The UI shows job history in pages of 50 records. The newest 50 jobs are protected from deletion so the latest dry-run/apply/rollback audit window stays available.

List a page through the API:

```sh
curl -fsS \
  -u "$MIGRATOR_BASIC_USER:$MIGRATOR_BASIC_PASSWORD" \
  "https://migrator.example.com/api/jobs?limit=50&offset=50"
```

Delete an older job, including its Migrator-owned events and report:

```sh
curl -fsS -X DELETE \
  -u "$MIGRATOR_BASIC_USER:$MIGRATOR_BASIC_PASSWORD" \
  -H "X-Migrator-Admin-Token: $MIGRATOR_ADMIN_TOKEN" \
  "https://migrator.example.com/api/jobs/$JOB_ID"
```

## API Apply Payload

For a dry-run that targets the main local Dokploy server, send
`"targetServerId": "__dokploy_local__"` to `POST /api/plan`. The response keeps
that marker in `plan.targetServerId` and each row's `newServerId`.

```json
{
  "jobId": "job-...",
  "plan": {
    "id": "plan-...",
    "schemaHash": "...",
    "rows": []
  },
  "schemaHashApproval": "<same schema hash, or empty to use MIGRATOR_SCHEMA_ALLOWLIST>",
  "confirmationText": "APPLY"
}
```

`confirmationText` must be exactly `APPLY`.

## CLI Apply

The CLI keeps the same confirmation contract as the Web UI/API. Prefer explicit
schema approval for one-off recovery:

```sh
dokploy-migrator apply \
  -job "$JOB_ID" \
  -plan plan.json \
  -schema-hash-approval "$SCHEMA_HASH" \
  -confirm APPLY
```

If `-schema-hash-approval` is omitted, the current schema hash must be present
in `MIGRATOR_SCHEMA_ALLOWLIST`.

## Rollback

Rollback reverses the reviewed plan rows. Use it only after checking the plan
and current database state. It uses the same schema approval boundary as apply:
pass the current `schemaHashApproval`, or leave it empty only when
`MIGRATOR_SCHEMA_ALLOWLIST` contains the current schema hash.

```sh
curl -fsS \
  -u "$MIGRATOR_BASIC_USER:$MIGRATOR_BASIC_PASSWORD" \
  -H "Content-Type: application/json" \
  -H "X-Migrator-Admin-Token: $MIGRATOR_ADMIN_TOKEN" \
  -d @rollback-payload.json \
  https://migrator.example.com/api/rollback
```

CLI rollback:

```sh
dokploy-migrator rollback \
  -job "$JOB_ID" \
  -plan plan.json \
  -schema-hash-approval "$SCHEMA_HASH"
```

## Troubleshooting

`401 Unauthorized` means the app is reachable and Basic Auth is active.

`Bad Gateway` usually means Dokploy/Traefik is forwarding to the wrong internal port. The container port should be `8080`.

Check local health from inside the container:

```sh
CID="$(docker ps -q --filter name=dokploy-migrator | head -n1)"

docker exec "$CID" sh -lc 'curl -fsS -u "$MIGRATOR_BASIC_USER:$MIGRATOR_BASIC_PASSWORD" http://127.0.0.1:8080/api/health'
```

Inspect Traefik labels:

```sh
docker inspect "$CID" --format '{{json .Config.Labels}}' | jq | grep -E 'rule|loadbalancer.server.port'
```

The load balancer server port should be `8080`.
