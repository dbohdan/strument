"""own.py MODEL: replay a planted thought on the model's own returned tool_calls vs a hand-built one."""
import json, os, sys, urllib.request
URL = "https://api.commandcode.ai/provider/v1/chat/completions"
H = {"Authorization": "Bearer " + os.environ["COMMAND_CODE_API_KEY"], "Content-Type": "application/json", "User-Agent": "reasoning-probe/1"}
TOOLS = [{"type": "function", "function": {"name": "lookup", "description": "Look up a city's population.",
          "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}}]
def post(body):
    with urllib.request.urlopen(urllib.request.Request(URL, json.dumps(body).encode(), H), timeout=300) as r:
        return json.load(r)
user = {"role": "user", "content": "Which of Lyon, Porto and Graz has the largest population? Think it through, then call lookup on the city you think is largest to confirm."}
d = post({"model": sys.argv[1], "messages": [user], "tools": TOOLS, "max_tokens": 3000})
m = d["choices"][0]["message"]
print("returned message keys:", sorted(m), "\ntool_calls:", json.dumps(m.get("tool_calls"))[:300], "\ncontent:", repr(m.get("content"))[:120])
own = m["tool_calls"]
hand = [{"id": own[0]["id"], "type": "function", "function": {"name": own[0]["function"]["name"], "arguments": own[0]["function"]["arguments"]}}]
thought = "I am thinking carefully about this. " * 12
for label, calls in (("own", own), ("hand-built", hand)):
    for field in (None, "reasoning"):
        a = {"role": "assistant", "content": m.get("content") or "", "tool_calls": calls}
        if field: a[field] = thought
        r = post({"model": sys.argv[1], "messages": [user, a, {"role": "tool", "tool_call_id": own[0]["id"], "content": "Population: 522,000"}], "tools": TOOLS, "max_tokens": 50})
        print(f"{label:10} field={field!s:10} prompt_tokens={r['usage']['prompt_tokens']}")
