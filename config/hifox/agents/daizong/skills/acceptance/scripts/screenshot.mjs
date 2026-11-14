#!/usr/bin/env node
// Best-effort evidence screenshots of the task-local dev web UI.
// Not a gate: failures change no verdict, but must be noted in the report.
//
// Usage: node screenshot.mjs --url http://localhost:30000 --out shots/
// Selectors are overridable via env (LOGIN_EMAIL_SELECTOR etc.) so the agent
// can adapt to UI drift without editing this file.
import { mkdirSync } from "node:fs";
import { join } from "node:path";

const args = process.argv.slice(2);
const flag = (name) => {
  const i = args.indexOf(`--${name}`);
  return i >= 0 ? args[i + 1] : undefined;
};
const BASE_URL = flag("url");
const OUT_DIR = flag("out") || "shots";
if (!BASE_URL) {
  console.error("screenshot: --url <frontend-origin> is required");
  process.exit(2);
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
  // dir unwritable) are evidence-tooling problems, not gate results: note and
  // exit 0 so a chained invocation can never flip the verdict (instructions
  // §2.5: screenshot failures annotate the report, never change it).
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
}

console.log(notes.join("\n"));
process.exit(0);
