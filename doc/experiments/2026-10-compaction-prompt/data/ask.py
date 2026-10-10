"""Ask several OpenRouter models the same brief. Key from OPENROUTER_API_KEY."""
import json, os, sys, urllib.request, time
brief = open(sys.argv[1]).read(); out = sys.argv[2]
models = sys.argv[3:]
for m in models:
    body = {"model": m, "messages": [{"role": "user", "content": brief}],
            "reasoning": {"effort": "low"}, "max_tokens": 6000, "usage": {"include": True}}
    req = urllib.request.Request("https://openrouter.ai/api/v1/chat/completions", data=json.dumps(body).encode(),
        headers={"Authorization": "Bearer " + os.environ["OPENROUTER_API_KEY"], "Content-Type": "application/json"})
    t = time.time()
    try:
        r = json.load(urllib.request.urlopen(req, timeout=600))
        text = r["choices"][0]["message"].get("content") or ""
        u = r.get("usage", {})
        meta = {"model": m, "provider": r.get("provider"), "secs": round(time.time() - t, 1),
                "prompt_tokens": u.get("prompt_tokens"), "completion_tokens": u.get("completion_tokens"), "cost": u.get("cost"),
                "finish": r["choices"][0].get("finish_reason")}
    except Exception as e:
        text, meta = "", {"model": m, "error": repr(e)}
    name = m.replace("/", "_")
    open(os.path.join(out, name + ".md"), "w").write(text)
    print(json.dumps(meta)); sys.stdout.flush()
