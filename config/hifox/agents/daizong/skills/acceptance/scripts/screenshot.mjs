// Evidence screenshots of the task-local dev web UI, plus an optional
// GATING verification pass (--verify <journey.json>): the suite's user
// journey emits a UI-shape-agnostic spec of what an end user must SEE for
// the deployed resources; this script asserts it text-by-text with a
// screenshot per check.
//
// Exit codes (chained invocation contract, instructions §2.5):
//   no --verify: 0 always — evidence tooling never flips the verdict alone
//   --verify:    0 = all UI checks passed
//                1 = an assertion failed (UI-layer gate result)
//                2 = infrastructure unavailable (playwright/chromium) — the
//                    round degrades, ui-verdict.json records verdict=skipped
//
// Usage:
//   node screenshot.mjs --url http://localhost:30000 --out shots/
//   node screenshot.mjs --url ... --out shots/ --verify <dir>/journey.json
// Selectors are overridable via env (LOGIN_EMAIL_SELECTOR etc.) so the agent
// can adapt to UI drift without editing this file.
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const args = process.argv.slice(2);
const flag = (name) => {
  const i = args.indexOf(`--${name}`);
  return i >= 0 ? args[i + 1] : undefined;
};
const BASE_URL = flag("url");
const OUT_DIR = flag("out") || "shots";
const VERIFY_SPEC_PATH = flag("verify");
if (!BASE_URL) {
  console.error("screenshot: --url <frontend-origin> is required");
  process.exit(2);
}
// A present-but-valueless --verify must never silently degrade to the
// evidence-only mode (which always exits 0) — that would bypass the UI gate
// on a caller typo (review F-1).
if (args.includes("--verify") && (!VERIFY_SPEC_PATH || VERIFY_SPEC_PATH.startsWith("--"))) {
  console.error("screenshot: --verify requires a path to the suite's journey.json — refusing to silently disable the UI gate");
  process.exit(2);
}
let VERIFY_SPEC = null;
if (VERIFY_SPEC_PATH) {
  // An unreadable spec (journey chain aborted before emitting it, or a
  // typo'd path) is a supply/infrastructure problem, not an assertion
  // failure: record skipped and exit 2 instead of crashing with exit 1
  // (review F-2).
  try {
    VERIFY_SPEC = JSON.parse(readFileSync(VERIFY_SPEC_PATH, "utf8"));
  } catch (error) {
    mkdirSync(OUT_DIR, { recursive: true });
    writeFileSync(
      join(OUT_DIR, "ui-verdict.json"),
      `${JSON.stringify({ verdict: "skipped", reason: `verify spec unreadable (${VERIFY_SPEC_PATH}): ${error.message?.split("\n")[0]}`, at: new Date().toISOString() }, null, 2)}\n`,
    );
    console.error(`screenshot: verify spec unreadable — ui layer skipped: ${error.message?.split("\n")[0]}`);
    process.exit(2);
  }
}

const EMAIL = process.env.DEVBOT_EMAIL || "devbot@example.com";
const CODE = process.env.DEVBOT_CODE || "888888";
const EMAIL_SELECTOR = process.env.LOGIN_EMAIL_SELECTOR || 'input[type="email"], input[name*="email" i]';
const CODE_SELECTOR = process.env.LOGIN_CODE_SELECTOR || 'input[type="text"], input[name*="code" i], input[inputmode="numeric"]';
const SUBMIT_SELECTOR = process.env.LOGIN_SUBMIT_SELECTOR || 'button[type="submit"], button:has-text("登录"), button:has-text("Continue"), button:has-text("继续")';
const ROUTES = (process.env.SHOT_ROUTES || "/,/agents,/crews,/skills,/workflows").split(",");

// How long any single auth-related wait may take (form reveal, redirect,
// confirmation signal). Page-load budgets stay separate literals.
const LOGIN_TIMEOUT_MS = 30_000;

