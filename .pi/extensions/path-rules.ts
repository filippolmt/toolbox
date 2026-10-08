// Pi-only enforcement. Codex follows the rule pointers in AGENTS.md; this
// extension closes the equivalent gap for Pi's edit and write tools.
import type { ExtensionAPI, ToolResultEvent } from "@earendil-works/pi-coding-agent";
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

export class ReadCoverage {
  private readonly through = new Map<string, number>();

  clear() {
    this.through.clear();
  }

  complete(file: string, totalLines: number): boolean {
    return (this.through.get(file) ?? 0) >= totalLines;
  }

  record(file: string, start: number, end: number) {
    const through = this.through.get(file) ?? 0;
    if (start <= through + 1) this.through.set(file, Math.max(through, end));
  }
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

function resultText(event: ToolResultEvent): string {
  return event.content.filter((item) => item.type === "text").map((item) => item.text).join("\n");
}

export default function (pi: ExtensionAPI) {
  const coverage = new ReadCoverage();
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
    if (event.toolName !== "edit" && event.toolName !== "write") return undefined;
    const input = event.input as { path?: string };
    if (!input.path) return undefined;

    const rules = await rulesAt(ctx.cwd);
    const missing: Rule[] = [];
    for (const rule of rulesForPath(ctx.cwd, input.path, rules)) {
      const total = (await readFile(rule.file, "utf8")).split("\n").length;
      if (!coverage.complete(rule.file, total)) missing.push(rule);
    }
    if (missing.length === 0) return undefined;
    return {
      block: true,
      reason: `Read or re-read the complete path-scoped rules before editing ${input.path} (rule edits invalidate prior reads): ${missing.map((rule) => relative(ctx.cwd, rule.file)).join(", ")}`,
    };
  });

  pi.on("tool_result", async (event, ctx) => {
    if (event.isError) return undefined;

    if (event.toolName === "read") {
      const input = event.input as { path?: string; offset?: number; limit?: number };
      if (!input.path) return undefined;
      const file = resolve(ctx.cwd, input.path);
      const rules = await rulesAt(ctx.cwd);
      if (!rules.some((rule) => rule.file === file)) return undefined;

      const total = (await readFile(file, "utf8")).split("\n").length;
      const start = input.offset ?? 1;
      const continuation = resultText(event).match(/Use offset=(\d+) to continue/);
      const end = continuation
        ? Number(continuation[1]) - 1
        : Math.min(total, start + (input.limit ?? total) - 1);
      coverage.record(file, start, end);
      return undefined;
    }

    if (event.toolName === "edit" || event.toolName === "write") {
      const input = event.input as { path?: string };
      const path = input.path && resolve(ctx.cwd, input.path);
      const rulesDir = resolve(ctx.cwd, ".claude/rules") + sep;
      if (path?.startsWith(rulesDir)) {
        byCwd.delete(ctx.cwd);
        coverage.clear();
      }
    }
    return undefined;
  });
}
