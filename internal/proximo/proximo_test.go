package proximo_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// TestEnabledTristate covers the three Proximo states against both CA presences.
// Explicit true/false win regardless of the CA; nil auto-detects on CA presence.
func TestEnabledTristate(t *testing.T) {
	noCA, _ := setupCA(t, false)
	if proximo.Enabled(noCA, nil) {
		t.Error("Enabled(nil) must be false")
	}

	// No CA on host: auto → off; explicit true → on; explicit false → off.
	if proximo.Enabled(noCA, autoCfg()) {
		t.Error("auto-detect with no CA must be off")
	}
	if !proximo.Enabled(noCA, forceOnCfg()) {
		t.Error("explicit true must be on even without a CA")
	}
	if proximo.Enabled(noCA, forceOffCfg()) {
		t.Error("explicit false must be off")
	}

	// CA present: auto → on; explicit false still wins (off).
	withCA, _ := setupCA(t, true)
	if !proximo.Enabled(withCA, autoCfg()) {
		t.Error("auto-detect with CA present must be on")
	}
	if proximo.Enabled(withCA, forceOffCfg()) {
		t.Error("explicit false must beat an installed CA")
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

// TestEnabledAutoDetectViaQuery pins auto-detect against the queried CA path:
// a proximo binary on PATH alone is NOT enough (`config ca-path` prints the
// path even before `proximo install` writes the CA — existence is our check),
// and once the CA exists at the path proximo reports, auto-detect turns on
// even though nothing sits at the ~/.proximo fallback.
func TestEnabledAutoDetectViaQuery(t *testing.T) {
	host, _ := setupCA(t, false) // own home: nothing at the fallback location
	caPath := filepath.Join(t.TempDir(), "tls", "ca.pem")
	host = fakeProximo(t, host, "echo "+caPath)

	if proximo.Enabled(host, autoCfg()) {
		t.Error("binary on PATH but CA absent: auto-detect must stay off")
	}

	if err := os.MkdirAll(filepath.Dir(caPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(caPath, []byte("-----BEGIN CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	if !proximo.Enabled(host, autoCfg()) {
		t.Error("CA present at queried path: auto-detect must turn on")
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

func TestCAMountForceOffEvenWithCA(t *testing.T) {
	host, _ := setupCA(t, true)
	if _, ok := proximo.CAMount(host, forceOffCfg()); ok {
		t.Error("CAMount must be absent when proximo is force-disabled")
	}
}

func TestCAMountPresentEvenWithoutFile(t *testing.T) {
	// CAMount does not stat the source: the mount resolver soft-skips a
	// missing file with a warning, which is more informative than silence.
	host, caPath := setupCA(t, false)
	m, ok := proximo.CAMount(host, forceOnCfg())
	if !ok {
		t.Fatal("CAMount should be present when enabled")
	}
	if m.Source != caPath {
		t.Errorf("CAMount.Source = %q, want %q", m.Source, caPath)
	}
	if m.Target != proximo.CATarget {
		t.Errorf("CAMount.Target = %q, want %q", m.Target, proximo.CATarget)
	}
	if !m.ReadOnly {
		t.Error("CAMount must be read-only")
	}
}

func TestEnvGatedOnExistence(t *testing.T) {
	noCA, _ := setupCA(t, false)
	if got := proximo.Env(noCA, forceOffCfg()); got != nil {
		t.Errorf("Env(force-off) = %v, want nil", got)
	}

	if got := proximo.Env(noCA, forceOnCfg()); got != nil {
		t.Errorf("Env(force-on, no CA file) = %v, want nil", got)
	}

	withCA, _ := setupCA(t, true)
	got := proximo.Env(withCA, forceOnCfg())
	wantNode := "NODE_EXTRA_CA_CERTS=" + proximo.CATarget
	wantCurl := "TOOLBOX_PROXIMO_CA=" + proximo.CATarget
	if !slices.Contains(got, wantNode) {
		t.Errorf("Env missing %q, got %v", wantNode, got)
	}
	if !slices.Contains(got, wantCurl) {
		t.Errorf("Env missing %q, got %v", wantCurl, got)
	}
}
