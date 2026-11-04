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
# The node's share add and remove, on its list ($T/data/node/shares.json):
# it refuses /mnt/user/Refused.
L=$T/data/node/shares.json
# The node's helpers reach a folder through the engine: in its list only.
if [ "$1 $2 $3 $4 $5 $6 $7" = "exec -e ERIPHE_SHARE_COMMAND= eriphe-node eriphe-node share add" ]; then
  shift 7
  rc=0 c=false
  [ -f "$L" ] || echo '{"folders": []}' >"$L"
  for a; do
    case $a in
    --copyable) c=true ;;
    /mnt/user/Refused) echo "Not shared: $a: no"; rc=1 ;;
    /*)
      if ! grep -Fxq -- "$a" "$T/data/proxy/binds" 2>/dev/null; then echo "Not shared: $a: not in the engine's list"; rc=1; continue; fi
      jq --arg p "$a" --argjson c "$c" '.folders |= (map(select(.path != $p)) + [{id: "x", name: ($p | split("/") | last), path: $p, access: "read", copyable: $c}])' "$L" >"$L.new" && mv "$L.new" "$L" ;;
    esac
  done
  exit $rc
fi
if [ "$1 $2 $3 $4 $5" = "exec eriphe-node eriphe-node share remove" ]; then
  shift 5
  for a; do
    jq --arg a "$a" '.folders |= map(select(.path != $a and .name != $a))' "$L" >"$L.new" && mv "$L.new" "$L"
  done
  exit 0
fi
case $* in
*"share check -host /host -data /mnt/user/appdata/eriphe /mnt/user/Old") echo "eriphe-node share: no eriphe-node in /data: give its data folder with -root" >&2; exit 1 ;;
*"share check -host /host -data /mnt/user/appdata/eriphe /mnt/user/Secret") echo "eriphe-node share: /mnt/user/Secret: this folder holds .ssh, which is never shared: share a folder inside it" >&2; exit 1 ;;
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
"logs eriphe-node") echo "2026/11/03 10:00:00 connected to http://192.0.2.10:8800" ;;
# The node joins at its first start (host_up's script): node.json, then.
*" up -d --pull never --remove-orphans") [ -z "${ERIPHE_JOIN_TOKEN:-}" ] || echo '{}' >"$T/data/node/node.json" ;;
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
out=$(launcher ERIPHE_WEB_PORT=8843 sh -c ". '$here/eriphe-launcher'; discover; settings; INFO=; settle; echo \"pin=\$PIN shares=\$SHARES \$WEB_PORT\"" 2>&1)
[ "$out" = "pin=edge shares= 8843" ] || fail "settings: $out"
# ERIPHE_SHARES, the places of before, is no longer used: said so.
out=$(launcher ERIPHE_SHARES=shares=/mnt/user sh -c ". '$here/eriphe-launcher'; discover; settings; echo \"shares=\$SHARES\"" 2>&1)
case $out in *"ERIPHE_SHARES is no longer used"*"docker exec eriphe eriphe share add"*"shares=") ;; *) fail "ERIPHE_SHARES: $out" ;; esac
out=$(launcher ERIPHE_VERSION=0.7.0 sh -c ". '$here/eriphe-launcher'; discover; settings; echo \"pin=\$PIN\"" 2>&1)
[ "$out" = pin=0.7.0 ] || fail "ERIPHE_VERSION: $out"
echo ghcr.io/alexis-marcel/eriphe:latest >"$T/ref"
for bad in "ERIPHE_MODE=node" "ERIPHE_MODE=node ERIPHE_CORE=http://192.0.2.10:8800" "ERIPHE_MODE=other" \
	"ERIPHE_CORE=http://192.0.2.10:8800" "ERIPHE_WEB_PORT=70000" "ERIPHE_WEB_PORT=8800"; do
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
# ---- the folders shared: each alone, nothing else of the host

# What may be shared, as written: before any container sees it.
for ok in "/mnt/user/1 PROJECTS" "/mnt/user/Projects/app/system" "/srv/docs" "/mnt/user/Sam #2"; do
	out=$(run_in "discover; folder_ok '$ok' && echo ok")
	[ "$out" = ok ] || fail "folder_ok $ok: $out"
done
for bad in "mnt/user/Docs" "/" "/mnt/user/Docs/" "/mnt/user/../user/Docs" "/mnt/user/./Docs" "/mnt/user/appdata" \
	"/mnt/user/AppData/plex" "/mnt/user/.ssh" "/mnt/user/Docs/.git" "/mnt/user/a,b" "/mnt/user/a:b" "/mnt/user/Sam's" \
	'/mnt/user/a$b' '/mnt/user/a"b' "/etc" "/var/run" "/root/docs" "/mnt/user/system" "/mnt/disk1/domains/vm" \
	"/mnt/user/appdata/eriphe/core" "/mnt" "/mnt/user/Docs "; do
	out=$(launcher sh -c ". '$here/eriphe-launcher'; discover; folder_ok \"\$1\" && echo ok" sh "$bad" 2>&1 || true)
	case $out in "" | *ok) fail "folder_ok takes $bad: $out" ;; esac
done

# An install of before with places: the folders shared in them become the
# list, each given alone, a :ro place's read-only; what is not in a place,
# or no longer passes, goes; once.
reset_folders() {
	rm -rf "$T/data"
	mkdir -p "$T/data/launcher" "$T/data/node"
	: >"$T/calls"
}
reset_folders
printf 'mode=core\nshares=shares=/mnt/user,media=/mnt/media:ro\n' >"$T/data/launcher/install.conf"
cat >"$T/data/node/shares.json" <<'EOF'
{"folders": [{"id": "a", "name": "1 PROJECTS", "path": "/mnt/user/1 PROJECTS", "access": "drop"},
 {"id": "b", "name": "Films", "path": "/mnt/media/Films", "access": "read"},
 {"id": "c", "name": "Other", "path": "/srv/other", "access": "read"},
 {"id": "d", "name": "Plex", "path": "/mnt/user/appdata/plex", "access": "read"}]}
EOF
run_in 'discover; folders_migrate' >"$T/out"
[ "$(cat "$T/data/launcher/folders")" = "/mnt/user/1 PROJECTS
/mnt/media/Films:ro" ] || fail "migration: $(cat "$T/data/launcher/folders")"
has "$T/out" "/mnt/user/appdata/plex is no longer given to Eriphe" "migration (a folder refused)"
has "$T/out" "each of the 2 folders shared is now given alone" "migration"
echo '{"folders": []}' >"$T/data/node/shares.json"
run_in 'discover; folders_migrate' >/dev/null
[ "$(grep -c . "$T/data/launcher/folders")" = 2 ] || fail "migration, again"
# From ERIPHE_SHARES, kept in the container's settings; places with nothing shared simply go.
reset_folders
launcher ERIPHE_SHARES=shares=/mnt/user sh -c ". '$here/eriphe-launcher'; discover; folders_migrate" >/dev/null 2>&1
{ [ -f "$T/data/launcher/folders" ] && [ ! -s "$T/data/launcher/folders" ]; } || fail "migration, nothing shared"

# ---- the compose project: the host's paths; no folder for the node, its
# ---- engine reading the list, Syncthing the folders it may copy only

reset_folders
printf '/mnt/user/1 PROJECTS\n/mnt/media/Films:ro\n/mnt/missing\n' >"$T/data/launcher/folders"
cat >"$T/data/node/shares.json" <<'EOF'
{"folders": [{"id": "a", "name": "1 PROJECTS", "path": "/mnt/user/1 PROJECTS", "access": "drop"},
 {"id": "b", "name": "Films", "path": "/mnt/media/Films", "access": "read", "copyable": true},
 {"id": "c", "name": "missing", "path": "/mnt/missing", "access": "read", "copyable": true}]}
EOF
launcher ERIPHE_API_PORT=18800 TZ=Europe/Paris sh -c ". '$here/eriphe-launcher'; discover; settings; INFO=; settle
	compose_yaml 100 1; env_file" >"$T/compose.yaml" 2>&1
for want in "- /mnt/user/appdata/eriphe/core:/data" "- /mnt/user/appdata/eriphe/node:/mnt/user/appdata/eriphe/node" \
	'command: ["-root", "/mnt/user/appdata/eriphe/node",' \
	'command: ["engine", "-listen", ":8792", "-allow-from", "eriphe-node",' "container_name: eriphe-engine" \
	'"-bind-from", "/mnt/user/appdata/eriphe/node", "-bind-file", "/etc/eriphe-engine/binds", "-host-root", "/host",' \
	"- /mnt/user/appdata/eriphe/proxy:/etc/eriphe-engine:ro" "source: /" "target: /host" \
	'"-network", "eriphe-system", "-task-network", "eriphe-tasks", "-node-image", "ghcr.io/alexis-marcel/eriphe-node:latest"]' \
	"cap_add: [DAC_READ_SEARCH]" "depends_on: [engine, core]" "ERIPHE_ENGINE_URL: http://eriphe-engine:8792" \
	"ERIPHE_FOLDER_HELPER: ghcr.io/alexis-marcel/eriphe-node:latest" '- "/mnt/media/Films:/mnt/media/Films:ro"' \
	'ports: ["0.0.0.0:18800:8800", "0.0.0.0:8443:8443"]' "ERIPHE_INSTALL: docker" 'ERIPHE_API_PORT: "18800"' \
	"TZ: 'Europe/Paris'" "image: ghcr.io/alexis-marcel/eriphe-core:latest" '"-always-on"' "user: \"0:100\"" \
	'ERIPHE_SHARE_COMMAND: "docker exec eriphe eriphe share"' "ERIPHE_NODE_SHARES=''"; do
	has "$T/compose.yaml" "$want" compose
done
hasnt "$T/compose.yaml" "/data/node" "compose (the launcher's own path)"
# No mount propagation on the host's /: Docker refuses rslave where / is not
# a shared mount (Unraid), and the engine would not start.
hasnt "$T/compose.yaml" "propagation" "compose (the engine's view of the host)"
# The node has no Docker access but the engine's verbs: no DOCKER_HOST, no proxy.
hasnt "$T/compose.yaml" "DOCKER_HOST" "compose (the node's Docker)"
hasnt "$T/compose.yaml" "docker-proxy" "compose (the socket proxy)"
# The node and the engine get no folder shared: only Syncthing, once, what it may copy.
hasnt "$T/compose.yaml" "1 PROJECTS:" "compose (a folder not copied)"
[ "$(grep -c 'Films:' "$T/compose.yaml")" = 1 ] || fail "compose: a folder copied is mounted into more than Syncthing"
# A folder not on the host now is left out, not the whole start.
hasnt "$T/compose.yaml" "/mnt/missing:" "compose (a folder away)"
has "$T/compose.yaml" "/mnt/missing is not a folder of this machine now" "compose (a folder away)"
# Nothing else of the host: no parent of a folder shared, nor the data.
for p in /mnt/user /mnt/media /mnt/user/appdata/eriphe; do
	hasnt "$T/compose.yaml" "- \"$p:" "compose (only the folders)"
done

# ---- eriphe share: the launcher checks, lists, then the node shares;
# ---- nothing restarts but Syncthing, when the folders copied change

# share_in ARGS: eriphe share, the stack's start and the host's answer faked.
share_in() {
	launcher sh -c ". '$here/eriphe-launcher'
		on_steps() { OUT=; }
		stack_up() { echo stack_up >>\"\$T/calls\"; }
		share \"\$@\"" sh "$@" 2>&1
}
# syncthing_up: Syncthing made again, how many times since $T/calls was emptied.
syncthing_up() { grep -c ' up -d --no-deps --pull never syncthing$' "$T/calls" || true; }
B=$T/data/proxy/binds
reset_folders
echo mode=core >"$T/data/launcher/install.conf"
: >"$T/all-calls"
keep_calls() { cat "$T/calls" >>"$T/all-calls"; : >"$T/calls"; }
out=$(share_in add --drop "/mnt/user/1 PROJECTS") || fail "share add: $out"
has "$T/calls" "run --rm --network none --read-only --cap-drop ALL --cap-add DAC_READ_SEARCH --security-opt no-new-privileges --user 0:0 --mount type=bind,src=/,dst=/host,readonly --entrypoint eriphe-node ghcr.io/alexis-marcel/eriphe-node:latest share check -host /host -data /mnt/user/appdata/eriphe /mnt/user/1 PROJECTS" "share add (the node's checks, on the host, first)"
has "$T/calls" "exec -e ERIPHE_SHARE_COMMAND= eriphe-node eriphe-node share add --drop /mnt/user/1 PROJECTS" "share add (then the node's)"
[ "$(cat "$T/data/launcher/folders")" = "/mnt/user/1 PROJECTS" ] || fail "share add: the list"
[ "$(cat "$B")" = "/mnt/user/1 PROJECTS" ] || fail "share add: the engine's list: $(cat "$B")"
[ "$(syncthing_up)" = 0 ] || fail "share add of a folder not copied makes Syncthing again"
case $out in *"pause"*) fail "share add of a folder not copied: $out" ;; esac
keep_calls
# Copies allowed: Syncthing alone made again, on it; said.
out=$(share_in add --drop --copyable "/mnt/user/1 PROJECTS") || fail "share add --copyable: $out"
[ "$(syncthing_up)" = 1 ] || fail "share add --copyable: $(cat "$T/calls")"
case $out in *"Copies to your other machines pause a few seconds"*) ;; *) fail "share add --copyable: $out" ;; esac
has "$T/data/launcher/compose.yaml" '- "/mnt/user/1 PROJECTS:/mnt/user/1 PROJECTS"' "share add --copyable (Syncthing's mount)"
[ "$(cat "$T/data/launcher/copied")" = "/mnt/user/1 PROJECTS" ] || fail "share add --copyable: $(cat "$T/data/launcher/copied")"
keep_calls
# Shared already, the same copies: its level anew, nothing made again.
share_in add --manage --copyable "/mnt/user/1 PROJECTS" >/dev/null || fail "share add again"
has "$T/calls" "eriphe-node share add --manage --copyable /mnt/user/1 PROJECTS" "share add again"
[ "$(syncthing_up)" = 0 ] || fail "share add again makes Syncthing again"
keep_calls
# Refused here or by the node's checks: never listed.
for bad in /mnt/user/appdata/plex /mnt/user/Secret; do
	out=$(share_in add "$bad") && fail "share add $bad passes"
	case $out in *"Not shared: $bad: "*) ;; *) fail "share add $bad: $out" ;; esac
	hasnt "$B" "$bad" "share add $bad"
	keep_calls
done
[ "$out" = "Not shared: /mnt/user/Secret: this folder holds .ssh, which is never shared: share a folder inside it" ] ||
	fail "share add, the node's reason: $out"
# A node from before eriphe share cannot check: update first.
out=$(share_in add /mnt/user/Old) && fail "share add with an older node passes"
case $out in *"cannot check a folder yet: update Eriphe first"*) ;; *) fail "share add with an older node: $out" ;; esac
keep_calls
# Refused by the node: off the list again, the engine's too.
share_in add /mnt/user/Docs /mnt/user/Refused >/dev/null && fail "share add of a folder the node refuses passes"
[ "$(cat "$T/data/launcher/folders")" = "/mnt/user/1 PROJECTS
/mnt/user/Docs" ] || fail "share add, refused by the node: $(cat "$T/data/launcher/folders")"
hasnt "$B" "Refused" "share add, refused by the node"
keep_calls
# Tasks at work: shared now all the same, nothing of theirs restarted.
echo tasks=1 >"$T/data/node/awake"
share_in add /mnt/user/Music >/dev/null || fail "share add while a task runs"
has "$B" "/mnt/user/Music" "share add while a task runs"
out=$(share_in add --now /mnt/user/Films) && fail "share add --now passes: $out"
keep_calls
# Remove: the node first, then off the lists at once.
share_in remove Music >/dev/null || fail "share remove"
hasnt "$T/data/launcher/folders" "Music" "share remove"
hasnt "$B" "Music" "share remove (the engine's list)"
[ "$(syncthing_up)" = 0 ] || fail "share remove of a folder not copied makes Syncthing again"
keep_calls
out=$(share_in remove "/mnt/user/1 PROJECTS")
[ "$(syncthing_up)" = 1 ] || fail "share remove of a folder copied: $(cat "$T/calls")"
case $out in *"pause a few seconds"*) ;; *) fail "share remove of a folder copied: $out" ;; esac
hasnt "$T/data/launcher/compose.yaml" "1 PROJECTS" "share remove (Syncthing's mount)"
[ "$(cat "$T/data/launcher/folders")" = "/mnt/user/Docs" ] || fail "share remove: $(cat "$T/data/launcher/folders")"
rm "$T/data/node/awake"
keep_calls
# A folder the node no longer shares (stopped from the workspace) leaves
# the lists at the next tick.
echo '{"folders": []}' >"$T/data/node/shares.json"
run_in 'discover; on_steps() { OUT=; }; tick' >/dev/null
[ ! -s "$T/data/launcher/folders" ] || fail "unshared from the workspace: $(cat "$T/data/launcher/folders")"
[ ! -s "$B" ] || fail "unshared from the workspace, the engine's list: $(cat "$B")"
keep_calls
# Never the node, the engine or eriphe-core: no compose up but Syncthing's, no restart.
cat "$T/all-calls" >"$T/calls"
if grep -E ' (up|restart|stop|rm|create)( |$)' "$T/calls" | grep -v ' up -d --no-deps --pull never syncthing$' | grep -v '^run ' >"$T/out"; then
	fail "eriphe share made more than Syncthing again: $(cat "$T/out")"
fi
hasnt "$T/calls" "stack_up" "eriphe share (the stack)"
# list, snapshots and restore are the node's.
: >"$T/calls"
share_in list >/dev/null
has "$T/calls" "exec eriphe-node eriphe-node share list" "share list"
out=$(share_in frobnicate) && fail "share frobnicate passes"
# As eriphe, only share.
ln -s "$here/eriphe-launcher" "$T/eriphe"
out=$(env ERIPHE_LAUNCHER_LIB="$here/install.sh" "$T/eriphe" update 2>&1) && fail "eriphe update passes"
case $out in *"eriphe share add"*) ;; *) fail "eriphe update: $out" ;; esac
out=$(env ERIPHE_LAUNCHER_LIB="$here/install.sh" "$T/eriphe" share --help 2>&1)
case $out in *"eriphe share add [--name NAME]"*) ;; *) fail "eriphe share --help: $out" ;; esac
case $out in *"--now"*) fail "eriphe share --help: $out" ;; esac
rm -rf "$T/data"
mkdir -p "$T/data"
has "$T/compose.yaml" 'ERIPHE_WAKE_MAC: ${ERIPHE_WAKE_MAC:-}' "compose (the card to wake)"
# The machine's network card, for Wake-on-LAN: read on the host's network,
# by a short-lived container of this image, not this container's own.
launcher ERIPHE_MODE=node ERIPHE_CORE=http://192.0.2.10:8800 ERIPHE_JOIN_TOKEN=t sh -c ". '$here/eriphe-launcher'; discover; settings; INFO=; settle
	env_file" >"$T/env" 2>&1
has "$T/env" "ERIPHE_WAKE_MAC='00:00:5e:00:53:01'" "the card's MAC"
has "$T/env" "ERIPHE_WAKE_BROADCAST='192.0.2.255'" "the card's network"
has "$T/calls" "run --rm --network host --entrypoint sh -e ERIPHE_INSTALL_LIB=1 -e CORE_URL=http://192.0.2.10:8800 sha256:old" "the card, from the host's network"
# The node and eriphe-core never get the socket: only the engine, read-only.
[ "$(grep -c 'docker.sock:' "$T/compose.yaml")" = 1 ] || fail "the socket is given to more than the engine"
has "$T/compose.yaml" "/var/run/docker.sock:/var/run/docker.sock:ro" compose

# ---- the stack up, by host_up's own script: a new install, and the update
# ---- of one from before eriphe-node engine (its socket proxy goes)

# stack_in [ENV=V...]: the launcher's stack_up, a node's, its host script run here.
stack_in() {
	launcher ERIPHE_MODE=node ERIPHE_CORE=http://192.0.2.10:8800 ERIPHE_JOIN_TOKEN=t "$@" sh -c ". '$here/eriphe-launcher'
		on_host() { sh -s; }
		discover; PULL=missing UPDATE_BY=loop; settings; INFO=; settle; COPIED=; write_binds; stack_up" >"$T/out" 2>&1 || fail "stack_up: $(cat "$T/out")"
}
C=$T/data/launcher/compose.yaml
rm -rf "$T/data"
mkdir -p "$T/data/launcher"
: >"$T/calls"
stack_in
has "$C" 'command: ["engine", "-listen", ":8792"' "new install (the engine)"
has "$C" "ERIPHE_ENGINE_URL: http://eriphe-engine:8792" "new install (the node's engine)"
has "$C" "depends_on: [engine]" "new install (the node after the engine)"
hasnt "$C" "DOCKER_HOST" "new install (the node's Docker)"
hasnt "$C" "docker-proxy" "new install (no socket proxy)"
has "$C" "- /mnt/user/appdata/eriphe/proxy:/etc/eriphe-engine:ro" "new install (the list of the folders shared)"
[ -f "$T/data/proxy/binds" ] || fail "new install: no list of the folders shared for the engine"
has "$T/calls" "up -d --pull never --remove-orphans" "new install (up)"
# An install from before the engine: its compose project has the socket
# proxy and the node reaches Docker through it. Made again with the engine;
# the proxy's container removed, before anything starts; the folders shared
# stay in the same list.
cat >"$C" <<'EOF'
name: eriphe
services:
  docker-proxy:
    container_name: eriphe-docker-proxy
    command: ["docker-proxy", "-listen", ":2375", "-bind-file", "/etc/eriphe-proxy/binds"]
  node:
    container_name: eriphe-node
    environment:
      DOCKER_HOST: tcp://eriphe-docker-proxy:2375
EOF
echo '/mnt/user/1 PROJECTS' >"$T/data/launcher/folders"
echo '{}' >"$T/data/node/node.json"
: >"$T/calls"
stack_in
has "$C" 'command: ["engine", "-listen", ":8792"' "update from the proxy (the engine)"
has "$C" "ERIPHE_ENGINE_URL: http://eriphe-engine:8792" "update from the proxy (the node's engine)"
hasnt "$C" "DOCKER_HOST" "update from the proxy (the node's Docker)"
hasnt "$C" "docker-proxy" "update from the proxy (the proxy's service)"
r=$(grep -n '^rm -f eriphe-docker-proxy$' "$T/calls" | cut -d : -f 1)
u=$(grep -n ' up -d --pull never --remove-orphans$' "$T/calls" | head -n 1 | cut -d : -f 1)
if [ -z "$r" ] || [ -z "$u" ] || [ "$r" -gt "$u" ]; then
	fail "update from the proxy: its container removed, then the stack up ($(tr '\n' '|' <"$T/calls"))"
fi
[ "$(cat "$T/data/proxy/binds")" = "/mnt/user/1 PROJECTS" ] || fail "update from the proxy: the engine's list: $(cat "$T/data/proxy/binds")"
rm -rf "$T/data"
mkdir -p "$T/data"

# ---- the machine's own settings, when the host is not the machine (WSL):
# ---- its wake card, its session folder, its share command; none by default

E=$T/data/launcher/.env
node_service() { sed -n '/^  node:$/,/^  syncthing:$/p' "$C"; }
mkdir -p "$T/data/launcher"
: >"$T/calls"
stack_in
has "$E" "ERIPHE_WAKE_MAC='00:00:5e:00:53:01'" "the wake card, found"
has "$T/calls" "run --rm --network host --entrypoint sh" "the wake card, looked for on the host's network"
hasnt "$C" "eriphe-session" "no session folder"
hasnt "$C" "ERIPHE_SESSION_FILE" "no session file"
has "$C" 'ERIPHE_SHARE_COMMAND: "docker exec eriphe eriphe share"' "the share command, by default"
cp "$C" "$T/compose.default"
rm -rf "$T/data"
mkdir -p "$T/data/launcher"
: >"$T/calls"
stack_in ERIPHE_WAKE_MAC="00-00-5E-00-53-02, 00:00:5e:00:53:03" ERIPHE_WAKE_BROADCAST=192.0.2.255 \
	ERIPHE_SESSION_DIR=/mnt/eriphe-session ERIPHE_SHARE_COMMAND='eriphe share $1'
has "$E" "ERIPHE_WAKE_MAC='00:00:5e:00:53:02,00:00:5e:00:53:03'" "the wake card, given"
has "$E" "ERIPHE_WAKE_BROADCAST='192.0.2.255'" "the wake card's broadcast, given"
hasnt "$T/calls" "--network host --entrypoint sh" "the wake card, given: not looked for"
node_service >"$T/node.yaml"
has "$T/node.yaml" '- "/mnt/eriphe-session:/run/eriphe-session:ro"' "the session folder, read-only, in the node"
has "$T/node.yaml" "ERIPHE_SESSION_FILE: /run/eriphe-session/session.json" "the session file, for the node"
[ "$(grep -c eriphe-session "$C")" = 2 ] || fail "the session folder elsewhere than the node: $(grep -n eriphe-session "$C")"
has "$T/node.yaml" 'ERIPHE_SHARE_COMMAND: "eriphe share $$1"' "the share command, given (its \$ for Compose)"
# Not given again: not kept from the run before.
rm -rf "$T/data"
mkdir -p "$T/data/launcher"
: >"$T/calls"
stack_in
cmp -s "$C" "$T/compose.default" || fail "the settings kept from the run before: $(diff "$T/compose.default" "$C")"
has "$E" "ERIPHE_WAKE_MAC='00:00:5e:00:53:01'" "the wake card, found again"
# The share command in what the launcher says.
rm -rf "$T/data"
mkdir -p "$T/data"
out=$(launcher ERIPHE_SHARE_COMMAND='eriphe share' ERIPHE_SHARES=shares=/mnt/user sh -c ". '$here/eriphe-launcher'; discover; settings" 2>&1 || true)
case $out in *"Share a folder with: eriphe share add <folder>"*) ;; *) fail "the share command, said: $out" ;; esac
for bad in "ERIPHE_WAKE_BROADCAST=192.0.2.255" "ERIPHE_WAKE_MAC=00:00:5e:00:53" "ERIPHE_WAKE_MAC=00:00:00:00:00:00" \
	"ERIPHE_WAKE_MAC=00:00:5e:00:53:zz" "ERIPHE_WAKE_MAC=," "ERIPHE_WAKE_MAC=00:00:5e:00:53:01 ERIPHE_WAKE_BROADCAST=192.0.2.256" \
	"ERIPHE_WAKE_MAC=00:00:5e:00:53:01 ERIPHE_WAKE_BROADCAST=fe80::1" "ERIPHE_SESSION_DIR=mnt/session" "ERIPHE_SESSION_DIR=/" \
	"ERIPHE_SESSION_DIR=/mnt/a:b" "ERIPHE_SESSION_DIR=/mnt/Sam's"; do
	# shellcheck disable=SC2086 # one argument per variable
	if launcher $bad sh -c ". '$here/eriphe-launcher'; discover; settings" >/dev/null 2>&1; then
		fail "settings take $bad"
	fi
done
if launcher ERIPHE_SHARE_COMMAND="$(printf 'eriphe share\nx')" sh -c ". '$here/eriphe-launcher'; discover; settings" >/dev/null 2>&1; then
	fail "settings take a share command on two lines"
fi
rm -rf "$T/data"
mkdir -p "$T/data"

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
has "$T/calls" "run -d --rm --name eriphe-launcher-swap --network none --label eriphe.launcher=swap --cap-drop ALL --read-only --tmpfs /tmp --security-opt no-new-privileges -v /var/run/docker.sock:/var/run/docker.sock sha256:new swap $ID" "the swap, hardened"
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
case $out in *"203.0.113.5 is a public address"*"cloud provider's firewall"*"first setup through an SSH tunnel"*"ssh -L 127.0.0.1:8443:127.0.0.1:8443 -o ExitOnForwardFailure=yes"*"Tailscale"*"reverse proxy"*"DOCKER-USER"*"--bind 127.0.0.1"*"#eriphe-on-a-vps"*) ;; *) fail "the public address notice: $out" ;; esac
out=$(installer 'WEB_PORT=8443 API_PORT=8800; exposed_notice 192.168.1.2; exposed_notice 100.64.0.1; printf "notes=%s" "$NOTES"')
[ "$out" = notes= ] || fail "a notice for a local address: $out"
# On 127.0.0.1 (--bind): how to reach it, not a warning.
out=$(installer 'WEB_PORT=8443 API_PORT=8800; exposed_notice 127.0.0.1; printf "%s" "$NOTES"')
case $out in *"ssh -L 127.0.0.1:8443:127.0.0.1:8443 -o ExitOnForwardFailure=yes"*"Tailscale"*) ;; *) fail "the notice on 127.0.0.1: $out" ;; esac
case $out in *"public address"*) fail "127.0.0.1 is called public: $out" ;; esac
# On a public address, the first setup goes through an SSH tunnel.
out=$(installer 'parse_args --core-mode; INFO=; settle; lan_address() { echo 203.0.113.5; }; UPDATING=; outcome "setup_code=ABCD-1234"')
case $out in *"ssh -L 127.0.0.1:8443:127.0.0.1:8443 -o ExitOnForwardFailure=yes <user>@203.0.113.5"*"https://127.0.0.1:8443/setup"*"ABCD-1234"*) ;; *) fail "the setup on a public address: $out" ;; esac
case $out in *"https://203.0.113.5:8443/setup"*) fail "the setup over the Internet: $out" ;; esac
out=$(installer 'parse_args --core-mode; INFO=; settle; lan_address() { echo 192.168.1.2; }; UPDATING=; outcome "setup_code=ABCD-1234"')
case $out in *"https://192.168.1.2:8443/setup"*) ;; *) fail "the setup on a local address: $out" ;; esac
# The same SSH tunnel everywhere.
out=$(installer 'WEB_PORT=8443; ssh_tunnel; echo; ssh_tunnel 203.0.113.5')
[ "$out" = "ssh -L 127.0.0.1:8443:127.0.0.1:8443 -o ExitOnForwardFailure=yes <user>@<server>
ssh -L 127.0.0.1:8443:127.0.0.1:8443 -o ExitOnForwardFailure=yes <user>@203.0.113.5" ] || fail "ssh_tunnel: $out"
# Run again with remote access on: its address, not only the SSH tunnel.
out=$(installer 'parse_args --core-mode; INFO=; settle; BIND=127.0.0.1; UPDATING=1 before=0.1.0; outcome "setup_code=
remote=https://eriphe.tail0000.ts.net
version=0.1.0"; printf "%s" "$NOTES"')
case $out in *"Open https://eriphe.tail0000.ts.net in your browser"*"Tailscale network"*) ;; *) fail "the tailnet address: $out" ;; esac
case $out in *"ssh -L"*) fail "the SSH tunnel with remote access on: $out" ;; esac
out=$(installer 'parse_args --core-mode; INFO=; settle; lan_address() { echo 192.168.1.2; }; UPDATING=1 before=; outcome "remote=https://eriphe.tail0000.ts.net"')
case $out in *"https://eriphe.tail0000.ts.net"*"On the local network: https://192.168.1.2:8443"*) ;; *) fail "the tailnet and local addresses: $out" ;; esac
# The setup code is printed, not logged.
out=$(installer 'LOG=$(mktemp); SCREEN=1; exec 5>&1; say_secret "  Setup code: " ABCD-1234; echo "log: $(cat "$LOG")"; rm -f "$LOG"')
case $out in *"  Setup code: ABCD-1234"*"log:   Setup code: (printed on the screen, not logged)") ;; *) fail "the setup code in the log: $out" ;; esac

# ---- printed paragraphs: wrapped between words, never in an address

long=https://github.com/Alexis-Marcel/eriphe-system/blob/main/install/README.md#eriphe-on-a-vps
out=$(installer "para 'The guide is here: $long, read it first.'")
[ "$out" = "The guide is here:
$long,
read it first." ] || fail "an address wrapped: $out"
out=$(installer "notice 'one: $long'; notice 'two'; good_to_know")
case $out in *"  - One:
    $long"*"  - Two"*) ;; *) fail "an address wrapped in a notice: $out" ;; esac

# ---- the model server it lends

for ok in http://eriphe-ollama:11434 'http://[fd00::5]:11434' https://models.example.org/v1; do
	out=$(installer "parse_args --core https://192.0.2.10:8800 --join-token t --model-server '$ok' && INFO= && settle && echo passed")
	case $out in *passed*) ;; *) fail "--model-server $ok refused: $out" ;; esac
done
for bad in http://127.0.0.1:11434 'http://[::1]:11434' 'http://[::1' 192.0.2.5:11434; do
	out=$(installer "parse_args --core https://192.0.2.10:8800 --join-token t --model-server '$bad' && INFO= && settle && echo passed")
	case $out in *passed*) fail "--model-server $bad accepted" ;; esac
done
out=$(installer 'MODEL_SERVER=http://eriphe-ollama:11434/v1/; models_url')
[ "$out" = http://eriphe-ollama:11434/v1/models ] || fail "models URL: $out"
out=$(installer 'MODEL_SERVER=http://eriphe-ollama:11434; model_server_line "model_server_answers=yes"; printf "notes=%s" "$NOTES"')
[ "$out" = "
Lends the model server at http://eriphe-ollama:11434 (answers: yes).
notes=" ] || fail "the model server line: $out"
out=$(installer 'MODEL_SERVER=http://eriphe-ollama:11434; model_server_line "model_server_answers=no"; printf "%s" "$NOTES"')
case $out in *"(answers: no)."*"does not answer"*) ;; *) fail "a model server that does not answer: $out" ;; esac
out=$(installer 'MODEL_SERVER=; model_server_line ""; echo end')
[ "$out" = end ] || fail "a model server line without one: $out"

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

# ---- eriphe-share: the node's command, with the folders made absolute

installer 'DATA=/srv/eriphe; share_cmd' >"$T/eriphe-share"
shellcheck -s sh "$T/eriphe-share" || fail "eriphe-share: shellcheck"
mkdir -p "$T/here/Clients" "$T/here/add"
# As root already: no sudo.
sed 's/^\[ "$(id -u)" = 0 \] || exec sudo .*//' "$T/eriphe-share" >"$T/eriphe-share-root"
rm -f "$T/calls"
(cd "$T/here" && sh "$T/eriphe-share-root" add Clients --name Clients --drop /mnt/user/Docs) >/dev/null 2>&1
has "$T/calls" "exec -i eriphe-node eriphe-node share -root /srv/eriphe/node add $(cd "$T/here/Clients" && pwd) --name Clients --drop /mnt/user/Docs" "eriphe-share"
rm -f "$T/calls"
(cd "$T/here" && sh "$T/eriphe-share-root" add) >/dev/null 2>&1
has "$T/calls" "share -root /srv/eriphe/node add" "eriphe-share: the command itself is never a folder"
installer 'DATA=/srv/eriphe MODE= NODE_IMAGE=n WORKER_IMAGE=w INSTALL=linux; compose_yaml 100 1' >"$T/compose-share"
has "$T/compose-share" "ERIPHE_ROOT: /srv/eriphe/node" "the node's data, for docker exec eriphe-node eriphe-node share"
# install.sh's engine: folders from the node's data and the shared places
# only, a :ro place read-only only; the node with the engine's URL alone.
installer 'parse_args --share docs=/srv/docs --share media=/srv/media:ro; DATA=/srv/eriphe MODE= NODE_IMAGE=n WORKER_IMAGE=w INSTALL=linux; compose_yaml 100 1' >"$T/compose-share"
has "$T/compose-share" '"-bind-from", "/srv/eriphe/node,/srv/docs,/srv/media:ro",' "install.sh's engine, the places"
has "$T/compose-share" '"-network", "eriphe-system", "-task-network", "eriphe-tasks", "-node-image", "n"]' "install.sh's engine, its networks and the node's image"
has "$T/compose-share" "ERIPHE_ENGINE_URL: http://eriphe-engine:8792" "install.sh's node, the engine"
has "$T/compose-share" "depends_on: [engine]" "install.sh's node, after the engine"
hasnt "$T/compose-share" "DOCKER_HOST" "install.sh's node, its Docker"
[ "$(grep -c 'docker.sock:' "$T/compose-share")" = 1 ] || fail "install.sh: the socket is given to more than the engine"
installer 'ERIPHE_HOME=/Users/sam/.eriphe CONTAINER=/usr/local/bin/container; qa_lang() { echo fr; }; mac_share_sh' >"$T/mac-share"
shellcheck -s sh "$T/mac-share" || fail "the Mac's eriphe-share: shellcheck"
has "$T/mac-share" 'exec "$E/bin/eriphe-node" share -docker "$CONTAINER" "$@"' "the Mac's eriphe-share"
has "$T/mac-share" "T_MANAGE='Gérer" "the Mac's eriphe-share: in French on a Mac in French"
# --ask: the level and the copies asked with osascript (faked: Gérer, then
# Oui), then eriphe-node share add with both, the outcome as a notification.
A=$T/ask
mkdir -p "$A/home/bin" "$A/fake"
: >"$A/home/node.env"
printf '#!/bin/sh\necho "eriphe-node $*" >>"%s/calls"\necho "Shared"\n' "$A" >"$A/home/bin/eriphe-node"
cat >"$A/fake/osascript" <<'EOF'
#!/bin/sh
case "$*" in
*"choose from list"*) echo "${ASK_LEVEL:-Gérer (modifier le dossier)}" ;;
*"display dialog"*) echo "${ASK_COPY:-Oui}" ;;
*"display notification"*) echo "notified $*" >>"$ASK_DIR/calls" ;;
esac
EOF
chmod +x "$A/home/bin/eriphe-node" "$A/fake/osascript"
sed "s|^E=.*|E='$A/home'|" "$T/mac-share" >"$A/eriphe-share"
PATH="$A/fake:$PATH" ASK_DIR=$A sh "$A/eriphe-share" --ask "/Users/sam/Documents/Notes" >/dev/null 2>&1
has "$A/calls" "eriphe-node share -docker /usr/local/bin/container add --manage --copyable /Users/sam/Documents/Notes" "Quick Action: the level and copies chosen"
has "$A/calls" "notified" "Quick Action: the outcome said"
rm -f "$A/calls"
PATH="$A/fake:$PATH" ASK_DIR=$A ASK_LEVEL=Lecture ASK_COPY=Non sh "$A/eriphe-share" --ask "/Users/sam/Documents/Notes" >/dev/null 2>&1
has "$A/calls" "add --read --copyable=false /Users/sam/Documents/Notes" "Quick Action: read, no copies, said always"
rm -f "$A/calls"
PATH="$A/fake:$PATH" ASK_DIR=$A ASK_LEVEL=false sh "$A/eriphe-share" --ask "/Users/sam/Documents/Notes" >/dev/null 2>&1
[ ! -s "$A/calls" ] || fail "Quick Action: cancelled, yet shared"
# The Finder's Quick Action: on folders, in the Finder, the folders given as
# the shell script's arguments, a path with & written for XML.
installer "quick_action '$T/svc' 'Partager avec Eriphe…' \"exec '/Users/a&b/.eriphe/bin/eriphe-share' --ask \\\"\\\$@\\\"\"" >/dev/null
w="$T/svc/Partager avec Eriphe….workflow/Contents"
has "$w/Info.plist" "<string>Partager avec Eriphe…</string>" "Quick Action: its name"
has "$w/Info.plist" "<string>public.folder</string>" "Quick Action: on folders"
has "$w/document.wflow" "<string>exec '/Users/a&amp;b/.eriphe/bin/eriphe-share' --ask \"\$@\"</string>" "Quick Action: its command"
tr -d '\n\t' <"$w/document.wflow" | grep -q '<key>inputMethod</key><integer>1</integer>' || fail "Quick Action: the folders as arguments"
if command -v plutil >/dev/null 2>&1; then
	plutil -lint "$w/Info.plist" "$w/document.wflow" >/dev/null || fail "Quick Action: not a property list"
