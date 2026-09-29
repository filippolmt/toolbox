package build

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// androidStubs writes stand-ins for every external command the installer
// runs, so a case observes what the installer decided without a network, a
// Debian archive or Rosetta. Each stub appends its argv to $CALLS. `dpkg`
// answers the architecture from $FAKE_ARCH; `curl` serves a manifest whose
// only licence is a marker line, and fails on anything else, so a case that
// reaches a real download shows up as a failure instead of a fetch.
func androidStubs(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	stubs := map[string]string{
		"dpkg": `echo "dpkg $*" >> "$CALLS"
[ "$1" = "--print-architecture" ] && echo "$FAKE_ARCH"`,
		"curl": `echo "curl $*" >> "$CALLS"
case "$*" in
*repository2-3.xml*) printf '%s\n' '<sdk>' '<license id="android-sdk-license" type="text">FAKE SDK LICENCE TEXT' '</license>' '</sdk>' ;;
*) exit 22 ;;
esac`,
		// `apt-get download` drops one .deb per requested pkg:amd64 in the
		// working directory, the way the real one does.
		"apt-get": `echo "apt-get $*" >> "$CALLS"
[ "${FAKE_APT_FAIL:-0}" = 1 ] && exit 1
if [ "${FAKE_APT_SLOW:-0}" = 1 ]; then
	if mkdir "$HOME/.apt-active" 2>/dev/null; then
		sleep 0.2
		rmdir "$HOME/.apt-active"
	else
		: > "$HOME/.apt-overlap"
		sleep 0.2
	fi
fi
case " $* " in *" download "*)
	for a in "$@"; do case "$a" in *:amd64) : > "${a%:amd64}_amd64.deb" ;; esac; done ;;
esac`,
		// `dpkg-deb -x` lays each package out the way Debian bookworm does: libc6
		// (the loader, which exits 0 as if Rosetta ran it) under lib/, the rest
		// under usr/lib/. The installer has to merge both into one directory.
		"dpkg-deb": `echo "dpkg-deb $*" >> "$CALLS"
name=$(basename "$2" _amd64.deb)
if [ "$name" = libc6 ]; then
	mkdir -p "$3/lib/x86_64-linux-gnu"
	printf '#!/bin/sh\nexit %s\n' "${FAKE_LOADER_EXIT:-0}" > "$3/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2"
	chmod +x "$3/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2"
else
	mkdir -p "$3/usr/lib/x86_64-linux-gnu"
	case "$name" in
	libgcc-s1) library=libgcc_s.so.1 ;;
	libstdc++6) library=libstdc++.so.6 ;;
	zlib1g) library=libz.so.1 ;;
	esac
	[ "${FAKE_MISSING_LIBRARY:-}" = "$library" ] || : > "$3/usr/lib/x86_64-linux-gnu/$library"
fi`,
	}
	for name, body := range stubs {
		writeExecutable(t, filepath.Join(bin, name), "#!/bin/sh\n"+body+"\n")
	}
	return bin
}

// installRun is what one installer run leaves behind: its combined output,
// its exit code and the external commands it called.
type installRun struct {
	out   string
	code  int
	calls string
}

// runAndroidInstall runs the embedded installer (the artefact the image
// ships) with HOME set to home, the stubs first on PATH, and the given arch.
func runAndroidInstall(t *testing.T, home, arch string, args ...string) installRun {
	t.Helper()
	dir := t.TempDir()
	script := writeInstallerScript(t)
	calls := filepath.Join(dir, "calls")
	cmd := exec.Command("sh", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"PATH="+androidStubs(t)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_ARCH="+arch,
		"CALLS="+calls,
	)
	out, err := cmd.CombinedOutput()
	code := 0
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run installer: %v", err)
	}
	recorded, _ := os.ReadFile(calls)
	return installRun{out: string(out), code: code, calls: string(recorded)}
}

