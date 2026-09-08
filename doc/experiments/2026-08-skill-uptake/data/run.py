#!/usr/bin/env python3
"""Pilot: what does a skill do to a model's work?

Arms
  A none    no skill installed at all
  B skill   the skill installed globally; the model may load it, or not
  C inline  no skill tool; the same body appended to the task message

A->C asks whether the instructions are worth anything. B->C asks what
on-demand loading costs. A->B is the feature as shipped. C is a CEILING, not a
replica of B: a loaded skill arrives as a tool result, inlined text arrives in
the user turn.

The skill is installed GLOBALLY (XDG_DATA_HOME), so trust -- which is
orthogonal to this question -- never enters. The skill file itself lives
outside the repository and is passed in with --skill.

Order is shuffled across the whole job list with a recorded seed. In the
prompt-scope trial that was worth more than tripling the sample: shuffling
alone moved a baseline from 65% to 84%.
"""

import argparse
import concurrent.futures as cf
import json
import os
import pathlib
import random
import re
import shutil
import subprocess
import sys

HERE = pathlib.Path(__file__).parent
sys.path.insert(0, str(HERE))
import score as scorer  # noqa: E402

MODELS = {
    "v4flash": "deepseek/deepseek-v4-flash-0731",
    "luna": "openai/gpt-5.6-luna",
    "qwen": "qwen/qwen3.8-27b",
    "hy3": "tencent/hy3",
    "mimo": "xiaomi/mimo-v2.5",
    "glm": "z-ai/glm-5.3-flash",
}

CONFIG = """\
router = provider(adapter = "openrouter", base_url = env("STRUMENT_BASE_URL", "https://openrouter.ai/api/v1"), api_key = env("OPENROUTER_API_KEY"))
models = {{"m": model(router, "{slug}", context = 260000)}}
default = "m"
sandbox = ""
reasoning_display = "full"
"""

# Names no rule and no tool. A difference between arms is then a difference in
# choice, which is the shape 2026-08-symbol-uptake used.
CHART_TASK = ("Make the chart in chart.html look more professional. "
              "Keep the data exactly as it is.")
DECOY_TASK = "Fix the off-by-one in Window.Slice in window.go."
# The sharper decoy. It is HTML, it holds a ten-row table of near-identical
# rows, and it has nothing to do with charts -- so a model that loads a
# chart-styling skill here is over-triggering on the file type rather than on
# the task. A Go bug does not probe that.
LINKS_TASK = "One link in index.html points at a file that does not exist. Fix it."

TASKS = {
    "revenue": {"task": CHART_TASK, "pin": "chart.html", "kind": "chart"},
    "latency": {"task": CHART_TASK, "pin": "chart.html", "kind": "chart"},
    "storage": {"task": CHART_TASK, "pin": "chart.html", "kind": "chart"},
    "decoy": {"task": DECOY_TASK, "pin": "window.go", "kind": "gotest"},
    "links": {"task": LINKS_TASK, "pin": "index.html", "kind": "links"},
}
CHARTS = [k for k, v in TASKS.items() if v["kind"] == "chart"]
DECOYS = [k for k, v in TASKS.items() if v["kind"] != "chart"]


def skill_body(path):
    """The SKILL.md minus its frontmatter -- what arm C inlines."""
    src = pathlib.Path(path).read_text()
    if src.startswith("---"):
        end = src.index("\n---", 3)
        src = src[src.index("\n", end + 1) + 1:]
    return src.strip()


def setup_run(root, arm, fixture, args):
    """One disposable world: project copy, config home, data home."""
    root.mkdir(parents=True, exist_ok=True)
    proj = root / "proj"
    shutil.copytree(HERE / "fixtures" / fixture, proj)

    cfg = root / "cfg" / "strument"
    cfg.mkdir(parents=True)
    (cfg / "config.star").write_text(CONFIG.format(slug=MODELS[args._model]))

    data = root / "data"
    (data / "strument" / "skills").mkdir(parents=True)
    if arm == "skill":
        shutil.copytree(pathlib.Path(args.skill).parent,
                        data / "strument" / "skills" / pathlib.Path(args.skill).parent.name)
    return proj, cfg.parent, data


