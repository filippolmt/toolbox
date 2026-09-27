# shellcheck shell=sh
# android-env.sh — sourced by zshrc.sh. Exports the Android variables once
# android-sdk-install has populated the android-sdk bind; the bind itself
# always exists, so the gate is the installed adb. ADR 0017.
_toolbox_sdk="${HOME}/.android-sdk"
if [ -x "${_toolbox_sdk}/platform-tools/adb" ]; then
    export ANDROID_HOME="${_toolbox_sdk}"
    # ~/.android for every Android tool, kept in the bind: android-cli (which
    # sdkmanager runs; its wrapper adds --no-metrics), and AGP's
    # debug.keystore, so debug builds keep one signature across recreates.
    export ANDROID_USER_HOME="${_toolbox_sdk}/user-home"
    export ANDROID_CLI_BIN="${_toolbox_sdk}/android-cli-no-metrics"
    # The Host Device Server: the developer's own adb server, reached on the
    # host's loopback through host.docker.internal's IPv4 address (the name
    # resolves IPv6 first, and that address is unreachable). No fallback: no
    # other address reaches a server bound to the host's loopback.
    if [ -z "${ADB_SERVER_SOCKET:-}" ]; then
        _toolbox_gw=$(getent ahostsv4 host.docker.internal 2>/dev/null | awk '{print $1; exit}')
        if [ -n "${_toolbox_gw}" ]; then
            export ADB_SERVER_SOCKET="tcp:${_toolbox_gw}:5037"
        fi
        unset _toolbox_gw
    fi
fi
unset _toolbox_sdk
