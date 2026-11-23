#!/usr/bin/env node
// 本实例的注册表自检：框架不变量（vendor/super-agent 的 capabilities --check）+
// 本仓专属的跨仓对账。框架只认能力注册表自己的规则（ADR 0002）；「平台指针 ↔
// config/ 声明」「部署记录」「线上真值」是本仓的知识，住在这里。
//
// 用法：
//   node super-agent/scripts/check-registry.mjs            # 离线对账
//   node super-agent/scripts/check-registry.mjs --no-framework   # 只跑本实例规则（测试用）
//   node super-agent/scripts/check-registry.mjs --live     # 再拿平台真值核一遍（需要 hifox 登录）
//
// YAML 用 yq 转 JSON 读：与 x-deploy 同一套工具前提，不引 node 依赖。

import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const DEFAULT_ROOT = resolve(HERE, "../..");

// ---------- 通用 ----------

function readYaml(path) {
  if (!existsSync(path)) return null;
  const out = execFileSync("yq", ["-o=json", "."], { input: readFileSync(path), encoding: "utf8" });
  if (!out.trim()) return null;
  return JSON.parse(out);
}

function listDirs(path) {
  if (!existsSync(path)) return [];
  return readdirSync(path, { withFileTypes: true })
    .filter((e) => e.isDirectory())
    .map((e) => e.name)
    .sort();
}

function listFiles(path) {
  if (!existsSync(path)) return [];
  return readdirSync(path, { withFileTypes: true })
    .filter((e) => e.isFile())
    .map((e) => e.name)
    .sort();
}

// isOrgUUID 判定组织标识是不是完整 UUID（8-4-4-4-12 十六进制）。
export function isOrgUUID(s) {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(s);
}

// splitInstance 把平台名字拆成「基名 + 实例后缀」。平台上有两种后缀写法：
// `戴宗 (staging)` 与 `燕青 - staging`，都要认（与派活解析同一套规则）。
export function splitInstance(platformName) {
  const paren = platformName.lastIndexOf(" (");
  if (paren > 0 && platformName.endsWith(")")) {
    return [platformName.slice(0, paren), platformName.slice(paren + 2, -1)];
  }
  const dash = platformName.lastIndexOf(" - ");
  if (dash > 0) return [platformName.slice(0, dash), platformName.slice(dash + 3)];
  return [platformName, ""];
}

// nameMatches 判定平台基名是否就是声明的能力名：声明名可以是基名的前缀，但前缀之后
// 必须是词边界（免得「萧」误配「萧让」）。与框架 ResolveDelegate 同规则。
export function nameMatches(declared, base) {
  if (base === declared) return true;
  if (!base.startsWith(declared)) return false;
  const rest = [...base.slice(declared.length)];
  if (rest.length === 0) return false;
  return [" ", "(", "-", "（", "·", "　"].includes(rest[0]);
}

// platformNameMatches 剥掉实例后缀再比（已部署实例的名字必须仍是同一个身份）。
export function platformNameMatches(declared, platformName) {
  return nameMatches(declared, splitInstance(platformName)[0]);
}

// ---------- 数据收集 ----------

