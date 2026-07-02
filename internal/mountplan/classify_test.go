package mountplan

import (
	"slices"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

// findClassified returns the ClassifiedMount with the given name, or nil.
func findClassified(ms []ClassifiedMount, name string) *ClassifiedMount {
	for i := range ms {
		if ms[i].Name == name {
			return &ms[i]
		}
	}
	return nil
}

// TestClassifyAssignsEveryOrigin is the core contract: a single cfg that
// exercises all four origins at once — an untouched default, a patched
// default, a user mount, and a disabled default — must classify each
// correctly through the one Classify seam.
func TestClassifyAssignsEveryOrigin(t *testing.T) {
	cfg := config.Config{
		Mounts: []config.Mount{
			{Name: "gws", Source: "/custom/gws"},                           // patch a default
			{Name: "docker-sock", Disabled: true},                          // disable a default
			{Name: "scratch", Source: "/host/scratch", Target: "/scratch"}, // user mount
		},
	}

	got, err := Classify(&cfg)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}

	cases := map[string]Origin{
		"claude":      OriginDefault,  // untouched default
		"gws":         OriginPatched,  // default patched by a name-only entry
		"scratch":     OriginUser,     // name absent from defaults
		"docker-sock": OriginDisabled, // default dropped from the resolved set
	}
	for name, want := range cases {
		m := findClassified(got, name)
		if m == nil {
			t.Errorf("mount %q missing from classified set", name)
			continue
		}
		if m.Origin != want {
			t.Errorf("mount %q Origin = %v, want %v", name, m.Origin, want)
		}
	}
}

// TestClassifyDisabledByFeatureToggle: a default dropped by a code-driven
// feature toggle (bridge: false) — not a user disable patch — still surfaces
// as OriginDisabled, matching the pre-refactor list view.
func TestClassifyDisabledByFeatureToggle(t *testing.T) {
	off := false
	cfg := config.Config{Bridge: &off}

	got, err := Classify(&cfg)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	for _, name := range []string{"bridge", "bridge-legacy", "bridge-run"} {
		m := findClassified(got, name)
		if m == nil {
			t.Errorf("dropped default %q missing from classified set", name)
			continue
		}
		if m.Origin != OriginDisabled {
			t.Errorf("mount %q Origin = %v, want OriginDisabled", name, m.Origin)
		}
	}
}

// TestClassifyPropagatesMergeError: Classify wraps Merge, so a merge failure
// (unknown patch name) must surface as an error, not a partial set.
func TestClassifyPropagatesMergeError(t *testing.T) {
	cfg := config.Config{Mounts: []config.Mount{{Name: "nonexistent", Source: "/tmp/x"}}}
	if _, err := Classify(&cfg); err == nil {
		t.Fatal("Classify should propagate Merge's unknown-name error")
	}
}

// TestOriginString pins the canonical tokens, which are documented domain
// vocabulary (mounts list help + docs/mounts.md), not ad-hoc UI strings.
func TestOriginString(t *testing.T) {
	cases := map[Origin]string{
		OriginDefault:  "default",
		OriginPatched:  "patched",
		OriginUser:     "user",
		OriginDisabled: "disabled",
	}
	for o, want := range cases {
		if got := o.String(); got != want {
			t.Errorf("Origin(%d).String() = %q, want %q", o, got, want)
		}
	}
}

// TestNamesReturnsSortedUnion: Names is the disable-validation universe —
// every default plus named user entries, sorted, anonymous excluded.
func TestNamesReturnsSortedUnion(t *testing.T) {
	cfg := config.Config{
		Mounts: []config.Mount{
			{Name: "scratch", Source: "/host/scratch", Target: "/scratch"},
			{Source: "/host/anon", Target: "/anon"}, // anonymous — excluded
		},
	}

	got, err := Names(&cfg)
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	if !slices.IsSorted(got) {
		t.Errorf("Names not sorted: %v", got)
	}
	if !slices.Contains(got, "scratch") {
		t.Error("Names should include the user mount 'scratch'")
	}
	if !slices.Contains(got, "claude") {
		t.Error("Names should include default 'claude'")
	}
	if slices.Contains(got, "") {
		t.Error("Names must exclude anonymous mounts")
	}
	// No duplicates.
	seen := map[string]struct{}{}
	for _, n := range got {
		if _, dup := seen[n]; dup {
			t.Errorf("Names contains duplicate %q", n)
		}
		seen[n] = struct{}{}
	}
}