fi
# Installed: one Quick Action, the former two gone (the Services menu's
# refresh, pbs, left out).
mkdir -p "$T/qahome/Library/Services/Share with Eriphe (read + results).workflow" "$T/qahome/Library/Services/Partager avec Eriphe.workflow"
installer "HOME='$T/qahome' ERIPHE_HOME=/Users/sam/.eriphe; qa_lang() { echo en; }; mac_quick_actions_uninstall; quick_action \"\$HOME/Library/Services\" \"\$(qa_name)\" x" >/dev/null
[ -d "$T/qahome/Library/Services/Share with Eriphe….workflow" ] || fail "Quick Action: not installed"
[ "$(find "$T/qahome/Library/Services" -maxdepth 1 -name '*.workflow' | wc -l | tr -d ' ')" = 1 ] || fail "Quick Action: the former ones are left"

# ---- a Mac's reinstall over a running node: never left stopped

# A Mac with Apple's container, faked: launchctl loads and unloads the
# LaunchAgents (the node, once loaded, says it is connected), sudo can do
# nothing without a terminal, container pulls.
M=$T/mac
mkdir -p "$M/fake"
cat >"$M/fake/launchctl" <<'EOF'
#!/bin/sh
echo "launchctl $*" >>"$M/calls"
case $1 in
bootstrap)
	l=$(basename "$3" .plist)
	[ "$l" != "${FAIL_LABEL:-}" ] || exit 5
	touch "$M/loaded.$l"
	[ "$l" != org.eriphe.node ] || echo "connected to eriphe-core" >>"$M/home/.eriphe/node.log"
	;;
