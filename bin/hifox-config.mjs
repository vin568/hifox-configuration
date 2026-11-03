#!/usr/bin/env node

import { execFileSync, spawnSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, isAbsolute, join, relative, resolve, sep } from "node:path";

const root = resolve(dirname(new URL(import.meta.url).pathname), "..");
const argv = process.argv.slice(2);
const command = argv.shift() || "help";

function option(name, fallback) {
  const index = argv.indexOf(name);
  return index === -1 ? fallback : argv[index + 1];
}

function has(name) {
  return argv.includes(name);
}

const environmentName = option("--environment", "staging");
const shouldApply = has("--apply");
const explicitProduction = has("--production");
const explicitSuffix = option("--suffix", undefined);
const backupOutputDirectory = option("--output-dir", join(root, "backups"));

function yaml(path, optional = false) {
  if (optional && !existsSync(path)) return {};
  const output = execFileSync("yq", ["-o=json", ".", path], { encoding: "utf8" });
  return JSON.parse(output);
}

function read(path) {
  return readFileSync(path, "utf8");
}

function hifox(args, { mutate = false } = {}) {
  console.log(`$ hifox ${args.map(shellDisplay).join(" ")}`);
  if (mutate && !shouldApply) return null;
  const result = spawnSync("hifox", args, { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
  if (result.status !== 0) {
    throw new Error(result.stderr || result.stdout || `hifox exited with ${result.status}`);
  }
  return result.stdout.trim() ? JSON.parse(result.stdout) : null;
}

function shellDisplay(value) {
  if (value.includes("\n")) return `<multiline:${value.length}-chars>`;
  return /^[A-Za-z0-9_./:@-]+$/.test(value) ? value : JSON.stringify(value);
}

function bool(value) {
  return value === true ? "enabled" : value === false ? "disabled" : undefined;
}

function boolLiteral(value) {
  return value === true ? "true" : value === false ? "false" : undefined;
}

function add(args, flag, value) {
  if (value !== undefined && value !== null) args.push(flag, String(value));
}

function visibilityFor(kind, resource, context) {
  return context.env.visibility?.[kind] ?? resource.visibility ?? "space";
}

function loadConfiguration() {
  const environmentPath = join(root, "environments", environmentName);
  const env = yaml(join(environmentPath, "environment.yaml"));
  const statePath = join(environmentPath, "state.yaml");
  const state = yaml(statePath, true);
  const configBase = join(root, "config");
  const configRoot = resolve(root, env.configRoot || "config");
  const configRelative = relative(configBase, configRoot);
  if (configRelative === ".." || configRelative.startsWith(`..${sep}`) || isAbsolute(configRelative)) {
    throw new Error(`${environmentName}: configRoot must be inside config/`);
  }
  const crewsRoot = join(configRoot, "crews");
  const legacyCrewPath = join(configRoot, "crew.yaml");
  if (!existsSync(crewsRoot) && !existsSync(legacyCrewPath)) {
    throw new Error(`${environmentName}: configRoot has no crews/ or crew.yaml: ${configRelative || "."}`);
  }
  const crews = {};
  if (existsSync(crewsRoot)) {
    const crewDirs = readdirSync(crewsRoot, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name));
    for (const entry of crewDirs) {
      if (!entry.isDirectory()) continue;
      const key = entry.name;
      const dir = join(crewsRoot, key);
      const configPath = join(dir, "crew.yaml");
      if (!existsSync(configPath)) continue;
      const crew = yaml(configPath) || {};
      if (crew.key !== key) throw new Error(`${configPath}: key must be "${key}"`);
      crew.__dir = dir;
      crews[key] = crew;
    }
  } else {
    const crew = yaml(legacyCrewPath) || {};
    if (crew.key) {
      crew.__dir = configRoot;
      crews[crew.key] = crew;
    }
  }
  const agents = {};
  const agentsRoot = join(configRoot, "agents");
  for (const key of readdirSync(agentsRoot).sort()) {
    const configPath = join(agentsRoot, key, "agent.yaml");
    if (!existsSync(configPath)) continue;
    const agent = yaml(configPath);
    if (agent.key !== key) throw new Error(`${configPath}: key must be "${key}"`);
    agents[key] = agent;
  }
  const skills = {};
  const skillsRoot = join(configRoot, "skills");
  if (existsSync(skillsRoot)) {
    for (const key of readdirSync(skillsRoot).sort()) {
      const configPath = join(skillsRoot, key, "skill.yaml");
      if (!existsSync(configPath)) continue;
      const skill = yaml(configPath);
      if (skill.key !== key) throw new Error(`${configPath}: key must be "${key}"`);
      skills[key] = skill;
    }
  }
  const suffix = explicitSuffix ?? state.nameSuffix ?? env.nameSuffix ?? "";
  const pattern = env.namingPattern || "{{name}}{{suffix}}";
  const names = Object.fromEntries(
    Object.entries(agents).map(([key, agent]) => [key, renderName(pattern, agent.name, suffix, environmentName)]),
  );
  const crewNames = Object.fromEntries(
    Object.entries(crews).map(([key, crew]) => [
      key,
      crew.name ? renderName(pattern, crew.name, suffix, environmentName) : "",
    ]),
  );
  const crewName = Object.keys(crewNames).length === 1 ? Object.values(crewNames)[0] : undefined;
  const skillNames = Object.fromEntries(
    Object.entries(skills).map(([key, skill]) => [
      key,
      renderName(skill.namingPattern || pattern, skill.name, suffix, environmentName),
    ]),
  );
  return { env, state, statePath, crews, crewNames, crewName, agents, skills, names, skillNames, suffix, configRoot };
}

function renderName(pattern, name, suffix, environment) {
  return pattern
    .replaceAll("{{name}}", name)
    .replaceAll("{{suffix}}", suffix)
    .replaceAll("{{environment}}", environment);
}

function renderInstructions(path, context, crewKey) {
  let value = read(path);
  for (const [key, name] of Object.entries(context.names)) {
    value = value.replaceAll(`{{agents.${key}.name}}`, name);
  }
  for (const [key, name] of Object.entries(context.crewNames || {})) {
    value = value.replaceAll(`{{crews.${key}.name}}`, name);
  }
  if (value.includes("{{crew.name}}")) {
    const crewName = crewKey ? context.crewNames?.[crewKey] : context.crewName;
    if (crewName === undefined) {
      throw new Error(`${path}: {{crew.name}} is ambiguous with multiple crews; use {{crews.<key>.name}}`);
    }
    value = value.replaceAll("{{crew.name}}", crewName);
  }
  value = value.replaceAll("{{environment}}", environmentName);
  const unresolved = value.match(/{{[^}]+}}/g);
  if (unresolved) throw new Error(`${path}: unresolved variables: ${[...new Set(unresolved)].join(", ")}`);
  return value;
}

