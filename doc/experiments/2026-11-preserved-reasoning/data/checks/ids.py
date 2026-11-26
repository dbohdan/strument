"""ids.py MODEL N: does the server substitute its own stored message for a replayed call ID?

Generate once, then replay the same assistant message with (a) its own call ID and (b) a fresh one,
each with no reasoning field and with a planted thought. Prompt tokens are compared."""
import json, os, sys, urllib.request
URL = "https://api.commandcode.ai/provider/v1/chat/completions"
H = {"Authorization": "Bearer " + os.environ["COMMAND_CODE_API_KEY"], "Content-Type": "application/json", "User-Agent": "reasoning-probe/1"}
TOOLS = [{"type": "function", "function": {"name": "lookup", "description": "Look up a city's population.",
          "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}}]
def post(body):
    return json.load(urllib.request.urlopen(urllib.request.Request(URL, json.dumps(body).encode(), H), timeout=300))
user = {"role": "user", "content": "Which of Lyon, Porto and Graz has the largest population? Think it through, then call lookup on the city you think is largest to confirm."}
thought = "I am thinking carefully about this. " * 12
for i in range(int(sys.argv[2])):
    d = post({"model": sys.argv[1], "messages": [user], "tools": TOOLS, "max_tokens": 3000})
    m = d["choices"][0]["message"]; own = m["tool_calls"][0]
    rtok = d["usage"].get("completion_tokens_details", {}).get("reasoning_tokens")
    out = {"rep": i, "reasoning_tokens": rtok}
    for idlabel, cid in (("own", own["id"]), ("fresh", "call_fresh%d" % i)):
        for field in (None, "reasoning"):
            a = {"role": "assistant", "content": m.get("content") or "",
                 "tool_calls": [{"id": cid, "type": "function", "function": own["function"]}]}
            if field: a[field] = thought
            r = post({"model": sys.argv[1], "messages": [user, a, {"role": "tool", "tool_call_id": cid, "content": "Population: 522,000"}], "tools": TOOLS, "max_tokens": 20})
            out[f"{idlabel}_{field or 'none'}"] = r["usage"]["prompt_tokens"]
    print(json.dumps(out), flush=True)