bootout) rm -f "$M/loaded.${2##*/}" ;;
print) [ -f "$M/loaded.${2##*/}" ] ;;
kickstart)
	[ -f "$M/loaded.${3##*/}" ] || exit 1
	[ "${3##*/}" != org.eriphe.node ] || echo "connected to eriphe-core" >>"$M/home/.eriphe/node.log"
	;;
esac
EOF
printf '#!/bin/sh\necho "sudo $*" >>"$M/calls"\necho "sudo: a terminal is required to read the password" >&2\nexit 1\n' >"$M/fake/sudo"
printf '#!/bin/sh\necho "container $*" >>"$M/calls"\n' >"$M/fake/container"
chmod +x "$M/fake/launchctl" "$M/fake/sudo" "$M/fake/container"
export M
E=$M/home/.eriphe
# mac_reset: an installed node, 0.54.0, running, its relay loaded with an
# older binary than the one coming.
mac_reset() {
	rm -rf "${M:?}/home" "${M:?}/calls" "${M:?}"/loaded.*
	mkdir -p "$E/bin" "$E/node" "$E/update" "$M/home/Library/LaunchAgents"
	echo old-node >"$E/bin/eriphe-node"
	echo 0.54.0 >"$E/bin/eriphe-node.version"
	echo OLD=1 >"$E/node.env"
	echo old-start >"$E/start-node.sh"
	echo '{"name": "mac"}' >"$E/node/node.json"
	echo old-plist >"$M/home/Library/LaunchAgents/org.eriphe.node.plist"
	touch "$M/loaded.org.eriphe.node" "$M/loaded.org.eriphe.update"
	echo new-node >"$M/new-node"
	echo old-node >"$M/relay-bin"
	: >"$E/node.log"
}
# mac_up OVERRIDES: native_up there, with OVERRIDES (relay_loaded, root_ready).
mac_up() {
	PATH="$M/fake:$PATH" HOME="$M/home" ERIPHE_NODE_BINARY="$M/new-node" sh -c "ERIPHE_INSTALL_LIB=1; . '$here/install.sh'
		OS=Darwin ERIPHE_HOME=\$HOME/.eriphe CONTAINER='$M/fake/container' MODE=node NAME=mac PIN= API_PORT=8800 JOIN_TOKEN=
		CORE_IMAGE=c NODE_IMAGE=ghcr.io/alexis-marcel/eriphe-node:latest WORKER_IMAGE=ghcr.io/alexis-marcel/eriphe-base:latest
		RELAY_BIN='$M/relay-bin' RELAY_PLIST='$M/relay.plist'
		native_env() { echo NEW=1; }; native_conf() { echo mode=node; }; mac_quick_actions() { :; }
		syncthing_fetch() { printf '#!/bin/sh\n' >\"\$1/syncthing\"; chmod +x \"\$1/syncthing\"; }
		$1
		native_up" >"$M/out" 2>&1
}
# The relay loaded, only its binary older, no password to be had (a script
# run without a terminal): no sudo, the node updated and running, the relay
# left for later.
mac_reset
if mac_up 'relay_loaded() { true; }; root_ready() { false; }'; then :; else fail "Mac reinstall without root: $(cat "$M/out")"; fi
[ ! -f "$M/calls" ] || hasnt "$M/calls" "sudo" "Mac reinstall, the relay's binary only"
[ "$(cat "$E/bin/eriphe-node")" = new-node ] || fail "Mac reinstall: the new node is not in place"
[ -f "$M/loaded.org.eriphe.node" ] || fail "Mac reinstall: the node is not running"
[ -f "$M/loaded.org.eriphe.update" ] || fail "Mac reinstall: the updater is not running"
{ [ ! -d "$E/update/lock" ] && [ ! -d "$E/update/install-backup" ]; } || fail "Mac reinstall leaves its lock or its backup"
has "$M/out" "updater=1" "Mac reinstall"
has "$M/out" "relay_kept=1" "Mac reinstall, the relay left for later"
# Syncthing: its own LaunchAgent, running the binary itself (not the node's
# child, not a shell), its first configuration written with a key of its
# own, never in the LaunchAgent.
[ -f "$M/loaded.org.eriphe.syncthing" ] || fail "Mac reinstall: Syncthing is not running"
sp=$M/home/Library/LaunchAgents/org.eriphe.syncthing.plist
has "$sp" "<string>$E/syncthing/syncthing</string>" "Syncthing's LaunchAgent runs its binary"
has "$sp" "<key>STGUIADDRESS</key><string>127.0.0.1:18384</string>" "Syncthing's API on the loopback"
has "$sp" "<key>STNOUPGRADE</key><string>1</string>" "Syncthing never upgrades itself"
hasnt "$sp" "apikey" "Syncthing's LaunchAgent"
hasnt "$sp" "/bin/sh" "Syncthing's LaunchAgent"
has "$E/syncthing/home/config.xml" "<address>127.0.0.1:18384</address>" "Syncthing's first configuration"
[ "$(cat "$E/syncthing/version")" = 2.1.7 ] || fail "Syncthing's version is not kept"
[ "$(stat -c %a "$E/syncthing/home/config.xml" 2>/dev/null || stat -f %Lp "$E/syncthing/home/config.xml")" = 600 ] || fail "Syncthing's config.xml is readable by others"
key=$(sed -n 's|.*<apikey>\(.*\)</apikey>.*|\1|p' "$E/syncthing/home/config.xml")
[ "${#key}" -ge 32 ] || fail "Syncthing's API key: $key"
# Run again: the same configuration, its key kept.
mac_up 'relay_loaded() { true; }; root_ready() { false; }' || fail "Mac reinstall, again: $(cat "$M/out")"
has "$E/syncthing/home/config.xml" "<apikey>$key</apikey>" "Syncthing's key, kept"
# Root can be asked but is not given: the previous node back, running.
mac_reset
if mac_up 'relay_loaded() { true; }; root_ready() { true; }'; then fail "Mac reinstall: sudo refused, and it went on"; fi
has "$M/calls" "sudo -v" "Mac reinstall, root asked"
[ "$(cat "$E/bin/eriphe-node")" = old-node ] || fail "Mac reinstall failed: the previous node is not back"
{ [ "$(cat "$E/node.env")" = OLD=1 ] && [ "$(cat "$E/start-node.sh")" = old-start ]; } || fail "Mac reinstall failed: the previous settings are not back"
[ "$(cat "$M/home/Library/LaunchAgents/org.eriphe.node.plist")" = old-plist ] || fail "Mac reinstall failed: the previous LaunchAgent is not back"
[ -f "$M/loaded.org.eriphe.node" ] || fail "Mac reinstall failed: the node is left stopped"
[ ! -d "$E/update/lock" ] || fail "Mac reinstall failed: its lock stays"
# The relay to install, no root: it stops before anything stops.
mac_reset
if mac_up 'relay_loaded() { false; }; root_ready() { false; }'; then fail "Mac reinstall: no relay, no root, and it went on"; fi
hasnt "$M/calls" "launchctl bootout gui/$(id -u)/org.eriphe.node" "Mac reinstall, stopped before the node"
{ [ "$(cat "$E/bin/eriphe-node")" = old-node ] && [ "$(cat "$E/node.env")" = OLD=1 ]; } || fail "Mac reinstall stopped early: not as before"
[ -f "$M/loaded.org.eriphe.node" ] || fail "Mac reinstall stopped early: the node is stopped"
# An update under way (its updater alive, holding the lock): waited for.
mac_reset
mkdir "$E/update/lock"
sleep 3 &
holder=$!
echo "$holder" >"$E/update/lock/pid"
mac_up 'relay_loaded() { true; }; root_ready() { false; }' || fail "Mac reinstall after an update: $(cat "$M/out")"
has "$M/out" "Waiting for the update under way to end" "Mac reinstall during an update"
wait 2>/dev/null || true

# ---- a Mac's layout, from its updater (install.sh --layout)

# A Mac from before Syncthing, its relay to install again (a new plist),
# no password to be had: Syncthing added and running, the node restarted on
# the binaries the updater put in place (none fetched), the relay left as
# it is and said in update-needs, the updater's lock and LaunchAgent left
# to the updater that runs this.
mac_reset
mkdir "$E/update/lock"
echo $$ >"$E/update/lock/pid"
if mac_up 'LAYOUT_RUN=1 LOCK_HELD=1 NEEDS=; relay_loaded() { false; }; root_ready() { false; }'; then :; else
	fail "Mac layout: $(cat "$M/out")"
fi
[ "$(cat "$E/bin/eriphe-node")" = old-node ] || fail "Mac layout: the binaries in place are replaced"
[ -f "$M/loaded.org.eriphe.syncthing" ] || fail "Mac layout: Syncthing is not running"
[ -f "$M/loaded.org.eriphe.node" ] || fail "Mac layout: the node is not running"
hasnt "$M/calls" "sudo" "Mac layout, no password"
hasnt "$M/calls" "bootout gui/$(id -u)/org.eriphe.update" "Mac layout, the updater running it"
[ -f "$M/loaded.org.eriphe.update" ] || fail "Mac layout: the updater is stopped"
[ -d "$E/update/lock" ] || fail "Mac layout: the updater's lock is taken from it"
has "$E/node/update-needs" "why=password" "Mac layout, the relay for its user"
has "$M/out" "needs=password" "Mac layout"
layout=$(sed -n 's/^LAYOUT=\([0-9]*\)$/\1/p' "$here/install.sh")
has "$E/update.conf" "LAYOUT=$layout" "Mac layout, its number"
has "$E/node/updater" "layout=$layout" "Mac layout, told to the node"
has "$M/home/Library/LaunchAgents/org.eriphe.update.plist" "<string>$E/update.sh</string>" "Mac layout, the updater's LaunchAgent"
# A step that fails (Syncthing does not start): what ran before is back,
# the Syncthing it brought gone, the node running, the lock still the
# updater's.
mac_reset
mkdir "$E/update/lock"
echo $$ >"$E/update/lock/pid"
export FAIL_LABEL=org.eriphe.syncthing
if mac_up 'LAYOUT_RUN=1 LOCK_HELD=1 NEEDS=; relay_loaded() { true; }; root_ready() { false; }'; then
	fail "Mac layout: a failed step goes on"
fi
{ [ "$(cat "$E/node.env")" = OLD=1 ] && [ "$(cat "$E/start-node.sh")" = old-start ]; } || fail "Mac layout failed: the previous settings are not back"
[ ! -f "$M/home/Library/LaunchAgents/org.eriphe.syncthing.plist" ] || fail "Mac layout failed: the Syncthing it brought stays"
[ -f "$M/loaded.org.eriphe.node" ] || fail "Mac layout failed: the node is left stopped"
[ -d "$E/update/lock" ] || fail "Mac layout failed: the updater's lock is taken from it"
[ ! -f "$E/node/update-needs" ] || fail "Mac layout failed: needs written"
unset FAIL_LABEL
rm -rf "${E:?}/update/lock"
# A full install leaves nothing waiting for the user.
mac_reset
printf 'why=password\n' >"$E/node/update-needs"
mac_up 'relay_loaded() { true; }; root_ready() { false; }' || fail "Mac reinstall, needs: $(cat "$M/out")"
[ ! -f "$E/node/update-needs" ] || fail "Mac reinstall: the needs stay"

# ---- the Mac's updater: its lock, and a request left "running"

mac_reset
PATH="$M/fake:$PATH" HOME="$M/home" sh -c "ERIPHE_INSTALL_LIB=1; . '$here/install.sh'
	ERIPHE_HOME=\$HOME/.eriphe CONTAINER='$M/fake/container' MODE=node PIN= API_PORT=8800
	CORE_IMAGE=c NODE_IMAGE=n WORKER_IMAGE=w; mac_updater_conf >\$ERIPHE_HOME/update.conf; mac_updater_sh >\$ERIPHE_HOME/update.sh"
printf 'id=u-1\naction=update\noutcome=running\nmessage=\nfrom=0.55.0\nto=\nat=1\n' >"$E/node/update-status"
mkdir -p "$E/update/lock" "$E/update/fetch.abc"
echo 999999 >"$E/update/lock/pid" # its updater is gone
PATH="$M/fake:$PATH" HOME="$M/home" sh "$E/update.sh" 2>/dev/null
has "$E/node/update-status" "outcome=interrupted" "Mac updater: a running status of a dead updater"
has "$E/node/update-status" "from=0.55.0" "Mac updater: the interrupted status keeps its versions"
{ [ ! -d "$E/update/fetch.abc" ] && [ ! -d "$E/update/lock" ]; } || fail "Mac updater: leftovers of the dead one stay"
# A live holder: it leaves everything as it is.
printf 'id=u-2\naction=update\noutcome=running\n' >"$E/node/update-status"
mkdir "$E/update/lock"
echo $$ >"$E/update/lock/pid"
PATH="$M/fake:$PATH" HOME="$M/home" sh "$E/update.sh" 2>/dev/null
has "$E/node/update-status" "outcome=running" "Mac updater: another one's running update"
rm -rf "${E:?}/update/lock"

# ---- the Mac's updater: a newer layout, by the node image's install.sh

# The install.sh of the node's image, faked: its layout, what it was run
# with, and the number it leaves (LAYOUT_FAIL: a step fails).
cat >"$M/installer" <<'EOF'
#!/bin/sh
LAYOUT=2
echo "installer $* home=$HOME image=$ERIPHE_NODE_IMAGE" >>"$M/calls"
[ -z "${LAYOUT_FAIL:-}" ] || { echo "eriphe: Syncthing does not start" >&2; exit 1; }
sed 's/^LAYOUT=.*/LAYOUT=2/' "$HOME/.eriphe/update.conf" >"$HOME/.eriphe/update.conf.new" && mv "$HOME/.eriphe/update.conf.new" "$HOME/.eriphe/update.conf"
EOF
echo old-node >"$M/same-node"
# mac_updater: the updater of a Mac at layout 1 (from before Syncthing).
mac_updater() {
	mac_reset
	PATH="$M/fake:$PATH" HOME="$M/home" sh -c "ERIPHE_INSTALL_LIB=1; . '$here/install.sh'
		ERIPHE_HOME=\$HOME/.eriphe CONTAINER='$M/fake/container' MODE=node PIN= API_PORT=8800 LAYOUT_DONE=1
		CORE_IMAGE=c NODE_IMAGE=n WORKER_IMAGE=w; mac_updater_conf >\$ERIPHE_HOME/update.conf; mac_updater_sh >\$ERIPHE_HOME/update.sh"
}
# mac_update ID BINARY: eriphe-core's request, the node image's binary BINARY.
mac_update() {
	printf 'id=%s\naction=update\ncore=\nauto=0\n' "$1" >"$E/node/update-requested"
	PATH="$M/fake:$PATH" HOME="$M/home" ERIPHE_UPDATE_HEALTH=4 ERIPHE_NODE_BINARY="$2" ERIPHE_NODE_INSTALLER="$M/installer" \
		sh "$E/update.sh" 2>/dev/null
}
# A new build that brings a layout: its binary, then its install.sh --layout.
mac_updater
has "$E/update.conf" "LAYOUT=1" "Mac updater at layout 1"
mac_update u-10 "$M/new-node"
has "$E/node/update-status" "outcome=ok" "Mac updater, a new layout"
has "$M/calls" "installer --layout home=$M/home image=n" "Mac updater runs the image's install.sh"
[ "$(cat "$E/bin/eriphe-node")" = new-node ] || fail "Mac updater, a new layout: the new node is not in place"
# Again, nothing newer: the layout is not applied twice.
: >"$M/calls"
mac_update u-11 "$M/new-node"
has "$E/node/update-status" "outcome=current" "Mac updater, at its layout"
hasnt "$M/calls" "installer" "Mac updater, at its layout"
# The same build on an older layout: the layout only.
mac_updater
mac_update u-12 "$M/same-node"
has "$E/node/update-status" "outcome=ok" "Mac updater, the layout only"
has "$E/node/update-status" "message=the install's layout is now its version's" "Mac updater, the layout only"
# The layout fails: the whole update goes back, the node running.
mac_updater
export LAYOUT_FAIL=1
mac_update u-13 "$M/new-node"
unset LAYOUT_FAIL
has "$E/node/update-status" "outcome=rolled_back" "Mac updater, a failed layout"
has "$E/node/update-status" "the new install layout did not apply: Syncthing does not start" "Mac updater, a failed layout"
[ "$(cat "$E/bin/eriphe-node")" = old-node ] || fail "Mac updater, a failed layout: the previous node is not back"
[ -f "$M/loaded.org.eriphe.node" ] || fail "Mac updater, a failed layout: the node is stopped"

# ---- the Linux updater: a request left "running", and the same build again

L=$T/linux
mkdir -p "$L/fake" "$L/conf" "$L/data/node" "$L/data/core"
installer "CONF='$L/conf' DATA='$L/data' CORE_UID=$(id -u) MODE=node PIN= CORE_IMAGE=c NODE_IMAGE=r/eriphe-node:latest WORKER_IMAGE=r/eriphe-base:latest; updater_conf >'$L/conf/update.conf'; updater_sh >'$L/conf/update.sh'" >/dev/null
# A fake docker: the node runs image sha256:a, the same build (0.54.0) as
# sha256:b, which the pull brings.
cat >"$L/fake/docker" <<'EOF'
#!/bin/sh
echo "docker $*" >>"$L/calls"
case $* in
"inspect -f {{.Image}} eriphe-node") echo sha256:a ;;
"inspect -f {{.Image}} eriphe-engine") [ -n "${ENGINE:-}" ] && echo sha256:b ;;
"inspect -f {{.Image}} "*) exit 1 ;;
"inspect -f {{.Config.Image}} eriphe-engine") [ -n "${ENGINE:-}" ] && echo r/eriphe-node:latest ;;
"image inspect -f {{.Id}} r/eriphe-node:latest") if [ -f "$L/pulled" ]; then echo sha256:b; else echo sha256:a; fi ;;
"image inspect -f {{.Id}} "*) echo sha256:w ;;
"image inspect -f "*sha256:b) echo "${NEWV:-0.54.0}" ;;
"image inspect -f "*) echo 0.54.0 ;;
"pull -q "*) touch "$L/pulled" ;;
esac
EOF
printf '#!/bin/sh\nexit 0\n' >"$L/fake/flock"
chmod +x "$L/fake/docker" "$L/fake/flock"
export L
printf 'id=u-3\naction=update\noutcome=running\nfrom=0.53.0\nto=0.54.0\n' >"$L/data/node/update-status"
printf 'id=u-4\naction=update\ncore=0.54.0\nauto=0\n' >"$L/data/node/update-requested"
PATH="$L/fake:$PATH" sh "$L/conf/update.sh" 2>/dev/null
has "$L/data/update/update.log" "node update u-3: interrupted" "Linux updater: a running status of a dead updater"
has "$L/data/node/update-status" "id=u-4" "Linux updater: the request after"
has "$L/data/node/update-status" "outcome=current" "Linux updater: the same build is current"
hasnt "$L/calls" "compose" "Linux updater: the same build recreated"
has "$L/calls" "tag sha256:a r/eriphe-node:latest" "Linux updater: the running image back under its reference"
# A new build: recreated (then, the fake not running it, put back).
rm -f "$L/pulled" "$L/calls"
printf 'id=u-5\naction=update\ncore=0.55.0\nauto=0\n' >"$L/data/node/update-requested"
NEWV=0.55.0 PATH="$L/fake:$PATH" ERIPHE_UPDATE_HEALTH=2 sh "$L/conf/update.sh" 2>/dev/null
has "$L/calls" "compose -p eriphe" "Linux updater: a new build not recreated"