function crewInstructionsPath(key, context) {
  const crew = context.crews[key];
  return join(crew.__dir || context.configRoot, crew.instructionsFile || "");
}

function validate(context) {
  const errors = [];
  const crewEntries = Object.entries(context.crews);
  if (!crewEntries.length) errors.push("at least one crew is required");
  for (const [key, crew] of crewEntries) {
    if (!crew.key || !crew.name || !crew.leader) errors.push(`${key}: crew key, name and leader are required`);
    if (crew.leader && !context.agents[crew.leader]) errors.push(`${key}: unknown crew leader: ${crew.leader}`);
    const memberKeys = new Set();
    for (const member of crew.members || []) {
      if (!context.agents[member.agent]) errors.push(`${key}: unknown crew member: ${member.agent}`);
      if (memberKeys.has(member.agent)) errors.push(`${key}: duplicate crew member: ${member.agent}`);
      memberKeys.add(member.agent);
    }
    if (!["personal", "space"].includes(visibilityFor("crew", crew, context))) {
      errors.push(`${key}: crew visibility must be personal or space`);
    }
    if (!crew.instructionsFile) errors.push(`${key}: crew instructionsFile is required`);
    else {
      const path = crewInstructionsPath(key, context);
      if (!existsSync(path)) errors.push(`${key}: crew instructionsFile does not exist`);
      else renderInstructions(path, context, key);
    }
  }
  for (const [key, agent] of Object.entries(context.agents)) {
    if (!agent.name) errors.push(`${key}: name is required`);
    if (!agent.instructionsFile) errors.push(`${key}: instructionsFile is required`);
    else renderInstructions(join(context.configRoot, "agents", key, agent.instructionsFile), context);
    if (!["personal", "space"].includes(visibilityFor("agents", agent, context))) {
      errors.push(`${key}: visibility must be personal or space`);
    }
  }
  for (const [key, skill] of Object.entries(context.skills)) {
    if (!skill.name) errors.push(`${key}: skill name is required`);
    if (!skill.contentFile || !existsSync(join(context.configRoot, "skills", key, skill.contentFile))) {
      errors.push(`${key}: skill contentFile does not exist`);
    }
    for (const agentKey of skill.assignTo || []) {
      if (!context.agents[agentKey]) errors.push(`${key}: unknown assigned Agent ${agentKey}`);
    }
  }
  if (Boolean(context.env.target?.computer) !== Boolean(context.env.target?.runtime)) {
    errors.push("environment target computer and runtime must be configured together");
  }
  if (context.env.ignoreAgentModels !== undefined && typeof context.env.ignoreAgentModels !== "boolean") {
    errors.push("environment ignoreAgentModels must be a boolean");
  }
  const deployedNames = [...Object.values(context.crewNames), ...Object.values(context.names)];
  if (new Set(deployedNames).size !== deployedNames.length) errors.push("rendered crew and agent names must be unique");
  if (errors.length) throw new Error(errors.join("\n"));
  const crewCount = crewEntries.length;
  console.log(`Valid: ${crewCount} crew${crewCount === 1 ? "" : "s"}, ${Object.keys(context.agents).length} agents, suffix=${JSON.stringify(context.suffix)}`);
}

