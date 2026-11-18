"""The String.raw trial's tasks: fixtures, prompts, and what each session left behind.

Three tasks carry source text with backslashes through a run_code program's
string literals into edit(); each asks for exact changes, so exactly one file
is right. The fourth is the counter-metric: a program that builds a report
where "\\t" and "\\n" are meant as escapes, which a model over-applying
String.raw would write literally.
"""

import os
import subprocess

C_BEFORE = r'''#include <stdio.h>
#include <string.h>

static void banner(const char *name) {
    printf("\033[1m%s\033[0m\n", name);
    printf("\tversion %d.%d\n", 1, 4);
}

static void usage(void) {
    fputs("usage: term [-h] [-v]\n", stderr);
    fputs("  -h\tshow help\n", stderr);
}

static int is_sep(char c) {
    return c == '\\' || c == '/';
}

int main(int argc, char **argv) {
    if (argc > 1 && strcmp(argv[1], "-h") == 0) {
        usage();
        return 0;
    }
    banner("term");
    return is_sep(argv[0][0]);
}
'''

C_AFTER = (C_BEFORE
           .replace(r'printf("\033[1m%s\033[0m\n", name);', r'printf("\033[1;4m%s\033[0m\n", name);')
           .replace(r'printf("\tversion %d.%d\n", 1, 4);', r'printf("\tversion %d.%d\n", 1, 5);')
           .replace('    fputs("  -h\\tshow help\\n", stderr);\n',
                    '    fputs("  -h\\tshow help\\n", stderr);\n    fputs("  -v\\tshow version\\n", stderr);\n')
           .replace(r"return c == '\\' || c == '/';", r"return c == '\\' || c == '/' || c == ':';"))

C_TEXT = r'''Make these four changes to term.c, all in one run_code program that calls edit() for each. Change only what is listed.

1. Replace the line
    printf("\033[1m%s\033[0m\n", name);
with
    printf("\033[1;4m%s\033[0m\n", name);
2. Replace the line
    printf("\tversion %d.%d\n", 1, 4);
with
    printf("\tversion %d.%d\n", 1, 5);
3. Right after the line
    fputs("  -h\tshow help\n", stderr);
add the line
    fputs("  -v\tshow version\n", stderr);
4. Replace the line
    return c == '\\' || c == '/';
with
    return c == '\\' || c == '/' || c == ':';'''

PY_BEFORE = r'''import re

RANGE = re.compile(r"(\d+)\s*-\s*(\d+)")
WORD = re.compile(r"\b[A-Za-z_]\w*\b")
SEP = "\\"


def split_path(p):
    return p.split(SEP)


def render(rows):
    return "\n".join("\t".join(r) for r in rows) + "\n"
'''

PY_AFTER = (PY_BEFORE
            .replace(r'RANGE = re.compile(r"(\d+)\s*-\s*(\d+)")', r'RANGE = re.compile(r"(\d+)\s*(?:-|\.\.)\s*(\d+)")')
            .replace(r'WORD = re.compile(r"\b[A-Za-z_]\w*\b")', r'WORD = re.compile(r"\b[A-Za-z_][\w.]*\b")')
            .replace('SEP = "\\\\"\n', 'SEP = "\\\\"\nNEWLINE = "\\r\\n"\n')
            .replace(r'return "\n".join("\t".join(r) for r in rows) + "\n"',
                     r'return "\n".join("\t\t".join(r) for r in rows) + "\n"'))

PY_TEXT = r'''Make these four changes to parse.py, all in one run_code program that calls edit() for each. Change only what is listed.

1. Replace the line
RANGE = re.compile(r"(\d+)\s*-\s*(\d+)")
with
RANGE = re.compile(r"(\d+)\s*(?:-|\.\.)\s*(\d+)")
2. Replace the line
WORD = re.compile(r"\b[A-Za-z_]\w*\b")
with
WORD = re.compile(r"\b[A-Za-z_][\w.]*\b")
3. Right after the line
SEP = "\\"
add the line
NEWLINE = "\r\n"
4. Replace the line
    return "\n".join("\t".join(r) for r in rows) + "\n"
with
    return "\n".join("\t\t".join(r) for r in rows) + "\n"'''

SH_BEFORE = r'''#!/bin/sh
# Normalize the log before it is diffed.
sed -e 's/\(ERROR\|WARN\):/[\1]/' \
    -e 's/\t/    /g' \
    "$1" | grep -v '^\s*$' > "$1.norm"
printf '%s\n' "wrote $1.norm"
'''

