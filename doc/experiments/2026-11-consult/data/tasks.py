#!/usr/bin/env python3
"""The consult trial's task set: documentation work on four small projects.

    tasks.py --check DIR    build every fixture under DIR, verify each plant by
                            running the code it is about, and check each task's
                            fact checks both ways: a stale or broken reference
                            fails them and a correct one passes

Each task asks for one file to be rewritten (or written) as user-facing text.
The projects share a STYLE.md, the house style the judge is told to apply.

Several tasks carry a plant: the current documentation states a value the
code contradicts — a stale default, a missing option — so a version that
copies the old text is wrong where the code is right. Plants are verified by
running the code, not by matching its text (experimenting.md §26).

Fact checks are counts, not judgments: values that must appear, values that
must not, flags that do not exist, format verbs a string must keep, and
whether a source file still parses. They are reported beside the judge's
verdicts, never folded into them.
"""

import json
import os
import re
import subprocess
import sys
import tempfile

STYLE = """# House style for user-facing text

- Write calm, specific sentences. Lead with what the reader can do.
- Name things exactly: the flag, the file, the default value.
- Do not hedge where the behavior is known ("may", "might", "should be able to").
- No marketing words, no exclamation marks, no emoji.
- Help text and error messages use sentence case and plain statements.
- An error message says what happened, why if it is known, and what to do next.
- Keep it short. Cut words that do not change what the reader knows or does.
"""

# --- tally: a Go CLI that adds up numbers --------------------------------

TALLY_MAIN = """package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

var (
	sum       = flag.Bool("sum", false, "print only the total")
	precision = flag.Int("precision", 2, "digits after the decimal point")
	sep       = flag.String("sep", ",", "field separator within a line")
)

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: tally [--sum] [--precision N] [--sep S] FILE")
		os.Exit(2)
	}
	var in io.Reader = os.Stdin
	if name := flag.Arg(0); name != "-" {
		f, err := os.Open(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		in = f
	}
	total := 0.0
	sc := bufio.NewScanner(in)
	for n := 1; sc.Scan(); n++ {
		for _, field := range strings.Split(sc.Text(), *sep) {
			field = strings.TrimSpace(field)
			if field == "" {
				continue
			}
			v, err := strconv.ParseFloat(field, 64)
			if err != nil {
				fmt.Fprintf(os.Stderr, errBadLine+"\\n", n, field)
				os.Exit(1)
			}
			total += v
			if !*sum {
				fmt.Printf("%.*f\\n", *precision, v)
			}
		}
	}
	fmt.Printf("%.*f\\n", *precision, total)
}
"""

TALLY_ERRORS = """package main

// errBadLine is printed when a field is not a number. The first verb is the
// line number and the second is the field as it was read.
const errBadLine = "ERROR!!! tally could not understand line %d, it seems like %q might not be a number"
"""

TALLY_HELP = """Usage: tally [options] FILE

tally is a simple and powerful tool that will add up all of the numbers that it
finds in FILE for you. Each number is printed as it is read, and then the total
is printed at the very end.

Options:
  --sum            If you only want to see the total, you can pass this flag and
                   tally will print only the total and nothing else.
  --precision N    This sets how many digits should be shown after the decimal
                   point. It defaults to 3.
  --sep S          Fields on a line may be separated by S.
"""

TALLY_README = """# tally

tally adds up numbers.

## Usage

To use tally, you will want to run it with the name of a file that contains
some numbers. The numbers can be one per line, or there might be several on a
line, in which case they should probably be separated by commas, although you
can also change that if you want to by using the right option. There are a few
options that can be used to change how tally behaves, which are described in
`tally --help`. By default the output shows three digits after the decimal
point.

## License

MIT
"""

# --- rotor: a Python log rotator ------------------------------------------

ROTOR_CONFIG = '''"""Configuration for rotor."""

import re

DEFAULTS = {
    "keep": 7,           # rotated files kept, oldest deleted first
    "max_size": "10M",   # rotate when the log grows past this
    "compress": True,    # gzip rotated files
}

_UNITS = {"K": 1024, "M": 1024**2, "G": 1024**3}


def parse_size(text):
    """Turn "10M" into bytes. Units are K, M and G, powers of 1024."""
    m = re.fullmatch(r"(\\d+)([KMG])", text.strip())
    if not m:
        raise ValueError(f"bad size {text!r}: use a number and K, M or G, such as 10M")
    return int(m.group(1)) * _UNITS[m.group(2)]


def load(path):
    values = dict(DEFAULTS)
    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            key, _, value = line.partition("=")
            key, value = key.strip(), value.strip()
            if key not in DEFAULTS:
                raise ValueError(f"unknown option {key!r}")
            if key == "keep":
                values[key] = int(value)
            elif key == "compress":
                values[key] = value.lower() in ("1", "true", "yes")
            else:
                parse_size(value)
                values[key] = value
    return values
'''

