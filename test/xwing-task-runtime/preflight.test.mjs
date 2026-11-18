// Tests for the shared environment preflight (skills/xwing-task-runtime/scripts/preflight.sh).
// The script is the P0 gate every xwing agent runs before touching a task; a surface
// that silently stops checking (or starts passing without its tools) would make
// missing-tool reports disappear instead of surfacing as blockers.
import assert from "node:assert/strict";
import { execFile, execFileSync } from "node:child_process";
import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

function which(name) {
  return execFileSync("/usr/bin/env", ["which", name], { encoding: "utf8" }).trim();
}

const SCRIPT = fileURLToPath(
  new URL("../../skills/xwing-task-runtime/scripts/preflight.sh", import.meta.url),
);

const BASE_TOOLS = ["git", "node", "pnpm", "make"];
// The script shells out only to these; the sandbox PATH holds nothing else so a
// tool the host happens to have installed (xcodebuild, adb) cannot leak in.
const COREUTILS = ["sed", "head", "sort", "tail"];
// Host paths differ per OS (macOS has no /usr/bin/ls, for example); resolve each
// utility once instead of hard-coding /usr/bin.
const SH = (cmd) => `#!/bin/sh\nexec ${cmd} "$@"\n`;
const HOST_CMD = Object.fromEntries(
  COREUTILS.map((name) => [name, which(name)]),
);

function makeSandbox(tools, { javaMajor, sdk } = {}) {
  const root = mkdtempSync(join(tmpdir(), "preflight-test-"));
  const bin = join(root, "bin");
  mkdirSync(bin);
  for (const name of COREUTILS) {
    writeFileSync(join(bin, name), SH(HOST_CMD[name]));
    chmodSync(join(bin, name), 0o755);
  }
  for (const name of [...tools, ...BASE_TOOLS]) {
    const path = join(bin, name);
    writeFileSync(path, "#!/bin/sh\nexit 0\n");
    chmodSync(path, 0o755);
  }
  let javaHome = join(root, "no-jdk");
  if (javaMajor !== undefined) {
    javaHome = join(root, "jdk");
    mkdirSync(join(javaHome, "bin"), { recursive: true });
    const java = join(javaHome, "bin", "java");
    writeFileSync(java, `#!/bin/sh\necho 'openjdk version "${javaMajor}.0.1" 2026-01-01' >&2\n`);
    chmodSync(java, 0o755);
  }
  let androidHome = "";
  if (sdk) {
    androidHome = join(root, "android-sdk");
    // platform-tools/adb and versioned build-tools are the two shapes the
    // resolver special-cases; both must be reachable without PATH.
    mkdirSync(join(androidHome, "platform-tools"), { recursive: true });
    writeFileSync(join(androidHome, "platform-tools", "adb"), "#!/bin/sh\nexit 0\n");
    chmodSync(join(androidHome, "platform-tools", "adb"), 0o755);
    for (const version of ["9.0.0", "36.0.0"]) {
      const dir = join(androidHome, "build-tools", version);
      mkdirSync(dir, { recursive: true });
      for (const tool of ["aapt2", "apksigner"]) {
        writeFileSync(join(dir, tool), "#!/bin/sh\nexit 0\n");
        chmodSync(join(dir, tool), 0o755);
      }
    }
  }
  return { root, bin, javaHome, androidHome };
}

function runSurfaces(sandbox, ...surfaces) {
  return new Promise((resolve) => {
    execFile(
      "/bin/bash",
      [SCRIPT, ...surfaces],
      {
        env: {
          PATH: sandbox.bin,
          HOME: sandbox.root,
          JAVA_HOME: sandbox.javaHome,
          ANDROID_HOME: sandbox.androidHome,
          ANDROID_SDK_ROOT: "",
          TASK_ENV_CHROME_EXECUTABLE: "",
          CHROME_PATH: "",
        },
      },
      (error, stdout, stderr) => resolve({ code: error ? error.code : 0, stdout, stderr }),
    );
  });
}

test("unknown surface exits 2 and names the mobile surfaces", async () => {
  const sandbox = makeSandbox([]);
  try {
    const { code, stderr } = await runSurfaces(sandbox, "mobile");
    assert.equal(code, 2);
    assert.match(stderr, /ios, android/);
  } finally {
    rmSync(sandbox.root, { recursive: true, force: true });
  }
});

test("ios surface reports missing xcodebuild as a blocker", async () => {
  const sandbox = makeSandbox(["xcrun"]);
  try {
    const { code, stdout } = await runSurfaces(sandbox, "ios");
    assert.equal(code, 1);
    assert.match(stdout, /OK\s+xcrun/);
    assert.match(stdout, /MISSING\s+xcodebuild/);
  } finally {
    rmSync(sandbox.root, { recursive: true, force: true });
  }
});

test("ios surface passes without xcodegen but flags it as optional", async () => {
  const sandbox = makeSandbox(["xcrun", "xcodebuild"]);
  try {
    const { code, stdout } = await runSurfaces(sandbox, "ios");
    assert.equal(code, 0);
    assert.match(stdout, /OPTIONAL\s+xcodegen/);
  } finally {
    rmSync(sandbox.root, { recursive: true, force: true });
  }
});

test("android surface passes with adb and a JDK 17+", async () => {
  const sandbox = makeSandbox(["adb"], { javaMajor: 17 });
  try {
    const { code, stdout } = await runSurfaces(sandbox, "android");
    assert.equal(code, 0);
    assert.match(stdout, /OK\s+java .*major 17/);
  } finally {
    rmSync(sandbox.root, { recursive: true, force: true });
  }
});

test("android surface fails on a pre-17 JDK", async () => {
  const sandbox = makeSandbox(["adb"], { javaMajor: 11 });
  try {
    const { code, stdout } = await runSurfaces(sandbox, "android");
    assert.equal(code, 1);
    assert.match(stdout, /MISSING\s+java\s+major >= 17 required, found 11/);
  } finally {
    rmSync(sandbox.root, { recursive: true, force: true });
  }
});

test("android surface resolves adb and the newest build-tools from ANDROID_HOME", async () => {
  // PATH has no adb/aapt2/apksigner: the resolver alone must find them, and
  // it must pick the newest build-tools (36.0.0 over 9.0.0).
  const sandbox = makeSandbox([], { javaMajor: 17, sdk: true });
  try {
    const { code, stdout } = await runSurfaces(sandbox, "android");
    assert.equal(code, 0);
    assert.match(stdout, /OK\s+adb\s+.*platform-tools\/adb/);
    assert.match(stdout, /OK\s+aapt2\s+.*build-tools\/36\.0\.0\/aapt2/);
    assert.match(stdout, /OK\s+apksigner\s+.*build-tools\/36\.0\.0\/apksigner/);
  } finally {
    rmSync(sandbox.root, { recursive: true, force: true });
  }
});

test("android surface fails when adb is absent", async () => {
  const sandbox = makeSandbox([], { javaMajor: 17 });
  try {
    const { code, stdout } = await runSurfaces(sandbox, "android");
    assert.equal(code, 1);
    assert.match(stdout, /MISSING\s+adb/);
  } finally {
    rmSync(sandbox.root, { recursive: true, force: true });
  }
});