# The updater recreates Syncthing with the node: it lives in the node's
# network namespace.
printf 'services:\n  node:\n  syncthing:\n' >"$L/conf/compose.yaml"
rm -f "$L/pulled" "$L/calls"
printf 'id=u-6\naction=update\ncore=0.55.0\nauto=0\n' >"$L/data/node/update-requested"
NEWV=0.55.0 PATH="$L/fake:$PATH" ERIPHE_UPDATE_HEALTH=2 sh "$L/conf/update.sh" 2>/dev/null
has "$L/calls" "up -d --pull never --no-deps --force-recreate syncthing" "Linux updater: Syncthing again with the node"

# The engine is the node's image: made again first, on the node's version.
rm -f "$L/pulled" "$L/calls"
printf 'id=u-7\naction=update\ncore=0.55.0\nauto=0\n' >"$L/data/node/update-requested"
ENGINE=1 NEWV=0.55.0 PATH="$L/fake:$PATH" ERIPHE_UPDATE_HEALTH=2 sh "$L/conf/update.sh" 2>/dev/null
has "$L/calls" "up -d --pull never --no-deps engine" "Linux updater: the engine on the node's version"

# ---- the Linux updater: a newer layout, by the node image's install.sh

# A machine at layout 1 (from before Syncthing), and a fake docker that
# keeps which image is under the node's reference (tag) and which one runs
# (running): the pull brings PULLED, compose's up runs what is tagged.
# sha256:b is 0.55.0, the others 0.54.0. The image's install.sh is
# $LL/installer, faked: it says its layout, writes Syncthing into the
# compose project, and fails with LAYOUT_FAIL. The machine's own paths
# (units, eriphe-share) are under $LL/root.
LL=$T/layout
mkdir -p "$LL/fake" "$LL/conf" "$LL/data/node" "$LL/root"
export LL
cat >"$LL/fake/docker" <<'EOF'
#!/bin/sh
echo "docker $*" >>"$LL/calls"
tag=$(cat "$LL/tag" 2>/dev/null || echo sha256:a)
case $* in
"inspect -f {{.Image}} eriphe-node") cat "$LL/running" ;;
"inspect -f {{.State.Status}} {{.RestartCount}} "*) echo "running 0" ;;
"inspect "*) exit 1 ;;
"image inspect -f {{.Id}} r/eriphe-node:latest") echo "$tag" ;;
"image inspect -f {{.Id}} "*) echo sha256:w ;;
"image inspect -f "*sha256:b) echo 0.55.0 ;;
"image inspect -f "*) echo 0.54.0 ;;
"pull -q r/eriphe-node:latest") echo "${PULLED:-sha256:a}" >"$LL/tag" ;;
"tag sha256:"*" r/eriphe-node:latest") echo "$2" >"$LL/tag" ;;
"create r/eriphe-node:latest") echo c1 ;;
"cp c1:/usr/local/share/eriphe/install.sh "*) [ ! -f "$LL/installer" ] || cp "$LL/installer" "$3" ;;
"logs --since "*) echo "connected to eriphe-core" ;;
*" up -d --pull never --no-deps node") cp "$LL/tag" "$LL/running" ;;
esac
exit 0
EOF
cat >"$LL/installer" <<'EOF'
#!/bin/sh
LAYOUT=2
echo "installer $* image=$ERIPHE_NODE_IMAGE" >>"$LL/calls"
printf 'services:\n  node:\n  syncthing:\n' >"$LL/conf/compose.yaml"
mkdir -p "$LL/root/etc/systemd/system"
echo new-unit >"$LL/root/etc/systemd/system/eriphe-awake.path"
[ -z "${LAYOUT_FAIL:-}" ] || { echo "eriphe: could not download Eriphe" >&2; exit 1; }
sed 's/^LAYOUT=.*/LAYOUT=2 BY=systemd/' "$LL/conf/update.conf" >"$LL/conf/update.conf.new" && mv "$LL/conf/update.conf.new" "$LL/conf/update.conf"
EOF
printf '#!/bin/sh\nexit 0\n' >"$LL/fake/flock"
chmod +x "$LL/fake/docker" "$LL/fake/flock"
# linux_layout [UPDATE_BY [INSTALL]]: the machine of before, its node running sha256:a.
linux_layout() {
	rm -rf "${LL:?}/calls" "${LL:?}/tag" "${LL:?}/root" "${LL:?}/data/update"
	echo sha256:a >"$LL/running"
	printf 'services:\n  node:\n' >"$LL/conf/compose.yaml"
	installer "CONF='$LL/conf' DATA='$LL/data' CORE_UID=$(id -u) MODE=node PIN= LAYOUT=1 UPDATE_BY=${1:-systemd} INSTALL=${2:-linux}
		CORE_IMAGE=c NODE_IMAGE=r/eriphe-node:latest WORKER_IMAGE=r/eriphe-base:latest
		updater_conf >'$LL/conf/update.conf'; updater_sh >'$LL/conf/update.sh'" >/dev/null
}
# linux_update ID [PULLED [LAYOUT_FAIL]]: eriphe-core's request, then the updater.
linux_update() {
	printf 'id=%s\naction=update\ncore=\nauto=0\n' "$1" >"$LL/data/node/update-requested"
	PULLED=${2:-} LAYOUT_FAIL=${3:-} PATH="$LL/fake:$PATH" ERIPHE_UPDATE_HEALTH=4 ERIPHE_UPDATE_ROOT="$LL/root" sh "$LL/conf/update.sh" 2>/dev/null
}
# A new build that brings a layout: its images, then its install.sh
# --layout, with the updater's images: Syncthing added.
linux_layout
has "$LL/conf/update.conf" "LAYOUT=1" "Linux updater at layout 1"
linux_update u-20 sha256:b
has "$LL/data/node/update-status" "outcome=ok" "Linux updater, a new layout"
has "$LL/data/node/update-status" "to=0.55.0" "Linux updater, a new layout"
has "$LL/calls" "installer --layout image=r/eriphe-node:latest" "Linux updater runs the image's install.sh"
has "$LL/conf/compose.yaml" "syncthing:" "Linux updater, a new layout: Syncthing"
has "$LL/conf/update.conf" "LAYOUT=2" "Linux updater, a new layout: its number"
# Again: nothing newer, nothing applied twice.
: >"$LL/calls"
linux_update u-21 sha256:b
has "$LL/data/node/update-status" "outcome=current" "Linux updater, at its layout"
hasnt "$LL/calls" "installer" "Linux updater, at its layout"
# The same build on an older layout: the layout only.
linux_layout
linux_update u-22
has "$LL/data/node/update-status" "outcome=ok" "Linux updater, the layout only"
has "$LL/data/node/update-status" "message=the install's layout is now its version's" "Linux updater, the layout only"
has "$LL/conf/compose.yaml" "syncthing:" "Linux updater, the layout only: Syncthing"
# The layout fails: what it wrote goes (the unit it added too), the
# services it added go with them, the previous images run again.
linux_layout
linux_update u-23 sha256:b 1
has "$LL/data/node/update-status" "outcome=rolled_back" "Linux updater, a failed layout"
has "$LL/data/node/update-status" "message=the new install layout did not apply: could not download Eriphe" "Linux updater, a failed layout"
hasnt "$LL/conf/compose.yaml" "syncthing" "Linux updater, a failed layout: the compose project of before"
has "$LL/conf/update.conf" "LAYOUT=1" "Linux updater, a failed layout: the layout of before"
[ ! -f "$LL/root/etc/systemd/system/eriphe-awake.path" ] || fail "Linux updater, a failed layout: a unit it added stays"
has "$LL/calls" "up -d --pull never --remove-orphans" "Linux updater, a failed layout: the services it added"
[ "$(cat "$LL/running")" = sha256:a ] || fail "Linux updater, a failed layout: the previous node does not run"
has "$LL/data/update/failed" "sha256:b" "Linux updater, a failed layout: the version noted as failed"
# eriphe-launcher's updater never runs one: the launcher brings its layout
# as it updates itself.
linux_layout loop
linux_update u-24 sha256:b
has "$LL/data/node/update-status" "outcome=ok" "Linux updater under eriphe-launcher"
hasnt "$LL/calls" "create r/eriphe-node" "Linux updater under eriphe-launcher"
hasnt "$LL/calls" "installer" "Linux updater under eriphe-launcher"
# ---- a layout-2 Linux install (the socket proxy) updated to this build

