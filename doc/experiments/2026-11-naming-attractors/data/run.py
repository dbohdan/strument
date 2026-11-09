#!/usr/bin/env python3
"""Submit, poll and collect the naming trial's batches on OpenRouter.

    run.py submit LABEL [--pilot] [--arm off|deliberate|both] [--model SLUG]
                                    one batch per model and arm; ids saved to
                                    OUT/LABEL/batches.json
    run.py collect LABEL            poll every minute until all are final, then write
                                    OUT/LABEL/<model>-<arm>.jsonl, one row per request

OUT is --out (default: ./runs). The key is read from OPENROUTER_API_KEY and
never written. A model and arm already in batches.json is not resubmitted, so
`submit` is safe to rerun; `collect` skips batches whose results are saved.

Each arm of each model is one batch. OpenRouter reserves a batch's cost as if
every request used its whole max_tokens, and refuses a batch whose estimate
exceeds the balance (402), so the arms go in separately: the three off batches
together, then the deliberate ones as the balance allows. Within a batch the
requests are shuffled (seed 20261109) so no prompt is systematically first.
"""

import argparse
import json
import os
import random
import sys
import time
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import prompts as P  # noqa: E402

API = "https://openrouter.ai/api/v1/batches"
MODELS = {
    # The lowest reasoning setting each model accepts. Opus 5.5 refuses "none"
    # ("Reasoning is mandatory for this endpoint"), so it gets "minimal"; the
    # reasoning tokens of every call are recorded, so what ran is measured.
    "anthropic/claude-opus-5": {"effort": "none"},
    "anthropic/claude-opus-5.5": {"effort": "minimal"},
    "openai/gpt-6-sol": {"effort": "none"},
}
REPS = 20
# Caps, from the pilot's largest real use (off 41 tokens, an inline thought;
# deliberate 644, Opus 5). A reply that reaches its cap ends with
# finish_reason "length" and is counted as truncated, not silently dropped.
MAX_TOKENS = {"off": 80, "deliberate": 1500}
ARMS = ("off", "deliberate")
FINAL = {"completed", "failed", "expired", "cancelled"}


def requests_for(model, pilot, arm=None):
    reps = 2 if pilot else REPS
    items = P.PROMPTS
    delib = P.DELIBERATION_IDS
    if pilot:
        firsts = {}
        for p in P.PROMPTS:
            firsts.setdefault(p[1], p)
        items = list(firsts.values())
        delib = delib[:2]
    out = []
    for pid, kind, thing, setting in items:
        for cond, text in (("bare", P.bare(thing)), ("context", P.context(thing, setting))):
            for r in range(reps):
                out.append({"custom_id": f"off_{pid}_{cond}_{r:02d}",
                            "body": {"messages": [{"role": "user", "content": text}],
                                     "reasoning": MODELS[model], "max_tokens": MAX_TOKENS["off"]}})
    things = {p[0]: p[2] for p in P.PROMPTS}
    for pid in delib:
        for r in range(reps):
            out.append({"custom_id": f"deliberate_{pid}_bare_{r:02d}",
                        "body": {"messages": [{"role": "user", "content": P.deliberate(things[pid])}],
                                 "reasoning": {"effort": "high"}, "max_tokens": MAX_TOKENS["deliberate"]}})
    if arm:
        out = [o for o in out if o["custom_id"].startswith(arm + "_")]
    random.Random(20261109).shuffle(out)
    return out


