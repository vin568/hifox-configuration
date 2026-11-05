---
name: test-review
description: "Evaluate and audit test suites to eliminate false sense of security (虚假的安全感) and improve fault-detection quality. Use when the user asks to evaluate tests, review test cases, check if tests are meaningful/necessary/high-quality, audit unit/integration/e2e coverage for a feature or diff, or prune low-value tests."
---

# Test Quality Review & Audit

Green tests do not mean working software. A large suite of low-quality tests creates a **false sense of security (虚假的安全感)**: CI stays green while real bugs slip into production, and harmless refactors break dozens of brittle assertions.

This skill evaluates test suites against **five orthogonal industry dimensions** (synthesizing Vladimir Khorikov's *Four Pillars of Unit Testing*, *Mutation Testing* fault-detection theory, Kent Beck's *Test Desiderata*, and *Contract/Test-Double Fidelity*).

Read [anti-patterns.md](anti-patterns.md) only when you need concrete before/after rewrite templates for findings.

---

## The 5 Evaluation Dimensions

Every test (and the suite as a whole) is judged across these five dimensions:

| Dimension | Core Question (How to Pass) | False-Security Failure Mode |
| --- | --- | --- |
| **1. Mutation Sensitivity**<br>*(缺陷检出率 / 防回归能力)* | **If I mutate one branch, condition, or side-effect in production code, does this test turn red?** Expected values must come from an independent specification or literal, never recomputed or copied blindly from code output. | **Tautological Test (同义反复)**: Recomputing the expected value the same way the code does, or copying buggy output into `expect()` / snapshots; or **Weak Assertion**: checking only `status === 200` / `toBeDefined()` without verifying state/side effects. |
| **2. Boundary & Double Fidelity**<br>*(边界与替身保真度)* | **Do mocks/fakes strictly honor the real external contract, and are real external contracts verified end-to-end?** Mock only at system boundaries, never internal modules. | **Mocking Your Own Assumptions (自嗨式 Mock)**: A fake client/server accepts payloads, query parameters, or states that the real dependency rejects; or **Phantom E2E**: an E2E test exists in code but is skipped or only run against an in-memory fake. |
| **3. Resistance to Refactoring**<br>*(抗重构性 / 接缝纯粹性)* | **Does the test observe behavior strictly through a public seam (public API, HTTP/RPC endpoint, CLI argv/exit/stdout, persisted state)?** | **Implementation-Coupled (实现耦合)**: Mocking internal collaborators, asserting private method call order, or regex-matching static prose/templates that break on harmless edits while missing logic bugs. |
| **4. Risk-Aligned Necessity**<br>*(风险对齐的必要性)* | **Does the suite target domain invariants, security boundaries, destructive mutations, and feature interactions rather than repeating nominal happy paths?** One test per equivalence class. | **Happy-Path Bloat (正常路径堆砌)**: Dozens of tests for nominal inputs while auth/tenant isolation, path/symlink traversal, partial failure recovery, idempotency, and combined options (`A + B`) have zero tests. |
| **5. Diagnostic Signal-to-Noise**<br>*(诊断信噪比与维护成本)* | **Is the test deterministic, fast, and immediately clear about which business invariant broke when it fails?** | **Brittle / Noisy Megatest**: Flaky timing/network dependencies, or giant tests with opaque setups whose maintenance cost exceeds the risk of the code under test. |

---

## Evaluation Workflow

Review only by default: **never leave persistent edits in production or test files** unless the user explicitly asks to apply fixes.

### Step 0: Scope Routing & Scale Guard

1. **Resolve Scope**:
   - Explicit files, directories, or commands specified by the user.
   - Otherwise, current uncommitted or branch changes (`git status -sb`, `git diff --stat`).
   - If neither exists, ask one concise question to pin the target module or test file instead of scanning the entire repository.
2. **Sub-Agent Sharding & Large-Suite Pruning Funnel**:
   - If the target scope exceeds ~300 lines of test code or ~20 test cases across multiple modules, split by module/feature and run parallel read-only sub-agents so single-context attention does not degrade into rubber-stamping `KEEP`.
   - For large legacy suites, run a fast **Pre-Screen Funnel** first (`grep` / scan) to pinpoint the 4 high-suspect categories before deep reading: (a) tests named `legacy` / `old` / `compat` / `v1` / `deprecated`, (b) adjacent micro-tests repeating heavy setup (`spawnSync`, DB/tempdir creation) for single-branch checks, (c) tests where mock setup lines exceed state assertions, and (d) static prose regex or snapshot-only tests.