function agentFlags(key, context, create) {
  const agent = context.agents[key];
  const args = [create ? "create" : "edit"];
  if (!create) args.push(context.state.agents[key].id);
  add(args, "--name", context.names[key]);
  add(args, "--job-title", agent.jobTitle);
  add(args, "--description", agent.description ?? "");
  add(args, "--instructions", renderInstructions(join(context.configRoot, "agents", key, agent.instructionsFile), context));
  add(args, "--visibility", visibilityFor("agents", agent, context));
  add(args, "--space", context.env.spaceId);
  add(args, "--computer", agent.target?.computer ?? context.env.target?.computer);
  add(args, "--runtime", agent.target?.runtime ?? context.env.target?.runtime);
  if (!context.env.ignoreAgentModels) add(args, "--model", agent.runtime?.model);
  add(args, "--thinking-effort", agent.runtime?.thinkingEffort);
  add(args, "--concurrency-setting-mode", agent.concurrency?.mode);
  add(args, "--max-concurrent-tasks", agent.concurrency?.maxTasks);
  add(args, "--task-assignment", bool(agent.permissions?.taskAssignment));
  add(args, "--direct-message", bool(agent.permissions?.directMessage));
  add(args, "--crew-membership", bool(agent.permissions?.crewMembership));
  add(args, "--mention", bool(agent.permissions?.mention));
  add(args, "--task-working-directory-mode", agent.taskWorkingDirectory?.mode);
  for (const path of agent.taskWorkingDirectory?.paths || []) add(args, "--task-working-directory", path);
  for (const customArg of agent.runtime?.customArguments || []) add(args, "--custom-argument", customArg);
  args.push("--output", "json");
  return ["agent", ...args];
}

