# Dokploy Migrator Operations Reference

Use this reference for live Dokploy checks, DSN discovery, server ID lookup, dry-run/apply verification, and Traefik/port debugging.

## Discover Postgres Password and DSN

Dokploy commonly runs PostgreSQL as `dokploy-postgres` with user/database `dokploy`.

```sh
PASS="$(docker exec $(docker ps -q --filter label=com.docker.swarm.service.name=dokploy-postgres | head -n1) sh -lc 'cat /run/secrets/postgres_password')"

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

```sh
docker run --rm --network dokploy-network \
  -e PGPASSWORD="$PASS" \
  postgres:18 \
  psql -h dokploy-postgres -U dokploy -d dokploy \
  -v ON_ERROR_STOP=1 -P pager=off \
  -c 'select "serverId", "name" from "server" order by "name";'
```

Use the dead server as source and the live server as target.

## Web UI Flow

1. Open the Migrator domain and authenticate with Basic Auth.
2. Scan servers with `GET /api/servers` from the UI.
3. Use scanned IDs as source and target.
4. Build dry-run.
5. Review resources and raw JSON.
6. Paste `plan.schemaHash` in `Schema hash approval`, or leave it empty only when `MIGRATOR_SCHEMA_ALLOWLIST` is already configured to the exact hash.
7. Type `APPLY`.
8. Apply metadata retarget.
9. Verify rows in PostgreSQL.
10. Redeploy/restart affected resources in Dokploy.

## Verify Apply

Check resources by names from the dry-run plan:

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

After a successful apply, another dry-run for the same source/target should normally return `rows: []`.

## Port and Traefik Debugging

The app listens inside the container on `8080`.

```sh
CID="$(docker ps -q --filter name=dokploy-migrator | head -n1)"

docker logs --tail=100 "$CID"

docker exec "$CID" sh -lc 'curl -fsS -u "$MIGRATOR_BASIC_USER:$MIGRATOR_BASIC_PASSWORD" http://127.0.0.1:8080/api/health'
```

For Dokploy Domains:

- `Container Port` should be `8080`.
- A `401` response from the domain means the app is reachable and Basic Auth is active.
- `Bad Gateway` usually means Traefik is forwarding to the wrong internal port or the container is not on the expected network.

Inspect Traefik labels:

```sh
docker inspect "$CID" --format '{{json .Config.Labels}}' | jq | grep -E 'rule|loadbalancer.server.port'
```

The load balancer server port should be `8080`.
