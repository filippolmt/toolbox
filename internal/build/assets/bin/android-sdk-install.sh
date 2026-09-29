#!/bin/sh
# android-sdk-install — fill the android-sdk bind (~/.android-sdk) with what an
# Android build needs: on arm64 the Foreign-Arch Runtime, then the Gradle JDK,
# cmdline-tools and platform-tools. Re-running refreshes moving dependencies and
# skips installs already at their pin. See ADR 0017.
set -eu

MANIFEST_URL=https://dl.google.com/android/repository/repository2-3.xml
# Renovate bumps both pins (renovate.json). cmdline-tools is a build number in
# Google's manifest; Temurin is the release tag, which is also the name the
# Adoptium binary API takes, on the JDK line the Capacitor Android template
# needs. Three downloads follow upstream instead, because none offers a pin
# the tool takes: sdkmanager installs only the latest platform-tools, the
# cmdline-tools launcher fetches the latest android-cli, and the runtime comes
# from the Debian archive, like the image's own apt layer.
CMDLINE_TOOLS_BUILD=16111833
TEMURIN_VERSION=jdk-21.0.12.1+1

accept=0
for arg in "$@"; do
	case "$arg" in
	--accept-licenses) accept=1 ;;
	*)
		echo "usage: android-sdk-install [--accept-licenses]" >&2
		exit 2
		;;
	esac
done

# sdkmanager writes the licence file on its own, with no prompt, so this flag
# is the only gate. Nothing below runs, or downloads, before it is passed.
if [ "$accept" -eq 0 ]; then
	if ! manifest=$(curl -fsSL "$MANIFEST_URL"); then
		echo "android-sdk-install: cannot fetch the SDK licence from $MANIFEST_URL" >&2
		exit 1
	fi

	printf '%s\n' "$manifest" |
		sed -n '/<license id="android-sdk-license"/,/<\/license>/p' |
		sed -e 's/^.*<license [^>]*>//' -e 's/<\/license>.*$//' \
			-e 's/&quot;/"/g' -e "s/&apos;/'/g" -e 's/&lt;/</g' -e 's/&gt;/>/g' -e 's/&amp;/\&/g'
	echo
	echo "Installing the Android SDK means accepting the licence above." >&2
	echo "Re-run with --accept-licenses to accept it and install." >&2
	exit 1
fi

SDK="$HOME/.android-sdk"
mkdir -p "$SDK"
# One bind is shared by every shell on the same profile. Serialize the whole
# install so their fixed staging paths and runtime publication cannot race.
exec 9>"$SDK/.install.lock"
flock 9

# The arm64 image's /usr/lib/x86_64-linux-gnu symlink points through current.
RUNTIMES="$SDK/x86_64-runtimes"
RUNTIME="$RUNTIMES/current"
arch=$(dpkg --print-architecture)

runtime_cannot_run() {
	cat >&2 <<'EOF'
android-sdk-install: this container cannot run x86_64 binaries, and Google
ships the Android host tools for x86_64 only. In Docker Desktop, turn on
Settings > General > "Use Rosetta for x86_64/amd64 emulation on Apple Silicon",
apply and restart, then run `toolbox stop` and open a new shell.
EOF
}

# build_runtime downloads Debian's amd64 loader and the libraries Google's
# binaries link, without root: apt's state is redirected into a scratch
# directory, and the image's arm64 dpkg database is never touched. Bookworm
# splits them between lib/ and usr/lib/; both land in $RUNTIME.
build_runtime() {
	work="$SDK/.runtime-build"
	rm -rf "$work"
	mkdir -p "$work/lists/partial" "$work/cache/archives/partial"
	set -- \
		-o Dir::State::Lists="$work/lists" -o Dir::Cache="$work/cache" \
		-o Dir::State::status=/dev/null -o Debug::NoLocking=1 \
		-o APT::Architectures=amd64 -o APT::Architecture=amd64
	apt-get "$@" -qq update
	(cd "$work" && apt-get "$@" -qq download libc6:amd64 libgcc-s1:amd64 libstdc++6:amd64 zlib1g:amd64)
	for deb in "$work"/*.deb; do
		dpkg-deb -x "$deb" "$work/root"
	done
	mkdir -p "$work/runtime"
	for d in "$work/root/lib/x86_64-linux-gnu" "$work/root/usr/lib/x86_64-linux-gnu"; do
		if [ -d "$d" ]; then cp -a "$d/." "$work/runtime/"; fi
	done
	# Validate the staged loader and the libraries the Google tools need before
	# publishing, so a failed refresh leaves the working runtime untouched.
	for required in ld-linux-x86-64.so.2 libgcc_s.so.1 libstdc++.so.6 libz.so.1; do
		if [ ! -e "$work/runtime/$required" ]; then
			echo "android-sdk-install: Foreign-Arch Runtime is missing $required" >&2
			return 1
		fi
	done
	if ! "$work/runtime/ld-linux-x86-64.so.2" --library-path "$work/runtime" --version >/dev/null 2>&1; then
		runtime_cannot_run
		return 1
	fi
	mkdir -p "$RUNTIMES"
	generation=$(mktemp -d "$RUNTIMES/generation.XXXXXX")
	rmdir "$generation"
	mv "$work/runtime" "$generation"
	old=$(readlink "$RUNTIME" 2>/dev/null || true)
	rm -f "$RUNTIMES/.current.new"
	ln -s "$(basename "$generation")" "$RUNTIMES/.current.new"
	mv -Tf "$RUNTIMES/.current.new" "$RUNTIME"
	case "$old" in
	generation.*) case "$old" in */*) ;; *) rm -rf "$RUNTIMES/$old" ;; esac ;;
	esac
	rm -rf "$work"
}

