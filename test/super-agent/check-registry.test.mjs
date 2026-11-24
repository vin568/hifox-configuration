// Tests for super-agent/scripts/check-registry.mjs — the instance-side reconciliation
// the framework deliberately does not do (ADR 0002). A check that silently stops
// catching drift is worse than no check: these assertions use fixture trees so the
// rule (not the live repo) is what's under test.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import {
  checkCapabilityDocs,
  checkLiveWith,
  checkNodes,
  checkNodesLive,
  checkOffline,
  collect,
  inflightDiff,
  isOrgUUID,
  loadInflightTaskIDs,
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
  write(root, "super-agent/capabilities/platforms/hifox/defaults.yaml",
    "org: 86555f76-3f5b-4f7a-93c6-406074110a59\nspace: HF\n");
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

test("node requires a saved herdr machine profile", () => {
  const root = fixture();
  t_cleanup(root);
  write(root, "super-agent/capabilities/nodes/workbox.yaml",
    "key: workbox\nssh: workbox\nmachine: workbox\nsession: super-agent\ninstance_root: /srv/inst\nstate_root: /srv/state\nwork_dir: /srv/repo\n");
  const data = collect(root);
  assert.equal(data.nodes.length, 1);
  const profile = { id: "0123456789abcdef0123456789abcdef", label: "workbox", target: "workbox", session: "super-agent" };
  assert.deepEqual(checkNodes(data.nodes, [profile]), []);
  assert.deepEqual(checkNodes(data.nodes, { machines: [profile] }), []);
  const problems = checkNodes(data.nodes, []);
  assert.match(problems.join("\n"), /machine list 里没有 profile/);
});

test("self node skips the saved-machine profile check", () => {
  const root = fixture();
  t_cleanup(root);
  write(root, "super-agent/capabilities/nodes/self.yaml",
    "key: self\nssh: self\nmachine: self\nsession: super-agent\nidentity: LOCAL-ID\ninstance_root: /srv/inst\nstate_root: /srv/state\nwork_dir: /srv/repo\n");
  const data = collect(root);
  // 本机节点用本地执行面，不需要 herdr machine add 自己（ADR 0042）。
  assert.deepEqual(checkNodes(data.nodes, [], "LOCAL-ID"), []);
  assert.deepEqual(checkNodesLive(data.nodes, () => "not-json", "LOCAL-ID"), []);
  assert.match(checkNodes(data.nodes, [], "OTHER-ID").join("\n"), /没有 profile/);
});

test("a label match from another profile is not a saved machine", () => {
  const root = fixture();
  t_cleanup(root);
  write(root, "super-agent/capabilities/nodes/work.yaml",
    "key: work\nssh: work\nmachine: work\nsession: super-agent\ninstance_root: /srv/inst\nstate_root: /srv/state\nwork_dir: /srv/repo\n");
  const data = collect(root);
  // 子串命中（work ⊂ workbox）不能当成 profile 存在。
  const problems = checkNodes(data.nodes, [{ label: "workbox", target: "workbox", session: "super-agent" }]);
  assert.match(problems.join("\n"), /没有 profile "work"/);
});

test("node profile target and session must match the declaration", () => {
  const root = fixture();
  t_cleanup(root);
  write(root, "super-agent/capabilities/nodes/workbox.yaml",
    "key: workbox\nssh: workbox\nmachine: workbox\nsession: super-agent\ninstance_root: /srv/inst\nstate_root: /srv/state\nwork_dir: /srv/repo\n");
  const data = collect(root);
  const problems = checkNodes(data.nodes, [
    { id: "workbox", target: "other-host", session: "other-session" },
  ]).join("\n");
  assert.match(problems, /SSH target 是 "other-host"/);
  assert.match(problems, /session 是 "other-session"/);
});

test("checkNodesLive fails closed on unreachable profiles", () => {
  const nodes = [{ key: "workbox", machine: "workbox" }];
  assert.deepEqual(checkNodesLive(nodes, () => JSON.stringify([{ reachable: true }])), []);
  assert.match(checkNodesLive(nodes, () => JSON.stringify([{ reachable: false }])).join("\n"), /不可达/);
  assert.deepEqual(checkNodesLive(nodes, () => JSON.stringify([
    { label: "other", reachable: false }, { label: "workbox", reachable: true },
  ])), []);
  assert.match(checkNodesLive(nodes, () => JSON.stringify([
    { label: "other", reachable: true }, { label: "workbox", reachable: false },
  ])).join("\n"), /不可达/);
  assert.match(checkNodesLive(nodes, () => "[]").join("\n"), /没有返回该 profile/);
  assert.match(checkNodesLive(nodes, () => "not-json").join("\n"), /失败/);
});

