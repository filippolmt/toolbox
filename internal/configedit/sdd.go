package configedit

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/filippolmt/toolbox/internal/configio"
	"github.com/filippolmt/toolbox/internal/sdd"
)

// =============================================================================
// SDD writers
// =============================================================================
//
// The single authority for the file mutations enabling or disabling an SDD
// skill performs: the sdd.<key> flag in .toolbox.yaml and the per-skill
// .gitignore fence. cmd/sdd.go (CLI) and configui.SaveSDD (TUI) both route
// their SDD writes here so the two paths produce identical file state.

const (
	sddYAMLKey       = "sdd"
	sddFenceTemplate = "sdd-managed/"
)

// GitignoreFenceStart / GitignoreFenceEnd render the per-skill fence markers.
// Each skill owns its own pair so enabling one skill touches only its section.
func GitignoreFenceStart(skill string) string {
	return "# >>> " + sddFenceTemplate + skill + " (toolbox)"
}

func GitignoreFenceEnd(skill string) string {
	return "# <<< " + sddFenceTemplate + skill + " (toolbox)"
}

// SetSDDEnabled toggles the sdd.<key> flag on doc — the shared yaml-flag
// primitive both callers compose inside their own write closure.
//
// on:  writes the bool shorthand, but leaves an existing object-form entry
//
//	(`sdd.<key>: {steps: […]}`) untouched — it already means enabled, and the
//	shorthand would clobber the user's steps override.
//
// off: removes the sdd.<key> key, dropping an emptied sdd map entirely.
func SetSDDEnabled(doc *yaml.Node, key string, on bool) {
	if on {
		sddMap := configio.EnsureChildMap(doc, sddYAMLKey)
		if v := configio.ChildValue(sddMap, key); v != nil && v.Kind == yaml.MappingNode {
			return
		}
		configio.SetMapBool(sddMap, key, true)
		return
	}
	sddMap := configio.ChildValue(doc, sddYAMLKey)
	if sddMap == nil || sddMap.Kind != yaml.MappingNode {
		return
	}
	configio.RemoveMapKey(sddMap, key)
	if len(sddMap.Content) == 0 {
		configio.RemoveMapKey(doc, sddYAMLKey)
	}
}

// WriteSDDGitignore splices the skill's fenced block into gitignorePath,
// creating the file if missing. A skill with no GitignoreEntries produces
// user-authored content and is skipped (changed=false, no write).
func WriteSDDGitignore(gitignorePath string, skill sdd.Skill) (bool, error) {
	if len(skill.GitignoreEntries) == 0 {
		return false, nil
	}
	existing, err := os.ReadFile(gitignorePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read %s: %w", gitignorePath, err)
	}
	updated, changed := configio.SpliceFence(existing,
		GitignoreFenceStart(skill.Key), GitignoreFenceEnd(skill.Key), renderGitignoreBlock(skill))
	if !changed {
		return false, nil
	}
	return true, configio.AtomicWriteFile(gitignorePath, updated, 0o600)
}

// RemoveSDDGitignore removes the skill's fenced block from gitignorePath. A
// missing file or absent fence is a no-op (changed=false).
func RemoveSDDGitignore(gitignorePath string, skill sdd.Skill) (bool, error) {
	existing, err := os.ReadFile(gitignorePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", gitignorePath, err)
	}
	updated, changed := configio.RemoveFence(existing,
		GitignoreFenceStart(skill.Key), GitignoreFenceEnd(skill.Key))
	if !changed {
		return false, nil
	}
	return true, configio.AtomicWriteFile(gitignorePath, updated, 0o600)
}

// ReconcileSDDGitignore brings the .gitignore fence set in line with enabled:
// each registered skill's fence is written when its key is enabled and removed
// otherwise. The multi-skill counterpart to EnableSDD, so the TUI batch save
// and the CLI single-skill init share one fence-write authority.
func ReconcileSDDGitignore(gitignorePath string, enabled map[string]bool) error {
	for _, skill := range sdd.Skills {
		var err error
		if enabled[skill.Key] {
			_, err = WriteSDDGitignore(gitignorePath, skill)
		} else {
			_, err = RemoveSDDGitignore(gitignorePath, skill)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func renderGitignoreBlock(skill sdd.Skill) string {
	var b strings.Builder
	b.WriteString(GitignoreFenceStart(skill.Key))
	b.WriteString("\n")
	for _, e := range skill.GitignoreEntries {
		b.WriteString(e)
		b.WriteString("\n")
	}
	b.WriteString(GitignoreFenceEnd(skill.Key))
	return b.String()
}

// SDDResult reports which files EnableSDD changed. GitignoreSkipped is true
// when the skill declares no gitignore entries (fence intentionally not
// written), distinct from GitignoreChanged=false (fence already current).
type SDDResult struct {
	YAMLChanged      bool
	GitignoreChanged bool
	GitignoreSkipped bool
}

// EnableSDD is the CLI convenience composing the yaml flag write and the
// gitignore fence write for a single skill. yamlPath, gitignorePath and cwd are
// explicit (callers join the first two from cwd) so it is testable with temp
// dirs. The two files are written non-atomically (yaml then fence), matching the
// prior CLI ordering; re-running converges. A rejected yaml write leaves the
// fence untouched — ApplyChecked returns before either file changes.
func EnableSDD(yamlPath, gitignorePath, cwd string, skill sdd.Skill) (SDDResult, error) {
	var res SDDResult
	yamlChanged, err := ApplyChecked(yamlPath, cwd, func(doc *yaml.Node) {
		SetSDDEnabled(doc, skill.Key, true)
	})
	if err != nil {
		return res, err
	}
	res.YAMLChanged = yamlChanged

	if len(skill.GitignoreEntries) == 0 {
		res.GitignoreSkipped = true
		return res, nil
	}
	giChanged, err := WriteSDDGitignore(gitignorePath, skill)
	if err != nil {
		return res, err
	}
	res.GitignoreChanged = giChanged
	return res, nil
}
