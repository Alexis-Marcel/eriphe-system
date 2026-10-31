#!/bin/sh
# Tests of eriphe-launcher's logic, against a fake docker that answers as the
# host would and writes down each call: the host paths found from its own
# container, the settings, the compose project it writes, the order of an
# update (the machine's, then itself), its new container, the uninstall.
# Needs jq. The real run is in install/README.md (One container).
# shellcheck disable=SC2016 # the launcher's shell expands them
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

ID=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
mkdir -p "$T/bin" "$T/data"
printf '1 2 0:3 /var/lib/docker/containers/%s/resolv.conf /etc/resolv.conf rw - ext4 /dev/sda1 rw\n' "$ID" >"$T/mountinfo"
# The fake docker: SELF_REF, the image of this container, in $T/ref.
cat >"$T/bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$T/calls"
case $* in
"inspect -f {{.Id}} "*) echo 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef ;;
"inspect -f {{.Name}} "*) echo /eriphe ;;
"inspect -f {{.Image}} "*) echo sha256:old ;;
"inspect -f {{.Config.Image}} "*) cat "$T/ref" ;;
*'Destination "'*'/data"'*) echo /mnt/user/appdata/eriphe ;;
*'Destination "/var/run/docker.sock"'*) echo /var/run/docker.sock ;;
"info -f {{.Name}}") echo Tower.local ;;
"image inspect -f {{.Id}} "*) echo sha256:new ;;
"run --rm --network none --entrypoint true --mount type=bind,src=/mnt/missing,"*) exit 1 ;;
"run --rm --network host --entrypoint sh "*) printf '00:00:5e:00:53:01 192.0.2.255' ;;
esac
exit 0
EOF
chmod +x "$T/bin/docker"
export T PATH="$T/bin:$PATH"
echo ghcr.io/alexis-marcel/eriphe:latest >"$T/ref"

# launcher [ENV=V...]: a shell with eriphe-launcher's functions, on $T/data.
launcher() {
	env ERIPHE_LAUNCHER_SOURCED=1 ERIPHE_LAUNCHER_LIB="$here/install.sh" ERIPHE_LAUNCHER_DATA="$T/data" \
		ERIPHE_MOUNTINFO="$T/mountinfo" "$@"
}
run_in() { launcher sh -c ". '$here/eriphe-launcher'; $1" 2>&1 || true; }

# ---- references and versions