function crewFlags(key, context, create) {
  const crew = context.crews[key];
  const args = [create ? "create" : "edit"];
  if (!create) args.push(context.state.crew[key].id);
  add(args, "--name", context.crewNames[key]);
  add(args, "--description", crew.description ?? "");
  add(args, "--instructions", renderInstructions(crewInstructionsPath(key, context), context, key));
  add(args, "--leader", context.state.agents[crew.leader].id);
  add(args, "--visibility", visibilityFor("crew", crew, context));
  add(args, "--space", context.env.spaceId);
  add(args, "--same-computer-shared-task-workdir-enabled", boolLiteral(crew.sameComputerSharedTaskWorkdirEnabled));
  args.push("--output", "json");
  return ["crew", ...args];
}

function stateYaml(context) {
  const lines = ["version: 1", `nameSuffix: ${JSON.stringify(context.suffix)}`, "crew:"];
  for (const [key, value] of Object.entries(context.state.crew || {})) {
    lines.push(`  ${key}:`, `    id: ${value.id}`, `    deployedName: ${JSON.stringify(value.deployedName)}`);
  }
  lines.push("agents:");
  for (const [key, value] of Object.entries(context.state.agents || {})) {
    lines.push(`  ${key}:`, `    id: ${value.id}`, `    deployedName: ${JSON.stringify(value.deployedName)}`);
  }
  lines.push("skills:");
  for (const [key, value] of Object.entries(context.state.skills || {})) {
    lines.push(`  ${key}:`, `    id: ${value.id}`, `    deployedName: ${JSON.stringify(value.deployedName)}`);
  }
  return lines.join("\n") + "\n";
}

function saveState(context) {
  mkdirSync(dirname(context.statePath), { recursive: true });
  writeFileSync(context.statePath, stateYaml(context));
}

function assertCreateState(context) {
  if (Object.values(context.state.crew || {}).some((crew) => crew?.id)) {
    throw new Error(`environment "${environmentName}" already has a deployed crew; use apply or a new environment`);
  }
  if (context.state.nameSuffix !== undefined && context.state.nameSuffix !== context.suffix) {
    throw new Error(`partial deployment suffix is ${JSON.stringify(context.state.nameSuffix)}, not ${JSON.stringify(context.suffix)}`);
  }
  if (!context.suffix) throw new Error("creating a clone requires a non-empty --suffix or environment nameSuffix");
}

function checkRemoteNameCollisions(context) {
  const remoteAgents = hifox(["agent", "list", "--include-archived", "--output", "json"]) || [];
  const remoteCrews = hifox(["crew", "list", "--output", "json"]) || [];
  const managedAgentIds = new Set(Object.values(context.state.agents || {}).map((item) => item.id));
  const agentNames = new Set(remoteAgents.filter((item) => !managedAgentIds.has(item.id)).map((item) => item.name));
  const remoteCrewNames = new Set(remoteCrews.map((item) => item.name));
  const collisions = Object.values(context.names).filter((name) => agentNames.has(name));
  for (const name of Object.values(context.crewNames)) {
    if (remoteCrewNames.has(name)) collisions.push(name);
  }
  if (collisions.length) throw new Error(`remote names already exist: ${collisions.join(", ")}`);
}

function skillFlags(key, context, create) {
  const skill = context.skills[key];
  const args = ["skill", create ? "create" : "edit"];
  if (!create) args.push(context.state.skills[key].id);
  if (create) add(args, "--space", context.env.spaceId);
  add(args, "--name", context.skillNames[key]);
  add(args, "--description", skill.description ?? "");
  args.push(`--content=${read(join(context.configRoot, "skills", key, skill.contentFile))}`);
  args.push(`--config=${JSON.stringify(skill.config || {})}`);
  args.push("--output", "json");
  return args;
}

