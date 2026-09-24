"""Runs the probe. Resumable: a job whose transcript exists with a final
status is skipped. The key comes from OPENROUTER_API_KEY, never an argument."""
import http.client
import concurrent.futures as cf, copy, json, os, random, subprocess, sys, time, urllib.request, urllib.error

import arms as armsmod

HERE = os.path.dirname(os.path.abspath(__file__))
TREE = os.path.join(HERE, "tree")
RUNS = os.path.join(HERE, "runs")
# A Strument binary built from the commit under test; the probe used 5fc31e9.
STRUMENT = os.environ.get("STRUMENT", "strument")
KEY = os.environ["OPENROUTER_API_KEY"]

MODELS = {
    "mimo": "xiaomi/mimo-v2.6-flash",
    "glm": "z-ai/glm-5.3-flash",
    "deepseek": "deepseek/deepseek-v4.1-flash",
}
TASKS = {
    "walk": "How many Markdown files under docs/ mention the phrase \"retry budget\"?",
    "total": "What is the sum of all the numbers in the .txt files under data/?",
    "top5": "Which five entries in data/sizes.csv have the largest byte counts? List them largest first.",
    "span": "How many days are there between the earliest and the latest date in logs/app.log?",
    "longest": "How long is the longest line in notes.md, in characters?",
    "cite": "On which line of notes.md does the XYZZY marker appear?",
}
REPS = 8
MAX_STEPS = 8
LOOKUPS = {"read", "grep", "glob", "ls", "symbol"}


def jobs():
    out = [(m, a, t, r) for m in MODELS for a in ("monty", "js") for t in TASKS for r in range(REPS)]
    random.Random(20260924).shuffle(out)
    return out


def call_model(body):
    data = json.dumps(body).encode()
    last = None
    for attempt in range(3):
        req = urllib.request.Request("https://openrouter.ai/api/v1/chat/completions", data=data,
                                     headers={"Authorization": "Bearer " + KEY, "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=240) as resp:
                out = json.loads(resp.read())
            if "choices" in out:
                return out
            last = out
        except (urllib.error.URLError, TimeoutError, json.JSONDecodeError, http.client.HTTPException, OSError, ValueError) as e:
            last = {"error": repr(e)}
        time.sleep(4 * (attempt + 1))
    return {"provider_failure": last}


def run_lookup(name, args):
    """Answers a lookup with what `strument tool` prints: the bytes a model
    would receive. Required arguments go positionally, the rest as flags."""
    positional = {"read": "path", "grep": "pattern", "glob": "pattern", "ls": "path", "symbol": "name"}[name]
    argv = [STRUMENT, "tool", "--root", TREE, name]
    if args.get(positional) not in (None, ""):
        argv.append(str(args[positional]))
    for k, v in args.items():
        if k == positional or v in (None, "", False, 0):
            continue
        flag = "--" + k.replace("_", "-")
        argv.append(flag if v is True else f"{flag}={v}")
    p = subprocess.run(argv, capture_output=True, text=True, timeout=60)
    return p.stdout if p.returncode == 0 else (p.stdout + p.stderr).strip()


def one(job, arms):
    model, arm, task, rep = job
    tag = f"{model}-{arm}-{task}-{rep}"
    path = os.path.join(RUNS, tag + ".json")
    if os.path.exists(path):
        return tag, "skipped"
    base = copy.deepcopy(arms[arm])
    messages = [base["messages"][0], {"role": "user", "content": TASKS[task]}]
    rec = {"model": model, "arm": arm, "task": task, "rep": rep, "steps": [], "status": None,
           "first_program": None, "other_calls": [], "cost": 0.0}
    for step in range(MAX_STEPS):
        body = {"model": MODELS[model], "messages": messages, "tools": base["tools"],
                "tool_choice": base.get("tool_choice", "auto"), "stream": False,
                "reasoning": {"effort": "low"}, "max_tokens": 6000, "usage": {"include": True}}
        out = call_model(body)
        if "provider_failure" in out:
            rec["status"] = "provider_failure"
            rec["error"] = out["provider_failure"]
            break
        rec["cost"] += (out.get("usage") or {}).get("cost") or 0.0
        msg = out["choices"][0]["message"]
        rec["steps"].append(msg)
        calls = msg.get("tool_calls") or []
        if not calls:
            rec["status"] = "answered_without_program"
            rec["answer"] = msg.get("content")
            break
        messages.append({"role": "assistant", "content": msg.get("content") or "", "tool_calls": calls})
        program = None
        for tc in calls:
            name = tc["function"]["name"]
            try:
                args = json.loads(tc["function"]["arguments"] or "{}")
            except json.JSONDecodeError:
                args = None
            if name == "run_code" and program is None:
                program = (args or {}).get("code") if isinstance(args, dict) else None
                result = "(probe stops here)"
            elif name in LOOKUPS and isinstance(args, dict):
                result = run_lookup(name, args)
            else:
                rec["other_calls"].append(name)
                result = "Not available in this session."
            messages.append({"role": "tool", "tool_call_id": tc["id"], "content": result})
        if program is not None:
            rec["status"] = "program"
            rec["first_program"] = program
            rec["program_step"] = step + 1
            break
    else:
        rec["status"] = "step_limit"
    with open(path, "w") as f:
        json.dump(rec, f, indent=1)
    return tag, rec["status"]


def main():
    os.makedirs(RUNS, exist_ok=True)
    arms = armsmod.load()
    todo = jobs()
    if len(sys.argv) > 1 and sys.argv[1] == "pilot":
        seen, pilot = set(), []
        for j in todo:
            if (j[0], j[1]) not in seen and j[3] == 0:
                seen.add((j[0], j[1])); pilot.append(j)
        todo = pilot
    print(f"{len(todo)} jobs", flush=True)
    with cf.ThreadPoolExecutor(max_workers=4) as ex:
        for tag, status in ex.map(lambda j: one(j, arms), todo):
            print(tag, status, flush=True)


if __name__ == "__main__":
    main()
