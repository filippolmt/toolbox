package proximo_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
	"github.com/filippolmt/toolbox/internal/proximo"
)

func forceOnCfg() *config.Config  { return &config.Config{Proximo: new(true)} }
func forceOffCfg() *config.Config { return &config.Config{Proximo: new(false)} }
func autoCfg() *config.Config     { return &config.Config{} } // Proximo nil → auto-detect

func TestExtraHostsDedupesSortsAndPinsGateway(t *testing.T) {
	got := proximo.ExtraHosts([]string{
		"zeromiglia.test, mailpit.test",
		"zeromiglia.test",
		"  api.test ",
		"",
		"   ",
	})
	want := []string{
		"api.test:host-gateway",
		"mailpit.test:host-gateway",
		"zeromiglia.test:host-gateway",
	}
	if !slices.Equal(got, want) {
		t.Errorf("ExtraHosts = %v, want %v", got, want)
	}
}

func TestExtraHostsEmpty(t *testing.T) {
	if got := proximo.ExtraHosts(nil); len(got) != 0 {
		t.Errorf("ExtraHosts(nil) = %v, want empty", got)
	}
	if got := proximo.ExtraHosts([]string{"", " , "}); len(got) != 0 {
		t.Errorf("ExtraHosts(blank) = %v, want empty", got)
	}
}

// TestResolveDecidesTheTristate covers the three Proximo states against both
// CA presences, and asserts all three of the gate's answers per arm — the
// point of one resolved value being that the mount and the env can no longer
// disagree with the decision they are supposed to follow.
//
// Explicit true/false win regardless of the CA; nil auto-detects on CA
// presence. The mount rides on the decision alone (a forced-on gate keeps it
// so the resolver can warn about the missing source), the env additionally on
// the file being there.
func TestResolveDecidesTheTristate(t *testing.T) {
	cases := []struct {
		name      string
		cfg       *config.Config
		ca        bool
		enabled   bool
		wantMount bool
		wantEnv   bool
	}{
		{name: "nil config"},
		{name: "auto, no CA", cfg: autoCfg()},
		{name: "auto, CA present", cfg: autoCfg(), ca: true, enabled: true, wantMount: true, wantEnv: true},
		{name: "explicit true, no CA", cfg: forceOnCfg(), enabled: true, wantMount: true},
		{name: "explicit true, CA present", cfg: forceOnCfg(), ca: true, enabled: true, wantMount: true, wantEnv: true},
		{name: "explicit false, no CA", cfg: forceOffCfg()},
		{name: "explicit false, CA present", cfg: forceOffCfg(), ca: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, caPath := setupCA(t, tc.ca)
			gate := proximo.Resolve(host, tc.cfg)

			if gate.Enabled != tc.enabled {
				t.Errorf("gate.Enabled = %v, want %v", gate.Enabled, tc.enabled)
			}
			m, ok := gate.CAMount()
			if ok != tc.wantMount {
				t.Errorf("gate.CAMount ok = %v, want %v", ok, tc.wantMount)
			}
			if ok {
				// The mount does not stat its source: a missing file is a
				// soft skip with a warning downstream, which is more
				// informative than silence.
				if m.Source != caPath {
					t.Errorf("gate.CAMount Source = %q, want %q", m.Source, caPath)
				}
				if m.Target != proximo.CATarget {
					t.Errorf("gate.CAMount Target = %q, want %q", m.Target, proximo.CATarget)
				}
				if !m.ReadOnly {
					t.Error("gate.CAMount must be read-only")
				}
			}
			if got := len(gate.Env()) > 0; got != tc.wantEnv {
				t.Errorf("gate.Env non-empty = %v, want %v (env %v)", got, tc.wantEnv, gate.Env())
			}
		})
	}
}

