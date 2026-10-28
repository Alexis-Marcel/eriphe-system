#!/bin/sh
# Installs Eriphe on Linux or macOS, bringing its own container runtime:
# nothing to install first, not even Docker.
#
#   # The first machine: eriphe-core here, with a node of its own:
#   curl -fsSL https://eriphe.com/install.sh | sh -s -- --core-mode
#   # Another machine, joining it (the workspace's "Add a machine" gives the
#   # whole command, token included):
#   curl -fsSL https://eriphe.com/install.sh | sh -s -- --core http://192.0.2.10:8800 --join-token <token> --name laptop
#   # Update (the same command, or no option at all), or remove everything:
#   curl -fsSL https://eriphe.com/install.sh | sh -s
#   curl -fsSL https://eriphe.com/install.sh | sh -s -- --uninstall
#   # Pin a version, or go back to the one before the last update:
#   curl -fsSL https://eriphe.com/install.sh | sh -s -- --version sha-1234567
#   curl -fsSL https://eriphe.com/install.sh | sh -s -- --rollback
#
# Linux: Docker Engine if it is missing, then a compose project in /opt/eriphe
# (data in /var/lib/eriphe): a socket proxy that only allows the node's Docker
# calls, the node, and eriphe-core in core mode, restarted with Docker at boot.
# macOS: Lima in ~/.eriphe, a Linux VM "eriphe" (Apple Virtualization) that
# sees no Mac folder, the same compose project inside it, and a LaunchAgent
# that starts the VM at login. On both, an updater that applies the updates
# eriphe-core asks for (a systemd unit, in the VM on macOS). See
# install/README.md.
set -eu

LIMA_VERSION=2.2.1
LIMA_SHA256_arm64=9e9eacce88f37e185c346bad73aa6136f738d8cdf8c3bb23cd42b071824bc66e
LIMA_SHA256_x86_64=6b776e4bc41f358e6ee01fefcc78e2eb26f11635a0fcff632234af77a566c6c4

REGISTRY=ghcr.io/alexis-marcel
PROXY_IMAGE=wollomatic/socket-proxy:1.13.2

# images: the image references, at the pinned version (--version) or latest.
# Fixed here, in the installation: eriphe-core never chooses them.
images() {
	tag=${PIN:-latest}
	CORE_IMAGE=${ERIPHE_CORE_IMAGE:-$REGISTRY/eriphe-core:$tag}
	NODE_IMAGE=${ERIPHE_NODE_IMAGE:-$REGISTRY/eriphe-node:$tag}
	WORKER_IMAGE=${ERIPHE_WORKER_IMAGE:-$REGISTRY/eriphe-worker:$tag}
}

# On the Linux machine Eriphe runs on: this one, or the VM on macOS.
CONF=/opt/eriphe
DATA=/var/lib/eriphe
CORE_UID=1993 # eriphe-core's user in its image

# On macOS.
VM=eriphe
AGENT_LABEL=org.eriphe.vm

say() { printf '%s\n' "$*"; }
warn() { printf 'eriphe: %s\n' "$*" >&2; }
die() {
	warn "$*"
	exit 1
}

usage() {
	cat <<'EOF'
Usage: curl -fsSL https://eriphe.com/install.sh | sh -s -- [options]
       sh install.sh [options]

  --core URL[,URL2]   eriphe-core to join, several addresses preferred first
  --join-token TOKEN  from `eriphe-core join-token -name NAME` (first install only)
  --name NAME         this node's name (default: the host name)
  --labels L1,L2      what agents can require of this node, e.g. gpu
  --ca-hash sha256:H  eriphe-core's CA, to check its certificate
  --share NAME=/PATH  a place of this machine whose folders may be given to
                      Eriphe (repeatable; NAME=/PATH:ro: never written to).
                      Replaces the places of an earlier install; Linux only
  --no-share          share no place any more
  --core-mode         run eriphe-core here, with a node of its own
  --version V         pin a version (sha-1234567), or latest to follow it again
  --rollback          go back to the version before the last update
  --uninstall         remove everything this script installed
  -h, --help          this help

Run again (with or without the options) to update: new images, same data and
same node identity. Afterwards the machine updates itself when eriphe-core
asks (Settings, Machines, Updates). "Add a machine" in the workspace gives the whole command.
Guide: https://eriphe.com/install
EOF
}

# q VALUE: VALUE quoted for sh.
q() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }

as_root() {
	if [ "$(id -u)" = 0 ]; then
		"$@"
	else
		command -v sudo >/dev/null 2>&1 || die "run this as root (no sudo here)"
		sudo "$@"
	fi
}

# on_host runs the script on its stdin as root on the Linux machine Eriphe
# runs on (a token never shows on a command line).
on_host() {
	if [ "$OS" = Darwin ]; then
		"$LIMACTL" --tty=false shell --workdir / "$VM" sudo sh -s
	else
		as_root sh -s
	fi
}

# prelude: what every host script starts with.
prelude() {
	cat <<EOF
set -eu
CONF=$(q "$CONF") DATA=$(q "$DATA") CORE_UID=$CORE_UID
MODE=$(q "${MODE:-}") NODE_NAME=$(q "${NAME:-}")
IMAGES=$(q "$PROXY_IMAGE ${NODE_IMAGE:-} ${WORKER_IMAGE:-}$([ "${MODE:-}" = core ] && printf ' %s' "${CORE_IMAGE:-}")")
ALL_REPOS=$(q "${PROXY_IMAGE%:*} $REGISTRY/eriphe-node $REGISTRY/eriphe-worker $REGISTRY/eriphe-core")
EOF
	cat <<'EOF'
die() { printf 'eriphe: %s\n' "$*" >&2; exit 1; }
dc() { docker compose -p eriphe --project-directory "$CONF" -f "$CONF/compose.yaml" --env-file "$CONF/.env" "$@"; }
EOF
}

