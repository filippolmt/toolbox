// Package proximo wires the toolbox container into proximo
// (https://github.com/filippolmt/proximo), a local-dev reverse proxy that
// makes any labelled Docker container reachable at https://<name>.<tld> with
// trusted HTTPS. proximo terminates TLS on the host (Traefik publishing :443)
// and installs a host resolver mapping *.<tld> to 127.0.0.1 — which breaks
// inside a sibling container, where 127.0.0.1 is the container itself, not the
// host where Traefik listens.
//
// This package supplies the host-side ingredients that restore reachability
// from within a toolbox container, gated on the `proximo` config flag:
//
//   - ExtraHosts: every effective bare and qualified hostname (read from
//     proximo's route inventory, with a label fallback for older installs) is
//     pinned to the Docker host-gateway, so https://<host> reaches the host
//     where Traefik listens instead of the container's own loopback.
//   - CA trust: proximo's local CA (path queried from proximo itself, with a
//     ~/.proximo state-home fallback — see CAPath) is
//     bind-mounted read-only at CATarget. entrypoint.sh then establishes
//     seamless trust for every in-container HTTPS client: update-ca-certificates
//     (curl / git / wget / python ssl+urllib) and a certutil import into
//     ~/.pki/nssdb (Chromium / Firefox, incl. Playwright's bundled browsers).
//     This package additionally exports NODE_EXTRA_CA_CERTS (Node, which uses
//     its own bundle) and TOOLBOX_PROXIMO_CA (a path pointer for the certifi
//     gap — e.g. REQUESTS_CA_BUNDLE for python-requests).
//
// These ingredients hang off one resolved value, the Gate: Resolve derives the
// availability decision plus the CA and inventory paths once per invocation,
// and the planners read that value through their PlanInput instead of asking
// again.
//
// Inventory parsing is host-local. Legacy label discovery — the only
// Docker-dependent step — lives in internal/container, which already owns the
// Docker client; ExtraHosts here is the pure parser it feeds. This keeps the
// Docker SDK out of the mount/session planners.
package proximo

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
)

const (
	// HostsLabel is the Docker label proximo reads to route a container; its
	// value is a comma-separated list of hostnames.
	HostsLabel = "proximo.hosts"

	// gateway is Docker's reserved ExtraHosts target that resolves to the
	// host's gateway IP — the host where proximo's Traefik publishes :443.
	gateway = "host-gateway"

	// InventoryFile is the effective-route inventory proximo writes.
	InventoryFile = "routes.json"

	// InventoryTarget is the in-container directory containing routes.json.
	// The directory, not the file, is mounted because proximo atomically replaces
	// the file by rename.
	InventoryTarget = "/etc/toolbox/proximo"

	// CATarget is the in-container path proximo's root CA is bind-mounted to.
	// It is the read-source the entrypoint consumes to establish trust, NOT a
	// drop-in trust-store path (a bare file under /etc/ssl/certs is not trusted
	// without update-ca-certificates). entrypoint.sh copies it into the
	// ca-certificates source dir + runs update-ca-certificates (curl/git/wget/
	// python-ssl) and certutil-imports it into ~/.pki/nssdb (Chromium); the
	// NODE_EXTRA_CA_CERTS / TOOLBOX_PROXIMO_CA env below point at this same file.
	CATarget = "/etc/ssl/proximo-ca.pem"

	// caMountName is the stable mountplan name for the CA bind.
	caMountName = "proximo-ca"
)

// Gate is the resolved [Proximo Availability Gate] for one invocation: the
// enablement decision plus the host CA path it was decided against. Resolve
// derives it once and every reader — the mount, the trust env, the create-edge
// discovery flag — reads that value instead of re-deriving the rule and
// re-paying the host path queries, which are subprocess spawns.
//
// It reaches the planners as a mountplan/sessionplan PlanInput field, the same
// seam that already carries the session's other resolved host-side facts. The
// zero value is a session with proximo off: nothing mounted, nothing exported,
// nothing discovered at the Docker edge.
//
// [Proximo Availability Gate]: https://github.com/filippolmt/toolbox/blob/main/CONTEXT.md#proximo-availability-gate
type Gate struct {
	// Enabled is the decision itself: is proximo usable in this shell.
	Enabled bool
	// CAPath is the host path of proximo's root CA, empty when neither the
	// binary's answer nor the state-home fallback resolves one (see CAPath).
	CAPath string
	// CAExists reports whether that file was present when the gate was
	// resolved. Enabled without it is the forced-on arm — `proximo: true` on a
	// host where `proximo install` has not written the CA — which keeps the
	// mount (soft-skipped downstream, with a warning) but not the env.
	CAExists bool
	// InventoryDir is the host directory proximo keeps routes.json in.
	InventoryDir string
	// InventoryExists keeps an older proximo from producing a skipped-mount
	// warning on every shell; label discovery remains its compatibility path.
	InventoryExists bool
}