// fakeSdkmanager plants a recording cmdline-tools where the installer would
// find an already-installed one, so "the installer ran sdkmanager" becomes
// observable. sdkmanager logs its argv and the two env vars that decide where
// android-cli lives and whether it reports metrics; the `android` launcher
// logs its argv and, like the real one, downloads android-cli into
// $ANDROID_USER_HOME/bin. That android-cli logs its own argv.
func fakeSdkmanager(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, ".android-sdk", "cmdline-tools", "latest", "bin")
	log := filepath.Join(home, "sdkmanager-calls")
	writeExecutable(t, filepath.Join(dir, "sdkmanager"), `#!/bin/sh
echo "sdkmanager $* ANDROID_USER_HOME=$ANDROID_USER_HOME ANDROID_CLI_BIN=$ANDROID_CLI_BIN" >> `+log+"\n")
	writeExecutable(t, filepath.Join(dir, "android"), `#!/bin/sh
echo "android $*" >> `+log+`
mkdir -p "$ANDROID_USER_HOME/bin"
printf '#!/bin/sh\necho "android-cli $*" >> `+log+`\n' > "$ANDROID_USER_HOME/bin/android-cli"
chmod +x "$ANDROID_USER_HOME/bin/android-cli"
`)
	return log
}

// sdkmanager writes the licence file on its own, with no prompt, so the
// --accept-licenses flag is the only gate (ADR 0017, Spike results). Without
// it the installer must print the licence and stop before sdkmanager runs.
func TestAndroidSdkInstallWithoutAcceptPrintsLicenceAndNeverRunsSdkmanager(t *testing.T) {
	home := t.TempDir()
	sdkLog := fakeSdkmanager(t, home)

	run := runAndroidInstall(t, home, "amd64")

	if run.code == 0 {
		t.Errorf("exit code = 0 without --accept-licenses, want non-zero\n%s", run.out)
	}
	if !strings.Contains(run.out, "FAKE SDK LICENCE TEXT") {
		t.Errorf("licence text not printed:\n%s", run.out)
	}
	if !strings.Contains(run.out, "--accept-licenses") {
		t.Errorf("output does not name the flag to pass:\n%s", run.out)
	}
	if _, err := os.Stat(sdkLog); err == nil {
		got, _ := os.ReadFile(sdkLog)
		t.Errorf("sdkmanager ran before the licence was accepted:\n%s", got)
	}
}

// fakeRuntime plants an already-extracted Foreign-Arch Runtime whose loader
// exits with the given status: 0 stands for "Rosetta runs x86_64 binaries",
// anything else for "the setting is off".
func fakeRuntime(t *testing.T, home string, loaderExit int) {
	t.Helper()
	runtimes := filepath.Join(home, ".android-sdk", "x86_64-runtimes")
	generation := filepath.Join(runtimes, "generation.old")
	loader := filepath.Join(generation, "ld-linux-x86-64.so.2")
	writeExecutable(t, loader, "#!/bin/sh\nexit "+strconv.Itoa(loaderExit)+"\n")
	if err := os.Symlink(filepath.Base(generation), filepath.Join(runtimes, "current")); err != nil {
		t.Fatal(err)
	}
}

// binfmt_misc is empty inside the container even with Rosetta on, so the only
// honest check is executing the x86_64 loader. When it cannot run, the
// installer stops before sdkmanager, itself an x86_64 binary, fails opaquely.
func TestAndroidSdkInstallOnArm64FailsFastNamingRosettaWhenTheLoaderCannotRun(t *testing.T) {
	home := t.TempDir()
	sdkLog := fakeSdkmanager(t, home)
	t.Setenv("FAKE_LOADER_EXIT", "1")

	run := runAndroidInstall(t, home, "arm64", "--accept-licenses")

	if run.code == 0 {
		t.Errorf("exit code = 0 with a loader that cannot run, want non-zero\n%s", run.out)
	}
	if !strings.Contains(run.out, "Use Rosetta for x86_64/amd64 emulation") {
		t.Errorf("output does not name the Docker Desktop setting:\n%s", run.out)
	}
	if _, err := os.Stat(sdkLog); err == nil {
		t.Errorf("sdkmanager ran although x86_64 binaries cannot run")
	}
}

