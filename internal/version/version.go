// Package version holds the CLI build metadata. Values are set via -ldflags
// at build time (see Makefile and .goreleaser.yaml). The defaults here keep
// `go run .` usable without extra flags.
package version

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