def run_one(job, args):
    model, arm, fixture, rep = job
    tag = f"{model}-{arm}-{fixture}-{rep}"
    out_txt = HERE / "runs" / f"{tag}.txt"
    root = HERE / "worlds" / tag

    if not out_txt.exists():
        if root.exists():
            shutil.rmtree(root)
        args._model = model
        proj, cfg_home, data_home = setup_run(root, arm, fixture, args)

        spec = TASKS[fixture]
        msg = spec["task"]
        if arm == "inline":
            msg = msg + "\n\n---\n\n" + skill_body(args.skill)

        env = dict(os.environ)
        env["XDG_CONFIG_HOME"] = str(cfg_home)
        env["XDG_DATA_HOME"] = str(data_home)
        env["XDG_STATE_HOME"] = str(root / "state")
        env["XDG_CACHE_HOME"] = str(root / "cache")

        cmd = [args.binary, "chat", spec["pin"], "--no-git", "--no-history",
               "--no-color", "--yes", "steps", "-m", msg]
        try:
            p = subprocess.run(cmd, cwd=proj, env=env, capture_output=True,
                               text=True, timeout=args.timeout)
            text = p.stdout + p.stderr
        except subprocess.TimeoutExpired as e:
            # TimeoutExpired carries RAW BYTES even when subprocess.run was
            # given text=True: the decoding happens after communicate()
            # returns, which on this path it never does. Concatenating them
            # with a str raised TypeError inside the worker thread, the
            # exception came back out of f.result(), and the collection loop
            # died -- while shutdown(wait=True) kept running all 234 jobs with
            # nobody reading their results. The work was fine; only the
            # bookkeeping stopped, which is the worst shape for this to fail in
            # because the progress counter freezes and looks like a stall.
            def dec(v):
                if v is None:
                    return ""
                return v.decode("utf-8", "replace") if isinstance(v, bytes) else v

            text = dec(e.stdout) + dec(e.stderr) + "\n[TIMEOUT]\n"
        out_txt.parent.mkdir(exist_ok=True)
        out_txt.write_text(text)
    else:
        text = out_txt.read_text()
        proj = root / "proj"

    return tag, model, arm, fixture, rep, text, proj


