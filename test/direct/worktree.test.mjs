// direct/worktree 的回归用例（ADR 0043）：脚本随作业物料投递到执行节点，那里没有
// 本仓布局，也不能查能力注册表——一切路径按 --repo 参数解析。这里用真 git 仓跑最短
// 闭环：建工作区、列、按脏拒删、--force 删。
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

const script = join(dirname(fileURLToPath(import.meta.url)), "../../direct/worktree");

function git(repo, ...args) {
  return execFileSync("git", ["-C", repo, ...args], { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
}

// hasUpstream 判定分支是否被自动绑了上游：有的话 git push.default=upstream 会直写集成分支。
function hasUpstream(repo) {
  try {
    execFileSync("git", ["-C", repo, "rev-parse", "--abbrev-ref", "@{u}"], { stdio: "pipe" });
    return true;
  } catch {
    return false;
  }
}

function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), "worktree-script-"));
  const repo = join(root, "checkout");
  const bare = join(root, "origin.git");
  // 必须有真实的 origin 远端：git 的自动 upstream 推断只对 remote-tracking 引用生效，
  // 没有远端时 `@{u}` 恒为空，断言“没有 upstream”会变成同义反复。
  execFileSync("git", ["init", "-q", "--bare", bare], { stdio: ["ignore", "pipe", "pipe"] });
  execFileSync("git", ["init", "-q", repo], { stdio: ["ignore", "pipe", "pipe"] });
  git(repo, "symbolic-ref", "HEAD", "refs/heads/main");
  writeFileSync(join(repo, "a.txt"), "a\n");
  execFileSync("git", ["-C", repo, "add", "."], { stdio: "ignore" });
  execFileSync("git", ["-C", repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init"], { stdio: "ignore" });
  git(repo, "remote", "add", "origin", bare);
  execFileSync("git", ["-C", repo, "push", "-q", "origin", "main"], { stdio: ["ignore", "pipe", "pipe"] });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  return { root, repo, worktree: join(root, "checkout-fix-1") };
}

test("worktree add 在检出同级建工作区并从分支起头", (t) => {
  const { repo, worktree } = fixture(t);
  const out = execFileSync("sh", [script, "add", "--repo", repo, "--name", "fix-1", "--base", "main"], { encoding: "utf8" });
  assert.match(out, /工作区：/);
  assert.equal(git(worktree, "rev-parse", "--abbrev-ref", "HEAD").trim(), "feature/fix-1");
  // 工作区不在检出内部：否则 git 会把它当成未跟踪文件，原地写模式又禁止清理。
  assert.ok(!out.includes(join(repo, "fix-1")));
});

test("worktree 不给 --base 时从检出当前 HEAD 切", (t) => {
  const { repo, worktree } = fixture(t);
  execFileSync("sh", [script, "add", "--repo", repo, "--name", "fix-1"], { encoding: "utf8" });
  // 当前 HEAD 是 main 上的 init 提交：新分支必须指向同一个 commit。
  const head = git(repo, "rev-parse", "HEAD").trim();
  assert.equal(git(worktree, "rev-parse", "HEAD").trim(), head);
  // 特性分支不能自动跟踪集成分支：push.default=upstream 时裸 push 会直写 main。
  assert.equal(hasUpstream(worktree), false, "特性分支不能设 upstream");
});

test("worktree --base 切出的分支同样不设 upstream", (t) => {
  const { repo, worktree } = fixture(t);
  execFileSync("sh", [script, "add", "--repo", repo, "--name", "fix-1", "--base", "main"], { encoding: "utf8" });
  assert.equal(hasUpstream(worktree), false, "基于 origin/main 也不能设 upstream");
});

test("worktree remove 默认拒删脏工作区，--force 才删", (t) => {
  const { repo, worktree } = fixture(t);
  execFileSync("sh", [script, "add", "--repo", repo, "--name", "fix-1", "--base", "main"], { encoding: "utf8" });
  writeFileSync(join(worktree, "wip.txt"), "wip\n");
  assert.throws(() => execFileSync("sh", [script, "remove", "--repo", repo, "--name", "fix-1"], { stdio: "pipe" }), /工作区不干净/);
  execFileSync("sh", [script, "remove", "--repo", repo, "--name", "fix-1", "--force"], { stdio: "ignore" });
  const listed = git(repo, "worktree", "list", "--porcelain");
  assert.ok(!listed.includes("fix-1"));
});

test("worktree 一切路径按 --repo 解析：缺 --repo、相对路径、缺失基线直接拒绝", (t) => {
  const { repo } = fixture(t);
  assert.throws(() => execFileSync("sh", [script, "add", "--name", "x"], { stdio: "pipe" }), /--repo/);
  assert.throws(() => execFileSync("sh", [script, "list", "--repo", "relative/path"], { stdio: "pipe" }), /绝对路径/);
  assert.throws(
    () => execFileSync("sh", [script, "add", "--repo", repo, "--name", "fix-2", "--base", "no-such"], { stdio: "pipe" }),
    /基线不存在/,
  );
});
