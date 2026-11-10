#!/usr/bin/env bash
# hifox-configuration 的 git worktree 助手（根仓 + 双子模块）。
# 用法: worktree.sh add <name> [-b <branch>] | list | remove <name> [--force] | prune
# 契约与机制说明见同目录 SKILL.md；仅在主检出运行。
set -euo pipefail

die() { echo "error: $*" >&2; exit 1; }
info() { echo "==> $*"; }

sub_paths() { git config -f .gitmodules --get-regexp '^submodule\..*\.path$' | cut -d' ' -f2; }
sub_branch() { git config -f .gitmodules --get "submodule.$1.branch" 2>/dev/null || true; }

main_worktree_only() {
  local git_dir common_dir
  git_dir=$(cd "$(git rev-parse --git-dir)" && pwd)
  common_dir=$(cd "$(git rev-parse --git-common-dir)" && pwd)
  [ "$git_dir" = "$common_dir" ] || die "请在主检出运行（当前在 linked worktree）"
}

worktree_dir() {
  local root
  root=$(git rev-parse --show-toplevel)
  printf '%s/../%s-%s' "$root" "$(basename "$root")" "$1"
}

usage() { sed -n '2,4p' "$0" | sed 's/^# \{0,1\}//'; exit 0; }

cmd_add() {
  local name="${1:-}" branch=""
  [ -n "$name" ] || die "add 需要 <name>"
  shift
  while [ $# -gt 0 ]; do
    case "$1" in
      -b) branch="${2:-}"; [ -n "$branch" ] || die "-b 需要分支名"; shift 2 ;;
      *) die "未知选项: $1" ;;
    esac
  done
  main_worktree_only
  branch="${branch:-feature/$name}"
  local dir
  dir=$(worktree_dir "$name")
  [ -e "$dir" ] && die "目标目录已存在: $dir"
  local base
  base=$(git symbolic-ref --short refs/remotes/origin/HEAD 2>/dev/null | sed 's|^origin/||' || echo main)

  if git show-ref --verify --quiet "refs/heads/$branch"; then
    info "挂载已有分支 $branch 到 $dir"
    git worktree add "$dir" "$branch"
  else
    info "创建 worktree ${dir}（新分支 ${branch}，基于 ${base}）"
    git worktree add -b "$branch" "$dir" "$base"
  fi

  info "初始化子模块（从远端克隆，需网络）"
  git -C "$dir" submodule update --init

  local path branch_name
  while IFS= read -r path; do
    branch_name=$(sub_branch "$path")
    if [ -n "$branch_name" ]; then
      info "子模块 $path -> 特性分支 ${branch}（基于 origin/${branch_name}，WIP 不进 ${branch_name}）"
      git -C "$dir/$path" switch --no-track -c "$branch" "origin/$branch_name"
    fi
  done < <(sub_paths)

  local root
  root=$(git rev-parse --show-toplevel)
  if [ -f "$root/vendor/xwing-deploy/.env.e2e" ] && [ ! -e "$dir/vendor/xwing-deploy/.env.e2e" ]; then
    cp "$root/vendor/xwing-deploy/.env.e2e" "$dir/vendor/xwing-deploy/.env.e2e"
    info "已复制 .env.e2e（机器本地，不入库）"
  fi

  dir=$(cd "$dir" && pwd)
  echo "就绪清单：cd $dir && mise install; (cd vendor/xwing-deploy && npm install); ./xwing-deploy doctor"
  echo "并回 dev（特性完成时）：cd vendor/xwing-deploy && npm test && git fetch origin dev && git merge origin/dev && git push origin HEAD:dev"
}

cmd_list() {
  git worktree list
  local dir path short branch
  while IFS= read -r dir; do
    for path in $(sub_paths); do
      [ -e "$dir/$path/.git" ] || continue
      short=$(git -C "$dir/$path" rev-parse --short HEAD)
      branch=$(git -C "$dir/$path" branch --show-current)
      echo "  $(basename "$dir")/$path @ $short (${branch:-detached})"
    done
  done < <(git worktree list --porcelain | cut -d' ' -f2 | grep -v '^$')
}

cmd_remove() {
  local name="${1:-}" force="${2:-}"
  [ -n "$name" ] || die "remove 需要 <name>"
  [ "$force" = "--force" ] || force=""
  main_worktree_only
  local dir
  dir=$(worktree_dir "$name")
  [ -d "$dir" ] || die "worktree 目录不存在: $dir"

  if [ -z "$force" ]; then
    local dirty=0 path
    [ -n "$(git -C "$dir" status --porcelain)" ] && { git -C "$dir" status --porcelain; dirty=1; }
    for path in $(sub_paths); do
      [ -e "$dir/$path/.git" ] || continue
      [ -n "$(git -C "$dir/$path" status --porcelain)" ] && { echo "[$path]"; git -C "$dir/$path" status --porcelain; dirty=1; }
    done
    [ "$dirty" = 1 ] && die "存在未提交改动（见上）；确认丢弃用 --force"
  fi

  # 子模块在位时 git worktree remove 无 --force 一律 fatal（git 2.50 实测）；脏检由上方承担。
  git worktree remove --force "$dir"
  info "已移除 ${dir}（分支保留）"
}

cmd_prune() { git worktree prune -v && info "已清理失联 worktree 元数据"; }

case "${1:-}" in
  add) shift; cmd_add "$@" ;;
  list) cmd_list ;;
  remove) shift; cmd_remove "$@" ;;
  prune) cmd_prune ;;
  -h|--help|help) usage ;;
  *) die "未知命令: ${1:-}（add <name> [-b <branch>] | list | remove <name> [--force] | prune）" ;;
esac
