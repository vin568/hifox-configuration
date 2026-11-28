#!/usr/bin/env bash
# Interim automation-sim bootstrap for task-local acceptance rounds.
#
# WHY: the Go gateway does not implement /rest/v1/wf/* — it reverse-proxies to
# the automation-sim engine (default http://127.0.0.1:3000). The agent-task
# stack has no sim slot yet (platform gap, HF-15952); first-class task-env
# support should replace this script entirely.
#
# MAINTAINABILITY CONTRACT — this script deliberately owns as little of the
# xwing interface as possible (HF-15957/15958 review):
#   1. The sim ENV BLOCK is NOT hand-assembled here. It is derived by xwing's
#      own `packages/automation-sim/derive-sim-env.mjs` from four task-shaped
#      inputs (SECRET_ENCRYPTION_KEY, DATABASE_URL, APP_SERVER_URL, APP_URL).
#      When xwing adds/renames sim env vars, derivation follows automatically.
#   2. NO explicit principal secret is minted: both the gateway
#      (automationSimPrincipalSecret()) and sim derive it from the SAME
#      SECRET_ENCRYPTION_KEY with the same HKDF label — parity is guaranteed
#      by construction, not by this script copying values around.
#   3. Port is fixed at 3000: the gateway's default upstream AND the sim dev
#      script (`next dev --port 3000`) both hardcode it. No --port knob.
#   4. Readiness is END-TO-END: sim root must answer 2xx, then a gateway-proxied
#      /rest/v1/wf route must answer non-503 (catches secret/upstream drift at
#      startup, not mid-suite).
#   5. The verified xwing rev is echoed into every ready line so drift between
#      rounds is diagnosable from the report.
#
# USAGE (after `make agent-task-start-web` has created the task database and
# runtime/auth.env — that ordering is inherent: sim shares the task database):
#   scripts/run-sim.sh --xwing <worktree> --task-env <worktree>/.task-env/<TASK_ID>
#   scripts/run-sim.sh ... --stop     # stop a previously started sim
#   scripts/run-sim.sh ... --check    # verify prerequisites AND that xwing's
#                                     # derivation still succeeds (contract probe)
set -euo pipefail

XWING=""
TASK_ENV=""
MODE="start"
while [ $# -gt 0 ]; do
  case "$1" in
    --xwing) XWING="$2"; shift 2 ;;
    --task-env) TASK_ENV="$2"; shift 2 ;;
    --stop) MODE="stop"; shift ;;
    --check) MODE="check"; shift ;;
    *) echo "run-sim: unknown flag $1" >&2; exit 2 ;;
  esac
done

