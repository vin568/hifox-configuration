// Tests for super-agent/scripts/check-registry.mjs — the instance-side reconciliation
// the framework deliberately does not do (ADR 0002). A check that silently stops
// catching drift is worse than no check: these assertions use fixture trees so the
// rule (not the live repo) is what's under test.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import {
  checkLiveWith,
  checkOffline,
  collect,
  isOrgUUID,
  nameMatches,
  platformNameMatches,
  recordChecks,
  splitInstance,
} from "../../super-agent/scripts/check-registry.mjs";

function write(root, rel, body) {
  const p = join(root, rel);
  mkdirSync(dirname(p), { recursive: true });
  writeFileSync(p, body);
}

// fixture 造一个与真实仓同形的实例：一个配置集合、一个环境、一条指针。
function fixture() {
  const root = mkdtempSync(join(tmpdir(), "registry-check-"));
  write(root, "super-agent/capabilities/platforms/hifox/daizong.yaml",
    "key: daizong\nname: 戴宗\nspace: HF\norg: 86555f76-3f5b-4f7a-93c6-406074110a59\n");
  write(root, "config/hifox/agents/daizong/agent.yaml", "key: daizong\nname: 戴宗\n");
  write(root, "environments/prod/environment.yaml",
    "organization: rayfox\nspace: HF\nconfigSet: hifox\ncli:\n  app: hifox\n");
  write(root, "environments/prod/state.yaml",
    "version: 1\nagents:\n  daizong:\n    id: bd6bf6a6-5191-422d-acfd-5b7cbaa38900\n    deployedName: 戴宗\n");
  return root;
}

test("healthy fixture has no problems", () => {
  const root = fixture();
  t_cleanup(root);
  const problems = checkOffline(collect(root));
  assert.deepEqual(problems, []);
});

test("missing pointer for a declared config agent is drift", () => {
  const root = fixture();
  t_cleanup(root);
  rmSync(join(root, "super-agent/capabilities/platforms/hifox/daizong.yaml"));
  const problems = checkOffline(collect(root));
  assert.match(problems.join("\n"),
    /config\/hifox\/agents\/daizong：缺 super-agent\/capabilities\/platforms\/hifox\/daizong\.yaml/);
});

test("pointer without config truth is drift", () => {
  const root = fixture();
  t_cleanup(root);
  rmSync(join(root, "config/hifox/agents/daizong"), { recursive: true });
  const problems = checkOffline(collect(root));
  assert.match(problems.join("\n"), /config\/ 下任何配置集合里都找不到/);
});

test("pointer name must be the same identity as config", () => {
  const root = fixture();
  t_cleanup(root);
  write(root, "config/hifox/agents/daizong/agent.yaml", "key: daizong\nname: 张三\n");
  const problems = checkOffline(collect(root));
  assert.match(problems.join("\n"), /name 是 "戴宗"/);
});

// 英文后缀是合法声明名（巴德尔 → 巴德尔 Baldur），但前缀必须落在词边界上。
test("nameMatches accepts word-boundary suffixes only", () => {
  assert.equal(nameMatches("巴德尔", "巴德尔 Baldur"), true);
  assert.equal(nameMatches("戴宗", "戴宗 (staging)"), true);
  assert.equal(nameMatches("萧", "萧让"), false);
  assert.equal(nameMatches("戴宗", "戴宗明"), false);
  assert.equal(platformNameMatches("燕青", "燕青 - staging"), true);
  assert.equal(splitInstance("戴宗 (staging)")[1], "staging");
  assert.equal(splitInstance("燕青 - staging")[1], "staging");
});

test("org must be a full UUID", () => {
  assert.equal(isOrgUUID("86555f76-3f5b-4f7a-93c6-406074110a59"), true);
  assert.equal(isOrgUUID("rayfox"), false);
  const root = fixture();
  t_cleanup(root);
  write(root, "super-agent/capabilities/platforms/hifox/daizong.yaml", "key: daizong\nname: 戴宗\norg: rayfox\n");
  assert.match(checkOffline(collect(root)).join("\n"), /必须写平台组织完整 UUID/);
});

test("config set without an environment binding is drift", () => {
  const root = fixture();
  t_cleanup(root);
  rmSync(join(root, "environments/prod"), { recursive: true });
  write(root, "environments/empty/environment.yaml", "organization: rayfox\nspace: HF\n");
  assert.match(checkOffline(collect(root)).join("\n"), /没有环境声明它部署在哪个平台/);
});

test("pointer must have a deployment record", () => {
  const root = fixture();
  t_cleanup(root);
  write(root, "environments/prod/state.yaml", "version: 1\nagents:\n  linchong:\n    id: x\n    deployedName: 卢克\n");
  assert.match(checkOffline(collect(root)).join("\n"), /没有任何部署记录/);
});

// nameMatches 必须与框架的派活解析同一套规则：两处各写一份会让「对账说的同名」
// 与「派活解析到的 agent」给出两种结论。这张表与
// vendor/super-agent/internal/hifox/client_test.go 的 NameMatches 用例同源。
test("nameMatches mirrors the framework resolver", () => {
  // 同一份 fixture 也被 vendor/super-agent/internal/hifox/client_test.go 读取：
  // Go 的派活解析与这里的跨仓对账必须给出同一结论。
  const cases = JSON.parse(
    readFileSync(new URL("../../vendor/super-agent/testdata/name-matching.json", import.meta.url), "utf8"),
  );
  assert.ok(cases.length > 0);
  for (const c of cases) {
    assert.equal(platformNameMatches(c.declared, c.candidate), c.want, `${c.declared} vs ${c.candidate}`);
  }
});