out=$(run_in 'ref_repo ghcr.io/alexis-marcel/eriphe:0.8.0; echo; ref_repo localhost:5000/eriphe; echo; ref_tag localhost:5000/eriphe; echo; ref_tag ghcr.io/alexis-marcel/eriphe:edge@sha256:ab; echo
version_tag 0.8.0; echo; version_tag 0.8.0-dev+abc1234; echo; version_tag dev || echo none')
[ "$out" = "ghcr.io/alexis-marcel/eriphe
localhost:5000/eriphe
latest
edge
0.8.0
sha-abc1234
none" ] || fail "references: $out"

# ---- host paths, from its own container

out=$(run_in 'discover; echo "$SELF $SELF_NAME $SELF_REF $HOST_DATA $HOST_SOCK"')
[ "$out" = "$ID eriphe ghcr.io/alexis-marcel/eriphe:latest /mnt/user/appdata/eriphe /var/run/docker.sock" ] || fail "discover: $out"
has "$T/calls" "inspect -f {{.Id}} $ID" "discover reads its id in mountinfo"

# ---- settings, from the environment

# core mode, by default; the name is the Docker host's; the version this image's tag.
out=$(run_in 'discover; settings; INFO=; settle; echo "$MODE $NAME $CORE_URL pin=$PIN shares=$SHARES $WEB_PORT $API_PORT"')
[ "$out" = "core tower http://eriphe-core:8800 pin= shares= 8443 8800" ] || fail "defaults: $out"
echo ghcr.io/alexis-marcel/eriphe:edge >"$T/ref"
out=$(launcher ERIPHE_SHARES=shares=/mnt/user,media=/mnt/media:ro ERIPHE_WEB_PORT=8843 sh -c ". '$here/eriphe-launcher'; discover; settings; INFO=; settle; echo \"pin=\$PIN shares=\$SHARES \$WEB_PORT\"" 2>&1)
[ "$out" = "pin=edge shares=shares=/mnt/user,media=/mnt/media:ro 8843" ] || fail "settings: $out"
out=$(launcher ERIPHE_VERSION=0.7.0 sh -c ". '$here/eriphe-launcher'; discover; settings; echo \"pin=\$PIN\"" 2>&1)
[ "$out" = pin=0.7.0 ] || fail "ERIPHE_VERSION: $out"
echo ghcr.io/alexis-marcel/eriphe:latest >"$T/ref"
for bad in "ERIPHE_MODE=node" "ERIPHE_MODE=node ERIPHE_CORE=http://192.0.2.10:8800" "ERIPHE_MODE=other" \
	"ERIPHE_CORE=http://192.0.2.10:8800" "ERIPHE_WEB_PORT=70000" "ERIPHE_WEB_PORT=8800" \
	"ERIPHE_SHARES=data=/mnt/user/appdata/eriphe/core" "ERIPHE_SHARES=bad"; do
	# shellcheck disable=SC2086 # one argument per variable
	if launcher $bad sh -c ". '$here/eriphe-launcher'; discover; settings" >/dev/null 2>&1; then
		fail "settings take $bad"
	fi
done
out=$(launcher ERIPHE_MODE=node ERIPHE_CORE=http://192.0.2.10:8800 ERIPHE_JOIN_TOKEN=t ERIPHE_CA_HASH=sha256:00 \
	sh -c ". '$here/eriphe-launcher'; discover; settings; INFO=; settle; echo \"\$MODE \$CORE_URL \$JOIN_TOKEN \$CA_HASH\"" 2>&1)
[ "$out" = "node http://192.0.2.10:8800 t sha256:00" ] || fail "node mode: $out"
# Several addresses of eriphe-core, preferred first (the connect commands
# give the local one, then the one away from home): kept as a list.
out=$(launcher ERIPHE_MODE=node ERIPHE_CORE=http://192.0.2.10:8800,https://eriphe.tail0000.ts.net:8800 ERIPHE_JOIN_TOKEN=t \
	sh -c ". '$here/eriphe-launcher'; discover; settings; INFO=; settle; echo \"\$CORE_URL\"" 2>&1)
[ "$out" = "http://192.0.2.10:8800,https://eriphe.tail0000.ts.net:8800" ] || fail "two addresses: $out"
mkdir -p "$T/data/launcher"
echo mode=core >"$T/data/launcher/install.conf"
out=$(launcher ERIPHE_MODE=node ERIPHE_CORE=http://192.0.2.10:8800 ERIPHE_JOIN_TOKEN=t sh -c ". '$here/eriphe-launcher'; discover; settings" 2>&1) &&
	fail "a core install turns into a node"
case $out in *"holds a core-mode install"*) ;; *) fail "mode change: $out" ;; esac
rm "$T/data/launcher/install.conf"
# A share is checked on the host, by a short-lived container.
if launcher ERIPHE_SHARES=gone=/mnt/missing sh -c ". '$here/eriphe-launcher'; discover; settings; check_places" >/dev/null 2>&1; then
	fail "a missing share passes"
fi

# ---- the compose project: the host's paths

launcher ERIPHE_SHARES=shares=/mnt/user ERIPHE_API_PORT=18800 TZ=Europe/Paris sh -c ". '$here/eriphe-launcher'; discover; settings; INFO=; settle
	compose_yaml 100 1" >"$T/compose.yaml" 2>&1
for want in "- /mnt/user/appdata/eriphe/core:/data" "- /mnt/user/appdata/eriphe/node:/mnt/user/appdata/eriphe/node" \
	'command: ["-root", "/mnt/user/appdata/eriphe/node",' "-allowbindmountfrom=/mnt/user/appdata/eriphe/node,/mnt/user" \
	"- /mnt/user:/mnt/user" 'ports: ["0.0.0.0:18800:8800", "0.0.0.0:8443:8443"]' "ERIPHE_INSTALL: docker" 'ERIPHE_API_PORT: "18800"' \
	"TZ: 'Europe/Paris'" "image: ghcr.io/alexis-marcel/eriphe-core:latest" '"-always-on"' "user: \"65534:100\""; do
	has "$T/compose.yaml" "$want" compose
done
hasnt "$T/compose.yaml" "/data/node" "compose (the launcher's own path)"
has "$T/compose.yaml" 'ERIPHE_WAKE_MAC: ${ERIPHE_WAKE_MAC:-}' "compose (the card to wake)"
# The machine's network card, for Wake-on-LAN: read on the host's network,
# by a short-lived container of this image, not this container's own.
launcher ERIPHE_MODE=node ERIPHE_CORE=http://192.0.2.10:8800 ERIPHE_JOIN_TOKEN=t sh -c ". '$here/eriphe-launcher'; discover; settings; INFO=; settle
	env_file" >"$T/env" 2>&1
has "$T/env" "ERIPHE_WAKE_MAC='00:00:5e:00:53:01'" "the card's MAC"
has "$T/env" "ERIPHE_WAKE_BROADCAST='192.0.2.255'" "the card's network"
has "$T/calls" "run --rm --network host --entrypoint sh -e ERIPHE_INSTALL_LIB=1 -e CORE_URL=http://192.0.2.10:8800 sha256:old" "the card, from the host's network"
# The node and eriphe-core never get the socket: only the proxy, read-only.
[ "$(grep -c 'docker.sock:' "$T/compose.yaml")" = 1 ] || fail "the socket is given to more than the proxy"
has "$T/compose.yaml" "/var/run/docker.sock:/var/run/docker.sock:ro" compose

# ---- an update: the machine's first, then itself, last

reset() {
	: >"$T/calls"
	rm -rf "$T/data"
	mkdir -p "$T/data/launcher" "$T/data/core" "$T/data/node"
	echo mode=core >"$T/data/launcher/install.conf"
	# install.sh's updater, faked: it writes how each request went.
	cat >"$T/data/launcher/update.sh" <<EOF
#!/bin/sh
echo "update.sh \$*" >>"$T/calls"
for t in core node; do
  [ -f "$T/data/\$t/update-requested" ] || continue
  id=\$(sed -n 's/^id=//p' "$T/data/\$t/update-requested")
  rm -f "$T/data/\$t/update-requested"
  printf 'id=%s\naction=update\noutcome=%s\nfrom=0.7.0\nto=0.8.0\n' "\$id" "\$OUTCOME" >"$T/data/\$t/update-status"
done
EOF
}
reset
printf 'id=a1\naction=update\ncore=\nauto=0\n' >"$T/data/core/update-requested"
OUTCOME=ok run_in 'discover; tick' >/dev/null
u=$(grep -n '^update.sh' "$T/calls" | cut -d : -f 1)
p=$(grep -n '^pull -q ghcr.io/alexis-marcel/eriphe:0.8.0' "$T/calls" | cut -d : -f 1)
if [ -z "$u" ] || [ -z "$p" ] || [ "$u" -gt "$p" ]; then
	fail "update order: the machine's update, then the launcher's pull ($(tr '\n' '|' <"$T/calls"))"
fi
has "$T/calls" "tag ghcr.io/alexis-marcel/eriphe:0.8.0 ghcr.io/alexis-marcel/eriphe:latest" "the new launcher under its reference"
has "$T/calls" "run -d --rm --name eriphe-launcher-swap --network none --label eriphe.launcher=swap -v /var/run/docker.sock:/var/run/docker.sock sha256:new swap $ID" "the swap"
# Nothing waiting: nothing done.
: >"$T/calls"
OUTCOME=ok run_in 'discover; tick' >/dev/null
hasnt "$T/calls" "update.sh" "no request"
# A failed update leaves the launcher as it is; so does a rollback.
for case in "failed update" "ok rollback"; do
	reset
	OUTCOME=${case% *}
	if [ "${case#* }" = rollback ]; then
		OUTCOME=$OUTCOME run_in 'discover; printf "id=r1\naction=rollback\n" > "$DATA/core/update-status"; SELF_TO=; apply rollback; [ -z "$SELF_TO" ] || self_update "$SELF_TO"' >/dev/null
	else
		printf 'id=a2\naction=update\ncore=\nauto=0\n' >"$T/data/core/update-requested"
		OUTCOME=$OUTCOME run_in 'discover; tick' >/dev/null
	fi
	hasnt "$T/calls" "pull -q ghcr.io/alexis-marcel/eriphe" "$case"
done
# A local build is never replaced by the official image.
reset
echo eriphe-launcher:test >"$T/ref"
printf 'id=a3\naction=update\ncore=\nauto=0\n' >"$T/data/core/update-requested"
OUTCOME=ok run_in 'discover; tick' >/dev/null
hasnt "$T/calls" "pull -q" "local build"
echo ghcr.io/alexis-marcel/eriphe:latest >"$T/ref"
# eriphe-launcher update: eriphe-core's machine, then its node on that build.
reset
OUTCOME=ok run_in 'local_update update' >"$T/out"
[ "$(grep -c '^update.sh' "$T/calls")" = 2 ] || fail "local update: $(cat "$T/calls")"
has "$T/out" "eriphe-core: Updated to 0.8.0." "local update"
has "$T/calls" "run -d --rm --name eriphe-launcher-swap" "local update, then itself"

# ---- its new container: its own settings, not its old image's

cat >"$T/inspect.json" <<EOF
{"Id": "$ID", "Name": "/eriphe", "Image": "sha256:old",
 "Config": {"Hostname": "0123456789ab", "Image": "ghcr.io/alexis-marcel/eriphe:latest",
  "Env": ["ERIPHE_SHARES=shares=/mnt/user", "PATH=/usr/bin", "ERIPHE_LAUNCHER_VERSION=0.7.0"],
  "Cmd": ["run"], "Entrypoint": ["/usr/local/bin/eriphe-launcher"], "WorkingDir": "",
  "Labels": {"net.unraid.docker.managed": "dockerman", "org.opencontainers.image.version": "0.7.0"}},
 "HostConfig": {"Binds": ["/var/run/docker.sock:/var/run/docker.sock", "/mnt/user/appdata/eriphe:/data"], "RestartPolicy": {"Name": "unless-stopped"}},
 "NetworkSettings": {"Networks": {"bridge": {"IPAMConfig": null, "Aliases": null, "IPAddress": "172.17.0.2"}}}}
EOF
img='{"Env": ["PATH=/usr/bin", "ERIPHE_LAUNCHER_VERSION=0.7.0"], "Cmd": ["run"], "Entrypoint": ["/usr/local/bin/eriphe-launcher"], "WorkingDir": "", "Labels": {"org.opencontainers.image.version": "0.7.0"}}'
run_in "create_body \"\$(cat '$T/inspect.json')\" '$img'" >"$T/body.json"
want='{"Env":["ERIPHE_SHARES=shares=/mnt/user"],"Labels":{"net.unraid.docker.managed":"dockerman"},"Binds":["/var/run/docker.sock:/var/run/docker.sock","/mnt/user/appdata/eriphe:/data"],"Restart":"unless-stopped","Networks":{"bridge":{"IPAMConfig":null,"Aliases":[]}},"Hostname":null,"Cmd":null,"Entrypoint":null}'
got=$(jq -c '{Env, Labels, Binds: .HostConfig.Binds, Restart: .HostConfig.RestartPolicy.Name, Networks: .NetworkingConfig.EndpointsConfig, Hostname, Cmd, Entrypoint}' "$T/body.json")
[ "$got" = "$want" ] || fail "create body: $got"

# ---- uninstall: what it made; the data stays unless --data

reset
echo "name: eriphe" >"$T/data/launcher/compose.yaml"
echo '{}' >"$T/data/node/node.json"
run_in 'uninstall' >"$T/out"
for want in "compose -p eriphe --project-directory $T/data/launcher -f $T/data/launcher/compose.yaml --env-file $T/data/launcher/.env down --remove-orphans" \
	"network rm eriphe-tasks eriphe-system eriphe-docker" "volume rm eriphe-cache" \
	"images --format {{.Repository}}:{{.Tag}} ghcr.io/alexis-marcel/eriphe-core" \
	"update --restart no $ID" "stop -t 2 $ID"; do
	has "$T/calls" "$want" uninstall
done
hasnt "$T/calls" "images --format {{.Repository}}:{{.Tag}} ghcr.io/alexis-marcel/eriphe " "uninstall (its own image)"
[ -f "$T/data/node/node.json" ] || fail "uninstall removed the data"
[ -f "$T/data/launcher/uninstalled" ] || fail "uninstall leaves no mark"
has "$T/out" "docker rm -f eriphe" "uninstall says what is left to do"
# Uninstalled: a start does nothing (it would install it again).
if run_in 'uninstalled' >/dev/null; then :; else fail "the mark is not seen"; fi
run_in 'uninstall --data' >/dev/null
[ "$(cd "$T/data" && find . -mindepth 1 | sort | tr '\n' ' ')" = "./launcher ./launcher/uninstalled " ] ||
	fail "uninstall --data leaves $(cd "$T/data" && find . -mindepth 1)"

# ---- install.sh's ports: options, kept, and free before anything is installed

# installer CMD: a shell with install.sh's functions, as on Linux.
installer() { sh -c "ERIPHE_INSTALL_LIB=1; . '$here/install.sh'; OS=Linux NATIVE= HERE='this computer'; $1" 2>&1 || true; }
out=$(installer 'parse_args --core-mode --web-port 9443 --api-port=9800; INFO=; settle; echo "$WEB_PORT $API_PORT"; install_conf')
case $out in "9443 9800"*"web_port=9443"*"api_port=9800"*) ;; *) fail "port options: $out" ;; esac
# Kept from the earlier install when not given; 8443 and 8800 otherwise.
out=$(installer 'parse_args --core-mode; INFO="saved_web_port=9443"; settle; echo "$WEB_PORT $API_PORT"')
[ "$out" = "9443 8800" ] || fail "saved ports: $out"
out=$(ERIPHE_WEB_PORT=7443 installer 'parse_args --core-mode; INFO=; settle; echo "$WEB_PORT $API_PORT"')
[ "$out" = "7443 8800" ] || fail "ERIPHE_WEB_PORT: $out"
for bad in "--web-port 0" "--web-port 70000" "--api-port x" "--web-port 8800" "--core http://192.0.2.10:8800 --api-port 9800"; do
	out=$(installer "parse_args --core-mode $bad && INFO= && settle && echo passed")
	case $out in *passed*) fail "port options take $bad" ;; esac
