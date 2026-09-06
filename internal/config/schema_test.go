package config

import (
	"slices"
	"testing"
)

// TestSchemaKeys pins the full field set in declaration order — which is also
// the order every surface presents the keys in (`config show`, the annotated
// example, the `config ui` list). It is deliberately exact: adding, renaming or
// moving a Config field forces an update here, which is the prompt to give the
// new key its row (TestEveryKeyHasACompleteRow) and to check the order the
// surfaces will show it in.
func TestSchemaKeys(t *testing.T) {
	want := []string{
		"shell", "agent", "image", "registry_mirror", "pull",
		"mounts_root", "bridge", "browser_bridge", "proximo", "managed_statusline",
		"image_reclaim", "peer_messaging", "sdd", "env", "worktree",
		"inherit_host_auth", "shells", "mounts",
	}
	if got := SchemaKeys(); !slices.Equal(got, want) {
		t.Errorf("SchemaKeys drifted:\n got %v\nwant %v", got, want)
	}
}
