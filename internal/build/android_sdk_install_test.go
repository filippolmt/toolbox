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
	printf '#!/bin/sh\nexit 0\n' > "$3/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2"
	chmod +x "$3/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2"
else
	mkdir -p "$3/usr/lib/x86_64-linux-gnu"
	: > "$3/usr/lib/x86_64-linux-gnu/$name.so"
fi`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatalf("write stub %s: %v", name, err)
		}
	}
	return bin
}

// runAndroidInstall runs the embedded installer (the artefact the image
// ships) with HOME set to home, the stubs first on PATH, and the given arch.
// It returns the combined output, the exit code and the recorded calls.
func runAndroidInstall(t *testing.T, home, arch string, args ...string) (string, int, string) {
	t.Helper()
	body, err := fs.ReadFile(Assets, AssetDir+"/bin/android-sdk-install.sh")
	if err != nil {
		t.Fatalf("read embedded android-sdk-install.sh: %v", err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "android-sdk-install")
	if err := os.WriteFile(script, body, 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
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
	return string(out), code, string(recorded)
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
	writeFile(t, filepath.Join(dir, "sdkmanager"), `#!/bin/sh
echo "sdkmanager $* ANDROID_USER_HOME=$ANDROID_USER_HOME ANDROID_CLI_BIN=$ANDROID_CLI_BIN" >> `+log+"\n")
	writeFile(t, filepath.Join(dir, "android"), `#!/bin/sh
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

	out, code, _ := runAndroidInstall(t, home, "amd64")

	if code == 0 {
		t.Errorf("exit code = 0 without --accept-licenses, want non-zero\n%s", out)
	}
	if !strings.Contains(out, "FAKE SDK LICENCE TEXT") {
		t.Errorf("licence text not printed:\n%s", out)
	}
	if !strings.Contains(out, "--accept-licenses") {
		t.Errorf("output does not name the flag to pass:\n%s", out)
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
	dir := filepath.Join(home, ".android-sdk", "x86_64-runtime")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\nexit " + strconv.Itoa(loaderExit) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ld-linux-x86-64.so.2"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
}

// binfmt_misc is empty inside the container even with Rosetta on, so the only
// honest check is executing the x86_64 loader. When it cannot run, the
// installer stops before sdkmanager, itself an x86_64 binary, fails opaquely.
func TestAndroidSdkInstallOnArm64FailsFastNamingRosettaWhenTheLoaderCannotRun(t *testing.T) {
	home := t.TempDir()
	sdkLog := fakeSdkmanager(t, home)
	fakeRuntime(t, home, 1)

	out, code, _ := runAndroidInstall(t, home, "arm64", "--accept-licenses")

	if code == 0 {
		t.Errorf("exit code = 0 with a loader that cannot run, want non-zero\n%s", out)
	}
	if !strings.Contains(out, "Use Rosetta for x86_64/amd64 emulation") {
		t.Errorf("output does not name the Docker Desktop setting:\n%s", out)
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

	out, _, calls := runAndroidInstall(t, home, "arm64")

	if !strings.Contains(calls, "download libc6:amd64 libgcc-s1:amd64 libstdc++6:amd64 zlib1g:amd64") {
		t.Errorf("the four amd64 packages were not downloaded; calls:\n%s", calls)
	}
	runtime := filepath.Join(home, ".android-sdk", "x86_64-runtime")
	for _, f := range []string{"ld-linux-x86-64.so.2", "libgcc-s1.so", "libstdc++6.so", "zlib1g.so"} {
		if _, err := os.Stat(filepath.Join(runtime, f)); err != nil {
			t.Errorf("runtime is missing %s: %v\n%s", f, err, out)
		}
	}
}

// On amd64 Google's binaries run natively on the image's own loader, so the
// installer builds no runtime at all.
func TestAndroidSdkInstallOnAmd64BuildsNoRuntime(t *testing.T) {
	home := t.TempDir()

	_, _, calls := runAndroidInstall(t, home, "amd64")

	if strings.Contains(calls, "apt-get") {
		t.Errorf("apt-get ran on amd64; calls:\n%s", calls)
	}
	if _, err := os.Stat(filepath.Join(home, ".android-sdk", "x86_64-runtime")); err == nil {
		t.Errorf("a runtime directory was created on amd64")
	}
}

// installerPin reads a NAME=value pin out of the embedded installer, so a
// case can plant an install that already matches it without restating it.
func installerPin(t *testing.T, name string) string {
	t.Helper()
	body, err := fs.ReadFile(Assets, AssetDir+"/bin/android-sdk-install.sh")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^` + name + `=(\S+)$`).FindSubmatch(body)
	if m == nil {
		t.Fatalf("installer pins no %s", name)
	}
	return string(m[1])
}

func writeFile(t *testing.T, path, body string) {
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
	writeFile(t, filepath.Join(sdk, "cmdline-tools", "latest", ".toolbox-version"), installerPin(t, "CMDLINE_TOOLS_BUILD")+"\n")
	writeFile(t, filepath.Join(sdk, "jdk", "bin", "java"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(sdk, "jdk", ".toolbox-version"), installerPin(t, "TEMURIN_VERSION")+"\n")
	props := filepath.Join(home, ".gradle", "gradle.properties")
	writeFile(t, props, "org.gradle.jvmargs=-Xmx2g\norg.gradle.java.home=/stale\n")

	out, code, calls := runAndroidInstall(t, home, "amd64", "--accept-licenses")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if strings.Contains(calls, "curl") {
		t.Errorf("a re-run with everything pinned in place downloaded something; calls:\n%s", calls)
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
	body, err := fs.ReadFile(Assets, AssetDir+"/bin/android-sdk-install.sh")
	if err != nil {
		t.Fatal(err)
	}
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
				m := regexp.MustCompile(ms).FindSubmatch(body)
				if m == nil || string(m[regexp.MustCompile(ms).SubexpIndex("currentValue")]) != pin {
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
