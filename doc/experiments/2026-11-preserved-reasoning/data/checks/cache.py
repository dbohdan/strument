"""cache.py MODEL ARM: does sending reasoning back change prompt caching at the next user turn?

ARM is "none" (no reasoning fields) or "send" (each assistant message keeps its reasoning).
Turn 1: two tool steps and an answer; turn 2: a new user message. Prints prompt and cached
tokens per request, with fresh call IDs so no route can re-insert stored reasoning."""
import json, os, sys, urllib.request
URL = "https://api.commandcode.ai/provider/v1/chat/completions"
H = {"Authorization": "Bearer " + os.environ["COMMAND_CODE_API_KEY"], "Content-Type": "application/json", "User-Agent": "reasoning-probe/1"}
TOOLS = [{"type": "function", "function": {"name": "lookup", "description": "Look up a city's population.",
          "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}}]
M, ARM = sys.argv[1], sys.argv[2]
SYSTEM = {"role": "system", "content": "You are a careful research assistant. " + " ".join(
    f"Reference note {i}: city records are kept in the municipal registry and updated yearly." for i in range(220))}
POP = {"lyon": 522000, "porto": 232000, "graz": 291000}
def post(msgs, mx=1500):
    body = {"model": M, "messages": msgs, "tools": TOOLS, "max_tokens": mx, "reasoning_effort": "low"}
    return json.load(urllib.request.urlopen(urllib.request.Request(URL, json.dumps(body).encode(), H), timeout=300))
def cached(u):
    d = u.get("prompt_tokens_details") or {}
    return d.get("cached_tokens") or d.get("cache_read_input_tokens") or 0
n = [0]
def keep(m):
    out = {"role": "assistant", "content": m.get("content") or ""}
    r = m.get("reasoning_content") or m.get("reasoning")
    if ARM == "send" and r:
        out["reasoning_content" if m.get("reasoning_content") else "reasoning"] = r
    if m.get("tool_calls"):
        out["tool_calls"] = []
        for c in m["tool_calls"]:
            n[0] += 1
            out["tool_calls"].append({"id": "call_x%d" % n[0], "type": "function", "function": c["function"]})
    return out
h = [SYSTEM, {"role": "user", "content": "Look up Lyon, then Porto, one call at a time, then say which is larger."}]
rows = []
def step(label):
    d = post(h); u = d["usage"]; m = d["choices"][0]["message"]
    rows.append((label, u["prompt_tokens"], cached(u), len(m.get("reasoning_content") or m.get("reasoning") or "")))
    a = keep(m); h.append(a)
    for c in a.get("tool_calls") or []:
        city = next((k for k in POP if k in c["function"]["arguments"].lower()), "lyon")
        h.append({"role": "tool", "tool_call_id": c["id"], "content": f"Population: {POP[city]}"})
    return bool(a.get("tool_calls"))
for i in range(4):
    if not step(f"t1 r{i+1}"):
        break
h.append({"role": "user", "content": "Now look up Graz too and tell me where it ranks."})
step("t2 r1")
for label, p, c, rlen in rows:
    print(f"{M.split('/')[-1]:18} {ARM:5} {label:6} prompt={p:5} cached={c:5} reasoning_chars={rlen}")
