#!/bin/sh
# Installs Eriphe on Linux or macOS, bringing its own container runtime:
# nothing to install first, not even Docker.
#
#   # The first machine: eriphe-core here, with a node of its own:
#   curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --core-mode
#   # Another machine, joining it (the workspace's "Add a machine" gives the
#   # whole command, token included):
#   curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --core http://192.0.2.10:8800 --join-token <token> --name laptop
#   # Update (the same command, or no option at all), or remove everything:
#   curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s
#   curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --uninstall
#   # Pin a release, follow main (testers), or go back to the version before
#   # the last update:
#   curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --version 0.2.0
#   curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --version edge
#   curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --rollback
#
# Linux: Docker Engine if it is missing, then a compose project in /opt/eriphe
# (data in /var/lib/eriphe): a socket proxy that only allows the node's Docker
# calls, the node, and eriphe-core in core mode, restarted with Docker at boot.
# macOS on Apple silicon with macOS 26: Apple's container (installed if it is
# missing), eriphe-node on the Mac itself, which runs each task in a micro-VM
# of its own, eriphe-core next to it in core mode, all in ~/.eriphe and
# started at login by LaunchAgents. Older or Intel Macs: Lima in ~/.eriphe, a
# Linux VM "eriphe" (Apple Virtualization) that sees no Mac folder, the same
# compose project inside it, and a LaunchAgent that starts the VM at login.
# Everywhere, an updater that applies the updates eriphe-core asks for (a
# systemd unit, a LaunchAgent on a Mac with Apple's container). See
# install/README.md.
set -eu

# Apple's container, its official signed package (github.com/apple/container).
CONTAINER_VERSION=1.6.1
CONTAINER_SHA256=cd47846819f06478d067060efe21e516b562ac0a9af8eecad6d5fea0b556b200
CONTAINER=/usr/local/bin/container

LIMA_VERSION=2.2.1
LIMA_SHA256_arm64=9e9eacce88f37e185c346bad73aa6136f738d8cdf8c3bb23cd42b071824bc66e
LIMA_SHA256_x86_64=6b776e4bc41f358e6ee01fefcc78e2eb26f11635a0fcff632234af77a566c6c4

REGISTRY=ghcr.io/alexis-marcel
PROXY_IMAGE=wollomatic/socket-proxy:1.13.2

# images: the image references, at the pinned version (--version: a release,
# or edge for main) or latest, the latest release. Fixed here, in the
# installation: eriphe-core never chooses them.
images() {
	tag=${PIN:-latest}
	CORE_IMAGE=${ERIPHE_CORE_IMAGE:-$REGISTRY/eriphe-core:$tag}
	NODE_IMAGE=${ERIPHE_NODE_IMAGE:-$REGISTRY/eriphe-node:$tag}
	WORKER_IMAGE=${ERIPHE_WORKER_IMAGE:-$REGISTRY/eriphe-base:$tag}
}

# On the Linux machine Eriphe runs on: this one, or the VM on macOS.
CONF=/opt/eriphe
DATA=/var/lib/eriphe
CORE_UID=1993 # eriphe-core's user in its image

# On macOS.
VM=eriphe
AGENT_LABEL=org.eriphe.vm

