# The updater install.ps1 wrote in its WSL distribution up to layout 3 (eriphe-node a binary there),
# as installs of before still run it: it takes the setup of a newer layout from the node image's
# install.ps1 (layout). install/launcher_test.sh runs its lines on this install.ps1.
#!/bin/sh
# Applies the update requests of eriphe-core and eriphe-node, in the WSL
# distribution (install/README.md, Updates): eriphe-node's binary from its
# image, eriphe-core's container in core mode, each checked and put back
# as it was if it does not come back. "rollback" goes back to the version
# before the last update. eriphe-update-loop runs it every 20 s.
set -u
# shellcheck disable=SC1091
. /etc/eriphe-update.conf
STATE=/var/lib/eriphe-update
HEALTH=${ERIPHE_UPDATE_HEALTH:-180}
BIN=/usr/local/bin/eriphe-node
NODE_LOG=/var/log/eriphe-node.log
mkdir -p "$STATE"
chmod 700 "$STATE"
# A layout rewrites this script (layout): it runs from a copy of itself.
if [ "${ERIPHE_UPDATE_COPY:-}" != 1 ] && cp "$0" "$STATE/update.run" 2>/dev/null; then
  ERIPHE_UPDATE_COPY=1 exec sh "$STATE/update.run" "$@"
fi
log() {
  printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" >>"$STATE/update.log"
  printf 'eriphe-update: %s\n' "$*" >&2
}
if command -v flock >/dev/null 2>&1; then
  exec 9>"$STATE/lock"
  flock -n 9 || exit 0
fi

dir_of() { if [ "$1" = core ]; then echo /var/lib/eriphe-core; else echo /var/lib/eriphe-node; fi; }

status() {
  f=$(dir_of "$1")/update-status
  msg=$(printf '%s' "$5" | tr '\n\r' '  ' | cut -c 1-300)
  printf 'id=%s\naction=%s\noutcome=%s\nmessage=%s\nfrom=%s\nto=%s\nat=%s\n' "$2" "$3" "$4" "$msg" "$6" "$7" "$(date +%s)" >"$f.new"
  chmod 600 "$f.new"
  [ "$1" = core ] && chown 1993:1993 "$f.new"
  mv "$f.new" "$f"
  [ "$4" = running ] || log "$1 $3 $2: $4${5:+ ($5)}"
}

label() {
  v=$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.version"}}' "$1" 2>/dev/null)
  case $v in
    '' | '<no value>') printf '%s' "$1" | sed 's/^sha256://' | cut -c 1-12 ;;
    *) printf '%s' "$v" ;;
  esac
}
node_version() { cat "$BIN.version" 2>/dev/null || echo unknown; }
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

# node_healthy SIZE: eriphe-node, restarted by eriphe-node-run, says it is
# connected after byte SIZE of its log.
node_healthy() {
  i=0
  until tail -c +"$(($1 + 1))" "$NODE_LOG" 2>/dev/null | grep -q 'connected to '; do
    i=$((i + 2)); [ $i -lt "$HEALTH" ] || { why="eriphe-node is not connected after $HEALTH s"; return 1; }
    sleep 2
  done
}

# node_swap: the binary in place is started again, and checked.
node_swap() {
  size=$(wc -c <"$NODE_LOG" 2>/dev/null || echo 0)
  pkill -x eriphe-node || true
  node_healthy "$size"
}

core_healthy() {
  i=0
  # shellcheck disable=SC1091
  [ ! -f /etc/eriphe-core.ports ] || . /etc/eriphe-core.ports
  until wget -q -O /dev/null "http://127.0.0.1:${API_PORT:-8800}/health" 2>/dev/null; do
    i=$((i + 2)); [ $i -lt "$HEALTH" ] || { why="eriphe-core does not answer"; return 1; }
    sleep 2
  done
}

# core_swap ID: eriphe-core's container again, from image ID, and checked.
core_swap() {
  docker tag "$1" "$CORE_IMAGE"
  /usr/local/bin/eriphe-core-run "$CORE_IMAGE" || { why="eriphe-core does not start"; return 1; }
  core_healthy
}

