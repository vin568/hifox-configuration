#!/usr/bin/env node
// Read-only collector for the nightly reflection. Emits one JSON document to stdout.
// Usage: node collect.mjs --window 2026-11-10 [--tier staging|prod]
import { execFileSync } from "node:child_process";

const MAX_BUFFER = 32 * 1024 * 1024;

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (!arg.startsWith("--")) continue;
    const key = arg.slice(2);
    const value = argv[i + 1] && !argv[i + 1].startsWith("--") ? argv[++i] : "true";
    out[key] = value;
  }
  return out;
}

function previousDayShanghai() {
  const d = new Date(Date.now() - 864e5);
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(d);
}

function currentMonthShanghai() {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "2-digit",
  })
    .format(new Date())
    .slice(0, 7);
}

function previousMonthShanghai() {
  const [year, month] = currentMonthShanghai().split("-").map(Number);
  return new Date(Date.UTC(year, month - 2, 1)).toISOString().slice(0, 7);
}

function nextDay(date) {
  const d = new Date(`${date}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + 1);
  return d.toISOString().slice(0, 10);
}

function sanitize(value) {
  if (value == null) return "";
  let text = typeof value === "string" ? value : JSON.stringify(value);
  text = text
    .replace(/(https?:\/\/)([^/\s:@]+):([^/\s@]+)@/g, "$1$2:***@")
    .replace(/\b(Bearer|token|secret|password|passwd|api[_-]?key|authorization)\b\s*[:=]\s*\S+/gi, "$1=***")
    .replace(/\b(?:sk|ghp|gho|xox[baprs])-[A-Za-z0-9_-]{10,}\b/g, "***")
    .replace(/\b(?:\d{1,3}\.){3}\d{1,3}\b/g, "<ip>")
    .replace(/(?:\/Users\/|\/home\/|\/tmp\/)[^\s"'`]+/g, "<path>");
  return text;
}

function truncate(text, limit) {
  const value = sanitize(text);
  return value.length > limit ? `${value.slice(0, limit)}…` : value;
}

function parseJson(text) {
  const trimmed = String(text || "").trim();
  if (!trimmed) return null;
  try {
    return JSON.parse(trimmed);
  } catch {
    return null;
  }
}

function cli(args, { allowFail = false } = {}) {
  try {
    const raw = execFileSync("hifox", args, { encoding: "utf8", maxBuffer: MAX_BUFFER, timeout: 30_000 });
    const parsed = parseJson(raw);
    return parsed == null ? raw : parsed;
  } catch (error) {
    if (allowFail) return { __error: sanitize(error.message) };
    throw error;
  }
}

function asArray(value, keys = []) {
  if (Array.isArray(value)) return value;
  for (const key of keys) {
    if (Array.isArray(value?.[key])) return value[key];
  }
  return [];
}

function isDoc(doc) {
  return doc?.kind === "doc" || doc?.kind === "document";
}

function docBaseName(name) {
  return String(name || "").replace(/\.md$/i, "");
}

const args = parseArgs(process.argv.slice(2));
const windowDate = args.window || previousDayShanghai();
const tier = args.tier || "staging";
const start = `${windowDate}T00:00:00+08:00`;
const end = `${nextDay(windowDate)}T00:00:00+08:00`;
const startMs = Date.parse(start);
const endMs = Date.parse(end);

function inWindow(value) {
  const ms = Date.parse(value || "");
  return Number.isFinite(ms) && ms >= startMs && ms < endMs;
}

const result = {
  meta: {
    window: windowDate,
    start,
    end,
    tier,
    generatedAt: new Date().toISOString(),
    coverage: {},
    warnings: [],
  },
  spaces: {},
  agents: [],
  runs: [],
  workflows: [],
  tasks: [],
  automations: [],
  ledger: { available: false, items: [] },
  improvements: { available: false, items: [] },
};

const partial = { agents: false, runs: false, workflows: false, tasks: false, automations: false, ledger: false, improvements: false };

function warn(message) {
  result.meta.warnings.push(sanitize(message));
}