// collect 读出本仓的四处真值：能力注册表指针、config 声明、environment 绑定、state 部署记录。
export function collect(root) {
  const capRoot = join(root, "super-agent", "capabilities");
  const platforms = [];
  const platformDefaults = [];
  for (const platform of listDirs(join(capRoot, "platforms"))) {
    for (const file of listFiles(join(capRoot, "platforms", platform))) {
      if (file === "defaults.yaml") { // 平台默认派活参数，不是能力指针
        const body = readYaml(join(capRoot, "platforms", platform, file));
        if (body) platformDefaults.push({ platform, ...body });
        continue;
      }
      const key = file.replace(/\.ya?ml$/, "");
      if (key === file) continue; // 不是 yaml
      const body = readYaml(join(capRoot, "platforms", platform, file));
      if (!body) continue;
      platforms.push({ platform, key, ...body });
    }
  }

  const configSets = listDirs(join(root, "config"));
  const configAgents = []; // { set, key, name }
  for (const set of configSets) {
    for (const key of listDirs(join(root, "config", set, "agents"))) {
      const body = readYaml(join(root, "config", set, "agents", key, "agent.yaml"));
      if (body) configAgents.push({ set, key, name: body.name || "" });
    }
  }

  // 执行节点（ADR 0041）：只收声明，profile 与在线状态在 checkNodes/check:live 里核。
  const nodes = [];
  for (const file of listFiles(join(capRoot, "nodes"))) {
    const key = file.replace(/\.ya?ml$/, "");
    if (key === file) continue; // README 等非声明文件
    const body = readYaml(join(capRoot, "nodes", file));
    if (body) nodes.push({ key, ...body });
  }

  const environments = [];
  for (const name of listDirs(join(root, "environments"))) {
    const dir = join(root, "environments", name);
    const manifest = readYaml(join(dir, "environment.yaml"));
    if (!manifest) continue;
    const state = readYaml(join(dir, "state.yaml")) || {};
    environments.push({
      name,
      organization: manifest.organization || "",
      space: manifest.space || "",
      configSet: manifest.configSet || "",
      platform: manifest.cli?.app || "",
      agents: state.agents || {},
    });
  }

  return { platforms, platformDefaults, configSets, configAgents, environments, nodes };
}

// ---------- 离线对账 ----------

// checkOffline 返回给人读的问题列表。规则都来自本仓 ADR 0023（指针 ↔ config 双向同步）
// 与 ADR 0033（environment 的字段名）。
export function checkOffline({ platforms, platformDefaults = [], configSets, configAgents, environments }) {
  const problems = [];
  // defaults.yaml 是「点名负责人、不派 agent」建单的默认 org/space；它必须与同平台
  // 能力指针的 org 一致，否则显式负责人建单会落到另一个组织。
  for (const d of platformDefaults) {
    if (d.org && !isOrgUUID(d.org)) {
      problems.push(`platforms/${d.platform}/defaults.yaml：org 不是完整 UUID（${d.org}）`);
    }
    for (const p of platforms.filter((x) => x.platform === d.platform && x.org)) {
      if (d.org && p.org !== d.org) {
        problems.push(`platforms/${d.platform}/defaults.yaml：org 与 ${p.key}.yaml 不一致（${d.org} != ${p.org}）`);
      }
    }
  }

  // 平台归属：environment.yaml 的 cli.app + configSet 共同决定「哪个平台覆盖哪个集合」。
  // 推不出平台就没有可对账的对象——如实报一条，别静默跳过。
  const covered = new Map(); // configSet -> Set(platform)
  for (const env of environments) {
    if (!env.platform || !env.configSet) {
      problems.push(
        `environments/${env.name}/environment.yaml：缺 cli.app 或 configSet，config→平台指针同步无法对账`,
      );
      continue;
    }
    if (!covered.has(env.configSet)) covered.set(env.configSet, new Set());
    covered.get(env.configSet).add(env.platform);
  }

  // 指针 → config：指针声明的 key 必须在某个配置集合里有真源。
  const byKey = new Map(); // key -> [{set,name}]
  for (const agent of configAgents) {
    if (!byKey.has(agent.key)) byKey.set(agent.key, []);
    byKey.get(agent.key).push(agent);
  }
  for (const pointer of platforms) {
    const label = `platforms/${pointer.platform}/${pointer.key}.yaml`;
    const hits = byKey.get(pointer.key) || [];
    if (hits.length === 0) {
      problems.push(
        `${label}：config/ 下任何配置集合里都找不到 agents/${pointer.key}/agent.yaml`,
      );
    }
    // same identity：注册表里的名字就是派活解析平台 ref 的主干，与 config 声明不同名
    // 会让「注册表说的 agent」和「config 部署的 agent」悄悄变成两个。
    for (const hit of hits) {
      if (!nameMatches(pointer.name, hit.name)) {
        problems.push(
          `${label}：name 是 ${JSON.stringify(pointer.name)}，config/${hit.set}/agents/${pointer.key}/agent.yaml 里是 ${JSON.stringify(hit.name)}`,
        );
      }
    }
    // org 只在跨组织派活时需要；写就得是完整 UUID（slug 会被平台判 404）。
    if (pointer.org && !isOrgUUID(pointer.org)) {
      problems.push(
        `${label}：org 是 ${JSON.stringify(pointer.org)}，必须写平台组织完整 UUID（slug 在 HIFOX_ORGANIZATION_ID 下会 404）`,
      );
    }
  }

  // config → 指针：覆盖该集合的每个平台都得有一份指针。
  const pointerKeys = new Map(); // platform -> Set(key)
  for (const pointer of platforms) {
    if (!pointerKeys.has(pointer.platform)) pointerKeys.set(pointer.platform, new Set());
    pointerKeys.get(pointer.platform).add(pointer.key);
  }
  for (const set of configSets) {
    const keys = configAgents.filter((a) => a.set === set).map((a) => a.key);
    if (keys.length === 0) continue; // 空集合谈不上「同步了吗」
    const platformsForSet = [...(covered.get(set) || [])].sort();
    if (platformsForSet.length === 0) {
      problems.push(
        `config/${set}/：没有环境声明它部署在哪个平台（environments/*/environment.yaml 缺 cli.app 或 configSet）`,
      );
      continue;
    }
    for (const key of keys) {
      for (const platform of platformsForSet) {
        if (!pointerKeys.get(platform)?.has(key)) {
          problems.push(
            `config/${set}/agents/${key}：缺 super-agent/capabilities/platforms/${platform}/${key}.yaml（config 的 agent 变更必须同步到主控能力目录，见 ADR 0023）`,
          );
        }
      }
    }
  }

  // 部署记录：只对「至少有一个环境声明了它」的平台查。没有环境接入时「没有部署记录」
  // 是必然的，报出来只是噪声。
  const declaredPlatforms = new Set(environments.map((e) => e.platform).filter(Boolean));
  const deployed = new Map(); // platform -> Set(key)
  for (const env of environments) {
    if (!env.platform) continue;
    if (!deployed.has(env.platform)) deployed.set(env.platform, new Set());
    for (const key of Object.keys(env.agents)) deployed.get(env.platform).add(key);
  }
  for (const pointer of platforms) {
    if (!declaredPlatforms.has(pointer.platform)) continue;
    if (!deployed.get(pointer.platform)?.has(pointer.key)) {
      problems.push(
        `platforms/${pointer.platform}/${pointer.key}.yaml：environments/ 下没有任何部署记录（声明了但没部署）`,
      );
    }
  }

  return problems;
}