function ensureSkills(context) {
  context.state.skills ||= {};
  for (const key of Object.keys(context.skills)) {
    const skill = context.skills[key];
    if (skill.binding?.mode === "inherited-copy" && !context.state.skills[key]?.id) {
      const sourceState = yaml(join(root, "environments", skill.binding.sourceEnvironment, "state.yaml"));
      const sourceSkill = sourceState.skills?.[key];
      if (!sourceSkill?.id) throw new Error(`${key}: missing source Skill state`);
      context.state.skills[key] = { id: sourceSkill.id, deployedName: context.skillNames[key] };
      if (shouldApply) saveState(context);
    }
    const existing = context.state.skills[key];
    const result = hifox(skillFlags(key, context, !existing?.id), { mutate: true });
    if (shouldApply && !existing?.id) {
      context.state.skills[key] = { id: result.id, deployedName: context.skillNames[key] };
      saveState(context);
    } else if (shouldApply) {
      existing.deployedName = context.skillNames[key];
    }
  }
}

function setSkills(key, context) {
  const hasInheritedBinding = Object.values(context.skills).some(
    (skill) => skill.binding?.mode === "inherited-copy" && (skill.assignTo || []).includes(key),
  );
  if (hasInheritedBinding) {
    console.log(`Skip skill-set for ${key}: inherited-copy binding is managed by agent copy`);
    return;
  }
  const ids = Object.entries(context.skills)
    .filter(([, skill]) => skill.binding?.mode !== "inherited-copy" && (skill.assignTo || []).includes(key))
    .map(([skillKey]) => context.state.skills?.[skillKey]?.id)
    .filter(Boolean);
  hifox(["agent", "skill", "set", context.state.agents[key].id, "--skill-ids", ids.join(","), "--output", "json"], { mutate: true });
}

function copySourceForAgent(key, context) {
  for (const skill of Object.values(context.skills)) {
    if (skill.binding?.mode !== "inherited-copy" || !(skill.assignTo || []).includes(key)) continue;
    const sourceState = yaml(join(root, "environments", skill.binding.sourceEnvironment, "state.yaml"));
    const sourceAgentKey = skill.binding.sourceAgent || key;
    const sourceAgent = sourceState.agents?.[sourceAgentKey];
    if (!sourceAgent?.id) throw new Error(`${key}: missing inherited-copy source Agent state`);
    return sourceAgent.id;
  }
  return null;
}

function addCrewMember(context, crewId, member) {
  const args = ["crew", "member", "add", crewId, "--type", "agent", "--id", context.state.agents[member.agent].id];
  add(args, "--role", member.role ?? "");
  args.push("--output", "json");
  hifox(args, { mutate: true });
}

function create(context) {
  assertCreateState(context);
  checkRemoteNameCollisions(context);
  context.state.version = 1;
  context.state.nameSuffix = context.suffix;
  context.state.agents ||= {};
  context.state.crew ||= {};
  for (const key of Object.keys(context.agents)) {
    if (context.state.agents[key]?.id) {
      console.log(`Skip existing partial Agent: ${key} (${context.state.agents[key].id})`);
      continue;
    }
    const copySource = copySourceForAgent(key, context);
    const result = copySource
      ? hifox(["agent", "copy", copySource, "--name", context.names[key]], { mutate: true })
      : hifox(agentFlags(key, context, true), { mutate: true });
    if (shouldApply) {
      context.state.agents[key] = { id: result.id, deployedName: context.names[key] };
      saveState(context);
      if (copySource) hifox(agentFlags(key, context, false), { mutate: true });
    }
  }
  if (!shouldApply) {
    for (const key of Object.keys(context.crews)) {
      console.log(`$ hifox crew create ... # ${key}`);
    }
    console.log("$ hifox crew member add ... # for each crew and non-leader member");
    return;
  }
  ensureSkills(context);
  for (const key of Object.keys(context.agents)) setSkills(key, context);
  for (const key of Object.keys(context.crews)) {
    const crew = context.crews[key];
    const crewResult = hifox(crewFlags(key, context, true), { mutate: true });
    context.state.crew[key] = { id: crewResult.id, deployedName: context.crewNames[key] };
    saveState(context);
    for (const member of crew.members || []) {
      if (member.agent === crew.leader) continue;
      addCrewMember(context, crewResult.id, member);
    }
  }
}

function requireCompleteAgents(context) {
  for (const key of Object.keys(context.agents)) {
    if (!context.state.agents?.[key]?.id) throw new Error(`missing state ID for agent ${key}`);
  }
}