parse_args() {
	MODE='' UNINSTALL='' LABELS_SET='' PIN='' PIN_SET='' ROLLBACK='' SHARES='' SHARES_SET=''
	CORE_URL=${ERIPHE_CORE:-}
	JOIN_TOKEN=${ERIPHE_JOIN_TOKEN:-}
	NAME=${ERIPHE_NODE_NAME:-}
	LABELS=${ERIPHE_NODE_LABELS:-}
	CA_HASH=${ERIPHE_CA_HASH:-}
	[ -n "$LABELS" ] && LABELS_SET=1
	while [ $# -gt 0 ]; do
		case $1 in
		--*=*)
			a=$1
			shift
			set -- "${a%%=*}" "${a#*=}" "$@"
			continue
			;;
		esac
		case $1 in
		--core | --join-token | --name | --labels | --ca-hash | --version)
			[ $# -ge 2 ] || die "$1 needs a value"
			case $1 in
			--core) CORE_URL=$2 ;;
			--join-token) JOIN_TOKEN=$2 ;;
			--name) NAME=$2 ;;
			--labels) LABELS=$2 LABELS_SET=1 ;;
			--ca-hash) CA_HASH=$2 ;;
			--version) PIN=$2 PIN_SET=1 ;;
			esac
			shift 2
			;;
		--share)
			[ $# -ge 2 ] || die "$1 needs a value"
			SHARES=${SHARES:+$SHARES,}$2 SHARES_SET=1
			shift 2
			;;
		--no-share)
			SHARES='' SHARES_SET=1
			shift
			;;
		--core-mode)
			MODE=core
			shift
			;;
		--uninstall)
			UNINSTALL=1
			shift
			;;
		--rollback)
			ROLLBACK=1
			shift
			;;
		-h | --help)
			usage
			exit 0
			;;
		*) die "unknown option $1 (see --help)" ;;
		esac
	done
	if [ -z "$MODE" ] && [ -n "$CORE_URL" ]; then
		MODE=node
	fi
	for v in "$CORE_URL" "$JOIN_TOKEN" "$NAME" "$LABELS" "$CA_HASH" "$SHARES"; do
		case $v in
		*"'"* | *'"'* | *\\* | *'$'* | *' '* | *'
'*) die "an option has a quote, a space, a \$ or a line break: $v" ;;
		esac
	done
	if [ "$MODE" = core ] && [ -n "$CORE_URL" ]; then
		die "--core-mode runs eriphe-core here: --core is for joining another one"
	fi
	case $PIN in
	latest) PIN='' ;;
	'') [ -z "$PIN_SET" ] || die "--version is latest or sha-<commit>" ;;
	sha-*) printf '%s' "$PIN" | grep -Eq '^sha-[0-9a-f]{7,40}$' || die "--version is latest or sha-<commit>, e.g. sha-1234567" ;;
	*) die "--version is latest or sha-<commit>, e.g. sha-1234567" ;;
	esac
	if [ -n "$CA_HASH" ]; then
		case $CA_HASH in
		sha256:*) ;;
		*) die "--ca-hash is sha256:<hex>" ;;
		esac
	fi
	check_shares
}

# check_shares: each --share is name=/absolute/path[:ro], the name in
# lowercase letters, digits, - or _, given once, the path neither / nor
# eriphe's own data, without a comma or a colon (Docker reads both).
check_shares() {
	names=' '
	old_ifs=$IFS
	IFS=,
	for s in $SHARES; do
		IFS=$old_ifs
		n=${s%%=*} p=${s#*=}
		p=${p%:ro}
		printf '%s' "$n" | grep -Eq '^[a-z0-9_-]+$' || die "--share $s: the name is lowercase letters, digits, - or _ (--share documents=/home/me/Documents)"
		[ "$s" != "$n" ] || die "--share $s: want NAME=/PATH"
		case $p in
		/*) ;;
		*) die "--share $s: the path must be absolute" ;;
		esac
		case $p in
		*:* | *,*) die "--share $s: the path has a colon or a comma" ;;
		/ | "$DATA" | "$DATA"/* | "$CONF" | "$CONF"/*) die "--share $s: not this path (the whole machine, or Eriphe's own files)" ;;
		esac
		case $names in
		*" $n "*) die "--share $s: $n is given twice" ;;
		esac
		names="$names$n "
	done
	IFS=$old_ifs
}

# share_paths: the paths of the shares, one per line, with :ro if so.
share_paths() {
	printf '%s\n' "$SHARES" | tr ',' '\n' | sed -n 's/^[^=]*=//p'
}

default_name() {
	if [ "$OS" = Darwin ]; then
		n=$(scutil --get LocalHostName 2>/dev/null || hostname -s)
	else
		n=$(hostname 2>/dev/null || cat /etc/hostname)
	fi
	printf '%s' "${n%%.*}" | tr 'A-Z_' 'a-z-' | tr -cd 'a-z0-9-' | sed 's/^-*//; s/-*$//'
}

# saved KEY: a setting of an earlier install, from the host's install.conf.
saved() { printf '%s\n' "$INFO" | sed -n "s/^saved_$1=//p" | tail -n 1; }
info() { printf '%s\n' "$INFO" | sed -n "s/^$1=//p" | tail -n 1; }

# settle: merges the options with an earlier install's, once the host has
# answered (INFO).
settle() {
	prev=$(saved mode)
	if [ -n "$prev" ] && [ -n "$MODE" ] && [ "$prev" != "$MODE" ]; then
		die "this machine is installed in $prev mode: run --uninstall first to change to $MODE mode"
	fi
	[ -n "$MODE" ] || MODE=$prev
	[ -n "$MODE" ] || {
		usage >&2
		die "give --core and --join-token to join an eriphe-core, or --core-mode"
	}
	[ -n "$CORE_URL" ] || CORE_URL=$(saved core)
	[ -n "$NAME" ] || NAME=$(saved name)
	[ -n "$NAME" ] || NAME=$(default_name)
	[ -n "$LABELS_SET" ] || LABELS=$(saved labels)
	[ -n "$CA_HASH" ] || CA_HASH=$(saved ca_hash)
	[ -n "$PIN_SET" ] || PIN=$(saved version)
	images
	[ -n "$SHARES_SET" ] || SHARES=$(saved shares)
	if [ "$OS" = Darwin ] && [ -n "$SHARES" ]; then
		# The VM sees no Mac folder: sharing them needs a helper on the Mac.
		warn "sharing Mac folders is coming: --share is ignored on macOS for now"
		SHARES=''
	fi
	if [ "$MODE" = core ]; then
		CORE_URL=http://eriphe-core:8800
	fi
	printf '%s' "$NAME" | grep -Eq '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$' ||
		die "the node's name is lowercase letters, digits and dashes: --name pc"
	joined=$(info joined)
	joined_as=$(info joined_as)
	if [ -n "$joined" ]; then
		if [ -n "$joined_as" ] && [ "$joined_as" != "$NAME" ]; then
			warn "this node joined as $joined_as and keeps that name"
			NAME=$joined_as
		fi
	elif [ "$MODE" = node ]; then
		[ -n "$CORE_URL" ] || die "give --core (eriphe-core's address)"
		[ -n "$JOIN_TOKEN" ] || die "a new node needs --join-token (from: eriphe-core join-token -name $NAME)"
	fi
}

# ---- What runs on the Linux host ----------------------------------------

# host_docker installs Docker Engine if it is missing and reports what the
# rest needs, as key=value lines.
host_docker() {
	{
		prelude
		cat <<'EOF'
if ! command -v docker >/dev/null 2>&1; then
  {
    echo "Installing Docker Engine..."
    # shellcheck disable=SC1091
    os=$(. /etc/os-release 2>/dev/null; echo " ${ID:-} ${ID_LIKE:-} ")
    case $os in
      # Docker's own packages, through its convenience script: a current
      # Engine with the compose plugin, which distribution packages often lack
      # or ship old (the tasks' network needs Docker 28).
      *" debian "*|*" ubuntu "*|*" raspbian "*|*" fedora "*|*" rhel "*|*" centos "*)
        command -v curl >/dev/null 2>&1 || die "curl is missing"
        curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
        sh /tmp/get-docker.sh </dev/null
        rm -f /tmp/get-docker.sh ;;
      # Where Docker has no packages: the distribution's own.
      *" alpine "*) apk add --no-cache docker docker-cli-compose ;;
      *" arch "*) pacman -Sy --noconfirm --needed docker docker-compose ;;
      *" suse "*|*opensuse*) zypper --non-interactive install docker docker-compose ;;
      *) die "no Docker Engine for this distribution:$os install it, then run this again" ;;
    esac
    mkdir -p "$CONF"
    touch "$CONF/docker-installed"
  } >&2
