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
// recipe. Only static-port browser-OAuth CLIs belong here: device-code CLIs
// (gh, glab, az, docker) have nothing to forward, and dynamic-port CLIs
// (gcloud, gws, tofu) cannot be pre-bound — cf is the lone dynamic exception,
// handled by a build-time sed patch onto a published range with no bridge.
// Ports are upstream defaults; see docs/runtime-notes.md#loopback-bridge.
var oauthRecipes = map[string]OAuthRecipe{
	"cf":       {Publish: "8877-8886:8877-8886", Bridge: false},
	"codex":    {Publish: "1455:1455", Bridge: true},
	"oci":      {Publish: "8181:8181", Bridge: true},
	"shopify":  {Publish: "13387:13387", Bridge: true},
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
				tool, strings.Join(supportedOAuthTools(), ", "))
		}
		publish = append(publish, recipe.Publish)
		bridge = bridge || recipe.Bridge
	}
	return publish, bridge, nil
}

// supportedOAuthTools returns the recipe map keys sorted for stable error
// messages and help text.
func supportedOAuthTools() []string {
	names := make([]string, 0, len(oauthRecipes))
	for name := range oauthRecipes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
