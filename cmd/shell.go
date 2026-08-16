package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/moby/moby/client"
	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/bridge"
	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// oauthToolList renders the supported --oauth tools once for both help
// strings, so the list never drifts from sessionplan's recipe map.
var oauthToolList = strings.Join(sessionplan.SupportedOAuthTools(), ", ")

var shellPublish []string
var shellCreate bool
var shellPath string
var shellBridgeLoopback bool
var shellOAuth []string
var shellProfile string
var shellShare []string

var shellCmd = &cobra.Command{
	Use:   "shell [name|dir]",
	Short: "Start an interactive shell session in the toolbox container",
	Long: `Start the toolbox container and attach an interactive zsh session.

Without arguments the current working directory is mounted at /workspace
and the container name is derived from that path, so each directory gets
its own dedicated container. If the container is already running, a new
session is attached to the existing one.

The positional argument is either:
  - a configured shell name from ~/.toolbox.yaml's shells: map, or
  - an absolute path for a one-shot session (e.g. "toolbox shell /tmp") —
    no config is read or written, the container name still derives from
    the path hash so re-running on the same path reattaches.

Use --publish/-p to forward a host port into the container. Accepts the
same formats as "docker run -p" (e.g. "7171", "7171:7171",
"127.0.0.1:7171:7171"). When the host IP is omitted it defaults to
127.0.0.1 — useful for OAuth callbacks from tools like gh/glab that listen
on localhost inside the container.

Use --bridge-loopback/-B together with -p when the in-container CLI binds
its OAuth callback to container loopback (127.0.0.1) rather than 0.0.0.0
— Docker port-forward delivers to eth0, and a loopback listener never
sees those packets. The bridge spawns one socat per published container
port that listens on eth0 and forwards to 127.0.0.1, making the listener
reachable from the host browser. See docs/commands.md#loopback-bridge
for recipes (codex, wrangler, sonar, cf) and the wildcard-bind carve-out (oci, glab).

Use --oauth <tool> as a shortcut for the documented OAuth recipes: it
expands to the right -p/-B combination for the tool (e.g. "--oauth wrangler"
equals "-B -p 8976:8976"; "--oauth oci" equals "-p 8181:8181" — oci binds
0.0.0.0, no bridge). Supported: ` + oauthToolList + `.`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: runShell,
}

func runShell(cmd *cobra.Command, args []string) error {
	// Expand --oauth presets first: ExpandOAuth is pure, so an unknown tool
	// fails fast before any fs side effects or container creation.
	publish, bridgeLoopback, err := expandShellOAuth(shellPublish, shellBridgeLoopback, shellOAuth)
	if err != nil {
		return err
	}

	// Resolve --profile / --share into a mountplan.Profile before any fs side
	// effect or container creation (same fail-fast contract as expandShellOAuth).
	// An invalid or empty profile name, or a --share without a profile, errors
	// here, leaving no ~/.toolbox/profiles/<name> dir behind.
	profile, err := resolveShellProfile(cmd, shellProfile, shellShare)
	if err != nil {
		return err
	}
	warnIfNewProfile(profile)

	ws, shellName, err := resolveShellWorkspace(args, shellCreate, shellPath)
	if err != nil {
		return err
	}

	// One-time relocation of toolbox-own state into the ~/.toolbox/toolbox
	// namespace. Best-effort: on failure CreateIfMissing rebuilds an empty
	// state dir and the pull cache regenerates, so warn instead of failing
	// the shell.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if err := mountplan.MigrateLegacyToolboxState(home); err != nil {
			fmt.Fprintf(os.Stderr, "toolbox: warning: %v\n", err)
		}
	}

	if cfg.Bridge != nil && *cfg.Bridge {
		printBridgeTipIfNeeded()
	}

	cli, err := container.NewClient()
	if err != nil {
		return dockerClientErr(err)
	}
	defer cli.Close()

	// Resolve the running image's repo digest host-side and thread it to the
	// planner so the in-container update poller can compare it against GHCR's
	// :latest. Best-effort: an unresolvable digest (locally built image,
	// inspect failure, image not yet pulled) yields "" and the planner omits
	// the env entry. See update-notification.
	imageDigest := resolveImageDigest(context.Background(), cli, build.ResolveImage(cfg.Image, cfg.RegistryMirror))

	// Plan after the Docker client is constructed so a failed client init
	// (env parse / socket misconfig) does not leave behind mountplan.Plan
	// fs side effects under ~/.toolbox and the workspace. shellName is the
	// user-typed named shell — empty for workspace sessions; both the
	// container-name format and the per-shell env: overlay are derived from
	// it behind the sessionplan seam.
	plan, err := sessionplan.Plan(sessionplan.PlanInput{
		Cfg:            cfg,
		Workspace:      ws,
		Ports:          publish,
		BridgeLoopback: bridgeLoopback,
		ImageDigest:    imageDigest,
		Name:           shellName,
		Profile:        profile,
	})
	if err != nil {
		return err
	}

	// Post-attach Ctrl+C reaches the container as a raw-mode byte; this
	// signal context only fires during pull/build or on external kill.
	ctx, stop := signalCtx()
	defer stop()

	return container.Shell(ctx, cli, plan)
}