done
compose=$(installer 'parse_args --core-mode --api-port 9800 --web-port 9443; INFO=; settle; MODE=core CORE_IMAGE=c NODE_IMAGE=n WORKER_IMAGE=w INSTALL=linux; compose_yaml 100 1')
case $compose in *'ports: ["0.0.0.0:9800:8800", "0.0.0.0:9443:8443"]'*'ERIPHE_API_PORT: "9800"'*) ;; *) fail "compose with other ports: $compose" ;; esac

# ---- --bind: one IPv4 address, kept; never IPv6 (Docker's userland proxy
# would hide the clients' addresses from eriphe-core)

compose=$(installer 'parse_args --core-mode --bind 127.0.0.1; INFO=; settle; MODE=core CORE_IMAGE=c NODE_IMAGE=n WORKER_IMAGE=w INSTALL=linux; compose_yaml 100 1; install_conf')
case $compose in *'ports: ["127.0.0.1:8800:8800", "127.0.0.1:8443:8443"]'*"bind=127.0.0.1"*) ;; *) fail "--bind: $compose" ;; esac
out=$(installer 'parse_args --core-mode; INFO="saved_mode=core
saved_bind=192.0.2.7"; settle; echo "$BIND"')
[ "$out" = 192.0.2.7 ] || fail "saved bind: $out"
out=$(installer 'parse_args --core-mode --bind 0.0.0.0; INFO="saved_mode=core
saved_bind=192.0.2.7"; settle; echo "bind=$BIND"')
[ "$out" = bind= ] || fail "--bind 0.0.0.0 goes back to every address: $out"
for bad in "--bind ::1" "--bind 256.0.0.1" "--bind host" "--core https://192.0.2.10:8800 --join-token t --bind 127.0.0.1"; do
	out=$(installer "parse_args --core-mode $bad && INFO= && settle && echo passed")
	case $out in *passed*) fail "--bind takes $bad" ;; esac
done
# The launcher: ERIPHE_BIND, not kept when it is no longer given.
mkdir -p "$T/data/launcher"
out=$(launcher ERIPHE_BIND=127.0.0.1 sh -c ". '$here/eriphe-launcher'; discover; settings; INFO=; settle; echo \"\$BIND\"" 2>&1)
[ "$out" = 127.0.0.1 ] || fail "ERIPHE_BIND: $out"
out=$(launcher sh -c ". '$here/eriphe-launcher'; discover; settings; INFO='saved_bind=127.0.0.1'; settle; echo \"bind=\$BIND\"" 2>&1)
[ "$out" = bind= ] || fail "the launcher keeps a bind no longer given: $out"

# ---- public addresses: the installers warn that the workspace is on the Internet

for a in 203.0.113.5 198.51.100.1 8.8.8.8 172.32.0.1 100.128.0.1 2001:db8::1 '[2001:db8::2]' ::ffff:203.0.113.5; do
	installer "public_address '$a' && echo yes" | grep -qx yes || fail "$a is not public"
done
for a in 10.0.0.1 172.16.0.1 172.31.255.1 192.168.1.2 100.64.0.1 100.127.0.1 127.0.0.1 169.254.1.1 0.0.0.0 \
	fd00::1 fc00::1 fe80::1 ::1 :: ::ffff:192.168.1.2 '<this-computer>' ''; do
	installer "public_address '$a' && echo yes" | grep -qx yes && fail "$a is public"
done
out=$(installer 'WEB_PORT=8443 API_PORT=8800; exposed_notice 203.0.113.5; printf "%s" "$NOTES"')
case $out in *"203.0.113.5 is a public address"*"cloud provider's firewall"*"Tailscale"*"reverse proxy"*"DOCKER-USER"*"--bind 127.0.0.1"*"#eriphe-on-a-vps"*) ;; *) fail "the public address notice: $out" ;; esac
out=$(installer 'WEB_PORT=8443 API_PORT=8800; exposed_notice 192.168.1.2; exposed_notice 100.64.0.1; printf "notes=%s" "$NOTES"')
[ "$out" = notes= ] || fail "a notice for a local address: $out"
# On 127.0.0.1 (--bind): how to reach it, not a warning.
out=$(installer 'WEB_PORT=8443 API_PORT=8800; exposed_notice 127.0.0.1; printf "%s" "$NOTES"')
case $out in *"ssh -L 8443:127.0.0.1:8443"*"Tailscale"*) ;; *) fail "the notice on 127.0.0.1: $out" ;; esac
case $out in *"public address"*) fail "127.0.0.1 is called public: $out" ;; esac