// On arm64 with an empty bind, the installer builds the Foreign-Arch Runtime
// from Debian's amd64 packages, without root, and merges bookworm's lib/ and
// usr/lib/ halves into the one directory the image's symlink points at.
func TestAndroidSdkInstallOnArm64BuildsTheForeignArchRuntime(t *testing.T) {
	home := t.TempDir()

	run := runAndroidInstall(t, home, "arm64", "--accept-licenses")

	if !strings.Contains(run.calls, "download libc6:amd64 libgcc-s1:amd64 libstdc++6:amd64 zlib1g:amd64") {
		t.Errorf("the four amd64 packages were not downloaded; calls:\n%s", run.calls)
	}
	runtime := filepath.Join(home, ".android-sdk", "x86_64-runtimes", "current")
	if info, err := os.Lstat(runtime); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("runtime current = %v, %v; want symlink", info, err)
	}
	for _, f := range []string{"ld-linux-x86-64.so.2", "libgcc_s.so.1", "libstdc++.so.6", "libz.so.1"} {
		if _, err := os.Stat(filepath.Join(runtime, f)); err != nil {
			t.Errorf("runtime is missing %s: %v\n%s", f, err, run.out)
		}
	}
}

// Re-running the installer refreshes the Foreign-Arch Runtime from Debian's
// current indexes instead of treating the loader's presence as a permanent
// cache hit. That is how security updates reach an existing Android SDK bind.
func TestAndroidSdkInstallOnArm64RefreshesAnExistingForeignArchRuntime(t *testing.T) {
	home := t.TempDir()
	fakeRuntime(t, home, 0)
	stale := filepath.Join(home, ".android-sdk", "x86_64-runtimes", "generation.stale")
	if err := os.Mkdir(stale, 0o755); err != nil {
		t.Fatal(err)
	}

	run := runAndroidInstall(t, home, "arm64", "--accept-licenses")

	if !strings.Contains(run.calls, "apt-get -o") || !strings.Contains(run.calls, " update") {
		t.Errorf("the existing runtime was not refreshed; calls:\n%s", run.calls)
	}
	current := filepath.Join(home, ".android-sdk", "x86_64-runtimes", "current")
	if target, err := os.Readlink(current); err != nil || target == "generation.old" {
		t.Errorf("current target = %q, %v; want a refreshed generation", target, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale runtime generation survived refresh: %v", err)
	}
}

// Download, extraction and Rosetta validation all happen before publication.
// A failure at any stage leaves current on the last complete generation.
func TestAndroidSdkInstallOnArm64KeepsTheExistingRuntimeAfterARefreshFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   string
		value string
	}{
		{"download fails", "FAKE_APT_FAIL", "1"},
		{"candidate cannot run", "FAKE_LOADER_EXIT", "1"},
		{"candidate library is missing", "FAKE_MISSING_LIBRARY", "libz.so.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			fakeRuntime(t, home, 0)
			t.Setenv(tc.env, tc.value)

			run := runAndroidInstall(t, home, "arm64", "--accept-licenses")

			if run.code == 0 {
				t.Fatalf("exit code = 0 after refresh failure, want non-zero\n%s", run.out)
			}
			assertRuntimeWorks(t, home)
		})
	}
}

func assertRuntimeWorks(t *testing.T, home string) {
	t.Helper()
	current := filepath.Join(home, ".android-sdk", "x86_64-runtimes", "current")
	if target, err := os.Readlink(current); err != nil || target != "generation.old" {
		t.Errorf("current target = %q, %v; want unchanged generation.old", target, err)
	}
	loader := filepath.Join(current, "ld-linux-x86-64.so.2")
	if out, err := exec.Command(loader, "--version").CombinedOutput(); err != nil {
		t.Errorf("the working runtime was replaced: %v\n%s", err, out)
	}
}

