// Unit tests for the derived command-surface helpers (lib/surface.mjs).
// Fixture: the real `x-deploy --help` usage block (captured 2026-11-16 from
// vendor/x-deploy dev, ADR 0031 era (capture previews by default; --user owner batch; --refresh re-capture). If x-deploy's help render format
// ever changes shape, these tests fail before the suite silently loses its
// derived coverage — that is the point.
import assert from "node:assert/strict";
import test from "node:test";
import { DIRECT_MUTATORS, parseCommandSurface, hasRequiredArgs, tiersFor, buildCoverage } from "../../../config/hifox/agents/daizong/skills/acceptance/scripts/lib/surface.mjs";

const HELP = `Usage:
  x-deploy init          [DIR] [--layout dedicated|embedded] [--config-set NAME] [--environment NAME] [--cli-source PATH] [--repo-url URL] [--ref NAME]
  x-deploy update        [--apply]
  x-deploy validate      [--environment NAME] [--suffix TEXT]
  x-deploy build         <workflow-key> [--environment NAME] [--out PATH]
  x-deploy create        --environment NAME --suffix TEXT [--apply]
  x-deploy clone         --environment NAME --suffix TEXT [--apply]
  x-deploy plan          [--environment NAME] [--suffix TEXT] [--verbose] [--json] [--no-color] [--detailed-exitcode] [--diff[=N]] [--diff-tool CMD] [--only KIND/KEY]
  x-deploy deploy        [--environment NAME] [--suffix TEXT] [--verbose] [--apply]
  x-deploy pull          [--environment NAME]
  x-deploy backup        [--environment NAME] [--output-dir PATH]
  x-deploy capture       --environment NAME --kind (workflow|automation|agent|crew|skill) ((--id ID | --name NAME)… | --user NAME)… [--key KEY] [--form graph|yaml] [--refresh] [--allow-lossy] [--apply]
  x-deploy bind          [--environment NAME] (--organization NAME|SLUG | --space NAME|IDENTIFIER | --computer NAME --runtime PROVIDER)… [--apply]
  x-deploy restore       --from PATH [--to PATH] [--environment NAME] [--apply]
  x-deploy model         <model-id|default> [--environment NAME] [--apply]
  x-deploy prune         (backups [--keep N] | snapshots | workflows | automations) [--environment NAME] [--output-dir PATH] [--apply]
  x-deploy doctor
  x-deploy guide         [topic]
  x-deploy --version

Options:`;

test("parseCommandSurface: every command line parses, non-command lines do not", () => {
  const commands = parseCommandSurface(HELP);
  assert.deepEqual(
    commands.map((c) => c.name),
    [
      "init", "update", "validate", "build", "create", "clone", "plan", "deploy",
      "pull", "backup", "capture", "bind", "restore", "model", "prune", "doctor", "guide",
    ],
  );
  // Usage:, Options:, and the --version line must not leak in as commands.
  assert.ok(commands.every((c) => !c.name.startsWith("-")));
});

test("parseCommandSurface: an unknown render shape must surface, not vanish", () => {
  // If a future help wraps or reformats, the command disappears from the
  // parsed surface — the coverage matrix flags it. Here: a mangled line is
  // simply absent, and the caller (suite) compares against nothing — this
  // test pins that parsing never invents commands from prose.
  const commands = parseCommandSurface("Usage:\n  x-deploy plan (renders plans)\nprose line");
  assert.deepEqual(commands.map((c) => c.name), ["plan"]);
  assert.equal(commands[0].args, "(renders plans)");
});

test("hasRequiredArgs: brackets stripped, choice groups and bare words count", () => {
  assert.equal(hasRequiredArgs("[--environment NAME] [--apply]"), false);
  assert.equal(hasRequiredArgs("[DIR] [--layout embedded|dedicated]"), false);
  assert.equal(hasRequiredArgs("--environment NAME --suffix TEXT [--apply]"), true);
  assert.equal(hasRequiredArgs("<workflow-key> [--environment NAME] [--out PATH]"), true);
  assert.equal(hasRequiredArgs("(backups [--keep N] | snapshots) [--apply]"), true);
  assert.equal(hasRequiredArgs("[--environment NAME] (--organization NAME|SLUG | --space NAME|IDENTIFIER)… [--apply]"), true);
  assert.equal(hasRequiredArgs("--apply [--environment NAME]"), true);
});

