#!/usr/bin/env node
// xwing-deploy acceptance suite — consumer-contract black-box scenarios.
// Self-contained on purpose: never imports anything from the CLI under test;
// asserts only consumer-observable behavior (exit codes, stdout contract
// lines, state.yaml registrations, task-CLI reads of remote resources).
//
// Env contract: HIFOX_SERVER_URL, HIFOX_TOKEN, HIFOX_ORGANIZATION_ID,
// HIFOX_E2E_SPACE_ID, [HIFOX_CLI_BIN]. See SKILL.md.
//
// Exit code 0 = all scenarios passed. Report at <out>/report.json.
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, join, resolve } from "node:path";

// Template braces are assembled at runtime: this file is synced as a skill
// attachment and must survive the deploy pipeline's variable expansion
// byte-identically (no literal double-brace sequences anywhere in source).
const LB = String.fromCharCode(123);
const RB = String.fromCharCode(125);
const mustache = (name) => LB + LB + name + RB + RB;
const NAMING_PATTERN = `${mustache("name")}${mustache("suffix")}`;

const CLI_BIN = process.env.HIFOX_CLI_BIN || "hifox";
const SPACE_ID = process.env.HIFOX_E2E_SPACE_ID || "";
const ORG_ID = process.env.HIFOX_ORGANIZATION_ID || "";

const args = process.argv.slice(2);
const flag = (name) => {
  const i = args.indexOf(`--${name}`);
  return i >= 0 ? args[i + 1] : undefined;
};
const KEEP = args.includes("--keep");
const CLEANUP_REPORT = flag("cleanup-report");
const OUT_DIR = flag("out") || join(tmpdir(), `xwd-acceptance-${Date.now()}`);
const CANDIDATE = flag("candidate");
const REPO = flag("repo") ? resolve(flag("repo")) : undefined;

function fail(message) {
  console.error(`acceptance: ${message}`);
  process.exit(2);
}

function spawnCliEnv() {
  const env = { ...process.env, HIFOX_ORGANIZATION_ID: ORG_ID };
  if (process.env.HIFOX_SERVER_URL) env.HIFOX_SERVER_URL = process.env.HIFOX_SERVER_URL;
  if (process.env.HIFOX_TOKEN) env.HIFOX_TOKEN = process.env.HIFOX_TOKEN;
  if (CLI_BIN !== "hifox") {
    env.PATH = `${dirname(CLI_BIN)}:${env.PATH || ""}`;
  }
  return env;
}

function cliRun(cliArgs, { ok = true } = {}) {
  const result = spawnSync(basename(CLI_BIN) === CLI_BIN ? CLI_BIN : basename(CLI_BIN), cliArgs, {
    encoding: "utf8",
    maxBuffer: 128 * 1024 * 1024,
    timeout: 300_000,
    env: spawnCliEnv(),
  });
  if (ok && result.status !== 0) {
    throw new Error(`task CLI ${cliArgs.join(" ")} failed: ${(result.stderr || result.stdout || "").trim()}`);
  }
  return result;
}

function cliJson(cliArgs) {
  const result = cliRun([...cliArgs, "--output", "json"]);
  return result.stdout.trim() ? JSON.parse(result.stdout) : null;
}

function deployRun(deployRoot, deployArgs) {
  return spawnSync(
    process.execPath,
    [join(REPO, "bin", "xwing-deploy.mjs"), ...deployArgs, "--root", deployRoot],
    { encoding: "utf8", maxBuffer: 128 * 1024 * 1024, timeout: 600_000, env: spawnCliEnv() },
  );
}

// Minimal state.yaml reader: sections (crew/agents/skills/workflows) at col 0,
// entries indented 2, fields (id/deployedName) indented 4.
function readState(deploymentRoot) {
  const path = join(deploymentRoot, "environments", "e2e", "state.yaml");
  if (!existsSync(path)) return {};
  const state = {};
  let section = null;
  let entry = null;
  for (const line of readFileSync(path, "utf8").split("\n")) {
    if (!line.trim() || line.trim().startsWith("#")) continue;
    if (!line.startsWith(" ")) {
      section = line.replace(/:$/, "").trim();
      entry = null;
      continue;
    }
    if (line.startsWith("  ") && !line.startsWith("    ")) {
      entry = line.trim().replace(/:$/, "");
      continue;
    }
    const field = line.trim().match(/^(id|deployedName):\s*(.+)$/);
    if (section && entry && field) {
      const value = field[2].trim().replace(/^"|"$/g, "");
      if (field[1] === "id") {
        state[section] ||= {};
        state[section][entry] ||= {};
        state[section][entry].id = value;
      }
    }
  }
  return state;
}