def measure(text, proj, fixture):
    r = {}
    # M1 delivery. The transcript prints one line per skill load.
    r["skill_calls"] = len(re.findall(r"^‹skill› ", text, re.M))
    r["read_skill_body"] = "House chart style" in text or "house palette" in text.lower()
    m = re.search(r"Cost: \$([0-9.]+) turn", text)
    r["cost"] = float(m.group(1)) if m else 0.0
    m = re.search(r"(\d+) steps?[.,]", text)
    r["steps"] = int(m.group(1)) if m else 0
    r["empty"] = "Empty response received" in text
    r["timeout"] = "[TIMEOUT]" in text
    # A turn the harness stopped is a NO ANSWER, not a wrong one
    # (doc/experimenting.md 3). Both pilot runs that produced no edit were
    # stopped by loop detection while the model quoted the fixture's repetitive
    # gridline block inside its reasoning -- a fault of the detector, not of the
    # arm, and scoring them 0/5 would deflate whichever arm they landed in.
    r["loop_stopped"] = "was repeating itself" in text

    spec = TASKS[fixture]
    target = proj / spec["pin"]
    orig = HERE / "fixtures" / fixture / spec["pin"]
    r["edited"] = target.exists() and target.read_bytes() != orig.read_bytes()
    # M4 scope creep: anything else in the tree that moved.
    changed = []
    for p in sorted(proj.rglob("*")) if proj.exists() else []:
        if not p.is_file():
            continue
        rel = p.relative_to(proj)
        src = HERE / "fixtures" / fixture / rel
        if not src.exists() or src.read_bytes() != p.read_bytes():
            changed.append(str(rel))
    r["changed_files"] = changed
    r["scope_creep"] = [c for c in changed if c != spec["pin"]]

    if spec["kind"] == "chart":
        key = json.load(open(HERE / "fixtures" / f"{fixture}.key.json"))
        if target.exists():
            s = scorer.score_file(str(target), key)
            r.update(n_rules=s["n_rules"], rules=s["rules"],
                     wellformed=s["wellformed"], data_attrs=s["data_attrs"],
                     data_ok=s["data_ok"], marks=s["marks"], detail=s["detail"])
        else:
            r.update(n_rules=0, rules={}, wellformed=False, data_attrs="missing",
                     data_ok=None, marks=0, detail={})
    elif spec["kind"] == "gotest":
        try:
            t = subprocess.run(["go", "test", "./..."], cwd=proj,
                               capture_output=True, text=True, timeout=180)
            r["decoy_fixed"] = t.returncode == 0
        except Exception as e:
            r["decoy_fixed"] = False
            r["decoy_err"] = str(e)[:120]
    else:
        # Every href must resolve to a file that exists, and the five links
        # must still all be there -- deleting the broken one also makes the
        # page consistent and is not the fix that was asked for.
        html = target.read_text() if target.exists() else ""
        hrefs = re.findall(r'href="([^"#?]+)"', html)
        r["decoy_fixed"] = (len(hrefs) == 5
                            and all((proj / h).exists() for h in hrefs))
        r["dead_links"] = [h for h in hrefs if not (proj / h).exists()]
    return r


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--binary", required=True)
    ap.add_argument("--skill", required=True, help="path to the SKILL.md")
    ap.add_argument("--reps", type=int, default=1)
    ap.add_argument("--seed", type=int, default=20260829)
    ap.add_argument("--workers", type=int, default=3)
    ap.add_argument("--timeout", type=int, default=900)
    ap.add_argument("--models", default="")
    ap.add_argument("--out", default=str(HERE / "results.json"))
    args = ap.parse_args()

    models = args.models.split(",") if args.models else list(MODELS)
    jobs = []
    for m in models:
        for rep in range(args.reps):
            for fx in CHARTS:
                for arm in ("none", "skill", "inline"):
                    jobs.append((m, arm, fx, rep))
            for fx in DECOYS:
                for arm in ("none", "skill"):   # C adds nothing on a decoy
                    jobs.append((m, arm, fx, rep))
    random.Random(args.seed).shuffle(jobs)
    print(f"{len(jobs)} runs, seed {args.seed}, {args.workers} workers", flush=True)

    results = []
    done = 0
    with cf.ThreadPoolExecutor(max_workers=args.workers) as ex:
        futs = {ex.submit(run_one, j, args): j for j in jobs}
        for f in cf.as_completed(futs):
            try:
                tag, model, arm, fixture, rep, text, proj = f.result()
            except Exception as exc:            # noqa: BLE001
                # One job must never take the run down with it. The executor
                # keeps draining either way, so an exception that escapes here
                # only costs the results -- every completed run stays on disk
                # and a re-run picks them all up.
                m, a, fx, rp = futs[f]
                done += 1
                print(f"[{done}/{len(jobs)}] {m}-{a}-{fx}-{rp} FAILED: "
                      f"{type(exc).__name__}: {exc}", flush=True)
                results.append({"tag": f"{m}-{a}-{fx}-{rp}", "model": m, "arm": a,
                                "fixture": fx, "rep": rp, "runner_error":
                                f"{type(exc).__name__}: {exc}", "cost": 0.0,
                                "skill_calls": 0, "empty": True, "timeout": True,
                                "edited": False, "scope_creep": [], "steps": 0})
                continue
            row = {"tag": tag, "model": model, "arm": arm,
                   "fixture": fixture, "rep": rep}
            row.update(measure(text, proj, fixture))
            results.append(row)
            done += 1
            mark = (f"{row.get('n_rules', '-')}/5"
                    if TASKS[fixture]["kind"] == "chart"
                    else ("fixed" if row.get("decoy_fixed") else "NOT FIXED"))
            print(f"[{done}/{len(jobs)}] {tag:<34} skill={row['skill_calls']} "
                  f"{mark} ${row['cost']:.4f}", flush=True)

    json.dump(results, open(args.out, "w"), indent=1)
    print(f"\nwrote {args.out}")


if __name__ == "__main__":
    main()
