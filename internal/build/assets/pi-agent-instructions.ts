// The image's agent instructions for pi, which has no system config layer:
// the /usr/local/bin/pi wrapper loads this extension with -e. A prompt section
// patches in place, so the model keeps its cached prefix.
import { readFileSync } from "node:fs";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const instructions = readFileSync("/etc/toolbox/agent-instructions.md", "utf8");

export default function (pi: ExtensionAPI) {
	pi.on("before_agent_start", (event) => {
		event.systemPromptOptions.sections.toolbox = instructions;
	});
}
