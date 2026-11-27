// Package version holds the CLI build metadata. Values are set via -ldflags
// at build time (see Makefile and .goreleaser.yaml). The defaults here keep
// `go run .` usable without extra flags.
package version

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Build is the build identity `toolbox version` prints and the bridge's
// /healthz reports: two builds of one Version (two local `dev` builds) still
// differ by Commit and Date.
func Build() string {
	return Version + " (commit: " + Commit + ", built: " + Date + ")"
}