ROTOR_CLI = '''"""rotor's command line."""

import argparse
import sys

from rotor import config

MISSING_CONFIG = "Oops! We looked everywhere but could not find the config file :( Path: {path}"


def main(argv=None):
    ap = argparse.ArgumentParser(prog="rotor")
    ap.add_argument("--config", default="/etc/rotor.conf", help="configuration file")
    ap.add_argument("log", help="log file to rotate")
    args = ap.parse_args(argv)
    try:
        settings = config.load(args.config)
    except FileNotFoundError:
        print(MISSING_CONFIG.format(path=args.config), file=sys.stderr)
        return 2
    print(settings)
    return 0


if __name__ == "__main__":
    sys.exit(main())
'''

ROTOR_DOC = """# Configuration

rotor reads `/etc/rotor.conf`, one `option = value` per line.

## keep

How many rotated files to keep. Defaults to 5.

## max_size

The size at which the log is rotated, for example `10M`.
"""

ROTOR_README = """# rotor

rotor is a blazing-fast, next-generation solution that seamlessly takes care of
all your log file needs, so you can focus on what really matters!

## Install

    pip install rotor
"""

ROTOR_CHANGELOG = """# Changelog

## Unreleased

## 1.4.0

- Add `compress`.
"""

ROTOR_FIX = """--- a/rotor/rotate.py
+++ b/rotor/rotate.py
@@ -12,7 +12,7 @@ def prune(directory, base, keep):
     rotated = sorted(glob.glob(os.path.join(directory, base + ".*")), key=os.path.getmtime)
-    excess = len(rotated) - keep - 1
+    excess = len(rotated) - keep
     for path in rotated[:max(excess, 0)]:
         os.remove(path)
"""

# --- pinwheel: a TypeScript retry library -----------------------------------

PIN_RETRY = """export interface RetryOptions {
  attempts?: number;
  baseMs?: number;
  factor?: number;
  maxMs?: number;
}

export const DEFAULTS: Required<RetryOptions> = {
  attempts: 3,
  baseMs: 200,
  factor: 2,
  maxMs: 5000,
};

export function delayFor(attempt: number, opts: Required<RetryOptions>): number {
  const cap = Math.min(opts.maxMs, opts.baseMs * Math.pow(opts.factor, attempt - 1));
  return Math.random() * cap; // full jitter
}

// retry calls fn until it resolves.
export async function retry<T>(fn: () => Promise<T>, opts: RetryOptions = {}): Promise<T> {
  const o = { ...DEFAULTS, ...opts };
  let last: unknown;
  for (let attempt = 1; attempt <= o.attempts; attempt++) {
    try {
      return await fn();
    } catch (err) {
      last = err;
      if (attempt < o.attempts) {
        await new Promise((r) => setTimeout(r, delayFor(attempt, o)));
      }
    }
  }
  throw last;
}
"""

PIN_LEGACY = """import { retry } from "./retry";

// retryLegacy is the 1.x API. It is removed in 3.0.
export function retryLegacy<T>(fn: () => Promise<T>, times: number): Promise<T> {
  console.warn("WARNING: retryLegacy is deprecated!!! please switch to the new one asap");
  return retry(fn, { attempts: times });
}
"""

PIN_README = """# pinwheel

Retry a promise-returning function.

## Retries and backoff

pinwheel will retry your function a number of times (5 by default) and it waits
a bit between tries, and the wait gets longer each time.

## License

MIT
"""

PIN_CHANGELOG = """# Changelog

## 3.0.0 (unreleased)

- Remove `retryLegacy`, deprecated since 2.1.
- `maxMs` now defaults to 5000. In 2.x there was no cap on a single delay.

## 2.1.0

- Deprecate `retryLegacy` in favour of `retry`.
"""

PIN_UPGRADING = """# Upgrading

## From 1.x to 2.x

Rename `backoff` to `baseMs`.
"""

# --- grepkit: a Go library behind a model-facing search tool ------------------