# pull_node [TAG]: the binary of eriphe-node at TAG, eriphe-core's build
# (or, without, the latest, edge or pinned one), as $BIN.new, and its
# version in $to.
pull_node() {
  src=$NODE_IMAGE
  case ${1:-}:$NODE_IMAGE in ?*:*/eriphe-node:*) src=${NODE_IMAGE%:*}:$1 ;; esac
  docker pull -q "$src" >/tmp/eriphe-pull.log 2>&1 || { why="could not pull $src: $(tail -n 1 /tmp/eriphe-pull.log)"; return 1; }
  to=$(label "$src")
  c=$(docker create "$src") || { why="docker create failed"; return 1; }
  docker cp "$c:/usr/local/bin/eriphe-node" "$BIN.new" >/dev/null || { docker rm "$c" >/dev/null; why="docker cp failed"; return 1; }
  # Its build's install.ps1, for its layout; none in an image from before it.
  rm -f "$STATE/install.ps1"
  docker cp "$c:/usr/local/share/eriphe/install.ps1" "$STATE/install.ps1" >/dev/null 2>&1 || rm -f "$STATE/install.ps1"
  docker rm "$c" >/dev/null
  docker rmi "$src" >/dev/null 2>&1 || true
  chmod 755 "$BIN.new"
}

# ps_number NAME: $NAME = <number> in the node image's install.ps1 (pull_node).
ps_number() { sed -n "s/^[$]$1 = \([0-9][0-9]*\)\$/\1/p" "$STATE/install.ps1" 2>/dev/null | head -n 1; }
# newer_layout: that install.ps1's layout is newer than this distribution's.
newer_layout() {
  n=$(ps_number layout)
  [ -n "$n" ] && [ "$n" -gt "${LAYOUT:-1}" ]
}
# set_value FILE KEY VALUE: KEY=VALUE in FILE, in place of the one there.
set_value() {
  { grep -v "^$2=" "$1" 2>/dev/null; echo "$2=$3"; } >"$1.new" && mv "$1.new" "$1"
}
# node_restart: eriphe-node-run again (the scheduled task starts it within
# a minute), with the settings and the scripts now in place, and checked.
node_restart() {
  size=$(wc -c <"$NODE_LOG" 2>/dev/null || echo 0)
  pkill -f /usr/local/bin/eriphe-node-run || true
  pkill -x eriphe-node || true
  node_healthy "$size"
}

# What the layout rewrites, kept to put back.
LAYOUT_FILES="/etc/eriphe-node.env /etc/eriphe-update.conf /etc/wsl.conf /etc/docker/daemon.json
  /usr/local/bin/eriphe-dockerd /usr/local/bin/eriphe-node-run /usr/local/bin/eriphe-syncthing-run
  /usr/local/bin/eriphe-share /usr/local/bin/eriphe-addr /usr/local/bin/eriphe-core-run
  /usr/local/bin/eriphe-update-loop /usr/local/bin/eriphe-update /var/lib/eriphe-node/updater"

# layout: the distribution brought to the layout of the node image's
# install.ps1, as that script brings it: its setup of the distribution (its
# scripts, this one too, and the settings every node has), and Syncthing's,
# both taken from it as they are; then eriphe-node-run again, checked as an
# update is. What it rewrote is put back if it fails. What only install.ps1
# can do, in an administrator PowerShell (Windows' side, $adminLayout, or a
# new wsl.conf, which needs WSL started again), is left for the user, and
# said in update-needs.
layout() {
  ps=$STATE/install.ps1 b=$STATE/layout-before
  n=$(ps_number layout) admin=$(ps_number adminLayout)
  log "the install's layout goes from ${LAYOUT:-1} to $n"
  awk '/^[$]changed = Invoke-InDistro @.$/ { f = 1; next } f && /^.@$/ { exit } f' "$ps" >"$STATE/layout-setup.sh"
  awk '/^  [$]stSetup = @.$/ { f = 1; next } f && /^.@$/ { exit } f' "$ps" >"$STATE/layout-syncthing.sh"
  v=$(sed -n 's/^[$]syncthingVersion = "\([0-9.]*\)"$/\1/p' "$ps")
  case $(uname -m) in x86_64) k=AMD64 ;; aarch64) k=ARM64 ;; *) k=none ;; esac
  st=$(awk '/^[$]syncthing = @[{]$/ { f = 1; next } f && /^[}]$/ { exit } f' "$ps" |
    sed -n "s/^  $k = @(\"\([a-z0-9]*\)\", \"\([0-9a-f]*\)\")\$/\1 \2/p")
  if ! grep -q '^cat > /usr/local/bin/eriphe-node-run ' "$STATE/layout-setup.sh"; then
    why="the node image's install.ps1 has no setup to apply"
    return 1
  fi
  rm -rf "$b"
  for f in $LAYOUT_FILES; do
    [ ! -f "$f" ] || { mkdir -p "$b$(dirname "$f")" && cp -p "$f" "$b$f"; }
  done
  done_=$n needs=
  if out=$(sh "$STATE/layout-setup.sh" 2>>"$STATE/update.log"); then
    case $out in *restart*) needs='admin' ;; esac
    if [ -n "$st" ] && [ -n "$v" ]; then
      # shellcheck disable=SC2086 # its build and its checksum
      set -- $st
      case $(v=$v a=$1 sum=$2 sh "$STATE/layout-syncthing.sh" 2>>"$STATE/update.log") in
        *syncthing=missing*) log "Syncthing could not be installed"; done_=1 ;;
      esac
    fi
    set_value /etc/eriphe-update.conf LAYOUT "$done_"
    set_value /var/lib/eriphe-node/updater layout "$done_"
    [ -z "$admin" ] || [ "$admin" -le "${ADMIN_LAYOUT:-1}" ] || needs='admin'
    if node_restart; then
      rm -rf "$b"
      if [ -n "$needs" ]; then
        printf 'why=%s\nlayout=%s\n' "$needs" "$n" >/var/lib/eriphe-node/update-needs
      else
        rm -f /var/lib/eriphe-node/update-needs
      fi
      return 0
    fi
  else
    why="its setup failed"
  fi
  why="the new install layout did not apply: $why"
  for f in $LAYOUT_FILES; do
    if [ -f "$b$f" ]; then cp -p "$b$f" "$f.new" && mv "$f.new" "$f"; else rm -f "$f"; fi
  done
  rm -rf "$b"
  failed_layout=$why
  node_restart || true
  why=$failed_layout
  return 1
}

