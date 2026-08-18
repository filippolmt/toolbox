package build

import (
	"strings"
	"testing"
)

// TestGlabCredentialHelperProbesPerHost pins what keeps the glab credential
// helper working when glab's config is only partially healthy.
//
// That config is a single host-shared file (~/.config/glab-cli, a RW host
// mount) holding one block per host, and an OAuth refresh token is single-use —
// so one host's session dying while the others stay healthy is the normal
// steady state, not an edge case. A bare `glab auth status` exits non-zero when
// ANY host fails. Gate the block on it alone and a single dead host costs every
// healthy host its `credential.https://<host>.helper`, silently: private HTTPS
// clones (`brew tap` of a private tap) fall back to a terminal prompt that
// fatals in non-interactive callers.
//
// 60-glab.sh is a static shell asset no Go code reads, so only a test over the
// embedded bytes can hold this — same technique as TestWorkspaceInstallRefreshGate.
func TestGlabCredentialHelperProbesPerHost(t *testing.T) {
	body := readAsset(t, "init.d/60-glab.sh")

	needles := []string{
		// Per-host probe, and the two lists it sorts hosts into.
		`glab auth status --hostname "${_glab_host}"`,
		`_glab_authed="${_glab_authed}${_glab_host} "`,
		`_glab_broken="${_glab_broken}${_glab_host} "`,
		// Registration is driven by the authenticated list alone, ungated: an
		// empty list just skips the loop.
		`for _glab_host in ${_glab_authed}; do`,
		`git config --system "credential.https://${_glab_host}.helper" "!${_glab_bin} auth git-credential"`,
		// A parse failure keeps its own diagnostic — "yq or glab config
		// missing" would name the wrong cause when both are present.
		`glab config parse failed`,
		// Middle state of D-08-creds-tristate: hosts are configured but
		// rejected live. 08-oci-creds.sh records why it must exist — the
		// original two-way probe "lumped 'config file missing' and 'config
		// present + live API rejection' into the same 'not configured' string
		// — hiding expired keys". It names the broken host, and the remedy is
		// a command the user can paste.
		`  glab: auth check failed for ${_glab_broken% } (try `,
		`glab auth login --hostname ${_glab_broken%% *}`,
	}
	for _, needle := range needles {
		if !strings.Contains(body, needle) {
			t.Errorf("60-glab.sh: missing %q — the per-host glab auth probe drifted", needle)
		}
	}

	// Ordering, not just presence: the middle state must be reached BEFORE the
	// "not configured" branch, or a host with expired credentials gets reported
	// as unconfigured — the exact lumping D-08-creds-tristate removed.
	mid := strings.Index(body, `glab: auth check failed for`)
	none := strings.Index(body, `glab: not configured`)
	if mid < 0 || none < 0 {
		t.Fatal("60-glab.sh: cannot locate both the middle and the not-configured probe lines")
	}
	if mid > none {
		t.Error("60-glab.sh: the `not configured` branch precedes `auth check failed` — a host with expired credentials would be reported as unconfigured")
	}

	// The bare probe must run FIRST, as a fast path, with the per-host loop as
	// its fallback. glab rewrites the whole shared config.yml whenever a probe
	// refreshes an expired token, so probing every host unconditionally turns
	// one unlocked read-modify-write per boot into N of them, racing the other
	// containers on the same mount.
	fast := strings.Index(body, `if glab auth status >/dev/null 2>&1; then`)
	perHost := strings.Index(body, `for _glab_host in ${_glab_hosts}; do`)
	if fast < 0 || perHost < 0 {
		t.Fatal("60-glab.sh: cannot locate both the bare fast-path probe and the per-host loop")
	}
	if fast > perHost {
		t.Error("60-glab.sh: the per-host loop runs before the bare fast-path probe — every boot then pays N config rewrites instead of 1")
	}
}