// 线上对账：space 只在 environment 所属组织里找（identifier 在组织内唯一，
// 同名 Space 可以出现在多个组织；不按组织收窄会选中别人的 spaceId）。
test("checkLiveWith scopes space lookup to the environment organization", () => {
  const data = {
    platforms: [{ platform: "hifox", key: "daizong", name: "戴宗", space: "HF" }],
    environments: [
      {
        name: "prod",
        organization: "rayfox",
        space: "HF",
        configSet: "hifox",
        platform: "hifox",
        agents: { daizong: { id: "a1", deployedName: "戴宗" } },
      },
    ],
  };
  const io = {
    listOrgs: () => [
      { id: "org-1", slug: "rayfox" },
      { id: "org-2", slug: "other" },
    ],
    listSpaces: () => [
      { id: "space-ours", identifier: "HF", organizationId: "org-1" },
      { id: "space-theirs", identifier: "HF", organizationId: "org-2" },
    ],
    listAgents: () => [
      { id: "a1", name: "戴宗", spaceIds: ["space-ours"], allowTaskAssignment: true },
    ],
  };
  assert.deepEqual(checkLiveWith(data, io), []);

  // 反向：线上 agent 在别的组织的同名空间里 → 必须报「不含环境 space」。
  io.listAgents = () => [
    { id: "a1", name: "戴宗", spaceIds: ["space-theirs"], allowTaskAssignment: true },
  ];
  assert.match(checkLiveWith(data, io).join("\n"), /不含环境 space/);
});

// 线上对账的纯函数部分：真值不一致、身份不一致、组织不一致、space 不含、归档、不可派活。
test("recordChecks reports every live mismatch", () => {
  const pointers = new Map([["daizong", { name: "戴宗", org: "86555f76-3f5b-4f7a-93c6-406074110a59" }]]);
  const env = {
    name: "prod",
    orgID: "86555f76-3f5b-4f7a-93c6-406074110a59",
    spaceID: "space-1",
    agents: { daizong: { id: "a1", deployedName: "戴宗" } },
  };
  // 线上名字带实例后缀，state 记的是部署时名字：两者不同是正常的，不做相等断言。
  const ok = { id: "a1", name: "戴宗", spaceIds: ["space-1"], allowTaskAssignment: true };
  assert.deepEqual(recordChecks(env, [ok], pointers), []);

  const problems = recordChecks(
    env,
    [{ ...ok, name: "尤弥尔 Ymir", spaceIds: ["space-2"], archivedAt: "2026-01-01", allowTaskAssignment: false }],
    pointers,
  ).join("\n");
  assert.match(problems, /平台名字是 "尤弥尔 Ymir"/);
  assert.match(problems, /不是同一个身份/);
  assert.match(problems, /不含环境 space/);
  assert.match(problems, /已归档/);
  assert.match(problems, /不允许派任务/);

  assert.match(recordChecks(env, [], pointers).join("\n"), /平台查不到这个 id/);
});

function t_cleanup(root) {
  test.after(() => rmSync(root, { recursive: true, force: true }));
}

// 退出码是 CI 唯一能看见的东西：漂移打印得再清楚，退出 0 也等于没有检查。
// 这里跑真实 CLI（--no-framework，避免测试依赖 go run）。
test("CLI exits 1 on drift and 0 when clean", () => {
  const clean = fixture();
  t_cleanup(clean);
  const script = new URL("../../super-agent/scripts/check-registry.mjs", import.meta.url).pathname;
  const run = (root) =>
    spawnSync(process.execPath, [script, "--root", root, "--no-framework"], {
      encoding: "utf8",
    });

  const ok = run(clean);
  assert.equal(ok.status, 0, ok.stderr);

  rmSync(join(clean, "super-agent/capabilities/platforms/hifox/daizong.yaml"));
  const bad = run(clean);
  assert.equal(bad.status, 1);
  assert.match(bad.stderr, /缺 super-agent\/capabilities\/platforms\/hifox\/daizong\.yaml/);
});

// 缺 yq 是「工具没装」，不是「检查通过」：必须用另一个退出码把它区分出来。
test("CLI exits 2 when yq is unavailable", () => {
  const root = fixture();
  t_cleanup(root);
  const emptyPath = mkdtempSync(join(tmpdir(), "no-yq-"));
  const script = new URL("../../super-agent/scripts/check-registry.mjs", import.meta.url).pathname;
  const out = spawnSync(process.execPath, [script, "--root", root, "--no-framework"], {
    encoding: "utf8",
    env: { ...process.env, PATH: emptyPath },
  });
  rmSync(emptyPath, { recursive: true, force: true });
  assert.equal(out.status, 2, out.stderr);
  assert.match(out.stderr, /yq/);
});


// 指针写的组织必须与环境一致：写错会让派活落到另一个组织（那边既没有实例，
// 也不该由这份记录管），而 state 与线上名字可以完全正确，只有这一条看得出来。
test("recordChecks reports an organization mismatch", () => {
  const pointers = new Map([["daizong", { name: "戴宗", org: "11111111-1111-4111-8111-111111111111" }]]);
  const env = {
    name: "prod",
    orgID: "86555f76-3f5b-4f7a-93c6-406074110a59",
    spaceID: "space-1",
    agents: { daizong: { id: "a1", deployedName: "戴宗" } },
  };
  const live = { id: "a1", name: "戴宗", spaceIds: ["space-1"], allowTaskAssignment: true };
  assert.match(recordChecks(env, [live], pointers).join("\n"), /org 是/);
});
