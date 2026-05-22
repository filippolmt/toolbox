package catalog_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/catalog"
)

// TestCatalogShape asserts every Entry has a non-empty Key. InitScript may
// be populated; when set it must end in ".sh". Description and SmokeTest are
// reserved for future use and must stay zero-valued.
func TestCatalogShape(t *testing.T) {
	if len(catalog.Entries) == 0 {
		t.Fatal("catalog.Entries must not be empty")
	}
	for i, e := range catalog.Entries {
		if e.Key == "" {
			t.Errorf("entry[%d]: Key must be non-empty (got %+v)", i, e)
		}
		if e.Description != "" || e.SmokeTest != "" {
			t.Errorf("entry[%d] %q: Description and SmokeTest must be zero; got Description=%q SmokeTest=%q",
				i, e.Key, e.Description, e.SmokeTest)
		}
		if e.InitScript != "" && !strings.HasSuffix(e.InitScript, ".sh") {
			t.Errorf("entry[%d] %q: InitScript must end in \".sh\" when populated; got %q",
				i, e.Key, e.InitScript)
		}
	}
}

// TestCatalogAlphabeticalByKey enforces the alphabetical-by-Key invariant.
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

// TestFindByKey spot-checks the lookup accessor.
func TestFindByKey(t *testing.T) {
	if entry, ok := catalog.Find("rtk"); !ok || entry.Key != "rtk" {
		t.Errorf("Find(\"rtk\") = %+v, %v; want Entry with Key=\"rtk\", true", entry, ok)
	}
	if _, ok := catalog.Find("no-such-tool"); ok {
		t.Error("Find(\"no-such-tool\") should report not found")
	}
}

// TestHostAuthMountWellFormed asserts every populated HostAuthMount has
// non-empty HostPath and ContainerPath, and that ContainerPath is absolute.
func TestHostAuthMountWellFormed(t *testing.T) {
	var found int
	for _, e := range catalog.Entries {
		if e.HostAuthMount == nil {
			continue
		}
		found++
		if e.HostAuthMount.HostPath == "" {
			t.Errorf("entry %q: HostAuthMount.HostPath must be non-empty", e.Key)
		}
		if e.HostAuthMount.ContainerPath == "" {
			t.Errorf("entry %q: HostAuthMount.ContainerPath must be non-empty", e.Key)
		}
		if !strings.HasPrefix(e.HostAuthMount.ContainerPath, "/") {
			t.Errorf("entry %q: HostAuthMount.ContainerPath must be absolute, got %q",
				e.Key, e.HostAuthMount.ContainerPath)
		}
	}
	if found == 0 {
		t.Error("expected at least one Entry with HostAuthMount populated")
	}
}

// TestHostAuthEligibleKeysSorted asserts the helper returns keys sorted.
func TestHostAuthEligibleKeysSorted(t *testing.T) {
	keys := catalog.HostAuthEligibleKeys()
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(keys, sorted) {
		t.Errorf("HostAuthEligibleKeys() not sorted:\n  got:  %v\n want: %v", keys, sorted)
	}
}