const now = () => new Date().toISOString();
const startedAt = now();

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------
function buildMainRoot() {
  const root = mkdtempSync(join(tmpdir(), "xwd-acc-main-"));
  const suffix = ` - xwdci-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`;
  const files = {
    ".xwing/environments/e2e/environment.yaml": [
      `organizationId: ${ORG_ID}`,
      `spaceId: ${SPACE_ID}`,
      "configRoot: config/test",
      "cli:",
      "  app: hifox",
      `nameSuffix: ${JSON.stringify(suffix)}`,
      `namingPattern: ${JSON.stringify(NAMING_PATTERN)}`,
      "",
    ].join("\n"),
    ".xwing/config/test/crews/team/crew.yaml": [
      "key: team",
      "name: Team e2e",
      "leader: alpha",
      "members:",
      "  - agent: alpha",
      "  - agent: beta",
      "    role: Reviewer",
      "instructionsFile: instructions.md",
      "",
    ].join("\n"),
    ".xwing/config/test/crews/team/instructions.md":
      `Crew ${mustache("crew.name")} runs in ${mustache("environment")}. Leader ${mustache("agents.alpha.name")}.\n`,
    ".xwing/config/test/agents/alpha/agent.yaml": [
      "key: alpha",
      "name: Alpha",
      "jobTitle: Engineer",
      'instructionsFile: "@/prompts/alpha.md"',
      "visibility: personal",
      "repositories:",
      "  - https://cnb.cool/tars-lab/xwing",
      "  - https://example.com/auxiliary.git",
      "",
    ].join("\n"),
    ".xwing/config/test/agents/beta/agent.yaml": [
      "key: beta",
      "name: Beta",
      "jobTitle: Reviewer",
      "instructionsFile: instructions.md",
      "",
    ].join("\n"),
    ".xwing/config/test/agents/beta/instructions.md": `Beta reviews in ${mustache("environment")}.\n`,
    ".xwing/config/test/skills/echo/skill.yaml": [
      "key: echo",
      "name: Echo",
      "description: Echo skill",
      'dir: "@/skills/echo"',
      'config: { mode: "strict" }',
      "assignTo:",
      "  - alpha",
      "",
    ].join("\n"),
    ".xwing/config/test/agents/alpha/skills/relay/skill.yaml": [
      "key: relay",
      "name: Relay",
      "description: Relay skill",
      "contentFile: SKILL.md",
      "config: {}",
      "",
    ].join("\n"),
    ".xwing/config/test/agents/alpha/skills/relay/SKILL.md":
      `# ${mustache("skills.alpha/relay.name")}\n\nRelay for ${mustache("agents.alpha.name")}.\n`,
    "prompts/alpha.md": [
      `Alpha works in ${mustache("environment")} for ${mustache("crew.name")} alongside ${mustache("agents.beta.name")}`,
      `using ${mustache("skills.echo.name")} and ${mustache("skills.alpha/relay.name")}`,
      `(literal \\${mustache("escaped.var")}).`,
      "",
    ].join(" "),
    "skills/echo/SKILL.md":
      `# ${mustache("skills.echo.name")}\n\nRepeat the user's request for ${mustache("agents.alpha.name")} with \\${mustache("raw.template")}.\n`,
    "skills/echo/references/guide.md": `Guide for ${mustache("agents.beta.name")} in ${mustache("environment")}.\n`,
    "skills/echo/references/obsolete.md": `Temporary file in ${mustache("environment")}.\n`,
  };
  for (const [path, content] of Object.entries(files)) {
    const target = join(root, path);
    mkdirSync(dirname(target), { recursive: true });
    writeFileSync(target, content);
  }
  return { root, suffix, deployRoot: join(root, ".xwing") };
}

