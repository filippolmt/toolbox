package configedit

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/filippolmt/toolbox/internal/config"
)

// FileValue is one config file's own, unmerged view of one key: the scalar it
// writes there, and how many entries it holds when the value is a collection.
// The two are exclusive — a scalar counts no entries, a collection has no
// scalar — but they travel together because the caller picks between them by
// the key's shape, not by inspecting the value.
type FileValue struct {
	Scalar  string // the file's scalar value; "" for a collection
	Entries int    // entries the file's value holds; 0 for a scalar
}

// FileValues reports what one config file sets, keyed by the path of the node
// that sets it: a top-level Config Schema key, or a dotted path into a nested
// mapping (worktree.seed). Presence in the map answers "does this layer's file
// set K" — a structural read of the file, not a comparison against the
// defaults, so a file that spells out a default value still counts as setting
// it. A missing file sets nothing and is not an error, so a layer with no file
// reads back as inheriting everything.
//
// A deprecated alias is credited to the live key it folds into, so a file that
// only sets browser_bridge counts as setting bridge. The fold is not performed
// here: config.FoldDeprecatedAliases owns the pairs and the precedence, and this
// reader supplies only its own answer to "does this carrier set the key" —
// which for a file is "is the key written at all".
func FileValues(path string) (map[string]FileValue, error) {
	b, err := readMaybe(path)
	if err != nil {
		return nil, err
	}
	var root yaml.Node
	// The parse error goes back unwrapped, unlike the typed readers above. This
	// one's error reaches `config ui`'s own error line, so wrapping it would
	// change what a user reads — and it is a branch config.Plan reaches first
	// (it parses these same layer files before the per-scope read runs), so the
	// better wording would never be seen and is not worth the change.
	if err := yaml.Unmarshal(b, &root); err != nil {
		return nil, err
	}
	out := map[string]FileValue{}
	if doc := documentMapping(&root); doc != nil {
		collectFileValues(out, "", doc)
		// The fold itself belongs to config, which owns both the alias pairs and
		// the precedence between them; only "what does this carrier call set" is
		// ours to answer.
		config.FoldDeprecatedAliases(
			func(key string) bool { _, ok := out[key]; return ok },
			func(alias, live string) { out[live] = out[alias] },
		)
	}
	return out, nil
}

// documentMapping returns the document's root mapping, or nil when the file
// holds none — empty, comments only, or a document that is not a mapping.
//
// Read-only, and that is the whole difference from configio.EnsureDocumentMap,
// which answers the same question for a writer by *creating* the mapping it
// does not find. A reader must be able to say "this file sets nothing"; a
// writer must always have somewhere to put the key. Neither can serve the
// other, so they stay two functions and this comment says why.
func documentMapping(root *yaml.Node) *yaml.Node {
	node := root
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil
		}
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return nil
	}
	return node
}

// collectFileValues records every key of a mapping under its dotted path, then
// descends into the nested mappings so a caller can ask about a field inside a
// container (worktree.seed) without walking nodes itself.
func collectFileValues(out map[string]FileValue, prefix string, mapping *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i].Value, mapping.Content[i+1]
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		out[path] = FileValue{Scalar: scalarValue(value), Entries: nodeEntries(value)}
		if value.Kind == yaml.MappingNode {
			collectFileValues(out, path, value)
		}
	}
}

func scalarValue(node *yaml.Node) string {
	if node.Kind == yaml.ScalarNode {
		return node.Value
	}
	return ""
}

// nodeEntries counts what a collection node holds: a mapping's key/value pairs
// or a sequence's items. A scalar holds none.
func nodeEntries(node *yaml.Node) int {
	switch node.Kind {
	case yaml.MappingNode:
		return len(node.Content) / 2
	case yaml.SequenceNode:
		return len(node.Content)
	}
	return 0
}

// readMaybe returns a file's bytes, or nil when it does not exist — a missing
// config file is an empty layer, never an error.
func readMaybe(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// UserMountNames returns the name of every named entry in path's mounts:
// list — the candidate set for remove-time suggestions. A missing file
// yields an empty list.
func UserMountNames(path string) ([]string, error) {
	var doc struct {
		Mounts []struct {
			Name string `yaml:"name"`
		} `yaml:"mounts"`
	}
	if err := readYAMLFile(path, &doc); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(doc.Mounts))
	for _, m := range doc.Mounts {
		if m.Name != "" {
			names = append(names, m.Name)
		}
	}
	return names, nil
}

// UserShells reads the shells: block of one config file (name → path) —
// the candidate set for remove-time existence checks and suggestions. A
// missing file yields an empty map.
func UserShells(path string) (map[string]string, error) {
	var doc struct {
		Shells map[string]struct {
			Path string `yaml:"path"`
		} `yaml:"shells"`
	}
	if err := readYAMLFile(path, &doc); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(doc.Shells))
	for n, s := range doc.Shells {
		out[n] = s.Path
	}
	return out, nil
}

// readYAMLFile is the shared single-file reader behind UserMountNames /
// UserShells: missing file decodes as the zero value, anything else
// unmarshals into out.
func readYAMLFile(path string, out any) error {
	b, err := readMaybe(path)
	if err != nil || b == nil {
		return err
	}
	if err := yaml.Unmarshal(b, out); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