fi
# Docker starts at boot; the containers restart with it.
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  systemctl enable --now docker.service >/dev/null 2>&1 || die "systemctl enable docker failed"
elif command -v rc-update >/dev/null 2>&1; then
  rc-update add docker default >/dev/null 2>&1 || true
  rc-service docker start >/dev/null 2>&1 || true
fi
i=0
until docker info >/dev/null 2>&1; do
  i=$((i + 1)); [ $i -le 60 ] || die "Docker does not answer"; sleep 1
done
docker compose version >/dev/null 2>&1 || die "Docker Compose is missing (the docker-compose-plugin package)"
[ -S /var/run/docker.sock ] || die "no /var/run/docker.sock (rootless Docker is not supported)"
echo "docker_gid=$(stat -c %g /var/run/docker.sock)"
echo "docker_version=$(docker version --format '{{.Server.Version}}')"
if [ -f "$DATA/node/node.json" ]; then
  echo joined=1
  echo "joined_as=$(sed -n 's/.*"name": *"\([^"]*\)".*/\1/p' "$DATA/node/node.json" | head -n 1)"
fi
[ -f "$CONF/install.conf" ] && sed 's/^/saved_/' "$CONF/install.conf"
true
EOF
	} | on_host
}

# compose_yaml: the compose project, as deploy/nas/compose.yaml with this
# machine's paths and Docker group.
compose_yaml() {
	gid=$1 isolated=$2
	cat <<EOF
# Written by install.sh: run it again rather than editing this file.
# The node reaches Docker only through the socket proxy, which allows the
# calls it makes and bind mounts from its root and the shared places only
# (--share). Tasks run on
# eriphe-tasks, an internal network: they reach the node (its task listener
# and its egress relay, their one way out, which never dials the LAN), not
# eriphe-core, the services nor this machine.
name: eriphe
services:
EOF
	if [ "$MODE" = core ]; then
		cat <<EOF
  core:
    image: $CORE_IMAGE
    container_name: eriphe-core
    restart: unless-stopped
    read_only: true
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    ports: ["8800:8800", "8443:8443"]
    environment:
      # The workspace shows the command that updates it.
      ERIPHE_INSTALL: $INSTALL
    volumes:
      - $DATA/core:/data
    networks: [eriphe-system]

EOF
	fi
	cat <<EOF
  docker-proxy:
    image: $PROXY_IMAGE
    container_name: eriphe-docker-proxy
    restart: unless-stopped
    # nobody, in the group that owns /var/run/docker.sock.
    user: "65534:$gid"
    read_only: true
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    networks: [eriphe-docker]
    command:
      - -listenip=0.0.0.0
      - -allowfrom=eriphe-node
      - -shutdowngracetime=5
      - -watchdoginterval=0
      - -allowbindmountfrom=$DATA/node$(share_paths | sed 's/:ro$//' | tr '\n' ',' | sed 's/,$//; s/^./,&/')
      - -allowHEAD=(/v1\.[0-9]+)?/_ping
      - -allowGET=(/v1\.[0-9]+)?/(_ping|version|networks/(bridge|eriphe-tasks)|containers/json|images/json|images/[a-zA-Z0-9][a-zA-Z0-9_./:@-]*/json)
      - -allowPOST=(/v1\.[0-9]+)?/(containers/create|containers/[a-zA-Z0-9][a-zA-Z0-9_.-]*/(attach|start|wait|stop)|images/create)
      - -allowDELETE=(/v1\.[0-9]+)?/containers/[a-zA-Z0-9][a-zA-Z0-9_.-]*

  node:
    image: $NODE_IMAGE
    container_name: eriphe-node
    restart: unless-stopped
    depends_on: [docker-proxy$([ "$MODE" = core ] && printf ', core')]
    read_only: true
    tmpfs: [/tmp]
    # Root in its container, to hand the task folders to the tasks' user.
    cap_drop: [ALL]
    cap_add: [CHOWN, DAC_OVERRIDE, FOWNER]
    security_opt: [no-new-privileges:true]
    environment:
      ERIPHE_CORE: \${ERIPHE_CORE}
      ERIPHE_NODE_NAME: \${ERIPHE_NODE_NAME}
      ERIPHE_NODE_LABELS: \${ERIPHE_NODE_LABELS:-}
      # The places whose folders may be given to Eriphe (--share).
      ERIPHE_NODE_SHARES: \${ERIPHE_NODE_SHARES:-}
      ERIPHE_CA_HASH: \${ERIPHE_CA_HASH:-}
      # Only for the first start, from install.sh, never kept in .env.
      ERIPHE_JOIN_TOKEN: \${ERIPHE_JOIN_TOKEN:-}
      ERIPHE_INSTALL: $INSTALL
      DOCKER_HOST: tcp://eriphe-docker-proxy:2375
    command: ["-root", "$DATA/node",
              "-network", "eriphe-system", "-task-network", "eriphe-tasks",
              "-callback-url", "http://eriphe-node:8790",
              "-image", "$WORKER_IMAGE"$([ "$MODE" = core ] && printf ', "-always-on"')]
    volumes:
      # Same path inside and outside: the Docker daemon understands it as is.
      - $DATA/node:$DATA/node
EOF
	share_paths | while IFS= read -r p; do
		case $p in
		*:ro) printf '      - %s:%s:ro\n' "${p%:ro}" "${p%:ro}" ;;
		?*) printf '      - %s:%s\n' "$p" "$p" ;;
		esac
	done
	cat <<EOF
    networks: [eriphe-system, eriphe-tasks, eriphe-docker]

networks:
  eriphe-system:
    name: eriphe-system
  # Internal: no route out, tasks go through the node's egress relay.
  eriphe-tasks:
    name: eriphe-tasks
    internal: true
EOF
	if [ "$isolated" = 1 ]; then
		cat <<EOF
    # The bridge has no address on this machine: its own services are not
    # reachable from the tasks on the gateway.
    driver_opts:
      com.docker.network.bridge.gateway_mode_ipv4: isolated
EOF
	fi
	cat <<EOF
  eriphe-docker:
    name: eriphe-docker
    internal: true
EOF
}