# update_node ID AUTO CORE [TAG]: without TAG, eriphe-core's build (the tag
# of CORE), unless pinned to a release.
update_node() {
  id=$1 auto=$2 core=$3 from=$(node_version)
  build=${4:-}
  if [ -z "$build" ] && ! pinned; then build=$(tag_of "$core"); fi
  status node "$id" update running "" "$from" ""
  pull_node "$build" || { status node "$id" update failed "$why" "$from" ""; return; }
  if cmp -s "$BIN.new" "$BIN"; then
    rm -f "$BIN.new"
    # The same build, on an older layout: the layout only.
    if newer_layout; then
      if layout; then
        status node "$id" update ok "the install's layout is now its version's" "$from" "$to"
      else
        status node "$id" update failed "$why" "$from" "$to"
      fi
      return
    fi
    if pinned; then status node "$id" update pinned "pinned to $PIN" "$from" "$to"; else status node "$id" update current "" "$from" "$to"; fi
    return
  fi
  # A node is never newer than eriphe-core (a release pinned here may be).
  if release "$core" && release "$to" && newer "$to" "$core"; then
    rm -f "$BIN.new"
    status node "$id" update refused "eriphe-core runs $core: $to would be newer" "$from" "$to"
    return
  fi
  sum=$(sha256sum "$BIN.new" | cut -d ' ' -f 1)
  if [ "$auto" = 1 ] && grep -qx "$sum" "$STATE/failed" 2>/dev/null; then
    rm -f "$BIN.new"
    status node "$id" update skipped "$to failed here before" "$from" "$to"
    return
  fi
  cp "$BIN" "$STATE/node.before"
  mv "$BIN.new" "$BIN"
  echo "$to" >"$BIN.version"
  # The new binary, then the new layout if it brings one: if it fails, the
  # whole update goes back.
  if node_swap && { ! newer_layout || layout; }; then
    mv "$STATE/node.before" "$STATE/node.previous"
    echo "$from" >"$STATE/node.previous.version"
    status node "$id" update ok "" "$from" "$to"
    return
  fi
  failed=$why
  echo "$sum" >>"$STATE/failed"
  mv "$STATE/node.before" "$BIN"
  echo "$from" >"$BIN.version"
  if node_swap; then status node "$id" update rolled_back "$failed" "$from" "$to"; else status node "$id" update rollback_failed "$failed; then: $why" "$from" "$to"; fi
}

rollback_node() {
  id=$1 from=$(node_version)
  [ -f "$STATE/node.previous" ] || { status node "$id" rollback failed "no previous version here" "$from" ""; return; }
  to=$(cat "$STATE/node.previous.version" 2>/dev/null || echo unknown)
  status node "$id" rollback running "" "$from" "$to"
  cp "$BIN" "$STATE/node.before"
  cp "$STATE/node.previous" "$BIN"
  echo "$to" >"$BIN.version"
  if node_swap; then
    sha256sum "$STATE/node.before" | cut -d ' ' -f 1 >>"$STATE/failed"
    mv "$STATE/node.before" "$STATE/node.previous"
    echo "$from" >"$STATE/node.previous.version"
    status node "$id" rollback ok "" "$from" "$to"
    return
  fi
  failed=$why
  mv "$STATE/node.before" "$BIN"
  echo "$from" >"$BIN.version"
  if node_swap; then status node "$id" rollback failed "$failed: back on $from" "$from" "$to"; else status node "$id" rollback rollback_failed "$failed; then: $why" "$from" "$to"; fi
}