# Its own updater, as install.sh wrote it then (testdata), runs this
# build's images: the proxy's container and the node again from the new
# node image first (it has no proxy any more, and the node starts without
# Docker: node/engine_remote.go, withoutEngine), the node connected, then
# the image's install.sh --layout, which brings the engine. The image's
# install.sh is faked past its LAYOUT line, this one's.
LP=$T/layout2
mkdir -p "$LP/fake" "$LP/conf" "$LP/data/node"
export LP
cp "$here/testdata/linux-update-layout2.sh" "$LP/conf/update.sh"
printf "CONF='%s' DATA='%s' CORE_UID=%s MODE='' PIN=''\nCORE_IMAGE='c' NODE_IMAGE='r/eriphe-node:latest' WORKER_IMAGE='r/eriphe-base:latest'\nLAYOUT=2 BY='systemd' INSTALL='linux'\n" \
	"$LP/conf" "$LP/data" "$(id -u)" >"$LP/conf/update.conf"
cat >"$LP/conf/compose.yaml" <<'EOF'
services:
  docker-proxy:
    container_name: eriphe-docker-proxy
    command: ["docker-proxy", "-listen", ":2375"]
  node:
    environment:
      DOCKER_HOST: tcp://eriphe-docker-proxy:2375
EOF
cat >"$LP/fake/docker" <<'EOF'
#!/bin/sh
echo "docker $*" >>"$LP/calls"
tag=$(cat "$LP/tag" 2>/dev/null || echo sha256:a)
case $* in
"inspect -f {{.Image}} eriphe-node") cat "$LP/running" ;;
"inspect -f {{.Image}} eriphe-docker-proxy") cat "$LP/proxy" ;;
"inspect -f {{.Config.Image}} eriphe-docker-proxy") echo r/eriphe-node:latest ;;
"inspect -f {{.State.Status}} {{.RestartCount}} "*) echo "running 0" ;;
"inspect "*) exit 1 ;;
"image inspect -f {{.Id}} r/eriphe-node:latest") echo "$tag" ;;
"image inspect -f {{.Id}} "*) echo sha256:w ;;
"image inspect -f "*sha256:b) echo 0.70.0 ;;
"image inspect -f "*) echo 0.66.0 ;;
"pull -q r/eriphe-node:latest") echo sha256:b >"$LP/tag" ;;
"tag sha256:"*" r/eriphe-node:latest") echo "$2" >"$LP/tag" ;;
"create r/eriphe-node:latest") echo c1 ;;
"cp c1:/usr/local/share/eriphe/install.sh "*) cp "$LP/installer" "$3" ;;
"logs --since "*) [ "$(cat "$LP/running")" = sha256:b ] && echo "no container engine here: connected all the same" && echo "connected to eriphe-core" ;;
*" up -d --pull never --no-deps node") cp "$LP/tag" "$LP/running" ;;
*" up -d --pull never --no-deps docker-proxy") cp "$LP/tag" "$LP/proxy" ;;
esac
exit 0
EOF
{
	grep '^LAYOUT=' "$here/install.sh"
	cat <<'EOF'
echo "installer $* image=$ERIPHE_NODE_IMAGE" >>"$LP/calls"
printf 'services:\n  engine:\n  node:\n    environment:\n      ERIPHE_ENGINE_URL: http://eriphe-engine:8792\n' >"$LP/conf/compose.yaml"
sed 's/^LAYOUT=.*/LAYOUT=3 BY=systemd/' "$LP/conf/update.conf" >"$LP/conf/update.conf.new" && mv "$LP/conf/update.conf.new" "$LP/conf/update.conf"
EOF
} >"$LP/installer"
printf '#!/bin/sh\nexit 0\n' >"$LP/fake/flock"
chmod +x "$LP/fake/docker" "$LP/fake/flock"
echo sha256:a >"$LP/running"
echo sha256:a >"$LP/proxy"
printf 'id=u-30\naction=update\ncore=\nauto=0\n' >"$LP/data/node/update-requested"
PATH="$LP/fake:$PATH" ERIPHE_UPDATE_HEALTH=4 ERIPHE_UPDATE_ROOT="$LP/root" sh "$LP/conf/update.sh" 2>/dev/null
has "$LP/data/node/update-status" "outcome=ok" "layout-2 update"
has "$LP/data/node/update-status" "to=0.70.0" "layout-2 update"
[ "$(cat "$LP/proxy")" = sha256:b ] || fail "layout-2 update: the proxy's container not on the new image"
has "$LP/calls" "installer --layout image=r/eriphe-node:latest" "layout-2 update: this build's layout"
has "$LP/conf/compose.yaml" "ERIPHE_ENGINE_URL: http://eriphe-engine:8792" "layout-2 update: the engine"
hasnt "$LP/conf/compose.yaml" "docker-proxy" "layout-2 update: the proxy"
has "$LP/conf/update.conf" "LAYOUT=3" "layout-2 update: its layout"

