---
name: Maintainer Review
description: Review code changes with a senior maintainer lens. Use when the user asks Codex to review a commit, diff, PR, branch, staged changes, working tree changes, or a specified git range, especially when the review should infer the minimal external behavior goal from the diff and check architecture fit, mechanism reuse, and change scope before judging implementation details.
---
Review code changes only. Do not modify files, do not commit, and do not rewrite the implementation unless the user explicitly asks for fixes after the review.

Do not invoke other specialized skills or plugins as part of this review unless the user explicitly asks to combine them. Use ordinary local code-reading tools, git commands, search, and tests when useful.

## Review Range

Determine the review range from the user's request.

Use the explicit range when provided, for example:

- `HEAD^..HEAD`
- `main..HEAD`
- `origin/main...HEAD`
- staged changes
- unstaged changes
- a specific commit hash
- a PR branch or named branch

If the user does not specify a range, default to the last commit:

```bash
git diff HEAD^..HEAD
```

Inspect commit metadata when useful:

```bash
git show --stat --oneline --decorate HEAD
git show --name-only --format=fuller HEAD
```

If the repository has uncommitted changes and the requested range is commit-based, do not include them unless the user asked to review working tree changes. Mention their presence only if they may affect the review.

If the current directory is not a git repository or the requested range cannot be resolved, ask one concise clarification question.

## Review Workflow

First infer the smallest plausible external behavior goal from the diff and nearby context. Do not treat every implementation choice in the diff as a requirement.

Before forming conclusions, inspect relevant context beyond the diff:

- direct callers and callees
- same-directory or same-module implementations
- existing configuration entry points
- shared helpers, abstractions, middleware, adapters, or service boundaries
- tests covering similar behavior
- prior patterns for the same kind of change

Use fast local search first, especially `rg` and `rg --files`.

Review with a maintainer bias toward existing architecture. Prefer existing mechanisms unless the inferred requirement truly needs a new path, abstraction, configuration surface, or behavior boundary.

Run focused tests when they are cheap, relevant, and available. If tests are not run, state that briefly in the final review only when it affects confidence.

## What To Check

Focus on concrete risks, not style preferences.

Check:

- Does the change satisfy the inferred minimal external behavior goal?
- Is there a smaller change that would satisfy that goal?
- Does it reuse existing mechanisms, helpers, configuration, and call paths?
- Does it bypass established boundaries or duplicate an existing abstraction?
- Does it introduce compatibility, migration, concurrency, security, data integrity, or lifecycle risks?
- Are tests missing where behavior could regress?
- Are assumptions hidden in the implementation that should be confirmed with the user?

If a new mechanism appears justified, explain:

- why the existing mechanism is insufficient
- what alternatives exist
- what compatibility or maintenance risks follow
- what assumption needs confirmation

## Severity

Use these levels:

- `P0`: severe correctness, security, data loss, or core flow breakage
- `P1`: likely behavior regression, architecture bypass, compatibility risk, or wrong mechanism
- `P2`: localized correctness or maintainability risk that should usually be fixed before merge
- `P3`: minor issue or follow-up improvement

Do not include generic style comments. If there are no blocking issues, say that clearly.

## Output

Use the user's language. Return these sections:

1. `反推的最小需求`
2. `需要确认的假设`
3. `结论：通过 / 有条件通过 / 需要修改 / 不建议合并`
4. `主要问题，按严重程度排序`

For each issue include:

- severity
- file and line
- impact
- recommendation

Prefer concise, evidence-backed findings. Put problems first. Do not bury serious issues in summary text.