# What it prints. Run as the installer (main), it shows only what a person
# needs: a title, short steps, the outcome and where the logs are (SCREEN,
# file descriptors 5 and 6); everything else, the output of every command
# included, goes to its log (LOG). eriphe-launcher, which sources this file,
# prints it all to its container's log.
SCREEN='' LOG='' NOTES='' ASSISTANT=Eriphe STEPPED=''
say() {
	if [ -n "$SCREEN" ]; then
		printf '%s\n' "$*" >&5
		printf '%s\n' "$*" >>"$LOG"
	else
		printf '%s\n' "$*"
	fi
}
# para TEXT: TEXT as a paragraph, wrapped.
para() { printf '%s\n' "$*" | fold -s -w 66 | sed 's/ *$//' | while IFS= read -r l; do say "$l"; done; }
step() {
	say "  ✓ $1"
	STEPPED=1
}
# note TEXT: a detail, for the log only.
note() { printf '%s\n' "$*" >&2; }
warn() { printf 'eriphe: warning: %s\n' "$*" >&2; }
# notice TEXT: something to know, said after the outcome (Good to know).
notice() {
	[ -z "$SCREEN" ] || warn "$*"
	NOTES="$NOTES$*
"
}
die() {
	printf 'eriphe: %s\n' "$*" >&2
	if [ -n "$SCREEN" ]; then
		m=$*
		case $m in *[.!?]) ;; *) m=$m. ;; esac
		{
			say ""
			para "Something went wrong: $m"
			say ""
			say "Details: $(tilde "$LOG")"
		} 5>&6
	fi
	exit 1
}
tilde() {
	case $1 in
	"$HOME"/*) printf '~%s' "${1#"$HOME"}" ;;
	*) printf '%s' "$1" ;;
	esac
}
# good_to_know: the notices, after the outcome.
good_to_know() {
	[ -n "$NOTES" ] || return 0
	say ""
	if [ "$(printf '%s' "$NOTES" | wc -l)" -le 1 ]; then
		para "Good to know: $(printf '%s' "$NOTES" | head -n 1)"
		return
	fi
	say "Good to know:"
	printf '%s' "$NOTES" | while IFS= read -r n; do
		printf '%s\n' "$n" | awk '{ print toupper(substr($0, 1, 1)) substr($0, 2) }' | fold -s -w 62 | sed 's/ *$//' | {
			IFS= read -r l && say "  - $l"
			while IFS= read -r l; do say "    $l"; done
		}
	done
}
# need_root REASON: says why the password is asked, then asks it once
# (sudo keeps it for a while), unless this runs as root or sudo has it.
need_root() {
	[ "$(id -u)" != 0 ] || return 0
	command -v sudo >/dev/null 2>&1 || die "run this as root (there is no sudo here)"
	sudo -n true 2>/dev/null && return 0
	# Between the steps: apart from them.
	[ -z "$STEPPED" ] || say ""
	if [ "${OS:-}" = Darwin ]; then
		para "macOS will ask for your Mac password: $1."
	else
		para "This computer will ask for your password: $1."
	fi
	# sudo reads the password from the terminal itself: stdin may be this script.
	sudo -v || die "the password was not accepted: run the command again"
	say ""
}
# on_steps CMD...: CMD, a host script: its output in the log, but the steps
# it reports on stderr ("eriphe-step: Downloaded"), shown as checkmarked
# lines; its stdout, key=value lines, in OUT. It stops on CMD's error, with
# the last one it gave ("eriphe: ...").
on_steps() {
	t=$(mktemp)
	{ if "$@" 2>&1 >"$t"; then echo eriphe-done; fi; } | while IFS= read -r l; do
		case $l in
		'eriphe-step: '*) step "${l#eriphe-step: }" ;;
		eriphe-done) echo ok >"$t.ok" ;;
		'eriphe: warning: '*) printf '%s\n' "$l" >&2 ;;
		'eriphe: '*)
			printf '%s\n' "$l" >&2
			printf '%s\n' "${l#eriphe: }" >"$t.err"
			;;
		*) printf '%s\n' "$l" >&2 ;;
		esac
	done
	OUT=$(cat "$t")
	ok=$(cat "$t.ok" 2>/dev/null || true)
	err=$(cat "$t.err" 2>/dev/null || true)
	rm -f "$t" "$t.ok" "$t.err"
	[ -z "$ok" ] || return 0
	# eriphe-launcher: the error is in its log already.
	[ -n "$SCREEN" ] || exit 1
	die "${err:-the installation stopped}"
}
# status_line ACTION OUTCOME FROM TO MESSAGE: how an update or a rollback
# went, in a sentence.
status_line() {
	case $1:$2 in
	update:ok) echo "Updated to $4." ;;
	update:current) echo "Already up to date${4:+ ($4)}." ;;
	update:pinned) echo "Already on ${4:-the version it is pinned to}: it is pinned to that version." ;;
	update:refused | update:skipped) echo "Not updated: $5." ;;
	update:rolled_back) echo "Could not update to $4, so it went back to $3: $5." ;;
	rollback:ok) echo "Back to $4." ;;
	*:rollback_failed) echo "Could not $1, nor go back to $3: $5." ;;
	update:*) echo "Could not update: ${5:-$2}." ;;
	rollback:*) echo "Could not go back to the previous version: ${5:-$2}." ;;
	esac
}
# show_status: status_line of an update-status file, on stdin.
show_status() {
	st=$(cat)
	status_line "$(status_get action)" "$(status_get outcome)" "$(status_get from)" "$(status_get to)" "$(status_get message)"
}
status_get() { printf '%s\n' "$st" | sed -n "s/^$1=//p" | head -n 1; }

usage() {
	cat <<'EOF'
Usage: curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- [options]
       sh install.sh [options]

  --core URL[,URL2]   eriphe-core to join, several addresses preferred first
  --join-token TOKEN  from `eriphe-core join-token -name NAME` (first install only)
  --name NAME         this node's name (default: the host name)
  --labels L1,L2      what agents can require of this node, e.g. gpu
  --ca-hash sha256:H  eriphe-core's CA, to check its certificate
  --share NAME=/PATH  a place of this machine whose folders may be given to
                      Eriphe (repeatable; NAME=/PATH:ro: never written to).
                      Replaces the places of an earlier install. A Mac with
                      Apple's container shares its home, iCloud Drive and
                      its cloud folders by itself; not in Lima's VM
  --no-share          share no place any more
  --assistant-name N  the assistant's name, for what this script prints
  --core-mode         run eriphe-core here, with a node of its own
  --web-port N        core mode: the workspace's port (default 8443, kept
                      for the next runs; env ERIPHE_WEB_PORT)
  --api-port N        core mode: the port other devices join on (default
                      8800, kept; env ERIPHE_API_PORT)
  --bind ADDR         core mode on Linux: the IPv4 address of this machine
                      the workspace and the API listen on (default 0.0.0.0,
                      every IPv4 address; 127.0.0.1 when they are reached
                      only through Tailscale or a reverse proxy here; kept;
                      env ERIPHE_BIND)
  --version V         pin a release (0.2.0, or 0.2 for its fixes), edge to
                      follow main (testers), or latest: the releases again
  --rollback          go back to the version before the last update
  --uninstall         remove everything this script installed
  -h, --help          this help

Run again (with or without the options) to update: new images, same data and
same node identity. Afterwards the machine updates itself when eriphe-core
asks (Settings, Devices, Updates). "Connect a device" in the workspace gives the whole command.
Guide: https://github.com/Alexis-Marcel/eriphe-system/blob/main/install/README.md
EOF
}

# q VALUE: VALUE quoted for sh.
q() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }

as_root() {
	if [ "$(id -u)" = 0 ]; then
		"$@"
	else
		need_root "${ROOT_WHY:-it installs Eriphe for the whole computer}"
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
MODE=$(q "${MODE:-}") NODE_NAME=$(q "${NAME:-}") ASSISTANT=$(q "$ASSISTANT")
IMAGES=$(q "$PROXY_IMAGE ${NODE_IMAGE:-} ${WORKER_IMAGE:-}$([ "${MODE:-}" = core ] && printf ' %s' "${CORE_IMAGE:-}")")
ALL_REPOS=$(q "${PROXY_IMAGE%:*} $REGISTRY/eriphe-node $REGISTRY/eriphe-base $REGISTRY/eriphe-worker $REGISTRY/eriphe-core")
EOF
	cat <<'EOF'
die() { printf 'eriphe: %s\n' "$*" >&2; exit 1; }
dc() { docker compose -p eriphe --project-directory "$CONF" -f "$CONF/compose.yaml" --env-file "$CONF/.env" "$@"; }
EOF
}

parse_args() {
	MODE='' UNINSTALL='' LABELS_SET='' PIN='' PIN_SET='' ROLLBACK='' SHARES='' SHARES_SET='' NO_SHARE=''
	CORE_URL=${ERIPHE_CORE:-}
	JOIN_TOKEN=${ERIPHE_JOIN_TOKEN:-}
	NAME=${ERIPHE_NODE_NAME:-}
	LABELS=${ERIPHE_NODE_LABELS:-}
	CA_HASH=${ERIPHE_CA_HASH:-}
	assistant=${ERIPHE_ASSISTANT_NAME:-}
	WEB_PORT=${ERIPHE_WEB_PORT:-} API_PORT=${ERIPHE_API_PORT:-} PORT_OPT=''
	BIND=${ERIPHE_BIND:-} BIND_OPT='' BIND_SET=''
	[ -n "$LABELS" ] && LABELS_SET=1
	[ -n "$BIND" ] && BIND_SET=1
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
		--core | --join-token | --name | --labels | --ca-hash | --version | --assistant-name | --web-port | --api-port | --bind)
			[ $# -ge 2 ] || die "$1 needs a value"
			case $1 in
			--bind) BIND=$2 BIND_OPT=1 BIND_SET=1 ;;
			--web-port) WEB_PORT=$2 PORT_OPT=1 ;;
			--api-port) API_PORT=$2 PORT_OPT=1 ;;
			--assistant-name) assistant=$2 ;;
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
			SHARES='' SHARES_SET=1 NO_SHARE=1
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
	# Printed only: no control character.
	assistant=$(printf '%s' "$assistant" | tr -d '[:cntrl:]')
	ASSISTANT=${assistant:-Eriphe}
	for v in "$CORE_URL" "$JOIN_TOKEN" "$NAME" "$LABELS" "$CA_HASH" "$SHARES"; do
		case $v in
		*"'"* | *'"'* | *\\* | *'$'* | *' '* | *'
'*) die "an option has a quote, a space, a \$ or a line break: $v" ;;
		esac
	done
	if [ "$MODE" = core ] && [ -n "$CORE_URL" ]; then
		die "--core-mode runs eriphe-core here: --core is for joining another one"
	fi
	# A release (v0.2.0 is 0.2.0), its line (0.2: its latest fixes), edge
	# (main), or a build of main (sha-<commit>).
	case $PIN in
	latest) PIN='' ;;
	'') [ -z "$PIN_SET" ] || die "--version is latest, edge, or a release such as 0.2.0" ;;
	v[0-9]*) PIN=${PIN#v} ;;
	esac
	[ -z "$PIN" ] || printf '%s' "$PIN" | grep -Eq '^(edge|sha-[0-9a-f]{7,40}|(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?)?)$' ||
		die "--version is latest, edge, or a release such as 0.2.0 (0.2 for its fixes), not $PIN"
	if [ -n "$CA_HASH" ]; then
		case $CA_HASH in
		sha256:*) ;;
		*) die "--ca-hash is sha256:<hex>" ;;
		esac
	fi
	# PORTS_SET: the ports were given, not kept from an earlier install.
	PORTS_SET=$WEB_PORT$API_PORT
	for p in $WEB_PORT $API_PORT; do
		if ! printf '%s' "$p" | grep -Eq '^[1-9][0-9]{0,4}$' || [ "$p" -gt 65535 ]; then
			die "a port is 1 to 65535, not $p"
		fi
	done
	if [ "$MODE" = node ]; then
		[ -z "$PORT_OPT" ] || die "--web-port and --api-port are for --core-mode: a node opens no port"
		WEB_PORT='' API_PORT='' PORTS_SET=''
		[ -z "$BIND_OPT" ] || die "--bind is for --core-mode: a node opens no port"
		BIND='' BIND_SET=''
	fi
	if [ -n "$BIND" ]; then
		ipv4 "$BIND" || die "--bind is an IPv4 address of this machine, such as 127.0.0.1 (not $BIND)"
		[ "${OS:-}" != Darwin ] || die "--bind is for Linux: on a Mac, eriphe-core listens on every address"
	fi
	check_shares
}

# ipv4 ADDR: ADDR is an IPv4 address (four numbers of 0 to 255).
ipv4() {
	printf '%s' "$1" | grep -Eq '^[0-9]{1,3}(\.[0-9]{1,3}){3}$' || return 1
	for n in $(printf '%s' "$1" | tr . ' '); do
		[ "$n" -le 255 ] || return 1
	done
}

# public_address ADDR: ADDR can be reached from the Internet: not a private
# address (RFC 1918, IPv6 fc00::/7), nor a shared one (CGNAT and Tailscale's
# 100.64.0.0/10), loopback, link-local or unspecified.
public_address() {
	a=$(printf '%s' "$1" | tr 'A-F' 'a-f' | sed 's/^\[//; s/\]$//')
	case $a in
	'' | 0.0.0.0 | :: | ::1) return 1 ;;
	::ffff:*.*) public_address "${a#::ffff:}" ;;
	fc* | fd* | fe[89ab]*:*) return 1 ;;
	*:*) return 0 ;;
	*)
		ipv4 "$a" || return 1
		o1=${a%%.*} r=${a#*.}
		o2=${r%%.*}
		case $o1 in
		0 | 10 | 127) return 1 ;;
		169) [ "$o2" != 254 ] ;;
		172) [ "$o2" -lt 16 ] || [ "$o2" -gt 31 ] ;;
		192) [ "$o2" != 168 ] ;;
		100) [ "$o2" -lt 64 ] || [ "$o2" -gt 127 ] ;;
		*) return 0 ;;
		esac
		;;
	esac
}

# host_tz: this machine's time zone (an IANA name), or nothing.
host_tz() {
	tz=''
	if command -v timedatectl >/dev/null 2>&1; then
		tz=$(timedatectl show -p Timezone --value 2>/dev/null || true)
	fi
	[ -n "$tz" ] || tz=$(sed -n 1p /etc/timezone 2>/dev/null || true)
	if [ -z "$tz" ] && [ -L /etc/localtime ]; then
		tz=$(readlink /etc/localtime 2>/dev/null || true)
		case $tz in
		*zoneinfo/*) tz=${tz#*zoneinfo/} ;;
		*) tz='' ;;
		esac
	fi
	if printf '%s' "$tz" | grep -Eq '^[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+)*$'; then
		printf '%s' "$tz"
	fi
}

# exposed_notice ADDR [HOW]: what to do when the address the workspace is
# printed with is public (a server rented in the cloud): it is reachable from
# the Internet. HOW: how to listen on 127.0.0.1 only. On 127.0.0.1 (--bind),
# how to reach it.
exposed_notice() {
	case $1 in
	127.*)
		notice "Eriphe listens on this machine's own address only (--bind): from another computer, open the workspace through an SSH tunnel (ssh -L $WEB_PORT:127.0.0.1:$WEB_PORT <user>@<this-machine>, then https://127.0.0.1:$WEB_PORT), then turn on Tailscale in Réglages › Accès à distance (Settings › Remote access), or put it behind a reverse proxy on this machine."
		return 0
		;;
	esac
	public_address "$1" || return 0
	notice "$1 is a public address: the workspace (port $WEB_PORT) and the API (port $API_PORT) can be reached from the Internet. Close them in your cloud provider's firewall, then open the workspace through Tailscale (Réglages › Accès à distance, Settings › Remote access) or your own domain behind a reverse proxy; open $API_PORT only if devices join over the Internet. UFW does not filter Docker's ports: use the DOCKER-USER chain, or ${2:-run this again with --bind 127.0.0.1}. Guide: https://github.com/Alexis-Marcel/eriphe-system/blob/main/install/README.md#eriphe-on-a-vps"
}

# check_ports: the workspace's and the API's ports differ.
check_ports() {
	[ "$WEB_PORT" != "$API_PORT" ] || die "the workspace and the API need two ports, not $WEB_PORT twice (--web-port, --api-port)"
}

# port_user PORT: the program that listens on PORT of this machine, if any
# ("a program" when this user may not see which one); nothing if it is free.
port_user() {
	if [ "$OS" = Darwin ]; then
		lsof -nP -iTCP:"$1" -sTCP:LISTEN 2>/dev/null | awk 'NR > 1 {print $1; exit}'
	elif command -v ss >/dev/null 2>&1; then
		ss -Hltnp "sport = :$1" 2>/dev/null | head -n 1 | awk '{
			if (match($0, /users:\(\("[^"]*"/)) print substr($0, RSTART + 9, RLENGTH - 10); else if (NF) print "a program" }'
	else
		hex=$(printf '%04X' "$1")
		cat /proc/net/tcp /proc/net/tcp6 2>/dev/null | awk -v p=":$hex" '$2 ~ p "$" && $4 == "0A" { print "a program"; exit }'
	fi
}

# ports_free OURS: says plainly, before anything is installed, when another
# program holds a port eriphe-core needs, and which option to use instead.
# OURS: the name of what holds them for an earlier install of Eriphe here.
ports_free() {
	for p in "$WEB_PORT:--web-port:9443" "$API_PORT:--api-port:9800"; do
		port=${p%%:*} opt=${p#*:} alt=${opt#*:} opt=${opt%:*}
		[ -n "$port" ] || continue # not given, and kept from an earlier install
		who=$(port_user "$port")
		[ -n "$who" ] || continue
		case " $1 " in *" $who "*) continue ;; esac
		[ "$who" != "a program" ] || who="another program"
		die "$who already uses port $port of $HERE, which Eriphe needs. Run the same command again with $opt $alt (or another free port), or stop that program"
	done
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
	if [ "$OS" = Darwin ] && [ -z "$NATIVE" ] && [ -n "$SHARES" ]; then
		# The VM sees no Mac folder.
		notice "Eriphe cannot open this Mac's folders: that needs a Mac with Apple silicon and macOS 26."
		SHARES=''
	fi
	[ -n "$WEB_PORT" ] || WEB_PORT=$(saved web_port)
	[ -n "$API_PORT" ] || API_PORT=$(saved api_port)
	WEB_PORT=${WEB_PORT:-8443} API_PORT=${API_PORT:-8800}
	check_ports
	CORE_TZ=''
	if [ "$MODE" = core ]; then
		[ -n "$BIND_SET" ] || BIND=$(saved bind)
		[ "$BIND" != 0.0.0.0 ] || BIND=''
		# eriphe-core's time zone: TZ if given, else the one kept, else this
		# machine's on a new install only (an older one keeps eriphe-core's
		# own default). In eriphe-launcher's container, whose zone is not the
		# host's, TZ alone says it.
		if [ -n "${TZ:-}" ] || [ -n "${HOST_DATA:-}" ]; then
			CORE_TZ=${TZ:-}
		else
			CORE_TZ=$(saved tz)
			if [ -z "$CORE_TZ" ] && [ -z "$(saved mode)" ]; then
				CORE_TZ=$(host_tz)
			fi
		fi
		CORE_URL=http://eriphe-core:8800
		# eriphe-core on the Mac itself, next to the node.
		[ -z "$NATIVE" ] || CORE_URL=http://127.0.0.1:$API_PORT
	fi
	printf '%s' "$NAME" | grep -Eq '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$' ||
		die "the node's name is lowercase letters, digits and dashes: --name pc"
	joined=$(info joined)
	joined_as=$(info joined_as)
	if [ -n "$joined" ]; then
		if [ -n "$joined_as" ] && [ "$joined_as" != "$NAME" ]; then
			notice "this machine joined as $joined_as and keeps that name."
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
    echo "eriphe-step: Docker installed"
  } >&2
fi
# Docker starts at boot; the containers restart with it.
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  systemctl enable --now docker.service >&2 || die "Docker does not start (systemctl enable docker failed)"
elif command -v rc-update >/dev/null 2>&1; then
  rc-update add docker default >/dev/null 2>&1 || true
  rc-service docker start >/dev/null 2>&1 || true
fi
i=0
until docker info >/dev/null 2>&1; do
  i=$((i + 1)); [ $i -le 60 ] || die "Docker does not start (docker info does not answer)"; sleep 1
done
docker compose version >/dev/null 2>&1 || die "Docker Compose is missing (the docker-compose-plugin package)"
[ -S /var/run/docker.sock ] || die "no /var/run/docker.sock (rootless Docker is not supported)"
echo "docker_gid=$(stat -c %g /var/run/docker.sock)"
echo "docker_version=$(docker version --format '{{.Server.Version}}')"
if [ -f "$DATA/node/node.json" ]; then
  echo joined=1
  echo "joined_as=$(sed -n 's/.*"name": *"\([^"]*\)".*/\1/p' "$DATA/node/node.json" | head -n 1)"
fi
# The version running, to say what an update changes.
c=eriphe-node
[ "$(sed -n 's/^mode=//p' "$CONF/install.conf" 2>/dev/null)" = core ] && c=eriphe-core
echo "running_version=$(docker inspect -f '{{index .Config.Labels "org.opencontainers.image.version"}}' "$c" 2>/dev/null | sed 's/<no value>//')"
[ -f "$CONF/install.conf" ] && sed 's/^/saved_/' "$CONF/install.conf"
true
EOF
	} | on_host
}

