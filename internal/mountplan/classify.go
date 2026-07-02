package mountplan

import (
	"slices"

	"github.com/filippolmt/toolbox/internal/config"
)

// Origin is the provenance of a mount in the merged set: where it came from
// relative to the canonical defaults and the user's mounts: list. The String
// tokens are documented domain vocabulary (see `toolbox mounts list` and
// docs/mounts.md), so they live on the enum rather than being reinvented at
// the cmd edge.
type Origin int

const (
	OriginDefault  Origin = iota // canonical default, unpatched
	OriginPatched                // default whose Source/Target a user entry patches
	OriginUser                   // user-declared, no matching default name
	OriginDisabled               // a default dropped from the resolved set
)

func (o Origin) String() string {
	switch o {
	case OriginPatched:
		return "patched"
	case OriginUser:
		return "user"
	case OriginDisabled:
		return "disabled"
	default:
		return "default"
	}
}

// ClassifiedMount pairs a resolved mount with its Origin.
type ClassifiedMount struct {
	config.Mount
	Origin Origin
}

// Classify returns the merged mount set tagged with each entry's Origin, plus
// the canonical defaults that the merge dropped (re-included as
// OriginDisabled) so the view stays complete. Pure: wraps Merge, no
// filesystem side-effects. This is the single seam callers cross to reason
// about mount provenance — the classification that used to be re-derived per
// cmd handler.
func Classify(cfg *config.Config) ([]ClassifiedMount, error) {
	merged, err := Merge(cfg, nil)
	if err != nil {
		return nil, err
	}

	defaultNames := namesSet(defaults())
	userNames := namesSet(cfg.Mounts)

	out := make([]ClassifiedMount, 0, len(merged))
	resolved := map[string]struct{}{}
	for _, m := range merged {
		if m.Name != "" {
			resolved[m.Name] = struct{}{}
		}
		out = append(out, ClassifiedMount{Mount: m, Origin: originOf(m.Name, defaultNames, userNames)})
	}

	// Defaults absent from the resolved set were disabled — by a user patch
	// ({name, disabled: true}) or a code-driven feature toggle (bridge: false).
	for _, d := range defaults() {
		if _, ok := resolved[d.Name]; !ok {
			out = append(out, ClassifiedMount{Mount: d, Origin: OriginDisabled})
		}
	}
	return out, nil
}

// Names returns every mount name the merge can resolve — the canonical
// defaults plus named user entries — sorted and deduplicated, anonymous
// entries excluded. This is the universe a disable patch may legally
// reference; an unknown name breaks the next config load.
func Names(cfg *config.Config) ([]string, error) {
	// Validate the user list the same way Merge would, so Names never reports a
	// name that Merge itself would reject.
	if _, err := Merge(cfg, nil); err != nil {
		return nil, err
	}
	names := []string{}
	for _, d := range defaults() {
		if d.Name != "" && !slices.Contains(names, d.Name) {
			names = append(names, d.Name)
		}
	}
	for _, m := range cfg.Mounts {
		if m.Name != "" && !slices.Contains(names, m.Name) {
			names = append(names, m.Name)
		}
	}
	slices.Sort(names)
	return names, nil
}

// originOf classifies a single resolved mount by name membership. A name in
// both the defaults and the user list is a patch/replace; a default-only name
// is untouched; anything else is a user mount.
func originOf(name string, defaultNames, userNames map[string]struct{}) Origin {
	_, isDefault := defaultNames[name]
	_, isUser := userNames[name]
	switch {
	case isDefault && isUser:
		return OriginPatched
	case isDefault:
		return OriginDefault
	default:
		return OriginUser
	}
}

func namesSet(ms []config.Mount) map[string]struct{} {
	s := make(map[string]struct{}, len(ms))
	for _, m := range ms {
		if m.Name != "" {
			s[m.Name] = struct{}{}
		}
	}
	return s
}
