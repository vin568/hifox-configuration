---
name: test-review
description: "Evaluate and audit test suites to eliminate false sense of security (虚假的安全感) and improve fault-detection quality. Use when the user asks to evaluate tests, review test cases, check if tests are meaningful/necessary/high-quality, audit unit/integration/e2e coverage for a feature or diff, or prune low-value tests."
---

# Test Quality Review & Audit

Green tests do not mean working software. A large suite of low-quality tests creates a **false sense of security (虚假的安全感)**: CI stays green while real bugs slip into production, and harmless refactors break dozens of brittle assertions.

This skill evaluates test suites against **five orthogonal industry dimensions** (synthesizing Vladimir Khorikov's *Four Pillars of Unit Testing*, *Mutation Testing* fault-detection theory, Kent Beck's *Test Desiderata*, and *Contract/Test-Double Fidelity*).

See [anti-patterns.md](anti-patterns.md) for concrete before/after examples of false-security tests.

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

### Step 1: Map the Domain Invariants & Risk Surface First (Before Reading Tests)

Do **not** start by reading the test file—reading tests first anchors you to what the author thought to test.

1. Read the production code/diff, project glossary, architecture docs, and specs/ADRs.
2. List the **public seams** (public functions/classes, HTTP/RPC endpoints, CLI commands, database/filesystem outputs, external API calls).
3. Write down the **Must-Hold Invariants & High-Risk Boundaries**:
   - **Safety & Security Guards**: Authn/authz checks, tenant/organization isolation, path traversal / symlink escapes, input validation, dry-run or confirmation gates on destructive actions.
   - **State & Lifecycle Transitions**: Empty/initial state vs partial failure/retry state vs existing state (idempotency, deduplication, stale resource cleanup, rollback).
   - **Feature Interactions**: Combined parameters or configuration modes that interact non-trivially when enabled together.
   - **External Contract Boundaries**: Third-party API request/response schemas, database constraints, subprocess CLI flags, serialization formats.

### Step 2: Run Mental (or Actual) Mutation Testing on Each Test

For every test in scope:

1. **Identify the Seam**: Is it testing a public seam or reaching into internal implementation details?
2. **Apply the Mutation Probe**: Point to 1–3 concrete lines in the production code and ask:
   - *"If I delete this guard, invert this `<` to `<=`, skip this side effect, or return stale data, will an assertion in this test fail?"*
   - *"Did the test author derive the expected value independently from the spec, or did they recompute/copy-paste the code's actual output into the assertion?"* (Check if the assertion accidentally locks in buggy or redundant output!)
3. **Audit Test Doubles (Mocks / Fakes / Stubs)**:
   - Verify that mocks only sit at external boundaries (network, third-party SDKs, clock/randomness) and never mock internal collaborators.
   - Compare how the Fake/Mock validates inputs and returns outputs against the real external dependency. Flag any unverified assumption in the Fake.
4. **Audit E2E & Integration Execution Status**:
   - Explicitly distinguish **Offline Seam Verification** (run against fakes/in-memory doubles) from **Live Contract Verification** (actually executed against a real database, binary, or test environment). Never count a skipped or fake-backed test as real contract verification.

### Step 3: Probe Production Code at Untested Boundaries

Whenever Step 1 reveals a high-risk boundary or feature combination that Step 2 shows is untested, **trace the production code through that exact scenario**. Untested boundaries frequently conceal real implementation bugs.

### Step 4: Assign a Verdict to Every Test & Gap

Classify every existing test into one of four actions:
- **`KEEP`**: High-signal, tests a public seam, catches realistic mutants, no redundancy.
- **`STRENGTHEN`**: Tests a valid scenario at the right seam, but assertions are weak, tautological, or miss key side effects.
- **`MERGE`**: Redundant equivalence class that duplicates another test's setup; fold into a single parameterized or cohesive behavioral check.
- **`DELETE`**: Tests implementation details, internal mocks, static prose, or tautologies that add maintenance drag without catching bugs.

Classify every missing high-risk scenario as:
- **`MUST-ADD (Offline Seam)`**: Missing boundary, negative path, or state/option combination testable deterministically offline.
- **`MUST-ADD (Live E2E / Contract)`**: External contract or cross-system behavior that can only be proven against the real dependency.

---

## Output Format

Unless the user asks you to immediately modify the tests, review only and output the report in the user's language using this structure:

1. **`核心不变量与高危边界清单`**
   - Brief bullet list of the public seams and critical invariants derived from the production code/spec.
2. **`总体评估结论`**
   - Verdict: `高信噪比` / `存在局部盲区` / `存在明显虚假安全感`
   - Summary of **Offline Seam Coverage** vs **Live E2E / Contract Verification** status (explicitly call out if E2E/integration tests are skipped or only run against fakes).
3. **`高优问题与虚假安全感诊断（按 P0–P3 排序）`**
   - `P0`: Test masks a real production bug (e.g., tautological assertion locking in buggy output) or misses a critical security / data-integrity / money-loss guard.
   - `P1`: False confidence at an external contract boundary (fake/mock divergence, unexecuted E2E, or missing state-transition / option-combination test).
   - `P2`: Weak assertions (`status === 200` / no-throw only) or missing negative/edge-case validation.
   - `P3`: Implementation-coupled / brittle assertions or redundant happy-path bloat to delete/merge.
   - *For each finding, cite `file:line`, name the violated dimension, show the specific mutant/bug that slips through, and give the concrete fix.*
4. **`存量用例处置矩阵（Keep / Strengthen / Merge / Delete）与必补用例（Must-Add）`**
   - Concise table mapping each test (and missing gap) to its action.
