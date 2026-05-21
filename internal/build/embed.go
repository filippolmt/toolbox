package build

import "embed"

// Assets is the Docker build context embedded into the CLI binary. The files
// below are the complete, self-contained context used by both `toolbox build`
// and the auto-build path in `toolbox shell`; the host does not need a repo
// checkout (e.g. Homebrew installs).
//
//go:embed assets/Dockerfile assets/bashrc.sh assets/entrypoint.sh assets/zshrc.sh assets/starship.toml assets/xterm-ghostty.src assets/init.d assets/bin
var Assets embed.FS

// AssetDir is the top-level directory inside Assets. Callers strip this prefix
// when producing a build context tar so the Dockerfile's `COPY bashrc.sh …`
// resolves correctly.
const AssetDir = "assets"