env_file() {
	cat <<EOF
ERIPHE_CORE='$CORE_URL'
ERIPHE_NODE_NAME='$NAME'
ERIPHE_NODE_LABELS='$LABELS'
ERIPHE_CA_HASH='$CA_HASH'
ERIPHE_NODE_SHARES='$SHARES'
EOF
}

install_conf() {
	cat <<EOF
mode=$MODE
core=$CORE_URL
name=$NAME
labels=$LABELS
ca_hash=$CA_HASH
version=$PIN
shares=$SHARES
EOF
}

b64() { base64 | tr -d '\n'; }

# ---- The updater, on the Linux host ---------------------------------------
#
# eriphe-core and the node have no right to pull an image or recreate a
# container (the node reaches Docker only through the socket proxy). The
# updater does it for them, on the host, as root: a systemd unit that starts
# when eriphe-core (core mode) or the node drops a request in its data
# folder (update-requested), and writes how it went next to it
# (update-status). A request says only "update" (to the latest version, or
# the pinned one) or "rollback": the images are the ones written here.

# updater_conf: what the updater works on, written at every install.
updater_conf() {
	cat <<EOF
# Written by install.sh: run it again rather than editing this file.
CONF=$(q "$CONF") DATA=$(q "$DATA") CORE_UID=$CORE_UID MODE=$(q "$MODE") PIN=$(q "$PIN")
CORE_IMAGE=$(q "$CORE_IMAGE") NODE_IMAGE=$(q "$NODE_IMAGE") WORKER_IMAGE=$(q "$WORKER_IMAGE")
EOF
}

# updater_sh: the updater itself, /opt/eriphe/update.sh. With no argument it
# applies the requests waiting; "rollback" goes back to the version before
# the last update (install.sh --rollback).
updater_sh() {
	cat <<'EOF'
#!/bin/sh
# Written by install.sh: applies the update requests of eriphe-core and
# eriphe-node (install/README.md, Updates). Run by eriphe-update.service.
set -u
# shellcheck disable=SC1091
. /opt/eriphe/update.conf
STATE=$DATA/update
HEALTH=${ERIPHE_UPDATE_HEALTH:-180} # seconds a recreated eriphe-node has to connect again
mkdir -p "$STATE"
chmod 700 "$STATE"
dc() { docker compose -p eriphe --project-directory "$CONF" -f "$CONF/compose.yaml" --env-file "$CONF/.env" "$@"; }
log() {
  printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" >>"$STATE/update.log"
  printf 'eriphe-update: %s\n' "$*" >&2
}
if command -v flock >/dev/null 2>&1; then
  exec 9>"$STATE/lock"
  flock -n 9 || { log "another update runs"; exit 0; }
fi

# status TARGET ID ACTION OUTCOME MESSAGE FROM TO: how a request went, for
# eriphe-core (core) or the node (node) to read.
status() {
  f=$DATA/$1/update-status
  msg=$(printf '%s' "$5" | tr '\n\r' '  ' | cut -c 1-300)
  printf 'id=%s\naction=%s\noutcome=%s\nmessage=%s\nfrom=%s\nto=%s\nat=%s\n' "$2" "$3" "$4" "$msg" "$6" "$7" "$(date +%s)" >"$f.new"
  chmod 600 "$f.new"
  [ "$1" = core ] && chown "$CORE_UID:$CORE_UID" "$f.new"
  mv "$f.new" "$f"
  [ "$4" = running ] || log "$1 $3 $2: $4${5:+ ($5)}"
}

# The images of a target, as "service ref" lines (service - when no
# container runs it: the worker image, which the node starts tasks from).
items() {
  if [ "$1" = core ]; then echo "core $CORE_IMAGE"; fi
  echo "node $NODE_IMAGE"
  echo "- $WORKER_IMAGE"
}
image_id() { docker image inspect -f '{{.Id}}' "$1" 2>/dev/null; }
running_id() { docker inspect -f '{{.Image}}' "eriphe-$1" 2>/dev/null; }
# version ID: the build's version (its label), or the start of its id.
version() {
  v=$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.version"}}' "$1" 2>/dev/null)
  case $v in
    '' | '<no value>') printf '%s' "$1" | sed 's/^sha256://' | cut -c 1-12 ;;
    *) printf '%s' "$v" ;;
  esac
}
repo() { printf '%s' "${1%:*}"; }

# current TARGET: "service ref id" of what runs now.
current() {
  items "$1" | while read -r svc ref; do
    id=
    [ "$svc" = - ] || id=$(running_id "$svc")
    [ -n "$id" ] || id=$(image_id "$ref")
    echo "$svc $ref $id"
  done
}

# put LIST: the images of LIST ("service ref id" lines) under their refs.
put() {
  printf '%s\n' "$1" | while read -r _ ref id; do
    [ -z "$id" ] || docker tag "$id" "$ref"
  done
}

# healthy SERVICE SINCE: eriphe-core answers (core), or eriphe-node is
# connected again since SINCE (node), within HEALTH seconds.
healthy() {
  i=0
  if [ "$1" = core ]; then
    until docker exec eriphe-core wget -q -O /dev/null http://127.0.0.1:8800/health 2>/dev/null; do
      i=$((i + 2)); [ $i -lt "$HEALTH" ] || { why="eriphe-core does not answer"; return 1; }
      sleep 2
    done
    return 0
  fi
  until docker logs --since "$2" eriphe-node 2>&1 | grep -q 'connected to '; do
    state=$(docker inspect -f '{{.State.Status}} {{.RestartCount}}' eriphe-node 2>/dev/null || echo missing)
    case $state in
      "running 0") ;;
      *) [ $i -lt 6 ] || { why="eriphe-node does not start ($state)"; return 1; } ;;
    esac
    i=$((i + 2)); [ $i -lt "$HEALTH" ] || { why="eriphe-node is not connected after $HEALTH s"; return 1; }
    sleep 2
  done
}

# start SERVICE REF: its container again, from the image now under REF.
# The old one is removed first: compose may leave one that keeps
# restarting in place.
start() {
  docker rm -f "eriphe-$1" >/dev/null 2>&1 || true
  if ! out=$(dc up -d --pull never --no-deps "$1" 2>&1); then
    why="docker compose up $1 failed: $(printf '%s' "$out" | tail -n 1)"
    return 1
  fi
  [ "$(running_id "$1")" = "$(image_id "$2")" ] || { why="eriphe-$1 does not run the image it should"; return 1; }
}

# recreate TARGET: the containers again from the images now under their
# refs, eriphe-core first; then the health check.
recreate() {
  since=$(date +%s)
  if [ "$1" = core ]; then
    start core "$CORE_IMAGE" || return 1
    healthy core "$since" || return 1
  fi
  start node "$NODE_IMAGE" || return 1
  healthy node "$since"
}

main_id() { printf '%s\n' "$1" | awk -v s="$2" '$1 == s { print $3 }'; }