SH_AFTER = (SH_BEFORE
            .replace(r"sed -e 's/\(ERROR\|WARN\):/[\1]/' \ ".rstrip(), r"sed -e 's/\(ERROR\|WARN\|INFO\):/[\1]/' \ ".rstrip())
            .replace(r"    -e 's/\t/    /g' \ ".rstrip(), r"    -e 's/\t/  /g' \ ".rstrip())
            .replace("printf '%s\\n' \"wrote $1.norm\"\n", "printf '%s\\n' \"wrote $1.norm\" >&2\n"))

SH_TEXT = r'''Make these three changes to fix.sh, all in one run_code program that calls edit() for each. Change only what is listed.

1. Replace the line
sed -e 's/\(ERROR\|WARN\):/[\1]/' \
with
sed -e 's/\(ERROR\|WARN\|INFO\):/[\1]/' \
2. Replace the line
    -e 's/\t/    /g' \
with
    -e 's/\t/  /g' \
3. Replace the line
printf '%s\n' "wrote $1.norm"
with
printf '%s\n' "wrote $1.norm" >&2'''

CSV = "name,a,b\nada,1,2\nbob,3,4\ncy,10,-1\n"
REPORT = "ada\t3\nbob\t7\ncy\t9\n"

CSV_TEXT = ('In one run_code program, read data.csv with read_text() and write report.txt with write(): '
            'one line per data row, in the file\'s order, with the name, a tab character, and the sum of a and b. '
            'No header line; end every line, the last included, with a newline. Change nothing else.')

TASKS = {
    "c": {"files": {"term.c": C_BEFORE}, "target": "term.c", "want": C_AFTER, "text": C_TEXT, "reps": 1},
    "py": {"files": {"parse.py": PY_BEFORE}, "target": "parse.py", "want": PY_AFTER, "text": PY_TEXT, "reps": 1},
    "sh": {"files": {"fix.sh": SH_BEFORE}, "target": "fix.sh", "want": SH_AFTER, "text": SH_TEXT, "reps": 1},
    "csv": {"files": {"data.csv": CSV}, "target": "report.txt", "want": REPORT, "text": CSV_TEXT, "reps": 1},
}


def git(d, *args):
    return subprocess.run(["git", "-C", d, *args], check=True, capture_output=True, text=True).stdout


def build(task, d):
    os.makedirs(d)
    git(d, "init", "-q")
    git(d, "config", "user.email", "trial@example.com")
    git(d, "config", "user.name", "Trial")
    for name, text in TASKS[task]["files"].items():
        with open(os.path.join(d, name), "w", newline="") as f:
            f.write(text)
    git(d, "add", "-A")
    git(d, "commit", "-q", "-m", "fixture")


def outcome(task, d):
    """correct: the target is exactly what was asked for. unchanged: it is what
    it was (or, for csv, absent). wrong: anything else, the silent failure."""
    t = TASKS[task]
    path = os.path.join(d, t["target"])
    got = open(path, newline="").read() if os.path.exists(path) else None
    before = t["files"].get(t["target"])
    if got == t["want"]:
        state = "correct"
    elif got == before:
        state = "unchanged"
    else:
        state = "wrong"
    others = {n: open(os.path.join(d, n), newline="").read() == text
              for n, text in t["files"].items() if n != t["target"] and os.path.exists(os.path.join(d, n))}
    return {"outcome": state, "others_intact": all(others.values())}


def check():
    """The fixtures' own consistency: every after differs from its before by
    exactly the asked changes, and every replace above applied."""
    assert C_AFTER.count("\n") == C_BEFORE.count("\n") + 1, "c: the added line"
    for a, b in [(C_BEFORE, C_AFTER), (PY_BEFORE, PY_AFTER), (SH_BEFORE, SH_AFTER)]:
        assert a != b
    assert "1;4m" in C_AFTER and ", 1, 5);" in C_AFTER and "show version" in C_AFTER and "c == ':'" in C_AFTER
    assert r"(?:-|\.\.)" in PY_AFTER and r"[\w.]*" in PY_AFTER and 'NEWLINE = "\\r\\n"' in PY_AFTER and r'"\t\t"' in PY_AFTER
    assert r"WARN\|INFO" in SH_AFTER and "'s/\\t/  /g' \\\n" in SH_AFTER and '>&2' in SH_AFTER
    assert SH_AFTER.count(" \\\n") == 2, "sh: both continuations survive"


if __name__ == "__main__":
    check()
    print("fixtures consistent")