// TestResolveForcedOffPaysNoQuery pins the one arm that must short-circuit
// before the CA path is resolved: a workspace that opted out never spawns the
// proximo binary to be told something it already decided.
func TestResolveForcedOffPaysNoQuery(t *testing.T) {
	host, caPath := setupCA(t, true)
	queries := filepath.Join(t.TempDir(), "queries")
	host = fakeProximo(t, host, "echo q >>"+queries+"; echo "+caPath)

	if gate := proximo.Resolve(host, forceOffCfg()); gate.Enabled {
		t.Error("gate.Enabled = true for `proximo: false`, want false")
	}
	if got := queryCount(t, queries); got != 0 {
		t.Errorf("`proximo config ca-path` ran %d times for an opted-out config, want 0", got)
	}
}

// setupCA returns a Host with a home of its own and a PATH that resolves
// nothing — a machine with no proximo installed — optionally writing the CA
// file at the state-home fallback. The returned path is where the CA is (or
// would be).
//
// Nothing here rewrites $HOME or $PATH: declaring the host is the whole point
// of fsx.Host, and it is also what makes the answer deterministic on a
// developer machine (or this project's own image) that really does ship a
// proximo binary.
func setupCA(t *testing.T, write bool) (fsx.Host, string) {
	t.Helper()
	host := fsx.Host{Home: t.TempDir()} // no resolver → nothing on this host's PATH
	caPath := host.Join(".proximo", "tls", "ca.pem")
	if write {
		if err := os.MkdirAll(filepath.Dir(caPath), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(caPath, []byte("-----BEGIN CERTIFICATE-----\n"), 0o600); err != nil {
			t.Fatalf("write CA: %v", err)
		}
	}
	return host, caPath
}

// fakeProximo returns a copy of host whose PATH resolves an executable
// `proximo` stub with the given shell body — a host with proximo installed.
func fakeProximo(t *testing.T, host fsx.Host, script string) fsx.Host {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "proximo")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatalf("write fake proximo: %v", err)
	}
	host.LookPath = func(name string) (string, error) {
		if name != "proximo" {
			return "", exec.ErrNotFound
		}
		return bin, nil
	}
	return host
}