# ---- the Windows updater: the layout it takes from the node image's install.ps1

# The updater of an install from before the launcher (testdata), its own
# lines that read install.ps1 (the layout's numbers, the setup of the
# distribution, Syncthing's build, gone with the launcher), run on this
# install.ps1: what the distribution's updater would apply to move to it.
W=$T/win
mkdir -p "$W/state"
cp "$here/install.ps1" "$W/state/install.ps1"
cp "$here/testdata/wsl-update-layout3.sh" "$W/update"
{
	echo 'log() { :; }; uname() { echo x86_64; }'
	sed -n '/^ps_number()/p' "$W/update"
	sed -n '/^layout() {/,/^  if ! grep -q /p' "$W/update" | sed '1d; $d'
	echo 'echo "n=$n admin=$admin v=$v st=$st"'
} >"$W/read.sh"
out=$(STATE=$W/state sh "$W/read.sh" 2>&1)
[ "$out" = "n=$layout admin=1 v= st=" ] || fail "Windows updater, install.ps1's layout: $out"
has "$W/state/layout-setup.sh" "cat > /usr/local/bin/eriphe-node-run <<'EOF'" "Windows updater, the distribution's setup"
has "$W/state/layout-setup.sh" "docker run -d --name eriphe --restart unless-stopped" "Windows updater, the setup starts the launcher"
sh -n "$W/state/layout-setup.sh" || fail "Windows updater: the setup it takes does not parse"
[ ! -s "$W/state/layout-syncthing.sh" ] || fail "Windows updater: a Syncthing of the distribution to set up"