// ---------- 能力技能与手册的文本不变量 ----------

// SKILL_REF 是手册/技能里引用另一个技能的唯一允许写法（ADR 0037）：裸技能名会
// 在改名后静默腐烂，锚定格式才能被检查器验。
const SKILL_REF = /\[\[skill:([a-z0-9-]+)\]\]/g;

// MACHINE_PATH 抓写死的本机绝对路径（/Users/<人>/…、/home/<人>/…）。路径是机制
// 事实，由框架首条注入给出（ADR 0007）；手册里写死会换机/换用户即失效——主控的
// pane cwd 也可能是旧工作树，绝对路径必须来自注入而不是抄一份。`/Users/...` 这种
// 省略写法是反例说明，不在此列（要求路径段后还有 `/`）。
const MACHINE_PATH = /\/(?:Users|home)\/[^/\s`'"）)]+\//;

// checkCapabilityDocs 检查本地运行时的能力集：
//  1. `[[skill:<name>]]` 必须命中实例内存在的技能；文档所属 agent 还必须已挂载它
//     （专属目录自动挂载，共享技能看 skills.yaml）。
//  2. 同一规范化正文行（≥24 字）不得在两处重复：SKILL.md 之间、手册之间、手册与
//     技能之间都算——「一个事实只许有一份」跨形态成立。标题、frontmatter、代码块
//     不参与比较（模板内容天然会重复）。
//  3. 手册/技能里不得出现机器绝对路径（`/Users/…`、`/home/…`）：那条路径只该在
//     首条注入里出现一次，写进文档等于抄了一份会腐烂的真值。
//
//  3. 同一字段的闭集声明（`字段：A | B | C` 形态）只许出现一次。逐字比较拦不住
//     改写过的漂移：实测主控手册写「Bug / 需求 / 未指定」、协议技能写四值，逐字
//     门禁放行了一整轮。闭集只有一个宿主，其余地方引用。
//
// 手册也纳入，是因为只查技能时实测漏过一次：主控手册的类型闭集写三值、协议技能
// 写四值，两边都没报错，漂移活了整整一轮。
//
// 只看本地能力目录；平台侧技能由 x-deploy 管理，不在这里（ADR 0037 的范围）。
export function checkCapabilityDocs(root) {
  const capRoot = join(root, "super-agent", "capabilities");
  const problems = [];

  const shared = new Map(); // key -> SKILL.md 路径
  for (const key of listDirs(join(capRoot, "skills"))) {
    const p = join(capRoot, "skills", key, "SKILL.md");
    if (existsSync(p)) shared.set(key, p);
  }

  const agents = new Map(); // key -> { dir, own: Map, declared: Set }
  for (const key of listDirs(join(capRoot, "agents"))) {
    const dir = join(capRoot, "agents", key);
    const own = new Map();
    for (const s of listDirs(join(dir, "skills"))) {
      const p = join(dir, "skills", s, "SKILL.md");
      if (existsSync(p)) own.set(s, p);
    }
    const declared = new Set();
    const skillsYaml = join(dir, "skills.yaml");
    if (existsSync(skillsYaml)) {
      const body = readYaml(skillsYaml);
      if (Array.isArray(body)) for (const k of body) declared.add(String(k));
    }
    agents.set(key, { dir, own, declared });
  }

  const allOwnKeys = new Set();
  for (const a of agents.values()) for (const k of a.own.keys()) allOwnKeys.add(k);

  const label = (p) => relative(root, p);
  const checkRefs = (path, scope) => {
    if (!existsSync(path)) return;
    const text = readFileSync(path, "utf8");
    if (MACHINE_PATH.test(text)) {
      problems.push(`${label(path)}：写死了本机绝对路径「${text.match(MACHINE_PATH)[0]}…」——路径由首条注入给出，文档里只引用注入项（ADR 0007）`);
    }
    for (const m of text.matchAll(SKILL_REF)) {
      const name = m[1];
      if (!shared.has(name) && !allOwnKeys.has(name)) {
        problems.push(`${label(path)}：引用的技能 "${name}" 不存在（应放在 capabilities/skills/${name}/ 或某 agent 的 skills/${name}/）`);
        continue;
      }
      if (!scope) continue;
      const a = agents.get(scope);
      if (!a.own.has(name) && !a.declared.has(name)) {
        problems.push(`${label(path)}：引用的技能 "${name}" 没有挂载给 ${scope}（加到 ${scope} 的 skills.yaml，或改引用）`);
      }
    }
  };

  for (const [key, a] of agents) {
    checkRefs(join(a.dir, "instructions.md"), key);
    for (const p of a.own.values()) checkRefs(p, key);
  }
  for (const p of shared.values()) checkRefs(p, null);

  // 重复正文行：规范化（去空白）后按长度过滤，避开短句与列表符号造成的噪声。
  const seen = new Map(); // norm -> { label, text }
  const scanDoc = (path) => {
    const text = readFileSync(path, "utf8")
      .replace(/^---\r?\n[\s\S]*?\r?\n---\r?\n/, "")
      .replace(/```[\s\S]*?```/g, "");
    for (const raw of text.split(/\r?\n/)) {
      const line = raw.trim();
      if (!line || line.startsWith("#")) continue;
      const norm = line.replace(/\s+/g, "");
      if (norm.length < 24) continue;
      const here = label(path);
      const prev = seen.get(norm);
      if (!prev) {
        seen.set(norm, { label: here, text: line });
        continue;
      }
      if (prev.label === here) continue;
      const key = `${prev.label}\u0000${here}\u0000${norm}`;
      if (reported.has(key)) continue;
      reported.add(key);
      problems.push(`${prev.label} 与 ${here}：正文重复（一个事实只许有一份）：「${line.slice(0, 48)}」`);
    }
  };
  const reported = new Set();

  // 闭集声明的单一宿主：`字段：甲 | 乙 | 丙`。相同字段第二次以这种形态出现即漂移，
  // 不管取值是否一致——改写版（`甲 / 乙`）与扩充版（多一个取值）都会被拦下。
  const closedSets = new Map(); // 字段名 -> { label, line }
  const scanEnums = (path) => {
    if (!existsSync(path)) return;
    const here = label(path);
    for (const raw of readFileSync(path, "utf8").split(/\r?\n/)) {
      const m = raw.trim().match(/^[-*\s]*([^：:（(`]{1,12})[：:]\s*([^|]+\|.+)$/);
      if (!m) continue;
      const field = m[1].replace(/\s+/g, "");
      const line = raw.trim();
      const prev = closedSets.get(field);
      if (!prev) {
        closedSets.set(field, { label: here, line });
        continue;
      }
      if (prev.label === here) continue;
      problems.push(
        `${prev.label} 与 ${here}：字段「${field}」的闭集声明出现两次（一个事实只许有一份）：` +
          `「${prev.line.slice(0, 40)}」/「${line.slice(0, 40)}」`,
      );
    }
  };

  for (const p of shared.values()) {
    scanDoc(p);
    scanEnums(p);
  }
  for (const a of agents.values()) {
    scanDoc(join(a.dir, "instructions.md"));
    scanEnums(join(a.dir, "instructions.md"));
    for (const p of a.own.values()) {
      scanDoc(p);
      scanEnums(p);
    }
  }

  return problems;
}

