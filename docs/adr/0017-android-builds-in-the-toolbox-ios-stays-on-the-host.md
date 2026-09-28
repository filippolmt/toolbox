# Android builds in the toolbox and runs on the host's emulator; iOS stays on the host

Status: accepted

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
  the arm64 image. `cmdline-tools` (`sdkmanager`) was believed to run because it
  is Java; the spike found that it no longer does (see Spike results).
- **No emulator can run in the container.** There is no KVM, Google publishes
  no linux-aarch64 emulator, and Redroid needs binder, which is not set in
  Docker Desktop's kernel (`CONFIG_ANDROID_BINDER_IPC is not set`).
- **Docker Desktop runs x86_64 binaries in an arm64 container through Rosetta**,
  once "Use Rosetta for x86_64/amd64 emulation on Apple Silicon" is on. The
  setting is off by default, and `/proc/sys/fs/binfmt_misc/` was empty in the
  session that raised the issue. The spike showed that directory is empty
  inside the container even with the setting on, so it proves nothing either
  way. The binary still needs the x86_64 loader at
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
also carries the two zero-byte directory symlinks of the
[Foreign-Arch Runtime](../../CONTEXT.md#foreign-arch-runtime):
`/usr/lib/x86_64-linux-gnu`, which points into the SDK's data directory, and
`/lib64`, which points at `/usr/lib/x86_64-linux-gnu`. Both dangle until the
installer runs. The installer downloads the x86_64 loader and libraries without
root, using `apt-get download` and `dpkg-deb -x`. It does this first, because
`sdkmanager` itself is an x86_64 binary (see Spike results). The amd64 image
carries no symlinks, because on amd64 `/usr/lib/x86_64-linux-gnu` holds the
real libraries and `/lib64` is the distribution's own link to `usr/lib64`.

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
and without it prints them and exits. `sdkmanager` writes the licence file
without asking, so the installer does not call `sdkmanager` at all until the
flag is passed. An agent must ask the developer before
passing it, because that flag is a legal agreement and not a build step. When
x86_64 binaries cannot run, the installer fails fast and names the Docker
Desktop setting to turn on, instead of letting `aapt2` fail later with an
opaque error. It detects this by executing the x86_64 loader it just
extracted, not by reading `binfmt_misc`, which the container does not see.

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
they are absent on amd64, because CI smoke-tests only amd64. On amd64 those
paths hold the real libraries and the distribution's `/lib64` link, so a
symlink in their place would break every binary in the image.

**Rosetta is a hard prerequisite on Apple Silicon.** Toolbox cannot turn it on,
so the installer's fail-fast check is the only thing standing between a
developer and an `aapt2` error that names nothing. On an amd64 host none of
this applies: Google's binaries run natively and the Host Device Server works
the same way.

**Two binds join `mountplan.Defaults()`.** Both follow the existing rules for
rows there: disabling, `mounts_root` and deletion by hand.

## Verification

The ADR was `proposed` until a spike confirmed, end to end, from an arm64
toolbox on Docker Desktop:

1. With a plain `adb start-server` on the host, `adb devices` in the container
   through `ADB_SERVER_SOCKET` lists the host's emulator.
2. `aapt2` runs through Rosetta with the loader reached through the dangling
   symlink into `~/`. If AGP spawns it in a way that bypasses the loader, the
   spike measures whether a wrapper set with `android.aapt2FromMavenOverride` is
   needed.
3. `./gradlew assembleDebug` and `npx cap run android --target <emulator>`
   succeed on a Capacitor project.

A failure in 1 would have reopened the transport choice, and a failure in 2
the Foreign-Arch Runtime. All three passed in the spike of
[#1074](https://github.com/filippolmt/toolbox/issues/1074), and the ADR is
accepted. The guide, `docs/mobile.md`, comes next.

## Spike results

Run on 27 September 2026 from an arm64 toolbox on Docker Desktop with Rosetta
on. It departed from the plan in three ways:

- It used a physical phone on USB, not an emulator. The container talks only to
  the host's server, and that server treats both kinds of device alike, but the
  emulator path itself was not run.
- The Capacitor project was the plain `npx cap add android` template, not an
  Ionic Vue one. The Android project Gradle builds comes from Capacitor either
  way.
- The image symlinks were simulated. They were created as root with
  `docker exec` in the running container, with no Dockerfile build. Everything
  else ran as the host UID without root.

1. **Transport: passed.** With a plain `adb start-server` on the Mac,
   `ADB_SERVER_SOCKET=tcp:192.168.65.254:5037 adb devices` in the container
   listed the device. The host server was not restarted. The client and server
   agreed on adb protocol 41, and that number is what the "server version
   doesn't match" check compares, not the platform-tools release.
2. **Foreign-Arch Runtime: passed, with two symlinks.** Four amd64 packages
   cover every x86_64 binary the spike ran (`adb`, `aapt2` from Maven and from
   build-tools, `sdkmanager` and its `android` CLI): `libc6`, `libgcc-s1`,
   `libstdc++6` and
   `zlib1g`, which take 26 MiB unpacked. `adb` alone needs `libgcc_s`.
   `apt-get download` runs without root when its state is redirected
   (`-o Dir::State::Lists=… -o Dir::Cache=… -o Dir::State::status=/dev/null
   -o APT::Architectures=amd64 -o APT::Architecture=amd64`). The loader path is
   not enough, because the loader then looks for libraries on its default path,
   so the image needs two symlinks:
   `/usr/lib/x86_64-linux-gnu` → the runtime directory, and
   `/lib64` → `/usr/lib/x86_64-linux-gnu`. `/lib` is already `usr/lib` in the
   image, so the first link covers both multiarch paths. A missing loader
   fails with `rosetta error: failed to open elf at /lib64/ld-linux-x86-64.so.2`.
3. **aapt2 under AGP: passed, no wrapper.** `./gradlew assembleDebug` ran the
   Maven `aapt2` (`linux` classifier, x86_64) straight through the symlinks.
   `android.aapt2FromMavenOverride` is not needed. AGP installed its default
   build-tools by itself, once the licence was on disk. A cold build, with the
   Gradle distribution and every dependency still to download, took 1 min 10 s.
   `npx cap run android --target <serial>` built, installed and launched the
   app on the device, and `native-run` picked up `ADB_SERVER_SOCKET` from the
   environment. `adb logcat` from the container read the device log.

What the spike changed in the design:

- **`sdkmanager` is no longer Java.** The `cmdline-tools` the spike installed
  ships `sdkmanager` as a shell shim over a native x86_64 `android` binary, with no
  Java fallback. That binary downloads a second one, `android-cli`, into
  `~/.android/bin` and execs it by path, so the loader must sit at its
  absolute path. Calling it through the loader explicitly does not work.
  The installer therefore builds the Foreign-Arch Runtime first, with
  `apt-get download`, which needs nothing from the SDK, and only then runs
  `sdkmanager`. `~/.android/bin` lies outside both binds, so after a container
  recreate `android-cli` is downloaded again on the next `sdkmanager` run.
  Whether it joins the `android-sdk` bind is left to the implementation.
- **`sdkmanager` accepts licences on its own.** `--licenses` now prints that
  it is "no longer needed", and a plain install writes
  `licenses/android-sdk-license` without a prompt. The installer's
  `--accept-licenses` flag is therefore the only gate left, as the decision
  above now says.
- **The Gradle JDK is a native arm64 JDK.** Only Google's host binaries are
  x86_64. The JDK from Adoptium runs natively, and `org.gradle.java.home` in the
  user `gradle.properties` was enough for the build to pick it up.

Unpacked sizes after one build: the SDK (`cmdline-tools`, `platform-tools`, one
platform, one build-tools) 489 MiB, the Gradle JDK 346 MiB, the Foreign-Arch
Runtime 26 MiB, `android-cli` 89 MiB, `~/.gradle` 1.1 GiB. That totals about
2.2 GB, inside the 2–2.5 GB estimate above.
