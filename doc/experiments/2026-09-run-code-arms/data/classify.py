"""Classifies a first program's text. Run directly for the self-test, which
must pass before score.py reports anything."""
import re

PY_HOST = [
    re.compile(r"^\s*(import|from)\s+(os|glob|subprocess|pathlib|shutil|io)\b", re.M),
    re.compile(r"(?<![\w.])open\s*\("),
    re.compile(r"(?<![\w.])os\.\w"),
    re.compile(r"(?<![\w.])Path\s*\("),
    re.compile(r"(?<![\w.])subprocess\."),
]
JS_HOST = [
    re.compile(r"(?<![\w.])require\s*\("),
    re.compile(r"^\s*import\s+[\w{*]", re.M),
    re.compile(r"(?<![\w.])import\s*\("),
    re.compile(r"(?<![\w.])fs\.\w"),
    re.compile(r"(?<![\w.])process\.\w"),
    re.compile(r"(?<![\w.])fetch\s*\("),
    re.compile(r"(?<![\w.])(Deno|Bun)\.\w"),
    re.compile(r"(?<![\w.])__dirname\b"),
    re.compile(r"(?<![\w.])(std|os)\.\w"),
    re.compile(r"(?<![\w.])XMLHttpRequest\b"),
]
BRIDGED = r"(read|read_text|read_bin|grep|glob|ls|symbol)"
JS_KWARGS = re.compile(BRIDGED + r"\s*\(\s*[a-z_]+\s*=(?!=)")
JS_TS = [
    re.compile(r"\b(const|let|var)\s+\w+\s*:\s*[A-Za-z_][\w\[\]<>, |]*\s*="),
    re.compile(r"\)\s*:\s*(string|number|boolean|void|any|unknown)\b"),
    re.compile(r"^\s*(interface|type)\s+\w+\s*[={]", re.M),
    re.compile(r"\bas\s+(string|number|any|unknown)\b"),
]
JS_BARE_SORT = re.compile(r"\.sort\(\s*\)")
PY_MISSING = [
    re.compile(r"^\s*(with|match|del)\s", re.M),
    re.compile(r"(?<![\w.])(eval|exec)\s*\("),
]


def strip_strings_and_comments(code, lang):
    """Removes string literals and comments, so "fs.readFile" inside a
    message or a comment saying 'no os here' does not count."""
    if lang == "py":
        code = re.sub(r'("""|\'\'\')[\s\S]*?\1', '""', code)
        code = re.sub(r"#[^\n]*", "", code)
        code = re.sub(r'[rbfu]*"(\\.|[^"\\\n])*"|[rbfu]*\'(\\.|[^\'\\\n])*\'', '""', code)
    else:
        code = re.sub(r"/\*[\s\S]*?\*/", "", code)
        code = re.sub(r"(?<![:\\])//[^\n]*", "", code)
        code = re.sub(r"`(\\.|[^`\\])*`", "``", code)
        code = re.sub(r'"(\\.|[^"\\\n])*"|\'(\\.|[^\'\\\n])*\'', '""', code)
    return code


def classify(code, lang):
    bare = strip_strings_and_comments(code, lang)
    out = {}
    if lang == "py":
        out["host"] = any(p.search(bare) for p in PY_HOST)
        out["missing"] = any(p.search(bare) for p in PY_MISSING)
    else:
        out["host"] = any(p.search(bare) for p in JS_HOST)
        out["kwargs"] = bool(JS_KWARGS.search(bare))
        out["ts"] = any(p.search(bare) for p in JS_TS)
        out["bare_sort"] = bool(JS_BARE_SORT.search(bare))
    return out


CASES = [
    # (lang, code, key, expected)
    ("py", "import os\nfor r, d, f in os.walk('docs'):\n    pass", "host", True),
    ("py", "from pathlib import Path\nPath('x').read_text()", "host", True),
    ("py", "with open('notes.md') as f:\n    f.read()", "host", True),
    ("py", "import glob\nglob.glob('**/*.md')", "host", True),
    ("py", "files = glob(pattern='docs/**/*.md')\nlen(files)", "host", False),
    ("py", "t = read_text(path='notes.md')\nmax(len(l) for l in t.splitlines())", "host", False),
    ("py", "# os is not available, so use glob\nx = glob('**/*.md')", "host", False),
    ("py", "print('use os.walk? no')\nx = 1", "host", False),
    ("py", "import re, json\nre.findall(r'\\d+', read_text('a'))", "host", False),
    ("py", "with open('x') as f: pass", "missing", True),
    ("py", "del x[0]", "missing", True),
    ("py", "x = [1, 2]\nx.pop()", "missing", False),
    ("js", "const fs = require('fs');\nfs.readdirSync('docs')", "host", True),
    ("js", "import fs from 'node:fs';\nfs.readFileSync('a')", "host", True),
    ("js", "const t = fs.readFileSync('notes.md', 'utf8')", "host", True),
    ("js", "process.cwd()", "host", True),
    ("js", "const r = await fetch('http://x')", "host", True),
    ("js", "std.loadFile('notes.md')", "host", True),
    ("js", "const files = glob({pattern: 'docs/**/*.md'});\nfiles.length", "host", False),
    ("js", "// no fs here, use read_text\nconst t = read_text('notes.md');\nt.length", "host", False),
    ("js", "console.log('not using process.env');\n1", "host", False),
    ("js", "const important = 1; const imports = 2; important + imports", "host", False),
    ("js", "const d = new Date('2026-01-03');\nd.getTime()", "host", False),
    ("js", "grep(pattern='TODO', glob='**/*.go')", "kwargs", True),
    ("js", "grep({pattern: 'TODO', glob: '**/*.go'})", "kwargs", False),
    ("js", "x == grep('a')", "kwargs", False),
    ("js", "const xs: number[] = [];\nxs", "ts", True),
    ("js", "function f(a): number { return 1 }", "ts", True),
    ("js", "const o = {a: 1, b: 2};\no", "ts", False),
    ("js", "const x = cond ? a : b;\nx", "ts", False),
    ("js", "nums.sort()", "bare_sort", True),
    ("js", "nums.sort((a, b) => b - a)", "bare_sort", False),
]


def selftest():
    bad = []
    for lang, code, key, want in CASES:
        got = classify(code, lang)[key]
        if got != want:
            bad.append((lang, key, want, got, code))
    return bad


if __name__ == "__main__":
    bad = selftest()
    for b in bad:
        print("FAIL", b)
    print(f"{len(CASES) - len(bad)}/{len(CASES)} self-test cases pass")
    raise SystemExit(1 if bad else 0)