// TestResolveAutoDetectsViaTheQueriedPath pins auto-detect against the queried
// CA path: a proximo binary on PATH alone is NOT enough (`config ca-path`
// prints the path even before `proximo install` writes the CA — existence is
// our check), and once the CA exists at the path proximo reports, auto-detect
// turns on even though nothing sits at the ~/.proximo fallback.
func TestResolveAutoDetectsViaTheQueriedPath(t *testing.T) {
	host, _ := setupCA(t, false) // own home: nothing at the fallback location
	caPath := filepath.Join(t.TempDir(), "tls", "ca.pem")
	host = fakeProximo(t, host, "echo "+caPath)

	if proximo.Resolve(host, autoCfg()).Enabled {
		t.Error("binary on PATH but CA absent: auto-detect must stay off")
	}

	if err := os.MkdirAll(filepath.Dir(caPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(caPath, []byte("-----BEGIN CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	gate := proximo.Resolve(host, autoCfg())
	if !gate.Enabled {
		t.Error("CA present at queried path: auto-detect must turn on")
	}
	if gate.CAPath != caPath {
		t.Errorf("gate.CAPath = %q, want the queried path %q", gate.CAPath, caPath)
	}
}

// TestCAPathPrefersProximoQuery pins the stable contract from
// filippolmt/proximo#20: when `proximo config ca-path` answers, its output
// wins over the hardcoded state-home fallback.
func TestCAPathPrefersProximoQuery(t *testing.T) {
	host, _ := setupCA(t, false)
	host = fakeProximo(t, host, "echo /custom/state/tls/ca.pem")
	path, ok := proximo.CAPath(host)
	if !ok || path != "/custom/state/tls/ca.pem" {
		t.Errorf("CAPath = %q, %v; want query result /custom/state/tls/ca.pem, true", path, ok)
	}
}

// TestCAPathFallback covers every degraded query against the same expectation:
// the ~/.proximo/tls/ca.pem state-home fallback.
func TestCAPathFallback(t *testing.T) {
	cases := []struct {
		name   string
		script string // empty → no proximo binary on PATH at all
	}{
		{name: "no proximo on PATH"},
		{name: "subcommand missing (pre-#20 proximo)", script: "exit 1"},
		{name: "junk stdout", script: "echo not-an-absolute-path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, want := setupCA(t, false)
			if tc.script != "" {
				host = fakeProximo(t, host, tc.script)
			}
			path, ok := proximo.CAPath(host)
			if !ok || path != want {
				t.Errorf("CAPath = %q, %v; want fallback %q, true", path, ok, want)
			}
		})
	}
}

// TestResolveWithNoResolvableCAPath covers the host that answers nothing: no
// proximo on PATH and no home to hang the state-home fallback off — the
// degraded shape mountplan.Merge relies on, where a Host with no home must
// drop the CA bind rather than fail. Auto-detect has nothing to detect, and
// even a forced-on config gets a gate with no mount and no trust env: the
// decision is the config's to make, the CA is not.
func TestResolveWithNoResolvableCAPath(t *testing.T) {
	host := fsx.Host{} // no home, no resolver

	if gate := proximo.Resolve(host, autoCfg()); gate.Enabled || gate.CAPath != "" {
		t.Errorf("auto-detect on a host with no CA path = %+v, want the zero gate", gate)
	}

	gate := proximo.Resolve(host, forceOnCfg())
	if !gate.Enabled {
		t.Error("gate.Enabled = false for `proximo: true`, want the config's answer")
	}
	if gate.CAPath != "" {
		t.Errorf("gate.CAPath = %q, want empty on a host that resolves none", gate.CAPath)
	}
	if _, ok := gate.CAMount(); ok {
		t.Error("gate.CAMount reported a mount with no CA path to mount")
	}
	if got := gate.Env(); got != nil {
		t.Errorf("gate.Env = %v, want nil with no CA on the host", got)
	}
}

// TestGateEnvNamesBothTrustVariables pins the two entries themselves: Node
// reads its own bundle (NODE_EXTRA_CA_CERTS) and TOOLBOX_PROXIMO_CA is the
// path pointer for the certifi gap. Both point at the in-container mount
// target, never at the host path the gate resolved.
func TestGateEnvNamesBothTrustVariables(t *testing.T) {
	withCA, _ := setupCA(t, true)
	got := proximo.Resolve(withCA, forceOnCfg()).Env()
	wantNode := "NODE_EXTRA_CA_CERTS=" + proximo.CATarget
	wantCurl := "TOOLBOX_PROXIMO_CA=" + proximo.CATarget
	if !slices.Contains(got, wantNode) {
		t.Errorf("Env missing %q, got %v", wantNode, got)
	}
	if !slices.Contains(got, wantCurl) {
		t.Errorf("Env missing %q, got %v", wantCurl, got)
	}
}

// TestResolveQueriesProximoOnceForEveryReader is the reason the gate is a
// value rather than three derivations: the CA path query is a subprocess
// spawn, and Resolve pays it once for the whole invocation no matter how many
// of the gate's answers a caller goes on to read.
func TestResolveQueriesProximoOnceForEveryReader(t *testing.T) {
	host, caPath := setupCA(t, true)
	queries := filepath.Join(t.TempDir(), "queries")
	host = fakeProximo(t, host, "echo q >>"+queries+"; echo "+caPath)

	gate := proximo.Resolve(host, autoCfg())

	// Every reader the callers use, in the order a session asks them.
	if !gate.Enabled {
		t.Error("gate.Enabled = false with the CA present, want true")
	}
	if _, ok := gate.CAMount(); !ok {
		t.Error("gate.CAMount reported no mount with the CA present")
	}
	if len(gate.Env()) == 0 {
		t.Error("gate.Env is empty with the CA present")
	}

	if got := queryCount(t, queries); got != 1 {
		t.Errorf("`proximo config ca-path` ran %d times, want exactly 1 per Resolve", got)
	}
}

// queryCount reports how many lines the fake proximo appended to path — one
// per exec, zero when the file was never created.
func queryCount(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read query log: %v", err)
	}
	return len(strings.Split(strings.TrimSpace(string(b)), "\n"))
}
