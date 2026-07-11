#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/e2e/dokploy/docker-compose.yml"
PROJECT_NAME="${E2E_PROJECT_NAME:-dokploy-migrator-e2e}"
E2E_POSTGRES_PORT="${E2E_POSTGRES_PORT:-55432}"
E2E_WORK_DIR="${E2E_WORK_DIR:-$ROOT_DIR/tmp/e2e-dokploy-db}"
E2E_SECRET_DIR="$E2E_WORK_DIR/secrets"
E2E_DOKPLOY_ETC="$E2E_WORK_DIR/etc-dokploy"
E2E_GO_CACHE="$E2E_WORK_DIR/go-build"
POSTGRES_PASSWORD="${E2E_POSTGRES_PASSWORD:-dokploy-e2e-password}"
AUTH_SECRET="${E2E_AUTH_SECRET:-dokploy-e2e-auth-secret}"
SOURCE_SERVER_ID="${E2E_SOURCE_SERVER_ID:-e2e-dead-server}"
TARGET_SERVER_ID="${E2E_TARGET_SERVER_ID:-e2e-live-server}"
PLAN_FILE="$E2E_WORK_DIR/plan.json"
LOCAL_SERVER_ID="${E2E_LOCAL_SERVER_ID:-__dokploy_local__}"
REMOTE_TO_LOCAL_PLAN_FILE="$E2E_WORK_DIR/remote-to-local-plan.json"
LOCAL_SERVER_SQL_MARKER="__e2e_null_server__"
LOCAL_COMPOSE_ID="${E2E_LOCAL_COMPOSE_ID:-e2e-local-compose}"
LOCAL_PLAN_FILE="$E2E_WORK_DIR/local-plan.json"

log() {
  printf '[e2e] %s\n' "$*"
}

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$1" >&2
    exit 1
  fi
}

cleanup() {
  if [ "${E2E_KEEP:-0}" = "1" ]; then
    log "E2E_KEEP=1, leaving compose project $PROJECT_NAME running"
    return
  fi
  compose down -v --remove-orphans || true
}

compose() {
  E2E_SECRET_DIR="$E2E_SECRET_DIR" \
  E2E_DOKPLOY_ETC="$E2E_DOKPLOY_ETC" \
  E2E_POSTGRES_PORT="$E2E_POSTGRES_PORT" \
  docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" "$@"
}

psql_e2e() {
  compose exec -T \
    -e PGPASSWORD="$POSTGRES_PASSWORD" \
    dokploy-postgres \
    psql \
    -U dokploy \
    -d dokploy \
    -v ON_ERROR_STOP=1 \
    -P pager=off \
    "$@"
}

wait_for_postgres() {
  log "waiting for PostgreSQL on 127.0.0.1:$E2E_POSTGRES_PORT"
  for _ in $(seq 1 60); do
    if psql_e2e -c 'select 1;' >/dev/null 2>&1; then
      return
    fi
    sleep 2
  done
  printf 'PostgreSQL did not become ready\n' >&2
  exit 1
}

wait_for_dokploy_schema() {
  log "waiting for Dokploy migrations"
  for _ in $(seq 1 120); do
    if psql_e2e -Atc "select to_regclass('public.server') is not null;" 2>/dev/null | grep -qx t; then
      return
    fi
    sleep 2
  done
  log "Dokploy logs:"
  compose logs --tail=200 dokploy || true
  printf 'Dokploy schema did not appear\n' >&2
  exit 1
}

