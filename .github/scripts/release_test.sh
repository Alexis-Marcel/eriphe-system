#!/bin/sh
# Tests of release.sh: sh .github/scripts/release_test.sh (CI runs it, job scripts).
set -eu

release="$(dirname "$0")/release.sh"
failed=0

# commits prints one record per argument, as release.sh log does; "\n" in an argument is a new
# line of the message.
commits() {
	i=0
	for m in "$@"; do
		i=$((i + 1))
		printf 'c%06d\037%b\n\036\n' "$i" "$m"
	done
}

# next <last> <want> <message>...: the version after <last> for these commits is <want> ("" for
# no release).
next() {
	last=$1 want=$2
	shift 2
	got=$(commits "$@" | sh "$release" next "$last")
	if [ "$got" != "$want" ]; then
		echo "FAIL next $last [$*]: got \"$got\", want \"$want\""
		failed=1
	fi
}

next 0.1.0 ''
next 0.1.0 '' 'docs: explain the backups' 'chore: bump Go' 'ci: cache modules' 'test: cover the tunnel' \
	'refactor: split the router' 'style: gofmt' 'build: pin the base image'
next 0.1.0 '' 'Keep the workspace page from scrolling (#6)' 'Merge pull request #3 from x/y'
next 0.1.0 '0.1.1' 'fix: keep the session on a reload'
next 0.1.0 '0.1.1' 'perf(core): cache the token'
next 0.1.0 '0.1.1' 'docs: a typo' 'fix: a crash' 'Not conventional'
next 0.1.0 '0.2.0' 'feat: let a node rejoin under its own name'
next 0.1.0 '0.2.0' 'fix: a' 'feat(node): b' 'fix: c'
next 0.1.3 '0.2.0' 'feat: resets the patch'
next 0.1.0 '0.2.0' 'feat!: drop the v1 API'
next 0.1.0 '0.2.0' 'refactor(core)!: rename the config'
next 0.1.0 '0.2.0' 'fix: a\n\nBREAKING CHANGE: the config moves'
next 0.1.0 '0.2.0' 'chore: a\n\nBREAKING-CHANGE: the config moves'
next 1.4.2 '2.0.0' 'feat!: drop the v1 API'
next 1.4.2 '2.0.0' 'fix: a' 'fix(core)!: b' 'feat: c'
next 1.4.2 '2.0.0' 'docs: a\n\nBREAKING CHANGE: b'
next 1.4.2 '1.5.0' 'feat: a'
next 1.4.2 '1.4.3' 'fix: a'
next 0.9.9 '0.10.0' 'feat: a'
next 0.0.0 '0.1.0' 'feat: the first one'
next 0.0.0 '0.0.1' 'fix: the first one'
next 0.1.0 '0.1.1' 'FIX: case does not matter'
# Not a breaking change: the footer must start a line, in a conventional commit; "!" must
# come before the colon.
next 1.0.0 '1.0.1' 'fix: a\n\nNo BREAKING CHANGE: here'
next 1.0.0 '' 'Not conventional\n\nBREAKING CHANGE: ignored'
next 1.0.0 '1.1.0' 'feat: wow!'
# Not conventional: no space after the colon, an empty description, a type with a digit.
next 1.0.0 '' 'feat:no space' 'fix:' 'fix2: a'

if commits 'fix: a' | sh "$release" next v0.1.0 >/dev/null 2>&1; then
	echo "FAIL next accepts v0.1.0"
	failed=1
fi

# notes <want> <message>...
notes() {
	want=$1
	shift
	got=$(commits "$@" | sh "$release" notes)
	if [ "$got" != "$want" ]; then
		printf 'FAIL notes [%s]:\n--- got\n%s\n--- want\n%s\n' "$*" "$got" "$want"
		failed=1
	fi
}

notes 'No changes.'
notes '## Breaking changes

- drop the v1 API (#9) (c000004)
- **core:** rename (c000006)

## Features

- **node:** rejoin under its own name (#8) (c000001)

## Fixes

- a crash (c000002)
- cache the token (c000005)

## Other

- explain the backups (c000003)
- Not conventional (c000007)' \
	'feat(node): rejoin under its own name (#8)' 'fix: a crash' 'docs: explain the backups' \
	'feat!: drop the v1 API (#9)' 'perf: cache the token' 'chore(core): rename\n\nBREAKING CHANGE: x' \
	'Not conventional'
notes '## Fixes

- one (c000001)' 'fix: one'

# title <ok|bad> <title>
title() {
	if sh "$release" title "$2" 2>/dev/null; then got=ok; else got=bad; fi
	if [ "$got" != "$1" ]; then
		echo "FAIL title \"$2\": got $got, want $1"
		failed=1
	fi
}

for t in feat fix perf docs chore ci test refactor style build; do title ok "$t: a change"; done
title ok 'fix: release automatically from Conventional Commits'
title ok 'feat(core): let a node rejoin'
title ok 'feat!: drop the v1 API'
title ok 'refactor(node)!: rename the flags'
title bad 'Release automatically'
title bad 'feature: a change'
title bad 'Fix: a change'
title bad 'fix:a change'
title bad 'fix: '
title bad 'fix:  two spaces'
title bad 'fix(): empty scope'
title bad 'fix!(core): bang first'
title bad ''
title bad 'fix: a
second line'

if [ "$failed" -ne 0 ]; then exit 1; fi
echo "release.sh: ok"
