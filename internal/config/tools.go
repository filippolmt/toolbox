package config

// KnownTools is the canonical list of opt-out tools baked into the Dockerfile.
// Keep this ordered alphabetically so the hash that drives local image tags
// is stable across refactors.
var KnownTools = []string{
	"claude",
	"docker",
	"gcloud",
	"gh",
	"glab",
	"helm",
	"jq",
	"kubectl",
	"pnpm",
	"starship",
	"tofu",
	"uv",
	"yq",
}

// ToolBuildArg maps a tool key in the user's config to the Docker ARG name
// the Dockerfile expects. The Dockerfile wraps each tool layer with
// `ARG INSTALL_<ARG>=true` + an `if` conditional.
var ToolBuildArg = map[string]string{
	"claude":   "INSTALL_CLAUDE_CODE",
	"docker":   "INSTALL_DOCKER_CLI",
	"gcloud":   "INSTALL_GCLOUD",
	"gh":       "INSTALL_GH",
	"glab":     "INSTALL_GLAB",
	"helm":     "INSTALL_HELM",
	"jq":       "INSTALL_JQ",
	"kubectl":  "INSTALL_KUBECTL",
	"pnpm":     "INSTALL_PNPM",
	"starship": "INSTALL_STARSHIP",
	"tofu":     "INSTALL_TOFU",
	"uv":       "INSTALL_UV",
	"yq":       "INSTALL_YQ",
}

// DefaultTools returns the canonical default tools map: every known tool enabled.
func DefaultTools() map[string]bool {
	out := make(map[string]bool, len(KnownTools))
	for _, k := range KnownTools {
		out[k] = true
	}
	return out
}

// IsDefaultTools reports whether the given tools map matches the defaults.
// A missing key is treated as enabled (the Viper default is `true`), so a
// config that never mentions `tools:` evaluates as default.
func IsDefaultTools(m map[string]bool) bool {
	for _, k := range KnownTools {
		enabled, present := m[k]
		if present && !enabled {
			return false
		}
	}
	return true
}