// On amd64 Google's binaries run natively on the image's own loader, so the
// installer builds no runtime at all.
func TestAndroidSdkInstallOnAmd64BuildsNoRuntime(t *testing.T) {
	home := t.TempDir()

	run := runAndroidInstall(t, home, "amd64", "--accept-licenses")

	if strings.Contains(run.calls, "apt-get") {
		t.Errorf("apt-get ran on amd64; calls:\n%s", run.calls)
	}
	if _, err := os.Stat(filepath.Join(home, ".android-sdk", "x86_64-runtimes")); err == nil {
		t.Errorf("a runtime directory was created on amd64")
	}
}

// The Android Installer Mutation Lock begins before cmdline-tools publication
// and stays held through the final Gradle configuration write. Every shared
// mutation is slow in turn so a second accepted installer has time to collide.
func TestAndroidSdkInstallSerializesConcurrentMutationsAfterTheRuntime(t *testing.T) {
	for _, slowPhase := range []string{"cmdline-tools", "android-cli", "platform-tools", "jdk", "gradle-properties"} {
		t.Run(slowPhase, func(t *testing.T) {
			home := t.TempDir()
			sdk := filepath.Join(home, ".android-sdk")
			latest := filepath.Join(sdk, "cmdline-tools", "latest")
			dir := t.TempDir()
			script := writeInstallerScript(t)
			bin := androidStubs(t)
			active := filepath.Join(home, ".installer-mutation-active")
			release := filepath.Join(home, ".installer-mutation-release")
			overlap := filepath.Join(home, ".installer-mutation-overlap")

			writeExecutable(t, filepath.Join(bin, "phase"), `#!/bin/sh
if [ "${INSTALLER_RUN:-}" = first ] && [ "$1" = "$SLOW_PHASE" ]; then
	: > "$HOME/.installer-mutation-active"
	while [ ! -e "$HOME/.installer-mutation-release" ]; do sleep 0.01; done
	rm -f "$HOME/.installer-mutation-active"
elif [ -e "$HOME/.installer-mutation-active" ]; then
	: > "$HOME/.installer-mutation-overlap"
fi
`)
			writeExecutable(t, filepath.Join(bin, "curl"), `#!/bin/sh
echo "curl $*" >> "$CALLS"
case "$*" in
*commandlinetools-linux*)
	phase cmdline-tools
	while [ "$#" -gt 0 ]; do
		if [ "$1" = -o ]; then shift; : > "$1"; exit 0; fi
		shift
	done
	;;
*api.adoptium.net*) printf archive ;;
*) exit 22 ;;
esac
`)
			writeExecutable(t, filepath.Join(bin, "unzip"), `#!/bin/sh
while [ "$#" -gt 0 ]; do
	if [ "$1" = -d ]; then shift; dest=$1; break; fi
	shift
done
mkdir -p "$dest/cmdline-tools/bin"
printf '%s\n' '#!/bin/sh' 'phase android-cli' 'mkdir -p "$ANDROID_USER_HOME/bin"' \
	'printf '\''#!/bin/sh\nphase android-cli\n'\'' > "$ANDROID_USER_HOME/bin/android-cli"' \
	'chmod +x "$ANDROID_USER_HOME/bin/android-cli"' > "$dest/cmdline-tools/bin/android"
printf '%s\n' '#!/bin/sh' 'phase platform-tools' \
	'mkdir -p "$HOME/.android-sdk/platform-tools"' \
	': > "$HOME/.android-sdk/platform-tools/adb"' \
	'chmod +x "$HOME/.android-sdk/platform-tools/adb"' > "$dest/cmdline-tools/bin/sdkmanager"
chmod +x "$dest/cmdline-tools/bin/android" "$dest/cmdline-tools/bin/sdkmanager"
`)
			writeExecutable(t, filepath.Join(bin, "tar"), `#!/bin/sh
phase jdk
while [ "$#" -gt 0 ]; do
	if [ "$1" = -C ]; then shift; dest=$1; break; fi
	shift
done
mkdir -p "$dest/bin"
printf '#!/bin/sh\nexit 0\n' > "$dest/bin/java"
chmod +x "$dest/bin/java"
`)
			writeExecutable(t, filepath.Join(bin, "mv"), `#!/bin/sh
case "$1" in *gradle.properties.tmp) phase gradle-properties ;; esac
exec /bin/mv "$@"
`)

			first := androidInstallerCommand(script, home, bin, "amd64", filepath.Join(dir, "calls-first"),
				"INSTALLER_RUN=first", "SLOW_PHASE="+slowPhase)
			if err := first.Start(); err != nil {
				t.Fatal(err)
			}
			for range 100 {
				if _, err := os.Stat(active); err == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if _, err := os.Stat(active); err != nil {
				t.Fatalf("first installer never reached %s: %v", slowPhase, err)
			}

			second := androidInstallerCommand(script, home, bin, "amd64", filepath.Join(dir, "calls-second"),
				"INSTALLER_RUN=second", "SLOW_PHASE="+slowPhase)
			if err := second.Start(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(100 * time.Millisecond)
			if err := os.WriteFile(release, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := first.Wait(); err != nil {
				t.Errorf("first installer: %v", err)
			}
			if err := second.Wait(); err != nil {
				t.Errorf("second installer: %v", err)
			}
			if _, err := os.Stat(overlap); err == nil {
				t.Error("a second installer entered the mutation tail before the first finished")
			}
			for _, path := range []string{
				filepath.Join(sdk, "cmdline-tools.zip"),
				filepath.Join(sdk, "cmdline-tools.tmp"),
				filepath.Join(sdk, "jdk.tmp"),
				filepath.Join(home, ".gradle", "gradle.properties.tmp"),
			} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("staging path survived successful installers: %s", path)
				}
			}
			if !pinnedInstall(t, latest, installerPin(t, "CMDLINE_TOOLS_BUILD")) {
				t.Error("cmdline-tools install is incomplete")
			}
			if !pinnedInstall(t, filepath.Join(sdk, "jdk"), installerPin(t, "TEMURIN_VERSION")) {
				t.Error("JDK install is incomplete")
			}
			for _, path := range []string{
				filepath.Join(latest, "bin", "sdkmanager"),
				filepath.Join(sdk, "user-home", "bin", "android-cli"),
				filepath.Join(sdk, "platform-tools", "adb"),
				filepath.Join(sdk, "jdk", "bin", "java"),
			} {
				if info, err := os.Stat(path); err != nil || info.Mode()&0o111 == 0 {
					t.Errorf("installed tool is not executable: %s", path)
				}
			}
			props, err := os.ReadFile(filepath.Join(home, ".gradle", "gradle.properties"))
			if err != nil {
				t.Fatal(err)
			}
			wantProps := "org.gradle.java.home=" + filepath.Join(sdk, "jdk") + "\n"
			if string(props) != wantProps {
				t.Errorf("gradle.properties = %q, want %q", props, wantProps)
			}
		})
	}
}

func pinnedInstall(t *testing.T, dir, want string) bool {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, ".toolbox-version"))
	return err == nil && strings.TrimSpace(string(got)) == want
}