seed_real_schema() {
  log "seeding Dokploy schema with two servers, one local resource, and movable resources"
  psql_e2e \
    -v source_server="$SOURCE_SERVER_ID" \
    -v target_server="$TARGET_SERVER_ID" \
    -v local_server_sql_marker="$LOCAL_SERVER_SQL_MARKER" \
    -v local_compose_id="$LOCAL_COMPOSE_ID" <<'SQL'
drop function if exists e2e_insert_minimal(text, text, text, text, text);
drop function if exists e2e_insert_minimal(text, text, text, text, text, jsonb);
drop function if exists e2e_literal_for(regtype, text, text);

create or replace function e2e_literal_for(column_type regtype, column_name text, suffix text)
returns text
language plpgsql
as $$
declare
  enum_value text;
  type_name text := column_type::text;
begin
  if type_name in ('text', 'character varying', 'character', 'varchar') then
    return quote_literal(column_name || '-' || suffix);
  end if;
  if type_name = 'uuid' then
    return quote_literal('00000000-0000-0000-0000-' || lpad(substr(md5(column_name || suffix), 1, 12), 12, '0')) || '::uuid';
  end if;
  if type_name in ('integer', 'bigint', 'smallint') then
    return '0';
  end if;
  if type_name in ('numeric', 'real', 'double precision') then
    return '0';
  end if;
  if type_name = 'boolean' then
    return 'false';
  end if;
  if type_name in ('timestamp with time zone', 'timestamp without time zone', 'date') then
    return 'now()';
  end if;
  if type_name = 'json' then
    return quote_literal('{}') || '::json';
  end if;
  if type_name = 'jsonb' then
    return quote_literal('{}') || '::jsonb';
  end if;
  if type_name like '%[]' then
    return quote_literal('{}') || '::' || type_name;
  end if;

  select e.enumlabel
    into enum_value
    from pg_enum e
    join pg_type t on t.oid = e.enumtypid
   where t.oid = column_type
   order by e.enumsortorder
   limit 1;
  if enum_value is not null then
    return quote_literal(enum_value) || '::' || type_name;
  end if;

  return 'null';
end;
$$;

create or replace function e2e_insert_minimal(
  p_table_name text,
  p_id_column text,
  p_id_value text,
  p_server_id text,
  p_resource_name text,
  p_overrides jsonb default '{}'::jsonb
)
returns void
language plpgsql
as $$
declare
  col record;
  cols text[] := array[]::text[];
  vals text[] := array[]::text[];
  suffix text := replace(p_id_value, '-', '_');
begin
  if to_regclass(format('public.%I', p_table_name)) is null then
    return;
  end if;

  for col in
    select
      a.attname as column_name,
      a.atttypid::regtype as column_type,
      case when a.attnotnull then 'NO' else 'YES' end as is_nullable,
      pg_get_expr(ad.adbin, ad.adrelid) as column_default
    from pg_class t
    join pg_namespace n on n.oid = t.relnamespace
    join pg_attribute a on a.attrelid = t.oid
    left join pg_attrdef ad on ad.adrelid = t.oid and ad.adnum = a.attnum
    where n.nspname = 'public'
      and t.relname = p_table_name
      and a.attnum > 0
      and not a.attisdropped
    order by a.attnum
  loop
    if col.column_name = p_id_column then
      cols := array_append(cols, quote_ident(col.column_name));
      vals := array_append(vals, quote_literal(p_id_value));
    elsif p_overrides ? col.column_name then
      cols := array_append(cols, quote_ident(col.column_name));
      vals := array_append(vals, quote_literal(p_overrides ->> col.column_name));
    elsif col.column_name = 'serverId' then
      cols := array_append(cols, quote_ident(col.column_name));
      if p_server_id = '__e2e_null_server__' then
        vals := array_append(vals, 'null');
      else
        vals := array_append(vals, quote_literal(p_server_id));
      end if;
    elsif col.column_name = 'name' then
      cols := array_append(cols, quote_ident(col.column_name));
      vals := array_append(vals, quote_literal(p_resource_name));
    elsif col.column_name in ('createdAt', 'updatedAt') then
      cols := array_append(cols, quote_ident(col.column_name));
      vals := array_append(vals, 'now()');
    elsif col.is_nullable = 'NO' and col.column_default is null then
      cols := array_append(cols, quote_ident(col.column_name));
      vals := array_append(vals, e2e_literal_for(col.column_type, col.column_name, suffix));
    end if;
  end loop;

  execute format('delete from %I where %I = %L', p_table_name, p_id_column, p_id_value);
  execute format('insert into %I (%s) values (%s)', p_table_name, array_to_string(cols, ', '), array_to_string(vals, ', '));
end;
$$;

delete from "application" where "applicationId" = 'e2e-application';
delete from "compose" where "composeId" = 'e2e-compose';
delete from "compose" where "composeId" = :'local_compose_id';
delete from "postgres" where "postgresId" = 'e2e-postgres';
delete from "redis" where "redisId" = 'e2e-redis';
delete from "server" where "serverId" in (:'source_server', :'target_server');
delete from "environment" where "environmentId" = 'e2e-environment';
delete from "project" where "projectId" = 'e2e-project';
delete from "organization" where "id" = 'e2e-organization';
delete from "user" where "id" = 'e2e-user';

select e2e_insert_minimal('user', 'id', 'e2e-user', '', 'e2e user');
select e2e_insert_minimal('organization', 'id', 'e2e-organization', '', 'e2e organization', jsonb_build_object('owner_id', 'e2e-user'));
select e2e_insert_minimal('project', 'projectId', 'e2e-project', '', 'e2e project', jsonb_build_object('organizationId', 'e2e-organization'));
select e2e_insert_minimal('environment', 'environmentId', 'e2e-environment', '', 'e2e environment', jsonb_build_object('projectId', 'e2e-project'));

select e2e_insert_minimal('server', 'serverId', :'source_server', '', 'e2e dead server', jsonb_build_object('organizationId', 'e2e-organization'));
select e2e_insert_minimal('server', 'serverId', :'target_server', '', 'e2e live server', jsonb_build_object('organizationId', 'e2e-organization'));

select e2e_insert_minimal('application', 'applicationId', 'e2e-application', :'source_server', 'e2e app', jsonb_build_object('environmentId', 'e2e-environment'));
select e2e_insert_minimal('compose', 'composeId', 'e2e-compose', :'source_server', 'e2e compose', jsonb_build_object('environmentId', 'e2e-environment'));
select e2e_insert_minimal('compose', 'composeId', :'local_compose_id', :'local_server_sql_marker', 'e2e local compose', jsonb_build_object('environmentId', 'e2e-environment'));
select e2e_insert_minimal('postgres', 'postgresId', 'e2e-postgres', :'source_server', 'e2e postgres', jsonb_build_object('environmentId', 'e2e-environment'));
select e2e_insert_minimal('redis', 'redisId', 'e2e-redis', :'source_server', 'e2e redis', jsonb_build_object('environmentId', 'e2e-environment'));
SQL
}