function buildWorkflowRoot(suffix) {
  const root = mkdtempSync(join(tmpdir(), "xwd-acc-wf-"));
  const graph = {
    blocks: {
      "b-trigger": {
        id: "b-trigger",
        type: "generic_webhook",
        name: "Webhook Trigger",
        position: { x: 0, y: 0 },
        subBlocks: {
          token: { id: "token", type: "short-input", value: null },
          triggerPath: { id: "triggerPath", type: "short-input", value: "b-trigger" },
          requireAuth: { id: "requireAuth", type: "switch", value: false },
        },
        outputs: {},
        enabled: true,
      },
      "b-code": {
        id: "b-code",
        type: "function",
        name: "Function",
        position: { x: 1, y: 1 },
        subBlocks: { code: { id: "code", type: "code", value: "console.log(1)" } },
        outputs: {},
        enabled: true,
      },
    },
    edges: [
      { id: "e-1", source: "b-trigger", target: "b-code", sourceHandle: "source", targetHandle: "target", type: "default", data: {} },
    ],
    loops: {},
    parallels: {},
    variables: {},
  };
  const files = {
    ".xwing/environments/e2e/environment.yaml": [
      `organizationId: ${ORG_ID}`,
      `spaceId: ${SPACE_ID}`,
      "configRoot: config/test",
      "cli:",
      "  app: hifox",
      `nameSuffix: ${JSON.stringify(suffix)}`,
      `namingPattern: ${JSON.stringify(NAMING_PATTERN)}`,
      "",
    ].join("\n"),
    ".xwing/config/test/workflows/demo/workflow.yaml": [
      "key: demo",
      'name: "Demo Flow"',
      'description: ""',
      "deployed: true",
      "source: ui",
      "",
    ].join("\n"),
    ".xwing/config/test/workflows/demo/graph.json": `${JSON.stringify(graph, null, 2)}\n`,
  };
  for (const [path, content] of Object.entries(files)) {
    const target = join(root, path);
    mkdirSync(dirname(target), { recursive: true });
    writeFileSync(target, content);
  }
  return { root, deployRoot: join(root, ".xwing") };
}

// ---------------------------------------------------------------------------
// Cleanup
// ---------------------------------------------------------------------------
function cleanupRemote(state) {
  const problems = [];
  for (const crew of Object.values(state.crew || {})) {
    if (crew.id && cliRun(["crew", "archive", crew.id, "-y"], { ok: false }).status !== 0) problems.push(`crew ${crew.id}`);
  }
  for (const agent of Object.values(state.agents || {})) {
    if (agent.id && cliRun(["agent", "archive", agent.id, "-y"], { ok: false }).status !== 0) problems.push(`agent ${agent.id}`);
  }
  for (const skill of Object.values(state.skills || {})) {
    if (skill.id && cliRun(["skill", "delete", skill.id, "-y"], { ok: false }).status !== 0) problems.push(`skill ${skill.id}`);
  }
  const workflow = state.workflows?.demo?.id;
  if (workflow) {
    cliRun(["workflow", "unlock", workflow, "--space", SPACE_ID, "--yes"], { ok: false });
    if (cliRun(["workflow", "delete", workflow, "--space", SPACE_ID, "--yes"], { ok: false }).status !== 0) {
      problems.push(`workflow ${workflow}`);
    }
  }
  return problems;
}

if (CLEANUP_REPORT) {
  const report = JSON.parse(readFileSync(CLEANUP_REPORT, "utf8"));
  const problems = [...cleanupRemote(report.cleanup.main || {}), ...cleanupRemote(report.cleanup.workflow || {})];
  if (problems.length) fail(`cleanup incomplete: ${problems.join(", ")}`);
  console.log("cleanup complete");
  process.exit(0);
}

// ---------------------------------------------------------------------------
// Preflight
// ---------------------------------------------------------------------------
if (!REPO) fail("--repo <xwing-deploy worktree> is required");
if (!CANDIDATE) fail("--candidate <commit sha> is required");
for (const name of ["HIFOX_SERVER_URL", "HIFOX_TOKEN", "HIFOX_ORGANIZATION_ID", "HIFOX_E2E_SPACE_ID"]) {
  if (!process.env[name]) fail(`env ${name} is required`);
}