// ---------- spaces ----------
const spacesRaw = cli(["space", "list", "--output", "json"], { allowFail: true });
if (spacesRaw?.__error) warn("spaces unavailable");
const spaces = asArray(spacesRaw);
for (const [key, name] of [
  ["hifox", "HiFox 产研"],
  ["apifox", "Apifox 产研"],
]) {
  const found = spaces.find((space) => space.name === name && !space.archivedAt);
  if (found) result.spaces[key] = { id: found.id, name: found.name };
  else warn(`space missing: ${name}`);
}

// ---------- agents ----------
const agentsRaw = cli(["agent", "list", "--output", "json"], { allowFail: true });
if (agentsRaw?.__error) {
  warn("agents unavailable");
  partial.agents = true;
}
const agents = asArray(agentsRaw).filter((agent) => !agent.archivedAt);
const team = [
  { base: "吴用", space: "hifox" },
  { base: "林冲", space: "hifox" },
  { base: "武松", space: "hifox" },
  { base: "戴宗", space: "hifox" },
  { base: "燕青", space: "apifox" },
  { base: "萧让", space: "apifox" },
  { base: "汤隆", space: "apifox" },
];
function expectedName(base, spaceKey) {
  if (tier === "staging") return spaceKey === "apifox" ? `${base} - staging` : `${base} (staging)`;
  return base;
}
for (const entry of team) {
  const spaceId = result.spaces[entry.space]?.id;
  const name = expectedName(entry.base, entry.space);
  const agent = agents.find((item) => item.name === name && item.spaceId === spaceId);
  if (!agent) {
    warn(`agent missing: ${name}`);
    partial.agents = true;
    continue;
  }
  result.agents.push({
    key: entry.base,
    name: agent.name,
    id: agent.id,
    space: entry.space,
    jobTitle: agent.jobTitle || "",
  });
}
result.meta.coverage.agents = result.agents.length ? (partial.agents ? "partial" : "ok") : "missing";

// ---------- runs ----------
for (const agent of result.agents) {
  const runsRaw = cli(
    ["run", "list", "--agent", agent.id, "--limit", "200", "--output", "json"],
    { allowFail: true },
  );
  if (runsRaw?.__error) {
    warn(`runs unavailable: ${agent.name}`);
    partial.runs = true;
    continue;
  }
  const runs = asArray(runsRaw);
  const filtered = runs.filter((run) => inWindow(run.createdAt));
  if (runs.length >= 200) {
    warn(`runs may be truncated: ${agent.name}`);
    partial.runs = true;
  }
  for (const run of filtered) {
    const trigger = run.automation ? "automation" : run.chatSessionId ? "chat" : run.taskId ? "task" : "other";
    if (trigger === "other") continue;
    result.runs.push({
      agent: agent.key,
      agentName: agent.name,
      id: run.id,
      status: run.status,
      createdAt: run.createdAt,
      startedAt: run.startedAt || null,
      completedAt: run.completedAt || null,
      trigger,
      taskId: run.taskId || null,
      chatSessionId: run.chatSessionId || null,
      automation: run.automation?.title || run.automation?.name || null,
      error: truncate(run.error || "", 300),
      waitReason: truncate(run.waitReason || "", 200),
      result: truncate(run.result?.output || run.result || "", 400),
    });
  }
}
result.meta.coverage.runs = result.runs.length ? (partial.runs ? "partial" : "ok") : (partial.runs ? "partial" : "empty");

