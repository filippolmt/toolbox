package mountplan

import (
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/proximo"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// testHost returns a Host rooted at a fresh temp dir. Tests that assert on
// the merge contract alone (patch, replace, disable, mounts_root, share)
// declare a home they never populate: only inherit_host_auth's pre-stat
// reads it, and those tests seed their own tree instead — see
// seedHostAuthPaths.
func testHost(t *testing.T) fsx.Host {
	t.Helper()
	return fsx.Host{Home: t.TempDir()}
}

// TestPathEntryPointsRefuseAHostWithoutAHome covers the other two exported
// functions that turn the home into a path. Plan is pinned by its own tests;
// these two would otherwise silently return "/.toolbox/..." — a path outside
// any user's tree, which the caller would then create or bind.
func TestPathEntryPointsRefuseAHostWithoutAHome(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func() (string, error)
	}{
		{"OverlayDockerfilePath", func() (string, error) {
			return OverlayDockerfilePath(fsx.Host{}, &config.Config{}, nil)
		}},
		{"StateDirPath", func() (string, error) {
			return StateDirPath(fsx.Host{}, &config.Config{}, nil, proximo.Gate{})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.call()
			if err == nil {
				t.Fatalf("%s returned %q for a host with no home, want an error", tc.name, got)
			}
			if got != "" {
				t.Errorf("%s returned a path %q alongside its error", tc.name, got)
			}
		})
	}
}

// TestOverlayDockerfilePathFollowsTheRoot: the overlay file lives beside the
// credential dirs, so it follows the same root resolution the mount pipeline
// uses — a profile root beats a config-level mounts_root, and neither leaves
// the declared home.
func TestOverlayDockerfilePathFollowsTheRoot(t *testing.T) {
	host := fsx.Host{Home: "/planned/home"}
	profile, err := NewProfile("work", nil)
	if err != nil {
		t.Fatalf("NewProfile: %v", err)
	}
	for _, tc := range []struct {
		name    string
		cfg     *config.Config
		profile *Profile
		want    string
	}{
		{"default root", &config.Config{}, nil, "/planned/home/.toolbox/Dockerfile"},
		{"mounts_root", &config.Config{MountsRoot: "/custom/root"}, nil, "/custom/root/Dockerfile"},
		{"profile beats mounts_root", &config.Config{MountsRoot: "/custom/root"}, profile,
			"/planned/home/.toolbox/profiles/work/Dockerfile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := OverlayDockerfilePath(host, tc.cfg, tc.profile)
			if err != nil {
				t.Fatalf("OverlayDockerfilePath: %v", err)
			}
			if got != tc.want {
				t.Errorf("OverlayDockerfilePath = %q, want %q", got, tc.want)
			}
		})
	}
}