// The Android bind is shared by every shell on the same profile. Two runtime
// refreshes serialize their staging and publication under the narrower runtime
// lock, before the Android Installer Mutation Lock is acquired.
func TestAndroidSdkInstallSerializesConcurrentRuntimeRefreshes(t *testing.T) {
	home := t.TempDir()
	sdk := filepath.Join(home, ".android-sdk")
	fakeRuntime(t, home, 0)
	fakeSdkmanager(t, home)
	writeExecutable(t, filepath.Join(sdk, "cmdline-tools", "latest", ".toolbox-version"), installerPin(t, "CMDLINE_TOOLS_BUILD")+"\n")
	writeExecutable(t, filepath.Join(sdk, "jdk", ".toolbox-version"), installerPin(t, "TEMURIN_VERSION")+"\n")

	dir := t.TempDir()
	script := writeInstallerScript(t)
	bin := androidStubs(t)
	first := androidInstallerCommand(script, home, bin, "arm64", filepath.Join(dir, "calls-first"), "FAKE_APT_SLOW=1")
	second := androidInstallerCommand(script, home, bin, "arm64", filepath.Join(dir, "calls-second"), "FAKE_APT_SLOW=1")
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	if err := second.Start(); err != nil {
		t.Fatal(err)
	}
	if err := first.Wait(); err != nil {
		t.Errorf("first installer: %v", err)
	}
	if err := second.Wait(); err != nil {
		t.Errorf("second installer: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".apt-overlap")); err == nil {
		t.Error("concurrent installers reached apt at the same time")
	}
}

func androidInstallerCommand(script, home, bin, arch, calls string, env ...string) *exec.Cmd {
	cmd := exec.Command("sh", script, "--accept-licenses")
	cmd.Env = append(os.Environ(), append([]string{
		"HOME=" + home,
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_ARCH=" + arch,
		"CALLS=" + calls,
	}, env...)...)
	return cmd
}

func writeInstallerScript(t *testing.T) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "android-sdk-install")
	writeExecutable(t, script, string(installerScript(t)))
	return script
}

