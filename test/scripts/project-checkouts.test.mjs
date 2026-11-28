// 项目 / 检出（ADR 0043）的检查器用例：静态引用闭合与 live 探针。
// 生产侧 checkProjects 只回答「声明了能不能派出去」；机器真值（路径存在、remote 命中）
// 属 check:live，这里用测试缝回答，不碰真机。
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";
import { checkCheckoutsLive, checkProjects } from "../../scripts/check-registry.mjs";

const projects = [{ key: "xwing", repo: "https://example.invalid/xwing", aliases: [], base: "develop" }];

// 探针输出与 Go 侧 internal/gitprobe 同格式：exists/worktree/remote/branch + `--` + porcelain。
const probe = ({ exists = 1, worktree = 1, remote = "", branch = "main", dirty = [] }) =>
  `exists=${exists}\nworktree=${worktree}\nremote=${remote}\nbranch=${branch}\n--\n${dirty.join("\n")}\n`;

test("没有检出的项目只在无人声明时报「派不出去」", () => {
  assert.match(checkProjects([{ key: "ghost", repo: "", base: "" }], [], []).join("\n"), /声明了但派不出去/);
  // 有机器检出或有能力声明就不算：两条各自都能让它派得出去。
  assert.deepEqual(checkProjects([{ key: "ghost", repo: "", base: "" }], [{ node: "n", project: "ghost", path: "/srv/g" }], []), []);
  assert.deepEqual(checkProjects([{ key: "ghost", repo: "", base: "" }], [], [{ key: "worker", projects: ["ghost"] }]), []);
});

test("检出表指向未声明的项目要报引用错误", () => {
  assert.match(
    checkProjects(projects, [{ node: "n", project: "nope", path: "/srv/n" }], []).join("\n"),
    /项目 "nope" 没有声明/,
  );
});

test("live 探针：remote 失配 fail；脏只提示不拦；本机节点直跑本地探针", () => {
  const nodes = [{ key: "self", ssh: "self", machine: "self", identity: "SELF" }];
  const opts = { identity: "SELF", runLocal: (_, path) => probe({ remote: "https://example.invalid/other" }) };
  const bad = checkCheckoutsLive(projects, [{ node: "self", project: "xwing", path: "/srv/x" }], nodes, opts);
  assert.match(bad.join("\n"), /都不匹配/);

  const okOpts = {
    identity: "SELF",
    runLocal: (_, path) => probe({ remote: "https://example.invalid/xwing.git", dirty: [" M a.go"] }),
  };
  assert.deepEqual(checkCheckoutsLive(projects, [{ node: "self", project: "xwing", path: "/srv/x" }], nodes, okOpts), []);
});

test("live 探针：路径缺失报「这台机器还没拉这个仓库」", () => {
  const nodes = [{ key: "self", ssh: "self", machine: "self", identity: "SELF" }];
  const problems = checkCheckoutsLive(projects, [{ node: "self", project: "xwing", path: "/srv/x" }], nodes, {
    identity: "SELF",
    runLocal: () => probe({ exists: 0 }),
  });
  assert.match(problems.join("\n"), /还没拉这个仓库/);
});

test("live 探针：节点不可达跳过，未声明的节点报准确错误而不是 TypeError", () => {
  const nodes = [{ key: "box", ssh: "box", machine: "box" }];
  const unreachable = { status: 255, stderr: "ssh: connect to host box port 22: Undefined error" };
  const skipped = checkCheckoutsLive(projects, [{ node: "box", project: "xwing", path: "/srv/x" }], nodes, {
    identity: "SELF",
    runRemote: () => {
      throw unreachable;
    },
  });
  assert.deepEqual(skipped, []);

  const undeclared = checkCheckoutsLive(projects, [{ node: "ghost", project: "xwing", path: "/srv/x" }], nodes, {
    identity: "SELF",
  });
  assert.match(undeclared.join("\n"), /没有声明/);
});

// main() 的接线只有跑真 CLI 才测得出来：两个导出函数各自有单测，但把调用整体删掉
// 仍然全绿（检查项形同不存在）。
test("CLI 退出码：项目没有检出也没有能力声明时 exit 1", () => {
  const script = join(dirname(fileURLToPath(import.meta.url)), "../../scripts/check-registry.mjs");
  const root = mkdtempSync(join(tmpdir(), "project-check-"));
  test.after(() => rmSync(root, { recursive: true, force: true }));
  const write = (rel, body) => {
    const p = join(root, rel);
    mkdirSync(dirname(p), { recursive: true });
    writeFileSync(p, body);
  };
  write("capabilities/projects/ghost.yaml", "key: ghost\nname: ghost\n");
  // 没有任何 checkout、也没有 agents/<key>/projects.yaml 引用它。
  // 布局由 CLI 解析（ADR 0036）：夹具仓没有 ./x-deploy / vendor，测试缝给出 CLI。
  const out = spawnSync(process.execPath, [script, "--root", root, "--no-framework"], {
    encoding: "utf8",
    env: {
      ...process.env,
      X_DEPLOY: new URL("../../vendor/x-deploy/bin/x-deploy.mjs", import.meta.url).pathname,
    },
  });
  assert.equal(out.status, 1, out.stdout + out.stderr);
  assert.match(out.stderr, /声明了但派不出去/);
});
