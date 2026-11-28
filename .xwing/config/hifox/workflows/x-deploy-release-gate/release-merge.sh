#!/bin/sh
# Workflow function block: release the accepted candidate (dev -> main).
# Language: shell. Runs after the Tester agent (run_agent block).
#
# Engine contract:
# - '<check.result>' / '<tester.result>' are upstream block outputs: the
#   engine rewrites each reference into a runtime env-var expansion that is
#   quote-context aware (single-quoted sites become ''"${var}"''), so values
#   are never parsed as code and carry no quoting contract. check.result is
#   the single-line routing JSON (shell stdout semantics); tester.result is
#   the agent's final message (single-line JSON verdict per daizong
#   instructions).
# - '{{XWING_DEPLOY_PUSH_TOKEN}}' is a workspace variable bound at execution.
# - Routing JSON on fd 3 (stdout hygiene, same as check-update):
#   {"action":"released","merge_commit":..,"candidate":..}
#   {"action":"no-merge","verdict":..} | {"action":"superseded","reason":..}
set -eu
exec 3>&1 1>&2

REMOTE="${XWING_DEPLOY_REMOTE:-https://cnb.cool/tars-lab/x-deploy}"
CACHE="${XWING_DEPLOY_CACHE_DIR:-$HOME/.cache/x-deploy-ci/repo.git}"
# Cold start guard, same as check-update: fetch cannot initialize the cache.
[ -d "$CACHE" ] || git init --quiet --bare "$CACHE"
TOKEN='{{XWING_DEPLOY_PUSH_TOKEN}}'
GIT_USER="${XWING_DEPLOY_GIT_USER:-x-access-token}"
MARKER="$(dirname "$CACHE")/gate-running"
TMP="$(mktemp -d)"
trap 'git --git-dir "$CACHE" worktree remove --force "$TMP/wt" 2>/dev/null || rm -rf "$TMP"; rm -rf "$TMP"; rm -f "$MARKER"' EXIT
AUTHED="https://${GIT_USER}:${TOKEN}@$(printf '%s' "$REMOTE" | sed 's#^https://##')"


CHECK_JSON='<check.result>'
TESTER_RESULT='<tester.result>'
CANDIDATE="$(printf '%s' "$CHECK_JSON" | sed -n 's/.*"candidate":"\([0-9a-f]*\)".*/\1/p')"
MAIN_AT="$(printf '%s' "$CHECK_JSON" | sed -n 's/.*"main_at":"\([0-9a-f]*\)".*/\1/p')"
VERDICT="$(printf '%s' "$TESTER_RESULT" | sed -n 's/.*"verdict"[[:space:]]*:[[:space:]]*"\([a-z]*\)".*/\1/p')"
VERDICT="${VERDICT:-fail}"

if [ "$VERDICT" != "pass" ]; then
  printf '{"action":"no-merge","verdict":"%s"}\n' "$VERDICT" >&3
  exit 0
fi
if [ -z "$CANDIDATE" ] || [ -z "$MAIN_AT" ]; then
  printf '{"action":"no-merge","verdict":"malformed-input"}\n' >&3
  exit 0
fi

git --git-dir "$CACHE" fetch --quiet "$AUTHED" +refs/heads/main:refs/remotes/origin/main +refs/heads/dev:refs/remotes/origin/dev

DEV_NOW="$(git --git-dir "$CACHE" rev-parse refs/remotes/origin/dev)"
MAIN_NOW="$(git --git-dir "$CACHE" rev-parse refs/remotes/origin/main)"
if [ "$DEV_NOW" != "$CANDIDATE" ]; then
  printf '{"action":"superseded","reason":"dev-moved"}\n' >&3
  exit 0
fi
if [ "$MAIN_NOW" != "$MAIN_AT" ]; then
  printf '{"action":"superseded","reason":"main-moved"}\n' >&3
  exit 0
fi

# Merge commits are excluded from the divergence classifier (--no-merges), so
# this --no-ff merge never trips the next cycle's check.
# The single EXIT trap from the top already removes both the worktree and the
# run marker ($TMP expands when the trap fires, so re-registering here would
# only risk dropping the marker cleanup again).
mkdir -p "$TMP/wt"
git --git-dir "$CACHE" worktree add --detach --quiet "$TMP/wt" refs/remotes/origin/main
SHORT="$(printf '%s' "$CANDIDATE" | cut -c1-12)"
git -C "$TMP/wt" -c user.name=xwing-ci -c user.email=ci@invalid merge --no-edit --no-ff -m "release: x-deploy $SHORT" "$CANDIDATE"
git -C "$TMP/wt" push --quiet "$AUTHED" HEAD:main

git --git-dir "$CACHE" fetch --quiet "$AUTHED" +refs/heads/main:refs/remotes/origin/main
MERGE_COMMIT="$(git --git-dir "$CACHE" rev-parse refs/remotes/origin/main)"
printf '{"action":"released","merge_commit":"%s","candidate":"%s"}\n' "$MERGE_COMMIT" "$CANDIDATE" >&3