// installerScript is the embedded installer, the artefact the image ships.
func installerScript(t *testing.T) []byte {
	t.Helper()
	body, err := fs.ReadFile(Assets, AssetDir+"/bin/android-sdk-install.sh")
	if err != nil {
		t.Fatalf("read embedded android-sdk-install.sh: %v", err)
	}
	return body
}

// installerPin reads a NAME=value pin out of the embedded installer, so a
// case can plant an install that already matches it without restating it.
func installerPin(t *testing.T, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^` + name + `=(\S+)$`).FindSubmatch(installerScript(t))
	if m == nil {
		t.Fatalf("installer pins no %s", name)
	}
	return string(m[1])
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// With the licence accepted and the pinned JDK and cmdline-tools already in
// the bind, a re-run downloads nothing and updates platform-tools. android-cli
// lives under ANDROID_USER_HOME inside the bind, so it survives a recreate,
// and every run of it passes --no-metrics: it ignores DO_NOT_TRACK and spools
// usage events otherwise (ADR 0017, Spike results). The JDK pointer is
// rewritten in ~/.gradle/gradle.properties, the gradle-cache bind, which is
// deletable on its own.
func TestAndroidSdkInstallAcceptedInstallsPlatformToolsWithoutMetrics(t *testing.T) {
	home := t.TempDir()
	sdk := filepath.Join(home, ".android-sdk")
	userHome := filepath.Join(sdk, "user-home")
	sdkLog := fakeSdkmanager(t, home)
	writeExecutable(t, filepath.Join(sdk, "cmdline-tools", "latest", ".toolbox-version"), installerPin(t, "CMDLINE_TOOLS_BUILD")+"\n")
	writeExecutable(t, filepath.Join(sdk, "jdk", "bin", "java"), "#!/bin/sh\n")
	writeExecutable(t, filepath.Join(sdk, "jdk", ".toolbox-version"), installerPin(t, "TEMURIN_VERSION")+"\n")
	props := filepath.Join(home, ".gradle", "gradle.properties")
	writeExecutable(t, props, "org.gradle.jvmargs=-Xmx2g\norg.gradle.java.home=/stale\n")

	run := runAndroidInstall(t, home, "amd64", "--accept-licenses")

	if run.code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", run.code, run.out)
	}
	if strings.Contains(run.calls, "curl") {
		t.Errorf("a re-run with everything pinned in place downloaded something; calls:\n%s", run.calls)
	}
	logged, _ := os.ReadFile(sdkLog)
	var cliBin string
	for _, line := range strings.Split(string(logged), "\n") {
		if strings.HasPrefix(line, "android ") && !strings.Contains(line, "--no-metrics") {
			t.Errorf("android launcher ran without --no-metrics: %q", line)
		}
		if strings.HasPrefix(line, "sdkmanager ") {
			if !strings.Contains(line, "--sdk_root="+sdk+" platform-tools") {
				t.Errorf("sdkmanager did not install platform-tools into the bind: %q", line)
			}
			if !strings.Contains(line, "ANDROID_USER_HOME="+userHome+" ") {
				t.Errorf("sdkmanager ran with android-cli outside the bind: %q", line)
			}
			if _, v, ok := strings.Cut(line, "ANDROID_CLI_BIN="); ok {
				cliBin = v
			}
		}
	}
	if cliBin == "" {
		t.Fatalf("sdkmanager never ran with ANDROID_CLI_BIN set; calls:\n%s", logged)
	}
	// What sdkmanager does with ANDROID_CLI_BIN: run it with its own argv.
	if b, err := exec.Command(cliBin, "--sdk=x", "sdk", "list").CombinedOutput(); err != nil {
		t.Fatalf("run ANDROID_CLI_BIN %s: %v\n%s", cliBin, err, b)
	}
	logged, _ = os.ReadFile(sdkLog)
	if !strings.Contains(string(logged), "android-cli --no-metrics --sdk=x sdk list") {
		t.Errorf("ANDROID_CLI_BIN does not add --no-metrics; calls:\n%s", logged)
	}
	gotProps, _ := os.ReadFile(props)
	want := "org.gradle.jvmargs=-Xmx2g\norg.gradle.java.home=" + filepath.Join(sdk, "jdk") + "\n"
	if string(gotProps) != want {
		t.Errorf("gradle.properties =\n%s\nwant\n%s", gotProps, want)
	}
}

// Renovate bumps the installer's two pins through regex managers whose
// matchStrings must keep matching the script, and whose version rules must
// keep reading the upstream shapes: a build number off one manifest line for
// cmdline-tools, and Temurin's release tag, a respin's extra build field
// included. Any drift silently stops the bump, so the managers are re-run
// here against the live script and a known-good upstream sample.
func TestRenovateBumpsBothAndroidInstallerPins(t *testing.T) {
	raw, err := os.ReadFile("../../renovate.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		CustomManagers []struct {
			ManagerFilePatterns    []string `json:"managerFilePatterns"`
			MatchStrings           []string `json:"matchStrings"`
			DepNameTemplate        string   `json:"depNameTemplate"`
			ExtractVersionTemplate string   `json:"extractVersionTemplate"`
			VersioningTemplate     string   `json:"versioningTemplate"`
		} `json:"customManagers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse renovate.json: %v", err)
	}
	body := installerScript(t)
	pins := map[string]string{
		"android-cmdline-tools": installerPin(t, "CMDLINE_TOOLS_BUILD"),
		"temurin21":             installerPin(t, "TEMURIN_VERSION"),
	}
	for dep, pin := range pins {
		matched := false
		for _, cm := range doc.CustomManagers {
			if cm.DepNameTemplate != dep || !slices.Contains(cm.ManagerFilePatterns, `/^internal/build/assets/bin/android-sdk-install\.sh$/`) {
				continue
			}
			for _, ms := range cm.MatchStrings {
				re := regexp.MustCompile(ms)
				m := re.FindSubmatch(body)
				if m == nil || string(m[re.SubexpIndex("currentValue")]) != pin {
					t.Errorf("%s: matchString %q does not capture the pin %q", dep, ms, pin)
				}
				matched = true
			}
		}
		if !matched {
			t.Errorf("no Renovate manager for %s on the installer", dep)
		}
	}
	rule := func(dep string) (extract, versioning string) {
		for _, cm := range doc.CustomManagers {
			if cm.DepNameTemplate == dep {
				return cm.ExtractVersionTemplate, cm.VersioningTemplate
			}
		}
		return "", ""
	}
	// One line of repository2-3.xml, verbatim, as the plain datasource reads it.
	extract, _ := rule("android-cmdline-tools")
	re, err := regexp.Compile(strings.ReplaceAll(extract, "(?<", "(?P<"))
	if err != nil || extract == "" {
		t.Fatalf("android-cmdline-tools extractVersion %q: %v", extract, err)
	}
	if m := re.FindStringSubmatch("<url>commandlinetools-linux-16111833_latest.zip</url>"); m == nil || m[re.SubexpIndex("version")] != "16111833" {
		t.Errorf("extractVersion %q does not read the build number off a manifest line", extract)
	}
	if re.MatchString("<url>commandlinetools-mac-16111833_latest.zip</url>") {
		t.Errorf("extractVersion %q also reads the macOS archive", extract)
	}
	_, versioning := rule("temurin21")
	vre, err := regexp.Compile(strings.ReplaceAll(strings.TrimPrefix(versioning, "regex:"), "(?<", "(?P<"))
	if err != nil || !strings.HasPrefix(versioning, "regex:") {
		t.Fatalf("temurin21 versioning %q: %v", versioning, err)
	}
	for _, tag := range []string{pins["temurin21"], "jdk-21.0.12+8", "jdk-21.0.12.1+1"} {
		if !vre.MatchString(tag) {
			t.Errorf("temurin21 versioning %q does not parse the release tag %q", versioning, tag)
		}
	}
	if vre.MatchString("jdk-21.0.13+3-ea-beta") {
		t.Errorf("temurin21 versioning %q accepts an early-access tag", versioning)
	}
}

