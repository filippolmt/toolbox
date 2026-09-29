# Mobile development

The toolbox builds Android apps and runs them on a device or emulator attached
to your host. iOS builds stay on the host. The toolchain is framework-agnostic:
anything that builds through Gradle gets the same SDK. Capacitor is the case
the design was verified on. Why it is shaped
this way: [ADR 0017](adr/0017-android-builds-in-the-toolbox-ios-stays-on-the-host.md).

Nothing Android lives in the image. The SDK, the JDK Gradle needs and the
x86_64 runtime all live in a bind under `~/.toolbox`, and they are filled on
demand by one installer. A developer who never builds for Android pays nothing.

## Prerequisites

### Apple Silicon: turn on Rosetta in Docker Desktop

Google ships the Android host tools (`adb`, `aapt2`, `sdkmanager`) for x86_64
Linux only. On an Apple Silicon Mac the toolbox image is arm64, so those
binaries run under Rosetta. In Docker Desktop, open **Settings → General**, turn
on **Use Rosetta for x86_64/amd64 emulation on Apple Silicon**, then **Apply &
restart**. Run `toolbox stop` afterwards, so the next shell starts on the
restarted engine.

Do not check `/proc/sys/fs/binfmt_misc/` from inside the container. It is empty
there even with Rosetta on. The installer checks for itself, by running an
x86_64 binary, and names this setting when that fails.

On an amd64 host none of this applies: Google's binaries run natively.

### The host's adb server

The emulator cannot run in the container: there is no KVM, and Google publishes
no Linux arm64 emulator. The device side stays on the host, and the container's
`adb` talks to the host's own adb server.

On the host you need `adb` and nothing else:

- **A physical phone on USB.** Install only the platform-tools
  (`brew install --cask android-platform-tools`). On the phone, turn on
  **Developer options → USB debugging**.
