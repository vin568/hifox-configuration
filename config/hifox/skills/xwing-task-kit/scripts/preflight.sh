#!/usr/bin/env bash
# Report whether this machine has the tools an xwing task needs.
# Usage: preflight.sh [base] [hifox] [server] [db] [runtime] [cli]
# Exit 1 when a required tool is missing; optional tools never fail the check.
set -uo pipefail

missing=0

report() {
  printf '%-8s %-14s %s\n' "$1" "$2" "$3"
}

find_tool() {
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

require() {
  local name=$1 path
  if path="$(find_tool "$name")"; then
    report OK "$name" "$path"
  else
    report MISSING "$name" "required"
    missing=1
  fi
}

optional() {
  local name=$1 fallback=$2 path
  if path="$(find_tool "$name")"; then
    report OK "$name" "$path"
  else
    report OPTIONAL "$name" "missing; fallback: $fallback"
  fi
}

find_chrome() {
  local candidate
  for candidate in "${TASK_ENV_CHROME_EXECUTABLE:-}" "${CHROME_PATH:-}" \
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"; do
    if [ -n "$candidate" ] && [ -x "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  for candidate in google-chrome google-chrome-stable chromium chromium-browser; do
    if command -v "$candidate" > /dev/null 2>&1; then
      command -v "$candidate"
      return 0
    fi
  done
  return 1
}

check_db() {
  local pg_isready_bin
  require psql
  require pg_isready
  if pg_isready_bin="$(find_tool pg_isready)"; then
    if "$pg_isready_bin" -q -h "${PGHOST:-localhost}" -p "${PGPORT:-5432}"; then
      report OK postgres "accepting connections at ${PGHOST:-localhost}:${PGPORT:-5432}"
    else
      report WARN postgres "not reachable at ${PGHOST:-localhost}:${PGPORT:-5432}; scripts/ensure-postgres.sh may start the shared container"
    fi
  fi
}

surfaces=("$@")
[ ${#surfaces[@]} -gt 0 ] || surfaces=(base)

require git
require node
require pnpm
require make

db_checked=0
for surface in "${surfaces[@]}"; do
  case "$surface" in
    base) ;;
    hifox) require hifox ;;
    server) require go ;;
    cli) require cargo ;;
    db | runtime)
      if [ "$db_checked" = 0 ]; then
        check_db
        db_checked=1
      fi
      if [ "$surface" = runtime ]; then
        if chrome="$(find_chrome)"; then
          report OK chrome "$chrome"
        else
          report MISSING chrome "required for task-local runtime login"
          missing=1
        fi
      fi
      ;;
    *)
      echo "unknown surface: $surface (expected base, hifox, server, db, runtime, cli)" >&2
      exit 2
      ;;
  esac
done

optional agent-browser "use make agent-task-evidence-* only"
optional ffmpeg "mark recordings as not reviewed and rely on screenshots"

if [ "$missing" = 1 ]; then
  echo "preflight: required tools are missing; report them as a blocker and do not install them"
  exit 1
fi
echo "preflight: all required tools are present"