run_migrator_flow() {
  log "running Migrator CLI plan/apply/rollback"
  local dsn="postgres://dokploy:${POSTGRES_PASSWORD}@127.0.0.1:${E2E_POSTGRES_PORT}/dokploy?sslmode=disable"
  local common_env=(
    "GOCACHE=$E2E_GO_CACHE"
    "MIGRATOR_BASIC_USER=e2e"
    "MIGRATOR_BASIC_PASSWORD=e2e"
    "MIGRATOR_ADMIN_TOKEN=e2e-token"
    "DOKPLOY_POSTGRES_DSN=$dsn"
    "MIGRATOR_STATE_PATH=$E2E_WORK_DIR/migrator-state.sqlite"
  )

  env "${common_env[@]}" go run ./cmd/dokploy-migrator plan \
    -source "$SOURCE_SERVER_ID" \
    -target "$TARGET_SERVER_ID" \
    -out "$PLAN_FILE"

  local job_id
  local schema_hash
  local row_count
  job_id="$(jq -r '.job.id' "$PLAN_FILE")"
  schema_hash="$(jq -r '.plan.schemaHash' "$PLAN_FILE")"
  row_count="$(jq -r '.plan.rows | length' "$PLAN_FILE")"

  if [ "$row_count" -lt 4 ]; then
    jq . "$PLAN_FILE"
    printf 'expected at least 4 plan rows, got %s\n' "$row_count" >&2
    exit 1
  fi

  env "${common_env[@]}" go run ./cmd/dokploy-migrator apply \
    -job "$job_id" \
    -plan "$PLAN_FILE" \
    -schema-hash-approval "$schema_hash" \
    -confirm APPLY

  assert_resources_on_server "$TARGET_SERVER_ID"

  env "${common_env[@]}" go run ./cmd/dokploy-migrator rollback \
    -job "$job_id" \
    -plan "$PLAN_FILE" \
    -schema-hash-approval "$schema_hash"

  assert_resources_on_server "$SOURCE_SERVER_ID"
}