const git = (repo, ...gitArgs) =>
  spawnSync("git", ["-C", repo, ...gitArgs], { encoding: "utf8" });
const resolved = git(REPO, "rev-parse", "--verify", `${CANDIDATE}^{commit}`);
if (resolved.status !== 0) fail(`candidate ${CANDIDATE} not found in ${REPO}`);
const candidateFull = resolved.stdout.trim();
const head = git(REPO, "rev-parse", "HEAD").stdout.trim();
if (head !== candidateFull) {
  fail(`worktree HEAD ${head} != candidate ${candidateFull}; check out the candidate before running`);
}

mkdirSync(OUT_DIR, { recursive: true });

// ---------------------------------------------------------------------------
// Scenarios
// ---------------------------------------------------------------------------
const scenarios = [];
let aborted = false;
function scenario(name, fn) {
  if (aborted) {
    scenarios.push({ name, status: "skipped" });
    return;
  }
  const started = Date.now();
  try {
    fn();
    scenarios.push({ name, status: "pass", ms: Date.now() - started });
    console.log(`pass: ${name}`);
  } catch (error) {
    scenarios.push({ name, status: "fail", error: String(error.message || error), ms: Date.now() - started });
    console.error(`FAIL: ${name}: ${error.message || error}`);
    aborted = true;
  }
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}
function assertEqual(actual, expected, message) {
  assert(Object.is(actual, expected), `${message} (got ${JSON.stringify(actual)}, want ${JSON.stringify(expected)})`);
}
function assertMatch(text, pattern, message) {
  assert(pattern.test(text), `${message} (output did not match ${pattern})`);
}
function mustPass(result, label) {
  assertEqual(result.status, 0, `${label} exited ${result.status}: ${(result.stderr || result.stdout || "").trim().slice(0, 2000)}`);
  return result;
}

const main = buildMainRoot();
let workflowFixture;
let keepMainState;
let keepWorkflowState;

scenario("validate: fixture root passes offline validation", () => {
  mustPass(deployRun(main.deployRoot, ["validate", "--environment", "e2e"]), "validate");
});

let state;
let crewId, alphaId, betaId, skillId, relaySkillId;

