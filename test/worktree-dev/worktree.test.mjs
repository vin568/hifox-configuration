// Tests for .agents/skills/worktree-dev/scripts/worktree.sh — the maintainer's only
// allowed worktree entry point. The failure this pins down was measured: `add` based
// the new branch on the *local* default branch, so a checkout whose local main had
// stopped advancing (0 ahead / 2 behind) produced worktrees missing five commits of
// already-merged work — and merging such a branch reverts the missing commits.
// A worktree that quietly starts from a stale base is worse than none, so the base
// has to be origin, and the test builds a real bare remote to prove it.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const SCRIPT = fileURLToPath(
  new URL("../../.agents/skills/worktree-dev/scripts/worktree.sh", import.meta.url),
);

const git = (cwd, ...args) =>
  execFileSync("git", [
    "-c", "user.email=test@example.invalid",
    "-c", "user.name=Test",
    // 本地 bare 远端是 file:// 传输，git 默认禁止子模块用它克隆；用例自己的
    // `submodule add` 与脚本的 `submodule update` 都要走这条。生产路径是 https/ssh。
    "-c", "protocol.file.allow=always",
    ...args,
  ], {
    cwd,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();

// repo 造一个「远端 + 本地检出」的对：本地已在 main 上，远端可独立前进。
// 用本地 bare 远端，不依赖本仓真实的 origin，也不碰网络。
function repo(t) {
  const root = mkdtempSync(join(tmpdir(), "worktree-test-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const remote = join(root, "remote.git");
  git(root, "init", "--bare", "-q", remote);
  git(remote, "symbolic-ref", "HEAD", "refs/heads/main");

  const seed = join(root, "seed");
  git(root, "init", "-q", "-b", "main", seed);
  writeFileSync(join(seed, "a.txt"), "1\n");
  git(seed, "add", ".");
  git(seed, "commit", "-qm", "one");
  git(seed, "remote", "add", "origin", remote);
  git(seed, "push", "-q", "-u", "origin", "main");

  const work = join(root, "work");
  git(root, "clone", "-q", remote, work);
  // 本地 bare 远端是 file:// 传输，git 默认禁止子模块用它克隆；写进 repo 配置
  // （worktree 共享同一份 config）才覆盖脚本内部的 submodule update。生产走 https/ssh。
  git(work, "config", "protocol.file.allow", "always");
  return { root, remote, work, seed };
}

// advanceRemote 让远端 main 前进一格：从种子仓再提交并 push。
// 随后在检出里 fetch 一次——这正是「origin/main 已是最新、本地 main 还停在旧 commit」
// 的形状（主检出不 checkout main 时就是这样），而脚本当前取的是本地 main。
function advanceRemote({ seed, work }, message) {
  writeFileSync(join(seed, "b.txt"), `${message}\n`);
  git(seed, "add", ".");
  git(seed, "commit", "-qm", message);
  git(seed, "push", "-q", "origin", "main");
  git(work, "fetch", "-q", "origin");
  return git(seed, "rev-parse", "HEAD");
}

// add 在会话里跑脚本：file:// 传输的允许项走环境变量，git 会把它传给
// 子模块 update 内部 fork 的 clone —— 仓库配置在 worktree 里不够（实测仍被拒）。
const add = (work, name) =>
  execFileSync("bash", [SCRIPT, "add", name], {
    cwd: work,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
    env: { ...process.env, GIT_ALLOW_PROTOCOL: "file:https:ssh:git" },
  });

// 用例的红线：新 worktree 的 HEAD 必须等于远端默认分支，而不是本地那个停住的 main。
test("add bases the new branch on origin, not a stale local default branch", (t) => {
  const r = repo(t);
  const head = advanceRemote(r, "two");
  // 本地 main 落后一格（正是出事时的形状：0 ahead / 1 behind）。
  assert.equal(git(r.work, "rev-list", "--count", "main..origin/main"), "1");
  add(r.work, "feat");
  const wt = join(r.root, "work-feat");
  assert.equal(git(wt, "rev-parse", "HEAD"), head);
  assert.equal(git(wt, "rev-parse", "--abbrev-ref", "HEAD"), "feature/feat");
});

// 子模块必须按集成分支的远端 tip 切分支：本地 gitlink 记的是子模块 HEAD，
// 不 fetch 就会从旧 tip 开特性分支（根仓 gitlink 落后的那次实测）。
test("add fetches the re-declared integration branch before branching in submodules", (t) => {
  const r = repo(t);
  const sub = join(r.root, "sub-seed");
  git(r.root, "init", "-q", "-b", "dev", sub);
  writeFileSync(join(sub, "s.txt"), "1\n");
  git(sub, "add", ".");
  git(sub, "commit", "-qm", "sub one");
  const subRemote = join(r.root, "sub-remote.git");
  git(r.root, "init", "--bare", "-q", subRemote);
  git(subRemote, "symbolic-ref", "HEAD", "refs/heads/dev");
  git(sub, "remote", "add", "origin", subRemote);
  git(sub, "push", "-q", "-u", "origin", "dev");

  writeFileSync(join(r.work, ".gitmodules"),
    `[submodule "vendor/demo"]\n\tpath = vendor/demo\n\turl = ${subRemote}\n\tbranch = dev\n`);
  git(r.work, "submodule", "add", subRemote, "vendor/demo");
  git(r.work, "add", ".gitmodules", "vendor/demo");
  git(r.work, "commit", "-qm", "add submodule");
  git(r.work, "push", "-q", "origin", "main");

  // 远端 dev 在检出之后又前进一格：worktree 必须拿到这一格。
  writeFileSync(join(sub, "s2.txt"), "2\n");
  git(sub, "add", ".");
  git(sub, "commit", "-qm", "sub two");
  git(sub, "push", "-q", "origin", "dev");
  const devTip = git(sub, "rev-parse", "HEAD");

  add(r.work, "feat");
  const wtSub = join(r.root, "work-feat", "vendor/demo");
  assert.equal(git(wtSub, "rev-parse", "HEAD"), devTip, "子模块特性分支必须基于 origin/dev 的最新 tip");
  assert.equal(git(wtSub, "rev-parse", "--abbrev-ref", "HEAD"), "feature/feat");
});

// 已有分支照旧挂载：不该被改写成基于远端的新分支（分支身份是 push 出去的物体）。
test("add mounts an existing branch as-is", (t) => {
  const r = repo(t);
  git(r.work, "branch", "feature/feat", "main");
  add(r.work, "feat");
  assert.equal(git(join(r.root, "work-feat"), "rev-parse", "HEAD"), git(r.work, "rev-parse", "main"));
});

// 脚本只在主检出运行：linked worktree 里跑必须报错，而不是在那里再建一层。
test("add refuses to run inside a linked worktree", (t) => {
  const r = repo(t);
  add(r.work, "feat");
  const wt = join(r.root, "work-feat");
  assert.throws(() => add(wt, "nested"), (err) => {
    assert.match(String(err.stderr), /请在主检出运行/);
    return true;
  });
});

// 远端默认分支名不是 main 时（例如 master）也必须跟远端走，不能写死 main。
test("add follows the remote default branch name", (t) => {
  const root = mkdtempSync(join(tmpdir(), "worktree-test-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const remote = join(root, "remote.git");
  git(root, "init", "--bare", "-q", remote);
  git(remote, "symbolic-ref", "HEAD", "refs/heads/trunk");
  const seed = join(root, "seed");
  git(root, "init", "-q", "-b", "trunk", seed);
  writeFileSync(join(seed, "a.txt"), "1\n");
  git(seed, "add", ".");
  git(seed, "commit", "-qm", "one");
  git(seed, "remote", "add", "origin", remote);
  git(seed, "push", "-q", "-u", "origin", "trunk");
  const work = join(root, "work");
  git(root, "clone", "-q", remote, work);

  writeFileSync(join(seed, "b.txt"), "2\n");
  git(seed, "add", ".");
  git(seed, "commit", "-qm", "two");
  git(seed, "push", "-q", "origin", "trunk");

  add(work, "feat");
  assert.equal(git(join(root, "work-feat"), "rev-parse", "HEAD"), git(seed, "rev-parse", "HEAD"));
  // 目录名里没有 trunk/master 之类平台残留。
  assert.match(readFileSync(SCRIPT, "utf8"), /refs\/remotes\/origin\/HEAD/);
});
