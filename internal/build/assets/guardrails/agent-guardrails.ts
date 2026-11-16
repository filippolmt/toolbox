// The image's agent guardrails for pi, which has no system config layer: the
// /usr/local/bin/pi wrapper loads this extension with -e. Each bash call goes
// to the hook binary Claude Code and Codex run, in their PreToolUse protocol:
// exit 2 blocks, with the reason on stderr. A hook that fails to run blocks
// nothing — the guardrails catch mistakes, they are not a sandbox.
import { spawnSync } from "node:child_process";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const hook = "/usr/local/lib/toolbox/agent-guardrails";

export default function (pi: ExtensionAPI) {
	pi.on("tool_call", (event, ctx) => {
		if (event.toolName !== "bash") return;
		const command = (event.input as { command?: unknown }).command;
		if (typeof command !== "string") return;
		const result = spawnSync(hook, {
			input: JSON.stringify({ hook_event_name: "PreToolUse", tool_name: "Bash", cwd: ctx.cwd, tool_input: { command } }),
			encoding: "utf8",
			timeout: 5000,
		});
		if (result.status === 2) return { block: true, reason: result.stderr.trim() };
	});
}
