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

// Render returns the bytes ApplyChecked would write for a file whose current
// content is src, without touching any file — the seam both a preview and
// ApplyChecked's own validation need to see a pending edit truthfully. exists
// must report whether the target file is already there, because that is what
// decides the header: a file being created carries headerComment, and a preview
// rendering src alone would under-report the write by exactly those lines. A nil
// mutate renders src as-is.
func Render(name string, src []byte, exists bool, mutate Mutator) ([]byte, error) {
	return configio.RenderDocument(name, src, headerAware(!exists, mutate))
}

// headerAware is the shared header policy behind Render, and so behind every
// write (they all render through it): a document being created gains
// headerComment, an existing one is left alone. Keeping it in one place is what
// lets a preview render the same bytes the write produces.
func headerAware(creating bool, mutate func(doc *yaml.Node)) func(doc *yaml.Node) {
	return func(doc *yaml.Node) {
		if creating && doc.HeadComment == "" {
			doc.HeadComment = headerComment
		}
		if mutate != nil {
			mutate(doc)
		}
	}
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
// Typed writers
// =============================================================================
//
// Every writer below is a thin naming of one Pending Mutation over
// ApplyChecked, so they all share its contract: cwd is the directory the config
// layers are resolved from (the doctor needs it to place the candidate in the
// right layer), and an error always means nothing was written — callers must
// not report a write.

// SetShell upserts shells.<name>.path on the file at path, preserving any
// sibling keys (env overlays survive a path change).
func SetShell(path, cwd, name, shellPath string) (bool, error) {
	return ApplyChecked(path, cwd, func(doc *yaml.Node) {
		entry := configio.EnsureChildMap(configio.EnsureChildMap(doc, "shells"), name)
		configio.SetMapValue(entry, "path", shellPath)
	})
}

// SetShellEnv upserts shells.<name>.env.<K>=<V> for every pair in env,
// applied in sorted key order so repeated runs render identically. Callers
// validate keys (config.ValidateEnv) before writing.
func SetShellEnv(path, cwd, name string, env map[string]string) (bool, error) {
	return ApplyChecked(path, cwd, func(doc *yaml.Node) {
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
func RemoveShell(path, cwd, name string) (bool, error) {
	return ApplyChecked(path, cwd, func(doc *yaml.Node) {
		shellsMap := configio.ChildValue(doc, "shells")
		if !configio.RemoveMapKey(shellsMap, name) {
			return
		}
		if len(shellsMap.Content) == 0 {
			configio.RemoveMapKey(doc, "shells")
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
func AddMount(path, cwd string, m config.Mount) (bool, error) {
	return ApplyChecked(path, cwd, func(doc *yaml.Node) {
		seq := configio.EnsureChildSeq(doc, "mounts")
		node := mountNode(m)
		if idx, _ := configio.FindSeqEntryByName(seq, m.Name); idx >= 0 {
			seq.Content[idx] = node
			return
		}
		seq.Content = append(seq.Content, node)
	})
}

// DisableMount marks name as disabled: an existing file entry gains
// disabled: true in place; otherwise the `{name, disabled: true}` patch
// shape mergeMounts reads is appended.
func DisableMount(path, cwd, name string) (bool, error) {
	return ApplyChecked(path, cwd, func(doc *yaml.Node) {
		seq := configio.EnsureChildSeq(doc, "mounts")
		if _, entry := configio.FindSeqEntryByName(seq, name); entry != nil {
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
func RemoveMount(path, cwd, name string) (bool, error) {
	return ApplyChecked(path, cwd, func(doc *yaml.Node) {
		seq := configio.ChildValue(doc, "mounts")
		if seq == nil || seq.Kind != yaml.SequenceNode {
			return
		}
		idx, _ := configio.FindSeqEntryByName(seq, name)
		if idx < 0 {
			return
		}
		seq.Content = append(seq.Content[:idx], seq.Content[idx+1:]...)
		if len(seq.Content) == 0 {
			configio.RemoveMapKey(doc, "mounts")
		}
	})
}

// SetMountsRoot upserts the top-level mounts_root: key. Callers pre-validate
// with config.ValidateMountsRoot so an invalid root never reaches the file.
func SetMountsRoot(path, cwd, root string) (bool, error) {
	return ApplyChecked(path, cwd, func(doc *yaml.Node) {
		configio.SetMapValue(doc, "mounts_root", root)
	})
}

// =============================================================================
// Top-level scalar writers (image / registry_mirror / pull)
// =============================================================================

// ScalarEdit is one top-level scalar mutation. An empty Value removes the key
// — the clean "reset to default" path that leaves no dangling key behind.
type ScalarEdit struct{ Key, Value string }

// SetScalars applies every edit in one comment-preserving Upsert, so writing
// several keys at once costs a single read-parse-write cycle. Callers
// pre-validate each value (config.ValidateImageRef / ValidateRegistryMirror /
// ValidatePull).
func SetScalars(path, cwd string, edits []ScalarEdit) (bool, error) {
	return ApplyChecked(path, cwd, func(doc *yaml.Node) {
		for _, e := range edits {
			if e.Value == "" {
				configio.RemoveMapKey(doc, e.Key)
				continue
			}
			configio.SetMapValue(doc, e.Key, e.Value)
		}
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