# ---- eriphe-core's time zone: this machine's on a new install, never
# changed on an older one, TZ first

out=$(TZ='' installer 'host_tz() { echo America/Montreal; }; parse_args --core-mode; INFO=; settle; echo "$CORE_TZ"; install_conf | grep ^tz=')
[ "$out" = "America/Montreal
tz=America/Montreal" ] || fail "new install's zone: $out"
out=$(TZ='' installer 'host_tz() { echo America/Montreal; }; parse_args --core-mode; INFO="saved_mode=core"; settle; echo "tz=$CORE_TZ"')
[ "$out" = tz= ] || fail "an older install takes this machine's zone: $out"
out=$(TZ='' installer 'host_tz() { echo America/Montreal; }; parse_args --core-mode; INFO="saved_mode=core
saved_tz=Asia/Tokyo"; settle; echo "$CORE_TZ"')
[ "$out" = Asia/Tokyo ] || fail "saved zone: $out"
out=$(TZ=Europe/Berlin installer 'host_tz() { echo America/Montreal; }; parse_args --core-mode; INFO="saved_mode=core
saved_tz=Asia/Tokyo"; settle; echo "$CORE_TZ"')
[ "$out" = Europe/Berlin ] || fail "TZ first: $out"
compose=$(TZ='' installer 'host_tz() { echo America/Montreal; }; parse_args --core-mode; INFO=; settle; MODE=core CORE_IMAGE=c NODE_IMAGE=n WORKER_IMAGE=w INSTALL=linux; compose_yaml 100 1')
case $compose in *"TZ: 'America/Montreal'"*) ;; *) fail "the zone in compose: $compose" ;; esac
# The launcher's container has its own zone, not the host's: TZ only.
out=$(launcher TZ='' sh -c ". '$here/eriphe-launcher'; discover; settings; host_tz() { echo America/Montreal; }; INFO=; settle; echo \"tz=\$CORE_TZ\"" 2>&1)
[ "$out" = tz= ] || fail "the launcher takes its container's zone: $out"
out=$(installer 'host_tz' | grep -Ec '^[A-Za-z]' || true)
[ "$out" -le 1 ] || fail "host_tz: several lines"

# ---- uninstall on Linux: its own log apart, not the install's, which goes

mkdir -p "$T/tmp"
out=$(installer "UNINSTALL=1 TMPDIR='$T/tmp' HOME='$T'; to_screen; echo \"\$LOG\" >&5")
case $out in "$T/tmp/eriphe-uninstall."*) ;; *) fail "the uninstall's log: $out" ;; esac
# A fake listener: ss names the program on a port.
mkdir -p "$T/ss"
cat >"$T/ss/ss" <<'EOF'
#!/bin/sh
case $* in
*":8443"*) echo 'LISTEN 0 511 0.0.0.0:8443 0.0.0.0:* users:(("nginx",pid=42,fd=6))' ;;
*":8800"*) echo 'LISTEN 0 4096 0.0.0.0:8800 0.0.0.0:* users:(("docker-proxy",pid=7,fd=4))' ;;
*":9800"*) echo 'LISTEN 0 4096 0.0.0.0:9800 0.0.0.0:*' ;;
esac
EOF
chmod +x "$T/ss/ss"
ports() { PATH="$T/ss:$PATH" installer "WEB_PORT=$1 API_PORT=$2; ports_free '$3'; echo free"; }
out=$(ports 8443 8801 "")
case $out in *"nginx already uses port 8443 of this computer"*"--web-port 9443"*) ;; *) fail "a taken port: $out" ;; esac
out=$(ports 9443 9800 "")
case $out in *"another program already uses port 9800"*"--api-port 9800"*) ;; *) fail "a port taken by someone unnamed: $out" ;; esac
out=$(ports 9443 8800 "docker-proxy a program")
[ "$out" = free ] || fail "Eriphe's own port, on an update: $out"
out=$(ports 9443 8801 "")
[ "$out" = free ] || fail "free ports: $out"
# A real listener, where python3 is: port_user sees it.
if command -v python3 >/dev/null 2>&1; then
	python3 -c 'import socket, sys, time
s = socket.socket(); s.bind(("127.0.0.1", 0)); s.listen(1)
open(sys.argv[1], "w").write(str(s.getsockname()[1])); time.sleep(20)' "$T/port" &
listener=$!
i=0
while [ ! -s "$T/port" ] && [ $i -lt 50 ]; do
	i=$((i + 1))
	sleep 0.1
done
p=$(cat "$T/port")
os=$(uname -s)
out=$(sh -c "ERIPHE_INSTALL_LIB=1; . '$here/install.sh'; OS=$os HERE=here; port_user $p" 2>&1)
[ -n "$out" ] || fail "a listener on port $p is not seen"
kill "$listener" 2>/dev/null || true
wait "$listener" 2>/dev/null || true
fi

if [ "$fails" -gt 0 ]; then
	echo "launcher_test.sh: $fails failed" >&2
	exit 1
fi
echo "launcher_test.sh: ok"
