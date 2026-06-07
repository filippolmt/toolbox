package configedit

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configio"
)

// headerComment is prepended (as a `#` block) to config files created by a
// writer command, pointing the user at the discovery surfaces. Lives here
// (not configio) so the low-level writer stays policy-free (D7).
const headerComment = `.toolbox.yaml — toolbox configuration.
Run 'toolbox config example' for an annotated template covering every field.
Docs: https://github.com/filippolmt/toolbox`

// Upsert is the header-aware wrapper every configedit writer goes through:
// when path does not exist yet, the new file starts with headerComment;
// otherwise it delegates straight to configio.UpsertFile (comment-preserving,
// atomic, idempotent — changed=false when the rendered bytes match disk).
func Upsert(path string, mutate func(doc *yaml.Node)) (changed bool, err error) {
	_, statErr := os.Stat(path)
	creating := errors.Is(statErr, os.ErrNotExist)
	return configio.UpsertFile(path, func(doc *yaml.Node) {
		if creating && doc.HeadComment == "" {
			doc.HeadComment = headerComment
		}
		mutate(doc)
	})
}

// EnsureFileWithHeader creates path containing only the documentation
// header when it does not exist yet. Used by `config edit` so the editor
// opens a file with discovery pointers instead of a blank page (a
// comment-only file is valid YAML and an empty document to the loader).
func EnsureFileWithHeader(path string) error {
	_, err := os.Stat(path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var b strings.Builder
	for line := range strings.SplitSeq(headerComment, "\n") {
		b.WriteString("# ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return configio.AtomicWriteFile(path, []byte(b.String()), 0o600)
}

// =============================================================================
// Shells writers
// =============================================================================

// SetShell upserts shells.<name>.path on the file at path, preserving any
// sibling keys (env overlays survive a path change).
func SetShell(path, name, shellPath string) (bool, error) {
	return Upsert(path, func(doc *yaml.Node) {
		entry := configio.EnsureChildMap(configio.EnsureChildMap(doc, "shells"), name)
		configio.SetMapValue(entry, "path", shellPath)
	})
}

// SetShellEnv upserts shells.<name>.env.<K>=<V> for every pair in env,
// applied in sorted key order so repeated runs render identically. Callers
// validate keys (config.ValidateEnv) before writing.
func SetShellEnv(path, name string, env map[string]string) (bool, error) {
	return Upsert(path, func(doc *yaml.Node) {
		entry := configio.EnsureChildMap(configio.EnsureChildMap(doc, "shells"), name)
		envMap := configio.EnsureChildMap(entry, "env")
		for _, k := range slices.Sorted(maps.Keys(env)) {
			configio.SetMapValue(envMap, k, env[k])
		}
	})
}

// RemoveShell deletes the shells.<name> entry from the file at path. A
// shells: map left empty by the removal is dropped entirely. changed=false
// means the entry was not present.
func RemoveShell(path, name string) (bool, error) {
	return Upsert(path, func(doc *yaml.Node) {
		shellsMap := configio.ChildValue(doc, "shells")
		if !removeMapKey(shellsMap, name) {
			return
		}
		if len(shellsMap.Content) == 0 {
			removeMapKey(doc, "shells")
		}
	})
}

// =============================================================================
// Mounts writers
// =============================================================================

// AddMount writes the replace/append form (name + source + target, optional
// readonly) to the mounts: sequence: an existing entry with the same name is
// replaced in place, otherwise the entry is appended — mirroring how
// mergeMounts reads the list. Callers validate the mount before writing.
func AddMount(path string, m config.Mount) (bool, error) {
	return Upsert(path, func(doc *yaml.Node) {
		seq := ensureChildSeq(doc, "mounts")
		node := mountNode(m)
		if idx, _ := findSeqEntryByName(seq, m.Name); idx >= 0 {
			seq.Content[idx] = node
			return
		}
		seq.Content = append(seq.Content, node)
	})
}

// DisableMount marks name as disabled: an existing file entry gains
// disabled: true in place; otherwise the `{name, disabled: true}` patch
// shape mergeMounts reads is appended.
func DisableMount(path, name string) (bool, error) {
	return Upsert(path, func(doc *yaml.Node) {
		seq := ensureChildSeq(doc, "mounts")
		if _, entry := findSeqEntryByName(seq, name); entry != nil {
			configio.SetMapBool(entry, "disabled", true)
			return
		}
		patch := &yaml.Node{Kind: yaml.MappingNode}
		configio.SetMapValue(patch, "name", name)
		configio.SetMapBool(patch, "disabled", true)
		seq.Content = append(seq.Content, patch)
	})
}

// RemoveMount deletes the user-list entry named name from the mounts:
// sequence. Defaults are not represented in the file, so this can only ever
// touch user entries; a mounts: list left empty is dropped entirely.
// changed=false means no entry with that name was present.
func RemoveMount(path, name string) (bool, error) {
	return Upsert(path, func(doc *yaml.Node) {
		seq := configio.ChildValue(doc, "mounts")
		if seq == nil || seq.Kind != yaml.SequenceNode {
			return
		}
		idx, _ := findSeqEntryByName(seq, name)
		if idx < 0 {
			return
		}
		seq.Content = append(seq.Content[:idx], seq.Content[idx+1:]...)
		if len(seq.Content) == 0 {
			removeMapKey(doc, "mounts")
		}
	})
}

// SetMountsRoot upserts the top-level mounts_root: key. Callers pre-validate
// with config.ValidateMountsRoot so an invalid root never reaches the file.
func SetMountsRoot(path, root string) (bool, error) {
	return Upsert(path, func(doc *yaml.Node) {
		configio.SetMapValue(doc, "mounts_root", root)
	})
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

// =============================================================================
// yaml.Node sequence helpers (private until a second consumer justifies
// graduating them to configio — D7)
// =============================================================================

// ensureChildSeq looks up key on parent and returns its sequence-node value,
// creating an empty sequence when the key is absent or its value is not a
// sequence. Style is reset to block so appending to a flow `[]` placeholder
// renders multi-line entries readably.
func ensureChildSeq(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		k := parent.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			v := parent.Content[i+1]
			if v.Kind != yaml.SequenceNode {
				*v = yaml.Node{Kind: yaml.SequenceNode}
			}
			v.Style = 0
			return v
		}
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	v := &yaml.Node{Kind: yaml.SequenceNode}
	parent.Content = append(parent.Content, k, v)
	return v
}

// findSeqEntryByName scans seq for a mapping item whose name: scalar equals
// name, returning its index and node, or (-1, nil) when absent.
func findSeqEntryByName(seq *yaml.Node, name string) (int, *yaml.Node) {
	for i, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if v := configio.ChildValue(item, "name"); v != nil && v.Kind == yaml.ScalarNode && v.Value == name {
			return i, item
		}
	}
	return -1, nil
}

// mountNode renders a config.Mount as the replace/append mapping shape
// mergeMounts reads. Zero-valued fields are omitted so the file stays
// minimal.
func mountNode(m config.Mount) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	configio.SetMapValue(n, "name", m.Name)
	if m.Source != "" {
		configio.SetMapValue(n, "source", m.Source)
	}
	if m.Target != "" {
		configio.SetMapValue(n, "target", m.Target)
	}
	if m.ReadOnly {
		configio.SetMapBool(n, "readonly", true)
	}
	return n
}

// removeMapKey deletes the key/value pair named key from a mapping node,
// reporting whether anything was removed.
func removeMapKey(parent *yaml.Node, key string) bool {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		k := parent.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			parent.Content = append(parent.Content[:i], parent.Content[i+2:]...)
			return true
		}
	}
	return false
}