# keep TARGET LIST: LIST becomes the version to go back to; the images of
# the one before are removed once nothing else uses them.
keep() {
  old=
  [ -f "$STATE/previous.$1" ] && old=$(cat "$STATE/previous.$1")
  printf '%s\n' "$2" >"$STATE/previous.$1"
  printf '%s\n' "$2" | while read -r _ ref id; do
    [ -z "$id" ] || docker tag "$id" "$(repo "$ref"):eriphe-previous-$1"
  done
  printf '%s\n' "$old" | while read -r _ _ id; do
    [ -z "$id" ] || docker image rm "$id" >/dev/null 2>&1 || true
  done
}

# update TARGET ID AUTO CORE: the latest images (or the pinned ones), each
# service recreated, checked, and the previous ones back if it fails.
update() {
  target=$1 id=$2 auto=$3 core=$4
  before=$(current "$target")
  main=core
  [ "$target" = core ] || main=node
  from=$(version "$(main_id "$before" "$main")")
  status "$target" "$id" update running "" "$from" ""
  # eriphe-core's machine takes eriphe-core's latest build, and the node and
  # worker of that same build (their sha-<commit> tag), not their own latest:
  # latest may be half published, and the node is never newer than
  # eriphe-core.
  match=
  for ref in $(printf '%s\n' "$before" | awk '{ print $2 }'); do
    src=$ref
    case $match:$ref in
      sha-*:*/eriphe-node:* | sha-*:*/eriphe-worker:*) src=$(repo "$ref"):$match ;;
    esac
    if ! out=$(docker pull -q "$src" 2>&1); then
      put "$before"
      status "$target" "$id" update failed "could not pull $src: $(printf '%s' "$out" | tail -n 1)" "$from" ""
      return
    fi
    if [ "$src" != "$ref" ]; then
      docker tag "$src" "$ref"
      docker image rm "$src" >/dev/null 2>&1 || true
    fi
    if [ "$target" = core ] && [ -z "$PIN" ] && [ -z "$match" ]; then
      match=$(version "$(image_id "$ref")")
      case $match in sha-*) ;; *) match= ;; esac
    fi
  done
  after=$(current "$target" | while read -r svc ref _; do echo "$svc $ref $(image_id "$ref")"; done)
  new=$(main_id "$after" "$main")
  to=$(version "$new")
  if [ "$after" = "$before" ]; then
    if [ -n "$PIN" ]; then
      status "$target" "$id" update pinned "pinned to $PIN" "$from" "$to"
    else
      status "$target" "$id" update current "" "$from" "$to"
    fi
    return
  fi
  # A node is never newer than eriphe-core.
  case $target:$core:$to in
    node:sha-*:sha-*)
      if [ "$to" != "$core" ]; then
        put "$before"
        status "$target" "$id" update refused "eriphe-core runs $core: $to would be newer" "$from" "$to"
        return
      fi ;;
  esac
  if [ "$auto" = 1 ] && grep -qx "$new" "$STATE/failed" 2>/dev/null; then
    put "$before"
    status "$target" "$id" update skipped "$to failed here before" "$from" "$to"
    return
  fi
  log "$target: $from -> $to"
  if recreate "$target"; then
    keep "$target" "$before"
    status "$target" "$id" update ok "" "$from" "$to"
    return
  fi
  failed=$why
  echo "$new" >>"$STATE/failed"
  put "$before"
  if recreate "$target"; then
    status "$target" "$id" update rolled_back "$failed" "$from" "$to"
  else
    status "$target" "$id" update rollback_failed "$failed; then: $why" "$from" "$to"
  fi
}

# rollback TARGET ID: the version before the last update, the one now kept
# to go forward again.
rollback() {
  target=$1 id=$2
  main=core
  [ "$target" = core ] || main=node
  before=$(current "$target")
  from=$(version "$(main_id "$before" "$main")")
  if [ ! -s "$STATE/previous.$target" ]; then
    status "$target" "$id" rollback failed "no previous version here" "$from" ""
    return
  fi
  prev=$(cat "$STATE/previous.$target")
  printf '%s\n' "$prev" | while read -r _ _ pid; do
    [ -z "$pid" ] || image_id "$pid" >/dev/null || echo gone
  done | grep -q gone && {
    status "$target" "$id" rollback failed "the previous images are gone" "$from" ""
    return
  }
  to=$(version "$(main_id "$prev" "$main")")
  status "$target" "$id" rollback running "" "$from" "$to"
  put "$prev"
  if recreate "$target"; then
    keep "$target" "$before"
    main_id "$before" "$main" >>"$STATE/failed"
    status "$target" "$id" rollback ok "" "$from" "$to"
    return
  fi
  failed=$why
  put "$before"
  if recreate "$target"; then
    status "$target" "$id" rollback failed "$failed: back on $from" "$from" "$to"
  else
    status "$target" "$id" rollback rollback_failed "$failed; then: $why" "$from" "$to"
  fi
}

# field FILE KEY: one value of a request, never sourced.
field() { sed -n "s/^$2=//p" "$1" | head -n 1; }

if [ "${1:-}" = rollback ]; then
  target=node
  [ "$MODE" = core ] && target=core
  rollback "$target" "local-$(date +%s)"
  exit 0
fi
for target in core node; do
  f=$DATA/$target/update-requested
  [ -f "$f" ] || continue
  [ "$target" = node ] || [ "$MODE" = core ] || { rm -f "$f"; continue; }
  id=$(field "$f" id) action=$(field "$f" action) core=$(field "$f" core) auto=$(field "$f" auto)
  rm -f "$f"
  printf '%s' "$id" | grep -Eq '^[a-z0-9-]{1,40}$' || { log "$target: a request without a valid id"; continue; }
  printf '%s' "$core" | grep -Eq '^(sha-[0-9a-f]{7,40}|dev)?$' || core=
  [ "$auto" = 1 ] || auto=0
  case $action in
    update) update "$target" "$id" "$auto" "$core" ;;
    rollback) rollback "$target" "$id" ;;
    *) status "$target" "$id" "$action" failed "unknown action" "" "" ;;
  esac
done
EOF
}

# updater_units: the systemd units that run it: when a request is dropped,
# and every 10 minutes in case one was missed.
updater_units() {
	cat <<EOF
UNIT_SERVICE=$(printf '%s\n' "[Unit]" "Description=Eriphe updater: applies eriphe-core's update requests" \
		"After=docker.service" "Requires=docker.service" "" "[Service]" "Type=oneshot" \
		"ExecStart=/bin/sh $CONF/update.sh" "TimeoutStartSec=30min" | b64)
UNIT_PATH=$(printf '%s\n' "[Unit]" "Description=Eriphe updater: a request from eriphe-core or eriphe-node" "" "[Path]" \
		"PathExists=$DATA/node/update-requested" \
		"$([ "$MODE" = core ] && printf 'PathExists=%s/core/update-requested' "$DATA")" \
		"Unit=eriphe-update.service" "" "[Install]" "WantedBy=multi-user.target" | b64)
UNIT_TIMER=$(printf '%s\n' "[Unit]" "Description=Eriphe updater: a request missed" "" "[Timer]" \
		"OnBootSec=2min" "OnUnitInactiveSec=10min" "Unit=eriphe-update.service" "" "[Install]" "WantedBy=timers.target" | b64)
EOF
}