- **An emulator.** Install Android Studio and create a device in its Device
  Manager, or use `android-commandlinetools` with an arm64 system image. Both
  bring `adb` with them. The emulator path is the one not yet verified end to
  end: [#1081](https://github.com/filippolmt/toolbox/issues/1081).

Then start the server and check that it sees the device:

```sh
adb start-server
adb devices        # the device must show as "device", not "unauthorized"
```

Run a plain `adb start-server`, **never `adb -a start-server`**. The container
reaches a server bound to the host's loopback, so `-a` gains nothing, and it
publishes your devices to the whole LAN. See [Security](#security) before you
run it.

Keep exactly one `adb` install on the host. Two installs of different versions
kill each other's server: `adb server version (…) doesn't match this client;
killing...`. Android Studio's copy and Homebrew's copy are the usual pair.

## Installing the SDK

From a toolbox shell:

```sh
android-sdk-install
```

Without a flag, the installer prints Google's Android SDK licence and exits,
having downloaded nothing. **Installing means accepting that licence**, which is
a legal agreement, not a build step. Once you have read it:

```sh
android-sdk-install --accept-licenses
```

An agent working in the session must ask you before passing
`--accept-licenses`. The underlying `sdkmanager` accepts the licence on its own
without asking, so this flag is the only point where the choice is yours.

The installer, in order:

1. On arm64, refreshes the Foreign-Arch Runtime from Debian's current package
   indexes: the x86_64 loader and the four libraries Google's binaries link,
   downloaded without root. It validates the complete candidate before an
   atomic switch, so a failed download or Rosetta check leaves the working
   runtime intact.
2. Installs the cmdline-tools and the platform-tools.
3. Installs the JDK Gradle needs and points `org.gradle.java.home` at it in
   `~/.gradle/gradle.properties`. The image's system `java` stays as it is.

Runs are serialized per Android SDK bind. Run it again after a toolbox update
or periodically to pick up new pins and Debian security updates, or after
deleting either bind.

**Open a new shell afterwards.** The shell exports `ANDROID_HOME` and
`ADB_SERVER_SOCKET` only once the SDK is installed, and it decides that when
the shell starts. An agent keeps the environment it started with, so an agent
session that was already running before the install needs a restart as well.
Without `ADB_SERVER_SOCKET`, `adb` starts a server of its own inside the
container, and that server sees no devices.

The SDK's own tools report usage statistics by default, and they ignore
`DO_NOT_TRACK`. The installer and the shell route every call through a wrapper
that passes `--no-metrics`, so nothing is collected. If you run
`$ANDROID_HOME/cmdline-tools/latest/bin/android` directly, pass `--no-metrics`
yourself.

## Building and running

With the SDK installed and the host's server running, the usual commands work
unchanged:

```sh
adb devices                                   # lists the host's devices
npx cap sync android
cd android && ./gradlew assembleDebug
npx cap run android --target <serial>         # serial from `adb devices`
adb logcat
```

`npx cap run android` and `native-run` spawn `$ANDROID_HOME/platform-tools/adb`,
which picks up `ADB_SERVER_SOCKET` from the environment. Nothing needs
configuring per project.

The first build downloads Gradle and every dependency. They persist in the
`gradle-cache` bind, so later builds and later containers start warm. Gradle
installs the SDK platform and build-tools a project asks for by itself, the
first time the project needs them.

The debug signing key (`debug.keystore`) is kept in the `android-sdk` bind. A
debug build keeps the same signature across container recreates, so reinstalling
over the previous build works without an uninstall.

**Pointing adb elsewhere.** `ADB_SERVER_SOCKET` defaults to
`tcp:<host.docker.internal IPv4>:5037`. The IPv4 address is deliberate:
`host.docker.internal` resolves IPv6 first, and that address does not reach the
host. A value you set yourself, in `env:` or in the shell, takes precedence.

## Security

**Everything in the container controls every device attached to the host's adb
server**, a physical phone on USB included. It can install and uninstall apps,
open a shell on the device, and pull app data. The adb server has no token and
no allowlist.

It opens no new path into the host itself. `adb forward` and `adb reverse`
reach only loopback services the container can reach already, and `adb push`
and `pull` read and write through the client in the container, not the server.

If that is more than you want an agent to have, stop the host server when you
are not building (`adb kill-server`), or unplug the phone.

## iOS

`xcodebuild` and the iOS simulator run only on macOS, so iOS builds stay on the
host, and toolbox triggers nothing there. A host-side iOS build would run code
from the project, such as build phases and package hooks, on your Mac, outside
the container.

The workspace sits at the same absolute path in the container as on the host,
so the host picks up what the container just wrote. From a host terminal, in
the same directory:

```sh
npx cap sync ios
npx cap run ios        # or: npx cap open ios, to build from Xcode
```

## Disk

| Host path | Container path | Holds |
|---|---|---|
| `~/.toolbox/android-sdk` | `~/.android-sdk` | SDK, Gradle JDK, x86_64 runtime, `debug.keystore`, android-cli |
| `~/.toolbox/gradle-cache` | `~/.gradle` | Gradle distributions and dependency cache, `gradle.properties` |

Together they reach a few gigabytes after one build. The measured breakdown is
in the ADR's [Spike results](adr/0017-android-builds-in-the-toolbox-ios-stays-on-the-host.md#spike-results).

To reclaim the space, stop the toolbox and delete either directory on the host.
Both are recreated empty on the next shell, and `android-sdk-install` fills the
SDK again. To stop mounting one at all, disable it by name like any other
default mount (`android-sdk` or `gradle-cache`); see [mounts.md](mounts.md).

**Profiles copy both.** Under `--profile` every default bind moves into the
profile's own root, so each profile downloads its own SDK. Keep them shared
with `--share android,gradle`, which matches `android-sdk` and `gradle-cache`
by prefix. [#1082](https://github.com/filippolmt/toolbox/issues/1082) tracks
sharing them by default.

## Troubleshooting

**`android-sdk-install: this container cannot run x86_64 binaries`** — Rosetta
is off. See [the setting](#apple-silicon-turn-on-rosetta-in-docker-desktop),
then `toolbox stop` and a new shell.

**`rosetta error: failed to open elf at /lib64/ld-linux-x86-64.so.2`** — an
x86_64 binary ran before the runtime was in place. Run `android-sdk-install
--accept-licenses`. If you deleted `~/.toolbox/android-sdk` on the host, the
same command rebuilds it.

**`adb devices` in the container lists nothing, or hangs** — the host server
is not running, or it runs without the device. Run `adb devices` on the host
first. `unauthorized` there means the phone is still waiting for you to accept
the debugging prompt.

**`ADB_SERVER_SOCKET` is empty** — the shell started before the SDK was
installed. Open a new shell.

**`adb server version (…) doesn't match this client`** — two `adb` installs
on the host. See [the host's adb server](#the-hosts-adb-server).
