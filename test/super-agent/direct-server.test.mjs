// 直连 server 脚本的边界（ADR 0019 第 2 级 + ADR 0036）：只有一个动词参数、
// 固定本实例配置与绝对稳定入口，非法动词/额外参数绝不触达二进制。
//
// 用「把真实脚本复制进 fixture 实例根」的方式测：路径由脚本自身位置推导，复制后
// 测到的仍是仓里那份脚本的行为，不依赖本机是否真的构建过 bin。
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import test from "node:test";

const HERE = dirname(fileURLToPath(import.meta.url));
const ROOT = join(HERE, "..", "..");
const REAL_SCRIPT = join(ROOT, "super-agent", "direct", "server");

// fixture 造一个最小实例根：direct/server（真实脚本的副本）+ bin/superctl（记录器）。
function fixture({ withBin = true } = {}) {
  const root = mkdtempSync(join(tmpdir(), "direct-server-"));
  const direct = join(root, "direct");
  mkdirSync(direct, { recursive: true });
  const script = join(direct, "server");
  writeFileSync(script, readFileSync(REAL_SCRIPT));
  chmodSync(script, 0o755);
  const record = join(root, "record.txt");
  if (withBin) {
    const binDir = join(root, "bin");
    mkdirSync(binDir, { recursive: true });
    const bin = join(binDir, "superctl");
    writeFileSync(
      bin,
      `#!/bin/sh\nprintf '%s\\n' "$@" > "${record}"\nprintf '%s\\n' "$SUPERAGENT_CONFIG" >> "${record}"\n`,
    );
    chmodSync(bin, 0o755);
  }
  return { root, script, record };
}

function run(fx, args, env = {}) {
  const from = mkdtempSync(join(tmpdir(), "direct-server-cwd-"));
  try {
    return spawnSync(fx.script, args, {
      cwd: from, // 故意不在实例根：脚本不能依赖调用者 cwd
      env: { HOME: fx.root, ...env },
      encoding: "utf8",
    });
  } finally {
    rmSync(from, { recursive: true, force: true });
  }
}

function recorded(fx) {
  return readFileSync(fx.record, "utf8").trimEnd().split("\n");
}

test("whitelisted verbs reach the stable bin with the fixed instance config", () => {
  for (const verb of ["status", "start", "stop", "restart"]) {
    const fx = fixture();
    // 调用者的环境变量不得改变实例指向：白名单入口只服务本实例配置。
    const res = run(fx, [verb], { SUPERAGENT_CONFIG: join(fx.root, "elsewhere", "superctl.toml") });
    assert.equal(res.status, 0, `${verb}: ${res.stderr}`);
    const lines = recorded(fx);
    const home = join(fx.root, ".config", "super-agent", "superctl.toml");
    assert.deepEqual(lines.slice(0, -1), ["server", verb, "--config", home, "--actor", "master"]);
    assert.equal(lines.at(-1), home, "SUPERAGENT_CONFIG 必须是固定实例路径，不能跟随调用者");
    rmSync(fx.root, { recursive: true, force: true });
  }
});

test("default config path is the documented home file", () => {
  const fx = fixture();
  const res = run(fx, ["status"]);
  assert.equal(res.status, 0, res.stderr);
  assert.equal(recorded(fx).at(-1), join(fx.root, ".config", "super-agent", "superctl.toml"));
  rmSync(fx.root, { recursive: true, force: true });
});

test("non-whitelisted verbs and extra args are rejected before the binary runs", () => {
  const cases = [
    ["logs"], ["build"], ["install"], [], ["status extra"],
    ["status", "extra"], ["--json"], ["stop", "--force"],
  ];
  for (const args of cases) {
    const fx = fixture();
    const res = run(fx, args);
    assert.equal(res.status, 2, `${JSON.stringify(args)}: 应拒绝（stderr=${res.stderr}）`);
    assert.match(res.stderr, /只允许 status\|start\|stop\|restart|只接受一个动词参数/);
    assert.equal(existsSync(fx.record), false, `${JSON.stringify(args)}: 非法调用绝不能触达二进制`);
    rmSync(fx.root, { recursive: true, force: true });
  }
});

test("missing stable entry tells the operator to build", () => {
  const fx = fixture({ withBin: false });
  const res = run(fx, ["status"]);
  assert.equal(res.status, 2);
  assert.match(res.stderr, /super-agent\/build/);
  rmSync(fx.root, { recursive: true, force: true });
});

test("the real script is executable and registered", () => {
  execFileSync("test", ["-x", REAL_SCRIPT]);
  const registration = join(ROOT, "super-agent", "capabilities", "scripts", "server.yaml");
  assert.ok(existsSync(registration), "server.yaml 必须存在（capabilities 两向校验）");
  const body = readFileSync(registration, "utf8");
  assert.match(body, /path:\s*direct\/server/);
});