### Step 1: Map Domain Invariants First (Anti-Anchoring)

Do **not** read `.test.*` / `.spec.*` files first—reading tests first anchors you to what the author thought to test.

1. **Read Production & Spec First**:
   - In diff mode, inspect production diffs excluding tests first (`git diff -- . ':!*test*' ':!*spec*'`).
   - If the user passed a test file directly, locate and read its corresponding production module, `GLOSSARY.md`, and relevant specs/ADRs before reading the test assertions.
2. **List Public Seams**: Public functions/classes, HTTP/RPC endpoints, CLI argv/exit/stdout, persisted filesystem/DB state, and external API calls.
3. **Number Must-Hold Invariants (`INV-1`, `INV-2`, …)**:
   - **Safety & Security Guards**: Authn/authz, tenant/organization isolation, path traversal / symlink escapes (`realpath`), destructive action gates (`--apply`, `--production`).
   - **State & Lifecycle Transitions**: Empty vs partial failure/retry vs existing state (idempotency, deduplication, stale cleanup, rollback).
   - **Feature Interactions**: Combined flags or configuration modes (`A + B`) that interact non-trivially.
   - **External Contract Boundaries**: Third-party API schemas, DB constraints, subprocess CLI flags, serialization formats.
4. **Negative Calibration (No Fault-Invention)**: Do not demand tests for guarantees already enforced by the compiler/type system, static schema validators, or branchless pass-through glue.

### Step 2: Run Mutation Probes (Mental First, Targeted Atomic Physical When Ambiguous) & Audit Boundaries

1. **Run Scoped Tests Once (Read-Only)**: When a local test runner is available and fast, execute the target test suite once to observe actual pass/skip counts and verify the test files are wired into the runner's file glob.
2. **Identify the Seam & Apply the Mutation Probe**:
   - **Default — Mental Probe (Static Tracing)**: Point to 1–3 concrete lines (`file:line`) in production code and check whether deleting a guard, inverting `<` to `<=`, or skipping a side effect turns an assertion red, and whether expected values come independently from the spec rather than copying/recomputing code output.
   - **Escalation — Targeted Atomic Physical Probe (Max 1–3 Ambiguous `P0`/`P1` Sites)**: When indirect multi-layer calls, complex regexes, or boundary math make static tracing uncertain, verify via a physical probe **only if the working tree has no uncommitted changes (`git status --porcelain` is empty)**. If uncommitted edits exist, stay strictly in Mental Probe mode and **explicitly inform the user in the report why physical mutation was skipped** (e.g., working tree has uncommitted changes—commit or stash first to enable physical mutation probes). When clean:
     1. **Write-Ahead Audit Log (`$(git rev-parse --git-dir)/test-review-mutations.jsonl`)**: Before modifying any file on disk, append a JSON line recording `{ timestamp, status: "MUTATING", file, line, originalSnippet, mutatedSnippet, testCommand }` inside the repo's git dir (so it persists for forensics without polluting `git status`).
     2. **Atomic Probe & Restore**: Run `try { mutate 1 line; run focused test via -t/--test-name-pattern; } finally { restore exact original bytes; verify git diff is empty; append status: "RESTORED" and testExitCode to the audit log }`.
3. **Audit Test Doubles Against Grounded Contract Evidence**:
   - Verify mocks/fakes sit only at external boundaries, never internal collaborators.
   - Check fake input validation and fallback behavior against primary sources in order: (a) repo schemas / OpenAPI / SDK types, (b) real binary/CLI `--help` output, (c) live E2E assertions. Flag permissive catch-all fallbacks (e.g., returning `200 OK` or `{ id: "fake-id" }` for unknown subcommands/flags). If the real contract cannot be verified locally, mark it `UNVERIFIED`—never guess.
4. **Audit E2E & Integration Execution Status**:
   - Distinguish **Offline Seam Verification** (run against fakes/in-memory doubles) from **Live Contract Verification** (executed against a real dependency). Explicitly flag tests gated by unset environment variables (`skip`) as unexecuted in the current run.

### Step 3: Probe Production Code at Untested Boundaries

For every `INV-*` boundary or option combination left uncovered in Step 2, **trace the production code path line-by-line**. Untested boundaries frequently conceal real implementation bugs.