# host_up writes the project, pulls the images and starts it, joining the
# node if it has not joined yet. In core mode it prints the setup code.
host_up() {
	{
		prelude
		printf 'JOIN_TOKEN=%s\n' "$(q "$JOIN_TOKEN")"
		printf 'SHARE_PATHS=%s\n' "$(q "$(share_paths | sed 's/:ro$//' | tr '\n' ' ')")"
		printf 'COMPOSE=%s\n' "$(compose_yaml "$1" "$2" | b64)"
		printf 'ENVF=%s\n' "$(env_file | b64)"
		printf 'INSTALL_CONF=%s\n' "$(install_conf | b64)"
		printf 'UPDATE_CONF=%s\n' "$(updater_conf | b64)"
		printf 'UPDATE_SH=%s\n' "$(updater_sh | b64)"
		printf 'PIN=%s\n' "$(q "$PIN")"
		updater_units
		cat <<'EOF'
# Progress on stderr; stdout carries only key=value lines.
exec 3>&1 >&2
umask 077
mkdir -p "$CONF" "$DATA/node"
chmod 700 "$DATA/node"
if [ "$MODE" = core ]; then
  mkdir -p "$DATA/core"
  chown "$CORE_UID:$CORE_UID" "$DATA/core"
  chmod 700 "$DATA/core"
fi
for p in $SHARE_PATHS; do
  [ -d "$p" ] || die "--share: $p is not a folder of this machine"
done
put() { printf '%s' "$2" | base64 -d > "$1.new" && mv "$1.new" "$1"; }
put "$CONF/compose.yaml" "$COMPOSE"
put "$CONF/.env" "$ENVF"
put "$CONF/install.conf" "$INSTALL_CONF"

for img in $IMAGES; do
  echo "Pulling $img..."
  if ! out=$(docker pull "$img" 2>&1); then
    out=$(printf '%s\n' "$out" | tail -n 1)
    if docker image inspect "$img" >/dev/null 2>&1; then
      echo "eriphe: could not pull $img ($out): using the copy already here" >&2
    else
      die "could not pull $img: $out (is the image published, and public?)"
    fi
  fi
done

up() { dc up -d --pull never --remove-orphans "$@"; }
if [ ! -f "$DATA/node/node.json" ] && [ "$MODE" = core ]; then
  up core docker-proxy
  echo "Waiting for eriphe-core..."
  i=0
  # -quiet prints the token alone; it fails until eriphe-core answers.
  until JOIN_TOKEN=$(docker exec eriphe-core eriphe-core join-token -data /data -name "$NODE_NAME" -quiet 2>/dev/null |
      tr -d '[:space:]') && [ -n "$JOIN_TOKEN" ]; do
    i=$((i + 1))
    if [ $i -gt 60 ]; then
      docker logs --tail 20 eriphe-core >&2
      die "eriphe-core gives no join token (above: its log)"
    fi
    sleep 2
  done
fi
connected() {
  echo "Waiting for eriphe-node..."
  i=0
  until docker logs eriphe-node 2>&1 | grep -q 'connected to '; do
    i=$((i + 1))
    state=$(docker inspect -f '{{.State.Status}} {{.RestartCount}}' eriphe-node 2>/dev/null || echo missing)
    case $state in
      "running 0") ;;
      *) [ $i -lt 3 ] || { docker logs --tail 20 eriphe-node >&2; die "eriphe-node does not start ($state; above: its log)"; } ;;
    esac
    [ $i -le 90 ] || { docker logs --tail 20 eriphe-node >&2; die "eriphe-node is not connected after 3 minutes (above: its log)"; }
    sleep 2
  done
}
if [ -f "$DATA/node/node.json" ]; then
  up
  connected
else
  ERIPHE_JOIN_TOKEN=$JOIN_TOKEN up
  connected
  # Joined: the node has its own secret. Recreated without the join token,
  # which stays nowhere, not even in the container's configuration.
  [ -f "$DATA/node/node.json" ] || die "eriphe-node is connected but has no node.json"
  up
  connected
fi
docker logs eriphe-node 2>&1 | grep 'connected to ' | tail -n 1 | sed 's/^[0-9/]* [0-9:]* /eriphe-node: /'
if [ "$MODE" = core ]; then
  code=$(docker logs eriphe-core 2>&1 | sed -n 's/.*with the setup code \([A-Za-z0-9-]*\).*/\1/p' | tail -n 1)
  echo "setup_code=$code" >&3
fi

# The updater, and the word to eriphe-core and the node that it is there.
put "$CONF/update.conf" "$UPDATE_CONF"
put "$CONF/update.sh" "$UPDATE_SH"
chmod 700 "$CONF/update.sh"
marks="$DATA/node"
[ "$MODE" = core ] && marks="$marks $DATA/core"
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  put /etc/systemd/system/eriphe-update.service "$UNIT_SERVICE"
  put /etc/systemd/system/eriphe-update.path "$UNIT_PATH"
  put /etc/systemd/system/eriphe-update.timer "$UNIT_TIMER"
  chmod 644 /etc/systemd/system/eriphe-update.service /etc/systemd/system/eriphe-update.path /etc/systemd/system/eriphe-update.timer
  systemctl daemon-reload
  systemctl enable --now eriphe-update.path eriphe-update.timer >/dev/null 2>&1 || die "systemctl enable eriphe-update failed"
  for d in $marks; do
    printf 'version=1\npin=%s\n' "$PIN" > "$d/updater"
  done
  [ "$MODE" != core ] || chown "$CORE_UID:$CORE_UID" "$DATA/core/updater"
  echo "updater=1" >&3
else
  for d in $marks; do rm -f "$d/updater"; done
  echo "updater=0" >&3
fi
EOF
	} | on_host
}

