package config

import (
	"slices"
	"testing"
)

// TestEveryKeyHasACompleteRow is the one guard behind the key rows, and it
// replaced six: a per-surface presence test in config (docs, validators,
// ValidateKey reachability, effective fallbacks) and in configui (a descriptor
// row, complete for its editor kind) each asserted one column of this table
// from a different package, because each surface held its own copy of it.
// There is one copy now, so there is one guard: every schema key has a row, and
// the row carries what its Kind promises.
func TestEveryKeyHasACompleteRow(t *testing.T) {
	rows := Keys()
	names := make([]string, 0, len(rows))
	for _, k := range rows {
		names = append(names, k.Name)
	}
	if schema := SchemaKeys(); !slices.Equal(names, schema) {
		t.Fatalf("key rows drifted from the schema:\n  rows   %v\n  schema %v", names, schema)
	}

	aliases := DeprecatedAliases()
	for _, k := range rows {
		if k.Kind == KindAlias {
			if _, ok := aliases[k.Name]; !ok {
				t.Errorf("row %q is declared an alias but config.DeprecatedAliases does not fold it into a live key", k.Name)
			}
			// An alias is input-only: the load path folds it, and no surface
			// presents it — so it must carry nothing a surface could present.
			if k.Editor != EditorNone || k.Summary != "" || k.Default != "" || k.Example != "" ||
				k.Effective != nil || k.Validate != nil || k.Scalar != nil {
				t.Errorf("alias row %q carries surface facts; only the live key it folds into is presented", k.Name)
			}
			continue
		}
		if _, ok := aliases[k.Name]; ok {
			t.Errorf("row %q is a deprecated alias but is not declared KindAlias", k.Name)
		}
		if k.Summary == "" || k.Default == "" || k.Example == "" {
			t.Errorf("row %q must carry a Summary, a Default and an Example — `config ui` and `config example` show them", k.Name)
		}
		if k.Editor == EditorNone {
			t.Errorf("row %q opens no editor: `config ui` would show it read-only", k.Name)
		}
		checkRenderReader(t, k)
		checkEditorReader(t, k)
	}
}

// checkEditorReader asserts a row carries the reader the editor it names is
// seeded from — the two are separate fields, so a row can claim an editor while
// carrying nothing that fills it, which shows up as an empty pane rather than
// as a value to edit.
func checkEditorReader(t *testing.T, k Key) {
	t.Helper()
	switch k.Editor {
	case EditorChoice, EditorText:
		if k.Str == nil {
			t.Errorf("row %q opens a scalar editor with no Str to prefill it", k.Name)
		}
	case EditorTri:
		if k.Tri == nil {
			t.Errorf("row %q opens a tri-state editor with no Tri to read", k.Name)
		}
	case EditorRows:
		if k.Pairs == nil && k.List == nil {
			t.Errorf("row %q opens a rows editor with neither Pairs nor List to fill it", k.Name)
		}
	case EditorSet:
		// A multi-select is seeded from the option set and the checked set,
		// both of which are configui's (one of them cannot leave mountplan).
	}
}

// checkRenderReader asserts a row carries the reader its Kind is presented
// through, and that the validation it declares matches that Kind: a scalar can
// always be judged from a lone value, a bool has no invalid value to judge, and
// only a scalar can have a fallback (a collection's empty state is its own
// value).
func checkRenderReader(t *testing.T, k Key) {
	t.Helper()
	switch k.Kind {
	case KindEnum, KindScalar:
		if k.Str == nil {
			t.Errorf("scalar row %q has no Str reader", k.Name)
		}
		if k.Scalar == nil {
			t.Errorf("scalar row %q declares no fail-fast verdict, so `config set --%s` would accept anything", k.Name, k.Name)
		}
	case KindTri, KindBool:
		if k.Tri == nil {
			t.Errorf("bool row %q has no Tri reader", k.Name)
		}
		if k.Scalar != nil {
			t.Errorf("bool row %q declares a fail-fast verdict, but no value of a toggle is invalid", k.Name)
		}
	case KindMap:
		if k.Pairs == nil {
			t.Errorf("map row %q has no Pairs reader", k.Name)
		}
	case KindList:
		if k.List == nil {
			t.Errorf("list row %q has no List reader", k.Name)
		}
	case KindBlock:
		// A block key's entries carry more than one field, so each surface
		// shapes them itself; the row promises no single reader.
	default:
		t.Errorf("row %q has no value shape — classify it with a Kind", k.Name)
	}
	if k.Effective != nil && k.Kind != KindEnum && k.Kind != KindScalar {
		t.Errorf("row %q declares a fallback, but only a scalar has one", k.Name)
	}
}

// TestKeyByNameIgnoresAnUnknownKey: the row lookup is the seam every surface
// asks a key about, so an unknown key must report itself as unknown rather than
// hand back a zero row that reads as a valid one.
func TestKeyByNameIgnoresAnUnknownKey(t *testing.T) {
	if k, ok := KeyByName("no_such_key"); ok {
		t.Errorf("KeyByName reported an unknown key as known: %+v", k)
	}
	k, ok := KeyByName("pull")
	if !ok || k.Name != "pull" || k.Kind != KindEnum {
		t.Errorf("KeyByName(\"pull\") = (%+v, %v), want the pull row", k, ok)
	}
}