GREP_TOOLS = """package grepkit

// ToolDef is a tool offered to a language model.
type ToolDef struct {
	Name        string
	Description string
}

// DefaultLimit is how many matches a search returns unless told otherwise.
const DefaultLimit = 100

// SearchTool is offered to the model. Its parameters are pattern (a regular
// expression, required), path (a directory or file, default the project root),
// ignore_case (default false) and limit (default DefaultLimit).
var SearchTool = ToolDef{
	Name:        "search",
	Description: "This tool lets you maybe search for stuff in the files, I guess it uses regex or something",
}
"""

GREP_PROMPTS = """package grepkit

// confirmDelete asks before deleting the files a search matched. The verb is
// the number of files.
const confirmDelete = "Are you really really REALLY sure you want to delete all %d of these files???"
"""

GREP_PARSE = """package grepkit

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// ParseTimeout parses a timeout.
func ParseTimeout(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty timeout")
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 {
			return 0, errors.New("negative timeout")
		}
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, errors.New("negative timeout")
	}
	return d, nil
}
"""

GREP_DIFF = """--- a/config.go
+++ b/config.go
@@ -8,12 +8,22 @@ type Config struct {
-	Timeout string `toml:"timeout"`
+	TimeoutSeconds int `toml:"timeout_seconds"`
+	// LegacyTimeout reads the old key so existing files keep working.
+	LegacyTimeout string `toml:"timeout"`
 }
+
+// effectiveTimeout prefers timeout_seconds and falls back to the old timeout
+// key, which is read but no longer documented.
+func (c Config) effectiveTimeout() (time.Duration, error) {
+	if c.TimeoutSeconds > 0 {
+		return time.Duration(c.TimeoutSeconds) * time.Second, nil
+	}
+	return ParseTimeout(c.LegacyTimeout)
+}
"""

GREP_GOMOD = "module example.com/grepkit\n\ngo 1.22\n"
TALLY_GOMOD = "module example.com/tally\n\ngo 1.22\n"

PROJECTS = {
    "tally": {
        "STYLE.md": STYLE, "go.mod": TALLY_GOMOD, "main.go": TALLY_MAIN, "errors.go": TALLY_ERRORS,
        "help.txt": TALLY_HELP, "README.md": TALLY_README,
    },
    "rotor": {
        "STYLE.md": STYLE, "rotor/__init__.py": "", "rotor/config.py": ROTOR_CONFIG, "rotor/cli.py": ROTOR_CLI,
        "docs/config.md": ROTOR_DOC, "README.md": ROTOR_README, "CHANGELOG.md": ROTOR_CHANGELOG,
        "fix.diff": ROTOR_FIX,
    },
    "pinwheel": {
        "STYLE.md": STYLE, "src/retry.ts": PIN_RETRY, "src/legacy.ts": PIN_LEGACY, "README.md": PIN_README,
        "CHANGELOG.md": PIN_CHANGELOG, "docs/upgrading.md": PIN_UPGRADING,
    },
    "grepkit": {
        "STYLE.md": STYLE, "go.mod": GREP_GOMOD, "tools.go": GREP_TOOLS, "prompts.go": GREP_PROMPTS,
        "parse.go": GREP_PARSE, "config.diff": GREP_DIFF,
    },
}

TALLY_FLAGS = {"--sum", "--precision", "--sep", "--help", "-h"}