// ---------- 线上真值 ----------

function hifoxJson(args, org = "") {
  const env = { ...process.env };
  if (org) env.HIFOX_ORGANIZATION_ID = org;
  else delete env.HIFOX_ORGANIZATION_ID;
  // maxBuffer 默认 1MB：agent list 一个产研组织轻松超，截断会报成 ENOBUFS 而不是数据问题。
  const out = execFileSync("hifox", [...args, "--output", "json"], {
    encoding: "utf8",
    env,
    maxBuffer: 64 * 1024 * 1024,
  });
  return out.trim() ? JSON.parse(out) : [];
}

// checkLive 拿平台真值核对部署记录。纯函数部分（recordChecks）抽出来便于测试；
// fetchLive 负责把 slug 解析成 UUID/space id 并列出 agent。
export function recordChecks(env, agents, pointersByKey) {
  const problems = [];
  for (const key of Object.keys(env.agents).sort()) {
    const dep = env.agents[key];
    const prefix = `environments/${env.name}/agents/${key}：`;
    const live = agents.find((a) => a.id === dep.id);
    if (!live) {
      problems.push(prefix + `平台查不到这个 id（${dep.id}）`);
      continue;
    }
    if (live.name !== dep.deployedName) {
      problems.push(
        prefix + `平台名字是 ${JSON.stringify(live.name)}，state.yaml 记的是 ${JSON.stringify(dep.deployedName)}`,
      );
    }
    const pointer = pointersByKey.get(key);
    if (pointer && !platformNameMatches(pointer.name, live.name)) {
      problems.push(
        prefix +
          `线上名字 ${JSON.stringify(live.name)} 与 platforms/${env.platform}/${key}.yaml 声明的 ${JSON.stringify(pointer.name)} 不是同一个身份`,
      );
    }
    if (pointer?.org && pointer.org !== env.orgID) {
      problems.push(
        `platforms/${env.platform}/${key}.yaml：org 是 ${JSON.stringify(pointer.org)}，环境 ${env.name} 的 organization 是 ${JSON.stringify(env.orgID)}`,
      );
    }
    if (env.spaceID && !(live.spaceIds || []).includes(env.spaceID)) {
      problems.push(prefix + `平台 spaceIds ${JSON.stringify(live.spaceIds)} 不含环境 space ${env.spaceID}`);
    }
    if (live.archivedAt) problems.push(prefix + `已归档（${live.archivedAt}）`);
    if (live.allowTaskAssignment === false) problems.push(prefix + "平台不允许派任务（allowTaskAssignment=false）");
  }
  return problems;
}