// Both must be declared: ESM strict mode makes the old bare `browser =`
// assignment throw ReferenceError, and the catch below swallowed it as a
// "startup failure", so the script silently never captured anything.
let chromium;
let browser;
try {
  ({ chromium } = await import("playwright"));
  mkdirSync(OUT_DIR, { recursive: true });
  browser = await chromium.launch({ headless: true });
} catch (error) {
  // Startup failures (playwright missing, chromium binary unavailable, output
  // dir unwritable) are evidence-tooling problems. Without --verify they
  // never flip a verdict (exit 0 + note). With --verify the UI layer is a
  // gate: record verdict=skipped and exit 2 so the executor degrades the
  // round explicitly instead of silently passing an unverified UI.
  if (VERIFY_SPEC) {
    try {
      mkdirSync(OUT_DIR, { recursive: true });
      writeFileSync(join(OUT_DIR, "ui-verdict.json"), `${JSON.stringify({ verdict: "skipped", reason: `startup failed: ${error.message?.split("\n")[0]}`, at: new Date().toISOString() }, null, 2)}\n`);
    } catch { /* nothing more we can honestly report */ }
    console.error(`screenshot: startup failed (ui layer skipped): ${error.message?.split("\n")[0]}`);
    process.exit(2);
  }
  console.error(`screenshot: startup failed (not a gate result): ${error.message?.split("\n")[0]}`);
  process.exit(0);
}
const context = await browser.newContext({ viewport: { width: 1600, height: 1000 } });
const page = await context.newPage();
const notes = [];

const atLogin = (u) => /\/login(\?|$|\/)/.test(typeof u === "string" ? u : u.href);