# Each task: project, target (the file to write), the files the work needs
# (sent to the generator and the judge, and what a consult ought to include),
# the instruction, and the fact checks:
#   must      regexes that must all match the new text
#   must_not  regexes none of which may match
#   flags     the real flags; any other --flag in the text is an invention
#   verbs     format verbs the text must keep, in order
#   syntax    "go" or "python": the target must still parse
TASKS = {
    "tally-help": {
        "project": "tally", "target": "help.txt", "files": ["STYLE.md", "main.go", "help.txt"],
        "instruction": "Rewrite help.txt, tally's --help text, so it is short and accurate to main.go.",
        "must": [r"(?i)precision[^\n]*(default[^\n]*\b(2|two)\b|\b(2|two)\b[^\n]*default)|default[^\n]*\b(2|two)\b",
                 r"(?<![-\w])-(?![-\w])|stdin|standard input"],
        "must_not": [r"(?i)defaults? (is |to )?(3|three)\b", r"(?i)powerful|simple and"],
        "flags": TALLY_FLAGS,
    },
    "tally-error": {
        "project": "tally", "target": "errors.go", "files": ["STYLE.md", "main.go", "errors.go"],
        "instruction": "Rewrite the errBadLine message in errors.go to follow STYLE.md. Keep the "
                       "file otherwise as it is.",
        "must": [r"const errBadLine = "],
        "must_not": [r"!", r"(?i)might|seems"],
        "verbs": ["%d", "%q"], "syntax": "go",
    },
    "tally-readme": {
        "project": "tally", "target": "README.md", "files": ["STYLE.md", "main.go", "README.md"],
        "instruction": "Rewrite the Usage section of README.md so it is short and accurate to "
                       "main.go. Leave the other sections as they are.",
        "must": [r"(?i)comma|\",\"|`,`", r"(?i)\b(2|two)\b", r"## License"],
        "must_not": [r"(?i)three digits|\b3 digits", r"(?i)probably|might"],
        "flags": TALLY_FLAGS,
    },
    "rotor-config": {
        "project": "rotor", "target": "docs/config.md", "files": ["STYLE.md", "rotor/config.py", "docs/config.md"],
        "instruction": "Bring docs/config.md up to date with rotor/config.py: every option, its "
                       "default, and the accepted values.",
        "must": [r"(?i)\bkeep\b[\s\S]{0,200}\b(7|seven)\b", r"\b10M\b", r"(?i)compress[\s\S]{0,200}\b(true|on|enabled|yes)\b",
                 r"\bK\b[\s\S]{0,40}\bM\b[\s\S]{0,40}\bG\b|\bK, M(,)? (and|or) G\b"],
        "must_not": [r"(?i)defaults? to (5|five)\b"],
    },
    "rotor-missing": {
        "project": "rotor", "target": "rotor/cli.py", "files": ["STYLE.md", "rotor/cli.py"],
        "instruction": "Rewrite the MISSING_CONFIG message in rotor/cli.py to follow STYLE.md. "
                       "Keep the file otherwise as it is.",
        "must": [r"MISSING_CONFIG = ", r"\{path\}", r"--config"],
        "must_not": [r"(?i)oops|looked everywhere|:\(", r"MISSING_CONFIG = [^\n]*!"],
        "syntax": "python",
    },
    "rotor-readme": {
        "project": "rotor", "target": "README.md", "files": ["STYLE.md", "rotor/config.py", "rotor/cli.py", "README.md"],
        "instruction": "Replace the opening paragraph of README.md with one that says what rotor does. "
                       "Keep the Install section.",
        "must": [r"(?i)rotat", r"## Install"],
        "must_not": [r"(?i)blazing|next-generation|seamless|what really matters", r"!"],
    },
    "rotor-changelog": {
        "project": "rotor", "target": "CHANGELOG.md", "files": ["STYLE.md", "CHANGELOG.md", "fix.diff"],
        "instruction": "fix.diff is the change about to be released. Add its entry under Unreleased "
                       "in CHANGELOG.md.",
        "must": [r"## Unreleased\s*\n\s*\n?- [^\n]*\bkeep\b", r"## 1\.4\.0"],
        "must_not": [r"!"],
    },
    "pin-readme": {
        "project": "pinwheel", "target": "README.md", "files": ["STYLE.md", "src/retry.ts", "README.md"],
        "instruction": "Rewrite the Retries and backoff section of README.md so it states exactly what "
                       "retry() does by default. Leave the other sections as they are.",
        "must": [r"(?i)\b(3|three)\b", r"\b200\b", r"\b5000\b|\b5 ?s(econds)?\b|5,000", r"(?i)jitter|random"],
        "must_not": [r"(?i)\(5 by default\)|defaults? (is |to )?(5|five) (times|attempts|tries)"],
    },
    "pin-jsdoc": {
        "project": "pinwheel", "target": "src/retry.ts", "files": ["STYLE.md", "src/retry.ts"],
        "instruction": "Replace the comment above retry() in src/retry.ts with a JSDoc block that "
                       "documents the function and each option with its default. Change nothing else.",
        "must": [r"/\*\*", r"@param", r"(?i)\b(3|three)\b", r"\b200\b", r"\b5000\b|5,000",
                 r"export async function retry<T>\(fn: \(\) => Promise<T>, opts: RetryOptions = \{\}\): Promise<T> \{"],
        "must_not": [r"// retry calls fn until it resolves\."],
        "syntax": "ts",
    },
    "pin-deprecation": {
        "project": "pinwheel", "target": "src/legacy.ts", "files": ["STYLE.md", "src/legacy.ts", "CHANGELOG.md"],
        "instruction": "Rewrite the deprecation warning in src/legacy.ts to follow STYLE.md. Change "
                       "nothing else.",
        "must": [r"console\.warn\(", r"\bretry\(|`retry`|\bretry\b(?!Legacy)", r"3\.0"],
        "must_not": [r"(?i)!!!|asap|the new one"],
        "syntax": "ts",
    },
    "pin-upgrade": {
        "project": "pinwheel", "target": "docs/upgrading.md",
        "files": ["STYLE.md", "CHANGELOG.md", "src/retry.ts", "src/legacy.ts", "docs/upgrading.md"],
        "instruction": "Add a section to docs/upgrading.md for upgrading from 2.x to 3.0.",
        "must": [r"(?i)## .*2\.x.*3\.0|## .*3\.0", r"retryLegacy", r"\bretry\(|`retry`", r"\b5000\b|5,000|5 ?s",
                 r"## From 1\.x to 2\.x"],
        "must_not": [],
    },
    "grep-tool": {
        "project": "grepkit", "target": "tools.go", "files": ["STYLE.md", "tools.go"],
        "instruction": "Rewrite SearchTool's Description in tools.go. A language model reads it to "
                       "decide when and how to call the tool. Change nothing else.",
        "must": [r"(?i)regular expression|regex", r"\b100\b|DefaultLimit", r"(?i)ignore_case|case"],
        "must_not": [r"(?i)maybe|I guess|or something"],
        "syntax": "go",
    },
    "grep-confirm": {
        "project": "grepkit", "target": "prompts.go", "files": ["STYLE.md", "prompts.go"],
        "instruction": "Rewrite confirmDelete in prompts.go to follow STYLE.md. Change nothing else.",
        "must": [r"const confirmDelete = "],
        "must_not": [r"(?i)really", r"\?\?"],
        "verbs": ["%d"], "syntax": "go",
    },
    "grep-commit": {
        "project": "grepkit", "target": "COMMIT_MSG.txt", "files": ["STYLE.md", "config.diff", "parse.go"],
        "instruction": "Write the commit message for config.diff into COMMIT_MSG.txt: a subject line "
                       "of at most 72 characters, a blank line, and a body.",
        "must": [r"\A[^\n]{1,72}\n\n\S", r"timeout_seconds", r"\btimeout\b"],
        "must_not": [r"!"],
    },
    "grep-doc": {
        "project": "grepkit", "target": "parse.go", "files": ["STYLE.md", "parse.go"],
        "instruction": "Replace ParseTimeout's doc comment in parse.go with one that says exactly "
                       "what it accepts and what it rejects. Change nothing else.",
        "must": [r"(?i)second", r"(?i)empty", r"(?i)negative", r"(?i)time\.ParseDuration|duration string|\"1m30s\"|\b\d+(ms|s|m|h)\b",
                 r"func ParseTimeout\(s string\) \(time\.Duration, error\) \{"],
        "must_not": [r"// ParseTimeout parses a timeout\.\n"],
        "syntax": "go",
    },
}


