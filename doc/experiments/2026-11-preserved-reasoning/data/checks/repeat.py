"""repeat.py MODEL N: the same replay request N times, interleaved with its no-reasoning twin; does delivery vary?"""
import json, os, sys, time, urllib.request
URL = "https://api.commandcode.ai/provider/v1/chat/completions"
H = {"Authorization": "Bearer " + os.environ["COMMAND_CODE_API_KEY"], "Content-Type": "application/json", "User-Agent": "reasoning-probe/1"}
TOOL = {"type": "function", "function": {"name": "lookup", "description": "Look up a city's population.",
        "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}}
thought = "I am thinking carefully about this. " * 12
call = [{"id": "c1", "type": "function", "function": {"name": "lookup", "arguments": "{\"city\": \"Lyon\"}"}}]
def req(field):
    a = {"role": "assistant", "content": "Let me check.", "tool_calls": call}
    if field: a[field] = thought
    body = {"model": sys.argv[1], "messages": [{"role": "user", "content": "Call lookup on Lyon, then say done."}, a,
            {"role": "tool", "tool_call_id": "c1", "content": "Population: 522,000"}], "tools": [TOOL], "max_tokens": 20}
    r = urllib.request.urlopen(urllib.request.Request(URL, json.dumps(body).encode(), H), timeout=120)
    d = json.load(r)
    return d["usage"]["prompt_tokens"], {k: v for k, v in d.items() if k in ("provider", "model", "system_fingerprint", "id")}, dict(r.headers)
for i in range(int(sys.argv[2])):
    n, meta_n, _ = req(None)
    y, meta_y, hdr = req("reasoning")
    extra = {k: v for k, v in hdr.items() if k.lower().startswith(("x-", "cf-ray", "server"))}
    print(f"{i}: none={n} reasoning={y} added={y-n} meta={meta_y} hdr={json.dumps(extra)[:200]}", flush=True)