fail() { echo "run-sim: $1" >&2; exit 1; }
[ -n "$XWING" ] || fail "--xwing <worktree> is required"
[ -n "$TASK_ENV" ] || fail "--task-env <dir> is required"
# Canonicalize to absolute paths: the launch subshell cd's into the app dir,
# so relative --task-env would write sim.pid/sim.log into the wrong place
# (HF-15962 field lesson).
case "$XWING" in /*) ;; *) XWING="$PWD/$XWING" ;; esac
case "$TASK_ENV" in /*) ;; *) TASK_ENV="$PWD/$TASK_ENV" ;; esac
SIM_DIR="$XWING/packages/automation-sim"
APP_DIR="$SIM_DIR/apps/sim"
DERIVE="$SIM_DIR/derive-sim-env.mjs"
ENV_FILE="$XWING/.env"
TASK_ENV_FILE="$TASK_ENV/.env.task"
AUTH_ENV="$TASK_ENV/runtime/auth.env"
PID_FILE="$TASK_ENV/sim.pid"
LOG_FILE="$TASK_ENV/sim.log"
PORT=3000

env_value() { grep -E "^$1=" "$2" | head -1 | cut -d= -f2- | tr -d '"' | tr -d "'"; }

if [ "$MODE" = "stop" ]; then
  if [ -f "$PID_FILE" ]; then
    PID="$(cat "$PID_FILE")"
    kill -- "-$PID" 2>/dev/null || kill "$PID" 2>/dev/null || true
    rm -f "$PID_FILE"
    echo "run-sim: stopped (pid $PID)"
  else
    echo "run-sim: nothing to stop"
  fi
  exit 0
fi

# --- prerequisites (fail fast, actionable) -------------------------------------
[ -f "$ENV_FILE" ] || fail "worktree .env missing (run make agent-task-prepare first)"
[ -d "$APP_DIR" ] || fail "sim app not found at $APP_DIR — xwing layout changed? (track HF-15952)"
[ -f "$DERIVE" ] || fail "derive-sim-env.mjs not found at $DERIVE — xwing layout changed? (track HF-15952)"
command -v bun >/dev/null 2>&1 || fail "bun not found — sim runs on bun (bun.lock). Install bun or attribute the workflow chain as environment-skipped."
[ -f "$TASK_ENV_FILE" ] || fail ".env.task missing at $TASK_ENV_FILE (run start-web first: sim shares the task database)"
DB_URL="$(env_value DATABASE_URL "$TASK_ENV_FILE")"
[ -n "$DB_URL" ] || fail "DATABASE_URL not found in .env.task"
MASTER_KEY="$(env_value SECRET_ENCRYPTION_KEY "$ENV_FILE")"
[ -n "$MASTER_KEY" ] || fail "SECRET_ENCRYPTION_KEY not found in worktree .env (both gateway and sim derive the principal secret from it)"
API_URL="$(env_value TASK_ENV_API_URL "$AUTH_ENV")"
# The four task-shaped inputs (HF-15962: derive-sim-env DERIVES from them but
# does not echo them back — put() only fills missing keys — so they must ride
# into the child process separately, or the sim boots without DATABASE_URL and
# its db instrumentation hook crashes (packages/db/db.ts:43) while the root
# stays 000 forever).
APP_SERVER_URL_VALUE="${API_URL:-http://127.0.0.1:20000}"
APP_URL_VALUE="http://127.0.0.1:$PORT"
derive_env() {
  ( cd "$SIM_DIR"
    export SECRET_ENCRYPTION_KEY="$MASTER_KEY" DATABASE_URL="$DB_URL" \
      APP_SERVER_URL="$APP_SERVER_URL_VALUE" \
      APP_URL="$APP_URL_VALUE" SIM_CONTAINER_TYPE=APP
    node ./derive-sim-env.mjs )
}
SIM_ENV_BLOCK="$(derive_env)" || { echo "$SIM_ENV_BLOCK" >&2; fail "derive-sim-env failed — the xwing sim env contract moved (see errors above; track HF-15952)"; }
VAR_COUNT="$(grep -c '^export ' <<<"$SIM_ENV_BLOCK" || true)"
[ "$VAR_COUNT" -ge 8 ] || { echo "$SIM_ENV_BLOCK" >&2; fail "derive-sim-env produced only $VAR_COUNT vars (expected the full sim block) — xwing contract moved? (track HF-15952)"; }

if [ "$MODE" = "check" ]; then
  echo "run-sim: check ok (bun $(bun --version), derived $VAR_COUNT vars, xwing $(git -C "$XWING" rev-parse --short HEAD 2>/dev/null || echo '?'))"
  exit 0
fi
API_TOKEN="$(env_value TASK_ENV_API_TOKEN "$AUTH_ENV")"

# Dependencies once per worktree (bun store makes repeats fast).
if [ ! -d "$SIM_DIR/node_modules" ]; then
  echo "run-sim: installing sim workspace dependencies (first run on this worktree)..."
  (cd "$SIM_DIR" && bun install) >> "$LOG_FILE" 2>&1 || { tail -20 "$LOG_FILE" >&2; fail "bun install failed (see $LOG_FILE)"; }
fi

# Detach as a session leader (setsid where present; macOS has none → python3
# os.setsid shim). Both branches EVAL the derived env block — the shim parses
# its `export K=V` lines (shlex handles quoting) instead of inheriting them,
# because a python child never sees shell evals. Environment = derived block +
# a minimal whitelist; nothing is inherited from the agent session (identity
# markers must never leak in). SIM_LOG_FILE is passed inline: the child's
# environment is fixed at exec, so a later `export` is invisible to it
# (HF-15961: exactly that KeyError killed the shim before Popen).
(
  cd "$APP_DIR"
  if command -v setsid >/dev/null 2>&1; then
    SECRET_ENCRYPTION_KEY="$MASTER_KEY" DATABASE_URL="$DB_URL" \
      APP_SERVER_URL="$APP_SERVER_URL_VALUE" APP_URL="$APP_URL_VALUE" \
      setsid bash -c "eval \"\$SIM_ENV_BLOCK\"; exec bun run dev" >> "$LOG_FILE" 2>&1 &
    echo $! > "$PID_FILE"
  else
    SIM_ENV_BLOCK="$SIM_ENV_BLOCK" SIM_LOG_FILE="$LOG_FILE" SIM_APP_DIR="$PWD" \
      SIM_IN_SECRET="$MASTER_KEY" SIM_IN_DB="$DB_URL" \
      SIM_IN_API="$APP_SERVER_URL_VALUE" SIM_IN_APP="$APP_URL_VALUE" \
      python3 - <<'PYEOF' &
import os, shlex, subprocess
os.setsid()
block = os.environ["SIM_ENV_BLOCK"]
env = {k: os.environ[k] for k in ("PATH", "HOME", "USER", "TMPDIR", "LANG", "LC_ALL") if os.environ.get(k)}
for line in block.splitlines():
    line = line.strip()
    if not line.startswith("export "):
        continue
    for part in shlex.split(line[len("export "):]):
        key, sep, value = part.partition("=")
        if sep and key:
            env[key] = value
# The four task inputs derive-sim-env consumes but does not echo back.
env.update({
    "SECRET_ENCRYPTION_KEY": os.environ["SIM_IN_SECRET"],
    "DATABASE_URL": os.environ["SIM_IN_DB"],
    "APP_SERVER_URL": os.environ["SIM_IN_API"],
    "APP_URL": os.environ["SIM_IN_APP"],
})
log = open(os.environ["SIM_LOG_FILE"], "a")
subprocess.Popen(["bun", "run", "dev"], stdout=log, stderr=log,
                 cwd=os.environ["SIM_APP_DIR"], env=env)
PYEOF
    echo $! > "$PID_FILE"
  fi
)



# --- readiness 1: sim root must be 2xx (a 500-ing app is NOT ready) --------------
echo "run-sim: waiting for healthy http://127.0.0.1:$PORT/ (2xx; budget 420s, next dev cold start is slow)..."
DEADLINE=$(( $(date +%s) + 420 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  CODE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "http://127.0.0.1:$PORT/" || true)"
  case "$CODE" in
    200|201|204) break ;;
  esac
  sleep 5
done
[ "$CODE" = "200" ] || [ "$CODE" = "201" ] || [ "$CODE" = "204" ] || { tail -30 "$LOG_FILE" >&2; fail "sim root not healthy within 420s (last http $CODE; log above)"; }

# --- readiness 2: END-TO-END through the gateway (drift catcher) ----------------
# A proxied /rest/v1/wf route answering anything other than 503 proves the
# gateway→sim wiring (upstream URL + derived principal parity). Auth may
# legitimately 401/403 for this probe — only 503 (upstream/principal) is fatal.
if [ -n "$API_URL" ] && [ -n "$API_TOKEN" ]; then
  WFCODE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 -H "Authorization: Bearer $API_TOKEN" "$API_URL/rest/v1/wf/workspaces" || true)"
  [ "$WFCODE" != "503" ] || { tail -20 "$LOG_FILE" >&2; fail "gateway /rest/v1/wf probe returned 503 — gateway↔sim wiring broken (secret/upstream drift); log above"; }
  echo "run-sim: gateway probe http $WFCODE (non-503 = wiring ok)"
fi

echo "run-sim: ready — sim http://127.0.0.1:$PORT (pid $(cat "$PID_FILE")), derived $VAR_COUNT vars, xwing $(git -C "$XWING" rev-parse --short HEAD 2>/dev/null || echo '?'), log $LOG_FILE"