# update_core ID AUTO: eriphe-core first, then this machine's node.
update_core() {
  id=$1 auto=$2
  have=$(docker inspect -f '{{.Image}}' eriphe-core 2>/dev/null)
  from=$(label "$have")
  status core "$id" update running "" "$from" ""
  if ! docker pull -q "$CORE_IMAGE" >/tmp/eriphe-pull.log 2>&1; then
    status core "$id" update failed "could not pull $CORE_IMAGE: $(tail -n 1 /tmp/eriphe-pull.log)" "$from" ""
    return
  fi
  want=$(docker image inspect -f '{{.Id}}' "$CORE_IMAGE")
  to=$(label "$want")
  # This machine's node: from the same build as eriphe-core (the tag of its
  # version), not its own latest, which may be half published.
  match=$(tag_of "$to")
  if [ "$want" = "$have" ]; then
    # eriphe-core is current: its node may not be.
    (update_node "$id-node" "$auto" "$to" "$match") # its own variables
    if pinned; then status core "$id" update pinned "pinned to $PIN" "$from" "$to"; else status core "$id" update current "" "$from" "$to"; fi
    return
  fi
  if [ "$auto" = 1 ] && grep -qx "$want" "$STATE/failed" 2>/dev/null; then
    docker tag "$have" "$CORE_IMAGE"
    status core "$id" update skipped "$to failed here before" "$from" "$to"
    return
  fi
  docker tag "$have" "${CORE_IMAGE%:*}:eriphe-previous"
  if core_swap "$want"; then
    echo "$have" >"$STATE/core.previous"
    (update_node "$id-node" "$auto" "$to" "$match") # its own variables
    status core "$id" update ok "" "$from" "$to"
    return
  fi
  failed=$why
  echo "$want" >>"$STATE/failed"
  if core_swap "$have"; then status core "$id" update rolled_back "$failed" "$from" "$to"; else status core "$id" update rollback_failed "$failed; then: $why" "$from" "$to"; fi
}

rollback_core() {
  id=$1
  have=$(docker inspect -f '{{.Image}}' eriphe-core 2>/dev/null)
  from=$(label "$have")
  prev=$(cat "$STATE/core.previous" 2>/dev/null)
  if [ -z "$prev" ] || ! docker image inspect "$prev" >/dev/null 2>&1; then
    status core "$id" rollback failed "no previous version here" "$from" ""
    return
  fi
  to=$(label "$prev")
  status core "$id" rollback running "" "$from" "$to"
  if core_swap "$prev"; then
    echo "$have" >"$STATE/core.previous"
    echo "$have" >>"$STATE/failed"
    docker tag "$have" "${CORE_IMAGE%:*}:eriphe-previous"
    (rollback_node "$id-node")
    status core "$id" rollback ok "" "$from" "$to"
    return
  fi
  failed=$why
  if core_swap "$have"; then status core "$id" rollback failed "$failed: back on $from" "$from" "$to"; else status core "$id" rollback rollback_failed "$failed; then: $why" "$from" "$to"; fi
}

field() { sed -n "s/^$2=//p" "$1" | head -n 1; }

if [ "${1:-}" = rollback ]; then
  if [ "$MODE" = core ]; then rollback_core "local-$(date +%s)"; else rollback_node "local-$(date +%s)"; fi
  exit 0
fi
for target in core node; do
  f=$(dir_of "$target")/update-requested
  [ -f "$f" ] || continue
  [ "$target" = node ] || [ "$MODE" = core ] || { rm -f "$f"; continue; }
  id=$(field "$f" id) action=$(field "$f" action) core=$(field "$f" core) auto=$(field "$f" auto)
  rm -f "$f"
  printf '%s' "$id" | grep -Eq '^[a-z0-9-]{1,40}$' || { log "$target: a request without a valid id"; continue; }
  printf '%s' "$core" | grep -Eq '^(sha-[0-9a-f]{7,40}|dev|[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?)?$' || core=
  [ "$auto" = 1 ] || auto=0
  case $target:$action in
    core:update) update_core "$id" "$auto" ;;
    core:rollback) rollback_core "$id" ;;
    node:update) update_node "$id" "$auto" "$core" ;;
    node:rollback) rollback_node "$id" ;;
    *) status "$target" "$id" "$action" failed "unknown action" "" "" ;;
  esac
done