test("defaults org must match every pointer org", () => {
  const root = fixture();
  t_cleanup(root);
  write(root, "super-agent/capabilities/platforms/hifox/defaults.yaml",
    "org: e6bd5e4c-f6eb-4772-ba43-24702e4c8563\nspace: HF\n");
  const problems = checkOffline(collect(root));
  assert.match(problems.join("\n"), /defaults\.yaml：org 与 daizong\.yaml 不一致/);
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

// 有节点声明但机器没装 herdr（CI/新检出）：声明照查，profile 对账跳过并提示；
// 不能让「没装探测工具」变成一条假的漂移。
test("CLI skips node profile check when herdr is absent", () => {
  const root = fixture();
  t_cleanup(root);
  write(root, "super-agent/capabilities/nodes/workbox.yaml",
    "key: workbox\nssh: workbox\nmachine: workbox\nsession: super-agent\ninstance_root: /srv/inst\nstate_root: /srv/state\nwork_dir: /srv/repo\n");
  const yq = spawnSync("which", ["yq"], { encoding: "utf8" }).stdout.trim();
  assert.ok(yq, "需要 yq 才能跑实例自检用例");
  const bin = mkdtempSync(join(tmpdir(), "only-yq-"));
  symlinkSync(yq, join(bin, "yq"));
  const script = new URL("../../super-agent/scripts/check-registry.mjs", import.meta.url).pathname;
  const out = spawnSync(process.execPath, [script, "--root", root, "--no-framework"], {
    encoding: "utf8",
    env: { ...process.env, PATH: bin },
  });
  rmSync(bin, { recursive: true, force: true });
  assert.equal(out.status, 0, out.stderr);
  assert.match(out.stderr, /未安装 herdr/);
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

// ---------- 能力集文本不变量（ADR 0037） ----------

// docsFixture 造一棵本地能力目录：一个主控、一个作业 agent、一个共享协议技能。
function docsFixture() {
  const root = mkdtempSync(join(tmpdir(), "cap-docs-"));
  write(root, "super-agent/capabilities/agents/master/capability.yaml",
    "key: master\nname: 主控\nrole: master\nworkspace: master\nkind: omp\n");
  write(root, "super-agent/capabilities/agents/master/instructions.md",
    "# 主控\n\n按 [[skill:proto]] 执行。\n");
  write(root, "super-agent/capabilities/agents/master/skills.yaml", "- proto\n");
  write(root, "super-agent/capabilities/agents/worker/capability.yaml",
    "key: worker\nname: 工人\nrole: job\nworkspace: jobs\nkind: omp\n");
  write(root, "super-agent/capabilities/agents/worker/instructions.md", "# 工人\n");
  write(root, "super-agent/capabilities/skills/proto/SKILL.md",
    "---\nname: proto\ndescription: 协议\n---\n\n# 协议\n\n只放接口字段与取值。\n");
  return root;
}

test("capability docs: declared shared skill reference passes", () => {
  const root = docsFixture();
  t_cleanup(root);
  assert.deepEqual(checkCapabilityDocs(root), []);
});

test("capability docs: reference to an unmounted skill is drift", () => {
  const root = docsFixture();
  t_cleanup(root);
  write(root, "super-agent/capabilities/agents/worker/instructions.md", "# 工人\n\n按 [[skill:proto]] 执行。\n");
  assert.match(checkCapabilityDocs(root).join("\n"), /没有挂载给 worker/);
  // 声明后即通过：挂载清单是 skills.yaml 的唯一职责。
  write(root, "super-agent/capabilities/agents/worker/skills.yaml", "- proto\n");
  assert.deepEqual(checkCapabilityDocs(root), []);
});

test("capability docs: reference to a missing skill is drift", () => {
  const root = docsFixture();
  t_cleanup(root);
  write(root, "super-agent/capabilities/agents/master/instructions.md", "# 主控\n\n按 [[skill:no-such]] 执行。\n");
  assert.match(checkCapabilityDocs(root).join("\n"), /引用的技能 "no-such" 不存在/);
});

test("capability docs: own skill can be referenced without declaration", () => {
  const root = docsFixture();
  t_cleanup(root);
  write(root, "super-agent/capabilities/agents/worker/skills/research/SKILL.md",
    "---\nname: research\ndescription: 调研\n---\n\n# 调研\n\n只读方法。\n");
  write(root, "super-agent/capabilities/agents/worker/instructions.md", "# 工人\n\n按 [[skill:research]] 执行。\n");
  assert.deepEqual(checkCapabilityDocs(root), []);
});

test("capability docs: duplicate body lines across skills are drift", () => {
  const root = docsFixture();
  t_cleanup(root);
  const line = "同一段纪律不应该在两个技能里各写一份，这会让改名和维护都漂移。";
  write(root, "super-agent/capabilities/agents/worker/skills/alpha/SKILL.md",
    `---\nname: alpha\ndescription: a\n---\n\n# alpha\n\n${line}\n`);
  write(root, "super-agent/capabilities/agents/worker/skills/beta/SKILL.md",
    `---\nname: beta\ndescription: b\n---\n\n# beta\n\n${line}\n`);
  assert.match(checkCapabilityDocs(root).join("\n"), /正文重复/);
});

// 机器路径不是实例知识：写死在手册/技能里，换机、换检出即失效；正确形态是从首条
// 注入的「直连脚本目录」拼绝对路径。省略写法（`/Users/...`）是反例说明，不该误报。
test("capability docs: hardcoded machine path is drift", () => {
  const root = docsFixture();
  t_cleanup(root);
  write(root, "super-agent/capabilities/agents/master/instructions.md",
    "# 主控\n\n```bash\n/Users/vin/code/repo/super-agent/direct/server status\n```\n");
  assert.match(checkCapabilityDocs(root).join("\n"), /写死了本机绝对路径/);

  write(root, "super-agent/capabilities/agents/master/instructions.md",
    "# 主控\n\n任务正文里不贴 `/Users/...` 这类机器路径。\n");
  assert.deepEqual(checkCapabilityDocs(root), []);
});

test("capability docs: headings and code fences are not duplicate candidates", () => {
  const root = docsFixture();
  t_cleanup(root);
  const body = "这是一行放在代码块里的长命令模板，重复出现是正常的，不该被当成纪律复制。";
  write(root, "super-agent/capabilities/agents/worker/skills/alpha/SKILL.md",
    `---\nname: alpha\ndescription: a\n---\n\n# 同样的标题\n\n\`\`\`bash\n${body}\n\`\`\`\n`);
  write(root, "super-agent/capabilities/agents/worker/skills/beta/SKILL.md",
    `---\nname: beta\ndescription: b\n---\n\n# 同样的标题\n\n\`\`\`bash\n${body}\n\`\`\`\n`);
  assert.deepEqual(checkCapabilityDocs(root), []);
});

// 手册也必须参与重复检查：只查技能时实测漏过一次——主控手册写类型三值、协议技能写
// 四值，两边都没报错，漂移活了一整轮。这条钉住「手册与技能之间同样不得复制」。
test("capability docs: manual restating a skill line is drift", () => {
  const root = docsFixture();
  t_cleanup(root);
  const line = "类型闭集是 Bug、需求、任务、未指定四个值，不要自己按标题猜。";
  write(root, "super-agent/capabilities/skills/proto/SKILL.md",
    `---\nname: proto\ndescription: 协议\n---\n\n# 协议\n\n${line}\n`);
  assert.deepEqual(checkCapabilityDocs(root), [], "技能里的那一行本身不该报");
  write(root, "super-agent/capabilities/agents/worker/instructions.md", `# 工人\n\n${line}\n`);
  assert.match(checkCapabilityDocs(root).join("\n"), /正文重复/);
});

// 两份手册之间复制同一段纪律，同样是漂移：这是重构前 checkpoint 段在石秀/朱贵
// 手册里逐字重复的那个形态。
test("capability docs: duplicate lines across two manuals are drift", () => {
  const root = docsFixture();
  t_cleanup(root);
  const line = "收到 checkpoint 指令时先把进度写进进度文件，再结束回合，不要继续任务。";
  write(root, "super-agent/capabilities/agents/master/instructions.md", `# 主控\n\n${line}\n`);
  write(root, "super-agent/capabilities/agents/worker/instructions.md", `# 工人\n\n${line}\n`);
  assert.match(checkCapabilityDocs(root).join("\n"), /正文重复/);
});

// 闭集声明只许有一个宿主。逐字重复门禁拦不住改写过的漂移：实测主控手册写
// 「Bug / 需求 / 未指定」、协议技能写四值，两边都没报错。
test("capability docs: same closed set declared twice is drift", () => {
  const root = docsFixture();
  t_cleanup(root);
  write(root, "super-agent/capabilities/skills/proto/SKILL.md",
    "---\nname: proto\ndescription: 协议\n---\n\n# 协议\n\n类型：Bug | 需求 | 任务 | 未指定\n");
  write(root, "super-agent/capabilities/agents/worker/instructions.md", "# 工人\n\n类型：Bug | 需求 | 未指定\n");
  assert.match(checkCapabilityDocs(root).join("\n"), /字段「类型」的闭集声明出现两次/);
});

// ---------- 在途差集：平台已指派、哨兵却看不见 ----------

// 平台侧 delegateStatus=assigned 且未完结的单，若不在在途清单里，哨兵看不见、停滞检测
// 永远不报。实测 HF-16321/16322/16324/16325 四张单这样静默了十小时——所以这条规则
// 的存在价值就是「一批单同时漏登时出错」，不是逐张盯。

const OUR_AGENT = "1bb5f895-01c8-4915-a495-4ad6c6368523";
const OTHER_AGENT = "99999999-9999-4999-8999-999999999999";

function task(id, { category = "started", delegateStatus = "assigned", delegate = OUR_AGENT } = {}) {
  return {
    identifier: id,
    status: { category },
    delegateStatus,
    delegate: delegate ? { type: "agent", id: delegate } : null,
  };
}

test("inflight diff: empty diff passes", () => {
  assert.deepEqual(inflightDiff({
    tasks: [task("HF-1"), task("HF-2")],
    inflight: new Set(["HF-1", "HF-2"]),
    untracked: [],
    deployedAgentIDs: [OUR_AGENT],
  }), []);
});

test("inflight diff: a delegated open task missing from the list is drift", () => {
  const problems = inflightDiff({
    tasks: [task("HF-1"), task("HF-16321"), task("HF-16322"), task("HF-16324"), task("HF-16325")],
    inflight: new Set(["HF-1"]),
    untracked: [],
    deployedAgentIDs: [OUR_AGENT],
  }).join("\n");
  // 任务号必须逐个列出：只说「有漏登」等于让人自己去翻平台。
  for (const id of ["HF-16321", "HF-16322", "HF-16324", "HF-16325"]) assert.match(problems, new RegExp(id));
  assert.doesNotMatch(problems, /HF-1[、—]/);
});

// 豁免表是「有意不跟踪」的唯一入口：登记后该轮不再报错，其余漏登照样报。
test("inflight diff: explicitly untracked tasks are not drift", () => {
  assert.deepEqual(inflightDiff({
    tasks: [task("HF-1")],
    inflight: new Set(),
    untracked: ["HF-1"],
    deployedAgentIDs: [OUR_AGENT],
  }), []);

  const problems = inflightDiff({
    tasks: [task("HF-1"), task("HF-2")],
    inflight: new Set(),
    untracked: ["HF-1"],
    deployedAgentIDs: [OUR_AGENT],
  }).join("\n");
  assert.match(problems, /HF-2/);
  assert.doesNotMatch(problems, /HF-1[、—]/);
});

// 终态与搁置状态不进差集：completed / canceled 是收口，duplicate / backlog 是用户
// 有意搁置（与 sentinel 的 parkedTask 同口径）。把这些报成漏登会让规则天天告警。
test("inflight diff: terminal and parked categories are never drift", () => {
  assert.deepEqual(inflightDiff({
    tasks: [
      task("HF-1", { category: "completed" }),
      task("HF-2", { category: "canceled" }),
      task("HF-3", { category: "duplicate" }),
      task("HF-4", { category: "backlog" }),
    ],
    inflight: new Set(),
    untracked: [],
    deployedAgentIDs: [OUR_AGENT],
  }), []);
});

// 没派出去的单（delegateStatus 不是 assigned）与别家 agent 的单不属于哨兵的视野：
// 组织里有几百张，全报只会把这条规则淹成噪声。
test("inflight diff: only our delegated tasks are in scope", () => {
  assert.deepEqual(inflightDiff({
    tasks: [
      task("HF-1", { delegateStatus: "needs_reassignment" }),
      task("HF-2", { delegateStatus: null }),
      task("HF-3", { delegate: OTHER_AGENT }),
      task("HF-4", { delegate: null }),
    ],
    inflight: new Set(),
    untracked: [],
    deployedAgentIDs: [OUR_AGENT],
  }), []);
});

// 读不到在途清单与「清单为空」必须分开：说成漏登会让人去追几十张本来就在跟踪的单。
test("inflight diff: unreadable inflight list says so instead of reporting every task", () => {
  const problems = inflightDiff({
    tasks: [task("HF-1")],
    inflight: null,
    untracked: [],
    deployedAgentIDs: [OUR_AGENT],
  }).join("\n");
  assert.match(problems, /读不到实例配置或在途清单/);
  assert.doesNotMatch(problems, /HF-1/);
});

// config -> state_dir -> inflight.json 这条链要真的走通（yq 读 TOML + ~ 展开）；
// 任何一环断掉都返回 null，交给上层说「跳过」，不冒充满清单。
test("loadInflightTaskIDs resolves state_dir from the instance config", () => {
  const root = mkdtempSync(join(tmpdir(), "inflight-"));
  t_cleanup(root);
  const stateDir = join(root, "state");
  mkdirSync(stateDir);
  writeFileSync(join(stateDir, "inflight.json"), JSON.stringify([
    { kind: "platform", platform: "hifox", task_id: "HF-1" },
    { kind: "local", agent: "worker-1" },
    { kind: "cloud", task_id: "HF-2" }, // 旧形状：读入按平台条目归一
  ]));
  const cfg = join(root, "superctl.toml");
  writeFileSync(cfg, `schema_version = 1\nsession = "s"\nstate_dir = "${stateDir}"\n`);
  const ids = loadInflightTaskIDs(cfg);
  assert.ok(ids instanceof Set);
  assert.deepEqual([...ids].sort(), ["HF-1", "HF-2"]);

  // 清单不存在、配置不存在、state_dir 是相对路径读不到：一律 null（明说跳过）。
  writeFileSync(join(root, "empty.toml"), `session = "s"\n`);
  assert.equal(loadInflightTaskIDs(join(root, "empty.toml")), null);
  assert.equal(loadInflightTaskIDs(join(root, "nope.toml")), null);
  assert.equal(loadInflightTaskIDs(""), null);
});

// 接线：checkLiveWith 必须把差集接进 --live 的结果里（规则写好了但没调用等于没做）。
test("checkLiveWith reports the inflight diff through the live io", () => {
  const data = {
    platforms: [{ platform: "hifox", key: "daizong", name: "戴宗", space: "HF" }],
    environments: [{
      name: "prod", organization: "rayfox", space: "HF", configSet: "hifox", platform: "hifox",
      agents: { daizong: { id: "a1", deployedName: "戴宗" } },
    }],
  };
  const io = {
    listOrgs: () => [{ id: "org-1", slug: "rayfox" }],
    listSpaces: () => [{ id: "space-ours", identifier: "HF", organizationId: "org-1" }],
    listAgents: () => [{ id: "a1", name: "戴宗", spaceIds: ["space-ours"], allowTaskAssignment: true }],
    listTasks: () => [task("HF-9", { delegate: "a1" })],
    inflightIDs: () => new Set(),
  };
  assert.match(checkLiveWith(data, io).join("\n"), /HF-9/);

  // 登记进豁免表即通过——这条规则不该逼人只能靠「塞进在途清单」消警。
  assert.deepEqual(checkLiveWith({ ...data, untracked: ["HF-9"] }, io), []);
});

// 父单不在任何承接人名下（delegate 为空），只能从子单反查。事故里 HF-16321 就是这种：
// 自己 assigned + Todo，四个子单里三张派给林冲。只按 delegate 收窄会漏掉父单。
test("inflight diff: a parent of a delegated task is in scope", () => {
  const parent = {
    id: "p1", identifier: "HF-16321", status: { category: "unstarted" },
    delegateStatus: "assigned", delegate: null, parentTaskId: null,
  };
  const child = { ...task("HF-16322"), id: "c1", parentTaskId: "p1" };
  const problems = inflightDiff({
    tasks: [parent, child],
    inflight: new Set(),
    untracked: [],
    deployedAgentIDs: [OUR_AGENT],
  }).join("\n");
  assert.match(problems, /HF-16321/);
  assert.match(problems, /HF-16322/);

  // 父单自己已收口时不报（子单仍在跟踪）。
  const closed = { ...parent, status: { category: "completed" } };
  assert.deepEqual(inflightDiff({
    tasks: [closed, child],
    inflight: new Set(["HF-16322"]),
    untracked: [],
    deployedAgentIDs: [OUR_AGENT],
  }), []);
});
