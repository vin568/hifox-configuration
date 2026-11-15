// Regression tests for the daizong acceptance skill's screenshot.mjs.
//
// Two field bugs these pin down (HF-15862, 2026-11):
//  1. `browser` was assigned without declaration — ESM strict mode throws
//     ReferenceError, the startup catch swallows it, exit 0, zero shots.
//  2. The login probe decided "already authenticated" from absence (no form
//     within 5s on a Loading shell) and captured login pages as evidence.
//
// Layout mirrors the source tree: test/<agent>/<skill>/<script>.test.mjs.
// The script under test is copied verbatim into a temp dir; the playwright
// stub in test/fixtures/playwright-stub is installed beside it as
// node_modules/playwright so `await import("playwright")` resolves to it.
import { spawnSync } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import assert from "node:assert/strict";
import test from "node:test";

const SCRIPT_URL = new URL("../../../config/hifox/agents/daizong/skills/acceptance/scripts/screenshot.mjs", import.meta.url);
const STUB_URL = new URL("../../fixtures/playwright-stub", import.meta.url);

function runScript({ args = [], env = {}, stub = true, files = {} } = {}) {
  const dir = mkdtempSync(join(tmpdir(), "shot-test-"));
  const scriptCopy = join(dir, "screenshot.mjs");
  writeFileSync(scriptCopy, readFileSync(SCRIPT_URL));
  for (const [name, content] of Object.entries(files)) {
    writeFileSync(join(dir, name), content);
  }
  if (stub) {
    mkdirSync(join(dir, "node_modules"));
    cpSync(STUB_URL, join(dir, "node_modules", "playwright"), { recursive: true });
  }
  const stubLog = join(dir, "stub.log");
  writeFileSync(stubLog, "");
  const res = spawnSync(process.execPath, [scriptCopy, ...args], {
    cwd: dir,
    encoding: "utf8",
    env: {
      ...process.env,
      STUB_LOG: stubLog,
      DEVBOT_EMAIL: "devbot@example.com",
      ...env,
    },
  });
  const raw = readFileSync(stubLog, "utf8");
  const events = raw.trim() ? raw.trim().split("\n").map((line) => JSON.parse(line)) : [];
  return { dir, res, events };
}

const shots = (events) => events.filter((e) => e.type === "screenshot");

test("missing --url is a usage error, not a gate pass", () => {
  const { res } = runScript({ args: ["--out", "shots"] });
  assert.equal(res.status, 2);
});

test("playwright unavailable degrades to a note without flipping the gate (exit 0, zero shots)", () => {
  const { res, events } = runScript({ args: ["--url", "http://app.test"], stub: false });
  assert.equal(res.status, 0, res.stderr);
  assert.match(res.stderr, /startup failed/);
  assert.equal(res.stdout.trim(), "");
  assert.equal(shots(events).length, 0);
});