// ---------- workflows ----------
const workflowDefs = [
  { space: "hifox", names: ["忠义堂", "x-deploy Release Gate"], suffix: " (staging)" },
  { space: "apifox", names: ["二龙山"], suffix: " - staging" },
];
for (const def of workflowDefs) {
  const space = result.spaces[def.space];
  if (!space) continue;
  const listRaw = cli(["workflow", "list", "--space", space.id, "--output", "json"], { allowFail: true });
  if (listRaw?.__error) {
    warn(`workflows unavailable: ${space.name}`);
    partial.workflows = true;
    continue;
  }
  const workflows = asArray(listRaw);
  for (const base of def.names) {
    const name = tier === "staging" ? `${base}${def.suffix}` : base;
    const workflow = workflows.find((item) => item.name === name);
    if (!workflow) {
      warn(`workflow missing: ${name}`);
      partial.workflows = true;
      continue;
    }
    const logsRaw = cli(
      [
        "workflow", "logs", "list",
        "--workflow", workflow.id,
        "--space", space.id,
        "--filter", JSON.stringify({ startDate: start }),
        "--limit", "200",
        "--output", "json",
      ],
      { allowFail: true },
    );
    if (logsRaw?.__error) {
      warn(`workflow logs unavailable: ${name}`);
      partial.workflows = true;
      continue;
    }
    const logs = asArray(logsRaw, ["data", "logs", "items"]);
    if (logsRaw?.nextCursor) {
      warn(`workflow logs may be truncated: ${name}`);
      partial.workflows = true;
    }
    if (logs.length >= 200) {
      warn(`workflow logs may be truncated: ${name}`);
      partial.workflows = true;
    }
    const filtered = logs.filter((log) => inWindow(log.createdAt));
    for (const log of filtered) {
      result.workflows.push({
        space: def.space,
        workflow: name,
        workflowId: workflow.id,
        executionId: log.executionId || log.id || null,
        status: log.status || null,
        createdAt: log.createdAt || null,
        error: truncate(log.error || log.message || "", 300),
      });
    }
  }
}
result.meta.coverage.workflows = result.workflows.length ? (partial.workflows ? "partial" : "ok") : (partial.workflows ? "partial" : "empty");

// ---------- tasks ----------
for (const [spaceKey, space] of Object.entries(result.spaces)) {
  const tasksRaw = cli(
    [
      "task", "list",
      "--space", space.id,
      "--status-changed-since", start,
      "--limit", "200",
      "--output", "json",
    ],
    { allowFail: true },
  );
  if (tasksRaw?.__error) {
    warn(`tasks unavailable: ${space.name}`);
    partial.tasks = true;
    continue;
  }
  const tasks = asArray(tasksRaw);
  const filtered = tasks.filter((task) => inWindow(task.createdAt) || inWindow(task.statusChangedAt));
  if (tasks.length >= 200) {
    warn(`tasks may be truncated: ${space.name} (raw ${tasks.length}, window ${filtered.length})`);
    partial.tasks = true;
  }
  const sorted = [...filtered].sort(
    (a, b) => Date.parse(b.statusChangedAt || b.createdAt || 0) - Date.parse(a.statusChangedAt || a.createdAt || 0),
  );
  if (sorted.length > 200) {
    warn(`tasks capped at 200: ${space.name}`);
    partial.tasks = true;
  }
  for (const task of sorted.slice(0, 200)) {
    result.tasks.push({
      space: spaceKey,
      identifier: task.identifier,
      title: truncate(task.title || "", 200),
      status: task.status?.name || null,
      category: task.status?.category || null,
      createdAt: task.createdAt || null,
      statusChangedAt: task.statusChangedAt || null,
      closedAt: task.closedAt || null,
      delegateType: task.delegate?.type || null,
      delegateId: task.delegate?.id || null,
      assignee: task.assignee?.name || task.assignee?.id || task.assignee || task.assignees || null,
      creatorType: task.creator?.type || null,
      creatorId: task.creator?.id || null,
      duplicateOfTaskId: task.duplicateOfTaskId || null,
      parentTaskId: task.parentTaskId || null,
      subtasksCount: task.subtasksCount ?? null,
      commentCount: null,
      reworkSignal: null,
    });
  }
}
result.meta.coverage.tasks = result.tasks.length ? (partial.tasks ? "partial" : "ok") : (partial.tasks ? "partial" : "empty");

// ---------- automations ----------
for (const [spaceKey, space] of Object.entries(result.spaces)) {
  const automationsRaw = cli(["automation", "list", "--space", space.id, "--output", "json"], { allowFail: true });
  if (automationsRaw?.__error) {
    warn(`automations unavailable: ${space.name}`);
    partial.automations = true;
    continue;
  }
  const automations = asArray(automationsRaw);
  const dispatchAgentIds = new Set(result.agents.map((agent) => agent.id));
  const relevant = automations.filter(
    (item) => (item.assigneeType === "agent" || !item.assigneeType) && dispatchAgentIds.has(item.assigneeId),
  );
  for (const automation of relevant) {
    const runsRaw = cli(
      ["automation", "runs", automation.id, "--limit", "50", "--output", "json"],
      { allowFail: true },
    );
    if (runsRaw?.__error) {
      warn(`automation runs unavailable: ${automation.name || automation.title}`);
      partial.automations = true;
      continue;
    }
    const runs = asArray(runsRaw);
    if (runs.length >= 50) {
      warn(`automation runs may be truncated: ${automation.name || automation.title}`);
      partial.automations = true;
    }
    const filtered = runs.filter((run) => inWindow(run.createdAt || run.triggeredAt));
    for (const run of filtered) {
      result.automations.push({
        space: spaceKey,
        name: automation.name || automation.title,
        automationId: automation.id,
        runId: run.runId || run.id || null,
        status: run.status || null,
        triggeredAt: run.triggeredAt || run.createdAt || null,
        completedAt: run.completedAt || null,
        failureReason: truncate(run.failureReason || "", 300),
      });
    }
  }
}
result.meta.coverage.automations = result.automations.length ? (partial.automations ? "partial" : "ok") : (partial.automations ? "partial" : "empty");

