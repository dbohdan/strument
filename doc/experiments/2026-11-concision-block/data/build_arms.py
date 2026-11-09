#!/usr/bin/env python3
"""Build one Strument binary per arm from the current commit.

    build_arms.py BINDIR

A is the commit as it is. Every other arm inserts arms/<ARM>.txt into the tool
prompt at one fixed place, after the tools section and before "Every call's
result comes back to you", the same place as the pilot. Each arm's diff is
written to arms/<ARM>.diff, and the run refuses to start unless each diff
touches internal/prompts/prompts.go alone, in one hunk.
"""

import json
import os
import shutil
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = subprocess.run(["git", "rev-parse", "--show-toplevel"], cwd=HERE, check=True,
                      capture_output=True, text=True).stdout.strip()
ARMS = ["C", "C-length", "C-direct", "P"]
ANCHOR = '\t"Every call\'s result comes back to you, so you can keep working within the same turn: read a " +'


def sh(*cmd, cwd=None):
    return subprocess.run(cmd, cwd=cwd, check=True, capture_output=True, text=True).stdout


def main():
    bindir = os.path.abspath(sys.argv[1])
    os.makedirs(bindir, exist_ok=True)
    head = sh("git", "rev-parse", "--short", "HEAD", cwd=REPO).strip()
    print(f"building from {head}")
    sh("go", "build", "-o", os.path.join(bindir, "strument-A"), "./cmd/strument", cwd=REPO)
    for arm in ARMS:
        wt = os.path.join(bindir, "wt-" + arm)
        if os.path.exists(wt):
            sh("git", "worktree", "remove", "--force", wt, cwd=REPO)
        sh("git", "worktree", "add", "-q", "--detach", wt, "HEAD", cwd=REPO)
        p = os.path.join(wt, "internal", "prompts", "prompts.go")
        s = open(p).read()
        assert s.count(ANCHOR) == 1, "the anchor moved; update ANCHOR"
        text = open(os.path.join(HERE, "arms", arm + ".txt")).read()
        s = s.replace(ANCHOR, "\t" + json.dumps(text) + " +\n" + ANCHOR)
        open(p, "w").write(s)
        diff = sh("git", "diff", cwd=wt)
        files = [l for l in diff.splitlines() if l.startswith("+++ ")]
        hunks = [l for l in diff.splitlines() if l.startswith("@@")]
        assert files == ["+++ b/internal/prompts/prompts.go"] and len(hunks) == 1, f"{arm}: unexpected diff"
        open(os.path.join(HERE, "arms", arm + ".diff"), "w").write(diff)
        sh("go", "build", "-o", os.path.join(bindir, "strument-" + arm), "./cmd/strument", cwd=wt)
        sh("git", "worktree", "remove", "--force", wt, cwd=REPO)
        print(f"{arm}: built, {len(text)} bytes inserted")


if __name__ == "__main__":
    main()