# install.sh --layout is the updaters' alone: no other option.
out=$(installer 'parse_args --layout --core-mode; layout_run')
case $out in *"--layout takes no other option"*) ;; *) fail "--layout with an option: $out" ;; esac
# In a Mac's VM (Lima) the layout is the Mac's install's: updated all the
# same, the step left for the user, in update-needs.
rm -f "$LL/data/node/update-needs"
linux_layout systemd macos
linux_update u-25 sha256:b
has "$LL/data/node/update-status" "outcome=ok" "Linux updater in a Mac's VM"
hasnt "$LL/calls" "installer" "Linux updater in a Mac's VM"
has "$LL/data/node/update-needs" "why=layout" "Linux updater in a Mac's VM"

# ---- Syncthing

# Its first configuration: the node's own test reads the same file
# (node/testdata/syncthing-config.xml), with the key in place of @KEY@.
installer 'syncthing_config @KEY@' >"$T/st-config.xml"
cmp -s "$T/st-config.xml" "$here/../node/testdata/syncthing-config.xml" ||
	fail "syncthing_config differs from node/testdata/syncthing-config.xml: $(diff "$T/st-config.xml" "$here/../node/testdata/syncthing-config.xml")"
k1=$(installer syncthing_key) k2=$(installer syncthing_key)
{ [ "${#k1}" = 32 ] && [ "$k1" != "$k2" ]; } || fail "syncthing_key: $k1 $k2"

