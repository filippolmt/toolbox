// Pi-only enforcement for the repository's containerised Go toolchain.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

// ponytail: this catches direct command positions, not nested shell strings; add a shell parser only if accidental bypasses recur.
const directGoCommand = /(?:^|[\n;&|])\s*(?:env\s+)?(?:[A-Za-z_][A-Za-z0-9_]*=\S*\s+)*(?:command\s+)?(?:\S*\/)?(?:go|gofmt)(?=\s|$)/;

export default function (pi: ExtensionAPI) {
  pi.on("tool_call", async (event) => {
    if (event.toolName !== "bash") return undefined;

    const command = (event.input as { command?: string }).command;
    if (!command || !directGoCommand.test(command)) return undefined;

    return {
      block: true,
      reason: "Run Go commands through the repository's `make go-*` targets; use `make help` to choose one.",
    };
  });
}