# compose_yaml: the compose project, as deploy/nas/compose.yaml with this
# machine's paths and Docker group. HOST_DATA is the data folder as Docker
# sees it when this runs in a container (eriphe-launcher, which sees it at
# /data); API_PORT and WEB_PORT the ports eriphe-core is published on.
compose_yaml() {
	gid=$1 isolated=$2
	data=${HOST_DATA:-$DATA}
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
    # IPv4 only: on [::], Docker's userland proxy would hand eriphe-core
    # every IPv6 client with the bridge's address instead of its own.
    ports: ["${BIND:-0.0.0.0}:${API_PORT:-8800}:8800", "${BIND:-0.0.0.0}:${WEB_PORT:-8443}:8443"]
    environment:
      # The workspace shows the command that updates it.
      ERIPHE_INSTALL: $INSTALL
EOF
		# The join commands give the port the API is published on.
		[ -z "${API_PORT:-}" ] || [ "$API_PORT" = 8800 ] || printf '      ERIPHE_API_PORT: "%s"\n' "$API_PORT"
		[ -z "${CORE_TZ:-}" ] || printf '      TZ: %s\n' "$(q "$CORE_TZ")"
		cat <<EOF
    volumes:
      - $data/core:/data
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
      - -allowbindmountfrom=$data/node$(share_paths | sed 's/:ro$//' | tr '\n' ',' | sed 's/,$//; s/^./,&/')
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
      # Its network card, so that eriphe-core can wake it (wake_card).
      ERIPHE_WAKE_MAC: \${ERIPHE_WAKE_MAC:-}
      ERIPHE_WAKE_BROADCAST: \${ERIPHE_WAKE_BROADCAST:-}
      # Only for the first start, from install.sh, never kept in .env.
      ERIPHE_JOIN_TOKEN: \${ERIPHE_JOIN_TOKEN:-}
      ERIPHE_INSTALL: $INSTALL
      DOCKER_HOST: tcp://eriphe-docker-proxy:2375
    command: ["-root", "$data/node",
              "-network", "eriphe-system", "-task-network", "eriphe-tasks",
              "-callback-url", "http://eriphe-node:8790",
              "-image", "$WORKER_IMAGE"$([ "$MODE" = core ] && printf ', "-always-on"')]
    volumes:
      # Same path inside and outside: the Docker daemon understands it as is.
      - $data/node:$data/node
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

# card_to: the network card this machine reaches $1 through (an IPv4
# address, or default): its MAC address and its network's broadcast address,
# or nothing for a card with no Ethernet address (the loopback, a VPN's).
card_to() {
	if [ "$(uname -s)" = Darwin ]; then
		ifc=$(route -n get "$1" 2>/dev/null | awk '/interface:/ {print $2}')
		[ -n "$ifc" ] || return 0
		mac=$(ifconfig "$ifc" 2>/dev/null | awk '/ether / {print $2; exit}')
		bcast=$(ifconfig "$ifc" 2>/dev/null | awk '/inet / {for (i = 1; i < NF; i++) if ($i == "broadcast") {print $(i + 1); exit}}')
	else
		[ "$1" != default ] || set -- 1.1.1.1
		ifc=$(ip route get "$1" 2>/dev/null | sed -n 's/.* dev \([^ ]*\).*/\1/p' | head -n 1)
		[ -n "$ifc" ] || return 0
		mac=$(cat "/sys/class/net/$ifc/address" 2>/dev/null || true)
		bcast=$(ip -4 addr show dev "$ifc" 2>/dev/null | sed -n 's/.* brd \([0-9.]*\).*/\1/p' | head -n 1)
	fi
	mac=$(printf '%s' "$mac" | tr 'A-F' 'a-f')
	printf '%s' "$mac" | grep -Eq '^([0-9a-f]{2}:){5}[0-9a-f]{2}$' || return 0
	[ "$mac" != 00:00:00:00:00:00 ] || return 0
	printf '%s %s' "$mac" "$bcast"
}

# wake_card: this machine's card towards eriphe-core, so that eriphe-core
# can wake it (Wake-on-LAN, node/wake.go): the card of the route to
# eriphe-core's address when it is an IPv4 address, else (a name, the
# loopback, a VPN) of the default route. eriphe-launcher has its own, from
# the host's network.
wake_card() {
	host=$(printf '%s' "${CORE_URL%%,*}" | sed -n 's|^[a-z]*://\([0-9][0-9.]*\)\([:/].*\)\{0,1\}$|\1|p')
	for to in $host default; do
		card=$(card_to "$to")
		if [ -n "$card" ]; then
			printf '%s' "$card"
			return
		fi
	done
}

# wake_settings sets WAKE_MAC and WAKE_BROADCAST, once.
wake_settings() {
	[ -z "${WAKE_DONE:-}" ] || return 0
	card=$(wake_card || true)
	WAKE_MAC=$(printf '%s' "$card" | cut -d ' ' -f 1)
	WAKE_BROADCAST=$(printf '%s' "$card" | cut -s -d ' ' -f 2)
	WAKE_DONE=1
}

env_file() {
	wake_settings
	cat <<EOF
ERIPHE_CORE='$CORE_URL'
ERIPHE_NODE_NAME='$NAME'
ERIPHE_NODE_LABELS='$LABELS'
ERIPHE_CA_HASH='$CA_HASH'
ERIPHE_NODE_SHARES='$SHARES'
ERIPHE_WAKE_MAC='$WAKE_MAC'
ERIPHE_WAKE_BROADCAST='$WAKE_BROADCAST'
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
web_port=$WEB_PORT
api_port=$API_PORT
bind=${BIND:-}
tz=${CORE_TZ:-}
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
. "$(dirname "$0")/update.conf"
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
# tag_of VERSION: the images' tag of a build: X.Y.Z for a release,
# sha-<commit> for a build of main (X.Y.Z-dev+<commit>); none for a local one.
tag_of() {
  case $1 in
    *-dev+*) t=sha-${1##*+} ;;
    *+*) t= ;;
    *) t=$1 ;;
  esac
  printf '%s' "$t" | grep -Eq '^(sha-[0-9a-f]{7,40}|[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?)$' && printf '%s' "$t"
}
release() { printf '%s' "$1" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; }
# newer A B: release A is newer than release B.
newer() {
  printf '%s %s\n' "$1" "$2" | awk '{ split($1, a, "."); split($2, b, ".")
    for (i = 1; i <= 3; i++) { if (a[i] + 0 > b[i] + 0) exit 0; if (a[i] + 0 < b[i] + 0) exit 1 }
    exit 1 }'
}
# pinned: on a release of its own choice (edge follows main instead).
pinned() { [ -n "$PIN" ] && [ "$PIN" != edge ]; }

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
  # eriphe-core's machine takes eriphe-core's build (latest, edge or the
  # pinned one), and the node and worker of that same build (the tag of its
  # version), not their own latest: latest may be half published, and the
  # node is never newer than eriphe-core. A node alone takes eriphe-core's
  # build too, unless it is pinned to a release.
  match=
  if [ "$target" = node ] && ! pinned; then match=$(tag_of "$core"); fi
  for ref in $(printf '%s\n' "$before" | awk '{ print $2 }'); do
    src=$ref
    case $match:$ref in
      ?*:*/eriphe-node:* | ?*:*/eriphe-base:* | ?*:*/eriphe-worker:*) src=$(repo "$ref"):$match ;;
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
    case $target:$match:$ref in
      core::*/eriphe-core:*) match=$(tag_of "$(version "$(image_id "$ref")")") ;;
    esac
  done
  after=$(current "$target" | while read -r svc ref _; do echo "$svc $ref $(image_id "$ref")"; done)
  new=$(main_id "$after" "$main")
  to=$(version "$new")
  if [ "$after" = "$before" ]; then
    if pinned; then
      status "$target" "$id" update pinned "pinned to $PIN" "$from" "$to"
    else
      status "$target" "$id" update current "" "$from" "$to"
    fi
    return
  fi
  # A node is never newer than eriphe-core (a release pinned here may be).
  if [ "$target" = node ] && release "$core" && release "$to" && newer "$to" "$core"; then
    put "$before"
    status "$target" "$id" update refused "eriphe-core runs $core: $to would be newer" "$from" "$to"
    return
  fi
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
  printf '%s' "$core" | grep -Eq '^(sha-[0-9a-f]{7,40}|dev|[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?)?$' || core=
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
UNIT_AWAKE_SERVICE=$(printf '%s\n' "[Unit]" "Description=Eriphe: no sleep while a task is at work" "" "[Service]" "Type=simple" \
		"ExecStart=/usr/bin/systemd-inhibit --what=sleep:idle --who=Eriphe \"--why=A task is at work\" --mode=block /bin/sh -c 'while [ -e \"$DATA/node/awake\" ]; do sleep 10; done'" | b64)
UNIT_AWAKE_PATH=$(printf '%s\n' "[Unit]" "Description=Eriphe: a task at work keeps the machine awake (eriphe-node writes awake)" "" "[Path]" \
		"PathExists=$DATA/node/awake" "Unit=eriphe-awake.service" "" "[Install]" "WantedBy=multi-user.target" | b64)
EOF
}

# host_up writes the project, pulls the images and starts it, joining the
# node if it has not joined yet. In core mode it prints the setup code.
host_up() {
	{
		prelude
		printf 'JOIN_TOKEN=%s\n' "$(q "$JOIN_TOKEN")"
		# In eriphe-launcher's container the shares are not here: it checks them itself.
		printf 'SHARE_PATHS=%s\n' "$(q "$([ -n "${HOST_DATA:-}" ] || share_paths | sed 's/:ro$//' | tr '\n' ' ')")"
		printf 'COMPOSE=%s\n' "$(compose_yaml "$1" "$2" | b64)"
		printf 'ENVF=%s\n' "$(env_file | b64)"
		printf 'INSTALL_CONF=%s\n' "$(install_conf | b64)"
		printf 'UPDATE_CONF=%s\n' "$(updater_conf | b64)"
		printf 'UPDATE_SH=%s\n' "$(updater_sh | b64)"
		printf 'PIN=%s\n' "$(q "$PIN")"
		# PULL=missing pulls only the images not here yet (eriphe-launcher's
		# start: updates go through the updater); UPDATE_BY=loop: the updater is
		# eriphe-launcher's loop, not systemd.
		printf 'PULL=%s UPDATE_BY=%s\n' "$(q "${PULL:-always}")" "$(q "${UPDATE_BY:-systemd}")"
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
  if [ "$PULL" = missing ] && docker image inspect "$img" >/dev/null 2>&1; then continue; fi
  echo "Pulling $img..."
  if ! out=$(docker pull "$img" 2>&1); then
    printf '%s\n' "$out"
    out=$(printf '%s\n' "$out" | tail -n 1)
    if docker image inspect "$img" >/dev/null 2>&1; then
      echo "eriphe: warning: could not pull $img ($out): using the copy already here"
    else
      die "could not download Eriphe: check this computer's Internet connection, then run the command again"
    fi
  fi
done
echo "eriphe-step: Downloaded"

up() { dc up -d --pull never --remove-orphans "$@"; }
if [ ! -f "$DATA/node/node.json" ] && [ "$MODE" = core ]; then
  up core docker-proxy
  echo "Waiting for eriphe-core..."
  i=0
  # It prints the command that joins a node, with the token and eriphe-core's
  # CA (-ca-hash); it fails until eriphe-core answers.
  until joining=$(docker exec eriphe-core eriphe-core join-token -data /data -name "$NODE_NAME" 2>/dev/null) &&
      JOIN_TOKEN=$(printf '%s\n' "$joining" | sed -n 's/.* -join-token \([^ ]*\).*/\1/p' | tail -n 1) &&
      [ -n "$JOIN_TOKEN" ]; do
    i=$((i + 1))
    if [ $i -gt 60 ]; then
      docker logs --tail 20 eriphe-core
      die "Eriphe did not start (eriphe-core gives no join token): run the command again"
    fi
    sleep 2
  done
  # A new eriphe-core accepts machines over TLS only, from the start: the
  # node joins with its CA's hash, kept for the next runs.
  ca=$(printf '%s\n' "$joining" | sed -n 's/.* -ca-hash \(sha256:[0-9a-f]*\).*/\1/p' | tail -n 1)
  if [ -n "$ca" ]; then
    sed "s/^ERIPHE_CA_HASH=.*/ERIPHE_CA_HASH='$ca'/" "$CONF/.env" >"$CONF/.env.new" && mv "$CONF/.env.new" "$CONF/.env"
    sed "s/^ca_hash=.*/ca_hash=$ca/" "$CONF/install.conf" >"$CONF/install.conf.new" && mv "$CONF/install.conf.new" "$CONF/install.conf"
  fi
fi
connected() {
  echo "Waiting for eriphe-node..."
  i=0
  until docker logs eriphe-node 2>&1 | grep -q 'connected to '; do
    i=$((i + 1))
    state=$(docker inspect -f '{{.State.Status}} {{.RestartCount}}' eriphe-node 2>/dev/null || echo missing)
    case $state in
      "running 0") ;;
      *) [ $i -lt 3 ] || { docker logs --tail 20 eriphe-node; die "Eriphe did not start (eriphe-node: $state): run the command again"; } ;;
    esac
    if [ $i -gt 90 ]; then
      docker logs --tail 20 eriphe-node
      [ "$MODE" = core ] && die "Eriphe did not start in 3 minutes: run the command again"
      die "this computer could not connect to $ASSISTANT in 3 minutes: check that the computer Eriphe runs on is on, then run the command again"
    fi
    sleep 2
  done
}
if [ -f "$DATA/node/node.json" ]; then
  up
  echo "eriphe-step: Installed"
  connected
