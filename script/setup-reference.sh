#!/bin/sh
# Restore the spec-verification environment: clone aider at the pinned SHA
# into reference/ and drop in the go.mod carve-out.
#
# reference/ is a read-only grep target for checking the port against
# upstream (see spec/strument-guide.md); it is gitignored and never
# committed, so a fresh clone of Strument does not carry aider's 13k-commit
# history. Strument itself builds and tests without reference/ — you only
# need this to consult or re-verify against the original source.
#
# Usage: script/setup-reference.sh
set -eu

AIDER_REPO="https://github.com/Aider-AI/aider"
AIDER_SHA="5dc9490bb35f9729ef2c95d00a19ccd30c26339c"

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
ref_dir="$repo_root/reference"

if [ -e "$ref_dir/.git" ]; then
	echo "reference/ already exists; leaving it in place."
	echo "Remove it and re-run to re-clone."
	exit 0
fi

echo "Cloning aider into reference/ ..."
git clone --no-checkout "$AIDER_REPO" "$ref_dir"
git -C "$ref_dir" checkout --detach "$AIDER_SHA"

# The carve-out: a stub go.mod so `go build ./...` in the outer module does
# not walk aider's Go test fixtures.
printf '%s\n' 'module reference_excluded // carve-out: stops the outer module from walking this tree' \
	>"$ref_dir/go.mod"

echo "reference/ is ready at $AIDER_SHA."