def build(project, d):
    """Write project's files under d."""
    for rel, body in PROJECTS[project].items():
        p = os.path.join(d, rel)
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w") as f:
            f.write(body)


def files_of(task):
    t = TASKS[task]
    return {rel: PROJECTS[t["project"]][rel] for rel in t["files"]}


FLAG_RE = re.compile(r"(?<![\w-])--[a-z][a-z-]*")


def syntax_ok(kind, text):
    """Whether text parses as kind. None when there is no check."""
    if not kind:
        return None
    with tempfile.TemporaryDirectory() as d:
        if kind == "go":
            p = os.path.join(d, "x.go")
            open(p, "w").write(text)
            return subprocess.run(["gofmt", "-e", p], capture_output=True).returncode == 0
        if kind == "python":
            p = os.path.join(d, "x.py")
            open(p, "w").write(text)
            return subprocess.run([sys.executable, "-I", "-m", "py_compile", p], capture_output=True).returncode == 0
        if kind == "ts":
            p = os.path.join(d, "x.ts")
            open(p, "w").write(text)
            r = subprocess.run(["tsc", "--noEmit", "--noResolve", "--isolatedModules", "--target", "es2020",
                                "--module", "es2020", p], capture_output=True, text=True)
            # An option error means tsc checked nothing. Fail loudly rather
            # than read its silence about syntax as a pass: an empty --types
            # once did exactly that, and every file "parsed".
            if re.search(r"error TS(5|6)\d{3}", r.stdout):
                raise RuntimeError("tsc refused its options: " + r.stdout[:300])
            # Only syntax: an import tsc cannot resolve with --noResolve is
            # not this file's fault. TS1xxx codes are syntax errors.
            return not re.search(r"error TS1\d{3}", r.stdout)
    return None


