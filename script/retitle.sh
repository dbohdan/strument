#!/bin/sh
# Retitle a commit whose subject is a placeholder like
# "(no commit message provided)". The body — and any trailer it carries,
# such as "Assisted-by:" — is kept; only the subject line is replaced.
#
# Usage: retitle.sh <sha> <new-subject> [<upstream>]
#
# <upstream> limits how far back to search and where the replay stops; the
# default is <sha>~1. The branch must be otherwise clean. The commit is
# located on the current branch by patch-id, so a prior rebase that already
# changed its hash does not matter.
set -eu

sha="$1"
subject="$2"
upstream="${3:-$(git rev-parse --short "$sha~1")}"

# Find the commit on the current branch with the same patch-id as <sha>.
want_pid=$(git show "$sha" | git patch-id --stable | cut -d' ' -f1)
target=
for c in $(git rev-list --reverse "$upstream..HEAD"); do
	pid=$(git show "$c" | git patch-id --stable | cut -d' ' -f1)
	if [ "$pid" = "$want_pid" ]; then
		target=$c
		break
	fi
done
if [ -z "$target" ]; then
	echo "retitle.sh: no commit on HEAD matches $sha" >&2
	exit 1
fi

# New message: subject, then the original body minus the placeholder line.
msgfile=$(mktemp)
	printf '%s\n\n' "$subject" >"$msgfile"
	git log --format=%b -1 "$target" |
		sed '/^(no commit message provided)$/d' >>"$msgfile"

# Replay the branch from before the target, amending only that commit.
branch=$(git rev-parse --abbrev-ref HEAD)
old_head=$(git rev-parse HEAD)
rest=$(git rev-list --reverse "$target..$old_head")

git checkout -q --detach "$target~1"
git cherry-pick --allow-empty "$target" >/dev/null
git commit --amend -q -F "$msgfile"
rm -f "$msgfile"
for c in $rest; do
	git cherry-pick --allow-empty "$c" >/dev/null
done

# Move the branch to the rewritten tip and return to it.
new_head=$(git rev-parse HEAD)
git checkout -q "$branch"
git reset -q --hard "$new_head"