scenario("create: full provisioning lands and state registers ids", () => {
  mustPass(deployRun(main.deployRoot, ["create", "--environment", "e2e", "--apply"]), "create --apply");
  state = readState(main.deployRoot);
  assertEqual(Object.keys(state.agents || {}).sort().join(","), "alpha,beta", "created agents");
  assertEqual(Object.keys(state.skills || {}).sort().join(","), "alpha/relay,echo", "created skills");
  assertEqual(Object.keys(state.crew || {}).join(","), "team", "created crew");
  crewId = state.crew.team.id;
  alphaId = state.agents.alpha.id;
  betaId = state.agents.beta.id;
  skillId = state.skills.echo.id;
  relaySkillId = state.skills["alpha/relay"].id;

  const agents = cliJson(["agent", "list", "--include-archived"]) || [];
  assert(agents.some((a) => a.id === alphaId && a.name === `Alpha${main.suffix}`), "remote Alpha name mismatch");
  assert(agents.some((a) => a.id === betaId && a.name === `Beta${main.suffix}`), "remote Beta name mismatch");

  const alpha = cliJson(["agent", "view", alphaId]);
  assertMatch(alpha.instructions, /Alpha works in e2e/, "instructions environment not rendered");
  assert(alpha.instructions.includes(`Team e2e${main.suffix}`), "crew name not rendered into instructions");
  assert(alpha.instructions.includes(`Beta${main.suffix}`), "peer agent name not rendered");
  assert(alpha.instructions.includes(`Echo${main.suffix}`), "skill name not rendered");
  assert(alpha.instructions.includes(`Relay${main.suffix}`), "agent skill name not rendered");
  assert(alpha.instructions.includes(`${LB}${LB}escaped.var${RB}${RB}`), "escaped literal braces not preserved");

  const repoUrls = () =>
    (cliJson(["agent", "repo", "list", alphaId]) || []).map((item) => item.url).sort();
  assertEqual(
    JSON.stringify(repoUrls()),
    JSON.stringify(["https://cnb.cool/tars-lab/xwing", "https://example.com/auxiliary.git"]),
    "declared repositories were not attached",
  );

  const skill = cliJson(["skill", "view", skillId]);
  assertEqual(skill.name, `Echo${main.suffix}`, "skill name mismatch");
  assertEqual(skill.spaceId, SPACE_ID, "skill must live in the acceptance space");
  assert(skill.content.includes(`# Echo${main.suffix}`), "skill name variable not rendered into SKILL.md");
  assert(skill.content.includes(`${LB}${LB}raw.template${RB}${RB}`), "escaped literal braces in SKILL.md not preserved");

  const relay = cliJson(["skill", "view", relaySkillId]);
  assertEqual(relay.scope, "agent", "colocated skill must be agent-scoped");
  assertEqual(relay.agentId, alphaId, "colocated skill must belong to Alpha");
  assertEqual(relay.spaceId, null, "agent skill must not live in a Space");

  const skillFiles = (cliJson(["skill", "files", "list", skillId]) || []).map((item) => item.path).sort();
  assertEqual(JSON.stringify(skillFiles), JSON.stringify(["references/guide.md", "references/obsolete.md"]), "skill attachment files mismatch");

  const agentSkills = cliJson(["agent", "skill", "list", alphaId]) || [];
  assert(agentSkills.some((item) => item.id === skillId), "skill not attached to Alpha");

  const crew = cliJson(["crew", "view", crewId]);
  assertEqual(crew.name, `Team e2e${main.suffix}`, "crew name mismatch");
  const members = cliJson(["crew", "member", "list", crewId]) || [];
  assert(members.some((m) => m.member.id === betaId && m.role === "Reviewer"), "reviewer role not applied");
});

scenario("doctor: self-check passes", () => {
  mustPass(deployRun(main.deployRoot, ["doctor", "--environment", "e2e"]), "doctor");
});

scenario("plan: previews without writing state", () => {
  mkdirSync(join(main.root, ".xwing/config/test/agents/gamma"), { recursive: true });
  writeFileSync(join(main.root, ".xwing/config/test/agents/gamma/agent.yaml"), [
    "key: gamma",
    "name: Gamma",
    "jobTitle: Auditor",
    "instructionsFile: instructions.md",
    "",
  ].join("\n"));
  writeFileSync(join(main.root, ".xwing/config/test/agents/gamma/instructions.md"), `Gamma runs in ${mustache("environment")}.\n`);
  const planned = mustPass(deployRun(main.deployRoot, ["plan", "--environment", "e2e"]), "plan");
  assertMatch(planned.stdout, new RegExp(`agent create .*--name "Gamma${main.suffix.replace(/[-\s]/g, "\\$&")}"`), "plan must preview the gamma provision");
  assertEqual(readState(main.deployRoot).agents.gamma, undefined, "plan must not write state.yaml");
});

