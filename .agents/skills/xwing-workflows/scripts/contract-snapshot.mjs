#!/usr/bin/env node
// contract-snapshot — record and diff the engine contract surfaces this CLI's
// workflow pipeline depends on (ADR 0020). The catalog block-type table is the
// detection surface for engine evolution: new block types, removals or category
// moves show up as snapshot drift and must be reviewed (promote to a sugar,
// ride extra/state passthrough, or add an engine-managed field name).
//
// Usage (inside a repo managed by x-deploy, run at the repo root):
//   node contract-snapshot.mjs                 # compare; exit 1 on drift
//   node contract-snapshot.mjs --update        # rewrite the snapshot
//   node contract-snapshot.mjs --update --cli hifox --space <id>
//
// The snapshot lives at contract-snapshots/catalog.tsv (commit it). cli.app and
// spaceId are auto-detected from environments/*/environment.yaml (first
// environment), same convention as catalog-doc.mjs.
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const flag = (name) => {
  const i = process.argv.indexOf(`--${name}`);
  return i >= 0 ? process.argv[i + 1] : undefined;
};

function detect() {
  const root = process.cwd();
  const envDirs = join(root, "environments");
  if (existsSync(envDirs)) {
    for (const name of readdirSync(envDirs)) {
      const file = join(envDirs, name, "environment.yaml");
      if (!existsSync(file)) continue;
      const text = readFileSync(file, "utf8");
      const space = text.match(/^spaceId:\s*(\S+)/m)?.[1];
      const app = text.match(/^\s+app:\s*(\S+)/m)?.[1];
      if (space && app) return { cli: app, space };
    }
  }
  return { cli: "hifox", space: undefined };
}

const detected = detect();
const CLI = flag("cli") || detected.cli;
const SPACE = flag("space") || detected.space;
if (!SPACE) {
  console.error("contract-snapshot: no spaceId found (pass --space <id> or run inside an x-deploy repo)");
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

const snapshotPath = join(process.cwd(), "contract-snapshots", "catalog.tsv");
if (process.argv.includes("--update") || !existsSync(snapshotPath)) {
  mkdirSync(join(process.cwd(), "contract-snapshots"), { recursive: true });
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
console.error(`contract-snapshot: engine contract drift detected (catalog ${currentRows.size} -> ${nextRows.size} type(s))`);
for (const type of added) console.error(`  + ${nextRows.get(type)}`);
for (const type of removed) console.error(`  - ${currentRows.get(type)}`);
for (const type of moved) console.error(`  ~ ${currentRows.get(type)} -> ${nextRows.get(type)}`);
console.error("review each entry, then re-run with --update; new structural families need a roundtrip.test.mjs fixture");
process.exit(1);
