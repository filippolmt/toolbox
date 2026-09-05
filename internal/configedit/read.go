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
// only sets browser_bridge counts as setting bridge. That fold belongs to the
// load path (config.Merge performs it), and the pairs are asked of
// config.DeprecatedAliases rather than restated here.
func FileValues(path string) (map[string]FileValue, error) {
	var root yaml.Node
	if err := readYAMLFile(path, &root); err != nil {
		return nil, err
	}
	out := map[string]FileValue{}
	if doc := documentMapping(&root); doc != nil {
		collectFileValues(out, "", doc)
		foldDeprecatedAliases(out)
	}
	return out, nil
}

// documentMapping returns the document's root mapping, or nil when the file
// holds none — empty, comments only, or a document that is not a mapping.
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

// foldDeprecatedAliases credits an alias's value to the live key it folds into.
// A file spelling both wins with the live key, matching the load path: the
// alias is a backstop, never an override.
func foldDeprecatedAliases(vals map[string]FileValue) {
	for alias, live := range config.DeprecatedAliases() {
		v, aliased := vals[alias]
		if _, set := vals[live]; aliased && !set {
			vals[live] = v
		}
	}
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
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, out); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