host_uninstall() {
	{
		prelude
		cat <<'EOF'
if command -v systemctl >/dev/null 2>&1 && [ -f /etc/systemd/system/eriphe-update.path ]; then
  systemctl disable --now eriphe-update.path eriphe-update.timer eriphe-update.service >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/eriphe-update.service /etc/systemd/system/eriphe-update.path /etc/systemd/system/eriphe-update.timer
  systemctl daemon-reload
fi
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  if [ -f "$CONF/compose.yaml" ]; then
    echo "Removing the containers..."
    dc down --remove-orphans >/dev/null 2>&1 || true
  fi
  ids=$(docker ps -aq --filter label=eriphe.task; docker ps -aq --filter label=eriphe.service)
  # shellcheck disable=SC2086 # one argument per id
  [ -z "$ids" ] || docker rm -f $ids >/dev/null
  docker network rm eriphe-tasks eriphe-system eriphe-docker >/dev/null 2>&1 || true
  docker volume rm eriphe-cache >/dev/null 2>&1 || true
  # Every tag of Eriphe's images: latest, a pinned one, the previous ones.
  for repo in $ALL_REPOS; do
    refs=$(docker images --format '{{.Repository}}:{{.Tag}}' "$repo" | grep -v ':<none>$' || true)
    # shellcheck disable=SC2086 # one argument per image
    [ -z "$refs" ] || docker image rm $refs >/dev/null 2>&1 || true
  done
fi
installed=
[ -f "$CONF/docker-installed" ] && installed=1
rm -rf "$CONF" "$DATA"
if [ -n "$installed" ]; then
  if [ -n "$(docker ps -aq 2>/dev/null)" ]; then
    echo "Docker Engine stays: other containers use it."
  else
    echo "Removing Docker Engine (installed for Eriphe)..."
    if command -v apt-get >/dev/null 2>&1; then
      DEBIAN_FRONTEND=noninteractive apt-get purge -y -q docker-ce docker-ce-cli containerd.io docker-buildx-plugin \
        docker-compose-plugin docker-ce-rootless-extras docker-model-plugin >/dev/null 2>&1 || true
      DEBIAN_FRONTEND=noninteractive apt-get autoremove -y -q >/dev/null 2>&1 || true
      rm -f /etc/apt/sources.list.d/docker.list /etc/apt/sources.list.d/docker.sources /etc/apt/keyrings/docker.asc
    elif command -v dnf >/dev/null 2>&1; then
      dnf remove -y -q docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin \
        docker-ce-rootless-extras docker-model-plugin >/dev/null 2>&1 || true
      rm -f /etc/yum.repos.d/docker-ce.repo
    elif command -v apk >/dev/null 2>&1; then
      rc-update del docker default >/dev/null 2>&1 || true
      apk del docker docker-cli-compose >/dev/null 2>&1 || true
    elif command -v pacman >/dev/null 2>&1; then
      systemctl disable --now docker.service docker.socket >/dev/null 2>&1 || true
      pacman -Rns --noconfirm docker docker-compose >/dev/null 2>&1 || true
    elif command -v zypper >/dev/null 2>&1; then
      zypper --non-interactive remove docker docker-compose >/dev/null 2>&1 || true
    fi
    rm -rf /var/lib/docker /var/lib/containerd
    groupdel docker >/dev/null 2>&1 || true
  fi
fi
EOF
	} | on_host
}

# host_rollback runs the updater by hand: back to the version before the
# last update.
host_rollback() {
	{
		prelude
		cat <<'EOF'
if [ ! -f "$CONF/update.sh" ]; then
  die "no updater here yet: run the install command once, without --rollback"
fi
sh "$CONF/update.sh" rollback
# shellcheck disable=SC1091
MODE=$(. "$CONF/update.conf"; echo "$MODE")
target=node
[ "$MODE" = core ] && target=core
echo "Rollback:"
sed -n 's/^outcome=//p; s/^message=\(..*\)/  \1/p; s/^from=\(..*\)/  from \1/p; s/^to=\(..*\)/  to \1/p' "$DATA/$target/update-status"
EOF
	} | on_host
}

# ---- macOS: Lima and the VM ---------------------------------------------

mac_setup() {
	ERIPHE_HOME=$HOME/.eriphe
	LIMACTL=$ERIPHE_HOME/lima/bin/limactl
	LIMA_HOME=$ERIPHE_HOME/vm # not ~/.lima: apart from any other Lima
	export LIMA_HOME
	AGENT_PLIST=$HOME/Library/LaunchAgents/$AGENT_LABEL.plist
}

lima_yaml() {
	cat <<EOF
# Written by install.sh. The VM Eriphe runs in: no folder of the Mac is
# mounted, no SSH key nor agent of the Mac goes in. Only eriphe-core's ports
# (8800, 8443), when it runs here, are forwarded, to every address of the Mac.
minimumLimaVersion: 2.0.0
base: template:_images/ubuntu-24.04
vmType: vz
cpus: ${ERIPHE_VM_CPUS:-4}
memory: ${ERIPHE_VM_MEMORY:-4GiB}
disk: ${ERIPHE_VM_DISK:-60GiB}
mounts: []
# Apple's NAT, next to Lima's own network: the VM's traffic goes out through
# the system, not through limactl, which macOS's local network privacy keeps
# off the LAN when launchd starts it (the node would reach the Internet only).
networks:
- vzNAT: true
containerd:
  system: false
  user: false
ssh:
  loadDotSSHPubKeys: false
  forwardAgent: false
  forwardX11: false
portForwards:
- guestPort: 8800
  guestIP: 0.0.0.0
  hostIP: 0.0.0.0
- guestPort: 8443
  guestIP: 0.0.0.0
  hostIP: 0.0.0.0
- guestIP: 0.0.0.0
  guestPortRange: [1, 65535]
  proto: any
  ignore: true
- guestIP: 127.0.0.1
  guestPortRange: [1, 65535]
  proto: any
  ignore: true
EOF
}

vm_status() { "$LIMACTL" list --format '{{.Status}}' "$VM" 2>/dev/null || true; }

mac_lima() {
	case $(uname -m) in
	arm64) arch=arm64 sum=$LIMA_SHA256_arm64 ;;
	x86_64) arch=x86_64 sum=$LIMA_SHA256_x86_64 ;;
	*) die "unsupported Mac: $(uname -m)" ;;
	esac
	if [ -x "$LIMACTL" ] && [ "$("$LIMACTL" --version 2>/dev/null)" = "limactl version $LIMA_VERSION" ]; then
		return
	fi
	say "Downloading Lima $LIMA_VERSION..."
	tmp=$(mktemp -d)
	curl -fL --progress-bar -o "$tmp/lima.tar.gz" \
		"https://github.com/lima-vm/lima/releases/download/v$LIMA_VERSION/lima-$LIMA_VERSION-Darwin-$arch.tar.gz"
	got=$(shasum -a 256 "$tmp/lima.tar.gz" | cut -d ' ' -f 1)
	if [ "$got" != "$sum" ]; then
		rm -rf "$tmp"
		die "the Lima archive does not match its checksum"
	fi
	mkdir -p "$tmp/lima"
	tar -xzf "$tmp/lima.tar.gz" -C "$tmp/lima"
	if [ -x "$LIMACTL" ] && [ "$(vm_status)" = Running ]; then
		say "Stopping the VM to update Lima..."
		"$LIMACTL" --tty=false stop "$VM" </dev/null
	fi
	mkdir -p "$ERIPHE_HOME"
	rm -rf "$ERIPHE_HOME/lima"
	mv "$tmp/lima" "$ERIPHE_HOME/lima"
	rm -rf "$tmp"
}