else
  ERIPHE_JOIN_TOKEN=$JOIN_TOKEN up
  echo "eriphe-step: Installed"
  connected
  # Joined: the node has its own secret. Recreated without the join token,
  # which stays nowhere, not even in the container's configuration.
  [ -f "$DATA/node/node.json" ] || die "eriphe-node is connected but has no node.json"
  up
  connected
fi
docker logs eriphe-node 2>&1 | grep 'connected to ' | tail -n 1 | sed 's/^[0-9/]* [0-9:]* /eriphe-node: /'
if [ "$MODE" = core ]; then
  echo "eriphe-step: Started"
  # The setup code, while there is no password (kept until it is used); an
  # older eriphe-core has no auth setup-code: its log says it.
  if code=$(docker exec eriphe-core eriphe-core auth setup-code 2>"$CONF/setup-code.err"); then
    code=$(printf '%s' "$code" | tr -d '[:space:]')
  elif grep -q 'has a password' "$CONF/setup-code.err"; then
    code=
  else
    code=$(docker logs eriphe-core 2>&1 | sed -n 's/.*with the setup code \([A-Za-z0-9-]*\).*/\1/p' | tail -n 1)
  fi
  rm -f "$CONF/setup-code.err"
  echo "setup_code=$code" >&3
  c=eriphe-core
else
  echo "eriphe-step: Connected"
  c=eriphe-node
fi
echo "version=$(docker inspect -f '{{index .Config.Labels "org.opencontainers.image.version"}}' "$c" 2>/dev/null | sed 's/<no value>//')" >&3

# The updater, and the word to eriphe-core and the node that it is there.
put "$CONF/update.conf" "$UPDATE_CONF"
put "$CONF/update.sh" "$UPDATE_SH"
chmod 700 "$CONF/update.sh"
marks="$DATA/node"
[ "$MODE" = core ] && marks="$marks $DATA/core"
# pin: the release it stays on; channel: edge when it follows main.
pin=$PIN channel=
[ "$PIN" != edge ] || pin='' channel=edge
updater=0
if [ "$UPDATE_BY" = loop ]; then
  updater=1
elif command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  updater=1
  put /etc/systemd/system/eriphe-update.service "$UNIT_SERVICE"
  put /etc/systemd/system/eriphe-update.path "$UNIT_PATH"
  put /etc/systemd/system/eriphe-update.timer "$UNIT_TIMER"
  chmod 644 /etc/systemd/system/eriphe-update.service /etc/systemd/system/eriphe-update.path /etc/systemd/system/eriphe-update.timer
  systemctl daemon-reload
  systemctl enable --now eriphe-update.path eriphe-update.timer >/dev/null 2>&1 || die "systemctl enable eriphe-update failed"
  # No sleep while a task is at work: the node, in its container, writes
  # $DATA/node/awake meanwhile (node/README.md, Sleep).
  if [ -x /usr/bin/systemd-inhibit ]; then
    put /etc/systemd/system/eriphe-awake.service "$UNIT_AWAKE_SERVICE"
    put /etc/systemd/system/eriphe-awake.path "$UNIT_AWAKE_PATH"
    chmod 644 /etc/systemd/system/eriphe-awake.service /etc/systemd/system/eriphe-awake.path
    systemctl daemon-reload
    systemctl enable --now eriphe-awake.path >/dev/null 2>&1 || echo "Eriphe cannot keep this machine awake during a task (systemctl enable eriphe-awake.path failed)"
  fi
fi
for d in $marks; do
  if [ $updater = 1 ]; then
    printf 'version=1\npin=%s\nchannel=%s\n' "$pin" "$channel" > "$d/updater"
  else
    rm -f "$d/updater"
  fi
done
[ $updater = 0 ] || [ "$MODE" != core ] || chown "$CORE_UID:$CORE_UID" "$DATA/core/updater"
echo "updater=$updater" >&3
EOF
	} | on_host
}

host_uninstall() {
	{
		prelude
		cat <<'EOF'
# Progress on stderr; stdout carries only key=value lines.
exec 3>&1 >&2
echo "mode=$(sed -n 's/^mode=//p' "$CONF/install.conf" 2>/dev/null)" >&3
if command -v systemctl >/dev/null 2>&1 && [ -f /etc/systemd/system/eriphe-update.path ]; then
  systemctl disable --now eriphe-update.path eriphe-update.timer eriphe-update.service >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/eriphe-update.service /etc/systemd/system/eriphe-update.path /etc/systemd/system/eriphe-update.timer
  systemctl disable --now eriphe-awake.path eriphe-awake.service >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/eriphe-awake.service /etc/systemd/system/eriphe-awake.path
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
rm -f /var/log/eriphe-install.log
if [ -n "$installed" ]; then
  if [ -n "$(docker ps -aq 2>/dev/null)" ]; then
    echo docker_kept=1 >&3
  else
    echo "Removing Docker Engine (installed for Eriphe)..."
    if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
      systemctl disable --now docker.service docker.socket containerd.service >/dev/null 2>&1 || true
    fi
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
    # The group Docker's packages made, which they leave behind.
    if grep -q '^docker:' /etc/group; then
      groupdel docker >/dev/null 2>&1 || delgroup docker >/dev/null 2>&1 || true
      ! grep -q '^docker:' /etc/group || echo docker_group_kept=1 >&3
    fi
  fi
fi
echo "eriphe-step: Removed"
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
sh "$CONF/update.sh" rollback >&2
# shellcheck disable=SC1091
MODE=$(. "$CONF/update.conf"; echo "$MODE")
target=node
[ "$MODE" = core ] && target=core
cat "$DATA/$target/update-status"
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
# (8800 and 8443, or --api-port and --web-port), when it runs here, are
# forwarded, to every address of the Mac.
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
- guestPort: ${API_PORT:-8800}
  guestIP: 0.0.0.0
  hostIP: 0.0.0.0
- guestPort: ${WEB_PORT:-8443}
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

# lima_ports: the API's and the workspace's ports the VM forwards, as its
# lima.yaml says ("8800 8443").
lima_ports() {
	sed -n 's/^- guestPort: \([0-9][0-9]*\)$/\1/p' "$LIMA_HOME/$VM/lima.yaml" 2>/dev/null | head -n 2 | tr '\n' ' ' | sed 's/ $//'
}

mac_lima() {
	case $(uname -m) in
	arm64) arch=arm64 sum=$LIMA_SHA256_arm64 ;;
	x86_64) arch=x86_64 sum=$LIMA_SHA256_x86_64 ;;
	*) die "unsupported Mac: $(uname -m)" ;;
	esac
	if [ -x "$LIMACTL" ] && [ "$("$LIMACTL" --version 2>/dev/null)" = "limactl version $LIMA_VERSION" ]; then
		return
	fi
	note "Downloading Lima $LIMA_VERSION..."
	tmp=$(mktemp -d)
	curl -fsSL -o "$tmp/lima.tar.gz" \
		"https://github.com/lima-vm/lima/releases/download/v$LIMA_VERSION/lima-$LIMA_VERSION-Darwin-$arch.tar.gz" ||
		die "could not download Lima: check this Mac's Internet connection, then run the command again"
	got=$(shasum -a 256 "$tmp/lima.tar.gz" | cut -d ' ' -f 1)
	if [ "$got" != "$sum" ]; then
		rm -rf "$tmp"
		die "the Lima archive does not match its checksum"
	fi
	mkdir -p "$tmp/lima"
	tar -xzf "$tmp/lima.tar.gz" -C "$tmp/lima"
	if [ -x "$LIMACTL" ] && [ "$(vm_status)" = Running ]; then
		note "Stopping the VM to update Lima..."
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
		note "Creating the VM $VM (the first start downloads Ubuntu, about 600 MB)..."
		lima_yaml >"$ERIPHE_HOME/$VM.yaml"
		"$LIMACTL" --tty=false create --name "$VM" "$ERIPHE_HOME/$VM.yaml" </dev/null ||
			die "could not create Eriphe's virtual machine: run the command again"
		status=Stopped
	elif [ -n "$PORTS_SET" ] && [ "$(lima_ports)" != "$API_PORT $WEB_PORT" ]; then
		# Other ports: the VM forwards them from its next start.
		note "Forwarding ports $API_PORT and $WEB_PORT from the VM..."
		[ "$status" != Running ] || "$LIMACTL" --tty=false stop "$VM" </dev/null
		f=$LIMA_HOME/$VM/lima.yaml
		awk -v a="$API_PORT" -v w="$WEB_PORT" '/^- guestPort: [0-9]+$/ && n < 2 { n++; print "- guestPort: " (n == 1 ? a : w); next } { print }' \
			"$f" >"$f.new" && mv "$f.new" "$f"
		lima_yaml >"$ERIPHE_HOME/$VM.yaml"
		status=Stopped
	fi
	if [ "$status" != Running ]; then
		note "Starting the VM $VM..."
		"$LIMACTL" --tty=false start "$VM" </dev/null ||
			die "Eriphe's virtual machine does not start: run the command again"
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
		note "Deleting the VM $VM..."
		"$LIMACTL" --tty=false delete --force "$VM" </dev/null
	fi
	rm -rf "$ERIPHE_HOME"
	# Lima's download cache (the Ubuntu image), unless another Lima uses it.
	if [ ! -d "$HOME/.lima" ] && ! command -v limactl >/dev/null 2>&1; then
		rm -rf "$HOME/Library/Caches/lima"
	fi
	step Removed
}

# ---- macOS with Apple's container -----------------------------------------
#
# On Apple silicon with macOS 26, eriphe-node runs on the Mac itself, as the
# user, and starts each task as a micro-VM of Apple's container (node/README.md,
# The container engine): a task sees only its own folders, mounted when it
# starts, and macOS asks once, for "container-runtime-linux" (Apple's), when a
# folder of Documents, the Desktop or iCloud Drive is first given to one.
# Everything is in ~/.eriphe: bin/ (eriphe-node, eriphe-core in core mode,
# taken from their public images), node/ (the node's root), core/ (eriphe-core's
# data), and LaunchAgents start them at login.

NODE_LABEL=org.eriphe.node
CORE_LABEL=org.eriphe.core
UPDATE_LABEL=org.eriphe.update

