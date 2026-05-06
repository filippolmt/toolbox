package catalog_test

import (
	"bytes"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/config"
)

// TestCatalogShape asserts every Entry has non-empty Key + BuildArg and
// leaves the optional Phase 09/10 fields zero-valued (D-08).
func TestCatalogShape(t *testing.T) {
	if len(catalog.Entries) == 0 {
		t.Fatal("catalog.Entries must not be empty")
	}
	for i, e := range catalog.Entries {
		if e.Key == "" || e.BuildArg == "" {
			t.Errorf("entry[%d]: Key and BuildArg must be non-empty (got %+v)", i, e)
		}
		if e.Description != "" || e.InitScript != "" || e.SmokeTest != "" {
			t.Errorf("entry[%d] %q: optional fields must be zero in Phase 07 (D-08); got Description=%q InitScript=%q SmokeTest=%q",
				i, e.Key, e.Description, e.InitScript, e.SmokeTest)
		}
	}
}

// TestCatalogAlphabeticalByKey enforces the alphabetical-by-Key invariant
// inherited from internal/config/tools.go:5 and cited by Pattern 07-PATTERNS.
func TestCatalogAlphabeticalByKey(t *testing.T) {
	keys := make([]string, len(catalog.Entries))
	for i, e := range catalog.Entries {
		keys[i] = e.Key
	}
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(keys, sorted) {
		t.Errorf("catalog.Entries must be alphabetical by Key:\n  got: %v\n want: %v", keys, sorted)
	}
}

// TestCatalogContainsLegacyKnownTools is the bridging invariant during the
// Phase 07 migration: the catalog Entry-key set must equal the legacy
// config.KnownTools set exactly. REMOVED in Plan 07-03 BEFORE the legacy
// literals are deleted, to keep every intermediate commit compiling.
func TestCatalogContainsLegacyKnownTools(t *testing.T) {
	catKeys := map[string]struct{}{}
	for _, e := range catalog.Entries {
		catKeys[e.Key] = struct{}{}
	}
	for _, k := range config.KnownTools {
		if _, ok := catKeys[k]; !ok {
			t.Errorf("catalog missing legacy KnownTools entry %q", k)
		}
	}
	if len(catKeys) != len(config.KnownTools) {
		t.Errorf("catalog has %d entries, legacy KnownTools has %d — sets must match exactly",
			len(catKeys), len(config.KnownTools))
	}
}

// TestCatalogBuildArgMatchesLegacyMap is the second bridging invariant:
// every Entry.BuildArg matches the legacy config.ToolBuildArg map. REMOVED
// in Plan 07-03 alongside TestCatalogContainsLegacyKnownTools.
func TestCatalogBuildArgMatchesLegacyMap(t *testing.T) {
	for _, e := range catalog.Entries {
		want, ok := config.ToolBuildArg[e.Key]
		if !ok {
			t.Errorf("catalog entry %q has no legacy ToolBuildArg mapping", e.Key)
			continue
		}
		if e.BuildArg != want {
			t.Errorf("catalog entry %q BuildArg=%q, legacy ToolBuildArg[%q]=%q",
				e.Key, e.BuildArg, e.Key, want)
		}
	}
}

// TestKeysReturnsAllEntries asserts catalog.Keys() returns one string per
// Entry, in catalog (alphabetical) order.
func TestKeysReturnsAllEntries(t *testing.T) {
	keys := catalog.Keys()
	if len(keys) != len(catalog.Entries) {
		t.Fatalf("Keys() len=%d, want %d", len(keys), len(catalog.Entries))
	}
	for i, k := range keys {
		if k != catalog.Entries[i].Key {
			t.Errorf("Keys()[%d]=%q, Entries[%d].Key=%q", i, k, i, catalog.Entries[i].Key)
		}
	}
}

// TestBuildArgLookup spot-checks the Key→BuildArg accessor and the empty
// fallback for unknown tools.
func TestBuildArgLookup(t *testing.T) {
	if got := catalog.BuildArg("rtk"); got != "INSTALL_RTK" {
		t.Errorf("BuildArg(\"rtk\") = %q, want \"INSTALL_RTK\"", got)
	}
	if got := catalog.BuildArg("no-such-tool"); got != "" {
		t.Errorf("BuildArg(\"no-such-tool\") = %q, want \"\"", got)
	}
}

// TestDefaultsAllEnabled asserts catalog.Defaults() returns a map with one
// entry per Entry, all true (Phase 07 ships every Entry.Default = true).
func TestDefaultsAllEnabled(t *testing.T) {
	d := catalog.Defaults()
	if len(d) != len(catalog.Entries) {
		t.Fatalf("Defaults() len=%d, want %d", len(d), len(catalog.Entries))
	}
	for k, v := range d {
		if !v {
			t.Errorf("Defaults()[%q] = false, want true (Phase 07 ships every Entry.Default = true)", k)
		}
	}
}