// ---------- ledger (read-only) ----------
const sharedRaw = cli(["docs", "shared", "list", "--space", "HiFox 产研", "--output", "json"], { allowFail: true });
if (sharedRaw?.__error) {
  warn("docs shared unavailable");
  partial.ledger = true;
  partial.improvements = true;
}
const shared = asArray(sharedRaw);
const agentsSet = shared.find((collection) => collection.name === "Agents");
if (!agentsSet) {
  warn("Agents docset missing");
} else {
  const rootRaw = cli(["docs", "list", "--shared", agentsSet.id, "--output", "json"], { allowFail: true });
  if (rootRaw?.__error) partial.ledger = true;
  const root = asArray(rootRaw);
  const ledgerName = tier === "staging" ? "吴用 (staging)" : "吴用";
  const ledgerRoot = root.find((doc) => doc.kind === "folder" && doc.name === ledgerName);
  if (!ledgerRoot) {
    warn(`ledger missing: ${ledgerName}`);
  } else {
    result.ledger.available = true;
    const statusNames = ["进行中", "等待用户", "计划"];
    for (const status of statusNames) {
      const statusListRaw = cli(
        ["docs", "list", "--shared", agentsSet.id, "--parent", ledgerRoot.id, "--output", "json"],
        { allowFail: true },
      );
      if (statusListRaw?.__error) {
        partial.ledger = true;
        continue;
      }
      const folder = asArray(statusListRaw).find((doc) => doc.kind === "folder" && doc.name === status);
      if (!folder) continue;
      const docsRaw = cli(
        ["docs", "list", "--shared", agentsSet.id, "--parent", folder.id, "--output", "json"],
        { allowFail: true },
      );
      if (docsRaw?.__error) {
        partial.ledger = true;
        continue;
      }
      const docs = asArray(docsRaw).filter(isDoc);
      if (docs.length > 30) partial.ledger = true;
      for (const doc of docs.slice(0, 30)) {
        const body = cli(["docs", "document", "view", doc.id, "--shared", agentsSet.id, "--output", "text"], {
          allowFail: true,
        });
        if (body?.__error) {
          partial.ledger = true;
          continue;
        }
        result.ledger.items.push({
          status,
          name: doc.name,
          id: doc.id,
          updatedAt: doc.updatedAt || null,
          body: truncate(typeof body === "string" ? body : body?.content || "", 3500),
        });
      }
    }
    const closedRootRaw = cli(
      ["docs", "list", "--shared", agentsSet.id, "--parent", ledgerRoot.id, "--output", "json"],
      { allowFail: true },
    );
    if (closedRootRaw?.__error) partial.ledger = true;
    const closedRoot = asArray(closedRootRaw).find((doc) => doc.kind === "folder" && doc.name === "已收口");
    if (closedRoot) {
      const months = new Set([windowDate.slice(0, 7), currentMonthShanghai(), previousMonthShanghai()]);
      for (const month of months) {
        const monthListRaw = cli(
          ["docs", "list", "--shared", agentsSet.id, "--parent", closedRoot.id, "--output", "json"],
          { allowFail: true },
        );
        if (monthListRaw?.__error) {
          partial.ledger = true;
          continue;
        }
        const monthFolder = asArray(monthListRaw).find((doc) => doc.kind === "folder" && doc.name === month);
        if (!monthFolder) continue;
        const closedDocsRaw = cli(
          ["docs", "list", "--shared", agentsSet.id, "--parent", monthFolder.id, "--output", "json"],
          { allowFail: true },
        );
        if (closedDocsRaw?.__error) {
          partial.ledger = true;
          continue;
        }
        const closedDocs = asArray(closedDocsRaw).filter(isDoc);
        if (closedDocs.length > 30) partial.ledger = true;
        for (const doc of closedDocs.slice(0, 30)) {
          const body = cli(["docs", "document", "view", doc.id, "--shared", agentsSet.id, "--output", "text"], {
            allowFail: true,
          });
          if (body?.__error) {
            partial.ledger = true;
            continue;
          }
          const text = typeof body === "string" ? body : body?.content || "";
          if (!/改进项：/.test(text) || !/决策：/.test(text)) continue;
          result.ledger.items.push({
            status: "已收口",
            name: doc.name,
            id: doc.id,
            updatedAt: doc.updatedAt || null,
            body: truncate(text, 3500),
          });
        }
      }
    }
  }
}
result.meta.coverage.ledger = result.ledger.available ? (partial.ledger ? "partial" : "ok") : "missing";