# mac_native tells whether this Mac runs Eriphe with Apple's container: Apple
# silicon, macOS 26 or later, and no install in Lima's VM already (it stays
# there; --uninstall first to move). ERIPHE_MAC_LIMA=1 keeps to Lima.
mac_native() {
	[ -z "${ERIPHE_MAC_LIMA:-}" ] || return 1
	[ "$(uname -m)" = arm64 ] || return 1
	[ "$(sw_vers -productVersion | cut -d . -f 1)" -ge 26 ] || return 1
	[ -f "$ERIPHE_HOME/native" ] || [ ! -d "$ERIPHE_HOME/vm" ]
}

# version_ge A B: version A is B or later.
version_ge() {
	printf '%s\n%s\n' "$1" "$2" | awk -F. '
		NR == 1 { for (i = 1; i <= 3; i++) a[i] = $i + 0 }
		NR == 2 { for (i = 1; i <= 3; i++) { b = $i + 0; if (a[i] != b) exit !(a[i] > b) } }'
}

# container_current: Apple's container is here, CONTAINER_VERSION or later
# (have: its version, if any).
container_current() {
	have=$("$CONTAINER" --version 2>/dev/null | sed -n 's/.*version \([0-9][0-9.]*\).*/\1/p')
	[ -n "$have" ] && version_ge "$have" "$CONTAINER_VERSION"
}

CONTAINER_WHY="it installs a tool from Apple that Eriphe needs"

# native_container installs Apple's container if it is missing or older than
# CONTAINER_VERSION, from its signed package (sudo: macOS asks for the
# password), and starts its services, with their Linux kernel.
native_container() {
	mkdir -p "$ERIPHE_HOME"
	if ! container_current; then
		note "Installing Apple's container $CONTAINER_VERSION..."
		need_root "$CONTAINER_WHY"
		tmp=$(mktemp -d)
		curl -fsSL -o "$tmp/container.pkg" \
			"https://github.com/apple/container/releases/download/$CONTAINER_VERSION/container-$CONTAINER_VERSION-installer-signed.pkg" ||
			die "could not download Apple's container: check this Mac's Internet connection, then run the command again"
		got=$(shasum -a 256 "$tmp/container.pkg" | cut -d ' ' -f 1)
		if [ "$got" != "$CONTAINER_SHA256" ] ||
			! pkgutil --check-signature "$tmp/container.pkg" | grep -q 'Developer ID Installer: Apple Inc'; then
			rm -rf "$tmp"
			die "Apple's container package does not match its checksum or Apple's signature"
		fi
		sudo installer -pkg "$tmp/container.pkg" -target / || die "the package of Apple's container did not install"
		rm -rf "$tmp"
		# Removed with Eriphe only if Eriphe brought it.
		[ -n "$have" ] || touch "$ERIPHE_HOME/container-installed"
	fi
	"$CONTAINER" system start --enable-kernel-install </dev/null ||
		die "Apple's container does not start ($CONTAINER system start): restart the Mac, then run the command again"
}

