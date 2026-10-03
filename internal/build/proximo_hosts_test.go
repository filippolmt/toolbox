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

// TestProximoHostsReadsTheEffectiveInventory exercises the shipped command at
// its user-visible seam: only effectively served local names reach /etc/hosts,
// and inventory mode never asks Docker to reconstruct proximo's model.
func TestProximoHostsReadsTheEffectiveInventory(t *testing.T) {
	body, err := fs.ReadFile(Assets, AssetDir+"/bin/proximo-hosts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `INVENTORY_DIR="${TOOLBOX_PROXIMO_INVENTORY_DIR:-`+proximo.InventoryTarget+`}"`) {
		t.Fatalf("proximo-hosts inventory target drifted from %q", proximo.InventoryTarget)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "proximo-hosts")
	if err := os.WriteFile(script, body, 0o755); err != nil {
		t.Fatal(err)
	}
	inventoryDir := filepath.Join(dir, "inventory")
	if err := os.MkdirAll(inventoryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	inventory := `{"routes":[
		{"bare":"api.test","qualified":"api.shop.test","peer":{"bare":"api.machine.example"}},
		{"claimed":"starting.test","warning":"starting"},
		{"collision":{"host":"lost.test","served_by":"other"}}
	]}`
	if err := os.WriteFile(filepath.Join(inventoryDir, "routes.json"), []byte(inventory), 0o644); err != nil {
		t.Fatal(err)
	}
	hosts := filepath.Join(dir, "hosts")
	if err := os.WriteFile(hosts, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCommand := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+content+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeCommand("getent", `echo "192.168.65.254 STREAM host.docker.internal"`)
	writeCommand("sudo", `exec "$@"`)
	writeCommand("docker", `echo "docker must not be called in inventory mode" >&2; exit 99`)

	cmd := exec.Command(script)
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"TOOLBOX_PROXIMO_INVENTORY_DIR="+inventoryDir,
		"TOOLBOX_HOSTS_FILE="+hosts,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("proximo-hosts: %v\n%s", err, out)
	}
	got, err := os.ReadFile(hosts)
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

// TestProximoHostsWatchFollowsAtomicInventoryReplacement proves the runtime
// complement observes proximo's rename-based publication without a Docker
// event or a container recreate.
func TestProximoHostsWatchFollowsAtomicInventoryReplacement(t *testing.T) {
	body, err := fs.ReadFile(Assets, AssetDir+"/bin/proximo-hosts")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "proximo-hosts")
	if err := os.WriteFile(script, body, 0o755); err != nil {
		t.Fatal(err)
	}
	inventoryDir := filepath.Join(dir, "inventory")
	if err := os.Mkdir(inventoryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	inventory := filepath.Join(inventoryDir, proximo.InventoryFile)
	if err := os.WriteFile(inventory, []byte(`{"routes":[{"bare":"first.test"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	hosts := filepath.Join(dir, "hosts")
	if err := os.WriteFile(hosts, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"getent": `echo "192.168.65.254 STREAM host.docker.internal"`,
		"sudo":   `exec "$@"`,
		"docker": `exit 99`,
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+content+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, script, "--watch")
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"TOOLBOX_PROXIMO_INVENTORY_DIR="+inventoryDir,
		"TOOLBOX_HOSTS_FILE="+hosts,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()
	waitForHostsEntry(t, hosts, "first.test")

	next := filepath.Join(inventoryDir, "routes.next")
	if err := os.WriteFile(next, []byte(`{"routes":[{"qualified":"second.app.test"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, inventory); err != nil {
		t.Fatal(err)
	}
	waitForHostsEntry(t, hosts, "second.app.test")
	contents, err := os.ReadFile(hosts)
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
