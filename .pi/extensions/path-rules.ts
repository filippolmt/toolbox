import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { readFile, readdir } from "node:fs/promises";
import { matchesGlob, relative, resolve, sep } from "node:path";

export type Rule = { file: string; globs: string[] };

export function parseRulePaths(source: string): string[] {
  const lines = source.split("\n");
  const end = lines.indexOf("---", 1);
  if (lines[0] !== "---" || end < 0) return [];

  const paths: string[] = [];
  let inPaths = false;
  for (const line of lines.slice(1, end)) {
    if (/^paths:\s*$/.test(line)) {
      inPaths = true;
      continue;
    }
    if (/^[A-Za-z_-]+:/.test(line)) {
      inPaths = false;
      continue;
    }
    if (inPaths) {
      const match = line.match(/^\s+-\s+["'](.+)["']\s*$/);
      if (match) paths.push(match[1]);
    }
  }
  return paths;
}

export function rulesForPath(cwd: string, target: string, rules: Rule[]): Rule[] {
  const rel = relative(cwd, resolve(cwd, target));
  if (rel === ".." || rel.startsWith(`..${sep}`)) return [];
  const portable = rel.split(sep).join("/");
  return rules.filter((rule) => rule.globs.some((glob) => matchesGlob(portable, glob)));
}

async function discoverRules(cwd: string): Promise<Rule[]> {
  const dir = resolve(cwd, ".claude/rules");
  let names: string[];
  try {
    names = (await readdir(dir)).filter((name) => name.endsWith(".md")).sort();
  } catch (error: any) {
    if (error?.code === "ENOENT") return [];
    throw error;
  }
  return Promise.all(names.map(async (name) => {
    const file = resolve(dir, name);
    return { file, globs: parseRulePaths(await readFile(file, "utf8")) };
  }));
}

export default function (pi: ExtensionAPI) {
  const readRules = new Set<string>();
  const byCwd = new Map<string, Promise<Rule[]>>();
  const rulesAt = (cwd: string) => {
    let rules = byCwd.get(cwd);
    if (!rules) {
      rules = discoverRules(cwd);
      byCwd.set(cwd, rules);
    }
    return rules;
  };

  pi.on("tool_call", async (event, ctx) => {
    if (event.toolName !== "read" && event.toolName !== "edit" && event.toolName !== "write") return undefined;
    const input = event.input as { path?: string };
    if (!input.path) return undefined;

    const rules = await rulesAt(ctx.cwd);
    const target = resolve(ctx.cwd, input.path);
    if (event.toolName === "read") {
      if (rules.some((rule) => rule.file === target)) readRules.add(target);
      return undefined;
    }

    const missing = rulesForPath(ctx.cwd, target, rules).filter((rule) => !readRules.has(rule.file));
    if (missing.length === 0) return undefined;
    return {
      block: true,
      reason: `Read the path-scoped rules before editing ${input.path}: ${missing.map((rule) => relative(ctx.cwd, rule.file)).join(", ")}`,
    };
  });
}