// checkLiveWith 是注入版（测试用）：io.listOrgs / listSpaces / listAgents。
export function checkLiveWith({ platforms, environments }, io) {
  const listOrgs = io.listOrgs;
  const listSpaces = io.listSpaces;
  const listAgents = io.listAgents;

  const orgs = listOrgs();
  const spaces = listSpaces();
  const problems = [];
  const agentsByOrg = new Map();
  const pointersByKey = new Map(platforms.map((p) => [p.key, p]));

  for (const env of environments) {
    if (Object.keys(env.agents).length === 0) continue; // 未部署：不参与线上对账
    if (!env.platform) {
      problems.push(`environments/${env.name}/environment.yaml：没有 cli.app，推不出平台，跳过线上对账`);
      continue;
    }
    if (env.platform !== "hifox") {
      problems.push(`environments/${env.name}/：平台 ${JSON.stringify(env.platform)} 尚无线上对账实现`);
      continue;
    }
    // 名称优先（ADR 0033）：organization 可以是 slug 或 UUID，space 可以是 identifier 或 UUID。
    const org = orgs.find((o) => o.slug === env.organization || o.id === env.organization);
    if (!org) {
      problems.push(`environments/${env.name}/environment.yaml：organization ${JSON.stringify(env.organization)} 在平台上查不到`);
      continue;
    }
    // identifier 只在组织内唯一（同一个 HF 可能出现在多个组织）；先按组织收窄，
    // 否则线上对账会把别的组织的同名 Space 当成候选。
    const inOrg = spaces.filter((s) => !s.organizationId || s.organizationId === org.id);
    const spaceIDs = new Set(
      inOrg
        .filter((s) => s.identifier === env.space || s.id === env.space)
        .map((s) => s.id),
    );
    const spaceID = spaceIDs.size === 1 ? [...spaceIDs][0] : "";
    if (!spaceID) {
      problems.push(`environments/${env.name}/environment.yaml：space ${JSON.stringify(env.space)} 在平台上查不到唯一实例`);
    }
    if (!agentsByOrg.has(org.id)) {
      try {
        agentsByOrg.set(org.id, listAgents(org.id));
      } catch (err) {
        problems.push(`org ${org.slug || org.id}：agent list 失败：${err.message}`);
        continue;
      }
    }
    problems.push(
      ...recordChecks(
        { ...env, orgID: org.id, spaceID },
        agentsByOrg.get(org.id),
        pointersByKey,
      ),
    );
  }
  return problems;
}