// Nothing is downloaded before the developer accepts the licence, on arm64
// included: the Foreign-Arch Runtime waits for the flag too. The manifest the
// licence text comes from is the only fetch.
func TestAndroidSdkInstallWithoutAcceptDownloadsNothingButTheLicence(t *testing.T) {
	home := t.TempDir()

	run := runAndroidInstall(t, home, "arm64")

	for _, line := range strings.Split(strings.TrimSpace(run.calls), "\n") {
		if strings.HasPrefix(line, "apt-get") || (strings.HasPrefix(line, "curl") && !strings.Contains(line, "repository2-3.xml")) {
			t.Errorf("downloaded before the licence was accepted: %q", line)
		}
	}
}

// android-cli has its own update channel, and ANDROID_CLI_BIN bypasses the
// launcher that would otherwise check it: a re-run updates the android-cli
// already in the bind, still without metrics, instead of fetching a new one.
func TestAndroidSdkInstallUpdatesAnInstalledAndroidCliWithoutMetrics(t *testing.T) {
	home := t.TempDir()
	sdk := filepath.Join(home, ".android-sdk")
	sdkLog := fakeSdkmanager(t, home)
	writeExecutable(t, filepath.Join(sdk, "cmdline-tools", "latest", ".toolbox-version"), installerPin(t, "CMDLINE_TOOLS_BUILD")+"\n")
	writeExecutable(t, filepath.Join(sdk, "jdk", ".toolbox-version"), installerPin(t, "TEMURIN_VERSION")+"\n")
	writeExecutable(t, filepath.Join(sdk, "user-home", "bin", "android-cli"), "#!/bin/sh\necho \"android-cli $*\" >> "+sdkLog+"\n")

	run := runAndroidInstall(t, home, "amd64", "--accept-licenses")

	if run.code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", run.code, run.out)
	}
	logged, _ := os.ReadFile(sdkLog)
	if !strings.Contains(string(logged), "android-cli --no-metrics update") {
		t.Errorf("the installed android-cli was not updated without metrics; calls:\n%s", logged)
	}
	if strings.Contains(string(logged), "\nandroid ") || strings.HasPrefix(string(logged), "android ") {
		t.Errorf("the launcher fetched android-cli again although one is installed; calls:\n%s", logged)
	}
}
