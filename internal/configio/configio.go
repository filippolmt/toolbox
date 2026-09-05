// Package configio owns side-effecting reads and writes against the user's
// host-side toolbox configuration files. It is the single seam that knows
//
//   - where ~/.toolbox.yaml lives (GlobalConfigPath),
//   - how to mutate a parsed yaml.Node tree in-place while preserving the
//     user's comments and key order (EnsureDocumentMap / EnsureChildMap /
//     SetMapValue).
//
// internal/config owns the *read* pipeline (Plan + Merge). configio supplies
// the primitives a write is built from — it deliberately owns no write path of
// its own: config files are only ever written through
// configedit.ApplyChecked, which is what makes the doctor gate unbypassable.
package configio

import (
	"bytes"
	"errors"
	"fmt"
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
	home, err := fsx.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".toolbox.yaml"), nil
}

// ReadMaybe returns a config file's bytes and whether it existed — the read
// counterpart of fsx.AtomicWriteFile, for callers that render a candidate
// document before deciding to write it. A missing file is not an error
// (existed=false), which is how an absent config layer reads as an empty
// document.
func ReadMaybe(path string) (data []byte, existed bool, err error) {
	b, err := os.ReadFile(path) //nolint:gosec // path is a resolved config file
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	return b, true, nil
}

// RenderDocument is the in-memory half of a config write: it parses src
// (missing/whitespace-only bootstraps an empty document), hands the top-level
// document mapping to mutate, and returns the re-encoded bytes — the exact
// bytes a write would put on disk for the same input. name only labels errors.
//
// A nil mutate renders src unchanged, which is how a caller obtains the
// encoder's own rendering of the input: the baseline to compare a mutated
// rendering against, with no formatting noise from the encoder itself.
//
// Each call parses src afresh, so the returned bytes never alias a node tree
// the write path owns.
func RenderDocument(name string, src []byte, mutate func(doc *yaml.Node)) ([]byte, error) {
	var root yaml.Node
	if len(bytes.TrimSpace(src)) > 0 {
		if err := yaml.Unmarshal(src, &root); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
	}
	doc := EnsureDocumentMap(&root)
	if mutate != nil {
		mutate(doc)
	}

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return nil, fmt.Errorf("encode %s: %w", name, err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode %s: %w", name, err)
	}
	return out.Bytes(), nil
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

// ChildValue returns the value node paired with key on parent, or nil when
// parent is not a mapping or the key is absent. Read-only sibling of
// EnsureChildMap for callers that must inspect a value's shape before
// deciding whether to mutate it (e.g. cmd/sdd refusing to clobber an
// object-form sdd.<key> entry with the bool shorthand).
func ChildValue(parent *yaml.Node, key string) *yaml.Node {
	if i := mapPairIndex(parent, key); i >= 0 {
		return parent.Content[i+1]
	}
	return nil
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
	if i := mapPairIndex(parent, key); i >= 0 {
		v := parent.Content[i+1]
		prepare(v)
		return v
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	v := make()
	parent.Content = append(parent.Content, k, v)
	return v
}