case "$arch" in
amd64) ;; # Google's binaries run natively; the image's loader is the real one.
arm64)
	# binfmt_misc is not visible in the container, so build_runtime validates
	# the staged x86_64 loader itself before publishing it.
	build_runtime
	;;
*)
	echo "android-sdk-install: unsupported architecture $arch" >&2
	exit 1
	;;
esac

# pinned DIR VERSION: true when DIR holds the install of VERSION.
pinned() {
	[ "$(cat "$1/.toolbox-version" 2>/dev/null)" = "$2" ]
}

latest="$SDK/cmdline-tools/latest"
if ! pinned "$latest" "$CMDLINE_TOOLS_BUILD"; then
	zip="$SDK/cmdline-tools.zip"
	staging="$SDK/cmdline-tools.tmp"
	rm -rf "$staging"
	mkdir -p "$staging" "$SDK/cmdline-tools"
	curl -fsSL -o "$zip" "https://dl.google.com/android/repository/commandlinetools-linux-${CMDLINE_TOOLS_BUILD}_latest.zip"
	unzip -q "$zip" -d "$staging"
	rm -f "$zip"
	echo "$CMDLINE_TOOLS_BUILD" >"$staging/cmdline-tools/.toolbox-version"
	rm -rf "$latest"
	mv "$staging/cmdline-tools" "$latest"
	rm -rf "$staging"
fi

# sdkmanager runs android-cli, which lives in ANDROID_USER_HOME (~/.android by
# default, outside the bind, and large to fetch again) and ignores DO_NOT_TRACK:
# keep it in the bind and run it with --no-metrics every time. zshrc exports
# the same two variables for the sdkmanager a developer runs by hand.
export ANDROID_USER_HOME="$SDK/user-home"
export ANDROID_CLI_BIN="$SDK/android-cli-no-metrics"
printf '#!/bin/sh\nexec "%s/bin/android-cli" --no-metrics "$@"\n' "$ANDROID_USER_HOME" >"$ANDROID_CLI_BIN"
chmod +x "$ANDROID_CLI_BIN"
if [ -x "$ANDROID_USER_HOME/bin/android-cli" ]; then
	# ANDROID_CLI_BIN bypasses the launcher that would check for a newer one.
	"$ANDROID_CLI_BIN" update
else
	# The launcher fetches android-cli, then runs it with these arguments.
	"$latest/bin/android" --no-metrics --version >/dev/null
fi

"$latest/bin/sdkmanager" --sdk_root="$SDK" platform-tools

# The Gradle JDK runs natively: only Google's host binaries are x86_64.
if ! pinned "$SDK/jdk" "$TEMURIN_VERSION"; then
	case "$arch" in amd64) jdk_arch=x64 ;; *) jdk_arch=aarch64 ;; esac
	tag=$(printf '%s' "$TEMURIN_VERSION" | sed 's/+/%2B/g')
	staging="$SDK/jdk.tmp"
	rm -rf "$staging"
	mkdir -p "$staging"
	curl -fsSL "https://api.adoptium.net/v3/binary/version/$tag/linux/$jdk_arch/jdk/hotspot/normal/eclipse" |
		tar -xz -C "$staging" --strip-components=1
	echo "$TEMURIN_VERSION" >"$staging/.toolbox-version"
	rm -rf "$SDK/jdk"
	mv "$staging" "$SDK/jdk"
fi

# ~/.gradle is the gradle-cache bind, deletable on its own: rewrite the JDK
# pointer on every run, keeping whatever else the file holds.
props="$HOME/.gradle/gradle.properties"
mkdir -p "$HOME/.gradle"
{
	grep -v '^org\.gradle\.java\.home=' "$props" 2>/dev/null || true
	echo "org.gradle.java.home=$SDK/jdk"
} >"$props.tmp"
mv "$props.tmp" "$props"

echo "Android SDK ready in $SDK. Open a new shell to get ANDROID_HOME and ADB_SERVER_SOCKET."
