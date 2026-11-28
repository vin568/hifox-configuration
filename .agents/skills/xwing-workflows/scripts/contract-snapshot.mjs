#!/usr/bin/env node
// contract-snapshot — record and diff the engine contract surfaces this CLI's
// workflow pipeline depends on (ADR 0020). The catalog block-type table is the
// detection surface for engine evolution: new block types, removals or category
// moves show up as snapshot drift and must be reviewed (promote to a sugar,
// ride extra/state passthrough, or add an engine-managed field name).
//
// Usage (inside a repo managed by x-deploy, run at the repo root):
//   node contract-snapshot.mjs                 # compare; exit 1 only on removed types
//   node contract-snapshot.mjs --update        # rewrite the snapshot
//   node contract-snapshot.mjs --update --cli hifox --space <id>
//
// The snapshot lives at .contract-snapshots/catalog.tsv next to the deployment
// directories (repo root in the dedicated layout, inside .xwing/ in the embedded
// layout — ADR 0035); commit it. cli.app and space (identifier or UUID, ADR 0033)
// are auto-detected from the first environment's environment.yaml, same
// convention as catalog-doc.mjs.
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

const flag = (name) => {
  const i = process.argv.indexOf(`--${name}`);
  return i >= 0 ? process.argv[i + 1] : undefined;
};

const LEGACY_SNAPSHOT_DIR = ".contract-snapshots";

// 布局一律问 CLI（ADR 0036）：`x-deploy settings` 用与 doctor 同一套严格校验解析
// x-deploy.yaml。手写解析曾在这里静默接受绝对 configDir，把目录读到仓外去。
// 调用形态跟 init 注入的同一套探测：仓根有 ./x-deploy 壳脚本就用它（copy/子模块
// 安装），否则 `npx --no-install x-deploy`（npm 安装；不联网、不自动装）。
function deploymentSettings(root) {
  const local = join(root, "x-deploy");
  const [cmd, args] = existsSync(local) ? [local, []] : ["npx", ["--no-install", "x-deploy"]];
  let out;
  try {
    out = execFileSync(cmd, [...args, "settings", "--root", root], {
      encoding: "utf8",
      maxBuffer: 8 * 1024 * 1024,
      stdio: ["ignore", "pipe", "pipe"],
    });
  } catch (err) {
    // 非零退出仍然带 JSON（payload 的 error 字段是判据）；拿不到 JSON 才是硬失败。
    out = err.stdout;
    if (!out) {
      console.error(`x-deploy settings 无法执行（${cmd}）：${String(err.stderr || err.message).trim()}`);
      process.exit(2);
    }
  }
  return JSON.parse(out);
}

function detect() {
  const root = process.cwd();
  const settings = deploymentSettings(root);
  if (settings.error) {
    console.error(`contract-snapshot: x-deploy.yaml 非法，拒绝在不明的布局上盖章：${settings.error}`);
    process.exit(2);
  }
  const envDirs = join(root, settings.environmentsDir);
  if (existsSync(envDirs)) {
    for (const name of readdirSync(envDirs)) {
      const file = join(envDirs, name, "environment.yaml");
      if (!existsSync(file)) continue;
      const text = readFileSync(file, "utf8");
      const space = text.match(/^space:\s*(\S+)/m)?.[1];
      if (space && space !== "null") {
        const app = text.match(/^\s+app:\s*(\S+)/m)?.[1];
        if (app) return { cli: app, space };
      }
    }
  }
  return { cli: "hifox", space: undefined };
}

const detected = detect();
const CLI = flag("cli") || detected.cli;
const SPACE = flag("space") || detected.space;
if (!SPACE) {
  console.error("contract-snapshot: no space declaration found (pass --space <id> or run bind first)");
  process.exit(2);
}

