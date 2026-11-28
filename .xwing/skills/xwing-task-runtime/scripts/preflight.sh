#!/usr/bin/env bash
# Report whether this machine has the tools an xwing task needs.
# Usage: preflight.sh [base] [hifox] [server] [db] [runtime] [cli] [ios] [android] [ci]
# Exit 1 when a required tool is missing; optional tools never fail the check.
set -uo pipefail

missing=0

report() {
  printf '%-8s %-14s %s\n' "$1" "$2" "$3"
}

find_tool() {
  local name=$1 root newest dir version
  if command -v "$name" > /dev/null 2>&1; then
    command -v "$name"
  elif [ -x "/opt/homebrew/opt/libpq/bin/$name" ]; then
    printf '/opt/homebrew/opt/libpq/bin/%s\n' "$name"
  elif [ -x "/usr/local/opt/libpq/bin/$name" ]; then
    printf '/usr/local/opt/libpq/bin/%s\n' "$name"
  else
    # Android SDK tools are commonly installed outside PATH.
    for root in "${ANDROID_HOME:-}" "${ANDROID_SDK_ROOT:-}" "$HOME/Library/Android/sdk"; do
      [ -n "$root" ] || continue
      case "$name" in
        adb)
          if [ -x "$root/platform-tools/adb" ]; then
            printf '%s\n' "$root/platform-tools/adb"
            return 0
          fi
          ;;
        emulator)
          if [ -x "$root/emulator/emulator" ]; then
            printf '%s\n' "$root/emulator/emulator"
            return 0
          fi
          ;;
        aapt2 | apksigner)
          # build-tools are versioned; newest wins. Sort the version strings alone
          # (a full path would defeat -k1,1n) with numeric per-component keys
          # because macOS BSD sort lacks -V.
          newest=""
          for dir in "$root"/build-tools/*/; do
            [ -d "$dir" ] || continue
            version="${dir%/}"
            newest="$newest${newest:+
}${version##*/}"
          done
          newest="$(printf '%s\n' "$newest" | sort -t. -k1,1n -k2,2n -k3,3n | tail -n 1)"
          if [ -n "$newest" ] && [ -x "$root/build-tools/$newest/$name" ]; then
            printf '%s\n' "$root/build-tools/$newest/$name"
            return 0
          fi
          ;;
      esac
    done
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

# Version check on top of the base presence check: node's presence is already
# reported by the base block, so only the major-version floor lives here.
check_node_major() {
  local minimum=$1 major
  command -v node > /dev/null 2>&1 || return 0
  major="$(node -p 'Number(process.versions.node.split(".")[0])' 2> /dev/null || printf 0)"
  if [ "$major" -ge "$minimum" ] 2> /dev/null; then
    report OK node "$(node --version) (>= ${minimum})"
  else
    report MISSING node "major >= ${minimum} required, found $(node --version)"
    missing=1
  fi
}

check_java_major() {
  local minimum=$1 java_bin major
  if [ -n "${JAVA_HOME:-}" ] && [ -x "$JAVA_HOME/bin/java" ]; then
    java_bin="$JAVA_HOME/bin/java"
  elif java_bin="$(find_tool java)"; then
    :
  else
    report MISSING java "required, JDK >= ${minimum} (set JAVA_HOME to a JDK ${minimum}+)"
    missing=1
    return 0
  fi
  major="$("$java_bin" -version 2>&1 | sed -n 's/.*version "\([0-9][0-9]*\).*/\1/p' | head -n 1)"
  if [ -n "$major" ] && [ "$major" -ge "$minimum" ] 2> /dev/null; then
    report OK java "$java_bin (major ${major})"
  else
    report MISSING java "major >= ${minimum} required, found ${major:-unknown} at ${java_bin}"
    missing=1
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
    ios)
      require xcrun
      require xcodebuild
      optional xcodegen "build an existing Hifox.xcodeproj; project.yml changes need xcodegen"
      ;;
    android)
      require adb
      check_java_major 17
      optional emulator "use a physical device over adb"
      optional aapt2 "skip APK badging checks and note it in the report"
      optional apksigner "skip signature verification and note it in the report"
      ;;
    ci)
      require yq
      require lsof
      require python3
      check_node_major 18
      optional playwright "skip the screenshot step and note it in the report"
      ;;
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
      echo "unknown surface: $surface (expected base, hifox, server, db, runtime, cli, ios, android, ci)" >&2
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