// TestIsDefaultMatchesLegacy mirrors legacy config.IsDefaultTools semantics:
// missing key = enabled, explicit false = non-default.
func TestIsDefaultMatchesLegacy(t *testing.T) {
	if !catalog.IsDefault(catalog.Defaults()) {
		t.Error("IsDefault(Defaults()) must be true")
	}
	if !catalog.IsDefault(map[string]bool{}) {
		t.Error("IsDefault(empty map) must be true (missing key = enabled)")
	}
	if catalog.IsDefault(map[string]bool{"rtk": false}) {
		t.Error("IsDefault({rtk: false}) must be false")
	}
}

// TestCanonicalEncodingDeterministic asserts WriteCanonical produces the
// same bytes across two calls and emits one line per Entry in catalog
// (alphabetical) order.
func TestCanonicalEncodingDeterministic(t *testing.T) {
	m := catalog.Defaults()
	var b1, b2 bytes.Buffer
	if err := catalog.WriteCanonical(&b1, m); err != nil {
		t.Fatalf("WriteCanonical #1: %v", err)
	}
	if err := catalog.WriteCanonical(&b2, m); err != nil {
		t.Fatalf("WriteCanonical #2: %v", err)
	}
	if !bytes.Equal(b1.Bytes(), b2.Bytes()) {
		t.Errorf("WriteCanonical not deterministic:\n  #1: %q\n  #2: %q", b1.String(), b2.String())
	}
	// Output must be sorted by Key — every line starts with the entry key after "tool:".
	lines := strings.Split(strings.TrimRight(b1.String(), "\n"), "\n")
	if len(lines) != len(catalog.Entries) {
		t.Fatalf("WriteCanonical wrote %d lines, want %d", len(lines), len(catalog.Entries))
	}
	for i, line := range lines {
		want := "tool:" + catalog.Entries[i].Key + "|"
		if !strings.HasPrefix(line, want) {
			t.Errorf("line[%d] = %q, want prefix %q (alphabetical by Key)", i, line, want)
		}
	}
}

// TestCanonicalEncodingIsNeutralToOptionalFieldPopulation is the D-10
// mutation test: it constructs two test-local []Entry slices with
// identical Key / Default / BuildArg fields. The "bare" slice leaves
// Description / InitScript / SmokeTest at zero values; the "populated"
// slice fills them with non-empty strings. Calling WriteCanonicalEntries
// on each must produce byte-identical output.
//
// This test FAILS if a future contributor adds Description / InitScript /
// SmokeTest to the canonical encoding format — exactly the regression
// D-10 forbids. The previous string-grep formulation (assert "Description"
// not in output) could not catch a new encoder format like `desc:%s` that
// omits the field name; the mutation form is structurally complete.
func TestCanonicalEncodingIsNeutralToOptionalFieldPopulation(t *testing.T) {
	enabled := map[string]bool{"foo": true, "bar": false}

	bareEntries := []catalog.Entry{
		{Key: "bar", Default: false, BuildArg: "INSTALL_BAR"},
		{Key: "foo", Default: true, BuildArg: "INSTALL_FOO"},
	}
	populatedEntries := []catalog.Entry{
		{Key: "bar", Default: false, BuildArg: "INSTALL_BAR",
			Description: "another description",
			InitScript:  "init-bar.sh",
			SmokeTest:   "test-bar"},
		{Key: "foo", Default: true, BuildArg: "INSTALL_FOO",
			Description: "this is a description",
			InitScript:  "init-foo.sh",
			SmokeTest:   "test-foo"},
	}

	var bareBuf, populatedBuf bytes.Buffer
	if err := catalog.WriteCanonicalEntries(&bareBuf, bareEntries, enabled); err != nil {
		t.Fatalf("WriteCanonicalEntries(bare): %v", err)
	}
	if err := catalog.WriteCanonicalEntries(&populatedBuf, populatedEntries, enabled); err != nil {
		t.Fatalf("WriteCanonicalEntries(populated): %v", err)
	}

	if !bytes.Equal(bareBuf.Bytes(), populatedBuf.Bytes()) {
		t.Errorf("D-10 violated: optional Entry fields shifted canonical encoding\n"+
			"  bare:      %q\n"+
			"  populated: %q\n"+
			"WriteCanonicalEntries MUST NOT serialise Description / InitScript / SmokeTest.",
			bareBuf.String(), populatedBuf.String())
	}
}
