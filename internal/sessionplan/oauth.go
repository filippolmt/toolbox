package sessionplan

import (
	"fmt"
	"sort"
	"strings"
)

// OAuthRecipe describes the documented OAuth callback recipe for one CLI:
// the docker -p publish spec for its callback listener and whether the
// loopback bridge is required (the CLI binds 127.0.0.1 inside the container,
// so Docker's eth0-delivered forward never reaches it without socat).
type OAuthRecipe struct {
	Publish string // docker -p spec, e.g. "8181:8181" or "8877-8886:8877-8886"
	Bridge  bool   // true → implies --bridge-loopback
}

// oauthRecipes maps tool names accepted by `toolbox shell --oauth` to their
// recipe. Only browser-OAuth CLIs with a known callback port (or range)
// belong here: device-code CLIs (gh, az, docker) have nothing to forward,
// and free-port CLIs with no fixed range (gcloud, gws, tofu) cannot be
// pre-bound. Bridge is only for loopback-binding listeners: oci binds
// 0.0.0.0:8181 (cli_setup_bootstrap.py passes an empty host to HTTPServer),
// so Docker's eth0 forward reaches it directly — and a socat holding
// eth0:8181 would make oci's wildcard bind fail EADDRINUSE (verified live;
// Linux refuses wildcard over a specific bind regardless of SO_REUSEADDR).
// glab binds 0.0.0.0:7171 — same no-bridge reason; socat on eth0:7171 would
// fail EADDRINUSE.
// cf and sonar are loopback-bind ranges: each binds 127.0.0.1 on a port from
// a fixed range (cf: startPort 8877, maxPortAttempts 10; sonar: SonarLint
// Core's EmbeddedServer 64120-64130, which rejects out-of-range callback
// ports) and advertises a `localhost` redirect_uri. The whole range is
// published and bridged — nat.ParsePortSpec expands it per-port. cf carries
// no dist patch (it binds loopback natively post-0.1.0); the historical
// `localhost`→`0.0.0.0` sed was dropped (it poisoned the redirect_uri —
// Cloudflare rejected the `0.0.0.0` callback).
// Ports are upstream defaults; see docs/commands.md#loopback-bridge.
var oauthRecipes = map[string]OAuthRecipe{
	"cf":       {Publish: "8877-8886:8877-8886", Bridge: true},
	"codex":    {Publish: "1455:1455", Bridge: true},
	"glab":     {Publish: "7171:7171", Bridge: false},
	"oci":      {Publish: "8181:8181", Bridge: false},
	"sonar":    {Publish: "64120-64130:64120-64130", Bridge: true},
	"wrangler": {Publish: "8976:8976", Bridge: true},
}

// ExpandOAuth maps tool names to their publish specs and ORed bridge
// requirement. Pure: callers append the specs to any explicit --publish
// values and OR the bridge bit into --bridge-loopback, so expansion only
// ever adds to what the user asked for. An unknown name is a hard error
// listing the sorted supported tools — silently ignoring it would create
// a container with wrong bindings that needs `toolbox stop` to fix.
func ExpandOAuth(tools []string) (publish []string, bridge bool, err error) {
	for _, tool := range tools {
		recipe, ok := oauthRecipes[tool]
		if !ok {
			return nil, false, fmt.Errorf(
				"unknown --oauth tool %q: supported tools are %s",
				tool, strings.Join(SupportedOAuthTools(), ", "))
		}
		publish = append(publish, recipe.Publish)
		bridge = bridge || recipe.Bridge
	}
	return publish, bridge, nil
}

// SupportedOAuthTools returns the recipe map keys sorted for stable error
// messages and help text. Exported so cmd can render the supported set in
// --oauth help strings without hardcoding a second copy of the list.
func SupportedOAuthTools() []string {
	names := make([]string, 0, len(oauthRecipes))
	for name := range oauthRecipes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