# native_info is what an earlier install left, as host_docker reports it.
native_info() {
	if [ -f "$ERIPHE_HOME/node/node.json" ]; then
		echo joined=1
		echo "joined_as=$(sed -n 's/.*"name": *"\([^"]*\)".*/\1/p' "$ERIPHE_HOME/node/node.json" | head -n 1)"
	fi
	[ -f "$ERIPHE_HOME/install.conf" ] && sed 's/^/saved_/' "$ERIPHE_HOME/install.conf"
	true
}

# mac_shares: the places of this Mac whose folders may be given to Eriphe:
# the home folder, iCloud Drive and the clouds' folders (Google Drive for
# desktop, OneDrive, Dropbox… in ~/Library/CloudStorage), then the --share
# ones. None with --no-share.
mac_shares() {
	[ "$AUTO_SHARES" = 1 ] || {
		printf '%s' "$SHARES"
		return
	}
	out="home=$HOME"
	names=' home '
	icloud="$HOME/Library/Mobile Documents/com~apple~CloudDocs"
	[ -d "$icloud" ] && out="$out,icloud=$icloud" names="$names icloud "
	for d in "$HOME"/Library/CloudStorage/*; do
		[ -d "$d" ] || continue
		case $d in *,*) continue ;; esac
		b=$(basename "$d")
		case $b in
		GoogleDrive*) n=googledrive ;;
		OneDrive*) n=onedrive ;;
		Dropbox*) n=dropbox ;;
		Box*) n=box ;;
		*) n=$(printf '%s' "$b" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9_-' '-' | cut -c 1-24 | sed 's/-*$//') ;;
		esac
		i=1 base=$n
		while case $names in *" $n "*) true ;; *) false ;; esac; do
			i=$((i + 1)) n=$base-$i
		done
		out="$out,$n=$d" names="$names $n "
	done
	printf '%s' "$out${SHARES:+,$SHARES}"
}

native_env() {
	wake_settings
	cat <<EOF
# Written by install.sh: run it again rather than editing this file.
ERIPHE_ROOT=$(q "$ERIPHE_HOME/node")
ERIPHE_ENGINE=container
ERIPHE_INSTALL=macos
ERIPHE_CORE=$(q "$CORE_URL")
ERIPHE_NODE_NAME=$(q "$NAME")
ERIPHE_NODE_LABELS=$(q "$LABELS")
ERIPHE_CA_HASH=$(q "$CA_HASH")
ERIPHE_NODE_SHARES=$(q "$(mac_shares)")
ERIPHE_ALWAYS_ON=$([ "$MODE" = core ] && echo 1)
ERIPHE_WORKER_IMAGE=$(q "$WORKER_IMAGE")
ERIPHE_CORE_RELAY=$([ "$MODE" = core ] || q "$RELAY_SOCKET")
ERIPHE_WAKE_MAC=$(q "$WAKE_MAC")
ERIPHE_WAKE_BROADCAST=$(q "$WAKE_BROADCAST")
EOF
}

# The core relay: macOS keeps a LaunchAgent off the LAN, and a daemon run as
# the user too (Apple TN3179: only root programs and daemons are allowed,
# without asking, and a daemon run as the user was asked about and held back
# on this Mac); eriphe-node itself must be an agent, in the user's session,
# where Apple's container has its services. So a root LaunchDaemon connects
# to eriphe-core for the node, and does nothing else (node/README.md, The
# container engine): eriphe-node's binary as `core-relay`, from a copy only
# root can change, on a socket in a folder of root's that only the user can
# open, to eriphe-core's addresses only. Not in core mode: eriphe-core is on
# this Mac, the loopback is never held back.
RELAY_LABEL=org.eriphe.relay
RELAY_PLIST=/Library/LaunchDaemons/$RELAY_LABEL.plist
RELAY_BIN=/usr/local/libexec/eriphe-relay
RELAY_SOCKET=/var/run/eriphe/relay.sock

relay_plist() {
	plist "$RELAY_LABEL" keep /var/log/eriphe-relay.log "$RELAY_BIN" core-relay \
		-socket "$RELAY_SOCKET" -dir "$ERIPHE_HOME" -owner "$(id -u)"
}

# relay_loaded: the relay is installed and loaded (maybe from an older
# binary).
relay_loaded() {
	[ -f "$RELAY_PLIST" ] && [ "$(cat "$RELAY_PLIST")" = "$(relay_plist)" ] &&
		launchctl print "system/$RELAY_LABEL" >/dev/null 2>&1
}

RELAY_WHY="it lets Eriphe reach your other devices on your home network"

# native_relay installs or updates the relay (sudo: macOS asks for the
# password), unless it is there already as it should be: the same plist,
# loaded, and the same binary as the node's.
native_relay() {
	new=$(relay_plist)
	if relay_loaded && cmp -s "$RELAY_BIN" "$ERIPHE_HOME/bin/eriphe-node"; then
		return
	fi
	note "Installing the node's way to eriphe-core on the LAN (the LaunchDaemon $RELAY_LABEL)..."
	need_root "$RELAY_WHY"
	tmp=$(mktemp)
	printf '%s\n' "$new" >"$tmp"
	sudo launchctl bootout "system/$RELAY_LABEL" >/dev/null 2>&1 || true
	if ! { sudo install -d -m 755 -o root -g wheel /usr/local/libexec &&
		sudo install -m 755 -o root -g wheel "$ERIPHE_HOME/bin/eriphe-node" "$RELAY_BIN" &&
		sudo install -m 644 -o root -g wheel "$tmp" "$RELAY_PLIST"; }; then
		die "could not install the relay"
	fi
	rm -f "$tmp"
	sudo launchctl bootstrap system "$RELAY_PLIST" || die "could not start $RELAY_LABEL"
}

native_relay_uninstall() {
	[ -f "$RELAY_PLIST" ] || [ -f "$RELAY_BIN" ] || return 0
	note "Removing the LaunchDaemon $RELAY_LABEL..."
	sudo launchctl bootout "system/$RELAY_LABEL" >/dev/null 2>&1 || true
	sudo rm -rf "$RELAY_PLIST" "$RELAY_BIN" "$(dirname "$RELAY_SOCKET")" /var/log/eriphe-relay.log
}

native_conf() {
	install_conf
	echo "auto_shares=$AUTO_SHARES"
}

# start_node_sh: what the node's LaunchAgent runs: container's services
# (started again after a restart of the Mac), the join token for the first
# start only, then eriphe-node.
start_node_sh() {
	cat <<EOF
#!/bin/sh
# Written by install.sh: starts eriphe-node on this Mac, with Apple's
# container (LaunchAgent $NODE_LABEL).
E=$(q "$ERIPHE_HOME")
CONTAINER=$(q "$CONTAINER")
EOF
	cat <<'EOF'
set -a
# shellcheck disable=SC1091
. "$E/node.env"
set +a
"$CONTAINER" system start --enable-kernel-install </dev/null >/dev/null 2>&1 || echo "eriphe: Apple's container does not start" >&2
if [ ! -f "$ERIPHE_ROOT/node.json" ] && [ -f "$E/join-token" ]; then
	ERIPHE_JOIN_TOKEN=$(cat "$E/join-token")
	export ERIPHE_JOIN_TOKEN
fi
# The node's programs for tasks (eriphe-cred), Linux builds for the tasks'
# VMs, taken from its image with it.
exec "$E/bin/eriphe-node" -docker "$CONTAINER" -image "$ERIPHE_WORKER_IMAGE" -task-bin "$E/bin/task-bin"
EOF
}

# plist LABEL KEEPALIVE LOG ARG...: a LaunchAgent that runs ARG... at login.
plist() {
	label=$1 keep=$2 logf=$3
	shift 3
	printf '%s\n' '<?xml version="1.0" encoding="UTF-8"?>' \
		'<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
		'<!-- Written by install.sh. -->' '<plist version="1.0">' '<dict>' \
		"  <key>Label</key><string>$label</string>" '  <key>ProgramArguments</key>' '  <array>'
	for a in "$@"; do
		printf '    <string>%s</string>\n' "$(printf '%s' "$a" | sed 's/&/\&amp;/g; s/</\&lt;/g')"
	done
	printf '%s\n' '  </array>' '  <key>RunAtLoad</key><true/>'
	case $keep in
	keep) printf '%s\n' '  <key>KeepAlive</key><true/>' '  <key>ThrottleInterval</key><integer>10</integer>' ;;
	update)
		printf '%s\n' '  <key>WatchPaths</key>' '  <array>' \
			"    <string>$ERIPHE_HOME/node/update-requested</string>" \
			"    <string>$ERIPHE_HOME/core/update-requested</string>" '  </array>' \
			'  <key>StartInterval</key><integer>600</integer>'
		;;
	esac
	printf '%s\n' "  <key>EnvironmentVariables</key>" \
		"  <dict><key>ERIPHE_INSTALL</key><string>macos</string><key>PATH</key><string>/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string></dict>" \
		"  <key>StandardOutPath</key><string>$logf</string>" \
		"  <key>StandardErrorPath</key><string>$logf</string>" '</dict>' '</plist>'
}

# load LABEL CONTENT: the LaunchAgent LABEL, (re)loaded with CONTENT.
load() {
	f=$HOME/Library/LaunchAgents/$1.plist
	mkdir -p "$HOME/Library/LaunchAgents"
	launchctl bootout "gui/$(id -u)/$1" >/dev/null 2>&1 || true
	printf '%s\n' "$2" >"$f"
	launchctl bootstrap "gui/$(id -u)" "$f"
}

unload() {
	launchctl bootout "gui/$(id -u)/$1" >/dev/null 2>&1 || true
	rm -f "$HOME/Library/LaunchAgents/$1.plist"
}

# mac_updater_conf: what the Mac's updater works on.
mac_updater_conf() {
	cat <<EOF
# Written by install.sh: run it again rather than editing this file.
E=$(q "$ERIPHE_HOME") CONTAINER=$(q "$CONTAINER") MODE=$(q "$MODE") PIN=$(q "$PIN") API_PORT=$(q "$API_PORT")
CORE_IMAGE=$(q "$CORE_IMAGE") NODE_IMAGE=$(q "$NODE_IMAGE") WORKER_IMAGE=$(q "$WORKER_IMAGE")
EOF
}

# mac_updater_sh: the updater of a Mac with Apple's container, ~/.eriphe/update.sh,
# the Linux one's counterpart (updater_sh) for binaries: eriphe-node and
# eriphe-core are taken from their images (/usr/local/share/eriphe/darwin-arm64),
# the previous ones kept to go back to. With no argument it applies the
# requests waiting; "install" puts the images' binaries in place (install.sh);
# "rollback" goes back to the version before the last update.
mac_updater_sh() {
	cat <<'EOF'
#!/bin/sh
# Written by install.sh: applies the update requests of eriphe-core and
# eriphe-node on this Mac (install/README.md, Updates). Run by the
# LaunchAgent org.eriphe.update.
set -u
# shellcheck disable=SC1091
. "$(dirname "$0")/update.conf"
STATE=$E/update
HEALTH=${ERIPHE_UPDATE_HEALTH:-180} # seconds a restarted eriphe-node has to connect again
mkdir -p "$STATE/previous"
chmod 700 "$STATE"
log() {
  printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" >>"$STATE/update.log"
  printf 'eriphe-update: %s\n' "$*" >&2
}
mkdir "$STATE/lock" 2>/dev/null || {
  # A lock older than an hour is from an updater that died.
  if [ -n "$(find "$STATE/lock" -maxdepth 0 -mmin +60 2>/dev/null)" ]; then
    rmdir "$STATE/lock"; mkdir "$STATE/lock" || exit 0
  else
    log "another update runs"; exit 0
  fi
}
trap 'rmdir "$STATE/lock" 2>/dev/null' EXIT

# status TARGET ID ACTION OUTCOME MESSAGE FROM TO: as the Linux updater's.
status() {
  f=$E/$1/update-status
  msg=$(printf '%s' "$5" | tr '\n\r' '  ' | cut -c 1-300)
  printf 'id=%s\naction=%s\noutcome=%s\nmessage=%s\nfrom=%s\nto=%s\nat=%s\n' "$2" "$3" "$4" "$msg" "$6" "$7" "$(date +%s)" >"$f.new"
  chmod 600 "$f.new"
  mv "$f.new" "$f"
  [ "$4" = running ] || log "$1 $3 $2: $4${5:+ ($5)}"
}

# The programs of a target, as "name image" lines, eriphe-core first.
items() {
  if [ "$1" = core ]; then echo "core $CORE_IMAGE"; fi
  echo "node $NODE_IMAGE"
}
repo() { printf '%s' "${1%:*}"; }
label() {
  "$CONTAINER" image inspect "$1" 2>/dev/null |
    jq -r '[.[0].variants[]?.config.config.Labels["org.opencontainers.image.version"] // empty][0] // empty'
}
version() { cat "$E/bin/eriphe-$1.version" 2>/dev/null || echo dev; }
# tag_of, release, newer, pinned: as the Linux updater's.
tag_of() {
  case $1 in
    *-dev+*) t=sha-${1##*+} ;;
    *+*) t= ;;
    *) t=$1 ;;
  esac
  printf '%s' "$t" | grep -Eq '^(sha-[0-9a-f]{7,40}|[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?)$' && printf '%s' "$t"
}
release() { printf '%s' "$1" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; }
newer() {
  printf '%s %s\n' "$1" "$2" | awk '{ split($1, a, "."); split($2, b, ".")
    for (i = 1; i <= 3; i++) { if (a[i] + 0 > b[i] + 0) exit 0; if (a[i] + 0 < b[i] + 0) exit 1 }
    exit 1 }'
}
pinned() { [ -n "$PIN" ] && [ "$PIN" != edge ]; }

# fetch NAME IMAGE DIR: the image's Mac binary of NAME into DIR, with its version.
fetch() {
  over=
  [ "$1" = node ] && over=${ERIPHE_NODE_BINARY:-}
  [ "$1" = core ] && over=${ERIPHE_CORE_BINARY:-}
  if [ -n "$over" ]; then
    cp "$over" "$3/eriphe-$1" && echo dev >"$3/eriphe-$1.version"
    return
  fi
  if ! out=$("$CONTAINER" image pull --progress none "$2" 2>&1); then
    why="could not pull $2: $(printf '%s' "$out" | tail -n 1)"
    return 1
  fi
  t=$(mktemp -d "$STATE/fetch.XXXXXX")
  if ! "$CONTAINER" run --rm --progress none --entrypoint cp --mount "type=bind,source=$t,target=/out" \
      "$2" "/usr/local/share/eriphe/darwin-arm64/eriphe-$1" /out/ >/dev/null 2>&1 || [ ! -s "$t/eriphe-$1" ]; then
    rm -rf "$t"
    why="$2 has no Mac binary (an image from before Apple's container)"
    return 1
  fi
  chmod 755 "$t/eriphe-$1"
  mv "$t/eriphe-$1" "$3/eriphe-$1"
  rm -rf "$t"
  # The node's programs for tasks, built for Linux: the tasks are Linux VMs.
  # An image from before them has none.
  if [ "$1" = node ]; then
    t=$(mktemp -d "$STATE/fetch.XXXXXX")
    if "$CONTAINER" run --rm --progress none --entrypoint cp --mount "type=bind,source=$t,target=/out" \
        "$2" -R /usr/local/share/eriphe/task-bin/linux-arm64 /out/ >/dev/null 2>&1 && [ -d "$t/linux-arm64" ]; then
      rm -rf "$3/task-bin"
      mkdir -p "$3/task-bin"
      mv "$t/linux-arm64" "$3/task-bin/"
    fi
    rm -rf "$t"
  fi
  v=$(label "$2")
  echo "${v:-dev}" >"$3/eriphe-$1.version"
}

# worker TAG: the worker image of that version (or the configured one) under
# its configured reference.
worker() {
  src=$WORKER_IMAGE
  [ -z "$1" ] || src=$(repo "$WORKER_IMAGE"):$1
  "$CONTAINER" image pull --progress none "$src" >/dev/null 2>&1 || { log "could not pull $src"; return 0; }
  [ "$src" = "$WORKER_IMAGE" ] || "$CONTAINER" image tag "$src" "$WORKER_IMAGE" >/dev/null 2>&1 || true
}

# restart NAME: its LaunchAgent again, from the binary now in place.
restart() { launchctl kickstart -k "gui/$(id -u)/org.eriphe.$1" >/dev/null 2>&1; }

# healthy NAME SINCE: eriphe-core answers, or eriphe-node is connected again
# (in its log after byte SINCE), within HEALTH seconds.
healthy() {
  i=0
  if [ "$1" = core ]; then
    until curl -fsk -o /dev/null "https://127.0.0.1:${API_PORT:-8800}/health" || curl -fs -o /dev/null "http://127.0.0.1:${API_PORT:-8800}/health"; do
      i=$((i + 2)); [ $i -lt "$HEALTH" ] || { why="eriphe-core does not answer"; return 1; }
      sleep 2
    done
    return 0
  fi
  until tail -c +"$(($2 + 1))" "$E/node.log" 2>/dev/null | grep -q 'connected to '; do
    i=$((i + 2)); [ $i -lt "$HEALTH" ] || { why="eriphe-node is not connected after $HEALTH s"; return 1; }
    sleep 2
  done
}

# swap TARGET FROM: the binaries of FROM (a folder) in place, the ones in
# place kept in $STATE/swap; then each restarted, eriphe-core first, and
# checked.
swap() {
  rm -rf "$STATE/swap"; mkdir -p "$STATE/swap"
  items "$1" | while read -r n _; do
    cp -p "$E/bin/eriphe-$n" "$E/bin/eriphe-$n.version" "$STATE/swap/" 2>/dev/null
    cp -p "$2/eriphe-$n" "$E/bin/eriphe-$n.new" && mv "$E/bin/eriphe-$n.new" "$E/bin/eriphe-$n"
    cp -p "$2/eriphe-$n.version" "$E/bin/"
    # The node's programs for tasks go with it.
    if [ "$n" = node ] && [ -d "$2/task-bin" ]; then
      [ ! -d "$E/bin/task-bin" ] || cp -Rp "$E/bin/task-bin" "$STATE/swap/"
      rm -rf "$E/bin/task-bin.new"
      cp -Rp "$2/task-bin" "$E/bin/task-bin.new" && rm -rf "$E/bin/task-bin" && mv "$E/bin/task-bin.new" "$E/bin/task-bin"
    fi
  done
  if [ "$1" = core ]; then
    restart core
    healthy core 0 || return 1
  fi
  # Only what the new eriphe-node logs: the old one reconnects to a
  # restarted eriphe-core meanwhile.
  since=$({ wc -c <"$E/node.log"; } 2>/dev/null | tr -d " ")
  since=${since:-0}
  restart node
  healthy node "$since"
}

# keep TARGET: what swap replaced is the version to go back to.
keep() {
  rm -rf "$STATE/previous.$1"
  mv "$STATE/swap" "$STATE/previous.$1"
}

# back TARGET: what swap replaced, in place again.
back() {
  rm -rf "$STATE/undo"; mv "$STATE/swap" "$STATE/undo"
  swap "$1" "$STATE/undo"
}

# update TARGET ID AUTO CORE: as the Linux updater's, for binaries.
update() {
  target=$1 id=$2 auto=$3 core=$4
  from=$(version "$target")
  status "$target" "$id" update running "" "$from" ""
  new=$STATE/new
  rm -rf "$new"; mkdir -p "$new"
  # The node (and worker) of eriphe-core's build: on eriphe-core's machine,
  # the one just fetched; on a node alone, the one eriphe-core runs, unless
  # it is pinned to a release.
  match=
  if [ "$target" = node ] && ! pinned; then match=$(tag_of "$core"); fi
  for line in $(items "$target" | tr ' ' '='); do
    n=${line%%=*} ref=${line#*=}
    src=$ref
    [ -z "$match" ] || [ "$n" != node ] || src=$(repo "$ref"):$match
    fetch "$n" "$src" "$new" || { status "$target" "$id" update failed "$why" "$from" ""; return; }
    if [ "$n" = core ]; then
      match=$(tag_of "$(cat "$new/eriphe-core.version")")
    fi
  done
  to=$(cat "$new/eriphe-$target.version")
  same=1
  for n in $(items "$target" | cut -d ' ' -f 1); do
    cmp -s "$new/eriphe-$n" "$E/bin/eriphe-$n" || same=
  done
  worker "$match"
  if [ -n "$same" ]; then
    if pinned; then
      status "$target" "$id" update pinned "pinned to $PIN" "$from" "$to"
    else
      status "$target" "$id" update current "" "$from" "$to"
    fi
    return
  fi
  # A node is never newer than eriphe-core (a release pinned here may be).
  if [ "$target" = node ] && release "$core" && release "$to" && newer "$to" "$core"; then
    status "$target" "$id" update refused "eriphe-core runs $core: $to would be newer" "$from" "$to"
    return
  fi
  if [ "$auto" = 1 ] && grep -qx "$to" "$STATE/failed" 2>/dev/null; then
    status "$target" "$id" update skipped "$to failed here before" "$from" "$to"
    return
  fi
  log "$target: $from -> $to"
  if swap "$target" "$new"; then
    keep "$target"
    status "$target" "$id" update ok "" "$from" "$to"
    return
  fi
  failed=$why
  echo "$to" >>"$STATE/failed"
  if back "$target"; then
    status "$target" "$id" update rolled_back "$failed" "$from" "$to"
  else
    status "$target" "$id" update rollback_failed "$failed; then: $why" "$from" "$to"
  fi
}

# rollback TARGET ID: the version before the last update.
rollback() {
  target=$1 id=$2
  from=$(version "$target")
  prev=$STATE/previous.$target
  if [ ! -s "$prev/eriphe-$target" ]; then
    status "$target" "$id" rollback failed "no previous version here" "$from" ""
    return
  fi
  to=$(cat "$prev/eriphe-$target.version" 2>/dev/null)
  status "$target" "$id" rollback running "" "$from" "$to"
  rm -rf "$STATE/back"; cp -Rp "$prev" "$STATE/back"
  if swap "$target" "$STATE/back"; then
    echo "$from" >>"$STATE/failed"
    keep "$target"
    status "$target" "$id" rollback ok "" "$from" "$to"
    return
  fi
  failed=$why
  if back "$target"; then
    status "$target" "$id" rollback failed "$failed: back on $from" "$from" "$to"
  else
    status "$target" "$id" rollback rollback_failed "$failed; then: $why" "$from" "$to"
  fi
}

field() { sed -n "s/^$2=//p" "$1" | head -n 1; }

target=node
[ "$MODE" = core ] && target=core
case ${1:-} in
  install)
    # install.sh: the images' binaries in place, the agents stopped.
    for line in $(items "$target" | tr ' ' '='); do
      n=${line%%=*} ref=${line#*=}
      echo "Taking eriphe-$n from $ref..." >&2
      fetch "$n" "$ref" "$E/bin" || { echo "eriphe: $why" >&2; exit 1; }
    done
    echo "Pulling $WORKER_IMAGE..." >&2
    worker ""
    exit 0 ;;
  rollback)
    rollback "$target" "local-$(date +%s)"
    exit 0 ;;
esac
for target in core node; do
  f=$E/$target/update-requested
  [ -f "$f" ] || continue
  [ "$target" = node ] || [ "$MODE" = core ] || { rm -f "$f"; continue; }
  id=$(field "$f" id) action=$(field "$f" action) core=$(field "$f" core) auto=$(field "$f" auto)
  rm -f "$f"
  printf '%s' "$id" | grep -Eq '^[a-z0-9-]{1,40}$' || { log "$target: a request without a valid id"; continue; }
  printf '%s' "$core" | grep -Eq '^(sha-[0-9a-f]{7,40}|dev|[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?)?$' || core=
  [ "$auto" = 1 ] || auto=0
  case $action in
    update) update "$target" "$id" "$auto" "$core" ;;
    rollback) rollback "$target" "$id" ;;
    *) status "$target" "$id" "$action" failed "unknown action" "" "" ;;
  esac
done
EOF
}

# native_up installs or updates everything: the binaries (from the images,
# by the updater's own install), the LaunchAgents, the join; in core mode it
# prints the setup code as host_up does.
native_up() {
	E=$ERIPHE_HOME
	umask 077
	mkdir -p "$E/bin" "$E/node" "$E/update"
	[ "$MODE" != core ] || mkdir -p "$E/core"
	touch "$E/native"
	native_env >"$E/node.env"
	native_conf >"$E/install.conf"
	mac_updater_conf >"$E/update.conf"
	mac_updater_sh >"$E/update.sh"
	start_node_sh >"$E/start-node.sh"
	chmod 700 "$E/update.sh" "$E/start-node.sh"
	# Stopped while their binaries change.
	launchctl bootout "gui/$(id -u)/$NODE_LABEL" >/dev/null 2>&1 || true
	[ "$MODE" != core ] || launchctl bootout "gui/$(id -u)/$CORE_LABEL" >/dev/null 2>&1 || true
	sh "$E/update.sh" install >&2 ||
		die "could not download Eriphe: check this Mac's Internet connection, then run the command again"
	step Downloaded

	if [ "$MODE" = core ]; then
		if [ -n "$CORE_TZ" ]; then
			load "$CORE_LABEL" "$(plist "$CORE_LABEL" keep "$E/core.log" /usr/bin/env "ERIPHE_API_LISTEN=0.0.0.0:$API_PORT" \
				"ERIPHE_WEB_LISTEN=0.0.0.0:$WEB_PORT" "TZ=$CORE_TZ" "$E/bin/eriphe-core" -data "$E/core")"
		else
			load "$CORE_LABEL" "$(plist "$CORE_LABEL" keep "$E/core.log" /usr/bin/env "ERIPHE_API_LISTEN=0.0.0.0:$API_PORT" \
				"ERIPHE_WEB_LISTEN=0.0.0.0:$WEB_PORT" "$E/bin/eriphe-core" -data "$E/core")"
		fi
		if [ ! -f "$E/node/node.json" ]; then
			note "Waiting for eriphe-core..."
			i=0
			until JOIN_TOKEN=$("$E/bin/eriphe-core" join-token -data "$E/core" -name "$NAME" -quiet 2>/dev/null | tr -d '[:space:]') &&
				[ -n "$JOIN_TOKEN" ]; do
				i=$((i + 1))
				[ $i -le 60 ] || {
					tail -n 20 "$E/core.log" >&2
					die "Eriphe did not start (eriphe-core gives no join token): run the command again"
				}
				sleep 2
			done
		fi
	fi
	[ "$MODE" = core ] || native_relay
	if [ ! -f "$E/node/node.json" ]; then
		(
			umask 077
			printf '%s\n' "$JOIN_TOKEN" >"$E/join-token"
		)
	fi
	since=$({ wc -c <"$E/node.log"; } 2>/dev/null | tr -d " ")
	since=${since:-0}
	load "$NODE_LABEL" "$(plist "$NODE_LABEL" keep "$E/node.log" /bin/sh "$E/start-node.sh")"
	step Installed
	note "Waiting for eriphe-node..."
	i=0
	until tail -c +"$((since + 1))" "$E/node.log" 2>/dev/null | grep -q 'connected to '; do
		i=$((i + 1))
		[ $i -le 90 ] || {
			rm -f "$E/join-token"
			tail -n 20 "$E/node.log" >&2
			[ "$MODE" != core ] || die "Eriphe did not start in 3 minutes: run the command again"
			die "this Mac could not connect to $ASSISTANT in 3 minutes: check that the computer Eriphe runs on is on, then run the command again"
		}
		sleep 2
	done
	if [ "$MODE" = core ]; then step Started; else step Connected; fi
	# Joined: the node has its own secret, the token is used.
	rm -f "$E/join-token"
	tail -c +"$((since + 1))" "$E/node.log" | grep 'connected to ' | tail -n 1 | sed 's/^[0-9/]* [0-9:]* /eriphe-node: /' >&2
	# pin: the release it stays on; channel: edge when it follows main.
	pin=$PIN channel=
	[ "$PIN" != edge ] || pin='' channel=edge
	for d in node core; do
		[ -d "$E/$d" ] || continue
		[ "$d" = node ] || [ "$MODE" = core ] || continue
		printf 'version=1\npin=%s\nchannel=%s\n' "$pin" "$channel" >"$E/$d/updater"
	done
	load "$UPDATE_LABEL" "$(plist "$UPDATE_LABEL" update "$E/update.log" /bin/sh "$E/update.sh")"
	echo updater=1
	if [ "$MODE" = core ]; then
		# The setup code, while there is no password (kept until it is used).
		if code=$("$E/bin/eriphe-core" auth setup-code -data "$E/core" 2>"$E/setup-code.err"); then
			echo "setup_code=$(printf '%s' "$code" | tr -d '[:space:]')"
		elif ! grep -q 'has a password' "$E/setup-code.err"; then
			echo "setup_code=$(sed -n 's/.*with the setup code \([A-Za-z0-9-]*\).*/\1/p' "$E/core.log" | tail -n 1)"
		fi
		rm -f "$E/setup-code.err"
		echo "version=$(cat "$E/bin/eriphe-core.version" 2>/dev/null)"
	else
		echo "version=$(cat "$E/bin/eriphe-node.version" 2>/dev/null)"
	fi
}

# native_uninstall removes what native_up made: the LaunchAgents, the tasks'
# and services' containers and networks, Eriphe's images, ~/.eriphe, and
# Apple's container if Eriphe installed it.
native_uninstall() {
	E=$ERIPHE_HOME
	for l in "$UPDATE_LABEL" "$NODE_LABEL" "$CORE_LABEL"; do unload "$l"; done
	native_relay_uninstall
	if [ -x "$CONTAINER" ] && "$CONTAINER" system status >/dev/null 2>&1; then
		note "Removing Eriphe's containers..."
		ids=$("$CONTAINER" ls -a --format json 2>/dev/null |
			jq -r '.[] | select(.configuration.labels["eriphe.task"] != null or .configuration.labels["eriphe.service"] != null) | .configuration.id')
		# shellcheck disable=SC2086 # one argument per id
		[ -z "$ids" ] || "$CONTAINER" delete --force $ids >/dev/null 2>&1 || true
		nets=$("$CONTAINER" network ls --format json 2>/dev/null |
			jq -r '.[] | select(.configuration.labels["eriphe.task"] != null or .configuration.labels["eriphe.service"] != null) | .id')
		# shellcheck disable=SC2086
		[ -z "$nets" ] || "$CONTAINER" network delete $nets >/dev/null 2>&1 || true
		refs=$("$CONTAINER" image ls --format json 2>/dev/null |
			jq -r --arg r "$REGISTRY/" '.[] | .configuration.name | select(startswith($r))')
		# shellcheck disable=SC2086
		[ -z "$refs" ] || "$CONTAINER" image delete $refs >/dev/null 2>&1 || true
	fi
	installed=
	[ -f "$E/container-installed" ] && installed=1
	rm -rf "$E"
	if [ -n "$installed" ]; then
		if [ -n "$("$CONTAINER" ls -a -q 2>/dev/null)" ]; then
			notice "Apple's container tool stays on this Mac: other containers use it."
		else
			note "Removing Apple's container (installed for Eriphe)..."
			"$CONTAINER" system stop >/dev/null 2>&1 || true
			sudo /usr/local/bin/uninstall-container.sh -d ||
				notice "Apple's container tool could not be removed: sudo /usr/local/bin/uninstall-container.sh -d removes it."
		fi
	fi
	step Removed
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

# to_screen: from here on, the screen shows what say prints, and the log
# (LOG) takes everything else.
to_screen() {
	if [ "$OS" = Darwin ] && [ -n "$UNINSTALL" ]; then
		t=${TMPDIR:-/tmp}
		LOG=${t%/}/eriphe-uninstall.log # ~/.eriphe goes
	elif [ -n "$UNINSTALL" ]; then
		# The install's log goes with Eriphe: the uninstall's own is apart.
		t=${TMPDIR:-/tmp}
		LOG=$(mktemp "${t%/}/eriphe-uninstall.XXXXXX") || die "cannot write in ${t%/}"
	elif [ "$OS" = Darwin ]; then
		LOG=$HOME/.eriphe/install.log
	elif [ "$(id -u)" = 0 ]; then
		LOG=/var/log/eriphe-install.log
	else
		LOG=$HOME/.eriphe-install.log
	fi
	(umask 077 && mkdir -p "$(dirname "$LOG")" && printf 'install.sh, %s\n' "$(date)" >"$LOG") ||
		die "cannot write $LOG"
	exec 5>&1 6>&2 >>"$LOG" 2>&1
	SCREEN=1
}

# installed: Eriphe is on this machine already.
installed() {
	if [ -n "$NATIVE" ]; then
		[ -f "$ERIPHE_HOME/install.conf" ]
	elif [ "$OS" = Darwin ]; then
		[ -x "$LIMACTL" ] && [ -n "$(vm_status)" ]
	else
		[ -d "$CONF" ]
	fi
}

do_uninstall() {
	say "Removing Eriphe from $HERE"
	say ""
	was=''
	if [ "$OS" = Darwin ] && [ -f "$ERIPHE_HOME/native" ]; then
		was=$(sed -n 's/^mode=//p' "$ERIPHE_HOME/install.conf" 2>/dev/null || true)
		if [ -f "$RELAY_PLIST" ] || [ -f "$RELAY_BIN" ] || [ -f "$ERIPHE_HOME/container-installed" ]; then
			need_root "it removes what Eriphe installed for the whole Mac"
		fi
		native_uninstall
	elif [ "$OS" = Darwin ]; then
		mac_uninstall
	else
		ROOT_WHY="it removes what Eriphe installed for the whole computer"
		on_steps host_uninstall
		rm -f "$HOME/.eriphe-install.log"
		was=$(printf '%s\n' "$OUT" | sed -n 's/^mode=//p')
		! printf '%s\n' "$OUT" | grep -qx docker_kept=1 ||
			notice "Docker stays on this computer: other containers use it."
		! printf '%s\n' "$OUT" | grep -qx docker_group_kept=1 ||
			notice "the docker group stays on this computer (it could not be removed): sudo groupdel docker removes it."
	fi
	say ""
	if [ "$was" = core ]; then
		say "Eriphe and its data were removed from $HERE."
	else
		para "Eriphe was removed from $HERE. If it was connected to Eriphe on another computer, forget it there too: Réglages › Appareils (Settings › Devices), in your browser."
	fi
	good_to_know
}

do_rollback() {
	say "Going back to the previous version on $HERE"
	say ""
	if [ -n "$NATIVE" ]; then
		[ -f "$ERIPHE_HOME/update.sh" ] || die "Eriphe is not installed on this Mac"
		sh "$ERIPHE_HOME/update.sh" rollback
		target=node
		[ -d "$ERIPHE_HOME/core" ] && target=core
		OUT=$(cat "$ERIPHE_HOME/$target/update-status")
		logs=$ERIPHE_HOME/update/update.log
	else
		if [ "$OS" = Darwin ]; then
			if [ ! -x "$LIMACTL" ] || [ -z "$(vm_status)" ]; then
				die "Eriphe is not installed on this Mac"
			fi
			[ "$(vm_status)" = Running ] || "$LIMACTL" --tty=false start "$VM" </dev/null
		fi
		ROOT_WHY="it changes what Eriphe installed for the whole computer"
		on_steps host_rollback
		logs=$DATA/update/update.log
	fi
	para "$(printf '%s\n' "$OUT" | show_status)"
	say ""
	say "Logs: $(tilde "$logs")"
}

do_install() {
	before=''
	if [ -n "$NATIVE" ]; then
		INFO=$(native_info)
		settle
		AUTO_SHARES=$(saved auto_shares)
		[ -n "$AUTO_SHARES" ] || AUTO_SHARES=1
		[ -z "$NO_SHARE" ] || AUTO_SHARES=0
		if [ "$MODE" = core ]; then
			ports_free eriphe-co
			before=$(cat "$ERIPHE_HOME/bin/eriphe-core.version" 2>/dev/null || true)
		else
			before=$(cat "$ERIPHE_HOME/bin/eriphe-node.version" 2>/dev/null || true)
		fi
	else
		if [ "$OS" = Darwin ]; then
			major=$(sw_vers -productVersion | cut -d . -f 1)
			[ "$major" -ge 13 ] || die "this Mac needs macOS 13.5 or later"
		fi
		# The ports, free before anything is installed: on a new install in
		# core mode, or when they change. The other ports, given or not, are
		# the ones the VM forwards (on a Mac) or the saved ones (settle).
		if [ "$OS" = Darwin ] && [ -n "$PORTS_SET" ]; then
			now=$(lima_ports)
			[ -n "$API_PORT" ] || API_PORT=${now%% *}
			[ -n "$WEB_PORT" ] || WEB_PORT=${now##* }
			API_PORT=${API_PORT:-8800} WEB_PORT=${WEB_PORT:-8443}
			check_ports
		fi
		if [ -n "$PORTS_SET" ] || { [ "$MODE" = core ] && ! installed; }; then
			web=$WEB_PORT api=$API_PORT
			if ! installed; then
				WEB_PORT=${WEB_PORT:-8443} API_PORT=${API_PORT:-8800}
				check_ports
			fi
			if [ "$OS" = Darwin ]; then
				ports_free limactl
			elif installed; then
				# Eriphe's own, through Docker; root's programs are not named to a user.
				ports_free "docker-proxy a program"
			else
				ports_free ""
			fi
			WEB_PORT=$web API_PORT=$api
		fi
	fi

	UPDATING=''
	if [ -n "$JOIN_TOKEN" ] && [ "$MODE" != core ]; then
		say "Connecting $HERE to $ASSISTANT"
	elif installed; then
		UPDATING=1
		ROOT_WHY="it updates Eriphe, which is installed for the whole computer"
		say "Updating Eriphe on $HERE"
	else
		say "Installing Eriphe on $HERE"
	fi
	[ -n "$UPDATING" ] || [ -n "$NATIVE" ] || say "It takes a few minutes the first time."
	say ""

	if [ -n "$NATIVE" ]; then
		# The password, asked once, before the steps, when it is needed.
		why=''
		container_current || why=$CONTAINER_WHY
		if [ "$MODE" != core ] && ! relay_loaded; then
			why=${why:+$why, and }${RELAY_WHY#"${why:+it }"}
		fi
		[ -z "$why" ] || need_root "$why"
		native_container
		out=$(native_up)
	else
		if [ "$OS" = Darwin ]; then
			mac_lima
			mac_vm
			mac_agent
		elif ! command -v docker >/dev/null 2>&1; then
			ROOT_WHY="it installs Eriphe for the whole computer, with Docker, which it needs"
		fi
		on_steps host_docker
		INFO=$OUT
		settle
		before=$(info running_version)
		version=$(info docker_version)
		isolated=1
		if [ "${version%%.*}" -lt 28 ] 2>/dev/null; then
			notice "Docker on this computer is older than version 28: update it, so that tasks cannot reach this computer's own services."
			isolated=0
		fi
		on_steps host_up "$(info docker_gid)" "$isolated"
		out=$OUT
	fi
	if [ "$OS" = Darwin ] && [ -z "$NATIVE" ] && [ "$MODE" = core ]; then
		for port in "$API_PORT" "$WEB_PORT"; do
			other=$(lsof -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | awk 'NR > 1 && $1 != "limactl" {print $1; exit}')
			[ -z "$other" ] || notice "$other already uses port $port of this Mac: other devices cannot reach Eriphe on it (--web-port and --api-port choose others)."
		done
	fi
	outcome "$out"
}

# password_reset: the command that forgets a forgotten password here, and
# prints a new setup code (eriphe-core auth password-reset).
password_reset() {
	if [ -n "$NATIVE" ]; then
		printf '%s auth password-reset -data %s' "$(tilde "$ERIPHE_HOME/bin/eriphe-core")" "$(tilde "$ERIPHE_HOME/core")"
	elif [ "$OS" = Darwin ]; then
		printf 'LIMA_HOME=%s %s shell %s sudo docker exec eriphe-core eriphe-core auth password-reset' "$(tilde "$LIMA_HOME")" "$(tilde "$LIMACTL")" "$VM"
	elif [ -n "${HOST_DATA:-}" ]; then
		printf 'docker exec eriphe-core eriphe-core auth password-reset'
	else
		printf 'sudo docker exec eriphe-core eriphe-core auth password-reset'
	fi
}

# outcome OUT: what was done, what to do next, where the logs are.
outcome() {
	addr=$(lan_address)
	[ -z "${BIND:-}" ] || addr=$BIND
	addr=${addr:-<this-computer>}
	code=$(printf '%s\n' "$1" | sed -n 's/^setup_code=//p')
	now=$(printf '%s\n' "$1" | sed -n 's/^version=//p')
	say ""
	if [ -n "$UPDATING" ]; then
		if [ -z "$now" ]; then
			say "Updated."
		elif [ "$now" = "$before" ]; then
			say "Already up to date ($now)."
		else
			say "Updated to $now."
		fi
	fi
	if [ "$MODE" = core ]; then
		if [ -n "$code" ]; then
			done_=''
			[ -n "$UPDATING" ] || done_="Done: Eriphe runs on $HERE. "
			para "${done_}To finish, open this address in your browser and give the setup code:"
			say ""
			say "  https://$addr:$WEB_PORT/setup"
			say "  Setup code: $code"
		else
			done_=''
			[ -n "$UPDATING" ] || done_="Done: Eriphe runs on $HERE. "
			para "${done_}Open https://$addr:$WEB_PORT in your browser and sign in (forgotten password: $(password_reset))."
		fi
		[ -n "$UPDATING" ] || notice "to connect your other computers, open Réglages › Appareils › Connecter un appareil (Settings › Devices › Connect a device) in your browser."
		exposed_notice "$addr"
		[ "$OS" != Darwin ] || [ -n "$UPDATING" ] ||
			notice "Eriphe runs on this Mac only while $(id -un) is logged in to it: it starts at login and stops at logout. For an Eriphe that is always there, prefer a machine that stays on (a NAS, a mini-PC, a server) as its home."
	elif [ -z "$UPDATING" ]; then
		para "Done: $HERE is connected to $ASSISTANT. Go back to your browser: it now appears in Appareils (Devices)."
	fi
	if [ "$PIN" = edge ]; then
		notice "it follows main, the version in development (--version latest follows the releases again)."
	elif [ -n "$PIN" ]; then
		notice "it stays on version $PIN (--version latest follows the releases again)."
	elif ! printf '%s\n' "$1" | grep -qx 'updater=1'; then
		notice "this computer does not update itself (it has no systemd): run the same command again to update it."
	fi
	if [ -n "$NATIVE" ]; then
		shared=$(mac_shares)
		note "Places shared with Eriphe: $(printf '%s' "$shared" | sed 's/=[^,]*//g; s/,/, /g')"
		[ -n "$UPDATING" ] || [ -z "$shared" ] ||
			notice "the first time a task opens Documents, the Desktop or iCloud Drive, macOS asks to allow \"container-runtime-linux\". Allow it."
	elif [ -n "$SHARES" ]; then
		note "Places shared with Eriphe: $(printf '%s' "$SHARES" | sed 's/=[^,]*//g; s/,/, /g')"
	elif [ "$OS" = Linux ] && [ -z "$UPDATING" ]; then
		hint="--share documents=/home/<you>/Documents"
		[ -d /mnt/user ] && hint="--share shares=/mnt/user"
		notice "Eriphe sees none of this computer's folders yet. To give it one, run the command again with $hint."
	fi
	good_to_know
	say ""
	if [ -n "$NATIVE" ]; then
		logs=$(tilde "$ERIPHE_HOME/node.log")
		[ "$MODE" != core ] || logs="$(tilde "$ERIPHE_HOME/core.log"), $logs"
	elif [ "$OS" = Darwin ]; then
		logs="LIMA_HOME=$(tilde "$LIMA_HOME") $(tilde "$LIMACTL") shell $VM sudo docker logs eriphe-node"
	else
		logs="docker logs eriphe-node"
		[ "$MODE" != core ] || logs="docker logs eriphe-core, $logs"
	fi
	say "Logs: $logs"
}

main() {
	OS=$(uname -s)
	case $OS in
	Linux | Darwin) ;;
	*) die "this installs on Linux and macOS (Windows: install/install.ps1)" ;;
	esac
	# Told to eriphe-core and the node: the workspace shows how to update.
	INSTALL=linux
	[ "$OS" = Darwin ] && INSTALL=macos
	parse_args "$@"
	NATIVE=''
	HERE="this computer"
	if [ "$OS" = Darwin ]; then
		[ "$(id -u)" != 0 ] || die "on macOS, run this as yourself, not root"
		mac_setup
		! mac_native || NATIVE=1
		HERE="this Mac"
	fi
	if [ -z "$UNINSTALL$ROLLBACK$MODE" ] && ! installed; then
		usage >&2
		die "give --core and --join-token to join an eriphe-core, or --core-mode"
	fi
	to_screen
	if [ -n "$UNINSTALL" ]; then
		do_uninstall
	elif [ -n "$ROLLBACK" ]; then
		do_rollback
	else
		do_install
	fi
}

# eriphe-launcher sources this file for its functions (install/eriphe-launcher).
[ -n "${ERIPHE_INSTALL_LIB:-}" ] || main "$@"