// ---------- improvements (read-only) ----------
if (agentsSet) {
  const rootRaw = cli(["docs", "list", "--shared", agentsSet.id, "--output", "json"], { allowFail: true });
  if (rootRaw?.__error) partial.improvements = true;
  const root = asArray(rootRaw);
  const ownName = tier === "staging" ? "裴宣 (staging)" : "裴宣";
  const ownRoot = root.find((doc) => doc.kind === "folder" && doc.name === ownName);
  if (ownRoot) {
    result.improvements.available = true;
    const childrenRaw = cli(
      ["docs", "list", "--shared", agentsSet.id, "--parent", ownRoot.id, "--output", "json"],
      { allowFail: true },
    );
    if (childrenRaw?.__error) partial.improvements = true;
    const children = asArray(childrenRaw);
    const itemsRoot = children.find((doc) => doc.kind === "folder" && doc.name === "改进项");
    if (itemsRoot) {
      const statusFoldersRaw = cli(
        ["docs", "list", "--shared", agentsSet.id, "--parent", itemsRoot.id, "--output", "json"],
        { allowFail: true },
      );
      if (statusFoldersRaw?.__error) partial.improvements = true;
      const statusFolders = asArray(statusFoldersRaw).filter((doc) => doc.kind === "folder");
      const pushDocs = (docs, status) => {
        for (const doc of docs.slice(0, 20)) {
          if (result.improvements.items.length >= 60) {
            partial.improvements = true;
            return;
          }
          const body = cli(["docs", "document", "view", doc.id, "--shared", agentsSet.id, "--output", "text"], {
            allowFail: true,
          });
          if (body?.__error) {
            partial.improvements = true;
            continue;
          }
          result.improvements.items.push({
            status,
            name: doc.name,
            id: doc.id,
            updatedAt: doc.updatedAt || null,
            body: truncate(typeof body === "string" ? body : body?.content || "", 3500),
          });
        }
      };
      for (const statusFolder of statusFolders) {
        const childrenOfStatusRaw = cli(
          ["docs", "list", "--shared", agentsSet.id, "--parent", statusFolder.id, "--output", "json"],
          { allowFail: true },
        );
        if (childrenOfStatusRaw?.__error) {
          partial.improvements = true;
          continue;
        }
        const childrenOfStatus = asArray(childrenOfStatusRaw);
        pushDocs(childrenOfStatus.filter(isDoc), statusFolder.name);
        for (const monthFolder of childrenOfStatus.filter((doc) => doc.kind === "folder")) {
          const monthDocsRaw = cli(
            ["docs", "list", "--shared", agentsSet.id, "--parent", monthFolder.id, "--output", "json"],
            { allowFail: true },
          );
          if (monthDocsRaw?.__error) {
            partial.improvements = true;
            continue;
          }
          pushDocs(asArray(monthDocsRaw).filter(isDoc), statusFolder.name);
        }
      }
      const overview = children.find((doc) => isDoc(doc) && docBaseName(doc.name) === "自省总览");
      if (overview) {
        const body = cli(["docs", "document", "view", overview.id, "--shared", agentsSet.id, "--output", "text"], {
          allowFail: true,
        });
        if (body?.__error) {
          partial.improvements = true;
        } else {
          result.improvements.overview = {
            id: overview.id,
            body: truncate(typeof body === "string" ? body : body?.content || "", 12000),
          };
        }
      }
    }
  }
}
result.meta.coverage.improvements = result.improvements.available ? (partial.improvements ? "partial" : "ok") : "missing";

