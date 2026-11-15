// Pure helpers for the derived command-surface layer of the acceptance
// suite. No I/O: everything here is fed strings/arrays and returns
// data, so it is unit-testable without a candidate or a task-local env.
//
// Design contract (HF-15862 review, 2026-11): the suite must NOT maintain a
// hand-written list of x-deploy commands. The command surface is read at
// run time from the candidate's own `--help` output (which x-deploy renders
// from its single-source registry, ADR 0013), so a new command enters the
// smoke net automatically and a removed command shows up as a coverage
// diff. The only per-command knowledge allowed anywhere is the
// `directMutators` denylist (commands that mutate without an --apply gate
// and therefore must never run against a fixture root).

// Parse `x-deploy --help` output into [{ name, args }]. Command lines look
// like "  x-deploy init          [DIR] [--layout ...]"; any other line
// (Usage:, Options:, prose) does not match. One command per line is the
// render contract; a wrapped line simply will not parse and will surface in
// the coverage matrix as an unlisted command, never silently.
export function parseCommandSurface(helpText) {
  const commands = [];
  for (const line of String(helpText).split("\n")) {
    const match = /^ {2}x-deploy ([a-z][a-z0-9-]*)\s*(.*)$/.exec(line);
    if (match) commands.push({ name: match[1], args: match[2].trim() });
  }
  return commands;
}

// Does the usage line require arguments? Strip every optional [...] group;
// any identifier left over (a bare word, <placeholder>, or --flag) means the
// command cannot be invoked generically. "(a | b)" choice groups and
// mandatory flags like `--environment NAME` both count as required.
export function hasRequiredArgs(args) {
  const stripped = String(args).replace(/\[[^\]]*\]/g, " ");
  return /[A-Za-z]/.test(stripped);
}

// Tier plan for one command (zero per-command knowledge; shape rules only):
// - "bare":    every command. Run with no arguments in an empty cwd; must
//              fail cleanly (or print info) — never a stack trace, never a
//              side-effect file. Pins argument-parse regressions and crashes.
// - "preview": usage contains [--apply] (the gate marker) and no required
//              args. Run WITHOUT --apply on a fixture root; state.yaml must
//              stay byte-identical (the gate itself is the contract).
// - "read":    usage contains [--environment NAME], no required args, no
//              [--apply]. Run against the fixture; must exit 0.
// Direct mutators (init/capture write user assets with no gate) get "bare"
// only — the denylist is the single allowed piece of command-name knowledge.
export function tiersFor({ name, args }, directMutators = ["init", "capture"]) {
  const tiers = ["bare"];
  if (directMutators.includes(name)) return tiers;
  const gated = /\[--apply\]/.test(args);
  const envFlag = /\[--environment NAME\]/.test(args);
  if (hasRequiredArgs(args)) return tiers;
  if (gated) tiers.push("preview");
  else if (envFlag) tiers.push("read");
  return tiers;
}

// Measured (not declared) coverage matrix: cross-reference the parsed
// command surface with what actually ran. `invocations` are deployRun calls
// logged by the suite ({ command, scenario, chain }); `smokeResults` are the
// tier executions ({ command, tier, status }). A command no pinned scenario
// or journey step exercised and only reachable via bare-tier smoke is
// visible as such — coverage is a measurement, never a claim.
export function buildCoverage(commands, invocations = [], smokeResults = []) {
  return commands.map(({ name, args }) => {
    const pinnedBy = [
      ...new Set(invocations.filter((i) => i.command === name && i.chain !== "smoke").map((i) => i.scenario)),
    ];
    const smoke = smokeResults
      .filter((r) => r.command === name)
      .map((r) => ({ tier: r.tier, status: r.status }));
    return {
      command: name,
      requiredArgs: hasRequiredArgs(args),
      pinnedBy,
      smoke,
      exercised: pinnedBy.length > 0 || smoke.some((r) => r.status === "pass"),
    };
  });
}
