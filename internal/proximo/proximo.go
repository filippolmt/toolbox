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
//   - CA trust: proximo's local CA (one file under the user config dir) is
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
// the pure parser it feeds. Everything in this package is pure so it stays
// unit-testable and keeps the Docker SDK out of the mount/session planners.
package proximo

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/filippolmt/toolbox/internal/config"
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
// cfg.Proximo: an explicit true/false wins; when unset (nil) it auto-detects,
// enabling the integration iff proximo is set up on this host — i.e. its root
// CA exists (written by `proximo install`). So a host with proximo installed
// gets `.test` reachability in every shell with no per-repo opt-in, while a
// host without proximo pays nothing.
func Enabled(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	if cfg.Proximo != nil {
		return *cfg.Proximo
	}
	return caExists()
}

// CAPath returns proximo's root-CA file on the host, mirroring proximo's own
// layout: <user-config-dir>/proximo/tls/ca.pem. ok is false when the user
// config dir cannot be resolved.
func CAPath() (path string, ok bool) {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return "", false
	}
	return filepath.Join(dir, "proximo", "tls", "ca.pem"), true
}

// caExists reports whether proximo's root CA is present on the host — the
// auto-detect signal for Enabled and the existence gate for Env.
func caExists() bool {
	path, ok := CAPath()
	if !ok {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// CAMount returns the read-only bind for proximo's CA when integration is
// enabled and the CA path resolves. The mount resolver soft-skips it with a
// warning when the source file is absent (proximo not installed), so callers
// need not pre-check existence.
func CAMount(cfg *config.Config) (config.Mount, bool) {
	if !Enabled(cfg) {
		return config.Mount{}, false
	}
	path, ok := CAPath()
	if !ok {
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
// NODE_EXTRA_CA_CERTS file.
func Env(cfg *config.Config) []string {
	if !Enabled(cfg) || !caExists() {
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
