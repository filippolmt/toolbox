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
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
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
func GlobalConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if home == "" {
		return "", fmt.Errorf("resolve home directory: empty $HOME")
	}
	return home, nil
}

// AtomicWriteFile writes data to dest by creating a temp file in the same
// directory, then renaming it over dest. POSIX guarantees rename(2) is
// atomic within a single filesystem, so a concurrent reader or a crash
// mid-write observes either the prior content or the new content — never a
// truncated/partially-written file. fsync is intentionally omitted: the
// toolbox configuration is user-rewritable, so durability after a power
// failure is not worth the extra IO syscall on every `--create` write.
func AtomicWriteFile(dest string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(dest)
	base := filepath.Base(dest)
	tmp, err := os.CreateTemp(dir, base+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", dest, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write temp for %s: %w", dest, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod temp for %s: %w", dest, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp for %s: %w", dest, err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		cleanup()
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, dest, err)
	}
	return nil
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
	scalar := func() *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	}
	findOrAppendPair(parent, key, scalar, func(existing *yaml.Node) {
		existing.Kind = yaml.ScalarNode
		existing.Tag = "!!str"
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