run_remote_to_local_migrator_flow() {
  log "running Migrator CLI remote-to-local plan/apply/rollback"
  local dsn="postgres://dokploy:${POSTGRES_PASSWORD}@127.0.0.1:${E2E_POSTGRES_PORT}/dokploy?sslmode=disable"
  local common_env=(
    "GOCACHE=$E2E_GO_CACHE"
    "MIGRATOR_BASIC_USER=e2e"
    "MIGRATOR_BASIC_PASSWORD=e2e"
    "MIGRATOR_ADMIN_TOKEN=e2e-token"
    "DOKPLOY_POSTGRES_DSN=$dsn"
    "MIGRATOR_STATE_PATH=$E2E_WORK_DIR/migrator-state.sqlite"
  )

  env "${common_env[@]}" go run ./cmd/dokploy-migrator plan \
    -source "$SOURCE_SERVER_ID" \
    -target "$LOCAL_SERVER_ID" \
    -out "$REMOTE_TO_LOCAL_PLAN_FILE"

  local job_id
  local schema_hash
  local row_count
  job_id="$(jq -r '.job.id' "$REMOTE_TO_LOCAL_PLAN_FILE")"
  schema_hash="$(jq -r '.plan.schemaHash' "$REMOTE_TO_LOCAL_PLAN_FILE")"
  row_count="$(jq -r '.plan.rows | length' "$REMOTE_TO_LOCAL_PLAN_FILE")"

  if [ "$row_count" -lt 4 ]; then
    jq . "$REMOTE_TO_LOCAL_PLAN_FILE"
    printf 'expected at least 4 remote-to-local plan rows, got %s\n' "$row_count" >&2
    exit 1
  fi
  if ! jq -e --arg local "$LOCAL_SERVER_ID" \
    '.plan.targetServerId == $local and all(.plan.rows[]; .newServerId == $local)' \
    "$REMOTE_TO_LOCAL_PLAN_FILE" >/dev/null; then
    jq . "$REMOTE_TO_LOCAL_PLAN_FILE"
    printf 'expected all remote-to-local plan rows to target %s\n' "$LOCAL_SERVER_ID" >&2
    exit 1
  fi

  env "${common_env[@]}" go run ./cmd/dokploy-migrator apply \
    -job "$job_id" \
    -plan "$REMOTE_TO_LOCAL_PLAN_FILE" \
    -schema-hash-approval "$schema_hash" \
    -confirm APPLY

  assert_resources_on_local_server

  env "${common_env[@]}" go run ./cmd/dokploy-migrator rollback \
    -job "$job_id" \
    -plan "$REMOTE_TO_LOCAL_PLAN_FILE" \
    -schema-hash-approval "$schema_hash"

  assert_resources_on_server "$SOURCE_SERVER_ID"
}

run_local_migrator_flow() {
  log "running Migrator CLI local-server plan/apply/rollback"
  local dsn="postgres://dokploy:${POSTGRES_PASSWORD}@127.0.0.1:${E2E_POSTGRES_PORT}/dokploy?sslmode=disable"
  local common_env=(
    "GOCACHE=$E2E_GO_CACHE"
    "MIGRATOR_BASIC_USER=e2e"
    "MIGRATOR_BASIC_PASSWORD=e2e"
    "MIGRATOR_ADMIN_TOKEN=e2e-token"
    "DOKPLOY_POSTGRES_DSN=$dsn"
    "MIGRATOR_STATE_PATH=$E2E_WORK_DIR/migrator-state.sqlite"
  )

  env "${common_env[@]}" go run ./cmd/dokploy-migrator plan \
    -source "$LOCAL_SERVER_ID" \
    -target "$TARGET_SERVER_ID" \
    -out "$LOCAL_PLAN_FILE"

  local job_id
  local schema_hash
  local row_count
  job_id="$(jq -r '.job.id' "$LOCAL_PLAN_FILE")"
  schema_hash="$(jq -r '.plan.schemaHash' "$LOCAL_PLAN_FILE")"
  row_count="$(jq -r '.plan.rows | length' "$LOCAL_PLAN_FILE")"

  if [ "$row_count" -lt 1 ]; then
    jq . "$LOCAL_PLAN_FILE"
    printf 'expected at least 1 local-server plan row, got %s\n' "$row_count" >&2
    exit 1
  fi
  if ! jq -e --arg id "$LOCAL_COMPOSE_ID" --arg local "$LOCAL_SERVER_ID" \
    '.plan.rows[] | select(.table == "compose" and .id == $id and .oldServerId == $local)' \
    "$LOCAL_PLAN_FILE" >/dev/null; then
    jq . "$LOCAL_PLAN_FILE"
    printf 'expected local compose row %s in plan\n' "$LOCAL_COMPOSE_ID" >&2
    exit 1
  fi

  env "${common_env[@]}" go run ./cmd/dokploy-migrator apply \
    -job "$job_id" \
    -plan "$LOCAL_PLAN_FILE" \
    -schema-hash-approval "$schema_hash" \
    -confirm APPLY

  assert_local_compose_on_server "$TARGET_SERVER_ID"

  env "${common_env[@]}" go run ./cmd/dokploy-migrator rollback \
    -job "$job_id" \
    -plan "$LOCAL_PLAN_FILE" \
    -schema-hash-approval "$schema_hash"

  assert_local_compose_unassigned
}

