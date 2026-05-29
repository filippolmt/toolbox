// Package configio owns side-effecting reads and writes against the user's
// host-side toolbox configuration files. It is the single seam that knows
//
//   - where ~/.toolbox.yaml lives (GlobalConfigPath),
//   - how to durably rewrite a host file without truncating the prior
//     content on crash (AtomicWriteFile),
//   - how to mutate a parsed yaml.Node tree in-place while preserving the
//     user's comments and key order (EnsureDocumentMap / EnsureChildMap /
//     SetMapValue).
//
// internal/config owns the *read* pipeline (Plan + Merge). configio is the
// complementary *write* pipeline used by cmd/* whenever a subcommand has to
// edit the user's YAML in place (e.g. `toolbox shell <name> --create`).
package configio

import (
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// GlobalConfigPath returns the absolute path of the user's global
// ~/.toolbox.yaml. The location is the single source of truth shared with
// internal/config/plan.go's global-config read; both call sites must agree
// or a write here would not be visible to the next read.
func GlobalConfigPath() (string, error) {
	home, err := GlobalConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".toolbox.yaml"), nil
}

// GlobalConfigDir returns the directory containing the global config
// (today: the user's home directory). Exposed so callers that need a
// writable sibling for an atomic temp file can avoid resolving HOME twice.
// Thin facade over fsx.Home so the strict resolution lives in one place.
func GlobalConfigDir() (string, error) {
	return fsx.Home()
}

// AtomicWriteFile durably rewrites a host config file without truncating the
// prior content on crash. Facade over fsx.AtomicWriteFile, retained so
// configio callers (cmd/sdd, cmd/shell_named) keep a config-scoped entry
// point; the crash-safe temp-write-then-rename implementation lives once in
// fsx.
func AtomicWriteFile(dest string, data []byte, mode os.FileMode) error {
	return fsx.AtomicWriteFile(dest, data, mode)
}

// EnsureDocumentMap returns the mapping node that holds the top-level
// document keys. Yaml.v3 unmarshals an empty/missing file into a
// zero-valued yaml.Node (Kind == 0); callers want a usable mapping in
// either case, so we materialise one in place.
func EnsureDocumentMap(root *yaml.Node) *yaml.Node {
	if root.Kind == 0 {
		root.Kind = yaml.DocumentNode
	}
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 || root.Content[0] == nil {
			root.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
		}
		if root.Content[0].Kind != yaml.MappingNode {
			root.Content[0] = &yaml.Node{Kind: yaml.MappingNode}
		}
		return root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		root.Kind = yaml.MappingNode
		root.Content = nil
	}
	return root
}

// EnsureChildMap looks up key on parent and returns its mapping-node value,
// creating an empty mapping if the key is absent or its value is not a
// mapping. Used to descend into nested sub-blocks (e.g. shells -> <name>)
// while preserving any sibling keys and their comments.
func EnsureChildMap(parent *yaml.Node, key string) *yaml.Node {
	return findOrAppendPair(parent, key, func() *yaml.Node { return &yaml.Node{Kind: yaml.MappingNode} },
		func(existing *yaml.Node) {
			if existing.Kind != yaml.MappingNode {
				existing.Kind = yaml.MappingNode
				existing.Content = nil
			}
		})
}

// SetMapValue upserts key=value on parent, replacing an existing scalar
// value in place (preserving sibling key order and comments) or appending
// a fresh key/value pair to the end.
func SetMapValue(parent *yaml.Node, key, value string) {
	setScalar(parent, key, "!!str", value)
}

// SetMapBool is the bool sibling of SetMapValue. Tagged !!bool so yaml.v3
// emits `true`/`false` unquoted (without the tag, scalar "true" round-trips
// as the string "true", which viper accepts on read but reads as a stringly-
// typed bool).
func SetMapBool(parent *yaml.Node, key string, value bool) {
	setScalar(parent, key, "!!bool", strconv.FormatBool(value))
}

func setScalar(parent *yaml.Node, key, tag, value string) {
	make := func() *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
	}
	findOrAppendPair(parent, key, make, func(existing *yaml.Node) {
		existing.Kind = yaml.ScalarNode
		existing.Tag = tag
		existing.Value = value
		existing.Content = nil
	})
}

// findOrAppendPair is the shared scan+upsert loop used by EnsureChildMap
// and SetMapValue. normalize coerces parent into a mapping node; if a pair
// with key already exists, prepare runs against the existing value node
// and the value is returned. Otherwise make builds a fresh value node and
// the pair is appended. The mapping invariant (even-length Content) is
// re-established before the scan so an upstream caller leaving an orphan
// key does not corrupt the result.
func findOrAppendPair(
	parent *yaml.Node,
	key string,
	make func() *yaml.Node,
	prepare func(existing *yaml.Node),
) *yaml.Node {
	if parent.Kind != yaml.MappingNode {
		parent.Kind = yaml.MappingNode
		parent.Content = nil
	}
	if len(parent.Content)%2 != 0 {
		parent.Content = parent.Content[:len(parent.Content)-1]
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		k := parent.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			v := parent.Content[i+1]
			prepare(v)
			return v
		}
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	v := make()
	parent.Content = append(parent.Content, k, v)
	return v
}
