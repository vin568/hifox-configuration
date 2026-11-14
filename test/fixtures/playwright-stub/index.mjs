// Deterministic playwright stub for testing screenshot.mjs without a browser.
//
// It models exactly the auth shape that bit the real probe: the root/gated
// routes render a shell and redirect to /login only after a delay, and the
// login form appears after a cold-start delay. Modes (env):
//   STUB_AUTHED=1        start authenticated (no login anywhere)
//   STUB_INLINE_FORM=1   form renders in place on the gated route, NO redirect
//   STUB_FAIL_ROUTE=p    goto(p) rejects (per-route capture failure)
//   STUB_FORM_DELAY_MS / STUB_REDIRECT_DELAY_MS / STUB_EXPECTED_CODE
//
// Contract fidelity rules (review round 1, F-1/F-2/F-4) — the fake must be
// NARROWER than real playwright, never wider:
//   - goto() rejects non-string URLs (real playwright does; observed live).
//   - fill()/click() throw when the element is not visible (real playwright
//     auto-waits for visibility then times out; we fail fast).
//   - selectors are matched against an exact whitelist of the script's
//     default selectors; anything else is never visible (a typo'd selector
//     must fail like it would in a real browser, not "match" loosely).
//
// Every observable interaction is appended to $STUB_LOG as JSON lines so
// tests can assert on the exact control flow, including whether screenshots
// were taken while authenticated (the old bug captured login pages).
import { appendFileSync } from "node:fs";

const log = (event) => appendFileSync(process.env.STUB_LOG, `${JSON.stringify(event)}\n`);
// Long script timeouts would make failure-path tests crawl; shrink them.
const scale = (ms) => (ms >= 1000 ? Math.max(50, Math.round(ms / 100)) : ms);

const AUTHED = process.env.STUB_AUTHED === "1";
const INLINE = process.env.STUB_INLINE_FORM === "1";
const FAIL_ROUTE = process.env.STUB_FAIL_ROUTE;
const FORM_DELAY = Number(process.env.STUB_FORM_DELAY_MS ?? 100);
const REDIRECT_DELAY = Number(process.env.STUB_REDIRECT_DELAY_MS ?? 30);
// Independent of DEVBOT_CODE so tests can simulate a wrong configured code.
const EXPECTED_CODE = process.env.STUB_EXPECTED_CODE ?? process.env.DEVBOT_CODE ?? "888888";

// loginStep: 0 shell · 1 email+submit visible · 2 code visible · 3 form gone.
const state = { authed: AUTHED, loginStep: 0, url: "about:blank", values: {}, listeners: new Set() };

const SELECTOR_KINDS = new Map([
  ['input[type="email"], input[name*="email" i]', "email"],
  ['input[type="text"], input[name*="code" i], input[inputmode="numeric"]', "code"],
  ['button[type="submit"], button:has-text("登录"), button:has-text("Continue"), button:has-text("继续")', "submit"],
]);

const path = () => {
  try {
    return new URL(state.url).pathname;
  } catch {
    return state.url;
  }
};
const notify = () => {
  for (const fn of [...state.listeners]) fn();
};
// Cold start: the SPA renders its Loading shell first, the form later.
const scheduleLoginForm = () => {
  setTimeout(() => {
    state.loginStep = 1;
  }, FORM_DELAY);
};
const visibility = (kind) => {
  if (kind === "unknown") return false;
  const atForm = INLINE ? true : path() === "/login";
  if (!atForm || state.loginStep >= 3) return false;
  if (kind === "code") return state.loginStep >= 2;
  if (kind === "email" || kind === "submit") return state.loginStep >= 1;
  return false;
};
const onSubmit = () => {
  if (INLINE ? state.loginStep < 1 : path() !== "/login") return;
  if (state.loginStep === 1) {
    state.loginStep = 2;
    return;
  }
  if (state.loginStep >= 2) {
    if (state.values.code === EXPECTED_CODE) {
      state.authed = true;
      state.loginStep = 3; // form gone
      if (!INLINE) {
        state.url = `${new URL(state.url).origin}/agents`;
      }
      log({ type: "auth-ok", url: state.url });
      notify();
    } else {
      log({ type: "auth-failed" });
    }
  }
};

const page = {
  url() {
    return state.url;
  },
  async goto(url, _opts) {
    // Real playwright rejects non-string URLs (observed live: "expected
    // string, got object"). The stub must not be wider than the real API.
    if (typeof url !== "string") {
      throw new Error(`page.goto: url: expected string, got ${url === null ? "null" : typeof url}`);
    }
    const u = new URL(url);
    if (FAIL_ROUTE && u.pathname === FAIL_ROUTE) {
      throw new Error(`net::ERR_CONNECTION_REFUSED (${url})`);
    }
    log({ type: "goto", url });
    state.url = url;
    state.loginStep = 0;
    if (u.pathname === "/login") {
      scheduleLoginForm();
      notify();
      return url;
    }
    if (state.authed) {
      notify();
      return url;
    }
    if (INLINE) {
      // Unauthenticated gated route renders the form in place; no redirect.
      scheduleLoginForm();
      notify();
      return url;
    }
    // Unauthenticated gated route: client-side redirect to /login, then the
    // form appears after the same cold-start delay.
    setTimeout(() => {
      state.url = `${u.origin}/login?next=${encodeURIComponent(u.pathname + u.search)}`;
      log({ type: "redirect", url: state.url });
      scheduleLoginForm();
      notify();
    }, REDIRECT_DELAY);
    return url;
  },
  async waitForURL(pred, opts = {}) {
    const matches = () => {
      try {
        return pred(new URL(state.url));
      } catch {
        return pred(state.url);
      }
    };
    if (matches()) return;
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        state.listeners.delete(check);
        reject(new Error("TimeoutError: waitForURL timed out"));
      }, scale(opts.timeout ?? 30_000));
      const check = () => {
        if (matches()) {
          clearTimeout(timer);
          state.listeners.delete(check);
          resolve();
        }
      };
      state.listeners.add(check);
    });
  },
  locator(selector) {
    const kind = SELECTOR_KINDS.get(selector) ?? "unknown";
    const self = {
      first: () => self,
      isVisible: () => Promise.resolve(visibility(kind)),
      waitFor: (opts = {}) =>
        new Promise((resolve, reject) => {
          const wantVisible = opts.state !== "hidden";
          const limit = scale(opts.timeout ?? 30_000);
          const started = Date.now();
          const tick = () => {
            if (visibility(kind) === wantVisible) return resolve();
            if (Date.now() - started >= limit) {
              return reject(new Error(`TimeoutError: locator(${kind}) ${wantVisible ? "never visible" : "still visible"}`));
            }
            setTimeout(tick, 5);
          };
          tick();
        }),
      async fill(value) {
        if (!visibility(kind)) throw new Error(`TimeoutError: locator(${kind}) not visible for fill`);
        state.values[kind] = value;
        log({ type: "fill", kind, value });
      },
      async click() {
        if (!visibility(kind)) throw new Error(`TimeoutError: locator(${kind}) not visible for click`);
        log({ type: "click", kind });
        onSubmit();
      },
    };
    return self;
  },
  async waitForLoadState(_state, _opts) {},
  async screenshot(opts = {}) {
    log({ type: "screenshot", path: opts.path, url: state.url, authed: state.authed });
  },
};

const browser = {
  async newContext(_opts) {
    return { newPage: async () => page };
  },
  async close() {
    log({ type: "close" });
  },
};

export const chromium = {
  async launch(_opts) {
    log({ type: "launch" });
    return browser;
  },
};
export default { chromium };