try {
  // The root route renders a Loading shell while the SPA resolves auth state,
  // so "no form after N seconds" proves nothing (the old probe mistook that
  // shell for an authenticated session and captured login pages). An
  // auth-gated route is deterministic instead: no session → redirect to
  // /login or the form itself appears; valid session → neither. Race the two
  // positive signals rather than guessing from absence.
  await page.goto(new URL("/agents", BASE_URL).toString(), { waitUntil: "domcontentloaded", timeout: 60_000 });
  const email = page.locator(EMAIL_SELECTOR).first();
  const redirectedToLogin = page.waitForURL(atLogin, { timeout: LOGIN_TIMEOUT_MS }).then(() => true, () => false);
  const loginFormShown = email.waitFor({ state: "visible", timeout: LOGIN_TIMEOUT_MS }).then(() => true, () => false);
  if (await Promise.race([redirectedToLogin, loginFormShown])) {
    await email.waitFor({ state: "visible", timeout: LOGIN_TIMEOUT_MS });
    await email.fill(EMAIL);
    await page.locator(SUBMIT_SELECTOR).first().click();
    const code = page.locator(CODE_SELECTOR).first();
    await code.waitFor({ state: "visible", timeout: 20_000 });
    await code.fill(CODE);
    const submit = page.locator(SUBMIT_SELECTOR).first();
    if (await submit.isVisible().catch(() => false)) await submit.click();
    // Confirm auth actually took with a signal a still-loading shell cannot
    // fake. On a redirect flow that is the URL leaving /login. When the form
    // rendered in place (no redirect), the URL never entered /login, so the
    // unfakeable transition is the code input disappearing.
    if (atLogin(page.url())) {
      await page.waitForURL((u) => !atLogin(u), { timeout: LOGIN_TIMEOUT_MS });
    } else {
      await page.locator(CODE_SELECTOR).first().waitFor({ state: "hidden", timeout: LOGIN_TIMEOUT_MS });
    }
    await page.waitForLoadState("networkidle", { timeout: LOGIN_TIMEOUT_MS });
    notes.push("login form filled");
  } else {
    notes.push("no login redirect or form on gated route within 30s (assumed already authenticated)");
  }
  await page.goto(BASE_URL, { waitUntil: "networkidle", timeout: 30_000 });
  await page.screenshot({ path: join(OUT_DIR, "00-home.png"), fullPage: true });

  if (VERIFY_SPEC) {
    // The UI gate: assert what a user SEES, text-based (no pixel compare, no
    // CSS selectors) so presentational churn cannot break the contract.
    const VERIFY_TIMEOUT_MS = Number(process.env.VERIFY_TIMEOUT_MS || 20_000);
    const checks = [];
    for (const check of VERIFY_SPEC.checks || []) {
      const entry = { name: check.name, pass: true, note: "" };
      try {
        const target = new URL(check.route || "/", BASE_URL).toString();
        await page.goto(target, { waitUntil: "networkidle", timeout: 30_000 });
        const texts = [...(check.texts || [])];
        if (check.clickText) texts.push(check.clickText);
        for (const text of texts) {
          await page.locator(`text=${text}`).first().waitFor({ state: "visible", timeout: VERIFY_TIMEOUT_MS });
        }
        if (check.clickText) {
          await page.locator(`text=${check.clickText}`).first().click();
          await page.waitForLoadState("networkidle", { timeout: 30_000 }).catch(() => {});
          for (const text of check.afterTexts || []) {
            await page.locator(`text=${text}`).first().waitFor({ state: "visible", timeout: VERIFY_TIMEOUT_MS });
          }
        }
        await page.screenshot({ path: join(OUT_DIR, `verify-${check.name}.png`), fullPage: true });
        notes.push(`verify ${check.name}: pass`);
      } catch (error) {
        entry.pass = false;
        entry.note = String(error.message || error).split("\n")[0].slice(0, 200);
        await page.screenshot({ path: join(OUT_DIR, `verify-${check.name}-fail.png`), fullPage: true }).catch(() => {});
        notes.push(`verify ${check.name}: FAIL (${entry.note})`);
      }
      checks.push(entry);
    }
    const uiVerdict = checks.every((c) => c.pass) ? "pass" : "fail";
    writeFileSync(join(OUT_DIR, "ui-verdict.json"), `${JSON.stringify({ verdict: uiVerdict, checks, at: new Date().toISOString() }, null, 2)}\n`);
    console.log(`ui verdict: ${uiVerdict}`);
  }

  for (const route of ROUTES) {
    if (!route || route === "/") continue;
    const target = new URL(route, BASE_URL).toString();
    try {
      await page.goto(target, { waitUntil: "networkidle", timeout: 30_000 });
      await page.screenshot({ path: join(OUT_DIR, `${route.replace(/\//g, "_").replace(/^_+|_+$/g, "") || "root"}.png`), fullPage: true });
      notes.push(`captured ${route}`);
    } catch (error) {
      notes.push(`route ${route} failed: ${error.message?.split("\n")[0]}`);
    }
  }
} catch (error) {
  notes.push(`fatal: ${error.message?.split("\n")[0]}`);
  await page.screenshot({ path: join(OUT_DIR, "error.png") }).catch(() => {});
} finally {
  await browser.close();
  // Verify mode guarantees ui-verdict.json on every path: if checks never
  // ran (fatal before them, e.g. login failure), record an explicit skip so
  // the exit code and the verdict file can never disagree (review F-2).
  if (VERIFY_SPEC && !existsSync(join(OUT_DIR, "ui-verdict.json"))) {
    const fatal = notes.find((n) => n.startsWith("fatal:")) || "fatal before UI checks";
    writeFileSync(join(OUT_DIR, "ui-verdict.json"), `${JSON.stringify({ verdict: "skipped", reason: fatal, at: new Date().toISOString() }, null, 2)}\n`);
  }
}

console.log(notes.join("\n"));
process.exit(!VERIFY_SPEC ? 0 : uiExitCode());

function uiExitCode() {
  // ui-verdict.json was written in the verify block; re-read to decide the
  // exit code so the file and the code can never disagree.
  try {
    const verdict = JSON.parse(readFileSync(join(OUT_DIR, "ui-verdict.json"), "utf8")).verdict;
    return verdict === "pass" ? 0 : verdict === "skipped" ? 2 : 1;
  } catch {
    return 1;
  }
}