// ---------- task scope and rework signals ----------
const ledgerTaskIds = new Set();
for (const item of result.ledger.items) {
  for (const match of String(item.body || "").matchAll(/task=([A-Za-z]+-\d+)/g)) {
    ledgerTaskIds.add(match[1]);
  }
}
const teamAgentIds = new Set(result.agents.map((agent) => agent.id));
for (const task of result.tasks) {
  task.dispatchLinked =
    ledgerTaskIds.has(task.identifier) ||
    (task.delegateType === "agent" && teamAgentIds.has(task.delegateId)) ||
    (task.creatorType === "agent" && teamAgentIds.has(task.creatorId));
}
let commentCalls = 0;
const commentTargets = result.tasks.filter(terminalTask).sort((a, b) => {
  const aCompleted = a.category === "completed" ? 0 : 1;
  const bCompleted = b.category === "completed" ? 0 : 1;
  if (aCompleted !== bCompleted) return aCompleted - bCompleted;
  return Date.parse(b.statusChangedAt || b.createdAt || 0) - Date.parse(a.statusChangedAt || a.createdAt || 0);
});
for (const task of commentTargets) {
  if (commentCalls >= 20) {
    partial.tasks = true;
    warn("task comments capped at 20; first-pass is over the checked sample");
    break;
  }
  commentCalls += 1;
  const commentsRaw = cli(["task", "comment", "list", task.identifier, "--output", "json"], { allowFail: true });
  if (commentsRaw?.__error) {
    partial.tasks = true;
    continue;
  }
  const comments = asArray(commentsRaw, ["comments", "data"]);
  const text = comments.map((comment) => comment.content || comment.body || "").join("\n");
  task.commentCount = comments.length;
  task.reworkSignal = /返工|重开|未通过|打回|reopen/i.test(text);
}

// ---------- metrics (deterministic; the model should not recount) ----------
function countBy(items, keyFn) {
  const out = {};
  for (const item of items) {
    const key = keyFn(item) ?? "unknown";
    out[key] = (out[key] || 0) + 1;
  }
  return out;
}
function median(values) {
  const sorted = values.filter((value) => Number.isFinite(value)).sort((a, b) => a - b);
  if (!sorted.length) return null;
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2;
}
function hoursBetween(from, to) {
  const a = Date.parse(from || "");
  const b = Date.parse(to || "");
  if (!Number.isFinite(a) || !Number.isFinite(b) || b < a) return null;
  return (b - a) / 36e5;
}
function ledgerField(body, label) {
  const text = String(body || "");
  const match = text.match(new RegExp(`(?:^|\\n)[-*]?\\s*${label}：\\s*(.+)`));
  if (match) return match[1].trim();
  if (label === "结论") {
    const timeline = text.match(/(?:^|\n)[-*]?\s*(\d{4}-\d{2}-\d{2}T[^\s]+)\s+结论：\s*(.+)/);
    if (timeline) return `${timeline[1]} ${timeline[2]}`;
  }
  return "";
}
function isoPrefix(value) {
  const match = String(value || "").match(/\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?/);
  return match ? match[0] : "";
}
function terminalRun(run) {
  return ["completed", "failed", "cancelled"].includes(run.status);
}
function terminalTask(task) {
  if (!["completed", "canceled", "cancelled", "duplicate"].includes(task.category)) return false;
  return inWindow(task.statusChangedAt || task.closedAt);
}

const terminalTasks = result.tasks.filter(terminalTask);
const checkedTerminalTasks = terminalTasks.filter((task) => task.commentCount != null);
const firstPassTasks = terminalTasks.filter(
  (task) =>
    task.category === "completed" &&
    task.commentCount != null &&
    !task.duplicateOfTaskId &&
    !task.reworkSignal,
);
const recentTerminalTasks = terminalTasks.filter((task) => {
  const created = Date.parse(task.createdAt || "");
  return Number.isFinite(created) && created >= startMs - 90 * 864e5;
});
const terminalCloseHours = (tasks) =>
  tasks
    .map((task) => hoursBetween(task.createdAt, task.statusChangedAt || task.closedAt))
    .filter((value) => value != null);

