---
name: XWing Handler Tests
description: Runs xwing server handler package tests against a disposable, auto-dropped PostgreSQL database derived from the task-local environment.
---

One script lives in this skill's `scripts/` directory. Run it with `bash <skill-dir>/scripts/<name>.sh` from the root of the task worktree; do not copy it into the xwing repository.

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
