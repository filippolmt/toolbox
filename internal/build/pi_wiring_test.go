package build

import (
	"strings"
	"testing"
)

// TestInitDWiresPiEverywhereItWiresCodex pins the third agent into the init.d
// member that wires agents but has no test of its own. herdr's half lives in
// TestHerdrInitWiresPi; this covers atuin.
//
// The failure it exists to catch is silent by construction. pi is a first-class
// agent — internal/catalog bundles it, config.SupportedAgents declares it
// one — and none of that makes atuin's history
// capture see it. Drop this block and pi still launches, still resumes, and
// quietly runs unwired.
//
// The gate is the ~/.pi bind mount, never ~/.pi/agent: pi creates that itself
// on first run whether or not the state is mounted, so a hook installed behind
// the wrong gate is thrown away by the next `toolbox stop` — and in atuin's
// case the marker recording the install, which lives in the state volume,
// outlives it, so the retry never fires.
func TestInitDWiresPiEverywhereItWiresCodex(t *testing.T) {
	for _, tc := range []struct {
		script string
		wants  []string
	}{
		{
			script: "assets/init.d/65-atuin.sh",
			wants: []string{
				`_pi_marker="$_atuin_hooks_dir/pi-${_atuin_key}"`,
				`[ ! -f "$_pi_marker" ] && toolbox_agent_available pi && toolbox_agent_persistent pi`,
				`atuin setup --only --agent-history --agents pi`,
			},
		},
	} {
		b, err := Assets.ReadFile(tc.script)
		if err != nil {
			t.Fatalf("read %s: %v", tc.script, err)
		}
		s := string(b)
		for _, want := range tc.wants {
			if !strings.Contains(s, want) {
				t.Errorf("%s is missing %q", tc.script, want)
			}
		}
		// No lock. Each agent gets its own file under ~/.pi/agent/extensions/,
		// so there is no shared writer to serialise.
		if strings.Contains(s, "flock 200\n        atuin setup --only --agent-history --agents pi") {
			t.Errorf("%s locks the pi install; it writes no shared file", tc.script)
		}
	}
}
