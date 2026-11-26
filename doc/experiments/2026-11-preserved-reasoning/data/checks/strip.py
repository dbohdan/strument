"""strip.py MODEL...: does the template drop reasoning from turns before the latest user message?

A fixed history with an 85-token planted thought on turn 1's tool call. Measured ending mid-turn
(after the tool result) and after a new user message, with and without the thought."""
import json, os, sys, urllib.request
URL = "https://api.commandcode.ai/provider/v1/chat/completions"
H = {"Authorization": "Bearer " + os.environ["COMMAND_CODE_API_KEY"], "Content-Type": "application/json", "User-Agent": "reasoning-probe/1"}
TOOLS = [{"type": "function", "function": {"name": "lookup", "description": "Look up a city's population.",
          "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}}]
thought = "I am thinking carefully about this. " * 12
def hist(field, new_turn):
    a = {"role": "assistant", "content": "", "tool_calls": [{"id": "call_s1", "type": "function", "function": {"name": "lookup", "arguments": "{\"city\": \"Lyon\"}"}}]}
    if field: a[field] = thought
    h = [{"role": "user", "content": "Look up Lyon."}, a, {"role": "tool", "tool_call_id": "call_s1", "content": "Population: 522,000"}]
    if new_turn:
        h += [{"role": "assistant", "content": "Lyon has 522,000 people."}, {"role": "user", "content": "Thanks. And Porto?"}]
    return h
for m in sys.argv[1:]:
    for field in ("reasoning_content", "reasoning"):
        res = []
        for new_turn in (False, True):
            t = []
            for f in (None, field):
                body = {"model": m, "messages": hist(f, new_turn), "tools": TOOLS, "max_tokens": 10}
                try:
                    t.append(json.load(urllib.request.urlopen(urllib.request.Request(URL, json.dumps(body).encode(), H), timeout=120))["usage"]["prompt_tokens"])
                except Exception as e:
                    t.append(None)
            res.append(t[1] - t[0] if None not in t else None)
        print(f"{m:28} {field:18} added mid-turn={res[0]}  added after a new user turn={res[1]}")
