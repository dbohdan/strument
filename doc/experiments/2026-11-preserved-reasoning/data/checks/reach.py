"""reach.py MODEL: how far back does server-side preservation reach?

Generates two tool steps and a final answer, then a second user turn. Each history is sent with the
model's own call IDs and with fresh ones, no reasoning fields; the difference is what the server re-inserts."""
import json, os, sys, urllib.request
URL = "https://api.commandcode.ai/provider/v1/chat/completions"
H = {"Authorization": "Bearer " + os.environ["COMMAND_CODE_API_KEY"], "Content-Type": "application/json", "User-Agent": "reasoning-probe/1"}
TOOLS = [{"type": "function", "function": {"name": "lookup", "description": "Look up a city's population.",
          "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}}]
M = sys.argv[1]
def post(msgs, mx=2000):
    return json.load(urllib.request.urlopen(urllib.request.Request(URL, json.dumps({"model": M, "messages": msgs, "tools": TOOLS, "max_tokens": mx}).encode(), H), timeout=300))
def strip(m):
    return {k: v for k, v in m.items() if k in ("role", "content", "tool_calls")}
def fresh(msgs):
    out, n = [], 0
    for m in msgs:
        m = json.loads(json.dumps(m))
        for c in m.get("tool_calls") or []:
            c["id"] = "call_f%d" % n; n += 1
        if m["role"] == "tool":
            m["tool_call_id"] = "call_f%d" % (n - 1)
        out.append(m)
    return out
u1 = {"role": "user", "content": "Look up the populations of Lyon and then Porto, one call at a time, then tell me which is larger."}
h = [u1]; rtoks = []
for step in range(3):
    d = post(h); m = d["choices"][0]["message"]; rtoks.append(d["usage"].get("completion_tokens_details", {}).get("reasoning_tokens"))
    h.append(strip(m))
    if not m.get("tool_calls"):
        break
    for c in m["tool_calls"]:
        h.append({"role": "tool", "tool_call_id": c["id"], "content": "Population: %d" % (522000 if "yon" in c["function"]["arguments"] else 232000)})
print("reasoning tokens per step:", rtoks, "| final has text:", bool(h[-1].get("content")))
def count(msgs):
    return post(msgs, 10)["usage"]["prompt_tokens"]
calls = [i for i, m in enumerate(h) if m.get("tool_calls")]
for label, cut in (("after step 1's result", calls[0] + 2), ("after step 2's result", calls[-1] + 2 if len(calls) > 1 else None)):
    if cut:
        print(f"{label:28} own={count(h[:cut])} fresh={count(fresh(h[:cut]))}")
u2 = {"role": "user", "content": "And Graz?"}
print(f"{'next user turn':28} own={count(h + [u2])} fresh={count(fresh(h) + [u2])}")
