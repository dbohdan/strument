"""tabulate.py RUNS_DIR: one row per run, from its transcript, grade and session record.

Every column is read off a file; none is a judgment. `equipment` names runs whose
end was caused by the rig rather than the model. They are kept in the table and
left out of the model results.
"""
import collections, glob, json, os, re, sys

# How the session ended, from the last 1500 characters of out.txt. First match wins.
ENDS = [
    ("leak caught", r"tool call \(<tool_call>\) came before its reasoning block closed"),
    ("context full", r"Pass `--yes context`"),
    ("no answer", r"reasoning but no answer|gave no answer"),
    ("output cap", r"raise `max_output`|prefill = True"),
    ("loop", r"repeating itself"),
    ("recovery limit", r"Error-recovery limit reached"),
]

EQUIPMENT = {
    **{f"ling-tiny-{i}": "provider accepted requests and never answered" for i in range(6)},
    "spark-4b-0": "server behind the VM's proxy stopped answering (502)",
    "spark-4b-1": "ended by hand to give run 0 the whole server",
    "spark-4b-2": "ended by hand to give run 0 the whole server",
    "spark-4b-3": "detached launch killed with the container",
    "spark-4b-4": "launched after a container restart against a dead egress proxy",
    "qwen35-4b-local-0": "configured context 131072 against the server's 65536 (HTTP 400)",
    "qwen35-4b-local-think-1": "tunnel to the GPU machine failed after about 2 minutes",
    "qwen35-4b-local-think-2": "tunnel down for the whole run",
}


def row(d):
    name = os.path.basename(d)
    out = open(os.path.join(d, "out.txt"), errors="replace").read()
    gp = os.path.join(d, "grade.txt")
    grade = open(gp).read().splitlines() if os.path.exists(gp) else []
    score = grade[0].split()[1] if grade else "-"
    fails = collections.Counter(l.split(": ", 1)[1] for l in grade[1:] if ": " in l)
    m = re.findall(r"exit=(\d+) seconds=(\d+)", out)
    code, secs = m[-1] if m else ("-", "-")

    p = os.path.join(d, "proj", "tetris.py")
    if not os.path.exists(p):
        prog = "no file"
    else:
        try:
            compile(open(p, errors="replace").read(), p, "exec")
            prog = "parses"
        except SyntaxError:
            prog = "syntax error"

    end = next((k for k, r in ENDS if re.search(r, out[-1500:])), None)
    if end is None:
        end = {"124": "timeout", "143": "killed", "0": "answered"}.get(code, "other")
    # A tool call whose opening markup the provider swallowed leaves only the
    # closing tags at the end of the reasoning.
    if end == "no answer" and re.search(r"</tool_call>|</function>", out[-6000:]):
        end = "leak, closers only"

    providers, cost = collections.Counter(), 0.0
    for f in glob.glob(os.path.join(d, "state", "strument", "projects", "*", "sessions", "*", "log", "*.jsonl")):
        providers.update(re.findall(r'"provider":"([^"]*)"', open(f, errors="replace").read()))
    for f in glob.glob(os.path.join(d, "state", "strument", "projects", "*", "cost.jsonl")):
        cost += sum(json.loads(l).get("cost", 0) for l in open(f) if l.strip())
    prov = " ".join(f"{k}x{v}" for k, v in providers.most_common()) or "local"
    failstr = " ".join(f"{k.replace(' ', '_')}={v}" for k, v in sorted(fails.items())) or "-"
    return [name, score, prog, end, failstr, prov, f"{cost:.4f}", secs, EQUIPMENT.get(name, "")]


print("\t".join(["run", "score", "program", "end", "failing_tests", "provider", "cost_usd", "seconds", "equipment"]))
for d in sorted(glob.glob(os.path.join(sys.argv[1], "*"))):
    if os.path.isdir(d):
        print("\t".join(row(d)))