function requireCompleteState(context) {
  requireCompleteAgents(context);
  for (const key of Object.keys(context.crews)) {
    if (!context.state.crew?.[key]?.id) throw new Error(`missing state ID for crew ${key}`);
  }
}

function apply(context) {
  context.state.agents ||= {};
  context.state.crew ||= {};
  context.state.version = 1;
  context.state.nameSuffix = context.suffix;
  for (const key of Object.keys(context.agents)) {
    if (context.state.agents[key]?.id) continue;
    const copySource = copySourceForAgent(key, context);
    const result = copySource
      ? hifox(["agent", "copy", copySource, "--name", context.names[key]], { mutate: true })
      : hifox(agentFlags(key, context, true), { mutate: true });
    if (shouldApply) {
      context.state.agents[key] = { id: result.id, deployedName: context.names[key] };
      saveState(context);
      if (copySource) hifox(agentFlags(key, context, false), { mutate: true });
    }
  }
  requireCompleteAgents(context);
  for (const key of Object.keys(context.agents)) {
    hifox(agentFlags(key, context, false), { mutate: true });
    context.state.agents[key].deployedName = context.names[key];
  }
  ensureSkills(context);
  for (const key of Object.keys(context.agents)) setSkills(key, context);
  for (const key of Object.keys(context.crews)) {
    if (!context.state.crew[key]?.id) {
      const result = hifox(crewFlags(key, context, true), { mutate: true });
      if (!shouldApply) {
        console.log(`$ hifox crew edit <${key} id> ... # after crew creation`);
        continue;
      }
      context.state.crew[key] = { id: result.id, deployedName: context.crewNames[key] };
      saveState(context);
    } else {
      hifox(crewFlags(key, context, false), { mutate: true });
      if (shouldApply) context.state.crew[key].deployedName = context.crewNames[key];
    }
  }
  context.state.nameSuffix = context.suffix;
  if (shouldApply) saveState(context);
  for (const key of Object.keys(context.state.crew || {})) {
    if (context.crews[key]) reconcileMembers(context, key);
  }
}

function reconcileMembers(context, key) {
  const crew = context.crews[key];
  const crewId = context.state.crew[key].id;
  const current = hifox(["crew", "member", "list", crewId, "--output", "json"]) || [];
  const byId = new Map(current.filter((x) => x.member.type === "agent").map((x) => [x.member.id, x]));
  const desiredIds = new Set();
  for (const member of crew.members || []) {
    const id = context.state.agents[member.agent].id;
    desiredIds.add(id);
    const found = byId.get(id);
    if (!found) {
      const args = ["crew", "member", "add", crewId, "--type", "agent", "--id", id];
      add(args, "--role", member.role ?? "");
      args.push("--output", "json");
      hifox(args, { mutate: true });
    } else if ((found.role || "") !== (member.role || "")) {
      hifox(["crew", "member", "role", crewId, "--type", "agent", "--id", id, "--role", member.role || "", "--output", "json"], { mutate: true });
    }
  }
  for (const found of current.filter((x) => x.member.type === "agent")) {
    if (!desiredIds.has(found.member.id)) {
      hifox(["crew", "member", "remove", crewId, "--type", "agent", "--id", found.member.id, "--output", "json"], { mutate: true });
    }
  }
}

