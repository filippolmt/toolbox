# shellcheck shell=sh
# android-env.sh — sourced by zshrc.sh. Exports the Android variables once
# android-sdk-install has populated the android-sdk bind; the bind itself
# always exists, so the gate is the installed adb. ADR 0017.
_toolbox_sdk="${HOME}/.android-sdk"
if [ -x "${_toolbox_sdk}/platform-tools/adb" ]; then
    export ANDROID_HOME="${_toolbox_sdk}"
    # android-cli's home, kept in the bind; its wrapper adds --no-metrics.
    export ANDROID_USER_HOME="${_toolbox_sdk}/user-home"
    export ANDROID_CLI_BIN="${_toolbox_sdk}/android-cli-no-metrics"
    # The Host Device Server: the developer's own adb server, over the IPv4
    # gateway (host.docker.internal resolves IPv6 first, and that address is
    # unreachable). Same lookup as proximo-hosts' gateway_ip.
    if [ -z "${ADB_SERVER_SOCKET:-}" ]; then
        _toolbox_gw=$(getent ahostsv4 host.docker.internal 2>/dev/null | awk '{print $1; exit}')
        if [ -z "${_toolbox_gw}" ] && command -v ip >/dev/null 2>&1; then
            _toolbox_gw=$(ip -4 route show default 2>/dev/null | awk '{print $3; exit}')
        fi
        if [ -n "${_toolbox_gw}" ]; then
            export ADB_SERVER_SOCKET="tcp:${_toolbox_gw}:5037"
        fi
        unset _toolbox_gw
    fi
fi
unset _toolbox_sdk
