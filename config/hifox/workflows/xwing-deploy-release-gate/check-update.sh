#!/bin/sh
# Workflow function block: classify the releasable delta of xwing-deploy.
# Language: shell (runs on the workflow computer via the sim runner).
#
# Engine contract:
# - '{{XWING_DEPLOY_PUSH_TOKEN}}' is a workspace variable; the engine binds
#   its value at execution time (graph.json stores only the placeholder).
# - Shell block output semantics: block `result` = cleaned stdout. ALL
#   diagnostics go to stderr (fd 1 redirected); the single-line routing JSON
#   is written to fd 3 so `result` is exactly that JSON.
# - Routing JSON consumed by the Route condition via JSON.parse(<check.result>).
#   {"action":"skip"} | {"action":"test","candidate":..,"main_at":..}
#   | {"action":"halt","reason":..}
# - Run-marker guard: the workflow is deployed by BOTH hifox environments
#   (two instances, two schedules); an atomic noclobber marker with TTL turns
#   the losing concurrent cycle into "skip". release-merge clears it.
set -eu
exec 3>&1 1>&2

REMOTE="${XWING_DEPLOY_REMOTE:-https://cnb.cool/tars-lab/x-deploy}"
CACHE="${XWING_DEPLOY_CACHE_DIR:-$HOME/.cache/xwing-deploy-ci/repo.git}"
# Cold start: nothing else creates the bare cache, and fetch refuses to
# initialize it — a missing cache would abort every scheduled run.
[ -d "$CACHE" ] || git init --quiet --bare "$CACHE"
TOKEN='{{XWING_DEPLOY_PUSH_TOKEN}}'
GIT_USER="${XWING_DEPLOY_GIT_USER:-x-access-token}"
AUTHED="https://${GIT_USER}:${TOKEN}@$(printf '%s' "$REMOTE" | sed 's#^https://##')"

# Token hygiene: fetch/push against the authed URL only; the bare cache never
# stores credentials (no remote is configured on it).
mkdir -p "$(dirname "$CACHE")"
git --git-dir "$CACHE" fetch --quiet "$AUTHED" +refs/heads/main:refs/remotes/origin/main +refs/heads/dev:refs/remotes/origin/dev

left()  { git --git-dir "$CACHE" rev-list --left-only  --cherry-pick --no-merges origin/main...origin/dev; }
right() { git --git-dir "$CACHE" rev-list --right-only --cherry-pick --no-merges origin/main...origin/dev; }

GATE_DIR="$(dirname "$CACHE")"
MARKER="$GATE_DIR/gate-running"
if [ -f "$MARKER" ]; then
  MARKER_TS="$(head -1 "$MARKER" 2>/dev/null || echo 0)"
  NOW="$(date +%s)"
  if [ "$((NOW - MARKER_TS))" -lt "${XWING_DEPLOY_GATE_TTL:-10800}" ]; then
    printf '{"action":"skip"}\n' >&3
    exit 0
  fi
fi
( set -C; printf '%s\n' "$(date +%s)" > "$MARKER" ) 2>/dev/null || {
  printf '{"action":"skip"}\n' >&3
  exit 0
}

# Real divergence (main-only content with no patch-equivalent on dev):
# auto-backmerge main into dev so the acceptance suite always tests a tree
# that is a superset of main. Conflicts stop the pipeline for a human.
if [ -n "$(left)" ]; then
  TMP="$(mktemp -d)"
  trap 'git --git-dir "$CACHE" worktree remove --force "$TMP/wt" 2>/dev/null || rm -rf "$TMP"; rm -rf "$TMP"' EXIT
  mkdir -p "$TMP/wt"
  git --git-dir "$CACHE" worktree add --detach --quiet "$TMP/wt" refs/remotes/origin/dev
  if ! git -C "$TMP/wt" -c user.name=xwing-ci -c user.email=ci@invalid merge --no-edit --no-ff refs/remotes/origin/main >/dev/null 2>&1; then
    printf '{"action":"halt","reason":"backmerge-conflict"}\n' >&3
    exit 0
  fi
  git -C "$TMP/wt" push --quiet "$AUTHED" HEAD:dev
  # The convergence re-check below reads refs/remotes/origin/dev from the
  # cache: refresh it from the just-pushed tip, or it still sees the
  # pre-backmerge state and every real divergence halts spuriously.
  git --git-dir "$CACHE" fetch --quiet "$AUTHED" +refs/heads/dev:refs/remotes/origin/dev
fi

if [ -n "$(left)" ]; then
  printf '{"action":"halt","reason":"backmerge-did-not-converge"}\n' >&3
  exit 0
fi

CANDIDATE="$(git --git-dir "$CACHE" rev-parse refs/remotes/origin/dev)"
MAIN_AT="$(git --git-dir "$CACHE" rev-parse refs/remotes/origin/main)"

if [ -z "$(right)" ]; then
  printf '{"action":"skip"}\n' >&3
else
  printf '{"action":"test","candidate":"%s","main_at":"%s"}\n' "$CANDIDATE" "$MAIN_AT" >&3
fi