assert_resources_on_server() {
  local expected_server="$1"
  log "verifying resources are on $expected_server"
  local count
  count="$(
    psql_e2e -At -v expected="$expected_server" <<'SQL'
select count(*) from (
  select "serverId" from "application" where "applicationId" = 'e2e-application' and "serverId" = :'expected'
  union all
  select "serverId" from "compose" where "composeId" = 'e2e-compose' and "serverId" = :'expected'
  union all
  select "serverId" from "postgres" where "postgresId" = 'e2e-postgres' and "serverId" = :'expected'
  union all
  select "serverId" from "redis" where "redisId" = 'e2e-redis' and "serverId" = :'expected'
) x;
SQL
  )"
  if [ "$count" != "4" ]; then
    printf 'expected 4 resources on %s, got %s\n' "$expected_server" "$count" >&2
    exit 1
  fi
}

assert_resources_on_local_server() {
  log "verifying resources are on the main local Dokploy server"
  local count
  count="$(
    psql_e2e -At <<'SQL'
select count(*) from (
  select "serverId" from "application" where "applicationId" = 'e2e-application' and "serverId" is null
  union all
  select "serverId" from "compose" where "composeId" = 'e2e-compose' and "serverId" is null
  union all
  select "serverId" from "postgres" where "postgresId" = 'e2e-postgres' and "serverId" is null
  union all
  select "serverId" from "redis" where "redisId" = 'e2e-redis' and "serverId" is null
) x;
SQL
  )"
  if [ "$count" != "4" ]; then
    printf 'expected 4 resources on the main local Dokploy server, got %s\n' "$count" >&2
    exit 1
  fi
}

assert_local_compose_on_server() {
  local expected_server="$1"
  log "verifying local compose moved to $expected_server"
  local count
  count="$(
    psql_e2e -At -v expected="$expected_server" -v local_compose_id="$LOCAL_COMPOSE_ID" <<'SQL'
select count(*) from "compose" where "composeId" = :'local_compose_id' and "serverId" = :'expected';
SQL
  )"
  if [ "$count" != "1" ]; then
    printf 'expected local compose on %s, got %s\n' "$expected_server" "$count" >&2
    exit 1
  fi
}

assert_local_compose_unassigned() {
  log "verifying local compose rolled back to serverId NULL"
  local count
  count="$(
    psql_e2e -At -v local_compose_id="$LOCAL_COMPOSE_ID" <<'SQL'
select count(*) from "compose" where "composeId" = :'local_compose_id' and "serverId" is null;
SQL
  )"
  if [ "$count" != "1" ]; then
    printf 'expected local compose with serverId NULL, got %s\n' "$count" >&2
    exit 1
  fi
}

cleanup_test_helpers() {
  log "dropping e2e helper functions"
  psql_e2e <<'SQL'
drop function if exists e2e_insert_minimal(text, text, text, text, text);
drop function if exists e2e_insert_minimal(text, text, text, text, text, jsonb);
drop function if exists e2e_literal_for(regtype, text, text);
SQL
}

main() {
  need docker
  need go
  need jq

  cd "$ROOT_DIR"
  mkdir -p "$E2E_SECRET_DIR" "$E2E_DOKPLOY_ETC" "$E2E_GO_CACHE"
  printf '%s' "$POSTGRES_PASSWORD" >"$E2E_SECRET_DIR/postgres_password"
  printf '%s' "$AUTH_SECRET" >"$E2E_SECRET_DIR/dokploy_auth_secret"

  trap cleanup EXIT

  log "starting official Dokploy stack for database e2e"
  compose up -d
  wait_for_postgres
  wait_for_dokploy_schema
  seed_real_schema
  run_migrator_flow
  run_remote_to_local_migrator_flow
  run_local_migrator_flow
  cleanup_test_helpers
  log "e2e passed"
}

main "$@"
