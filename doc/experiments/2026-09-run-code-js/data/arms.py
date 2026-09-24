"""Builds the two arms from the captured request. Monty is the capture as is;
JS replaces exactly three passages, each asserted present before replacing so
a drifted capture fails loudly instead of producing identical arms."""
import copy, json, os

HERE = os.path.dirname(os.path.abspath(__file__))

SYS_OLD = "- run_code runs a short Python program,"
SYS_NEW = "- run_code runs a short JavaScript program,"

DESC_OLD_START = "Do several lookups, or a computation"
JS_DESC = '''Do several lookups, or a computation, in one call instead of several. Use this when one answer needs multiple read/grep/glob/ls results combined, or needs arithmetic, counting, sorting, or date math.

The program can call the read-only tools directly — grep({pattern: "TODO", glob: "**/*.go"}), read({path: "a.go", limit: 20}) — up to 50 calls, each shown to the user like a direct call. Each function takes an options object as shown; a single leading argument may also be passed on its own, as in read("a.go"). Example:

```javascript
const caps = {};
for (const name of ["maxToolOutputBytes", "MaxSteps", "maxChatHistoryTokens"]) {
  caps[name] = grep({pattern: name + " =", glob: "**/*.go"});
}
caps
```

Only the program's last evaluated value comes back to you, so end it with what you want to see — two calls on two lines return the second one's result and drop the first. console.log() shows intermediate values.

The interpreter is goja, a JavaScript engine embedded in the harness. Standard JavaScript works: let and const, arrow functions, classes, template literals, destructuring, spread, try/catch, Map and Set, and JSON, Math, RegExp and Date. This is not Node or a browser: require, import, fs, path, process, child_process, fetch, timers and network access do not exist, so use glob({pattern: "**/*.py"}) to walk the tree and read() to open a file; the bash tool, not this one, runs commands. A missing name raises an error naming it — simplify and rerun; a failed program costs one cheap retry.

The callable functions are exactly: read, grep, glob, ls, symbol.

Inside a program, glob and ls return data rather than the tools' prose, and override them:
- glob({pattern}) returns the matching project-relative paths as an array of strings — data for the program, unlike the glob tool's prose. Empty array when nothing matches. The pattern is matched against the whole path, segment by segment; "**/*.go" reaches every directory, "*.go" only the root, and a bare directory name matches nothing.
- ls({path: ""}) returns one directory's entries as an array of objects {path, is_dir, link} sorted by path — data for the program, unlike the ls tool's prose. Empty path is the project root; a directory under the standard temp directory is allowed too. link is the symlink target, present only on symlinks.


Also callable, but only from inside a program (they return data for the program, not text for you):
- read_bin({path, offset: 0, limit: 4096}) reads a window of a file's raw bytes as {size, offset, truncated, data} where data is an array of 0-255 numbers. For computing over binary files (magic numbers, entropy, embedded strings); read is the text-shaped one and refuses binaries.
- read_text({path, offset: 0, limit: 0}) returns a file's text exactly as stored — no line numbers, no header — for computing over contents (lengths, parsing, hashing, counting). It keeps the file's final newline, so text.split("\\n") ends with an empty string; drop it before counting lines. read is the one to use when the answer cites a line number.'''

PARAM_OLD = "The Python program to run. Its last evaluated value is returned; use print() for intermediate values."
PARAM_NEW = "The JavaScript program to run. Its last evaluated value is returned; use console.log() for intermediate values."


def load():
    base = json.load(open(os.path.join(HERE, "request.json")))
    monty = copy.deepcopy(base)
    js = copy.deepcopy(base)

    sp = js["messages"][0]["content"]
    assert sp.count(SYS_OLD) == 1, "system bullet not found once"
    js["messages"][0]["content"] = sp.replace(SYS_OLD, SYS_NEW)

    rc = [t for t in js["tools"] if t["function"]["name"] == "run_code"]
    assert len(rc) == 1
    fn = rc[0]["function"]
    assert fn["description"].startswith(DESC_OLD_START)
    fn["description"] = JS_DESC
    p = fn["parameters"]["properties"]["code"]
    assert p["description"] == PARAM_OLD, p["description"]
    p["description"] = PARAM_NEW

    # The arms must differ, and only in the run_code tool and the system prompt.
    assert json.dumps(monty) != json.dumps(js)
    assert [t for t in monty["tools"] if t["function"]["name"] != "run_code"] == \
           [t for t in js["tools"] if t["function"]["name"] != "run_code"]
    assert monty["messages"][1:] == js["messages"][1:]
    return {"monty": monty, "js": js}


if __name__ == "__main__":
    arms = load()
    import difflib
    a = json.dumps(arms["monty"], indent=1).splitlines()
    b = json.dumps(arms["js"], indent=1).splitlines()
    changed = [l for l in difflib.unified_diff(a, b, lineterm="", n=0) if l.startswith(("+", "-")) and not l.startswith(("+++", "---"))]
    print(f"{len(changed)} changed JSON lines")
    for l in changed:
        print(l[:160])
