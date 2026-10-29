#!/bin/sh
# Lints install.sh, each script it runs on the Linux host (its quoted
# heredocs, which shellcheck sees as text), and the ones it writes there
# (they start with #!/bin/sh). Needs shellcheck.
set -eu
f=$(dirname "$0")/install.sh
shellcheck -s sh "$f"
sh -n "$f"
d=$(mktemp -d)
trap 'rm -rf "$d"' EXIT
awk -v d="$d" '/<<.EOF.$/ { f = 1; n++; next } /^EOF$/ { f = 0 } f { print > (d "/part" n ".sh") }' "$f"
for p in "$d"/part*.sh; do
	# A script written on the host, as it is.
	if head -n 1 "$p" | grep -q '^#!/bin/sh'; then
		shellcheck -s sh "$p"
		sh -n "$p"
		w=$((${w:-0} + 1))
		continue
	fi
	# Only the host scripts: they start with a comment or a command.
	grep -q '^if \|^# Progress\|^for \|^umask ' "$p" || continue
	{
		echo '#!/bin/sh'
		echo '# shellcheck disable=SC2034'
		echo 'CONF= DATA= CORE_UID= MODE= NODE_NAME= IMAGES= ALL_REPOS= JOIN_TOKEN= COMPOSE= ENVF= INSTALL_CONF='
		sed -n '/^die()/,/^dc()/p' "$d/part2.sh"
		cat "$p"
	} >"$p.x"
	shellcheck -s sh -e SC1007 "$p.x"
	sh -n "$p.x"
	n=$((${n:-0} + 1))
done
[ "${n:-0}" -ge 4 ] && [ "${w:-0}" -ge 1 ] || {
	echo "lint.sh: expected 4 host scripts and 1 written one, found ${n:-0} and ${w:-0}" >&2
	exit 1
}
# The updater install.ps1 writes in its WSL distribution.
awk '/^cat > \/usr\/local\/bin\/eriphe-update <<.EOF.$/ { f = 1; next } f && /^EOF$/ { exit } f' \
	"$(dirname "$0")/install.ps1" >"$d/eriphe-update"
[ -s "$d/eriphe-update" ] || {
	echo "lint.sh: no eriphe-update in install.ps1" >&2
	exit 1
}
shellcheck -s sh "$d/eriphe-update"
sh -n "$d/eriphe-update"
# The launcher of the one-container install, which sources install.sh, and its tests.
(
	cd "$(dirname "$0")/.."
	shellcheck -s sh -x install/eriphe-launcher install/launcher_test.sh
	sh -n install/eriphe-launcher
)
echo "install.sh, its ${n} host scripts, the ${w} it writes, install.ps1's updater and eriphe-launcher are clean"
