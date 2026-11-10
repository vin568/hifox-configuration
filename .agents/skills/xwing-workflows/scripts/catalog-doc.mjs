#!/usr/bin/env node
// catalog-doc — render the engine's live block catalog into authoring-friendly
// output. The catalog is the engine's truth (278+ block types; fields, widget
// types, required flags, defaults, options, bestPractices). Never trust stale
// snapshots: always query.
//
// Usage (inside a repo managed by x-deploy):
//   node catalog-doc.mjs list [filter]            # block types (type/name/category)
//   node catalog-doc.mjs view <blockType>         # full field detail for one block
//   node catalog-doc.mjs options <type> <field>   # dynamic options for a field
//
// cli.app and spaceId are auto-detected from environments/*/environment.yaml
// (first environment); override with --cli <app> / --space <id>.
import { execFileSync } from "node:child_process";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

const args = process.argv.filter((a) => !a.startsWith("--"));
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
  console.error("catalog-doc: no spaceId found (pass --space <id> or run inside an x-deploy repo)");
  process.exit(2);
}

function call(sub, extra) {
  const out = execFileSync(CLI, ["workflow", "catalog", sub, ...extra, "--space", SPACE, "--output", "json"], {
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
  });
  return JSON.parse(out);
}

function unwrap(payload) {
  return Array.isArray(payload) ? payload : (payload?.data ?? payload);
}

const [command, ...rest] = args.slice(2).filter((a) => !a.startsWith("--"));

if (command === "list") {
  const filter = rest[0]?.toLowerCase();
  const rows = unwrap(call("list", [])).filter(
    (row) => !filter || `${row.type} ${row.name} ${row.category}`.toLowerCase().includes(filter),
  );
  for (const row of rows) console.log(`${row.type}\t${row.category ?? "-"}\t${row.name}`);
  console.error(`# ${rows.length} block type(s)${filter ? ` matching "${filter}"` : ""}`);
} else if (command === "view") {
  if (!rest[0]) {
    console.error("catalog-doc: view requires a block type (run `list` first)");
    process.exit(2);
  }
  console.log(JSON.stringify(call("view", [rest[0]]), null, 2));
} else if (command === "options") {
  if (rest.length < 2) {
    console.error("catalog-doc: options requires <blockType> <fieldId>");
    process.exit(2);
  }
  console.log(JSON.stringify(call("options", [rest[0], rest[1]]), null, 2));
} else {
  console.error("catalog-doc: use `list [filter]` | `view <type>` | `options <type> <field>`");
  process.exit(2);
}
