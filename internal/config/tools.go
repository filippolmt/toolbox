package config

// KnownTools is the canonical list of opt-out tools baked into the Dockerfile.
// Keep this ordered alphabetically so the hash that drives local image tags
// is stable across refactors.
//
// Hash invalidation: ADDING (or removing) an entry here invalidates the
// `toolbox:local-<hash>` tag for every user who has any non-default tools
// config — the hash is computed over the sorted Tools map, so a new key
// shifts the digest even if the user never set it. Their next `toolbox
// shell` will see an "Image not found locally — building …" line and
// rebuild once. Document this in the release notes when bumping this list.
var KnownTools = []string{
	"azure",
	"bat",
	"cf",
	"claude",
	"codex",
	"compose",
	"docker",
	"gcloud",
	"gh",
	"glab",
	"go",
	"goimports",
	"gopls",
	"gws",
	"helm",
	"jq",
	"kubectl",
	"oci",
	"playwright",
	"playwright_cli",
	"pnpm",
	"pyright",
	"rtk",
	"starship",
	"tofu",
	"uv",
	"yq",
	"zsh",
}

// ToolBuildArg maps a tool key in the user's config to the Docker ARG name
// the Dockerfile expects. The Dockerfile wraps each tool layer with
// `ARG INSTALL_<ARG>=true` + an `if` conditional.
var ToolBuildArg = map[string]string{
	"azure":          "INSTALL_AZURE",
	"bat":            "INSTALL_BAT",
	"cf":             "INSTALL_CF",
	"claude":         "INSTALL_CLAUDE_CODE",
	"codex":          "INSTALL_CODEX_CLI",
	"compose":        "INSTALL_COMPOSE",
	"docker":         "INSTALL_DOCKER_CLI",
	"gcloud":         "INSTALL_GCLOUD",
	"gh":             "INSTALL_GH",
	"glab":           "INSTALL_GLAB",
	"go":             "INSTALL_GO",
	"goimports":      "INSTALL_GOIMPORTS",
	"gopls":          "INSTALL_GOPLS",
	"gws":            "INSTALL_GWS",
	"helm":           "INSTALL_HELM",
	"jq":             "INSTALL_JQ",
	"kubectl":        "INSTALL_KUBECTL",
	"oci":            "INSTALL_OCI",
	"playwright":     "INSTALL_PLAYWRIGHT",
	"playwright_cli": "INSTALL_PLAYWRIGHT_CLI",
	"pnpm":           "INSTALL_PNPM",
	"pyright":        "INSTALL_PYRIGHT",
	"rtk":            "INSTALL_RTK",
	"starship":       "INSTALL_STARSHIP",
	"tofu":           "INSTALL_TOFU",
	"uv":             "INSTALL_UV",
	"yq":             "INSTALL_YQ",
	"zsh":            "INSTALL_ZSH",
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