function pull(context) {
  requireCompleteState(context);
  const snapshotRoot = join(root, "snapshots", environmentName);
  mkdirSync(join(snapshotRoot, "agents"), { recursive: true });
  mkdirSync(join(snapshotRoot, "skills"), { recursive: true });
  const crewsSnapshotRoot = join(snapshotRoot, "crews");
  mkdirSync(crewsSnapshotRoot, { recursive: true });
  const stateCrewKeys = Object.keys(context.state.crew || {});
  for (const key of stateCrewKeys) {
    const crewId = context.state.crew[key].id;
    const crew = hifox(["crew", "view", crewId, "--output", "json"]);
    const members = hifox(["crew", "member", "list", crewId, "--output", "json"]);
    const crewSnapshotRoot = join(crewsSnapshotRoot, key);
    mkdirSync(crewSnapshotRoot, { recursive: true });
    writeFileSync(join(crewSnapshotRoot, "crew.json"), JSON.stringify(crew, null, 2) + "\n");
    writeFileSync(join(crewSnapshotRoot, "members.json"), JSON.stringify(members, null, 2) + "\n");
    if (stateCrewKeys.length === 1) {
      writeFileSync(join(snapshotRoot, "crew.json"), JSON.stringify(crew, null, 2) + "\n");
      writeFileSync(join(snapshotRoot, "members.json"), JSON.stringify(members, null, 2) + "\n");
    }
  }
  for (const [key, value] of Object.entries(context.state.agents)) {
    const agent = hifox(["agent", "view", value.id, "--output", "json"]);
    writeFileSync(join(snapshotRoot, "agents", `${key}.json`), JSON.stringify(agent, null, 2) + "\n");
  }
  for (const [key, value] of Object.entries(context.state.skills || {})) {
    const skill = hifox(["skill", "view", value.id, "--output", "json"]);
    writeFileSync(join(snapshotRoot, "skills", `${key}.json`), JSON.stringify(skill, null, 2) + "\n");
  }
  return snapshotRoot;
}

function backup(context) {
  const snapshotRoot = pull(context);
  const timestamp = new Date().toISOString().replaceAll(":", "-").replace(/\.\d{3}Z$/, "Z");
  const backupRoot = resolve(backupOutputDirectory, timestamp, environmentName);
  mkdirSync(backupRoot, { recursive: true });
  cpSync(join(root, "config"), join(backupRoot, "config"), { recursive: true });
  cpSync(join(root, "environments", environmentName), join(backupRoot, "environment"), { recursive: true });
  cpSync(snapshotRoot, join(backupRoot, "snapshot"), { recursive: true });
  writeFileSync(
    join(backupRoot, "manifest.json"),
    JSON.stringify(
      {
        version: 1,
        createdAt: new Date().toISOString(),
        environment: environmentName,
        configRoot: relative(root, context.configRoot),
        organizationId: context.env.organizationId,
        spaceId: context.env.spaceId,
        crews: Object.fromEntries(Object.entries(context.state.crew || {}).map(([key, value]) => [key, value.id])),
        crewId: Object.keys(context.state.crew || {}).length === 1 ? Object.values(context.state.crew)[0].id : undefined,
      },
      null,
      2,
    ) + "\n",
  );
  console.log(`Backup created: ${backupRoot}`);
}

function help() {
  console.log(`Usage:
  hifox-config validate [--environment NAME] [--suffix TEXT]
  hifox-config create   --environment NAME --suffix TEXT [--apply]
  hifox-config clone    --environment NAME --suffix TEXT [--apply]
  hifox-config apply    [--environment NAME] [--suffix TEXT] [--apply] [--production]
  hifox-config diff     [--environment NAME] [--suffix TEXT]
  hifox-config pull     [--environment NAME]
  hifox-config backup   [--environment NAME] [--output-dir PATH]

Mutating commands are dry-run unless --apply is provided.
The default environment is staging.
create/clone require an environment without deployed IDs and a non-empty suffix.`);
}

try {
  if (command === "help" || command === "--help" || command === "-h") {
    help();
  } else {
    if (shouldApply && environmentName === "current" && !explicitProduction) {
      throw new Error("publishing current requires explicit --production; use staging by default");
    }
    const context = loadConfiguration();
    validate(context);
    if (command === "validate") {
      // Validation is complete.
    } else if (command === "create" || command === "clone") {
      create(context);
    } else if (command === "apply" || command === "diff") {
      apply(context);
    } else if (command === "pull") {
      pull(context);
    } else if (command === "backup") {
      backup(context);
    } else {
      throw new Error(`unknown command: ${command}`);
    }
  }
} catch (error) {
  console.error(`Error: ${error.message}`);
  process.exitCode = 1;
}
