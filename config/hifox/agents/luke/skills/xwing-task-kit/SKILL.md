---
name: xwing Task Kit
description: Scripts for xwing task-mode delivery. Use at task intake to check that the machine has the tools the task's change surfaces need, and whenever server handler package tests must run against a database.
---

Two scripts live in this skill's `scripts/` directory. Run them with `bash <skill-dir>/scripts/<name>.sh` from the root of the task worktree; do not copy them into the xwing repository.

## Environment preflight: `preflight.sh`

Run once per task at intake (P0), before writing the research report or starting a simple task. Pass every surface the task touches:

```bash
bash <skill-dir>/scripts/preflight.sh base hifox server db runtime
```

| Surface | Required tools |
|---|---|
| `base` (always checked) | `git`, `node`, `pnpm`, `make` |
| `hifox` | `hifox` |
| `server` | `go` |
| `db` | `psql`, `pg_isready`; PostgreSQL reachability is reported as a warning because `scripts/ensure-postgres.sh` may start the shared container |
| `runtime` | Google Chrome (or `TASK_ENV_CHROME_EXECUTABLE` / `CHROME_PATH`) plus everything in `db` |
| `cli` | `cargo` |

Optional tools are always reported: `agent-browser` (custom browser interaction) and `ffmpeg` (recording keyframe review).

Exit code `1` means a required tool is missing. Do not install anything: report the missing tools as a blocker. For an optional tool, use the documented fallback (`make agent-task-evidence-*` only, or screenshots with the recording marked "not reviewed") and state it in the report.

## Handler tests: `handler-test.sh`

`server/internal/handler` has a database-writing `TestMain` that needs an explicit `DATABASE_URL` to a migrated, dedicated database. Its fixture uses fixed identities and deletes the previous fixture first, so two tasks sharing one database break each other, and the task-local runtime database must not be used. This script creates a disposable database per run, migrates it, runs the tests, and always drops it.

Prerequisite: `make agent-task-prepare TASK_ID=<task-key>` has created `.task-env/<task-id>/.env.task`; the script reuses only its PostgreSQL connection.

```bash
bash <skill-dir>/scripts/handler-test.sh --task <task-key> -- -run '^TestImportSkill' -count=1
```

- Arguments after `--` go to `go test ./internal/handler/`; without them the whole package runs.
- The database is `<task-local database>_ht_<timestamp>` on the same local server. Remote hosts are refused.
- The test process runs without the HiFox agent runtime variables (`APP_*`, legacy-prefixed variables, `CODEX_*`, `OPENCODE_*`, `HERMES_*`, `OPENCLAW_*`, `LLM_DEFAULT_MODEL`), matching what `scripts/check.sh` strips before `go test`.
- Migration output goes to a temporary log and is shown only when migration fails. The connection string is never printed.
- `--keep-db` skips the drop for debugging; drop the database yourself afterwards. Never leave it behind at handoff.
- The exit code is the `go test` exit code, or non-zero when setup fails.