mac_vm() {
	mkdir -p "$LIMA_HOME"
	status=$(vm_status)
	if [ -z "$status" ]; then
		say "Creating the VM $VM (the first start downloads Ubuntu, about 600 MB)..."
		lima_yaml >"$ERIPHE_HOME/$VM.yaml"
		"$LIMACTL" --tty=false create --name "$VM" "$ERIPHE_HOME/$VM.yaml" </dev/null
		status=Stopped
	fi
	if [ "$status" != Running ]; then
		say "Starting the VM $VM..."
		"$LIMACTL" --tty=false start "$VM" </dev/null
	fi
}

agent_plist() {
	cat <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- Written by install.sh: starts Eriphe's VM at login. -->
<plist version="1.0">
<dict>
  <key>Label</key><string>$AGENT_LABEL</string>
  <key>ProgramArguments</key>
  <array>
    <string>$LIMACTL</string><string>--tty=false</string><string>start</string><string>$VM</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict><key>LIMA_HOME</key><string>$LIMA_HOME</string></dict>
  <key>RunAtLoad</key><true/>
  <!-- limactl start leaves the VM's host agent running: launchd keeps it. -->
  <key>AbandonProcessGroup</key><true/>
  <key>StandardOutPath</key><string>$ERIPHE_HOME/vm.log</string>
  <key>StandardErrorPath</key><string>$ERIPHE_HOME/vm.log</string>
</dict>
</plist>
EOF
}

mac_agent() {
	mkdir -p "$(dirname "$AGENT_PLIST")"
	new=$(agent_plist)
	domain=gui/$(id -u)
	if [ -f "$AGENT_PLIST" ] && [ "$(cat "$AGENT_PLIST")" = "$new" ] &&
		launchctl print "$domain/$AGENT_LABEL" >/dev/null 2>&1; then
		return
	fi
	launchctl bootout "$domain/$AGENT_LABEL" >/dev/null 2>&1 || true
	printf '%s\n' "$new" >"$AGENT_PLIST"
	launchctl bootstrap "$domain" "$AGENT_PLIST"
}

mac_uninstall() {
	launchctl bootout "gui/$(id -u)/$AGENT_LABEL" >/dev/null 2>&1 || true
	rm -f "$AGENT_PLIST"
	if [ -x "$LIMACTL" ] && [ -n "$(vm_status)" ]; then
		say "Deleting the VM $VM..."
		"$LIMACTL" --tty=false delete --force "$VM" </dev/null
	fi
	rm -rf "$ERIPHE_HOME"
	# Lima's download cache (the Ubuntu image), unless another Lima uses it.
	if [ ! -d "$HOME/.lima" ] && ! command -v limactl >/dev/null 2>&1; then
		rm -rf "$HOME/Library/Caches/lima"
	fi
}

lan_address() {
	if [ "$OS" = Darwin ]; then
		ifc=$(route -n get default 2>/dev/null | awk '/interface:/ {print $2}')
		ipconfig getifaddr "${ifc:-en0}" 2>/dev/null || true
	else
		ip route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p' | head -n 1
	fi
}

# ---- Main ---------------------------------------------------------------

main() {
	OS=$(uname -s)
	case $OS in
	Linux | Darwin) ;;
	*) die "this installs on Linux and macOS (Windows: node/install/install.ps1)" ;;
	esac
	# Told to eriphe-core and the node: the workspace shows how to update.
	INSTALL=linux
	[ "$OS" = Darwin ] && INSTALL=macos
	parse_args "$@"
	if [ "$OS" = Darwin ]; then
		[ "$(id -u)" != 0 ] || die "on macOS, run this as yourself, not root"
		mac_setup
	fi

	if [ -n "$UNINSTALL" ]; then
		if [ "$OS" = Darwin ]; then
			mac_uninstall
		else
			host_uninstall
		fi
		say "Eriphe is removed from this machine. Delete its node in eriphe-core's workspace (Machines) if it had one."
		return
	fi

	if [ -n "$ROLLBACK" ]; then
		if [ "$OS" = Darwin ]; then
			if [ ! -x "$LIMACTL" ] || [ -z "$(vm_status)" ]; then
				die "Eriphe is not installed on this Mac"
			fi
			[ "$(vm_status)" = Running ] || "$LIMACTL" --tty=false start "$VM" </dev/null
		fi
		host_rollback
		return
	fi

	if [ "$OS" = Darwin ]; then
		major=$(sw_vers -productVersion | cut -d . -f 1)
		[ "$major" -ge 13 ] || die "macOS 13.5 or later is needed (Apple Virtualization)"
		mac_lima
		mac_vm
		mac_agent
	fi

	INFO=$(host_docker)
	settle
	version=$(info docker_version)
	isolated=1
	if [ "${version%%.*}" -lt 28 ] 2>/dev/null; then
		warn "Docker $version is older than 28: the tasks' network cannot hide this machine's own services (update Docker)"
		isolated=0
	fi
	out=$(host_up "$(info docker_gid)" "$isolated")
	if [ "$OS" = Darwin ] && [ "$MODE" = core ]; then
		for port in 8800 8443; do
			other=$(lsof -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | awk 'NR > 1 && $1 != "limactl" {print $1; exit}')
			[ -z "$other" ] || warn "$other already listens on port $port of this Mac: the VM's port $port cannot be forwarded"
		done
	fi

	say ""
	if [ "$MODE" = core ]; then
		addr=$(lan_address)
		code=$(printf '%s\n' "$out" | sed -n 's/^setup_code=//p')
		say "eriphe-core runs here, with node $NAME."
		say "  Workspace: https://${addr:-<this machine>}:8443"
		if [ -n "$code" ]; then
			say "  Setup code: $code (open https://${addr:-<this machine>}:8443/setup)"
		else
			say "  Already set up: sign in with your password."
		fi
		say "  Other machines join it at http://${addr:-<this machine>}:8800 (Add a machine, in the workspace)."
	else
		say "eriphe-node runs here as $NAME and is connected to eriphe-core."
	fi
	if [ -n "$PIN" ]; then
		say "Pinned to $PIN: it stays on that version (--version latest follows the latest one again)."
	elif printf '%s\n' "$out" | grep -qx 'updater=1'; then
		say "It updates itself when eriphe-core asks (Settings, Machines, Updates)."
	else
		warn "no systemd here: this machine does not update itself; run this command again to update it"
	fi
	if [ -n "$SHARES" ]; then
		say "  Places shared with Eriphe: $(printf '%s' "$SHARES" | sed 's/=[^,]*//g; s/,/, /g') (Settings > Folders, in the workspace)."
	elif [ "$OS" = Linux ]; then
		hint="--share documents=/home/<you>/Documents"
		[ -d /mnt/user ] && hint="--share shares=/mnt/user"
		say "  No place shared with Eriphe: run this again with $hint to give it folders."
	fi
	if [ "$OS" = Darwin ]; then
		say "It runs in the VM $VM, started at login. Logs: LIMA_HOME=$LIMA_HOME $LIMACTL shell $VM sudo docker logs eriphe-node"
	else
		say "It starts with Docker at boot. Logs: docker logs eriphe-node"
	fi
}

main "$@"
