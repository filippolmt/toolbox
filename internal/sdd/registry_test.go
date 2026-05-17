package sdd

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"testing"
)

// TestSkillEnvKey locks in the encode contract: SkillEnvKey concatenates
// the prefix + UPPERCASE(key) + field with no separator drift between the
// encode site (sessionplan.sddEnv) and the decode site (bash loop in
// entrypoint.sh).
func TestSkillEnvKey(t *testing.T) {
	cases := map[string]string{
		EnvFieldPkg:     "TOOLBOX_SDD_GSD_PKG",
		EnvFieldVersion: "TOOLBOX_SDD_GSD_VERSION",
		EnvFieldBin:     "TOOLBOX_SDD_GSD_BIN",
		EnvFieldSteps:   "TOOLBOX_SDD_GSD_STEPS",
		EnvFieldMarker:  "TOOLBOX_SDD_GSD_MARKER",
	}
	for field, want := range cases {
		if got := SkillEnvKey("gsd", field); got != want {
			t.Errorf("SkillEnvKey(\"gsd\", %q) = %q, want %q", field, got, want)
		}
	}
}

// TestLookupReturnsRegisteredSkills spot-checks the registry surface:
// every documented key is reachable via Lookup, and an unknown key fails.
func TestLookupReturnsRegisteredSkills(t *testing.T) {
	for _, key := range []string{"bmad", "gsd", "openspec"} {
		if _, ok := Lookup(key); !ok {
			t.Errorf("Lookup(%q) reported missing — registry drifted", key)
		}
	}
	if _, ok := Lookup("nonsense"); ok {
		t.Error("Lookup(nonsense) reported present — should be empty")
	}
}

// TestKeysMatchesSkills asserts Keys() mirrors the Skills slice in order.
// The cmd/sdd.go usage error formats Keys() into its message, so a silent
// reorder would change user-visible output.
func TestKeysMatchesSkills(t *testing.T) {
	keys := Keys()
	if len(keys) != len(Skills) {
		t.Fatalf("Keys() len=%d, Skills len=%d", len(keys), len(Skills))
	}
	for i, s := range Skills {
		if keys[i] != s.Key {
			t.Errorf("Keys()[%d] = %q, want %q", i, keys[i], s.Key)
		}
	}
}

// TestRenovateRegexMatchesEverySDDPin guards an invisible cross-file
// invariant: the Renovate customManagers in renovate.json bump each SDD
// pin via the regex `NpmPackage:\s*"<pkg>"[^}]*?Version:\s*"<v>"`. The
// `[^}]*?` excludes `}` to stop the match from crossing into the next
// Skill struct, which means `Version` MUST follow `NpmPackage` inside
// each row with no `}` in between (i.e. no InstallSteps before Version).
// A field reorder that violates the layout silently disables Renovate
// for that pin — the npm dependency stops getting bumped. This test
// re-runs every Renovate regex against the live source file so the
// breakage surfaces in CI instead of months later when a security
// patch fails to land.
func TestRenovateRegexMatchesEverySDDPin(t *testing.T) {
	src, err := os.ReadFile("registry.go")
	if err != nil {
		t.Fatalf("read registry.go: %v", err)
	}

	rnv, err := os.ReadFile("../../renovate.json")
	if err != nil {
		t.Fatalf("read renovate.json: %v", err)
	}
	var doc struct {
		CustomManagers []struct {
			ManagerFilePatterns []string `json:"managerFilePatterns"`
			MatchStrings        []string `json:"matchStrings"`
			DepNameTemplate     string   `json:"depNameTemplate"`
		} `json:"customManagers"`
	}
	if err := json.Unmarshal(rnv, &doc); err != nil {
		t.Fatalf("parse renovate.json: %v", err)
	}

	pinByPkg := map[string]string{}
	for _, s := range Skills {
		pinByPkg[s.NpmPackage] = s.Version
	}

	covered := map[string]bool{}
	managersForRegistry := 0
	for _, cm := range doc.CustomManagers {
		if !slices.Contains(cm.ManagerFilePatterns, "/^internal/sdd/registry\\.go$/") {
			continue
		}
		managersForRegistry++
		for _, ms := range cm.MatchStrings {
			re, err := regexp.Compile(ms)
			if err != nil {
				t.Errorf("renovate matchString %q does not compile: %v", ms, err)
				continue
			}
			m := re.FindStringSubmatch(string(src))
			if m == nil {
				t.Errorf("renovate matchString %q matches nothing in registry.go (depName=%q) — field reorder or syntax drift broke the bump", ms, cm.DepNameTemplate)
				continue
			}
			i := re.SubexpIndex("currentValue")
			if i < 0 {
				t.Errorf("renovate matchString %q lacks a (?P<currentValue>...) group", ms)
				continue
			}
			got := m[i]
			want, ok := pinByPkg[cm.DepNameTemplate]
			if !ok {
				t.Errorf("renovate manager points at depName %q which is not in registry.Skills", cm.DepNameTemplate)
				continue
			}
			if got != want {
				t.Errorf("renovate captured version %q for %q, registry pins %q — regex captures wrong field", got, cm.DepNameTemplate, want)
			}
			covered[cm.DepNameTemplate] = true
		}
	}

	if managersForRegistry == 0 {
		t.Fatalf("no Renovate customManager targets internal/sdd/registry.go — the managerFilePatterns syntax likely drifted (expected exact pattern %q)", "/^internal/sdd/registry\\.go$/")
	}

	for pkg := range pinByPkg {
		if !covered[pkg] {
			t.Errorf("registry package %q has no Renovate customManager — pin will never auto-bump", pkg)
		}
	}
}