// Resolve derives the gate for cfg on host. Tri-state on cfg.Proximo: an
// explicit true/false wins, and false (like a nil cfg) short-circuits before
// the CA query so an opted-out config never pays the subprocess spawn; when
// unset (nil) it auto-detects, enabling the integration iff proximo is set up
// on this host — i.e. its root CA exists (written by `proximo install`). So a
// host with proximo installed gets `.test` reachability in every shell with no
// per-repo opt-in, while a host without proximo pays nothing.
//
// This is the single place the rule is derived: a caller resolves one gate per
// invocation and hands it down rather than asking the host again.
func Resolve(host fsx.Host, cfg *config.Config) Gate {
	if cfg == nil || (cfg.Proximo != nil && !*cfg.Proximo) {
		return Gate{}
	}
	path, ok := CAPath(host)
	if !ok {
		// Nothing to mount and nothing to trust, but an explicit `proximo:
		// true` still says the integration is on — the arm where the config,
		// not the host, is the whole answer.
		return Gate{Enabled: cfg.Proximo != nil}
	}
	_, statErr := os.Stat(path)
	exists := statErr == nil
	enabled := exists
	if cfg.Proximo != nil {
		enabled = *cfg.Proximo
	}
	gate := Gate{Enabled: enabled, CAPath: path, CAExists: exists}
	if !enabled {
		return gate
	}
	if dir, ok := InventoryDir(host); ok {
		gate.InventoryDir = dir
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			gate.InventoryExists = true
		}
	}
	return gate
}

// InventoryMount returns the read-only bind for proximo's effective-route
// inventory. It is optional compatibility plumbing: an older proximo has no
// directory, so absence omits the mount without warning and leaves label
// discovery in place.
func (g Gate) InventoryMount() (config.Mount, bool) {
	if !g.Enabled || !g.InventoryExists || g.InventoryDir == "" {
		return config.Mount{}, false
	}
	return config.Mount{
		Name:     "proximo-inventory",
		Source:   g.InventoryDir,
		Target:   InventoryTarget,
		ReadOnly: true,
	}, true
}

// CAMount returns the read-only bind for proximo's CA when the gate is on and
// a CA path resolved. The mount resolver soft-skips it with a warning when the
// source file is absent (proximo not installed), so callers need not pre-check
// existence — a forced-on gate keeps the mount and gets the warning.
func (g Gate) CAMount() (config.Mount, bool) {
	if !g.Enabled || g.CAPath == "" {
		return config.Mount{}, false
	}
	return config.Mount{
		Name:     caMountName,
		Source:   g.CAPath,
		Target:   CATarget,
		ReadOnly: true,
	}, true
}

// Env returns the environment entries that make in-container tooling trust
// proximo's CA. Emitted only when the gate is on AND the CA exists on the
// host, so a missing CA never leaves Node pointing at an absent
// NODE_EXTRA_CA_CERTS file.
func (g Gate) Env() []string {
	if !g.Enabled || !g.CAExists {
		return nil
	}
	return []string{
		"NODE_EXTRA_CA_CERTS=" + CATarget,
		"TOOLBOX_PROXIMO_CA=" + CATarget,
	}
}

// pathQueryTimeout bounds each side-effect-free `proximo config *-path` query
// so a misbehaving binary can never hang `toolbox shell` startup.
const pathQueryTimeout = 2 * time.Second

// CAPath returns proximo's root-CA file on the host. It asks proximo itself
// first — `proximo config ca-path` (the stable contract from
// filippolmt/proximo#20) always prints the path, even before `proximo
// install` writes the CA — so toolbox survives future layout moves without a
// code change. When the query is unavailable it falls back to the known state
// layout. ok is false when neither resolves.
func CAPath(host fsx.Host) (path string, ok bool) {
	return configPath(host, "ca-path", "tls", "ca.pem")
}

// InventoryDir returns the host directory containing proximo's routes.json.
// The query is the stable cross-tool contract; the fallback preserves the
// label-based compatibility path when an older proximo lacks it.
func InventoryDir(host fsx.Host) (path string, ok bool) {
	return configPath(host, "inventory-dir", "data", "inventory")
}

func configPath(host fsx.Host, query string, fallback ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), pathQueryTimeout)
	defer cancel()
	if bin, err := host.Look("proximo"); err == nil {
		if out, err := exec.CommandContext(ctx, bin, "config", query).Output(); err == nil {
			if p := strings.TrimSpace(string(out)); filepath.IsAbs(p) {
				return p, true
			}
		}
	}
	if host.Home == "" {
		return "", false
	}
	return host.Join(append([]string{".proximo"}, fallback...)...), true
}

// InventoryExtraHosts reads the effective local names proximo publishes for
// consumers. Only bare and qualified are pinnable; peer, claimed and collision
// fields deliberately have no place in this projection.
func InventoryExtraHosts(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, InventoryFile))
	if err != nil {
		return nil, err
	}
	var doc struct {
		Routes []struct {
			Bare      string `json:"bare"`
			Qualified string `json:"qualified"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	values := make([]string, 0, len(doc.Routes)*2)
	for _, route := range doc.Routes {
		values = append(values, route.Bare, route.Qualified)
	}
	return ExtraHosts(values), nil
}

// ExtraHosts turns a set of proximo.hosts label values (each a comma-separated
// hostname list) into sorted, de-duplicated Docker --add-host entries pinning
// every declared hostname to host-gateway. Pure: the Docker container listing
// that produces labelValues lives in internal/container.
func ExtraHosts(labelValues []string) []string {
	seen := make(map[string]struct{})
	for _, raw := range labelValues {
		for _, h := range strings.Split(raw, ",") {
			h = strings.TrimSpace(h)
			if h == "" {
				continue
			}
			seen[h] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h+":"+gateway)
	}
	sort.Strings(out)
	return out
}
