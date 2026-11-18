#!/bin/sh
# Tests of the scripts install.ps1 writes in its WSL distribution, taken
# from it as they are, against a fake docker that writes down each call and
# another root: eriphe-device-run's settings for the launcher, and
# eriphe-share, the Explorer's command. The real run is on Windows
# (install/README.md, Windows).
# shellcheck disable=SC2016,SC1090,SC1091 # the scripts' shell expands them; sourced from the test's files
set -eu
[ -z "${TRACE:-}" ] || set -x
here=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fails=0
fail() {
	printf 'FAIL: %s\n' "$*" >&2
	fails=$((fails + 1))
}
has() { grep -qF -- "$2" "$1" || fail "$3: no \"$2\" in $1"; }
hasnt() { ! grep -qF -- "$2" "$1" || fail "$3: \"$2\" in $1"; }

# The scripts, as install.ps1 writes them.
script() { awk '/^cat > \/usr\/local\/bin\/'"$1"' <<.EOF.$/ { f = 1; next } f && /^EOF$/ { exit } f' "$here/install.ps1"; }
script eriphe-device-run >"$T/eriphe-device-run"
script eriphe-share >"$T/eriphe-share"
for s in eriphe-device-run eriphe-share; do
	[ "$(wc -l <"$T/$s")" -gt 5 ] || { echo "wsl_test.sh: no $s in install.ps1" >&2; exit 1; }
done

# The fake commands: docker, and what the scripts ask of the system.
mkdir -p "$T/bin"
cat >"$T/bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$T/calls"
exit 0
EOF
printf '#!/bin/sh\n[ -n "${SESSION_MOUNTED:-}" ]\n' >"$T/bin/mountpoint"
chmod +x "$T/bin/"*
export T PATH="$T/bin:$PATH"
R=$T/root

# device_run CMD: a shell with eriphe-device-run's functions, on $R, with
# /etc/eriphe-device.env's settings in its environment.
device_run() {
	(
		set -a
		[ ! -f "$R/etc/eriphe-device.env" ] || . "$R/etc/eriphe-device.env"
		set +a
		set +eu # as Windows runs it
		# shellcheck disable=SC2034 # read by the script sourced
		ERIPHE_WSL_ROOT=$R ERIPHE_DEVICE_RUN_LIB=1
		. "$T/eriphe-device-run"
		eval "$1"
	) 2>&1
}
fresh() {
	rm -rf "$R"
	mkdir -p "$R/etc" "$R/var/log" "$R/usr/local/bin" "$R/var/lib"
	: >"$T/calls"
}

# ---- the launcher's settings, from install.ps1's

fresh
cat >"$R/etc/eriphe-device.env" <<'EOF'
ERIPHE_INSTALL_MODE=device
ERIPHE_INSTALL=windows
ERIPHE_DISTRO=eriphe
ERIPHE_CORE=http://192.0.2.10:8800
ERIPHE_DEVICE_NAME=pc
ERIPHE_DEVICE_LABELS=gpu
ERIPHE_CA_HASH=sha256:00
ERIPHE_JOIN_TOKEN=t0k
ERIPHE_VERSION=
ERIPHE_WAKE_MAC=00:00:5e:00:53:01
ERIPHE_WAKE_BROADCAST=192.0.2.255
ERIPHE_MODEL_SERVER=http://192.0.2.5:11434
ERIPHE_SESSION_DIR='C:\ProgramData\eriphe-device\session'
ERIPHE_WINDOWS_HOME='C:\Users\Sam'
EOF
(export SESSION_MOUNTED=1; device_run launcher_env) >"$T/env"
for want in ERIPHE_MODE=device ERIPHE_CORE=http://192.0.2.10:8800 ERIPHE_CA_HASH=sha256:00 ERIPHE_JOIN_TOKEN=t0k \
	ERIPHE_DEVICE_NAME=pc ERIPHE_DEVICE_LABELS=gpu ERIPHE_WAKE_MAC=00:00:5e:00:53:01 ERIPHE_WAKE_BROADCAST=192.0.2.255 \
	ERIPHE_MODEL_SERVER=http://192.0.2.5:11434 ERIPHE_SESSION_DIR=/mnt/eriphe-session "ERIPHE_SHARE_COMMAND=wsl -d eriphe eriphe-share"; do
	grep -qxF -- "$want" "$T/env" || fail "launcher settings: no $want in $(tr '\n' ' ' <"$T/env")"
done
hasnt "$T/env" ERIPHE_API_PORT "device mode: no port"
# Joined: no token; no session folder mounted: none; another distribution's command.
mkdir -p "$R/var/lib/eriphe/device"
echo '{}' >"$R/var/lib/eriphe/device/device.json"
sed -i.bak 's/^ERIPHE_DISTRO=.*/ERIPHE_DISTRO=eriphe-test/' "$R/etc/eriphe-device.env"
device_run launcher_env >"$T/env"
hasnt "$T/env" ERIPHE_JOIN_TOKEN "joined: the token"
hasnt "$T/env" ERIPHE_SESSION_DIR "no session folder"
has "$T/env" "ERIPHE_SHARE_COMMAND=wsl -d eriphe-test eriphe-share" "the share command of another distribution"
# Windows' loopback: the device, in its container, does not reach it.
for lo in http://127.0.0.1:11434 http://localhost:11434 'http://[::1]:11434'; do
	sed -i.bak "s|^ERIPHE_MODEL_SERVER=.*|ERIPHE_MODEL_SERVER=$lo|" "$R/etc/eriphe-device.env"
	device_run launcher_env >"$T/env"
	grep -qx ERIPHE_MODEL_SERVER= "$T/env" || fail "the loopback model server $lo is lent: $(grep MODEL "$T/env")"
done
has "$R/var/log/eriphe-device.log" "not lent" "the loopback model server, said"
# Core mode: its ports, no eriphe-core to join.
fresh
printf 'ERIPHE_INSTALL_MODE=core\nERIPHE_CORE=\nERIPHE_DEVICE_NAME=pc\nERIPHE_CA_HASH=sha256:00\n' >"$R/etc/eriphe-device.env"
printf 'API_PORT=9800\nWEB_PORT=9443\n' >"$R/etc/eriphe-core.ports"
device_run launcher_env >"$T/env"
for want in ERIPHE_MODE=core ERIPHE_API_PORT=9800 ERIPHE_WEB_PORT=9443; do
	grep -qxF -- "$want" "$T/env" || fail "core mode: no $want in $(tr '\n' ' ' <"$T/env")"
done
hasnt "$T/env" ERIPHE_CORE= "core mode: an eriphe-core to join"
hasnt "$T/env" ERIPHE_CA_HASH "core mode: a CA to trust"

# ---- eriphe-share: the launcher's, without --copyable=false

: >"$T/calls"
sh "$T/eriphe-share" add --read --copyable=false "/mnt/eriphe/home/Sam Docs"
has "$T/calls" "exec eriphe eriphe share add --read /mnt/eriphe/home/Sam Docs" "eriphe-share"
: >"$T/calls"
sh "$T/eriphe-share" add --read --copyable /mnt/eriphe/home/Projects
has "$T/calls" "exec eriphe eriphe share add --read --copyable /mnt/eriphe/home/Projects" "eriphe-share, copyable"

if [ "$fails" -gt 0 ]; then
	echo "wsl_test.sh: $fails failed" >&2
	exit 1
fi
echo "wsl_test.sh: ok"