// resolveImageDigest returns the resolved repo digest (`sha256:...`) of the
// image at ref, read from the local daemon's RepoDigests. Best-effort: any
// inspect failure (image absent, daemon error) or a locally built image with
// no repo digest returns "" so the caller threads an empty identity rather
// than failing the shell. The digest is what the in-container poller compares
// against GHCR's :latest manifest.
func resolveImageDigest(ctx context.Context, cli client.APIClient, ref string) string {
	res, err := cli.ImageInspect(ctx, ref)
	if err != nil {
		return ""
	}
	return build.RepoDigest(ref, res.RepoDigests)
}

// expandShellOAuth merges --oauth recipe expansion into the explicit -p/-B
// flag values: expanded publish specs are appended (never replacing explicit
// ones) and the bridge bit is ORed in (never cleared). Pure so the cmd-level
// tests can compare it against the equivalent explicit flag spelling.
func expandShellOAuth(publish []string, bridge bool, oauthTools []string) ([]string, bool, error) {
	oauthPublish, oauthBridge, err := sessionplan.ExpandOAuth(oauthTools)
	if err != nil {
		return nil, false, err
	}
	return append(append([]string(nil), publish...), oauthPublish...), bridge || oauthBridge, nil
}

// resolveShellProfile validates the --profile / --share flags and builds the
// mountplan.Profile (nil for a default session). Pure — the profile owns its
// own root and share skip-set, so nothing here mutates cfg. An explicit
// `--profile ""` (flag set to empty) is an error, distinct from the flag being
// absent; --share without --profile is an error; the profile name is rejected
// before it can become a filesystem path. --share token matching is validated
// downstream in mountplan.Merge.
func resolveShellProfile(cmd *cobra.Command, name string, share []string) (*mountplan.Profile, error) {
	if name == "" {
		if cmd.Flags().Changed("profile") {
			return nil, fmt.Errorf("--profile: name must not be empty")
		}
		if len(share) > 0 {
			return nil, fmt.Errorf("--share requires --profile")
		}
		return nil, nil
	}
	// Name validation (trust boundary) lives in the Profile constructor.
	return mountplan.NewProfile(name, share)
}

// warnIfNewProfile prints a one-line stderr notice when a --profile names a
// directory that does not exist yet, so a typo (`--profile cluade`) surfaces as
// a visibly new, empty profile instead of a silent logged-out shell. Best
// effort: an unresolvable home just skips the notice.
func warnIfNewProfile(p *mountplan.Profile) {
	if p == nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	dir := filepath.Join(home, ".toolbox", "profiles", p.Name)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "toolbox: creating new profile %q (empty credentials — every CLI starts logged out)\n", p.Name)
	}
}

// printBridgeTipIfNeeded prints a one-line install hint when the
// host-side bridge is not yet installed. Build tags select an Agent
// that returns ErrUnsupported on non-darwin/linux, which short-circuits here.
// Uses IsInstalled (stat-only) instead of Status to keep the shell-start hot
// path off launchctl/systemctl exec costs.
func printBridgeTipIfNeeded() {
	a, err := bridge.NewAgent()
	if err != nil {
		return
	}
	if a.IsInstalled() {
		return
	}
	fmt.Fprintln(os.Stderr, "toolbox: tip — run 'toolbox bridge install' to forward in-container URLs to your host browser")
}

func init() {
	shellCmd.Flags().StringSliceVarP(&shellPublish, "publish", "p", nil,
		"publish a container port to the host (repeatable). Format: '[host_ip:]host_port:container_port' or 'port'. "+
			"Examples: 7171, 7171:7171, 127.0.0.1:7171:7171, 0.0.0.0:8000:8000. "+
			"Host IP defaults to 127.0.0.1. Bindings apply only at container creation — run 'toolbox stop' to refresh.")
	shellCmd.Flags().StringSliceVar(&shellOAuth, "oauth", nil,
		"expand a known CLI's OAuth callback recipe (repeatable): publishes its callback port and "+
			"enables the loopback bridge when the tool needs it. Supported: "+oauthToolList+". "+
			"Composes with explicit -p/-B (only adds, never overrides). "+
			"Bindings apply only at container creation — run 'toolbox stop' to refresh.")
	shellCmd.Flags().BoolVarP(&shellBridgeLoopback, "bridge-loopback", "B", false,
		"Forward published ports to container loopback so CLIs that bind 127.0.0.1 "+
			"are reachable from the host browser (e.g. codex/wrangler OAuth callbacks). "+
			"Requires at least one -p; see docs/commands.md#loopback-bridge.")
	shellCmd.Flags().StringVar(&shellProfile, "profile", "",
		"isolate the whole ~/.toolbox/ credential + state set under ~/.toolbox/profiles/<name>, "+
			"so every CLI in the shell (claude, codex, gh, gcloud, …) uses that profile's own auth. "+
			"All-or-nothing (not per-tool); SSH keys and git config stay shared with the host. "+
			"Overrides a configured mounts_root for this invocation. Runs in its own container.")
	shellCmd.Flags().StringSliceVar(&shellShare, "share", nil,
		"under --profile, keep the named tools on the host's ~/.toolbox/ root instead of the profile "+
			"(repeatable/comma-separated). Names match 'toolbox mounts' identifiers; a prefix like 'cf' or "+
			"'rtk' covers its split mounts. Requires --profile.")
	shellCmd.Flags().BoolVar(&shellCreate, "create", false, "Auto-bootstrap a missing named shell in ~/.toolbox.yaml")
	shellCmd.Flags().StringVar(&shellPath, "path", "", "Path to use with --create (default: $HOME/toolbox-shells/<name>; falls back to /tmp/<name> when home is unresolvable)")
	rootCmd.AddCommand(shellCmd)
}