def facts(task, text):
    """The fact checks on one candidate, as counts and lists."""
    t = TASKS[task]
    missing = [p for p in t.get("must", []) if not re.search(p, text)]
    present = [p for p in t.get("must_not", []) if re.search(p, text)]
    invented = sorted(set(FLAG_RE.findall(text)) - t["flags"]) if "flags" in t else []
    verbs_ok = None
    if "verbs" in t:
        found = re.findall(r"%[a-z]", text)
        verbs_ok = found == t["verbs"]
    syn = syntax_ok(t.get("syntax"), text)
    ok = not missing and not present and not invented and verbs_ok is not False and syn is not False
    return {"ok": ok, "missing": missing, "forbidden": present, "invented_flags": invented,
            "verbs_ok": verbs_ok, "syntax_ok": syn}


# Reference answers: a correct version, and the version that copies the stale
# text, for checking the checks both ways.
GOOD = {
    "tally-help": "Usage: tally [--sum] [--precision N] [--sep S] FILE\n\nAdds up the numbers in FILE, or "
                  "standard input when FILE is -.\n\n  --sum          Print only the total.\n  --precision N  "
                  "Digits after the decimal point. Default 2.\n  --sep S        Field separator. Default \",\".\n",
    "tally-error": TALLY_ERRORS.replace(
        '"ERROR!!! tally could not understand line %d, it seems like %q might not be a number"',
        '"line %d: %q is not a number"'),
    "tally-readme": TALLY_README.split("## Usage")[0] + "## Usage\n\n    tally FILE\n\nNumbers may be one per line "
                    "or separated by commas (change with `--sep`). Output shows 2 digits after the decimal "
                    "point; see `tally --help`.\n\n## License\n\nMIT\n",
    "rotor-config": "# Configuration\n\n## keep\n\nRotated files kept. Default 7.\n\n## max_size\n\nRotate past "
                    "this size: a number and K, M or G. Default 10M.\n\n## compress\n\nGzip rotated files. "
                    "Default true.\n",
    "rotor-missing": ROTOR_CLI.replace(
        '"Oops! We looked everywhere but could not find the config file :( Path: {path}"',
        '"No config file at {path}. Create it, or pass --config with another path."'),
    "rotor-readme": "# rotor\n\nrotor rotates a log file when it grows past a size, keeps a set number of "
                    "rotated copies, and gzips them.\n\n## Install\n\n    pip install rotor\n",
    "rotor-changelog": "# Changelog\n\n## Unreleased\n\n- Fix pruning, which kept one more file than `keep`.\n\n"
                       "## 1.4.0\n\n- Add `compress`.\n",
    "pin-readme": PIN_README.replace(
        "pinwheel will retry your function a number of times (5 by default) and it waits\na bit between "
        "tries, and the wait gets longer each time.",
        "`retry()` makes up to 3 attempts. Before each retry it waits a random time up to\n200 ms × 2^(n−1), "
        "capped at 5000 ms (full jitter)."),
    "pin-jsdoc": PIN_RETRY.replace(
        "// retry calls fn until it resolves.",
        "/**\n * Calls fn until it resolves, waiting between attempts.\n * @param fn The operation.\n * "
        "@param opts attempts (default 3), baseMs (default 200), factor (default 2), maxMs (default 5000).\n */"),
    "pin-deprecation": PIN_LEGACY.replace(
        '"WARNING: retryLegacy is deprecated!!! please switch to the new one asap"',
        '"retryLegacy is deprecated and is removed in 3.0. Use retry(fn, { attempts }) instead."'),
    "pin-upgrade": PIN_UPGRADING + "\n## From 2.x to 3.0\n\n- `retryLegacy` is removed. Call `retry(fn, { attempts })`."
                   "\n- `maxMs` defaults to 5000. Pass a larger value to keep 2.x delays.\n",
    "grep-tool": GREP_TOOLS.replace(
        '"This tool lets you maybe search for stuff in the files, I guess it uses regex or something"',
        '"Search file contents with a regular expression. Set ignore_case for a case-insensitive match. '
        'Returns up to limit matches, 100 by default."'),
    "grep-confirm": GREP_PROMPTS.replace(
        '"Are you really really REALLY sure you want to delete all %d of these files???"',
        '"Delete %d files?"'),
    "grep-commit": "Rename the timeout config key to timeout_seconds\n\nThe old timeout key is still read, so "
                   "existing files keep working.\n",
    "grep-doc": GREP_PARSE.replace(
        "// ParseTimeout parses a timeout.\n",
        "// ParseTimeout accepts a whole number of seconds (\"30\") or a time.ParseDuration\n// string "
        "(\"1m30s\"). It rejects an empty string and a negative value.\n"),
}


