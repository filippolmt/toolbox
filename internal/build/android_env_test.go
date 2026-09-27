package build

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/mountplan"
)

// androidEnv sources the embedded android-env.sh (what zshrc.sh sources) with
// HOME set to home and a stub getent that resolves host.docker.internal the
// way Docker Desktop does, then returns the environment it leaves behind.
func androidEnv(t *testing.T, home string, env ...string) map[string]string {
	t.Helper()
	body, err := fs.ReadFile(Assets, AssetDir+"/bin/android-env.sh")
	if err != nil {
		t.Fatalf("read embedded android-env.sh: %v", err)
	}
	dir := t.TempDir()
	lib := filepath.Join(dir, "android-env.sh")
	writeFile(t, lib, string(body))
	writeFile(t, filepath.Join(dir, "bin", "getent"), `#!/bin/sh
[ "$1 $2" = "ahostsv4 host.docker.internal" ] && echo "192.168.65.254  STREAM host.docker.internal"
`)
	cmd := exec.Command("sh", "-c", `. "$0" && env`, lib)
	cmd.Env = append([]string{
		"HOME=" + home,
		"PATH=" + filepath.Join(dir, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
	}, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("source android-env.sh: %v\n%s", err, out)
	}
	vars := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			vars[k] = v
		}
	}
	return vars
}

// The bind is CreateIfMissing, so it always exists: the shell exports the
// Android variables only once the installer has populated it (ADR 0017).
func TestAndroidEnvIsSilentUntilTheSdkIsInstalled(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".android-sdk"), 0o755); err != nil {
		t.Fatal(err)
	}

	vars := androidEnv(t, home)

	for _, k := range []string{"ANDROID_HOME", "ADB_SERVER_SOCKET", "ANDROID_USER_HOME", "ANDROID_CLI_BIN"} {
		if v, ok := vars[k]; ok {
			t.Errorf("%s=%s exported with an empty SDK bind", k, v)
		}
	}
}

// Once installed, adb talks to the host's own server over the IPv4 gateway
// (host.docker.internal resolves IPv6 first, and that address is
// unreachable), and android-cli keeps its home in the bind, without metrics.
func TestAndroidEnvPointsAdbAtTheHostDeviceServerOnceInstalled(t *testing.T) {
	home := t.TempDir()
	sdk := filepath.Join(home, ".android-sdk")
	writeFile(t, filepath.Join(sdk, "platform-tools", "adb"), "#!/bin/sh\n")

	vars := androidEnv(t, home)

	want := map[string]string{
		"ANDROID_HOME":      sdk,
		"ADB_SERVER_SOCKET": "tcp:192.168.65.254:5037",
		"ANDROID_USER_HOME": filepath.Join(sdk, "user-home"),
		"ANDROID_CLI_BIN":   filepath.Join(sdk, "android-cli-no-metrics"),
	}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("%s = %q, want %q", k, vars[k], v)
		}
	}
}

// A developer who points adb somewhere else (another host, another port)
// keeps that choice.
func TestAndroidEnvKeepsAnAdbServerSocketTheDeveloperSet(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".android-sdk", "platform-tools", "adb"), "#!/bin/sh\n")

	vars := androidEnv(t, home, "ADB_SERVER_SOCKET=tcp:10.0.0.5:5037")

	if got := vars["ADB_SERVER_SOCKET"]; got != "tcp:10.0.0.5:5037" {
		t.Errorf("ADB_SERVER_SOCKET = %q, want the developer's own", got)
	}
}

// The Foreign-Arch Runtime's two symlinks exist on arm64 only: on amd64 the
// paths hold the real libraries and the distro's /lib64 link, and CI
// smoke-tests only amd64, so a link reaching that image would break every
// binary in it while
// the arm64 build it was meant for goes unexercised. The link also has to
// point into the android-sdk bind's target, where the installer builds the
// runtime.
func TestForeignArchRuntimeSymlinksAreArm64OnlyAndPointIntoTheSdkBind(t *testing.T) {
	var sdk string
	for _, m := range mountplan.Defaults() {
		if m.Name == "android-sdk" {
			sdk = m.Target
		}
	}
	if sdk == "" {
		t.Fatal("no android-sdk default mount")
	}
	stage := finalStage(t)
	block := regexp.MustCompile(`(?s)if \[ "\$\{TARGETARCH\}" = "arm64" \]; then(.*?)\bfi\b`).FindAllStringSubmatch(stage, -1)
	links := []string{
		"ln -s " + sdk + "/x86_64-runtime /usr/lib/x86_64-linux-gnu",
		"ln -s /usr/lib/x86_64-linux-gnu /lib64",
	}
	for _, l := range links {
		if n := strings.Count(stage, l); n != 1 {
			t.Errorf("final stage has %d copies of %q, want 1", n, l)
			continue
		}
		inside := false
		for _, b := range block {
			inside = inside || strings.Contains(b[1], l)
		}
		if !inside {
			t.Errorf("%q is not inside an arm64-only branch", l)
		}
	}
}
