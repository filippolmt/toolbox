package mountplan

import (
	"os"
	"path/filepath"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
)

// RemoveLegacyRTKState deletes RTK data from the mount roots used by this
// session. It intentionally does not scan other profiles or arbitrary custom
// mount sources.
func RemoveLegacyRTKState(host fsx.Host, cfg *config.Config, profile *Profile) error {
	if err := host.Validate(); err != nil {
		return err
	}
	if profile == nil {
		return removeLegacyRTKPath(host, cfg.MountsRoot, "")
	}
	if shareCovers(profile.Share, "rtk") {
		return removeLegacyRTKPath(host, "", "")
	}
	if shareCovers(profile.Share, "rtk-data") {
		if err := removeLegacyRTKPath(host, profile.Root(), "config"); err != nil {
			return err
		}
		return removeLegacyRTKPath(host, "", "data")
	}
	return removeLegacyRTKPath(host, profile.Root(), "")
}

func removeLegacyRTKPath(host fsx.Host, root, child string) error {
	if err := config.ValidateMountsRoot(root); err != nil {
		return err
	}
	parent := filepath.Clean(host.Expand(mountsRootJoin(root, "rtk")))
	if err := os.RemoveAll(filepath.Join(parent, child)); err != nil {
		return err
	}
	if child != "" {
		_ = os.Remove(parent)
	}
	return nil
}
