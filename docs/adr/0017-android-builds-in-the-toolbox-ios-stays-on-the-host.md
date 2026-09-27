# Android builds in the toolbox and runs on the host's emulator; iOS stays on the host

Status: proposed

Every figure below is **as measured when this decision was taken** — evidence for the choice, not a description of the repo today. Nothing here is kept in sync; current values live in the files that set them.

The question comes from [#1073](https://github.com/filippolmt/toolbox/issues/1073).
A session was building a Capacitor + Ionic Vue prototype and could not start it
on an Android emulator or an iOS simulator, because the image has no mobile
tooling. Node is there, so `npx cap add` and `cap sync` already work. What fails
is the native build and run, so the developer switches to the macOS host and
copies the results back.

The target is the author's own setup: Docker Desktop on Apple Silicon, running
the arm64 image with no `/dev/kvm`. The toolchain stays framework-agnostic.
Capacitor is the test case, and anything Gradle needs, every Android framework
needs too.

Facts that shaped the decision, checked on 27 September 2026:

- **Google ships no linux-arm64 Android host binaries.** In the SDK manifest
  (`repository2-3.xml`), every `aarch64` archive is `macosx`. `platform-tools`
  (`adb`), `build-tools` and the emulator are linux x86_64 only. `aapt2` on
  Google Maven has only `linux`, `osx` and `windows` classifiers, and an arm64
  one has never existed. `./gradlew assembleDebug` therefore fails natively in
  the arm64 image. Only `cmdline-tools` (`sdkmanager`) runs, because it is Java.
- **No emulator can run in the container.** There is no KVM, Google publishes
  no linux-aarch64 emulator, and Redroid needs binder, which is not set in
  Docker Desktop's kernel (`CONFIG_ANDROID_BINDER_IPC is not set`).
- **Docker Desktop runs x86_64 binaries in an arm64 container through Rosetta**,
  once "Use Rosetta for x86_64/amd64 emulation on Apple Silicon" is on. The
  setting is off by default, and `/proc/sys/fs/binfmt_misc/` was empty in the
  session that raised the issue. The binary still needs the x86_64 loader at
  the absolute path its ELF names.
- **The container reaches a service bound to the host's loopback.**
  [ADR 0014](0014-the-session-network-is-the-developers-to-choose.md) verified
  this on Docker Desktop, and the bridge's TCP fallback depends on it.
  `host.docker.internal` resolves IPv6 first, and that address was unreachable,
  so the IPv4 gateway address is the one to use.
- **The image's system JDK is older than the one the Capacitor Android template
  requires.**
- **The toolchain is large.** Measured as zips: cmdline-tools 172 MiB,
  platform-tools 8 MiB, one build-tools 61 MiB, one platform 62 MiB. The
  estimate unpacked, with the Gradle distribution and its caches, is about
  2–2.5 GB.
- **The workspace appears at the same absolute path in the container as on the
  host** (`mountplan.WorkspaceMirrorPath`). A host-side step can therefore run
  on the files the agent just wrote.

The decision:

**The Android build runs in the toolbox, on Google's own x86_64 binaries under
Rosetta.** The image carries only a small, idempotent installer. On arm64 it
also carries the zero-byte symlinks of the
[Foreign-Arch Runtime](../../CONTEXT.md#foreign-arch-runtime), which point into
the SDK's data directory and dangle until the installer runs. The installer
downloads the x86_64 loader and libraries without root, using `apt-get download`
and `dpkg-deb -x`. The amd64 image carries no symlinks, because on amd64 those
paths hold the real loader.

**The SDK lives in two binds under `~/.toolbox`, never in the image.** The two
rows follow the [Tool Cache](../../CONTEXT.md#tool-cache) split and
[ADR 0015](0015-tool-caches-persist-as-one-bind-per-tool.md):

- an `android-sdk` data directory, holding the SDK, the JDK Gradle needs, the
  Foreign-Arch Runtime and the accepted licences;
- a `gradle-cache` Tool Cache on `~/.gradle`.

Both are `CreateIfMissing` and always mounted, like `playwright-cache`. An empty
bind costs nothing. The Gradle JDK is set with `org.gradle.java.home` in the
user `gradle.properties`, so the system `java` does not change for anything else.

**No config key turns it on.** The shell exports `ANDROID_HOME` and
`ADB_SERVER_SOCKET` only when the SDK directory is populated. The installer
accepts Google's SDK licences only with an explicit `--accept-licenses` flag,
and without it prints them and exits. An agent must ask the developer before
passing it, because that flag is a legal agreement and not a build step. When
binfmt has no x86_64 handler, the installer fails fast and names the Docker
Desktop setting to turn on, instead of letting `aapt2` fail later with an
opaque error.

**The emulator runs on the host, reached through the
[Host Device Server](../../CONTEXT.md#host-device-server).** The developer runs
a plain `adb start-server` on the Mac. The container's `adb` client, set with
`ADB_SERVER_SOCKET=tcp:<gateway IPv4>:5037`, uses that server over loopback.
`npx cap run android` and `native-run` work unchanged, because they spawn
`$ANDROID_HOME/platform-tools/adb` and inherit the variable.

**iOS stays on the host, and toolbox triggers nothing there.** `xcodebuild` and
the simulator are macOS only, and nothing short of a macOS VM changes that.

## Considered options

**Put the SDK in the base image.** Rejected: it adds gigabytes for every user,
against [ADR 0016](0016-image-size-two-denominators.md). The per-tool opt-out
`ARG` that could have made it optional was removed on purpose (#276), so there
is one canonical image.

**Install `libc6:amd64` and friends in the image through multiarch.** This is
the conventional way to run x86_64 binaries on arm64. Rejected, because it puts
tens of MB in the image for every user so that only a mobile developer can use
them. The loader has to sit at an absolute path, and the container runs as the
host UID without root, so the path must exist in the image. A zero-byte symlink
meets that need, and the payload does not have to be in the image.

**Document overlay lines for `~/.toolbox/Dockerfile`.** Toolbox would ship
nothing. Rejected, because every mobile user would then build and maintain a
private `:local` image, and Renovate cannot see lines that live in a user's
file.

**Use community arm64 builds of `aapt2` and build-tools.** No Rosetta is needed.
Rejected, because they are third-party binaries run with full workspace access,
and they have no upstream that Renovate can pin with any trust.

**Run the whole image as `linux/amd64` under Rosetta.** Rejected, because every
other tool in the session would run emulated to speed up one toolchain.

**A bridge route `/adb` with closed verbs and an `adb` shim.** This would
inherit the [Bridge Contract](../../CONTEXT.md#bridge-contract)'s token and
rate limit. Rejected: `native-run` calls subcommands that no allowlist could
predict, and the route would guard the host, which adb does not expose (see
Consequences), rather than the device, which it does.

**A host helper for iOS with closed verbs** (`build`, `install`, `launch`,
`log`), over SSH with `command=` or as a bridge route. Rejected for this
iteration, because the allowlist contains nothing. The `build` verb runs
`xcodebuild` on a project the agent wrote, and that project can carry Run
Script build phases, CocoaPods hooks, npm scripts and SwiftPM plugins. All of
them execute on the Mac as the developer, unsandboxed. A host iOS build
therefore gives the agent arbitrary code execution on the host by construction,
whatever the verb list says. If the need is confirmed, the helper comes back as
its own issue: opt-in per workspace, with that grant stated where the
developer turns it on, not hidden behind an allowlist. The alternative that
contains the build, a macOS VM (for example Tart), is out of toolbox's scope.

## Consequences

**The developer's devices are exposed to the container, and this is accepted.**
The Host Device Server has no token and no allowlist. Anything in the container
can install on, shell into and pull app data from every device attached to the
host's adb server, a physical phone on USB included. It opens no new path into
the host itself: `adb forward` and `adb reverse` reach only loopback services
the container reaches already, and `adb push` and `pull` read and write through
the client, not the server. The guide has to say this plainly next to the
`adb start-server` step.

**The Foreign-Arch Runtime's symlinks must never reach the amd64 image.** The
Dockerfile adds them only when `TARGETARCH=arm64`. A test has to assert that
they are absent on amd64, because CI smoke-tests only amd64, and a symlink over
the real loader would break every binary in the image.

**Rosetta is a hard prerequisite on Apple Silicon.** Toolbox cannot turn it on,
so the installer's fail-fast check is the only thing standing between a
developer and an `aapt2` error that names nothing. On an amd64 host none of
this applies: Google's binaries run natively and the Host Device Server works
the same way.

**Two binds join `mountplan.Defaults()`.** Both follow the existing rules for
rows there: disabling, `mounts_root` and deletion by hand.

## Verification

This ADR stays `proposed` until a spike confirms, end to end, from an arm64
toolbox on Docker Desktop:

1. With a plain `adb start-server` on the host, `adb devices` in the container
   through `ADB_SERVER_SOCKET` lists the host's emulator.
2. `aapt2` runs through Rosetta with the loader reached through the dangling
   symlink into `~/`. If AGP spawns it in a way that bypasses the loader, the
   spike measures whether a wrapper set with `android.aapt2FromMavenOverride` is
   needed.
3. `./gradlew assembleDebug` and `npx cap run android --target <emulator>`
   succeed on a Capacitor project.

A failure in 1 reopens the transport choice. A failure in 2 reopens the
Foreign-Arch Runtime. The guide, `docs/mobile.md`, is written once this ADR is
accepted.
