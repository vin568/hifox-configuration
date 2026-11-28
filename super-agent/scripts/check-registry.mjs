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
import { existsSync, readFileSync, readdirSync, realpathSync, statSync } from "node:fs";
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const DEFAULT_ROOT = resolve(HERE, "../..");

// LESSON_SCOPE_SECTIONS 与框架 internal/lessons 的三段作用域同构：改这里必须同步那边。
const LESSON_SCOPE_RE = /^(全局|仓库\/[^/\s]+|agent\/[^/\s]+)$/;
const LESSON_LINE_RE = /^- \[(已验证|待确认)\] (\(\d{4}-\d{2}-\d{2}\)) .+$/;

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

  // 项目与检出表（ADR 0043）：项目是机器无关的仓库身份，检出路径写在 nodes/<key>/。
  const projects = [];
  for (const file of listFiles(join(capRoot, "projects"))) {
    const key = file.replace(/\.ya?ml$/, "");
    if (key === file) continue;
    const body = readYaml(join(capRoot, "projects", file));
    if (body) projects.push({ key, ...body });
  }
  // 能力对项目的声明（agents/<key>/projects.yaml，ADR 0043）。
  const capabilityAgents = [];
  for (const key of listDirs(join(capRoot, "agents"))) {
    const body = readYaml(join(capRoot, "agents", key, "projects.yaml"));
    capabilityAgents.push({ key, projects: Array.isArray(body) ? body.map(String) : [] });
  }
  const checkouts = []; // { node, project, path }
  for (const node of listDirs(join(capRoot, "nodes"))) {
    const file = join(capRoot, "nodes", node, "checkouts.yaml");
    const body = readYaml(file);
    if (!body) continue;
    for (const [project, path] of Object.entries(body)) {
      checkouts.push({ node, project, path: String(path || "") });
    }
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

  // 哨兵盲区的显式豁免（在途差集检查用）。它必须住在清单之外：在途清单就是
  // 哨兵的视野，「在清单里但标不跟踪」自相矛盾——框架不读 note，照样轮询、照样注入事件。
  const untrackedBody = readYaml(join(root, "super-agent", "untracked.yaml")) || {};
  const untracked = Array.isArray(untrackedBody.tasks) ? untrackedBody.tasks : [];

  return {
    platforms, platformDefaults, configSets, configAgents, environments, nodes,
    projects, checkouts, capabilityAgents, untracked,
  };
}

// ---------- 离线对账 ----------

// checkProjects 校验项目与检出表的引用闭合（ADR 0043）。框架 Check 已覆盖 schema
// 半边；这里补本仓口径：一个项目至少要有一台机器能干活，否则声明了也派不出去。
export function checkProjects(projects, checkouts, agents) {
  const problems = [];
  const keys = new Set((projects || []).map((p) => p.key));
  for (const p of projects || []) {
    if (!(checkouts || []).some((c) => c.project === p.key) && !(agents || []).some((a) => a.projects?.includes(p.key))) {
      // 只声明、没人用也没机器能跑：留着等于让主控以为它能派。
      problems.push(`projects/${p.key}.yaml：没有机器检出，也没有任何能力声明它——声明了但派不出去`);
    }
  }
  for (const c of checkouts || []) {
    if (!keys.has(c.project)) {
      problems.push(`nodes/${c.node}/checkouts.yaml：项目 ${JSON.stringify(c.project)} 没有声明（应放在 projects/${c.project}.yaml）`);
    }
  }
  return problems;
}

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

