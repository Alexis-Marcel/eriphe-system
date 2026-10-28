#!/bin/sh
# Releases from Conventional Commits (https://www.conventionalcommits.org/en/v1.0.0/),
# see RELEASING.md. CI (.github/workflows/ci.yml) runs it; so can you:
#
#   release.sh log [<from>] [<to>]  the commits of <from>..<to> (default: all of <to>, HEAD),
#                                   as records for next and notes
#   release.sh next <version>       the version after <version> (X.Y.Z) for the commits read on
#                                   stdin, or nothing when none of them is releasable
#   release.sh notes                the release notes of the commits read on stdin (Markdown)
#   release.sh title <title>        fails, saying why, when <title> is not a Conventional Commit
#                                   subject of one of the types of TYPES (a pull request's title)
#
#   release.sh log v0.1.0 | release.sh next 0.1.0
#
# A commit's subject is "type(scope)!: description" (scope and "!" optional). feat bumps the
# minor; fix and perf the patch; a breaking change ("!", or a "BREAKING CHANGE:" footer) the
# major, or the minor before 1.0.0. The other types, and commits that do not follow the
# convention, release nothing by themselves. The highest bump wins.
#
# A record is "<short commit>\037<message>\036" (git log --format='%h%x1f%B%x1e').
set -eu

TYPES='feat fix perf docs chore ci test refactor style build'

usage() {
	sed -n '4,13p' "$0" | sed 's/^# \{0,1\}//' >&2
	exit 2
}

# parse runs the awk program $1 on the records read on stdin, with each commit's short commit
# (sha), subject, type, scope, description and breaking (1 or 0) set, and the variables in $2...
parse() {
	prog=$1
	shift
	awk -v RS='\036' "$@" '
		{
			sub(/^\n+/, "")
			if ($0 == "") next
			i = index($0, "\037")
			sha = substr($0, 1, i - 1)
			msg = substr($0, i + 1)
			n = split(msg, lines, "\n")
			subject = lines[1]
			type = ""; scope = ""; description = subject; breaking = 0
			if (match(subject, /^[A-Za-z]+(\([^()]*\))?!?: /)) {
				head = substr(subject, 1, RLENGTH - 2)
				description = substr(subject, RLENGTH + 1)
				if (head ~ /!$/) { breaking = 1; head = substr(head, 1, length(head) - 1) }
				j = index(head, "(")
				if (j) { scope = substr(head, j + 1, length(head) - j - 1); head = substr(head, 1, j - 1) }
				type = tolower(head)
				for (k = 2; k <= n; k++)
					if (lines[k] ~ /^BREAKING[ -]CHANGE: /) breaking = 1
			}
			commit()
		}
		'"$prog"
}

case ${1-} in
log)
	range=${3:-HEAD}
	[ -z "${2-}" ] || range="$2..$range"
	git log --format='%h%x1f%B%x1e' "$range"
	;;

next)
	[ $# -eq 2 ] || usage
	n='(0|[1-9][0-9]*)'
	printf '%s' "$2" | grep -Eq "^$n\.$n\.$n$" || { echo "release.sh: $2 is not X.Y.Z" >&2; exit 2; }
	parse '
		function commit() {
			if (type == "") return
			if (breaking) b = 3
			else if (type == "feat") b = 2
			else if (type == "fix" || type == "perf") b = 1
			else b = 0
			if (b > bump) bump = b
		}
		END {
			split(version, v, ".")
			if (bump == 3 && v[1] == 0) bump = 2
			if (bump == 3) print (v[1] + 1) ".0.0"
			else if (bump == 2) print v[1] "." (v[2] + 1) ".0"
			else if (bump == 1) print v[1] "." v[2] "." (v[3] + 1)
		}' -v version="$2" -v bump=0
	;;

notes)
	[ $# -eq 1 ] || usage
	parse '
		function commit(  line, s) {
			line = "- " (scope != "" ? "**" scope ":** " : "") description " (" sha ")"
			if (breaking) s = 1
			else if (type == "feat") s = 2
			else if (type == "fix" || type == "perf") s = 3
			else s = 4
			out[s] = out[s] line "\n"
		}
		END {
			title[1] = "Breaking changes"; title[2] = "Features"; title[3] = "Fixes"; title[4] = "Other"
			sep = ""
			for (s = 1; s <= 4; s++)
				if (s in out) { printf "%s## %s\n\n%s", sep, title[s], out[s]; sep = "\n" }
			if (sep == "") print "No changes."
		}'
	;;

title)
	[ $# -eq 2 ] || usage
	t=$2
	types=$(printf '%s' "$TYPES" | sed 's/ /|/g')
	if printf '%s\n' "$t" | grep -Eq "^($types)(\([^()]+\))?!?: [^ ].*$" && [ "$(printf '%s\n' "$t" | wc -l)" -eq 1 ]; then
		exit 0
	fi
	cat >&2 <<EOF
"$t" is not a Conventional Commit subject.
The title of a pull request becomes its commit on main, which CI reads to release
(RELEASING.md). Write it as "type(scope)!: description", scope and "!" optional, with
type one of: $TYPES
  feat: let a node rejoin under its own name    -> minor release
  fix(core): keep the session on a reload       -> patch release
  feat!: drop the v1 API                        -> breaking change
  docs: explain the backups                     -> no release by itself
EOF
	exit 1
	;;

*) usage ;;
esac
