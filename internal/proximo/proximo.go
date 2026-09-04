// Package proximo wires the toolbox container into proximo
// (https://github.com/filippolmt/proximo), a local-dev reverse proxy that
// makes any labelled Docker container reachable at https://<name>.<tld> with
// trusted HTTPS. proximo terminates TLS on the host (Traefik publishing :443)
// and installs a host resolver mapping *.<tld> to 127.0.0.1 — which breaks
// inside a sibling container, where 127.0.0.1 is the container itself, not the
// host where Traefik listens.
//
// This package supplies the two host-side ingredients that restore
// reachability from within a toolbox container, both gated on the
// `proximo: true` config flag:
//
//   - ExtraHosts: every routed hostname (read from the proximo.hosts label on
//     running containers) is pinned to the Docker host-gateway, so
//     https://<host> reaches the host where Traefik listens instead of the
//     container's own loopback. host-gateway routing bypasses Docker networks
//     entirely — no shared network or upstream proximo change is required.
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
// The label discovery — the only Docker-dependent step — lives in
// internal/container, which already owns the Docker client; ExtraHosts here is
// the pure parser it feeds. Everything else in this package is host-local
// (fs probes plus one optional `proximo config ca-path` exec) so it stays
// unit-testable and keeps the Docker SDK out of the mount/session planners.
package proximo

import (
	"context"
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

// Enabled reports whether proximo integration is active for cfg. Tri-state on
// cfg.Proximo: an explicit true/false wins (and skips the CA probe entirely);
// when unset (nil) it auto-detects, enabling the integration iff proximo is
// set up on this host — i.e. its root CA exists (written by `proximo
// install`). So a host with proximo installed gets `.test` reachability in
// every shell with no per-repo opt-in, while a host without proximo pays
// nothing.
func Enabled(host fsx.Host, cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	if cfg.Proximo != nil {
		return *cfg.Proximo
	}
	_, _, exists := caStatus(host)
	return exists
}

// caPathQueryTimeout bounds the `proximo config ca-path` exec so a
// misbehaving binary can never hang `toolbox shell` startup. The query is
// documented side-effect free (no Docker, no sudo), so 2s is generous.
const caPathQueryTimeout = 2 * time.Second

// CAPath returns proximo's root-CA file on the host. It asks proximo itself
// first — `proximo config ca-path` (the stable contract from
// filippolmt/proximo#20) always prints the path, even before `proximo
// install` writes the CA — so toolbox survives future layout moves without a
// code change. When the binary is absent or predates the subcommand, it falls
// back to the known layout ~/.proximo/tls/ca.pem (proximo's state home since
// v0.3.0, filippolmt/proximo#17). ok is false when neither resolves.
//
// Both halves are host inputs: the binary is looked up on host's PATH and the
// fallback hangs off host.Home, so a caller decides which host is probed
// rather than the process deciding for it.
func CAPath(host fsx.Host) (path string, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), caPathQueryTimeout)
	defer cancel()
	bin, lookErr := host.Look("proximo")
	if lookErr == nil {
		if out, err := exec.CommandContext(ctx, bin, "config", "ca-path").Output(); err == nil {
			// IsAbs guards against junk stdout from an older proximo that exits
			// 0 on unknown subcommands (none known to, but the contract is
			// cheap).
			if p := strings.TrimSpace(string(out)); filepath.IsAbs(p) {
				return p, true
			}
		}
	}
	if host.Home == "" {
		return "", false
	}
	return host.Join(".proximo", "tls", "ca.pem"), true
}

// caStatus resolves the CA path and probes its existence in one shot — the
// single internal seam that pays the CAPath query (a subprocess spawn), so
// every public entry point execs `proximo config ca-path` at most once.
func caStatus(host fsx.Host) (path string, ok, exists bool) {
	path, ok = CAPath(host)
	if !ok {
		return "", false, false
	}
	_, err := os.Stat(path)
	return path, true, err == nil
}

// forcedOff reports an explicit `proximo: false` — the one tri-state arm that
// must short-circuit before the CA probe, so an opted-out config never pays
// the subprocess spawn.
func forcedOff(cfg *config.Config) bool {
	return cfg == nil || (cfg.Proximo != nil && !*cfg.Proximo)
}

// CAMount returns the read-only bind for proximo's CA when integration is
// enabled and the CA path resolves. The mount resolver soft-skips it with a
// warning when the source file is absent (proximo not installed), so callers
// need not pre-check existence.
func CAMount(host fsx.Host, cfg *config.Config) (config.Mount, bool) {
	if forcedOff(cfg) {
		return config.Mount{}, false
	}
	path, ok, exists := caStatus(host)
	// Explicit true keeps the mount even without the CA file (soft-skip
	// downstream); auto (nil) requires the CA — same gate as Enabled.
	if !ok || (cfg.Proximo == nil && !exists) {
		return config.Mount{}, false
	}
	return config.Mount{
		Name:     caMountName,
		Source:   path,
		Target:   CATarget,
		ReadOnly: true,
	}, true
}

// Env returns the environment entries that make in-container tooling trust
// proximo's CA. Emitted only when integration is enabled AND the CA file
// exists on the host, so a missing CA never leaves Node pointing at an absent
// NODE_EXTRA_CA_CERTS file. (With the CA present, auto and explicit true
// coincide, so existence is the only probe needed past the forced-off gate.)
func Env(host fsx.Host, cfg *config.Config) []string {
	if forcedOff(cfg) {
		return nil
	}
	if _, ok, exists := caStatus(host); !ok || !exists {
		return nil
	}
	return []string{
		"NODE_EXTRA_CA_CERTS=" + CATarget,
		"TOOLBOX_PROXIMO_CA=" + CATarget,
	}
}

// ExtraHosts turns a set of proximo.hosts label values (each a comma-separated
// hostname list) into sorted, de-duplicated Docker --add-host entries pinning
// every routed hostname to host-gateway. Pure: the Docker container listing
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