test("cold login: enters via gated route, fills two-step code, captures only authenticated pages", () => {
  const { res, events } = runScript({ args: ["--url", "http://app.test", "--out", "shots"] });
  assert.equal(res.status, 0, res.stderr);
  assert.match(res.stdout, /login form filled/);

  // Probe must enter through an auth-gated route, not the shell-rendering root.
  const gotos = events.filter((e) => e.type === "goto");
  assert.ok(gotos.length > 0, "expected goto events");
  assert.match(gotos[0].url, /\/agents$/);

  // The unauthenticated visit redirected before login.
  assert.ok(events.some((e) => e.type === "redirect" && /\/login/.test(e.url)));

  // Two-step login: email then code fills, two submits.
  const fills = events.filter((e) => e.type === "fill");
  assert.deepEqual(fills.map((f) => f.kind), ["email", "code"]);
  assert.equal(events.filter((e) => e.type === "click").length, 2);

  // Every capture must be an authenticated page — the old bug shot /login.
  const captured = shots(events);
  assert.deepEqual(
    captured.map((s) => s.path.replace(/^.*\//, "")),
    ["00-home.png", "agents.png", "crews.png", "skills.png", "workflows.png"],
  );
  assert.ok(captured.every((s) => s.authed), "screenshot taken before authentication");
  assert.match(captured[0].url, /^http:\/\/app\.test\/?$/);

});

test("already-authenticated environment: no false login attempt", () => {
  const { res, events } = runScript({ args: ["--url", "http://app.test", "--out", "shots"], env: { STUB_AUTHED: "1" } });
  assert.equal(res.status, 0, res.stderr);
  assert.match(res.stdout, /assumed already authenticated/);
  assert.equal(events.filter((e) => e.type === "fill" || e.type === "click").length, 0);
  const captured = shots(events);
  assert.equal(captured.length, 5);
  assert.ok(captured.every((s) => s.authed));
});

test("bad login code: fatal note, single error shot, still exit 0 (evidence tooling never flips verdict)", () => {
  const { res, events } = runScript({
    args: ["--url", "http://app.test", "--out", "shots"],
    env: { DEVBOT_CODE: "000000", STUB_EXPECTED_CODE: "888888" },
  });
  assert.equal(res.status, 0, res.stderr);
  assert.match(res.stdout, /fatal/);
  assert.ok(events.some((e) => e.type === "auth-failed"));
  const captured = shots(events);
  assert.equal(captured.length, 1);
  assert.match(captured[0].path, /error\.png$/);
  assert.equal(captured[0].authed, false);
});

test("inline login form (no redirect): code input disappearing confirms auth", () => {
  const { res, events } = runScript({
    args: ["--url", "http://app.test", "--out", "shots"],
    env: { STUB_INLINE_FORM: "1" },
  });
  assert.equal(res.status, 0, res.stderr);
  assert.match(res.stdout, /login form filled/);
  // No redirect ever happened: the form rendered in place on the gated route.
  assert.equal(events.filter((e) => e.type === "redirect").length, 0);
  const captured = shots(events);
  assert.equal(captured.length, 5);
  assert.ok(captured.every((s) => s.authed), "screenshot taken before authentication");
});

test("inline login form with bad code: the vacuous confirmation must still fatal", () => {
  const { res, events } = runScript({
    args: ["--url", "http://app.test", "--out", "shots"],
    env: { STUB_INLINE_FORM: "1", DEVBOT_CODE: "000000", STUB_EXPECTED_CODE: "888888" },
  });
  assert.equal(res.status, 0, res.stderr);
  assert.match(res.stdout, /fatal/);
  assert.ok(events.some((e) => e.type === "auth-failed"));
  const captured = shots(events);
  assert.equal(captured.length, 1);
  assert.match(captured[0].path, /error\.png$/);
  assert.equal(captured[0].authed, false);
});

test("per-route capture failure is noted and never gates (exit 0, other shots taken)", () => {
  const { res, events } = runScript({
    args: ["--url", "http://app.test", "--out", "shots"],
    env: { STUB_FAIL_ROUTE: "/skills" },
  });
  assert.equal(res.status, 0, res.stderr);
  assert.match(res.stdout, /route \/skills failed/);
  assert.match(res.stdout, /captured \/agents/);
  assert.match(res.stdout, /captured \/workflows/);
  const captured = shots(events);
  assert.deepEqual(
    captured.map((s) => s.path.replace(/^.*\//, "")),
    ["00-home.png", "agents.png", "crews.png", "workflows.png"],
  );
  assert.ok(captured.every((s) => s.authed));
});
// ---------------------------------------------------------------------------
// --verify: the UI gate. journey.json-style spec, text assertions, per-check
// screenshots, ui-verdict.json, exit codes 0/1/2.
// ---------------------------------------------------------------------------
const VERIFY_SPEC = {
  kind: "ui-verify",
  checks: [
    { name: "agents-list", route: "/agents", texts: ["Marlin Prime - journey-x", "Skipper - journey-x"] },
    {
      name: "agent-detail",
      route: "/agents",
      clickText: "Marlin Prime - journey-x",
      afterTexts: ["Marlin runs the e2e journey", "https://cnb.cool/tars-lab/xwing"],
    },
  ],
};
const VERIFY_PAGES = {
  "/agents": {
    texts: ["Marlin Prime - journey-x", "Skipper - journey-x"],
    click: { "Marlin Prime - journey-x": "/agents/marlin" },
  },
  "/agents/marlin": {
    texts: ["Marlin runs the e2e journey", "https://cnb.cool/tars-lab/xwing"],
  },
};

test("verify pass: every check asserts, clicks through to detail, writes ui-verdict pass, exit 0", () => {
  const { dir, res, events } = runScript({
    args: ["--url", "http://app.test", "--out", "shots", "--verify", "journey.json"],
    env: { STUB_AUTHED: "1", STUB_VERIFY_PAGES: JSON.stringify(VERIFY_PAGES) },
    files: { "journey.json": JSON.stringify(VERIFY_SPEC) },
  });
  assert.equal(res.status, 0, res.stderr);
  assert.match(res.stdout, /ui verdict: pass/);
  const verdict = JSON.parse(readFileSync(join(dir, "shots", "ui-verdict.json"), "utf8"));
  assert.equal(verdict.verdict, "pass");
  assert.equal(verdict.checks.length, 2);
  assert.ok(verdict.checks.every((c) => c.pass));
  const clicks = events.filter((e) => e.type === "click-text");
  assert.equal(clicks.length, 1);
  assert.equal(clicks[0].to, "/agents/marlin");
  const names = shots(events).map((s) => s.path.replace(/^.*\//, ""));
  assert.ok(names.includes("verify-agents-list.png"));
  assert.ok(names.includes("verify-agent-detail.png"));
});

test("verify fail: a text the UI never shows fails the check, ui-verdict fail, exit 1", () => {
  const { dir, res } = runScript({
    args: ["--url", "http://app.test", "--out", "shots", "--verify", "journey.json"],
    env: {
      STUB_AUTHED: "1",
      VERIFY_TIMEOUT_MS: "1000",
      // Pages spec lists only Marlin on /agents: agents-list fails on the
      // missing Skipper text; the detail click has no navigation map, so its
      // afterTexts never appear — both failure paths red.
      STUB_VERIFY_PAGES: JSON.stringify({ "/agents": { texts: ["Marlin Prime - journey-x"] } }),
      SHOT_ROUTES: "",
    },
    files: { "journey.json": JSON.stringify(VERIFY_SPEC) },
  });
  assert.equal(res.status, 1, res.stderr);
  assert.match(res.stdout, /ui verdict: fail/);
  const verdict = JSON.parse(readFileSync(join(dir, "shots", "ui-verdict.json"), "utf8"));
  assert.equal(verdict.verdict, "fail");
});

test("verify + playwright unavailable: explicit skip (exit 2), ui-verdict records it", () => {
  const { dir, res } = runScript({
    args: ["--url", "http://app.test", "--out", "shots", "--verify", "journey.json"],
    stub: false,
    files: { "journey.json": JSON.stringify(VERIFY_SPEC) },
  });
  assert.equal(res.status, 2, res.stderr);
  assert.match(res.stderr, /ui layer skipped/);
  const verdict = JSON.parse(readFileSync(join(dir, "shots", "ui-verdict.json"), "utf8"));
  assert.equal(verdict.verdict, "skipped");
});
test("verify without a path value refuses to silently disable the UI gate (exit 2, review F-1)", () => {
  const { res } = runScript({
    args: ["--url", "http://app.test", "--out", "shots", "--verify"],
    env: { STUB_AUTHED: "1" },
  });
  assert.equal(res.status, 2, res.stderr);
  assert.match(res.stderr, /--verify requires a path/);
});

test("verify with an unreadable spec: explicit skip (exit 2) + ui-verdict records it (review F-2)", () => {
  const { dir, res } = runScript({
    args: ["--url", "http://app.test", "--out", "shots", "--verify", "no-such-journey.json"],
    env: { STUB_AUTHED: "1" },
  });
  assert.equal(res.status, 2, res.stderr);
  assert.match(res.stderr, /ui layer skipped/);
  const verdict = JSON.parse(readFileSync(join(dir, "shots", "ui-verdict.json"), "utf8"));
  assert.equal(verdict.verdict, "skipped");
  assert.match(verdict.reason, /no-such-journey\.json/);
});

test("verify with fatal login: verdict file still written, skip semantics (exit 2, review F-2)", () => {
  const { dir, res } = runScript({
    args: ["--url", "http://app.test", "--out", "shots", "--verify", "journey.json"],
    env: {
      STUB_EXPECTED_CODE: "000000", // login can never succeed
      SHOT_ROUTES: "",
    },
    files: { "journey.json": JSON.stringify(VERIFY_SPEC) },
  });
  assert.equal(res.status, 2, res.stderr);
  assert.match(res.stdout, /fatal/);
  const verdict = JSON.parse(readFileSync(join(dir, "shots", "ui-verdict.json"), "utf8"));
  assert.equal(verdict.verdict, "skipped");
  assert.match(verdict.reason, /fatal/);
});
