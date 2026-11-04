#!/bin/sh
# (Test fixture: the Linux updater of a layout-2 install, the socket proxy
# before eriphe-node engine, as install.sh wrote it then. launcher_test.sh
# updates such a machine to this build.)
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
LOCKED=
if command -v flock >/dev/null 2>&1; then
  exec 9>"$STATE/lock"
  flock -n 9 || { log "another update runs"; exit 0; }
  LOCKED=1
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

# interrupted: a request still "running" is one whose updater stopped
# before its end (killed, the machine restarted): this one holds the lock,
# no other runs. Its outcome says so, rather than "running" for ever.
interrupted() {
  for t in core node; do
    f=$DATA/$t/update-status
    [ "$(sed -n 's/^outcome=//p' "$f" 2>/dev/null)" = running ] || continue
    status "$t" "$(sed -n 's/^id=//p' "$f")" "$(sed -n 's/^action=//p' "$f")" interrupted \
      "the updater stopped before its end" "$(sed -n 's/^from=//p' "$f")" "$(sed -n 's/^to=//p' "$f")"
  done
}
[ -z "$LOCKED" ] || interrupted

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
  # The socket proxy is the node's image (eriphe-node docker-proxy): it
  # checks what the node sends, so it takes the node's version, first. An
  # older compose project's proxy is another image, left as it is.
  if [ "$(docker inspect -f '{{.Config.Image}}' eriphe-docker-proxy 2>/dev/null)" = "$NODE_IMAGE" ]; then
    start docker-proxy "$NODE_IMAGE" || return 1
  fi
  start node "$NODE_IMAGE"
  ok=$?
  syncthing_again
  [ "$ok" = 0 ] || return 1
  healthy node "$since"
}

# syncthing_again: Syncthing runs in the node's network namespace, which a
# new node container replaces: its container again too. It does not hold
# up an update: the node runs without it, and says so.
syncthing_again() {
  grep -q '^  syncthing:' "$CONF/compose.yaml" 2>/dev/null || return 0
  if ! out=$(dc up -d --pull never --no-deps --force-recreate syncthing 2>&1); then
    log "Syncthing does not start again: $(printf '%s' "$out" | tail -n 1)"
  fi
}

main_id() { printf '%s\n' "$1" | awk -v s="$2" '$1 == s { print $3 }'; }

# installer: the install.sh of the node image now under its reference, as
# $STATE/install.sh, when its layout (LAYOUT) is newer than this machine's.
# It is that image's (/usr/local/share/eriphe/install.sh), from the build
# pulled and checked here: no other download. None from an image from
# before it, nor under eriphe-launcher, which brings its own layout as it
# updates itself (BY=loop). In a Mac's VM (Lima), the install is the Mac's
# (its settings, its network card): its user runs it there, told so in the
# node's update-needs.
installer() {
  rm -f "$STATE/install.sh"
  [ "${BY:-}" != loop ] || return 1
  c=$(docker create "$NODE_IMAGE" 2>/dev/null) || return 1
  docker cp "$c:/usr/local/share/eriphe/install.sh" "$STATE/install.sh" >/dev/null 2>&1
  docker rm "$c" >/dev/null 2>&1
  n=$(sed -n 's/^LAYOUT=\([0-9][0-9]*\)$/\1/p' "$STATE/install.sh" 2>/dev/null | head -n 1)
  if [ -z "$n" ] || [ "$n" -le "${LAYOUT:-1}" ]; then
    rm -f "$STATE/install.sh"
    return 1
  fi
  if [ "${INSTALL:-}" = macos ]; then
    rm -f "$STATE/install.sh"
    log "the install's layout $n is the Mac's to apply: its install command, run there"
    printf 'why=layout\nlayout=%s\n' "$n" >"$DATA/node/update-needs"
    return 1
  fi
  log "the install's layout goes from ${LAYOUT:-1} to $n"
}

# What install.sh --layout rewrites, kept to put back (ERIPHE_UPDATE_ROOT:
# the tests' own place for the machine's paths).
R=${ERIPHE_UPDATE_ROOT:-}
LAYOUT_FILES="$CONF/compose.yaml $CONF/.env $CONF/install.conf $CONF/update.conf $CONF/update.sh
  $DATA/node/updater $DATA/core/updater $R/usr/local/bin/eriphe-share
  $R/etc/systemd/system/eriphe-update.service $R/etc/systemd/system/eriphe-update.path
  $R/etc/systemd/system/eriphe-update.timer $R/etc/systemd/system/eriphe-awake.service
  $R/etc/systemd/system/eriphe-awake.path"

# layout: the new layout, by the new install.sh (installer) run with
# --layout, as root here: the compose project, its settings, the units, the
# components the layout brings (their images pulled if missing), with the
# images now under their references; the services up, the node connected.
# If it fails, what it rewrote is put back and the services it added go.
layout() {
  b=$STATE/layout-before
  rm -rf "$b"
  for f in $LAYOUT_FILES; do
    [ ! -f "$f" ] || { mkdir -p "$b$(dirname "$f")" && cp -p "$f" "$b$f"; }
  done
  if ERIPHE_CORE_IMAGE=$CORE_IMAGE ERIPHE_NODE_IMAGE=$NODE_IMAGE ERIPHE_WORKER_IMAGE=$WORKER_IMAGE \
      sh "$STATE/install.sh" --layout </dev/null >"$STATE/layout.log" 2>&1; then
    rm -rf "$b" "$STATE/install.sh"
    return 0
  fi
  why="the new install layout did not apply: $(sed -n 's/^eriphe: //p' "$STATE/layout.log" | tail -n 1)"
  for f in $LAYOUT_FILES; do
    if [ -f "$b$f" ]; then
      cp -p "$b$f" "$f.new" && mv "$f.new" "$f"
    else
      rm -f "$f"
    fi
  done
  rm -rf "$b" "$STATE/install.sh"
  [ -n "$R" ] || ! command -v systemctl >/dev/null 2>&1 || systemctl daemon-reload >/dev/null 2>&1 || true
  dc up -d --pull never --remove-orphans >/dev/null 2>&1 || true
  return 1
}

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
  # The same published build already runs in each container (another image
  # of it: a release built again, or a tag moved): nothing to recreate.
  same=1
  for svc in $(printf '%s\n' "$after" | awk '$1 != "-" { print $1 }'); do
    a=$(version "$(main_id "$after" "$svc")") b=$(version "$(main_id "$before" "$svc")")
    { [ "$a" = "$b" ] && tag_of "$a" >/dev/null; } || same=
  done
  if [ "$after" = "$before" ] || [ -n "$same" ]; then
    [ "$after" = "$before" ] || put "$before"
    # The same build, on an older layout (installed by an older install.sh,
    # or one whose layout failed): the layout only, checked as an update.
    if installer; then
      if layout; then
        status "$target" "$id" update ok "the install's layout is now its version's" "$from" "$to"
      else
        failed=$why
        if recreate "$target"; then
          status "$target" "$id" update failed "$failed" "$from" "$to"
        else
          status "$target" "$id" update rollback_failed "$failed; then: $why" "$from" "$to"
        fi
      fi
      return
    fi
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
  # The new images, then the new layout if they bring one: if it fails,
  # the whole update goes back.
  if recreate "$target" && { ! installer || layout; }; then
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
