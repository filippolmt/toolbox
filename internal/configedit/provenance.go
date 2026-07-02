package configedit

import (
	"fmt"
	"reflect"

	"github.com/filippolmt/toolbox/internal/config"
)

// Origin identifies the config layer that determined a resolved key's value.
// The zero value is OriginDefault so missing Provenance entries read as
// built-in defaults.
type Origin int

const (
	OriginDefault Origin = iota
	OriginGlobal
	OriginProject
	OriginExplicit
)

// Label renders the git-config-style origin annotation. OriginExplicit
// callers that know the resolved --config path should prefer LabelWithPath.
func (o Origin) Label() string {
	switch o {
	case OriginGlobal:
		return "(~/.toolbox.yaml)"
	case OriginProject:
		return "(./.toolbox.yaml)"
	case OriginExplicit:
		return "(--config)"
	default:
		return "(default)"
	}
}

// LabelWithPath is Label with the resolved --config path spliced into the
// OriginExplicit annotation.
func (o Origin) LabelWithPath(explicitPath string) string {
	if o == OriginExplicit && explicitPath != "" {
		return fmt.Sprintf("(--config %s)", explicitPath)
	}
	return o.Label()
}

// Provenance maps a resolved-config key to the layer that set it. Keys are
// the top-level container names (shell, mounts_root, inherit_host_auth, …)
// except shells and mounts, which are attributed per entry via ShellKey /
// MountKey — granularity inside an entry stays container-level in v1
// (documented in --origin help).
type Provenance map[string]Origin

// MountKey returns the Provenance key for a named mount entry; anonymous
// entries (no name) fall back to the "mounts" container key. Owns the key
// format shared by Compute (producer) and the show --origin renderer
// (consumer) so the two cannot drift.
func MountKey(name string) string {
	if name == "" {
		return "mounts"
	}
	return "mounts." + name
}

// ShellKey is the shells: sibling of MountKey.
func ShellKey(name string) string { return "shells." + name }

// Compute attributes every resolved key to its source layer by re-running
// the pure config.Merge once per layer (defaults / +global / +project) and
// crediting each key to the highest layer whose value differs from the layer
// below. An explicit --config short-circuits to a defaults-vs-explicit diff,
// matching Plan's File Load stage. Cost is two or three extra viper passes —
// only paid by `config show --origin` / `config doctor`, never on the hot
// `toolbox shell` path.
func Compute(searchFrom, explicitOverride string) (Provenance, error) {
	global, project, explicit, _, err := config.LoadLayers(searchFrom, explicitOverride)
	if err != nil {
		return nil, err
	}

	base, err := config.Merge(nil, nil, nil)
	if err != nil {
		return nil, err
	}
	prov := Provenance{}

	if len(explicit) > 0 {
		full, merr := config.Merge(nil, nil, explicit)
		if merr != nil {
			return nil, merr
		}
		diffLayer(prov, base, full, OriginExplicit)
		return prov, nil
	}

	withGlobal, err := config.Merge(global, nil, nil)
	if err != nil {
		return nil, err
	}
	diffLayer(prov, base, withGlobal, OriginGlobal)

	full, err := config.Merge(global, project, nil)
	if err != nil {
		return nil, err
	}
	diffLayer(prov, withGlobal, full, OriginProject)
	return prov, nil
}

// perEntryDiffKeys are the collection fields attributed per entry (by name)
// rather than per top-level key; the generic field walk skips them and the
// hand-written passes below handle their finer-grained attribution.
var perEntryDiffKeys = map[string]bool{"shells": true, "mounts": true}

// diffLayer credits origin to every key whose resolved value in upper differs
// from lower (the layer below). Scalar / slice / map / pointer / struct fields
// are compared generically by reflecting over Config keyed by the mapstructure
// tag, so a new field is tracked the moment it is added — no per-field branch
// to forget (the gap that once dropped agent and managed_statusline). Shells
// and mounts keep per-entry attribution.
func diffLayer(prov Provenance, lower, upper *config.Config, origin Origin) {
	lv := reflect.ValueOf(*lower)
	uv := reflect.ValueOf(*upper)
	for f := range reflect.TypeFor[config.Config]().Fields() {
		tag := f.Tag.Get("mapstructure")
		if tag == "" || tag == "-" || perEntryDiffKeys[tag] {
			continue
		}
		if !reflect.DeepEqual(uv.FieldByName(f.Name).Interface(), lv.FieldByName(f.Name).Interface()) {
			prov[tag] = origin
		}
	}
	for name, s := range upper.Shells {
		if ls, ok := lower.Shells[name]; !ok || !reflect.DeepEqual(s, ls) {
			prov[ShellKey(name)] = origin
		}
	}
	diffMounts(prov, lower.Mounts, upper.Mounts, origin)
}

// diffMounts attributes named user mount entries individually; anonymous
// entries (no name) fall back to the "mounts" container key.
func diffMounts(prov Provenance, lower, upper []config.Mount, origin Origin) {
	lowerByName := map[string]config.Mount{}
	var lowerAnon []config.Mount
	for _, m := range lower {
		if m.Name != "" {
			lowerByName[m.Name] = m
		} else {
			lowerAnon = append(lowerAnon, m)
		}
	}
	var upperAnon []config.Mount
	for _, m := range upper {
		if m.Name == "" {
			upperAnon = append(upperAnon, m)
			continue
		}
		if lm, ok := lowerByName[m.Name]; !ok || !reflect.DeepEqual(m, lm) {
			prov[MountKey(m.Name)] = origin
		}
	}
	if !reflect.DeepEqual(upperAnon, lowerAnon) {
		prov[MountKey("")] = origin
	}
}