// `workflow catalog list` is read-only: the same surface catalog-doc.mjs uses.
const payload = JSON.parse(
  execFileSync(CLI, ["workflow", "catalog", "list", "--space", SPACE, "--output", "json"], {
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
  }),
);
const rows = Array.isArray(payload) ? payload : (payload?.data ?? []);
if (!Array.isArray(rows) || !rows.length) {
  console.error("contract-snapshot: catalog list returned no rows — refusing to write an empty snapshot");
  process.exit(2);
}
// Stable ordering: type is unique across the catalog.
const table = rows
  .map((row) => [row.type, row.category ?? "-", row.name ?? "-"])
  .sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0))
  .map((row) => row.join("\t"));
const next = `${table.join("\n")}\n# ${rows.length} block type(s) — engine contract snapshot (see skills/xwing-workflows/scripts/contract-snapshot.mjs)\n`;

// 快照跟部署目录同层（ADR 0035）：dedicated 布局在仓根，embedded 布局在 .xwing/。
// 不新开 settings 键——configDir 的父目录就是「部署载荷住在哪一层」。
const settings = deploymentSettings(process.cwd());
if (settings.error) {
  console.error(`contract-snapshot: x-deploy.yaml 非法，拒绝在不明的布局上盖章：${settings.error}`);
  process.exit(2);
}
const snapshotPath = join(process.cwd(), dirname(settings.configDir), ".contract-snapshots", "catalog.tsv");
// 布局迁移的静默陷阱：旧基线与新位置不同层时若直接盖章，会凭空造出一份「全新基线」，
// 「类型删除 → exit 1」从此永久失效。只能在全新的项目里直接盖章；迁移过的仓必须先 --update。
if (!existsSync(snapshotPath) && existsSync(join(process.cwd(), LEGACY_SNAPSHOT_DIR, "catalog.tsv"))) {
  console.error(
    `contract-snapshot: 仓根还有旧位置的基线 ${LEGACY_SNAPSHOT_DIR}/catalog.tsv，但当前布局的基线在 ${snapshotPath}。\n` +
      "  先确认位置迁移正确，再跑 --update 在新位置重新盖章并提交（否则引擎类型删除将不再报错）。",
  );
  process.exit(2);
}
if (process.argv.includes("--update") || !existsSync(snapshotPath)) {
  mkdirSync(join(process.cwd(), dirname(settings.configDir), ".contract-snapshots"), { recursive: true });
  writeFileSync(snapshotPath, next);
  console.log(`contract-snapshot: wrote ${rows.length} block type(s) to ${snapshotPath}`);
  process.exit(0);
}

const current = readFileSync(snapshotPath, "utf8");
if (current === next) {
  console.log(`contract-snapshot: in sync (${rows.length} block type(s))`);
  process.exit(0);
}

// Diff in terms reviewers care about: added/removed types and category moves.
const currentRows = new Map(current.split("\n").filter((line) => line && !line.startsWith("#")).map((line) => [line.split("\t")[0], line]));
const nextRows = new Map(table.map((line) => [line.split("\t")[0], line]));
const added = [...nextRows.keys()].filter((type) => !currentRows.has(type));
const removed = [...currentRows.keys()].filter((type) => !nextRows.has(type));
const moved = [...nextRows.keys()].filter((type) => currentRows.has(type) && currentRows.get(type) !== nextRows.get(type));
console.error(`contract-snapshot: engine contract drift (catalog ${currentRows.size} -> ${nextRows.size} type(s))`);
for (const type of added) console.error(`  + ${nextRows.get(type)}`);
for (const type of removed) console.error(`  - ${currentRows.get(type)}`);
for (const type of moved) console.error(`  ~ ${currentRows.get(type)} -> ${nextRows.get(type)}`);
if (removed.length) {
  console.error("drift is not a broken workflow: deployed instances keep running; only the next apply of declarations can break.");
  console.error("removed types are the only hard risk: if one is used in config/*/workflows/**, migrate that declaration before deploy (removals usually pair with a rename — see the + entries).");
  console.error("then re-run with --update to re-stamp the baseline and commit it; new structural families (sugar, roundtrip fixtures) are handled in the x-deploy repo.");
  process.exit(1);
}
console.error("additions and label/category tweaks are informational (no removals): re-run with --update to re-stamp the baseline and commit it.");
process.exit(0);
