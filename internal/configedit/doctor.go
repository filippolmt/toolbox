package configedit

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	yaml "gopkg.in/yaml.v3"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
	"github.com/filippolmt/toolbox/internal/mountplan"
)

// Severity classifies a doctor finding. Errors drive the non-zero exit;
// warnings are advisory.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Finding is one doctor diagnostic.
type Finding struct {
	Severity Severity
	Message  string
}

// toolsRemovalDoc is the image-build internals anchor explaining why the
// legacy tools: block is ignored.
const toolsRemovalDoc = "https://github.com/filippolmt/toolbox/blob/main/docs/internals/image-build.md#tools-removal"

// Doctor validates the configuration without mutating anything: it surfaces
// load/merge errors (everything Plan's validation tail already checks),
// unknown top-level keys per layer (with suggestions), the legacy tools:
// block, empty / missing shells.<name>.path, mount-merge failures, and
// duplicate resolved mount targets. Strictly a read-only superset of the
// load path — it never changes merge behaviour.
func Doctor(searchFrom, explicitOverride string) []Finding {
	var findings []Finding

	global, project, explicit, projectPath, err := config.LoadLayers(searchFrom, explicitOverride)
	if err != nil {
		return append(findings, Finding{SeverityError, err.Error()})
	}

	findings = append(findings, lintLayerKeys("~/.toolbox.yaml", global)...)
	findings = append(findings, lintLayerKeys(projectPath, project)...)
	findings = append(findings, lintLayerKeys(explicitOverride, explicit)...)

	cfg, err := config.Merge(global, project, explicit)
	if err != nil {
		// Schema / validation-tail failure: nothing downstream is resolvable.
		return append(findings, Finding{SeverityError, err.Error()})
	}

	findings = append(findings, lintShellPaths(cfg)...)
	findings = append(findings, lintMounts(cfg)...)
	return findings
}

// HasErrors reports whether any finding is error-severity — the doctor
// command's exit-code predicate.
func HasErrors(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// lintLayerKeys flags unknown top-level keys (with a did-you-mean
// suggestion) and the legacy tools: block in one layer's raw bytes. Parse
// failures are skipped here — Merge surfaces them as proper errors.
func lintLayerKeys(label string, b []byte) []Finding {
	if len(b) == 0 {
		return nil
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil
	}
	known := config.SchemaKeys()
	var findings []Finding
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if k == "tools" {
			findings = append(findings, Finding{SeverityWarning, fmt.Sprintf(
				"%s: legacy 'tools:' block is ignored — all bundled CLIs install unconditionally; remove it (see %s)",
				label, toolsRemovalDoc)})
			continue
		}
		if !slices.Contains(known, k) {
			findings = append(findings, Finding{SeverityWarning, fmt.Sprintf(
				"%s: unknown top-level key %q%s", label, k, DidYouMean(k, known))})
		}
	}
	return findings
}

// lintShellPaths checks every shells.<name>.path: empty is an error (the
// shell can never start); a non-existent directory is only a warning (it
// may be created later, e.g. via `toolbox shell <name> --create`).
func lintShellPaths(cfg *config.Config) []Finding {
	home, _ := fsx.Home()
	var findings []Finding
	for _, name := range slices.Sorted(maps.Keys(cfg.Shells)) {
		path := strings.TrimSpace(cfg.Shells[name].Path)
		if path == "" {
			findings = append(findings, Finding{SeverityError, fmt.Sprintf("shells.%s.path is empty", name)})
			continue
		}
		if _, err := os.Stat(fsx.ExpandTilde(path, home)); os.IsNotExist(err) {
			findings = append(findings, Finding{SeverityWarning, fmt.Sprintf(
				"shells.%s.path %s does not exist (created on first 'toolbox shell %s --create')",
				name, path, name)})
		}
	}
	return findings
}

// lintMounts surfaces mount-merge failures as errors and duplicate resolved
// targets as warnings (mountplan does not dedupe today; the last bind wins
// silently at the Docker layer).
func lintMounts(cfg *config.Config) []Finding {
	resolved, err := mountplan.Merge(cfg, nil)
	if err != nil {
		return []Finding{{SeverityError, err.Error()}}
	}
	var findings []Finding
	seen := map[string]string{} // target → first declaring entry
	for _, m := range resolved {
		id := m.Name
		if id == "" {
			id = m.Source
		}
		if prev, dup := seen[m.Target]; dup {
			findings = append(findings, Finding{SeverityWarning, fmt.Sprintf(
				"mounts: duplicate target %s (declared by %q and %q)", m.Target, prev, id)})
			continue
		}
		seen[m.Target] = id
	}
	return findings
}