test("tiersFor: shape rules only — gate marker, env flag, required args, direct denylist", () => {
  const plan = { name: "plan", args: "[--environment NAME] [--suffix TEXT] [--verbose]" };
  const update = { name: "update", args: "[--apply]" };
  const create = { name: "create", args: "--environment NAME --suffix TEXT [--apply]" };
  const init = { name: "init", args: "[DIR] [--config-set NAME] [--environment NAME]" };
  const doctor = { name: "doctor", args: "" };
  const guide = { name: "guide", args: "[topic]" };
  assert.deepEqual(tiersFor(plan), ["bare", "read"]);
  assert.deepEqual(tiersFor(update), ["bare", "preview"]);
  assert.deepEqual(tiersFor(create), ["bare"]); // required args: no generic invocation
  assert.deepEqual(tiersFor(init), ["bare"]); // direct mutator denylist, never on a fixture
  assert.deepEqual(tiersFor(doctor), ["bare"]); // no env flag, no gate
  assert.deepEqual(tiersFor(guide), ["bare"]); // optional topic only
});

test("tiersFor: direct denylist is the only name knowledge allowed", () => {
  // A hypothetical new no-gate mutator is NOT in the denylist: it would get
  // read/preview tiers. The read tier only runs commands whose usage carries
  // [--environment NAME] AND no required args — a mutator that mutates with
  // zero required args on a bare flag shape is the residual risk the
  // coverage matrix makes visible (smoke-only), by design.
  const hypothetical = { name: "nuke", args: "" };
  assert.deepEqual(tiersFor(hypothetical), ["bare"]);
});

test("buildCoverage: measured provenance — pinned from invocations, smoke from tier results", () => {
  const commands = parseCommandSurface(HELP);
  const invocations = [
    { command: "deploy", scenario: "create: full provisioning lands and state registers ids", chain: "main" },
    { command: "create", scenario: "create: full provisioning lands and state registers ids", chain: "main" },
    { command: "backup", scenario: "user-journey: init → bind → create → converge → backup", chain: "journey" },
    { command: "bind", scenario: "user-journey: init → bind → create → converge → backup", chain: "journey" },
    { command: "plan", scenario: "smoke: derived command surface", chain: "smoke" },
  ];
  const smokeResults = [
    { command: "plan", tier: "read", status: "pass" },
    { command: "model", tier: "bare", status: "pass" },
    { command: "guide", tier: "bare", status: "pass" },
  ];
  const coverage = buildCoverage(commands, invocations, smokeResults);
  const by = Object.fromEntries(coverage.map((c) => [c.command, c]));
  // Pinned + smoke.
  assert.ok(by.deploy.pinnedBy.length === 1 && by.deploy.exercised);
  // Journey provenance is pinned provenance too.
  assert.deepEqual(by.bind.pinnedBy, ["user-journey: init → bind → create → converge → backup"]);
  // Smoke-only commands stay visible instead of silently uncovered.
  assert.equal(by.model.pinnedBy.length, 0);
  assert.deepEqual(by.model.smoke, [{ tier: "bare", status: "pass" }]);
  assert.ok(by.model.exercised);
  // Never-touched commands are flagged, not hidden.
  assert.equal(by.clone.pinnedBy.length, 0);
  assert.equal(by.clone.smoke.length, 0);
  assert.equal(by.clone.exercised, false);
  // Smoke-chain invocations do not count as pinned provenance.
  assert.equal(by.plan.pinnedBy.length, 0);
});

test("DIRECT_MUTATORS: init is the sole direct mutator (ADR 0031 moved capture behind --apply)", () => {
  // Re-adding a command here silently relaxes the bare-tier "no side-effect
  // files" assertion — pin the exact list so the denylist is itself under test.
  assert.deepEqual(DIRECT_MUTATORS, ["init"]);
});