def stale(task):
    """The candidate a stale-copying or careless model would write: the file as shipped."""
    t = TASKS[task]
    return PROJECTS[t["project"]].get(t["target"], "")


def verify_plants(root):
    """Run the code each plant is about and read the value it produces."""
    problems = []
    # The defaults are read by a test file written only here, beside the
    # fixture rather than in it: a flag added to the fixture for this purpose
    # was documented by every model as a real one.
    d = os.path.join(root, "tally")
    build("tally", d)
    with open(os.path.join(d, "defaults_test.go"), "w") as f:
        f.write('package main\n\nimport (\n\t"flag"\n\t"testing"\n)\n\nfunc TestDefaults(t *testing.T) {\n'
                '\tfor _, n := range []string{"sum", "precision", "sep"} {\n'
                '\t\tt.Logf("%s=%s", n, flag.Lookup(n).DefValue)\n\t}\n}\n')
    out = subprocess.run(["go", "test", "-run", "TestDefaults", "-v", "."], cwd=d, capture_output=True, text=True)
    if not all(s in out.stdout for s in ("sum=false", "precision=2", "sep=,")):
        problems.append(f"tally defaults: {out.stdout[-300:]!r} {out.stderr[-300:]!r}")
    if "defaults to 3" not in TALLY_HELP:
        problems.append("tally help.txt does not carry the stale default")
    d = os.path.join(root, "rotor")
    build("rotor", d)
    out = subprocess.run([sys.executable, "-I", "-c", "import sys; sys.path.insert(0, sys.argv[1]); "
                          "from rotor import config; print(config.DEFAULTS)", d], capture_output=True, text=True)
    if out.stdout.strip() != "{'keep': 7, 'max_size': '10M', 'compress': True}":
        problems.append(f"rotor defaults: {out.stdout!r} {out.stderr[-300:]!r}")
    if "Defaults to 5" not in ROTOR_DOC or "compress" in ROTOR_DOC:
        problems.append("rotor docs/config.md does not carry the stale keep and the missing compress")
    d = os.path.join(root, "pinwheel")
    build("pinwheel", d)
    out = subprocess.run(["node", "--experimental-strip-types", "--no-warnings", "-e",
                          "import('" + os.path.join(d, "src", "retry.ts") + "').then(m => console.log(JSON.stringify(m.DEFAULTS)))"],
                         capture_output=True, text=True)
    if json.loads(out.stdout or "null") != {"attempts": 3, "baseMs": 200, "factor": 2, "maxMs": 5000}:
        problems.append(f"pinwheel defaults: {out.stdout!r} {out.stderr[-300:]!r}")
    if "5 by default" not in PIN_README:
        problems.append("pinwheel README does not carry the stale attempts")
    d = os.path.join(root, "grepkit")
    build("grepkit", d)
    out = subprocess.run(["go", "vet", "."], cwd=d, capture_output=True, text=True)
    if out.returncode != 0:
        problems.append(f"grepkit does not build: {out.stderr[-300:]!r}")
    return problems


def check(root):
    problems = verify_plants(root)
    for task in TASKS:
        good = facts(task, GOOD[task])
        if not good["ok"]:
            problems.append(f"{task}: the reference answer fails its checks: {good}")
        bad = facts(task, stale(task))
        if bad["ok"]:
            problems.append(f"{task}: the file as shipped passes its checks")
        missing = set(TASKS[task]["files"]) - set(PROJECTS[TASKS[task]["project"]])
        if missing:
            problems.append(f"{task}: files not in the project: {missing}")
    return problems


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == "--check":
        ps = check(sys.argv[2])
        for p in ps:
            print("PROBLEM", p)
        print(f"{len(TASKS)} tasks, {len(ps)} problems")
        sys.exit(1 if ps else 0)
    print(__doc__)


# --- The long set: whole documentation pages, written from Strument's own code ---
#
# Short tasks left a good writer no room to be better: every candidate passed
# its fact checks and most pairs tied. These ask for a user-facing page about
# one feature, from the source alone, frozen at LONG_COMMIT so the input cannot
# drift with the branch. Neither writer nor judge is shown the existing docs.