scenario("apply-drift: additions, role change, file prune and repo shrink converge", () => {
  // Add gamma (from the plan scenario), delete an obsolete skill file, shrink
  // the declared repository list: one apply must converge all three.
  rmSync(join(main.root, "skills/echo/references/obsolete.md"));
  const alphaYaml = join(main.root, ".xwing/config/test/agents/alpha/agent.yaml");
  writeFileSync(alphaYaml, readFileSync(alphaYaml, "utf8").replace("  - https://example.com/auxiliary.git\n", ""));
  mustPass(deployRun(main.deployRoot, ["apply", "--environment", "e2e", "--apply"]), "apply --apply (drift)");

  const skillFiles = (cliJson(["skill", "files", "list", skillId]) || []).map((item) => item.path).sort();
  assertEqual(JSON.stringify(skillFiles), JSON.stringify(["references/guide.md"]), "stale skill file not deleted during apply");
  const repoUrls = (cliJson(["agent", "repo", "list", alphaId]) || []).map((item) => item.url).sort();
  assertEqual(JSON.stringify(repoUrls), JSON.stringify(["https://cnb.cool/tars-lab/xwing"]), "apply must replace the repository list, not append");
  const gammaId = readState(main.deployRoot).agents.gamma?.id;
  assert(gammaId, "gamma was not registered by apply");
  const members = cliJson(["crew", "member", "list", crewId]) || [];
  assert(members.some((m) => m.member.id === alphaId) && members.some((m) => m.member.id === betaId), "crew missing members after drift");

  // Role edit lands; emptied role must not be sent as an empty override.
  const crewYaml = join(main.root, ".xwing/config/test/crews/team/crew.yaml");
  writeFileSync(crewYaml, readFileSync(crewYaml, "utf8").replace("role: Reviewer", "role: Reviewer-plus"));
  mustPass(deployRun(main.deployRoot, ["apply", "--environment", "e2e", "--apply"]), "apply --apply (role)");
  const role = () => (cliJson(["crew", "member", "list", crewId]) || []).find((m) => m.member.id === betaId)?.role;
  assertEqual(role(), "Reviewer-plus", "member role change must reach the remote");
  writeFileSync(crewYaml, readFileSync(crewYaml, "utf8").replace(/^\s*role: Reviewer-plus.*\n/m, ""));
  mustPass(deployRun(main.deployRoot, ["apply", "--environment", "e2e", "--apply"]), "apply --apply (role cleared)");
  assertEqual(role(), "Reviewer-plus", "an emptied role must not be sent as an empty --role");
});

scenario("apply-idempotent: second apply keeps ids and reports in sync", () => {
  const reapplied = mustPass(deployRun(main.deployRoot, ["apply", "--environment", "e2e", "--apply"]), "re-apply");
  assertEqual(readState(main.deployRoot).crew.team.id, crewId, "crew id changed on re-apply");
  assertEqual(readState(main.deployRoot).agents.alpha.id, alphaId, "agent id changed on re-apply");
  assertEqual(readState(main.deployRoot).skills.echo.id, skillId, "skill id changed on re-apply");
  const agentSkills = cliJson(["agent", "skill", "list", alphaId]) || [];
  assert(agentSkills.some((item) => item.id === skillId), "skill attachment lost after re-apply");
});

scenario("pull: snapshots land on disk", () => {
  mustPass(deployRun(main.deployRoot, ["pull", "--environment", "e2e"]), "pull");
  // Layout contract (README 快照与备份): agents/<key>.json, skills/<key>.json
  // (Agent Skill keys nest), crews/<key>/{crew,members}.json, plus root-level
  // crew.json/members.json for the single-crew case.
  const expected = [
    "snapshots/e2e/crew.json",
    "snapshots/e2e/members.json",
    "snapshots/e2e/agents/alpha.json",
    "snapshots/e2e/agents/beta.json",
    "snapshots/e2e/agents/gamma.json",
    "snapshots/e2e/skills/echo.json",
    "snapshots/e2e/skills/alpha/relay.json",
    "snapshots/e2e/crews/team/crew.json",
    "snapshots/e2e/crews/team/members.json",
  ];
  for (const path of expected) {
    assert(existsSync(join(main.deployRoot, path)), `missing snapshot ${path}`);
  }
});

let backupEnvDir;
scenario("backup-restore: backup dir forms, in-place and --to restores work", () => {
  mustPass(deployRun(main.deployRoot, ["backup", "--environment", "e2e"]), "backup");
  const stamps = readdirSync(join(main.deployRoot, "backups"));
  assert(stamps.length > 0, "no backup timestamp directory");
  backupEnvDir = join(main.deployRoot, "backups", stamps[0], "e2e");
  assert(existsSync(backupEnvDir), "backup env dir missing");

  const restoredTarget = mkdtempSync(join(tmpdir(), "xwd-acc-restore-"));
  mustPass(
    deployRun(main.deployRoot, ["restore", "--from", backupEnvDir, "--to", restoredTarget, "--apply"]),
    "restore --to",
  );
  assert(existsSync(join(restoredTarget, "config/test/skills/echo/references/guide.md")), "external reference not materialized by --to restore");

  const promptPath = join(main.root, "prompts/alpha.md");
  const pristine = readFileSync(promptPath, "utf8");
  writeFileSync(promptPath, "corrupted\n");
  mustPass(deployRun(main.deployRoot, ["restore", "--from", backupEnvDir, "--apply"]), "restore in-place");
  assert(readFileSync(promptPath, "utf8") === pristine, "in-place restore did not restore the external prompt");
});