# The compose project: the pinned image, in the node's network namespace,
# its API on the loopback, the places and the copies at their paths; the
# node reads its home, read-only.
installer 'DATA=/srv/eriphe MODE=node NODE_IMAGE=n WORKER_IMAGE=w INSTALL=linux SHARES=docs=/srv/docs,media=/srv/media:ro; compose_yaml 100 1' >"$T/compose-st"
for want in "image: syncthing/syncthing:2.1.7@sha256:2b71da7c0d0a91a6ae5285ad079226fecc0a365d08b27720e1f7037b018f7546" \
	'network_mode: "service:node"' "STGUIADDRESS: 127.0.0.1:18384" 'STNOUPGRADE: "1"' 'entrypoint: ["/bin/syncthing"]' \
	"STHOMEDIR: /srv/eriphe/syncthing" "- /srv/eriphe/syncthing:/srv/eriphe/syncthing" "- /srv/eriphe/node/sync:/srv/eriphe/node/sync" \
	"- /srv/eriphe/syncthing:/srv/eriphe/syncthing:ro" "ERIPHE_SYNCTHING_HOME: /srv/eriphe/syncthing" \
	'- "/srv/docs:/srv/docs"' '- "/srv/media:/srv/media:ro"'; do
	has "$T/compose-st" "$want" "compose, Syncthing"
done
hasnt "$T/compose-st" "8384:" "compose, Syncthing (a published port)"
hasnt "$T/compose-st" "22000" "compose, Syncthing (a published port)"

# Its signature on a Mac: valid, and Kastelo AB's Team ID, or refused.
S=$T/st
mkdir -p "$S/fake" "$S/dl"
cat >"$S/fake/codesign" <<'EOF'
#!/bin/sh
case $1 in
--verify) case $* in *'leaf[subject.OU] = "LQE5SYM783"'*) [ "${ST_VALID:-1}" = 1 ] ;; *) exit 1 ;; esac ;;
-dv) printf 'Executable=%s
Identifier=syncthing
TeamIdentifier=%s
' "$2" "${ST_TEAM:-LQE5SYM783}" >&2 ;;
esac
EOF
cat >"$S/fake/curl" <<'EOF'
#!/bin/sh
while [ $# -gt 1 ]; do [ "$1" = -o ] && { echo zip >"$2"; }; shift; done
echo "$1" >>"$S/urls"
EOF
printf '#!/bin/sh
echo "${ST_SUM:-a98fb709c79412e5586c81a1586bfb256b6e5a97b067ddf7447155bcdf6aac3f}  $3"
' >"$S/fake/shasum"
printf '#!/bin/sh
mkdir -p "$4/syncthing-macos-universal-v2.1.7" && echo bin >"$4/syncthing-macos-universal-v2.1.7/syncthing"
' >"$S/fake/ditto"
chmod +x "$S/fake/"*
export S
st() { env PATH="$S/fake:$PATH" "$@" sh -c "ERIPHE_INSTALL_LIB=1; . '$here/install.sh'; rm -rf '$S/dl'; mkdir -p '$S/dl'; syncthing_fetch '$S/dl'" >/dev/null 2>&1; }
st || fail "Syncthing's build, signed and matching: refused"
[ -f "$S/dl/syncthing" ] || fail "Syncthing's build is not in place"
has "$S/urls" "https://github.com/syncthing/syncthing/releases/download/v2.1.7/syncthing-macos-universal-v2.1.7.zip" "Syncthing's download"
if st ST_SUM=0000; then fail "Syncthing: another checksum accepted"; fi
if st ST_TEAM=ABCDE12345; then fail "Syncthing: another Team ID accepted"; fi
if st ST_VALID=0; then fail "Syncthing: an invalid signature accepted"; fi
[ ! -f "$S/dl/syncthing" ] || fail "Syncthing: a refused build left in place"

if [ "$fails" -gt 0 ]; then
	echo "launcher_test.sh: $fails failed" >&2
	exit 1
fi
echo "launcher_test.sh: ok"
