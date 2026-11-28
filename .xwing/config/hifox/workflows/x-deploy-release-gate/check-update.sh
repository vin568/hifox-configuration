#!/bin/sh
# Workflow function block: classify the releasable delta of x-deploy.
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
#   the losing concurrent cycle into "skip". The holder always clears it:
#   check-update on "skip" classification or its own failure, release-merge
#   via its EXIT trap on terminal outcomes; a stale marker past the TTL is
#   reclaimed by an atomic rename-aside, which elects exactly one reclaimer.
set -eu
exec 3>&1 1>&2

REMOTE="${XWING_DEPLOY_REMOTE:-https://cnb.cool/tars-lab/x-deploy}"
CACHE="${XWING_DEPLOY_CACHE_DIR:-$HOME/.cache/x-deploy-ci/repo.git}"
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
  MARKER_TS="$(head -1 "$MARKER" 2>/dev/null | tr -cd '0-9')"
  MARKER_TS="${MARKER_TS:-0}"
  NOW="$(date +%s)"
  if [ "$((NOW - MARKER_TS))" -lt "${XWING_DEPLOY_GATE_TTL:-10800}" ]; then
    printf '{"action":"skip"}\n' >&3
    exit 0
  fi
  # Stale marker: its cycle claimed the gate but never reached release-merge
  # (crash, or a "skip" emitted while still holding it). Election is a rename
  # aside — atomic, so exactly one reclaimer moves the file; losers' mv hits a
  # missing file and they skip. The reclaimer then re-reads the MOVED content:
  # the staleness decision above can race a concurrent claim (we may have
  # swept away a fresh winner's file, not the stale one we saw), so fresh
  # content is put back with a create-only copy — never clobbering whoever
  # claimed in between — and this cycle skips. Residual: a third claimant
  # landing inside that µs holding window can still double-claim with the
  # robbed owner; needs ≥3 concurrent cycles (production runs 2), and a
  # duplicate acceptance run is all it costs — agent dispatch serializes and
  # merge's superseded guards reject the second release.
  if mv "$MARKER" "$MARKER.stale.$$" 2>/dev/null; then
    ASIDE_TS="$(head -1 "$MARKER.stale.$$" 2>/dev/null | tr -cd '0-9')"
    ASIDE_TS="${ASIDE_TS:-0}"
    if [ "$((NOW - ASIDE_TS))" -lt "${XWING_DEPLOY_GATE_TTL:-10800}" ]; then
      ( set -C; cat "$MARKER.stale.$$" > "$MARKER" ) 2>/dev/null || :
      rm -f "$MARKER.stale.$$"
      printf '{"action":"skip"}\n' >&3
      exit 0
    fi
    rm -f "$MARKER.stale.$$"
  else
    printf '{"action":"skip"}\n' >&3
    exit 0
  fi
fi
( set -C; printf '%s\n' "$(date +%s)" > "$MARKER" ) 2>/dev/null || {
  printf '{"action":"skip"}\n' >&3
  exit 0
}

# This cycle owns the marker from here. Keep it held only when a downstream
# block will actually run (test / halt, cleared by release-merge); clear it
# on failure (a crashed check must not lock the gate for a full TTL) and on
# the skip classification at the bottom.
TMP=''
cleanup() {
  code=$?
  if [ -n "$TMP" ]; then
    git --git-dir "$CACHE" worktree remove --force "$TMP/wt" 2>/dev/null || rm -rf "$TMP"
    rm -rf "$TMP"
  fi
  if [ "$code" -ne 0 ]; then
    rm -f "$MARKER"
  fi
}
trap cleanup EXIT

# Real divergence (main-only content with no patch-equivalent on dev):
# auto-backmerge main into dev so the acceptance suite always tests a tree
# that is a superset of main. Conflicts stop the pipeline for a human.
if [ -n "$(left)" ]; then
  TMP="$(mktemp -d)"
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
  rm -f "$MARKER"
  printf '{"action":"skip"}\n' >&3
else
  printf '{"action":"test","candidate":"%s","main_at":"%s"}\n' "$CANDIDATE" "$MAIN_AT" >&3
fi
