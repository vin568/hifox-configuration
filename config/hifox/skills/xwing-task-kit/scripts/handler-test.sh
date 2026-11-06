#!/usr/bin/env bash
# Run server/internal/handler tests against a disposable PostgreSQL database.
# Usage: handler-test.sh --task <task-key> [--keep-db] [-- <go test args>]
# Run from the task worktree root after `make agent-task-prepare TASK_ID=<task-key>`.
set -euo pipefail

usage() {
  echo "usage: handler-test.sh --task <task-key> [--keep-db] [-- <go test args>]" >&2
  exit 2
}

task=""
keep_db=0
while [ $# -gt 0 ]; do
  case "$1" in
    --task)
      [ $# -ge 2 ] || usage
      task=$2
      shift 2
      ;;
    --keep-db)
      keep_db=1
      shift
      ;;
    --)
      shift
      break
      ;;
    *) usage ;;
  esac
done
[ -n "$task" ] || usage

if [ ! -f server/go.mod ] || [ ! -f scripts/ensure-postgres.sh ]; then
  echo "handler-test: run from the xwing task worktree root" >&2
  exit 2
fi

task_dir=".task-env/$(printf '%s' "$task" | tr '[:upper:]' '[:lower:]')"
task_env="$task_dir/.env.task"
if [ ! -f "$task_env" ]; then
  echo "handler-test: $task_env not found; run make agent-task-prepare TASK_ID=$task first" >&2
  exit 2
fi

# Read single keys in a subshell so the rest of the task env never leaks into this process.
task_var() {
  (
    # shellcheck disable=SC1090
    . "$task_env"
    eval "printf '%s' \"\${$1-}\""
  )
}

pg_user="$(task_var POSTGRES_USER)"
pg_password="$(task_var POSTGRES_PASSWORD)"
pg_host="$(task_var POSTGRES_HOST)"
pg_port="$(task_var POSTGRES_PORT)"
pg_db="$(task_var POSTGRES_DB)"
task_url="$(task_var DATABASE_URL)"
if [ -z "$pg_db" ] || [ -z "$task_url" ]; then
  echo "handler-test: $task_env has no POSTGRES_DB / DATABASE_URL" >&2
  exit 2
fi

case "$pg_host" in
  localhost | 127.0.0.1 | ::1) ;;
  *)
    echo "handler-test: refusing a non-local PostgreSQL host" >&2
    exit 2
    ;;
esac

find_pg_tool() {
  local name=$1
  if command -v "$name" > /dev/null 2>&1; then
    command -v "$name"
  elif [ -x "/opt/homebrew/opt/libpq/bin/$name" ]; then
    printf '/opt/homebrew/opt/libpq/bin/%s\n' "$name"
  elif [ -x "/usr/local/opt/libpq/bin/$name" ]; then
    printf '/usr/local/opt/libpq/bin/%s\n' "$name"
  else
    return 1
  fi
}
psql_bin="$(find_pg_tool psql)" || {
  echo "handler-test: psql not found" >&2
  exit 2
}

# PostgreSQL identifiers are limited to 63 bytes.
test_db="${pg_db:0:40}_ht_$(date +%s)_$$"
url_base="${task_url%%\?*}"
url_query=""
case "$task_url" in *\?*) url_query="?${task_url#*\?}" ;; esac
server_url="${url_base%/*}"
test_url="$server_url/$test_db$url_query"
admin_url="$server_url/postgres$url_query"

work_dir="$(mktemp -d)"
test_env="$work_dir/test.env"
migrate_log="$work_dir/migrate.log"
created=0

cleanup() {
  local status=$?
  if [ "$created" = 1 ]; then
    if [ "$keep_db" = 1 ]; then
      echo "handler-test: kept database $test_db; drop it before handoff" >&2
    elif ! "$psql_bin" -w -q -d "$admin_url" -v ON_ERROR_STOP=1 \
      -c "DROP DATABASE IF EXISTS \"$test_db\" WITH (FORCE)" > /dev/null; then
      echo "handler-test: failed to drop database $test_db; drop it manually" >&2
      status=1
    fi
  fi
  rm -rf "$work_dir"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

umask 077
printf 'POSTGRES_USER=%q\nPOSTGRES_PASSWORD=%q\nPOSTGRES_HOST=%q\nPOSTGRES_PORT=%q\nPOSTGRES_DB=%q\nDATABASE_URL=%q\n' \
  "$pg_user" "$pg_password" "$pg_host" "$pg_port" "$test_db" "$test_url" > "$test_env"

created=1
bash scripts/ensure-postgres.sh "$test_env"

echo "==> Migrating $test_db"
if ! (cd server && DATABASE_URL="$test_url" go run ./cmd/migrate up) > "$migrate_log" 2>&1; then
  echo "handler-test: migration failed; last log lines:" >&2
  tail -n 40 "$migrate_log" >&2
  exit 1
fi

# Strip the HiFox agent runtime variables, as scripts/check.sh does before go test.
scrub=()
while IFS= read -r name; do
  case "$name" in
    APP_* | SHARKLY_* | CODEX_* | OPENCODE_* | HERMES_* | OPENCLAW_* | LLM_DEFAULT_MODEL) scrub+=(-u "$name") ;;
  esac
done < <(compgen -e)

echo "==> go test ./internal/handler/ $*"
set +e
(cd server && env ${scrub[@]+"${scrub[@]}"} DATABASE_URL="$test_url" go test ./internal/handler/ "$@")
test_status=$?
set -e
exit "$test_status"
