#!/usr/bin/env bash
# Starts the backend from a checkout exactly as a new reader would (cp .env.example .env, then
# docker compose up --build, without the dev override) and checks it end to end:
#
#   1. the whole flow, run twice at the same time (flow.sh full)
#   2. the retention job (compose profile "maintenance")
#   3. the price provider cannot be reached (flow.sh provider-down)
#   4. production configuration that is not valid: the API must refuse to start
#   5. a broken migration: the API must not start and the migration must leave no trace
#
# Usage: scripts/e2e/stack-check.sh [-p PROJECT] DIR
#
# DIR is a checkout (a fresh clone, or the repository itself in CI); it must not have a .env, which is
# created there. Everything runs in its own Compose project (default stock-check) on its own ports, with
# its own images and volumes, and `down -v` at the end removes only those. The project of a normal
# `docker compose up` (stock-portfolio) is refused so this can never touch your development stack.
set -euo pipefail

PROJECT=stock-check
while getopts 'p:' opt; do
  case $opt in
    p) PROJECT=$OPTARG ;;
    *)
      echo "usage: $0 [-p PROJECT] DIR" >&2
      exit 2
      ;;
  esac
done
shift $((OPTIND - 1))
[[ $# -eq 1 ]] || {
  echo "usage: $0 [-p PROJECT] DIR" >&2
  exit 2
}

if [[ $PROJECT == stock-portfolio ]]; then
  echo "refusing project stock-portfolio: that is the development stack, and this script deletes its volumes" >&2
  exit 2
fi
DIR=$(cd "$1" && pwd)
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
[[ -f $DIR/.env.example && -f $DIR/docker-compose.yml ]] || {
  echo "$DIR is not a checkout of this repository" >&2
  exit 2
}
if [[ -e $DIR/.env ]]; then
  echo "refusing to overwrite $DIR/.env: run this on a fresh clone" >&2
  exit 2
fi

API_PORT=${STACK_CHECK_API_PORT:-18080}
MAILPIT_UI_PORT=${STACK_CHECK_MAILPIT_UI_PORT:-18025}
MAILPIT_SMTP_PORT=${STACK_CHECK_MAILPIT_SMTP_PORT:-11025}
POSTGRES_PORT=${STACK_CHECK_POSTGRES_PORT:-15432}
API_ORIGIN=http://localhost:$API_PORT

# Nothing from the caller's environment (for example the exported contents of a developer's .env,
# when started through make) may reach Compose: it would win over the .env created below.
while IFS='=' read -r key _; do
  [[ $key =~ ^[A-Z][A-Z0-9_]*$ ]] && unset "$key"
done <"$DIR/.env.example"
unset API_HOST_PORT MAILPIT_UI_HOST_PORT MAILPIT_SMTP_HOST_PORT API_IMAGE MAINTENANCE_IMAGE

cp "$DIR/.env.example" "$DIR/.env"
set_env() { # KEY VALUE
  grep -v "^$1=" "$DIR/.env" >"$DIR/.env.next" || true
  echo "$1=$2" >>"$DIR/.env.next"
  mv "$DIR/.env.next" "$DIR/.env"
}
set_env PUBLIC_ORIGIN "$API_ORIGIN"
set_env POSTGRES_HOST_PORT "$POSTGRES_PORT"
set_env API_HOST_PORT "$API_PORT"
set_env MAILPIT_UI_HOST_PORT "$MAILPIT_UI_PORT"
set_env MAILPIT_SMTP_HOST_PORT "$MAILPIT_SMTP_PORT"

WORK=$(mktemp -d)
BROKEN_MIGRATION="$DIR/migrations/99999_stack_check_broken.sql"

dc() {
  (cd "$DIR" && API_IMAGE="$PROJECT-api:local" MAINTENANCE_IMAGE="$PROJECT-maintenance:local" \
    docker compose -p "$PROJECT" -f docker-compose.yml --profile maintenance "$@")
}

cleanup() {
  local status=$?
  if [[ $status -ne 0 ]]; then
    echo "--- stack-check failed; last log lines of the stack" >&2
    dc logs --no-color --tail 40 api migrate 2>&1 | sed 's/^/    /' >&2 || true
  fi
  rm -f "$BROKEN_MIGRATION"
  dc down -v --remove-orphans >/dev/null 2>&1 || true
  docker image rm -f "$PROJECT-api:local" "$PROJECT-maintenance:local" >/dev/null 2>&1 || true
  rm -rf "$WORK" "$DIR/.env.next"
  exit "$status"
}
trap cleanup EXIT

say() { echo "== $*"; }
die() {
  echo "FAIL: $*" >&2
  exit 1
}

wait_ready() { # SECONDS
  local deadline=$((SECONDS + $1))
  until [[ $(curl -s -o /dev/null -w '%{http_code}' "$API_ORIGIN/readyz" || true) == 200 ]]; do
    ((SECONDS < deadline)) || die "the API was not ready at $API_ORIGIN/readyz within $1 seconds"
    sleep 2
  done
}

flow() { API_ORIGIN=$API_ORIGIN MAILPIT_URL=http://localhost:$MAILPIT_UI_PORT "$HERE/flow.sh" "$@"; }

say "1/5 start the stack and run the whole flow twice at once"
dc up --build -d postgres migrate api mailpit
wait_ready 180
flow full >"$WORK/flow1.log" 2>&1 &
first=$!
flow full >"$WORK/flow2.log" 2>&1 &
second=$!
flows_ok=true
wait "$first" || flows_ok=false
wait "$second" || flows_ok=false
cat "$WORK/flow1.log" "$WORK/flow2.log"
$flows_ok || die "a flow failed"

say "2/5 retention job"
dc run --rm maintenance >"$WORK/maintenance.log" 2>&1 || {
  cat "$WORK/maintenance.log"
  die "the maintenance job failed"
}
grep -q '"event":"maintenance.retention_finished"' "$WORK/maintenance.log" || {
  cat "$WORK/maintenance.log"
  die "the maintenance job did not report retention_finished"
}

say "3/5 price provider cannot be reached"
(
  export PRICE_PROVIDER=alpaca ALPACA_KEY=stack-check-key ALPACA_SECRET=stack-check-secret
  export ALPACA_TRADING_BASE_URL=http://127.0.0.1:9 ALPACA_DATA_BASE_URL=http://127.0.0.1:9
  dc up -d --force-recreate --no-deps api
)
wait_ready 60
flow provider-down
dc up -d --force-recreate --no-deps api
wait_ready 60

say "4/5 production configuration that is not valid"
(
  export APP_ENV=production
  dc up -d --force-recreate --no-deps api
)
deadline=$((SECONDS + 30))
# Read the logs into a file first: `dc logs | grep -q` would fail under pipefail when grep quits early.
until dc logs --no-color api >"$WORK/api-production.log" 2>&1 && grep -q 'CSRF_SECRET' "$WORK/api-production.log"; do
  ((SECONDS < deadline)) || die "the API did not report the invalid production configuration in 30 seconds"
  sleep 1
done
[[ $(curl -s -o /dev/null -w '%{http_code}' "$API_ORIGIN/readyz" || true) != 200 ]] || die "the API serves with a production configuration that must be refused"
if grep -q 'dev-only-\|dev_runtime_password\|dev_admin_password' "$WORK/api-production.log"; then
  die "the production configuration error leaks a configured value"
fi
dc stop api >/dev/null
dc up -d --force-recreate --no-deps api
wait_ready 60

say "5/5 a migration that fails"
dc stop api >/dev/null
printf -- '-- +goose Up\nSELECT broken_on_purpose;\n' >"$BROKEN_MIGRATION"
dc build migrate >/dev/null
if dc up -d --force-recreate >"$WORK/broken.log" 2>&1; then
  cat "$WORK/broken.log"
  die "docker compose up succeeded although a migration failed"
fi
running=$(dc ps --status running --services)
if grep -qx api <<<"$running"; then
  die "the API is running although a migration failed"
fi
# The variables are the container's own, so they must expand inside it, not here.
# shellcheck disable=SC2016
recorded=$(dc exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "SELECT count(*) FROM app.goose_db_version WHERE version_id = 99999"')
[[ $recorded == 0 ]] || die "the failed migration was recorded as applied"

say "stack check passed"