const resultErrors = result.runs.filter((run) =>
  /429|insufficient balance|no resource package|rate limit|quota/i.test(run.result),
);

const metrics = {
  runs: {
    total: result.runs.length,
    byTrigger: countBy(result.runs, (run) => run.trigger),
    byStatus: countBy(result.runs, (run) => run.status),
    terminal: result.runs.filter(terminalRun).length,
    failed: result.runs.filter((run) => run.status === "failed").length,
    errors: countBy(
      result.runs.filter((run) => run.error).map((run) => truncate(run.error, 120)),
      (error) => error,
    ),
    resultErrors: resultErrors.length,
    resultErrorSamples: countBy(
      resultErrors.map((run) => truncate(run.result, 120)),
      (text) => text,
    ),
  },
  tasks: {
    total: result.tasks.length,
    terminal: terminalTasks.length,
    byCategory: countBy(result.tasks, (task) => task.category),
    completed: terminalTasks.filter((task) => task.category === "completed").length,
    cancelled: terminalTasks.filter((task) => ["canceled", "cancelled"].includes(task.category)).length,
    duplicate: result.tasks.filter((task) => task.duplicateOfTaskId).length,
    firstPass: firstPassTasks.length,
    commentChecked: checkedTerminalTasks.length,
    medianCloseHours: median(terminalCloseHours(recentTerminalTasks)),
    medianCloseHoursAll: median(terminalCloseHours(terminalTasks)),
    ancientTerminal: terminalTasks.length - recentTerminalTasks.length,
  },
  workflows: {
    total: result.workflows.length,
    byStatus: countBy(result.workflows, (log) => log.status),
    failed: result.workflows.filter((log) => log.status === "failed").length,
  },
  automations: {
    total: result.automations.length,
    byStatus: countBy(result.automations, (run) => run.status),
    failed: result.automations.filter((run) => run.status === "failed").length,
  },
  ledger: {
    available: result.ledger.available,
    total: result.ledger.items.length,
    byStatus: countBy(result.ledger.items, (item) => item.status),
    clarifications: result.ledger.items.filter((item) => /判定依据：.*澄清/.test(item.body)).length,
    waitingOver7d: result.ledger.items.filter((item) => {
      if (item.status !== "等待用户") return false;
      const hours = hoursBetween(isoPrefix(ledgerField(item.body, "创建")), new Date().toISOString());
      return hours != null && hours > 7 * 24;
    }).length,
    plannedOver90d: result.ledger.items.filter((item) => {
      if (item.status !== "计划") return false;
      const hours = hoursBetween(isoPrefix(ledgerField(item.body, "创建")), new Date().toISOString());
      return hours != null && hours > 90 * 24;
    }).length,
    medianVerdictHours: median(
      result.ledger.items
        .map((item) => {
          const created = isoPrefix(ledgerField(item.body, "创建"));
          const verdict = isoPrefix(ledgerField(item.body, "结论"));
          return created && verdict ? hoursBetween(created, verdict) : null;
        })
        .filter((value) => value != null),
    ),
  },
  improvements: {
    available: result.improvements.available,
    total: result.improvements.items.length,
    byStatus: countBy(result.improvements.items, (item) => item.status),
  },
};
metrics.runs.failureRate = metrics.runs.terminal ? metrics.runs.failed / metrics.runs.terminal : null;
metrics.tasks.firstPassRate = checkedTerminalTasks.length
  ? firstPassTasks.length / checkedTerminalTasks.length
  : null;
metrics.workflows.successRate = metrics.workflows.total
  ? (["completed", "succeeded", "success"].reduce((sum, key) => sum + (metrics.workflows.byStatus[key] || 0), 0) /
      metrics.workflows.total)
  : null;
metrics.automations.failureRate = metrics.automations.total
  ? metrics.automations.failed / metrics.automations.total
  : null;
result.meta.coverage.tasks = result.tasks.length ? (partial.tasks ? "partial" : "ok") : (partial.tasks ? "partial" : "empty");
result.metrics = metrics;

process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