// checkLive 用真实 hifox CLI 取平台真值。
export function checkLive(data) {
  return checkLiveWith(data, {
    listOrgs: () => hifoxJson(["organization", "list"]),
    listSpaces: () => hifoxJson(["space", "list"]),
    listAgents: (org) => hifoxJson(["agent", "list", "--include-archived"], org),
  });
}

// ---------- CLI ----------

function runFrameworkCheck(root) {
  // 优先用构建好的稳定入口：实例自检不该依赖 Go 工具链，也不该受 PATH 里
  // 旧 go 的影响（bin 缺失时才回退到 go run 包装脚本）。
  const stable = join(root, "super-agent", "bin", "superctl");
  const wrapper = join(root, "super-agent", "superctl");
  if (!existsSync(stable) && !existsSync(wrapper)) {
    throw new Error(`找不到本实例的 superctl 入口 ${stable} 或 ${wrapper}（--root 指仓根，不是实例根）`);
  }
  const bin = existsSync(stable) ? stable : wrapper;
  // --root 固定检查这个检出：否则框架会按本机配置去查另一个实例的目录，
  // worktree/CI 里就会「测的是主检出」。
  const out = execFileSync(bin, ["capabilities", "--check", "--root", join(root, "super-agent")], {
    encoding: "utf8",
    maxBuffer: 8 * 1024 * 1024,
  });
  return out.trim();
}