LONG_COMMIT = "45f0f00"

LONG_STYLE = STYLE + """
For a documentation page:

- Open with what the feature is for and when a reader would use it.
- Show configuration and commands as examples the reader can copy.
- Say what the defaults are and what each setting changes.
- Write for a user of the tool, not a maintainer: describe behavior, not
  implementation, unless the reader needs it to use the feature well.
- Markdown, with a single top-level heading.
"""

LONG_TASKS = {
    "long-consult": {
        "files": ["internal/coder/consult.go", "internal/config/grants.go"],
        "target": "docs/consult.md", "subject": "the consult tool and the `consult_models` setting",
        # Not run_code's exclusion: that is in codebridge.go, which this task
        # does not send, and a check on a fact the writer cannot see scores
        # every writer down alike (the pilot: 3 of 3).
        "must": [r"consult_models", r"(?i)three.quarters|75 ?%|0\.75", r"--yes consult|\bconsult\b[^\n]{0,40}grant",
                 r"(?i)purpose|what (it|each|the model) is for"],
    },
    "long-yes": {
        "files": ["internal/config/grants.go", "internal/coder/grants.go", "internal/repl/yes.go"],
        "target": "docs/approvals.md", "subject": "approving prompts automatically: `--yes`, `auto_approve` and `/yes`",
        "must": [r"auto_approve", r"/yes", r"\bbash\b", r"\bwebfetch\b", r"\bwebsearch\b", r"\bconsult\b",
                 r"read-outside", r"\bsteps\b", r"\bcontext\b", r"add-output", r"\ball\b"],
    },
    "long-adopt": {
        "files": ["internal/history/adopt.go", "internal/history/artifact.go", "cmd/strument/project.go"],
        "target": "docs/moving-projects.md", "subject": "what happens to Strument's saved state when a project "
                                                       "is moved or renamed, and `strument project adopt`",
        "must": [r"strument project adopt", r"\.adopted-", r"(?i)nothing is deleted|not deleted|never deleted|is kept",
                 r"strument project list"],
    },
    "long-outside": {
        "files": ["internal/coder/outside.go", "internal/secretfile/secretfile.go"],
        "target": "docs/outside-reads.md", "subject": "how the model reads files outside the project, and which "
                                                     "files it may never read",
        "must": [r"(?i)secret", r"(?i)home directory", r"read-outside", r"(?i)symlink", r"\.env\b"],
    },
    "long-webfetch": {
        "files": ["internal/coder/webfetch.go"],
        "target": "docs/webfetch.md", "subject": "the webfetch tool: fetching pages, sections and outlines, and "
                                                 "when Strument asks first",
        "must": [r"webfetch_allow", r"#[a-z]", r"(?i)outline", r"(?i)origin", r"(?i)\b(this|the) run\b|rest of the run"],
    },
    "long-skills": {
        "files": ["internal/coder/skills.go", "internal/skill/discover.go"],
        "target": "docs/skills.md", "subject": "skills: where Strument looks for them, which ones are used, and how "
                                               "the model loads one",
        "must": [r"\.strument/skills", r"\.agents/skills", r"strument trust", r"SKILL\.md"],
    },
}


def _git_show(path):
    root = subprocess.run(["git", "rev-parse", "--show-toplevel"], cwd=HERE_DIR, capture_output=True,
                          text=True, check=True).stdout.strip()
    return subprocess.run(["git", "show", f"{LONG_COMMIT}:{path}"], cwd=root, capture_output=True,
                          text=True, check=True).stdout


HERE_DIR = os.path.dirname(os.path.abspath(__file__))


def use(which):
    """Switch TASKS and PROJECTS to the long set. The scripts call this for
    --set long; everything else reads TASKS, files_of and facts as before."""
    if which != "long":
        return
    proj = {"STYLE.md": LONG_STYLE}
    for t in LONG_TASKS.values():
        for rel in t["files"]:
            proj[rel] = _git_show(rel)
    PROJECTS.clear()
    PROJECTS["strument"] = proj
    TASKS.clear()
    for name, t in LONG_TASKS.items():
        TASKS[name] = {
            "project": "strument", "target": t["target"], "files": ["STYLE.md"] + t["files"],
            "instruction": f"Write {t['target']}, a user-facing documentation page about {t['subject']}, "
                           "from the source files below.",
            "must": t["must"], "must_not": [r"!"],
        }
