package build

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The Orca Shell's init script, run as the image ships it with a stubbed
// `curl` (serving a fixture release from $FAKE_RELEASE) and `uname`, and a
// temporary data directory. `--foreground` is the half the entrypoint
// backgrounds, so a case observes the install and the start synchronously.

const orcaInitScript = "init.d/80-orca.sh"

// fakeOrcaRelease writes a release directory holding the server archive for
// arch, laid out like the real one (orca-server-<v>-linux-<arch>/bin/orca-ide),
// and its checksum file. The fake orca-ide records its argv and the data path
// it was given, and prints one line, as the server's readiness record would.
func fakeOrcaRelease(t *testing.T, arch string, corrupt bool) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	top := "orca-server-1.4.262-linux-" + arch + "/"
	server := `#!/bin/sh
echo "orca-ide $* ORCA_USER_DATA_PATH=$ORCA_USER_DATA_PATH" >> "$ORCA_USER_DATA_PATH/served"
echo '{"type":"orca_server_ready"}'
`
	for _, h := range []*tar.Header{
		{Name: top, Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: top + "bin/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: top + "bin/orca-ide", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(server))},
	} {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(server)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	archive := "orca-server-linux-" + arch + ".tar.gz"
	sum := sha256.Sum256(buf.Bytes())
	digest := hex.EncodeToString(sum[:])
	if corrupt {
		digest = strings.Repeat("0", len(digest))
	}
	sums := "1111111111111111111111111111111111111111111111111111111111111111  orca-server-linux-other.tar.gz\n" +
		digest + "  " + archive + "\n"
	for name, body := range map[string][]byte{archive: buf.Bytes(), "orca-server-sha256sums.txt": []byte(sums)} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

type orcaRun struct {
	out, calls string
	code       int
}

// runOrcaInit runs the embedded script with the stubs first on PATH.
func runOrcaInit(t *testing.T, data, release, machine string, env []string, args ...string) orcaRun {
	t.Helper()
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	writeExecutable(t, filepath.Join(bin, "uname"), "#!/bin/sh\necho \"$FAKE_MACHINE\"\n")
	writeExecutable(t, filepath.Join(bin, "curl"), `#!/bin/sh
out=""; url=""
while [ $# -gt 0 ]; do
	case "$1" in -o) out="$2"; shift ;; http*) url="$1" ;; esac
	shift
done
echo "curl $url" >> "$CALLS"
cp "$FAKE_RELEASE/$(basename "$url")" "$out"
`)
	script := filepath.Join(t.TempDir(), "80-orca.sh")
	writeExecutable(t, script, readAsset(t, orcaInitScript))

	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(),
		"HOME="+t.TempDir(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CALLS="+calls,
		"FAKE_RELEASE="+release,
		"FAKE_MACHINE="+machine,
		"ORCA_USER_DATA_PATH="+data,
	)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run %s: %v", orcaInitScript, err)
	}
	recorded, _ := os.ReadFile(calls)
	return orcaRun{out: string(out), calls: string(recorded), code: code}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// Without the gate the script does nothing: an ordinary shell never runs Orca code.
func TestOrcaInitWithoutTheGateDoesNothing(t *testing.T) {
	data := filepath.Join(t.TempDir(), "orca")
	run := runOrcaInit(t, data, fakeOrcaRelease(t, "arm64", false), "aarch64", nil)
	if run.code != 0 || run.calls != "" {
		t.Errorf("exit %d, calls %q; want 0 and none\n%s", run.code, run.calls, run.out)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Errorf("data dir created without the gate (stat err %v)", err)
	}
}

// With the gate, the release for the container's architecture is verified,
// installed into its versioned directory, linked as current and started with
// the agreed flags; the server's output lands in serve.log.
func TestOrcaInitInstallsVerifiesAndStarts(t *testing.T) {
	for machine, arch := range map[string]string{"aarch64": "arm64", "x86_64": "x64"} {
		t.Run(machine, func(t *testing.T) {
			data := filepath.Join(t.TempDir(), "orca")
			run := runOrcaInit(t, data, fakeOrcaRelease(t, arch, false), machine,
				[]string{"TOOLBOX_ORCA_VERSION=v1.4.262"}, "--foreground")
			if run.code != 0 {
				t.Fatalf("exit %d\n%s\n%s", run.code, run.out, readFile(t, filepath.Join(data, "serve.log")))
			}
			base := "https://github.com/stablyai/orca/releases/download/v1.4.262/"
			for _, want := range []string{base + "orca-server-linux-" + arch + ".tar.gz", base + "orca-server-sha256sums.txt"} {
				if !strings.Contains(run.calls, want) {
					t.Errorf("no download of %s; calls:\n%s", want, run.calls)
				}
			}
			if link, err := os.Readlink(filepath.Join(data, "server", "current")); err != nil || link != "v1.4.262" {
				t.Errorf("current -> %q (%v), want v1.4.262", link, err)
			}
			served := readFile(t, filepath.Join(data, "served"))
			for _, want := range []string{"orca-ide serve", "--port 6768", "--bind 0.0.0.0", "--pairing-address 127.0.0.1", "--json"} {
				if !strings.Contains(served, want) {
					t.Errorf("server argv %q lacks %q", served, want)
				}
			}
			if !strings.Contains(readFile(t, filepath.Join(data, "serve.log")), `"orca_server_ready"`) {
				t.Error("serve.log does not hold the server's output")
			}
			if entries, _ := os.ReadDir(filepath.Join(data, "server")); len(entries) != 2 {
				t.Errorf("server dir holds %d entries, want the version and current only: %v", len(entries), entries)
			}
		})
	}
}

// A checksum mismatch installs nothing, leaves current on the previous
// version, starts nothing and says why in the log.
func TestOrcaInitRefusesAChecksumMismatch(t *testing.T) {
	data := filepath.Join(t.TempDir(), "orca")
	if err := os.MkdirAll(filepath.Join(data, "server", "v1.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("v1.0.0", filepath.Join(data, "server", "current")); err != nil {
		t.Fatal(err)
	}

	run := runOrcaInit(t, data, fakeOrcaRelease(t, "arm64", true), "aarch64",
		[]string{"TOOLBOX_ORCA_VERSION=v1.4.262"}, "--foreground")

	if run.code == 0 {
		t.Error("exit 0 on a checksum mismatch")
	}
	if _, err := os.Stat(filepath.Join(data, "server", "v1.4.262")); !os.IsNotExist(err) {
		t.Errorf("mismatched version installed (stat err %v)", err)
	}
	if link, _ := os.Readlink(filepath.Join(data, "server", "current")); link != "v1.0.0" {
		t.Errorf("current -> %q, want v1.0.0 untouched", link)
	}
	if _, err := os.Stat(filepath.Join(data, "served")); !os.IsNotExist(err) {
		t.Error("a server started after a checksum mismatch")
	}
	if !strings.Contains(readFile(t, filepath.Join(data, "serve.log")), "checksum") {
		t.Error("serve.log does not say the checksum failed")
	}
}

// An installed version is not downloaded again.
func TestOrcaInitReusesAnInstalledVersion(t *testing.T) {
	data := filepath.Join(t.TempDir(), "orca")
	release := fakeOrcaRelease(t, "arm64", false)
	env := []string{"TOOLBOX_ORCA_VERSION=v1.4.262"}
	if run := runOrcaInit(t, data, release, "aarch64", env, "--foreground"); run.code != 0 {
		t.Fatalf("first run exit %d\n%s", run.code, run.out)
	}

	run := runOrcaInit(t, data, release, "aarch64", env, "--foreground")
	if run.code != 0 || run.calls != "" {
		t.Errorf("second run exit %d, calls %q; want 0 and no download", run.code, run.calls)
	}
	if n := strings.Count(readFile(t, filepath.Join(data, "served")), "orca-ide serve"); n != 2 {
		t.Errorf("server started %d times, want 2", n)
	}
}