// machineRows 把 `herdr machine list --json` 的两种返回形态（数组 / 单对象）归一。
// 直接 JSON.stringify + includes 会把别的 profile 的 label 当成命中，也会漏掉
// target/session 不一致——ADR 0041 要求逐字段核对。
function machineRows(machines) {
  if (Array.isArray(machines)) return machines;
  if (Array.isArray(machines?.machines)) return machines.machines;
  if (Array.isArray(machines?.result?.machines)) return machines.result.machines;
  if (machines && typeof machines === "object") return [machines];
  return [];
}

function rowProfileIds(row) {
  return [row.id, row.profile_id].filter((v) => typeof v === "string" && v);
}

function rowMatchesMachine(row, machine) {
  return rowProfileIds(row).includes(machine) || row.label === machine;
}

function rowSSHTarget(row) {
  return typeof row.target === "string" ? row.target
    : typeof row.ssh === "string" ? row.ssh : "";
}

// localIdentity 读当前机器的稳定标识（ADR 0042）。判断「这条声明是不是本机节点」：
// 本机节点不走 herdr machine profile（自己连自己），profile 检查必须跳过它。
export function localIdentity() {
  try {
    if (existsSync("/etc/machine-id")) {
      return readFileSync("/etc/machine-id", "utf8").trim();
    }
  } catch {
    // 继续按 macOS 读
  }
  try {
    const out = execFileSync("ioreg", ["-rd1", "-c", "IOPlatformExpertDevice"],
      { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] });
    const m = out.match(/"IOPlatformUUID"\s*=\s*"([^"]+)"/);
    return m ? m[1] : "";
  } catch {
    return "";
  }
}

// isSelfNode 判定声明是否属于当前机器：identity 非空且相等。
export function isSelfNode(node, identity) {
  return Boolean(node && node.identity && identity && node.identity === identity);
}

// checkNodes 校验节点声明与本地 herdr saved machine 的对齐（ADR 0041）。离线：
// profile 必须存在，且 target 与 ssh、session 与 profile 一致；在线：checkNodesLive
// 再探可达性。在线状态永不写进声明。
export function checkNodes(nodes, machines, identity) {
  const problems = [];
  const rows = machineRows(machines);
  const selfId = identity === undefined ? localIdentity() : identity;
  for (const n of nodes || []) {
    if (isSelfNode(n, selfId)) continue; // 本机节点不走 --machine（ADR 0042）
    if (!n.machine) continue; // 框架 Check 已报缺字段，这里不重复
    const row = rows.find((r) => r && rowMatchesMachine(r, n.machine));
    if (!row) {
      problems.push(`nodes/${n.key}.yaml：herdr machine list 里没有 profile ${JSON.stringify(n.machine)}（先 herdr machine add）`);
      continue;
    }
    if (n.ssh) {
      const target = rowSSHTarget(row);
      if (!target) {
        problems.push(`nodes/${n.key}.yaml：profile ${JSON.stringify(n.machine)} 没有 SSH target，无法核对 ssh ${JSON.stringify(n.ssh)}`);
      } else if (target !== n.ssh) {
        problems.push(`nodes/${n.key}.yaml：profile ${JSON.stringify(n.machine)} 的 SSH target 是 ${JSON.stringify(target)}，与 ssh ${JSON.stringify(n.ssh)} 不一致`);
      }
    }
    if (n.session) {
      if (typeof row.session !== "string" || !row.session) {
        problems.push(`nodes/${n.key}.yaml：profile ${JSON.stringify(n.machine)} 没有 session，无法核对 ${JSON.stringify(n.session)}`);
      } else if (row.session !== n.session) {
        problems.push(`nodes/${n.key}.yaml：profile ${JSON.stringify(n.machine)} 的 session 是 ${JSON.stringify(row.session)}，与声明的 ${JSON.stringify(n.session)} 不一致`);
      }
    }
  }
  return problems;
}