scenario("workflow-lifecycle: apply converges, re-applies in sync, prune archives", () => {
  workflowFixture = buildWorkflowRoot(main.suffix);
  const deployRoot = workflowFixture.deployRoot;
  mustPass(deployRun(deployRoot, ["validate", "--environment", "e2e"]), "workflow validate");
  const applied = mustPass(deployRun(deployRoot, ["apply", "--apply", "--environment", "e2e"]), "workflow apply");
  assertMatch(applied.stdout, /workflow demo: in sync/, "one-shot import must land the full graph");

  const id = readState(deployRoot).workflows?.demo?.id;
  assert(id, "state.yaml must register the created workflow");
  const view = cliJson(["workflow", "view", id, "--space", SPACE_ID]);
  const row = view?.data ?? view;
  assertEqual(row.name, `Demo Flow${main.suffix}`, "envelope name not adopted");
  assertEqual(row.isDeployed, true, "deployed: true must reach the engine");
  assertEqual(row.locked, true, "lockUiEdits default must lock the workflow");

  const reapply = mustPass(deployRun(deployRoot, ["apply", "--apply", "--environment", "e2e"]), "workflow re-apply");
  assertMatch(reapply.stdout, /workflow demo: in sync/, "re-apply must converge in sync");
  assert(!/SavedGraph differs/.test(reapply.stdout), "engine round-trip must not look like drift");

  rmSync(join(deployRoot, "config/test/workflows/demo"), { recursive: true });
  const pruned = mustPass(deployRun(deployRoot, ["prune", "workflows", "--apply", "--environment", "e2e"]), "prune workflows");
  assertMatch(pruned.stdout, /Archiving demo/, "prune must archive the removed workflow");
  assertEqual(readState(deployRoot).workflows?.demo, undefined, "prune must deregister state");
});


// ---------------------------------------------------------------------------
// Report + cleanup
// ---------------------------------------------------------------------------
keepMainState = readState(main.deployRoot);
keepWorkflowState = workflowFixture ? readState(workflowFixture.deployRoot) : {};

let cleanupProblems = [];
if (!KEEP) {
  // Pass and fail paths alike: archive whatever the fixtures managed to
  // create, so the acceptance space does not accumulate garbage from red runs.
  cleanupProblems = [...cleanupRemote(keepMainState), ...cleanupRemote(keepWorkflowState)];
}

// Cleanup failures are part of the verdict. The executor (instructions §2.4)
// trusts the exit code AND report.json — the two must never disagree, so
// cleanup problems are recorded in the report and flip it to fail.
const verdict =
  aborted || scenarios.some((s) => s.status === "fail") || cleanupProblems.length ? "fail" : "pass";
const report = {
  suite: "xwing-deploy-acceptance",
  verdict,
  candidate: candidateFull,
  spaceId: SPACE_ID,
  organizationId: ORG_ID,
  startedAt,
  finishedAt: now(),
  scenarios,
  workdirs: { main: main.root, workflow: workflowFixture?.root || null },
  cleanup: { main: keepMainState, workflow: keepWorkflowState },
  cleanupProblems,
};

mkdirSync(OUT_DIR, { recursive: true });
writeFileSync(join(OUT_DIR, "report.json"), `${JSON.stringify(report, null, 2)}\n`);
console.log(`\nverdict: ${verdict} (${scenarios.filter((s) => s.status === "pass").length}/${scenarios.length} passed)`);
console.log(`report: ${join(OUT_DIR, "report.json")}`);
if (KEEP) console.log("remote resources kept for screenshots; clean up with --cleanup-report");
if (cleanupProblems.length) console.error(`cleanup problems: ${cleanupProblems.join(", ")}`);
process.exit(verdict === "pass" ? 0 : 1);