def call(method, url, payload=None):
    key = os.environ.get("OPENROUTER_API_KEY")
    if not key:
        sys.exit("OPENROUTER_API_KEY is not set")
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(url, data=data, method=method,
                                 headers={"Authorization": "Bearer " + key,
                                          "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=300) as r:
            return r.status, json.loads(r.read())
    except urllib.error.HTTPError as e:
        body = e.read()
        try:
            return e.code, json.loads(body)
        except ValueError:
            return e.code, {"raw": body[:500].decode(errors="replace")}


def slug(model):
    return model.split("/")[-1]


def submit(a):
    d = os.path.join(a.out, a.label)
    path = os.path.join(d, "batches.json")
    os.makedirs(d, exist_ok=True)
    ids = json.load(open(path)) if os.path.exists(path) else {}
    arms = ARMS if a.arm == "both" else (a.arm,)
    models = [m for m in MODELS if not a.model or m == a.model]
    for arm in arms:
        for model in models:
            key = f"{model}|{arm}"
            if key in ids:
                print(f"{key}: already submitted as {ids[key]['id']}")
                continue
            reqs = requests_for(model, a.pilot, arm)
            # endpoint and model before requests: OpenRouter reads the body as a stream.
            payload = {"endpoint": "/v1/chat/completions", "model": model, "requests": reqs}
            status, out = call("POST", API, payload)
            if status != 202:
                print(f"{key}: HTTP {status}: {json.dumps(out)[:400]}")
                sys.exit(1)
            ids[key] = {"id": out["id"], "model": model, "arm": arm, "n": len(reqs),
                        "provider": out.get("provider"), "submitted": time.time()}
            print(f"{key}: {out['id']} ({len(reqs)} requests) on {out.get('provider')}")
            with open(path, "w") as f:  # after each, so a failure midway keeps what was submitted
                json.dump(ids, f, indent=1)


def row(model, res):
    cid = res["custom_id"]
    arm, pid, cond, rep = cid.split("_")  # Anthropic allows only [A-Za-z0-9_-]
    r = {"model": model, "arm": arm, "prompt": pid, "condition": cond, "rep": int(rep),
         "content": None, "finish_reason": None, "reasoning_tokens": None,
         "completion_tokens": None, "prompt_tokens": None, "error": None}
    if res.get("error"):
        r["error"] = json.dumps(res["error"])[:300]
        return r
    body = (res.get("response") or {}).get("body") or {}
    try:
        ch = body["choices"][0]
        r["content"] = ch["message"].get("content")
        r["finish_reason"] = ch.get("finish_reason")
    except (KeyError, IndexError, TypeError):
        r["error"] = "no choices: " + json.dumps(body)[:200]
    u = body.get("usage") or {}
    r["completion_tokens"] = u.get("completion_tokens")
    r["prompt_tokens"] = u.get("prompt_tokens")
    r["reasoning_tokens"] = (u.get("completion_tokens_details") or {}).get("reasoning_tokens")
    return r


def collect(a):
    d = os.path.join(a.out, a.label)
    ids = json.load(open(os.path.join(d, "batches.json")))
    out_name = lambda key: f"{slug(ids[key].get('model', key))}-{ids[key].get('arm', 'all')}.jsonl"  # noqa: E731
    pending = {k: v for k, v in ids.items() if not os.path.exists(os.path.join(d, out_name(k)))}
    summary_path = os.path.join(d, "summary.json")
    summary = json.load(open(summary_path)) if os.path.exists(summary_path) else {}
    while pending:
        for key, v in list(pending.items()):
            model = v.get("model", key)
            status, b = call("GET", f"{API}/{v['id']}")
            # A batch can 404 for a short while after its 202: seen in the
            # pilot, a second after submit. Ten minutes is generous.
            young = time.time() - v["submitted"] < 600
            if status == 429 or status >= 500 or (status == 404 and young):
                print(f"{slug(model)}: HTTP {status}, waiting", flush=True)
                continue
            if status != 200:
                sys.exit(f"{model}: HTTP {status}: {json.dumps(b)[:300]}")
            st = b.get("status")
            ready = st in FINAL and (st != "completed" or b.get("results") is not None)
            print(f"{time.strftime('%H:%M:%S')} {key}: {st} {b.get('request_counts')}", flush=True)
            if not ready:
                continue
            rows = [row(model, res) for res in (b.get("results") or [])]
            with open(os.path.join(d, out_name(key)), "w") as f:
                for r in sorted(rows, key=lambda r: (r["arm"], r["prompt"], r["condition"], r["rep"])):
                    f.write(json.dumps(r, ensure_ascii=False) + "\n")
            summary[key] = {"id": v["id"], "status": st, "provider": b.get("provider"),
                              "request_counts": b.get("request_counts"), "usage": b.get("usage"),
                              "error": b.get("error"), "rows": len(rows), "expected": v["n"]}
            with open(summary_path, "w") as f:
                json.dump(summary, f, indent=1)
            print(f"  saved {len(rows)}/{v['n']} rows; cost {(b.get('usage') or {}).get('cost')}", flush=True)
            del pending[key]
        if pending:
            time.sleep(60)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("cmd", choices=["submit", "collect"])
    ap.add_argument("label")
    ap.add_argument("--pilot", action="store_true")
    ap.add_argument("--arm", choices=["off", "deliberate", "both"], default="both")
    ap.add_argument("--model", default="", help="submit for this model only")
    ap.add_argument("--out", default="runs")
    a = ap.parse_args()
    submit(a) if a.cmd == "submit" else collect(a)


if __name__ == "__main__":
    main()