// checkNodesLive 是 --live 的在线探测：转发面从不启动/安装/重启远端 server，
// 不可达就是失败，由人处理。status 是测试缝（默认调 herdr machine status）。
export function checkNodesLive(nodes, status, identity) {
  const problems = [];
  const selfId = identity === undefined ? localIdentity() : identity;
  const probe = status || ((machine) => execFileSync(
    "herdr", ["machine", "status", machine, "--json"],
    { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] },
  ));
  for (const n of nodes || []) {
    if (isSelfNode(n, selfId)) continue; // 本机节点不需要可达探测（ADR 0042）
    if (!n.machine) continue;
    let rows;
    try {
      rows = machineRows(JSON.parse(probe(n.machine)));
    } catch (err) {
      const detail = String(err.stderr || err.message || "").trim().split("\n")[0];
      problems.push(`nodes/${n.key}.yaml：herdr machine status ${JSON.stringify(n.machine)} 失败（${detail}）`);
      continue;
    }
    if (rows.length === 0) {
      problems.push(`nodes/${n.key}.yaml：herdr machine status ${JSON.stringify(n.machine)} 没有返回该 profile`);
      continue;
    }
    const matches = rows.filter((r) => r && rowMatchesMachine(r, n.machine));
    const selected = matches.length > 0 ? matches : rows.length === 1 ? rows : [];
    if (selected.length === 0) {
      problems.push(`nodes/${n.key}.yaml：herdr machine status ${JSON.stringify(n.machine)} 没有返回该 profile`);
      continue;
    }
    if (selected.some((r) => r && r.reachable === false)) {
      problems.push(`nodes/${n.key}.yaml：profile ${JSON.stringify(n.machine)} 不可达（machine status reachable=false）`);
    }
  }
  return problems;
}

function main(argv) {
  const args = new Set(argv);
  if (args.has("-h") || args.has("--help")) {
    console.log("用法: check-registry.mjs [--root <仓根>] [--no-framework] [--live]");
    return 0;
  }
  const rootIndex = argv.indexOf("--root");
  const root = rootIndex >= 0 ? resolve(argv[rootIndex + 1] || ".") : DEFAULT_ROOT;
  const data = collect(root);
  const problems = checkOffline(data);
  problems.push(...checkCapabilityDocs(root));
  if ((data.nodes || []).length > 0) {
    let machines = [];
    let herdrAvailable = true;
    try {
      machines = JSON.parse(execFileSync("herdr", ["machine", "list", "--json"], { encoding: "utf8" }));
    } catch (err) {
      if (err.code === "ENOENT") {
        herdrAvailable = false;
        console.error("提示：未安装 herdr，跳过节点 profile 对账（只校验声明）");
      } else {
        problems.push(`nodes：无法读取 herdr machine list（${err.message}）`);
      }
    }
    if (herdrAvailable) problems.push(...checkNodes(data.nodes, machines));
    if (args.has("--live")) {
      if (!herdrAvailable) problems.push("nodes：--live 需要 herdr（machine status）");
      else problems.push(...checkNodesLive(data.nodes));
    }
  }
  if (args.has("--live")) problems.push(...checkLive(data));

  if (!args.has("--no-framework")) {
    try {
      process.stdout.write(runFrameworkCheck(root).trimEnd() + "\n");
    } catch (err) {
      problems.push(`框架自检失败：${err.message}`);
    }
  }

  for (const p of problems) console.error("漂移:", p);
  if (problems.length > 0) {
    console.error(`本实例检查：${problems.length} 处问题`);
    return 1;
  }
  console.log(`本实例检查通过（${relative(process.cwd(), root) || "."}）`);
  return 0;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const code = main(process.argv.slice(2));
    // 输出可能经过多路写入（框架子进程直写 stdout）；退出码才是调用方的判据。
    process.exitCode = code;
  } catch (err) {
    console.error("check-registry:", err.message);
    if (err.message.includes("ENOENT") && err.message.includes("yq")) {
      console.error("需要 yq（brew install yq / https://github.com/mikefarah/yq）");
    }
    process.exit(2);
  }
}


