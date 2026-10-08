package build

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/filippolmt/toolbox/internal/proximo"
)

// TestEntrypointSyncsProximoHostsBeforeTheWatcherAndShell pins the startup
// contract: one bounded foreground attempt finishes before the background
// watcher starts, and both happen before the interactive command is execed.
func TestEntrypointSyncsProximoHostsBeforeTheWatcherAndShell(t *testing.T) {
	body, err := fs.ReadFile(Assets, AssetDir+"/entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	routeGate := strings.Index(text, `if [ -f "$_proximo_ca" ] || [ "${TOOLBOX_PROXIMO_ENABLED:-}" = "1" ]; then`)
	sync := strings.Index(text, "if ! timeout ")
	watch := strings.Index(text, "setsid nohup proximo-hosts --watch")
	shell := strings.LastIndex(text, `exec "$@"`)
	if routeGate < 0 || sync < 0 || watch < 0 || shell < 0 {
		t.Fatalf("entrypoint startup sequence missing: gate=%d sync=%d watch=%d shell=%d", routeGate, sync, watch, shell)
	}
	if !strings.Contains(text[sync:watch], "proximo-hosts") {
		t.Fatal("bounded startup command does not run proximo-hosts")
	}
	if routeGate >= sync || sync >= watch || watch >= shell {
		t.Fatalf("entrypoint startup order = gate:%d sync:%d watch:%d shell:%d, want gate < sync < watch < shell", routeGate, sync, watch, shell)
	}
}

type proximoHostsHarness struct {
	dir, script, inventoryDir, hosts, bin string
}

func newProximoHostsHarness(t *testing.T, hostsContent string) *proximoHostsHarness {
	t.Helper()
	body, err := fs.ReadFile(Assets, AssetDir+"/bin/proximo-hosts")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	h := &proximoHostsHarness{
		dir:          dir,
		script:       filepath.Join(dir, "proximo-hosts"),
		inventoryDir: filepath.Join(dir, "inventory"),
		hosts:        filepath.Join(dir, "hosts"),
		bin:          filepath.Join(dir, "bin"),
	}
	if err := os.WriteFile(h.script, body, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.hosts, []byte(hostsContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(h.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *proximoHostsHarness) writeCommand(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.bin, name), []byte("#!/bin/sh\n"+content+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (h *proximoHostsHarness) env(extra ...string) []string {
	return append(os.Environ(), append([]string{
		"PATH=" + h.bin + ":" + os.Getenv("PATH"),
		"TOOLBOX_PROXIMO_INVENTORY_DIR=" + h.inventoryDir,
		"TOOLBOX_HOSTS_FILE=" + h.hosts,
	}, extra...)...)
}

// TestProximoHostsReadsTheEffectiveInventory exercises the shipped command at
// its user-visible seam: only effectively served local names reach /etc/hosts,
// and inventory mode never asks Docker to reconstruct proximo's model.
func TestProximoHostsReadsTheEffectiveInventory(t *testing.T) {
	h := newProximoHostsHarness(t, "127.0.0.1 localhost\n")
	body, err := os.ReadFile(h.script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `INVENTORY_DIR="${TOOLBOX_PROXIMO_INVENTORY_DIR:-`+proximo.InventoryTarget+`}"`) {
		t.Fatalf("proximo-hosts inventory target drifted from %q", proximo.InventoryTarget)
	}
	if err := os.Mkdir(h.inventoryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	inventory := `{"routes":[
		{"bare":"api.test","qualified":"api.shop.test","peer":{"bare":"api.machine.example"}},
		{"claimed":"starting.test","warning":"starting"},
		{"collision":{"host":"lost.test","served_by":"other"}}
	]}`
	if err := os.WriteFile(filepath.Join(h.inventoryDir, "routes.json"), []byte(inventory), 0o644); err != nil {
		t.Fatal(err)
	}
	h.writeCommand(t, "getent", `echo "192.168.65.254 STREAM host.docker.internal"`)
	h.writeCommand(t, "sudo", `exec "$@"`)
	h.writeCommand(t, "docker", `echo "docker must not be called in inventory mode" >&2; exit 99`)

	cmd := exec.Command(h.script)
	cmd.Env = h.env()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("proximo-hosts: %v\n%s", err, out)
	}
	got, err := os.ReadFile(h.hosts)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{"192.168.65.254\tapi.test", "192.168.65.254\tapi.shop.test"} {
		if !strings.Contains(text, want) {
			t.Errorf("hosts missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"api.machine.example", "starting.test", "lost.test"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("hosts unexpectedly contains %q:\n%s", unwanted, text)
		}
	}
}

// TestProximoHostsFallsBackToLegacyLabels keeps older proximo installations
// reachable when no effective-route inventory is mounted.
func TestProximoHostsFallsBackToLegacyLabels(t *testing.T) {
	h := newProximoHostsHarness(t, "127.0.0.1 localhost\n")
	h.writeCommand(t, "getent", `echo "192.168.65.254 STREAM host.docker.internal"`)
	h.writeCommand(t, "sudo", `exec "$@"`)
	h.writeCommand(t, "docker", `case "$1" in ps) printf 'one\ntwo\n' ;; inspect) printf ' api.test, mailpit.test \napi.test\n' ;; *) exit 99 ;; esac`)

	cmd := exec.Command(h.script)
	cmd.Env = h.env()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("proximo-hosts: %v\n%s", err, out)
	}
	contents, err := os.ReadFile(h.hosts)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	for _, want := range []string{"192.168.65.254\tapi.test", "192.168.65.254\tmailpit.test"} {
		if !strings.Contains(text, want) {
			t.Errorf("hosts missing %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "api.test") != 1 {
		t.Errorf("duplicate legacy host was not removed:\n%s", text)
	}
}

const projectedHosts = "127.0.0.1 localhost\n# >>> toolbox proximo (managed) >>>\n192.168.65.254\tlast.test\n# <<< toolbox proximo (managed) <<<\n"

func assertInventoryFailurePreservesProjection(t *testing.T, prepare func(*proximoHostsHarness)) {
	t.Helper()
	h := newProximoHostsHarness(t, projectedHosts)
	if err := os.Mkdir(h.inventoryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	prepare(h)
	h.writeCommand(t, "getent", `echo "192.168.65.254 STREAM host.docker.internal"`)
	h.writeCommand(t, "docker", `touch "$DOCKER_CALLED"; exit 99`)
	h.writeCommand(t, "sudo", `echo "sudo must not be called after discovery fails" >&2; exit 99`)

	dockerCalled := filepath.Join(h.dir, "docker-called")
	cmd := exec.Command(h.script)
	cmd.Env = h.env("DOCKER_CALLED=" + dockerCalled)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("proximo-hosts unexpectedly succeeded:\n%s", out)
	}
	if _, err := os.Stat(dockerCalled); !os.IsNotExist(err) {
		t.Fatalf("legacy Docker discovery ran for mounted inventory: %v", err)
	}
	contents, err := os.ReadFile(h.hosts)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != projectedHosts {
		t.Fatalf("hosts changed after inventory failure:\n%s", contents)
	}
}

// TestProximoHostsKeepsTheLastProjectionWhenInventoryIsUnreadable ensures a
// transient publication failure neither falls back to declared intent nor
// destroys the last valid managed block.
func TestProximoHostsKeepsTheLastProjectionWhenInventoryIsUnreadable(t *testing.T) {
	assertInventoryFailurePreservesProjection(t, func(h *proximoHostsHarness) {
		if err := os.WriteFile(filepath.Join(h.inventoryDir, proximo.InventoryFile), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
}

// TestProximoHostsKeepsTheLastProjectionWhenMountedInventoryIsMissing ensures
// a temporary gap during publication is not mistaken for an older Proximo.
func TestProximoHostsKeepsTheLastProjectionWhenMountedInventoryIsMissing(t *testing.T) {
	assertInventoryFailurePreservesProjection(t, func(*proximoHostsHarness) {})
}

// TestProximoHostsWatchFollowsAtomicInventoryReplacement proves the route
// projection observes proximo's rename-based publication without a Docker
// event or a container recreate.
func TestProximoHostsWatchFollowsAtomicInventoryReplacement(t *testing.T) {
	h := newProximoHostsHarness(t, "127.0.0.1 localhost\n")
	if err := os.Mkdir(h.inventoryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	inventory := filepath.Join(h.inventoryDir, proximo.InventoryFile)
	if err := os.WriteFile(inventory, []byte(`{"routes":[{"bare":"first.test"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h.writeCommand(t, "getent", `echo "192.168.65.254 STREAM host.docker.internal"`)
	h.writeCommand(t, "sudo", `exec "$@"`)
	h.writeCommand(t, "docker", `exit 99`)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, h.script, "--watch")
	cmd.Env = h.env()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()
	waitForHostsEntry(t, h.hosts, "first.test")

	next := filepath.Join(h.inventoryDir, "routes.next")
	if err := os.WriteFile(next, []byte(`{"routes":[{"qualified":"second.app.test"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, inventory); err != nil {
		t.Fatal(err)
	}
	waitForHostsEntry(t, h.hosts, "second.app.test")
	contents, err := os.ReadFile(h.hosts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "first.test") {
		t.Fatalf("stale inventory entry survived replacement:\n%s", contents)
	}
}

func waitForHostsEntry(t *testing.T, path, entry string) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(contents), entry) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	contents, _ := os.ReadFile(path)
	t.Fatalf("hosts never contained %q:\n%s", entry, contents)
}