// 节点锚点：定义 `{#id}`（机器可读的阶段身份），引用 `[[node:id]]`（回流/跳转）。
// 与框架 internal/capability/nodeanchor.go 同构——两份实现是刻意的，检查器零依赖。
const NODE_DEF = /\{#([a-z][a-z0-9-]*)\}/g;
// 与 Go 的 ParseNodeRefs 一致：内部允许空白（TrimSpace 后取 id），否则
// `[[node: implement]]` 在解析器眼里合法、在检查器眼里不存在，静默漏过。
const NODE_REF = /\[\[node:\s*([a-z][a-z0-9-]*)\s*\]\]/g;

// findAll 用全新的正则实例取全部匹配：matchAll 要求带 g，而带 g 的正则跨调用会
// 保留 lastIndex。逐行调用时这不是理论风险——同一个正则实例被反复 matchAll 时，
// 上一轮残留的 lastIndex 会让下一行开头的匹配被跳过。
const findAll = (re, text) => [...text.matchAll(new RegExp(re.source, re.flags))];

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
  // `@/` 的锚：持有 super-agent/ 的那个仓库根（ADR 0050）。本检查器只跑本仓，
  // 根就是 root；不读实例配置，避免 CLI 没登录/没配置时检查面跟着变。
  const pathRoot = root;
  const problems = [];

  const shared = new Map(); // key -> SKILL.md 路径
  for (const key of listDirs(join(capRoot, "skills"))) {
    const p = join(capRoot, "skills", key, "SKILL.md");
    if (existsSync(p)) shared.set(key, p);
  }

  // 解析一条 skills.yaml 声明：裸名 → 共享池；`@/…` → 仓库根下的技能目录。
  // 返回 { key, path } 或抛错（路径越界/目录不存在都是硬错误，与 Go 侧同口径）。
  const resolveDeclared = (spec) => {
    const s = String(spec).trim();
    if (!s.startsWith("@/")) {
      const p = shared.get(s);
      return p ? { key: s, path: p } : null;
    }
    const rel = s.slice(2).trim();
    if (!rel) throw new Error(`技能引用 ${JSON.stringify(s)} 缺少路径`);
    // `..` 一律拒绝，不是只拦越界（ADR 0050）：根内绕一圈的写法解析后与直写那份
    // 无法区分，排查时对不上声明。与 Go 侧 ResolveSkillRef 同口径——两处不一致时
    // 这里放行的引用会在派活链上 fatal。
    if (rel.split("/").includes("..")) {
      throw new Error(`技能引用 ${JSON.stringify(s)} 含 \`..\`（路径根内直接写目标位置，不要绕）`);
    }
    const abs = resolve(pathRoot, rel);
    const back = relative(pathRoot, abs);
    if (back === ".." || back.startsWith(`..${sep}`) || isAbsolute(back)) {
      throw new Error(`技能引用 ${JSON.stringify(s)} 越出路径根 ${pathRoot}`);
    }
    // 符号链接二次校验：字符串前缀判断挡不住「根内的链接指向根外」。
    // 与 Go 的 ResolveSkillRef、x-deploy 的 assertWithinRoot 同一套判据。
    if (existsSync(pathRoot) && existsSync(abs)) {
      const realBase = realpathSync(pathRoot);
      const realTarget = realpathSync(abs);
      const back2 = relative(realBase, realTarget);
      if (back2 === ".." || back2.startsWith(`..${sep}`) || isAbsolute(back2)) {
        throw new Error(`技能引用 ${JSON.stringify(s)} 经符号链接越出路径根 ${pathRoot}`);
      }
    }
    const p = join(abs, "SKILL.md");
    if (!existsSync(p)) throw new Error(`技能引用 ${JSON.stringify(s)} 指向的目录缺 SKILL.md（${p}）`);
    return { key: basename(abs), path: p, external: true };
  };

  const agents = new Map(); // key -> { dir, own: Map, declared: Set, external: Map, playbooks: Set }
  // playbooks.yaml 与 skills.yaml 同形态（裸 YAML 列表）；只用来回答「这门作业法
  // 由谁持有」。文件不存在 = 这个 agent 没有作业法（合法）。解析失败**不吞**：
  // 坏 YAML 会让「谁持有」判断静默变成空，作业法里的技能引用就再也报不出来——
  // 与框架 --check 的口径一致（那里也是 fatal 而不是忽略）。
  const readPlaybookKeys = (dir) => {
    const p = join(dir, "playbooks.yaml");
    if (!existsSync(p)) return [];
    const body = readYaml(p);
    return Array.isArray(body) ? body.map(String) : [];
  };
  for (const key of listDirs(join(capRoot, "agents"))) {
    const dir = join(capRoot, "agents", key);
    const own = new Map();
    for (const s of listDirs(join(dir, "skills"))) {
      const p = join(dir, "skills", s, "SKILL.md");
      if (existsSync(p)) own.set(s, p);
    }
    const declared = new Set();
    const external = []; // [{ key, path }]：以路径为单位，同一 key 出现在两个路径必须都留
                              // （以 key 为键时后一条会覆盖前一条，真实重名就被吞掉）
    const skillsYaml = join(dir, "skills.yaml");
    if (existsSync(skillsYaml)) {
      const body = readYaml(skillsYaml);
      if (Array.isArray(body)) {
        for (const raw of body) {
          let hit;
          try {
            hit = resolveDeclared(raw);
          } catch (error) {
            problems.push(`agents/${key}/skills.yaml：${error.message}`);
            continue;
          }
          if (!hit) {
            // 裸名落到共享池的判定留给框架（它报得更准）；这里只记下声明本身。
            declared.add(String(raw));
            continue;
          }
          if (hit.external) external.push({ key: hit.key, path: hit.path });
          else declared.add(String(raw));
        }
      }
    }
    agents.set(key, { dir, own, declared, external, playbooks: new Set(readPlaybookKeys(dir)) });
  }

  const allOwnKeys = new Set();
  for (const a of agents.values()) for (const k of a.own.keys()) allOwnKeys.add(k);
  const allExternalKeys = new Set();
  for (const a of agents.values()) for (const e of a.external) allExternalKeys.add(e.key);

  // 同名技能不能在两处出现：底层按 name 注册，重名会被加前缀，而带前缀的名字在
  // `[[skill:]]` 里写不出来（ADR 0050）。跨共享池、专属目录、外部引用一起算。
  const nameHost = new Map(); // name -> 出处
  const noteHost = (name, where) => {
    const prev = nameHost.get(name);
    if (prev && prev !== where) {
      problems.push(`${prev} 与 ${where} 的技能名都是 "${name}"：底层按 name 注册，重名会被加前缀，裸名只剩一个`);
      return;
    }
    if (!prev) nameHost.set(name, where);
  };
  for (const [key] of shared) noteHost(key, `capabilities/skills/${key}`);
  for (const [agent, a] of agents) {
    for (const k of a.own.keys()) noteHost(k, `agents/${agent}/skills/${k}`);
    for (const e of a.external) noteHost(e.key, `skills.yaml → ${relative(root, e.path)}`);
  }

  const label = (p) => relative(root, p);
  const checkRefs = (path, scope) => {
    if (!existsSync(path)) return;
    const text = readFileSync(path, "utf8");
    if (MACHINE_PATH.test(text)) {
      problems.push(`${label(path)}：写死了本机绝对路径「${text.match(MACHINE_PATH)[0]}…」——路径由首条注入给出，文档里只引用注入项（ADR 0007）`);
    }
    for (const m of text.matchAll(SKILL_REF)) {
      const name = m[1];
      if (!shared.has(name) && !allOwnKeys.has(name) && !allExternalKeys.has(name)) {
        problems.push(`${label(path)}：引用的技能 "${name}" 不存在（应放在 capabilities/skills/${name}/、某 agent 的 skills/${name}/，或用 @/ 路径引用并在 skills.yaml 声明）`);
        continue;
      }
      if (!scope) continue;
      const a = agents.get(scope);
      if (!a.own.has(name) && !a.declared.has(name) && !a.external.some((e) => e.key === name)) {
        problems.push(`${label(path)}：引用的技能 "${name}" 没有挂载给 ${scope}（加到 ${scope} 的 skills.yaml，或改引用）`);
      }
    }
  };

  for (const [key, a] of agents) {
    checkRefs(join(a.dir, "instructions.md"), key);
    for (const p of a.own.values()) checkRefs(p, key);
  }
  for (const p of shared.values()) checkRefs(p, null);
  for (const [key, a] of agents) for (const e of a.external) checkRefs(e.path, key);

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
  // 作业法里的 `[[skill:]]`：必须命中某个技能，且至少被「声明了这门作业法」的某个
  // agent 挂载。作业法是按 key 派给具体 agent 的，全仓都没人挂就是死引用——派活时
  // 模型拿不到实现，只会照自己的理解干。
  // 节点锚点：定义不重复、引用有落点。与框架同口径（去掉围栏与行内代码再判）。
  const checkPlaybookAnchors = (path) => {
    // 围栏与行内代码整段置空但**保留行数**：删字符会让后面所有行号前移，报错指错位置。
    const blank = (t) => t.replace(/[^\n]/g, "");
    const raw = readFileSync(path, "utf8")
      .replace(/```[\s\S]*?```/g, blank)
      .replace(/`[^`\n]*`/g, blank);
    const defined = new Map(); // id -> 首次出现的行号
    raw.split(/\r?\n/).forEach((line, i) => {
      for (const m of findAll(NODE_DEF, line)) {
        const prev = defined.get(m[1]);
        if (prev !== undefined) {
          problems.push(`${label(path)}:${i + 1}：节点锚点 ${m[0]} 与第 ${prev} 行重复（一个 id 只能标一个节点）`);
          continue;
        }
        defined.set(m[1], i + 1);
      }
    });
    raw.split(/\r?\n/).forEach((line, i) => {
      for (const m of findAll(NODE_REF, line)) {
        if (!defined.has(m[1])) {
          problems.push(`${label(path)}:${i + 1}：引用了节点 ${m[0]}，但本文没有定义 {#${m[1]}}`);
        }
      }
    });
  };

  const checkPlaybookSkillRefs = (path) => {
    const text = readFileSync(path, "utf8");
    const key = basename(path, ".md");
    const declaring = [...agents.entries()].filter(([, a]) => a.playbooks.has(key)).map(([k]) => k);
    for (const m of text.matchAll(SKILL_REF)) {
      const name = m[1];
      if (!shared.has(name) && !allOwnKeys.has(name) && !allExternalKeys.has(name)) {
        problems.push(`${label(path)}：引用的技能 "${name}" 不存在（作业法的节点与实现之间就断在这里）`);
        continue;
      }
      if (declaring.length === 0) continue; // 没有持有者的作业法由框架的 --check 报
      const mounted = declaring.some((agent) => {
        const a = agents.get(agent);
        return a.own.has(name) || a.declared.has(name) || a.external.some((e) => e.key === name);
      });
      if (!mounted) {
        problems.push(
          `${label(path)}：引用的技能 "${name}" 没有挂载给任何声明这门作业法的能力（${declaring.join("、")}）——` +
            `派这种作业时模型拿不到实现，把这个技能加进对应 skills.yaml`,
        );
      }
    }
  };

  // 作业法与项目约定是同一类散文面：从仓库手册搬出去的事实会落在这里，不扫等于让
  // 「一个事实只许有一份」在最容易长第二份的地方失效（ADR 0049）。
  // 作业法里的 `[[skill:]]` 也要校验（ADR 0050）：它是节点与实现之间的唯一连接，
  // 引用写错时没有任何运行时报错——模型只会拿不到方法照自己的理解干。
  for (const file of listFiles(join(capRoot, "playbooks"))) {
    const p = join(capRoot, "playbooks", file);
    scanDoc(p);
    scanEnums(p);
    checkPlaybookSkillRefs(p);
    checkPlaybookAnchors(p);
  }
  for (const file of listFiles(join(capRoot, "projects"))) {
    if (!file.endsWith(".instructions.md")) continue;
    const p = join(capRoot, "projects", file);
    scanDoc(p);
    scanEnums(p);
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

// ---------- 在途差集：平台已指派、哨兵却看不见 ----------

// INFLIGHT_TERMINAL 是「不该再被跟踪」的平台状态类别：completed / canceled /
// duplicate / backlog（最后两个是用户有意搁置，与 sentinel 的 parkedTask 同口径）。
const INFLIGHT_TERMINAL = /^(completed|canceled|cancelled|duplicate|backlog)$/i;

// loadInflightTaskIDs 读在途清单里的平台任务号。清单是运行时状态（state_dir），
// 不是本仓文件：路径从实例配置解析，读不到就返回 null 让调用方明说跳过——把
// 「读不到清单」和「清单是空的」混为一谈，差集就会把所有在途单当漏登。
export function loadInflightTaskIDs(configPath) {
  if (!configPath) return null;
  let stateDir = "";
  try {
    stateDir = execFileSync("yq", ["-p=toml", "-o=json", ".state_dir"], {
      input: readFileSync(configPath),
      encoding: "utf8",
    }).trim();
    stateDir = JSON.parse(stateDir);
  } catch {
    return null;
  }
  if (typeof stateDir !== "string" || !stateDir) return null;
  const expanded = stateDir.replace(/^~(?=\/|$)/, process.env.HOME || "~");
  const file = join(expanded, "inflight.json");
  if (!existsSync(file)) return null;
  try {
    const entries = JSON.parse(readFileSync(file, "utf8") || "[]");
    if (!Array.isArray(entries)) return null;
    // kind 缺失或为 cloud 的旧条目也是平台条目（框架 Load 的归一规则）：这里只认
    // task_id 非空，别因为 kind 写法旧就把它算成漏登。
    return new Set(entries.map((e) => e?.task_id).filter((t) => typeof t === "string" && t));
  } catch {
    return null;
  }
}

// inflightDiff 是在 live 检查里补的那条规则：平台侧 delegateStatus=assigned 且未完结的
// 单，若既不在在途清单、也没登记进 untracked.yaml，哨兵就永远看不见它——停滞、拒执、
// 疑似卡死三类事件都不会报。实测 HF-16321/16322/16324/16325 四张单这样静默了十小时。
//
// 作用域按「承接人是我们部署的 agent」收窄（delegate.id ∈ 部署记录）：组织里有几百张
// 指派给别人的单，全量报错只会被人忽略。**父单也纳入**：HF-16321 自己 delegate 为空，
// 但它是 16322/16324/16325 的父单且同样 assigned+未完结——只按 delegate 收窄会漏掉
// 事故四张里的一张。纯函数，注入版便于测试。
export function inflightDiff({ tasks, inflight, untracked, deployedAgentIDs }) {
  const problems = [];
  if (inflight === null) {
    return ["在途差集：读不到实例配置或在途清单（--config 指 superctl.toml，或运行时未注册），跳过本轮差集检查"];
  }
  const untrackedSet = new Set(untracked.map(String));
  const ours = new Set(deployedAgentIDs.map(String));
  const open = (t) => String(t.delegateStatus || "") === "assigned"
    && !INFLIGHT_TERMINAL.test(String(t.status?.category || t.category || "").toLowerCase());
  const delegatedToUs = (t) => ours.size > 0 && ours.has(String(t.delegate?.id || t.delegate_id || ""));
  const leaks = new Set();
  const consider = (t) => {
    const id = String(t.identifier || t.task_id || "");
    if (!id || inflight.has(id) || untrackedSet.has(id)) return;
    leaks.add(id);
  };

  // 父单先收：它们不挂在任何承接人身上，只能从子单反查。
  const parentIDs = new Set();
  for (const t of tasks) {
    if (!open(t) || !delegatedToUs(t)) continue;
    if (t.parentTaskId) parentIDs.add(String(t.parentTaskId));
  }
  for (const t of tasks) {
    if (!open(t)) continue;
    if (delegatedToUs(t) || parentIDs.has(String(t.id || ""))) consider(t);
  }

  if (leaks.size > 0) {
    const list = [...leaks].sort();
    problems.push(
      `平台已指派但不在在途清单（哨兵看不见，停滞/拒执/卡死都不会报）：${list.join("、")}` +
        `——修法：补登记进在途清单，或写进 super-agent/untracked.yaml 的 tasks（有意不跟踪）`,
    );
  }
  return problems;
}

// checkLiveWith 是注入版（测试用）：io.listOrgs / listSpaces / listAgents /
// listTasks(orgID) / inflightIDs / untracked。
export function checkLiveWith({ platforms, environments, untracked = [] }, io) {
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

  // 在途差集按组织各查一遍：平台侧「已指派且未完结」的单必须进哨兵视野。
  // 部署记录里的 id 就是「我们部署的承接人」——差集只对它们收窄，免得上百张派给
  // 别人的单把这条规则淹成噪声。
  if (io.listTasks && io.inflightIDs) {
    const deployedAgentIDs = [...new Set(environments.flatMap((e) => Object.values(e.agents || {}).map((a) => a.id)))];
    const orgIDs = [...new Set(environments.filter((e) => e.platform === "hifox").map((e) => {
      const org = orgs.find((o) => o.slug === e.organization || o.id === e.organization);
      return org?.id;
    }).filter(Boolean))];
    for (const orgID of orgIDs) {
      let tasks;
      try {
        tasks = io.listTasks(orgID);
      } catch (err) {
        problems.push(`org ${orgID}：task list 失败，在途差集未核（${err.message}）`);
        continue;
      }
      problems.push(...inflightDiff({
        tasks,
        inflight: io.inflightIDs(),
        untracked,
        deployedAgentIDs,
      }));
    }
  }
  return problems;
}

// checkLive 用真实 hifox CLI 取平台真值。
export function checkLive(data) {
  return checkLiveWith(data, {
    listOrgs: () => hifoxJson(["organization", "list"]),
    listSpaces: () => hifoxJson(["space", "list"]),
    listAgents: (org) => hifoxJson(["agent", "list", "--include-archived"], org),
    // 全组织的单一次拉：按 Space 拉会漏掉部署在别的 Space 的能力，按承接人拉
    // 则是 N 个 id × 一次 CLI（实测 25 个 agent 要 40 秒）。组织内全量约 3 秒。
    listTasks: (org) => hifoxJson(["task", "list", "--limit", "10000"], org),
    inflightIDs: () => loadInflightTaskIDs(findInstanceConfig()),
  });
}

// stateDirOf 从实例配置读 state_dir（与 loadInflightTaskIDs 同一套展开规则）。
function stateDirOf(configPath) {
  try {
    const raw = execFileSync("yq", ["-p=toml", "-o=json", ".state_dir", configPath], { encoding: "utf8" }).trim();
    const parsed = JSON.parse(raw);
    if (typeof parsed !== "string" || !parsed) return "";
    return parsed.replace(/^~(?=\/|$)/, process.env.HOME || "~");
  } catch {
    return "";
  }
}

// findInstanceConfig 定位实例配置（差集要读 state_dir）。顺序与框架一致：
// $SUPERAGENT_CONFIG > ~/.config/super-agent/superctl.toml。
function findInstanceConfig() {
  const explicit = process.env.SUPERAGENT_CONFIG;
  if (explicit && existsSync(explicit)) return explicit;
  const home = process.env.HOME || "";
  const fallback = join(home, ".config", "super-agent", "superctl.toml");
  return existsSync(fallback) ? fallback : "";
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

// GIT_PROBE 与框架 internal/gitprobe 的脚本一致（ADR 0043）：只读，不 fetch、不 checkout。
// 跨语言重复是有意的：实例检查器零依赖（只要求 node + yq + git），不能 import Go 包；
// 两处改一处时另一处必须同改，格式由 parseGitProbe 的用例钉住。
// status 不留 `|| true`：读不出工作树状态（index.lock、坏索引）必须让探针失败，不能
// 当成「干净」——脏提示会变成假阴性。
const GIT_PROBE = `set -e
d=$1
if [ ! -e "$d" ]; then echo exists=0; exit 0; fi
echo exists=1
if ! git -C "$d" rev-parse --is-inside-work-tree >/dev/null 2>&1; then echo worktree=0; exit 0; fi
echo worktree=1
printf 'remote=%s\\n' "$(git -C "$d" config --get remote.origin.url 2>/dev/null || true)"
printf 'branch=%s\\n' "$(git -C "$d" symbolic-ref --short -q HEAD 2>/dev/null || echo detached)"
echo --
git -C "$d" status --porcelain=v1
`;

function parseGitProbe(out) {
  const st = { exists: false, worktree: false, remote: "", branch: "", dirty: [] };
  const lines = String(out).split("\n");
  let i = 0;
  for (; i < lines.length; i++) {
    const line = lines[i].replace(/\r$/, "");
    if (line === "--") { i += 1; break; }
    const at = line.indexOf("=");
    if (at < 0) continue;
    const key = line.slice(0, at), value = line.slice(at + 1);
    if (key === "exists") st.exists = value === "1";
    else if (key === "worktree") st.worktree = value === "1";
    else if (key === "remote") st.remote = value;
    else if (key === "branch") st.branch = value;
  }
  for (; i < lines.length; i++) {
    const line = lines[i].replace(/\r$/, "");
    if (line.trim()) st.dirty.push(line);
  }
  return st;
}

function normalizeRepo(url) {
  return String(url || "").trim().replace(/\/$/, "").replace(/\.git$/, "");
}

function matchesRepo(project, url) {
  const want = normalizeRepo(url);
  if (!want) return false;
  if (normalizeRepo(project.repo) === want) return true;
  return (project.aliases || []).some((a) => normalizeRepo(a) === want);
}

// checkCheckoutsLive 用固定只读探针逐台核检出（ADR 0043）：路径存在、是工作副本、
// remote 命中项目 repo/aliases；脏只报告不拦（原地写的脏门禁在派活时由运行时判）。
// sshFn 是测试缝；本机节点直跑本地探针，不走 ssh。
export function checkCheckoutsLive(projects, checkouts, nodes, { identity, runLocal, runRemote } = {}) {
  const problems = [];
  const byKey = new Map((projects || []).map((p) => [p.key, p]));
  const selfId = identity === undefined ? localIdentity() : identity;
  const local = runLocal || ((script, arg) => execFileSync("sh", ["-c", script, "probe", arg], { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }));
  const remote = runRemote || ((target, script, arg) => execFileSync("ssh", [
    "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "--", target, "sh -c " + shellQuote(script) + " probe " + shellQuote(arg),
  ], { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }));
  for (const c of checkouts || []) {
    const project = byKey.get(c.project);
    if (!project) continue; // 未声明的引用已由静态检查报过
    if (!c.path || !c.path.startsWith("/")) continue; // 相对路径由框架 Check 报
    const node = (nodes || []).find((n) => n.key === c.node);
    // 只有目录没有 <key>.yaml：引用本身已由 checkProjects 报「项目没有声明」的反面
    // （这里反过来，是检出挂在一个不存在的节点上），别让 node.ssh 抛成探针失败。
    if (!node) {
      problems.push(`nodes/${c.node}/checkouts.yaml：目录存在，但 nodes/${c.node}.yaml 没有声明（检出表挂在未声明的节点上）`);
      continue;
    }
    const isSelf = isSelfNode(node, selfId);
    if (!isSelf && !node.ssh) continue;
    let out;
    try {
      out = isSelf ? local(GIT_PROBE, c.path) : remote(node.ssh, GIT_PROBE, c.path);
    } catch (err) {
      // ssh 自身失败（255）是「节点不可达」：ADR 0043 说跳过，可达性由 checkNodesLive 报。
      if (!isSelf && err.status === 255) {
        console.error(`提示：节点 ${c.node} 不可达，跳过 ${c.project} 的检出探针`);
        continue;
      }
      const detail = String(err.stderr || err.message || "").trim().split("\n")[0];
      problems.push(`nodes/${c.node}/checkouts.yaml：${c.project} 检出探针失败（${detail}）`);
      continue;
    }
    const st = parseGitProbe(out);
    const label = `nodes/${c.node}/checkouts.yaml（${c.project}）`;
    if (!st.exists) {
      problems.push(`${label}：路径 ${c.path} 不存在（这台机器还没拉这个仓库）`);
      continue;
    }
    if (!project.repo) continue; // 目录型项目只需存在
    if (!st.worktree) {
      problems.push(`${label}：${c.path} 不是 git 工作副本，与项目声明的 repo 不符`);
      continue;
    }
    if (!matchesRepo(project, st.remote)) {
      problems.push(`${label}：${c.path} 的 origin 是 ${JSON.stringify(st.remote)}，与项目 ${c.project} 的 repo/aliases 都不匹配`);
    }
    if (st.dirty.length > 0) {
      console.error(`提示：${label} 有 ${st.dirty.length} 项未提交改动（原地写会在派活时要求 --allow-dirty）`);
    }
  }
  return problems;
}

function shellQuote(s) {
  return "'" + String(s).replace(/'/g, `'\\''`) + "'";
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
  problems.push(...checkProjects(data.projects, data.checkouts, data.capabilityAgents));
  problems.push(...checkCapabilityDocs(root));
  const instanceConfig = findInstanceConfig();
  const stateDir = instanceConfig ? stateDirOf(instanceConfig) : "";
  problems.push(...checkLessons(stateDir));
  if (stateDir) {
    const note = findingsJournalNote(stateDir);
    if (note) console.error(note);
  }
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
  if (args.has("--live")) {
    problems.push(...checkLive(data));
    problems.push(...checkCheckoutsLive(data.projects, data.checkouts, data.nodes));
  }

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



// ---------- 记忆面（ADR 0048） ----------

// checkLessons 校验经验文件的形态与上限。超上限必须报红（写入侧要合并或晋升，
// 不能靠读侧截断）；行格式坏只提示，不阻断——经验是旁路增强，不是运行前提。
export function checkLessons(stateDir) {
  const problems = [];
  if (!stateDir) return problems;
  const path = join(stateDir, "lessons.md");
  if (!existsSync(path)) return problems; // 还没有经验 = 正常（首次运行）
  let text;
  try {
    text = readFileSync(path, "utf8");
  } catch (err) {
    return [`经验文件：读不到 ${path}（${err.message}）`];
  }
  let scope = "";
  let count = 0;
  text.split("\n").forEach((line, i) => {
    const at = `${path}:${i + 1}`;
    if (line.startsWith("## ")) {
      scope = line.slice(3).trim();
      if (!LESSON_SCOPE_RE.test(scope)) {
        problems.push(`经验文件：${at} 小节「${scope}」不在三段闭集（全局 / 仓库/<名> / agent/<key>）`);
      }
      return;
    }
    if (!line.startsWith("- ")) return;
    count++;
    if (!scope) problems.push(`经验文件：${at} 条目出现在任何小节之前`);
    if (!LESSON_LINE_RE.test(line)) {
      problems.push(`经验文件：${at} 格式应为 - [已验证|待确认] (YYYY-MM-DD) 正文 — 证据：…`);
      return;
    }
    // 形状对不代表日期有效、正文非空：Go 读侧会整行丢弃这两种行（lessons.parseLine），
    // 只查形状会让 `npm run check` 绿灯、prompt 里却永远没有这条。
    const date = line.slice(line.indexOf("(") + 1, line.indexOf(")"));
    const [y, m, d] = date.split("-").map(Number);
    const round = new Date(Date.UTC(y, m - 1, d));
    if (round.getUTCFullYear() !== y || round.getUTCMonth() !== m - 1 || round.getUTCDate() !== d) {
      problems.push(`经验文件：${at} 日期 ${date} 不是合法日历日期`);
    }
    if (line.slice(line.indexOf(")") + 1).trim() === "") {
      problems.push(`经验文件：${at} 正文为空`);
    }
    // ADR 0048 的格式是「… — 证据：…」：没有证据的经验无法被推翻，也没法判断多旧。
    // 读侧不校验它（缺了照样注入），所以只有这里能拦住——宁可报红也不要积累无主断言。
    if (!/—\s*证据：\S/.test(line)) {
      problems.push(`经验文件：${at} 缺少「— 证据：…」`);
    }
  });
  if (count > 100) problems.push(`经验文件：${count} 条超过上限 100，先合并或晋升`);
  return problems;
}

// findingsJournalNote 只做容量提示：流水是旁路历史，不参与运行判定，所以超量是
// 提示而不是红——但超到没人会读的时候，就该归档了。
export function findingsJournalNote(stateDir) {
  if (!stateDir) return "";
  const path = join(stateDir, "findings-journal.jsonl");
  let size = 0;
  try {
    size = statSync(path).size;
  } catch {
    return "";
  }
  const limit = 2 << 20; // 2 MiB：约一万条消解记录
  if (size <= limit) return "";
  return `提示：发现流水 ${path} 已 ${(size / 1024 / 1024).toFixed(1)} MiB，考虑归档（check 不自动删）`;
}
