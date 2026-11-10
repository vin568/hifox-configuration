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

let chromium;
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

try {
  await page.goto(BASE_URL, { waitUntil: "networkidle", timeout: 60_000 });
  const email = page.locator(EMAIL_SELECTOR).first();
  if (await email.isVisible({ timeout: 5_000 }).catch(() => false)) {
    await email.fill(EMAIL);
    await page.locator(SUBMIT_SELECTOR).first().click();
    const code = page.locator(CODE_SELECTOR).first();
    await code.waitFor({ state: "visible", timeout: 20_000 });
    await code.fill(CODE);
    const submit = page.locator(SUBMIT_SELECTOR).first();
    if (await submit.isVisible({ timeout: 3_000 }).catch(() => false)) await submit.click();
    await page.waitForLoadState("networkidle", { timeout: 30_000 });
    notes.push("login form filled");
  } else {
    notes.push("no login form visible (assumed already authenticated)");
  }
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