### Step 4: Classify Actionable Verdicts & Prune Low-Value Tests (宁缺毋滥)

1. **Zero Marginal Value Rule (零边际价值即淘汰)**: For every existing test, ask: *"If we delete this test today, what realistic business bug could slip through that is not already caught by the type system or an upper-level integration test?"* If the answer is "none", **never mark it `KEEP`**—classify as **`DELETE` (`删除冗余`)** or **`MERGE` (`合并重复`, fold $N$ fragmented setup-heavy tests into $1$)**.
2. **Dead-Code Symbiosis Check (测试与生产死代码共生识别)**: When a test exercises a legacy or compatibility branch, check whether any real production entrypoint or current domain spec (`GLOSSARY.md` / `ADR`) still uses that branch. If the test is the only caller keeping obsolete production code alive, classify as **`DELETE TEST + DEAD CODE` (`连同生产死代码下线`)**.
3. **Missing Boundaries**: Classify each uncovered high-risk scenario as **`MUST-ADD (Offline Seam)` (`新增用例（本地）`)** or **`MUST-ADD (Live E2E / Contract)` (`新增用例（真实环境）`)**.

---

## Output Format & Writing Style

Write in **plain, direct engineering language** (in the user's language).
- **Ban literal translationese & academic jargon**: Never output terms like "公共接缝 (Seam)", "核心不变量 (Invariant)", "变异体存活 / 杀死变异体", or "Offline Seam Coverage". Say "对外入口", "核心业务规则", "删掉这行代码测试依然通过", and "本地离线测试 vs 真实环境测试".
- **Self-contained tags over lookup IDs**: In findings, use direct tags (`[漏测边界]`, `[弱断言]`, `[Mock 失真]`, `[绑定内部实现]`, `[历史僵尸/死代码]`, `[重复碎片]`) instead of forcing the reader to look up `(违反维度 4，关联 INV-2)`.
- **Consequence first, code second**: Lead every finding with what breaks in plain language before citing function names or line numbers.

Unless the user asks to modify files, output the review using this structure:

1. **`核心业务规则与高危边界`**
   - Concise bullet list of public entrypoints ("对外入口": CLI/API/functions) and the must-hold business rules (`INV-1 … INV-N`).
2. **`总体结论与测试运行概况`**
   - **结论**：`高信噪比` / `存在局部盲区` / `存在明显虚假安全感` (if high-signal, state `高信噪比` directly without padding).
   - **运行与验证概况**：
     - 实跑结果（通过数 / 跳过数）；
     - 变异实证情况（说明对哪些代码做了物理删改验证，并附上取证日志路径 `<git-dir>/test-review-mutations.jsonl`；若未做物理变异，必须写明原因：如“工作区存在未提交改动，为防冲突仅做静态推演，如需物理验证请先 commit/stash”或“静态推演已全部确认”）；
     - 真实环境覆盖（区分哪些走了真实外部依赖，哪些只测了本地假对象/Mock，以及未核实的外部契约 `UNVERIFIED`）。
3. **`重点问题诊断（按 P0–P3 排序）`**
   - Severity: `P0` (漏掉真实 Bug 或高危安全/数据损坏守卫), `P1` (Mock 与真实接口不符、未跑真实 E2E 或关键状态流转漏测), `P2` (断言过弱只测不报错，或缺少负向校验), `P3` (绑定内部实现或重复用例).
   - Format each item as 3 short lines (no dense walls of text):
     - **`P0/P1/P2/P3` `[分类标签]` 一句话大白话讲清什么场景下会出什么问题**（标题不堆砌函数名和行号）
     - **出问题场景**：用户或系统怎么操作会触发这个问题，实际后果是什么。
     - **为什么测试没拦住**：指向具体代码行（`file:line`）和测试行（`test:line`），说明哪段逻辑没测到，或实测删掉某行代码后测试为什么依然全绿。
     - **怎么改**：一句话给出补测试或修代码的具体动作。
4. **`测试用例修改清单`**
   - Table of **actionable items only** using plain verbs (**`补强断言`** / **`合并重复`** / **`删除冗余`** / **`连同生产死代码下线`** / **`新增用例（本地）`** / **`新增用例（真实环境）`**). Summarize untouched good tests as a single count per file (e.g., `其余 8 个用例覆盖有效，保持不变`).
