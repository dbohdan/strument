"""Is replace_all reached when it is named, and when it is not?

Our own archive says a feature offered only in the schema goes unused: 1/18 for
replace_all, 0/36 for code, "both unused when unnamed". The comparable naming
intervention moved code from 0/24 to 8/24. This asks the same question for the
ambiguity message, which is a better naming site than a prompt because it
arrives exactly when the feature is wanted.

The metric is uptake -- did any edit call carry replace_all -- which is a count.
The counter-metric is whether the file ends correct, since a feature that is
reached and wrong is worse than one ignored.
"""
import json, os, pathlib, random, shutil, subprocess, time

D = pathlib.Path("/tmp/ra")
# The field report's phrasing, which says nothing like "every" or "all". The
# first pilot's prompt did the naming itself and saturated both arms at 5/5,
# so the message never fired and the comparison was vacuous.
PROMPT = ("Upgrade the FreeBSD point releases in .github/workflows/ci.yml: "
          "14.3 becomes 14.5 and 15.0 becomes 15.1.")
REPS = int(os.environ.get("REPS", "5"))
CONFIG = '''p = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))
models = {"m": model(p, "z-ai/glm-5.3-flash", context = 128000, max_output = 4096,
                     reasoning = "low")}
default = "m"
sandbox = ""
'''
FILE = """name: CI
on: [push]
jobs:
  test:
    strategy:
      matrix:
        os:
          - name: freebsd
            architecture: aarch64
            version: '14.3'
            host: ubuntu-latest

          - name: freebsd
            architecture: x86-64
            version: '14.3'
            host: ubuntu-latest

          - name: freebsd
            architecture: aarch64
            version: '15.0'
            host: ubuntu-latest

          - name: freebsd
            architecture: x86-64
            version: '15.0'
            host: ubuntu-latest
"""


def run(arm, rep):
    out = D / "runs" / f"{arm}-{rep}"
    shutil.rmtree(out, ignore_errors=True)
    (out / "cfg" / "strument").mkdir(parents=True)
    wf = out / "work" / ".github" / "workflows"
    wf.mkdir(parents=True)
    (out / "cfg" / "strument" / "config.star").write_text(CONFIG)
    (wf / "ci.yml").write_text(FILE)

    started = time.time()
    p = subprocess.run(
        [str(D / f"strument-{arm}"), "chat", "-m", PROMPT, "--no-git", "--no-history",
         "--no-color", "--no-shell", "--jsonl", str(out / "session.jsonl")],
        cwd=out / "work", env={**os.environ, "XDG_CONFIG_HOME": str(out / "cfg")},
        stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=300)

    used = ambig = 0
    for line in (out / "session.jsonl").read_text().splitlines():
        try:
            r = json.loads(line)
        except Exception:
            continue
        for c in r.get("tool_calls") or []:
            if c["name"] == "edit" and '"replace_all"' in c["arguments"]:
                if json.loads(c["arguments"]).get("replace_all"):
                    used += 1
        if r.get("role") == "tool" and "ambiguous" in (r.get("text") or ""):
            ambig += 1

    final = (wf / "ci.yml").read_text()
    correct = (final.count("'14.5'") == 2 and final.count("'15.1'") == 2
               and "'14.3'" not in final and "'15.0'" not in final)
    return {"arm": arm, "rep": rep, "used_replace_all": used, "ambiguities": ambig,
            "correct": correct, "exit": p.returncode,
            "seconds": round(time.time() - started, 1)}


def main():
    (D / "runs").mkdir(exist_ok=True)
    jobs = [(a, r) for a in ("named", "unnamed") for r in range(REPS)]
    random.Random(20260914).shuffle(jobs)
    res = []
    for n, (arm, rep) in enumerate(jobs, 1):
        try:
            row = run(arm, rep)
        except subprocess.TimeoutExpired:
            row = {"arm": arm, "rep": rep, "used_replace_all": -1, "ambiguities": -1,
                   "correct": False, "exit": -1, "seconds": 300.0}
        res.append(row)
        print(f"[{n}/{len(jobs)}] {arm}-{rep}: replace_all={row['used_replace_all']} "
              f"ambiguities={row['ambiguities']} correct={row['correct']} {row['seconds']}s",
              flush=True)
        (D / "runs" / "_results.json").write_text(json.dumps(res, indent=1))


if __name__ == "__main__":
    main()
