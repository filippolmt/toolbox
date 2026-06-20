package build

import (
	"reflect"
	"regexp"
	"sort"
	"testing"
)

// TestModePluginListsAgree enforces that the behavioral-mode plugin list stays
// in sync between the two places it is encoded: the emit_mode_badge calls in
// statusline-command.sh (which render the [PONYTAIL]/[CAVEMAN] badge) and the
// reconciliation pairs in init.d/36-mode-flags.sh (which reap a disabled
// plugin's stale flag). The two use different representations and live in
// separate scripts that deliberately don't share a source — statusline-command.sh
// must stay portable, with no dependency on toolbox image paths. Without this
// test, adding a third mode to one script but not the other would silently break:
// either the badge never renders, or its stale flag is never reaped.
func TestModePluginListsAgree(t *testing.T) {
	read := func(name string) string {
		b, err := Assets.ReadFile(AssetDir + "/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}

	// emit_mode_badge "$CFG/plugins/cache/<name>/...
	badgeSet := matchSet(t, `emit_mode_badge "\$CFG/plugins/cache/([a-z0-9-]+)/`, read("statusline-command.sh"))
	// "<name>@<marketplace>:.<flag>-active"
	flagSet := matchSet(t, `"([a-z0-9-]+)@[a-z0-9-]+:\.[a-z0-9-]+-active"`, read("init.d/36-mode-flags.sh"))

	if len(badgeSet) == 0 {
		t.Fatal("no emit_mode_badge plugins matched in statusline-command.sh — regex drift?")
	}
	if len(flagSet) == 0 {
		t.Fatal("no plugin pairs matched in 36-mode-flags.sh — regex drift?")
	}
	if !reflect.DeepEqual(badgeSet, flagSet) {
		t.Errorf("behavioral-mode plugin lists disagree — add the mode to BOTH scripts:\n  statusline-command.sh (emit_mode_badge): %v\n  36-mode-flags.sh (pairs):                %v",
			keys(badgeSet), keys(flagSet))
	}
}

func matchSet(t *testing.T, pattern, s string) map[string]struct{} {
	t.Helper()
	re := regexp.MustCompile(pattern)
	out := map[string]struct{}{}
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out[m[1]] = struct{}{}
	}
	return out
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
