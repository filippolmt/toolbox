package configui

import (
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

// The mounts editor's checkboxes select the default mounts to *disable*
// (openEditor seeds them from DisabledMounts), and SaveMountDisabled records
// that as a `{name, disabled: true}` patch. A preview that renders the
// selection as a bare `mounts:` list therefore claims the inverse of what will
// be written: that the checked mount is the only one kept.
func TestPreviewMountsShowsDisablePatch(t *testing.T) {
	m := Model{ed: editor{
		key:      "mounts",
		kind:     edMulti,
		options:  []string{"claude", "gh"},
		selected: map[string]bool{"claude": true},
	}}

	out, err := yaml.Marshal(m.previewDoc())
	if err != nil {
		t.Fatalf("marshal preview: %v", err)
	}
	got := string(out)

	if !strings.Contains(got, "name: claude") || !strings.Contains(got, "disabled: true") {
		t.Errorf("preview does not show the disable patch the writer produces:\n%s", got)
	}
	if strings.Contains(got, "- claude") {
		t.Errorf("preview lists claude as a kept mount, inverting the edit:\n%s", got)
	}
}
