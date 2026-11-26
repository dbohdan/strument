"""factor.py MODEL: which part of the message decides whether replayed reasoning is rendered?"""
import json, os, sys, itertools, urllib.request
URL = "https://api.commandcode.ai/provider/v1/chat/completions"
H = {"Authorization": "Bearer " + os.environ["COMMAND_CODE_API_KEY"], "Content-Type": "application/json", "User-Agent": "reasoning-probe/1"}
TOOL = {"type": "function", "function": {"name": "lookup", "description": "Look up a city's population.",
        "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}}
USERS = {"short": "Call lookup on Lyon, then say done.",
         "long": "Which of Lyon, Porto and Graz has the largest population? Think it through, then call lookup on the city you think is largest to confirm."}
CONTENTS = {"empty": "", "short": "Let me call the tool now.",
            "long": "Let me think about this: Lyon (France) has a city population of roughly 500,000 and a metro area of about 1.7 million; Porto is smaller; Graz is smaller still. I'll confirm Lyon."}
thought = "I am thinking carefully about this. " * 12
call = [{"id": "c1", "type": "function", "function": {"name": "lookup", "arguments": "{\"city\": \"Lyon\"}"}}]
for u, c in itertools.product(USERS, CONTENTS):
    got = []
    for field in (None, "reasoning"):
        a = {"role": "assistant", "content": CONTENTS[c], "tool_calls": call}
        if field: a[field] = thought
        body = {"model": sys.argv[1], "messages": [{"role": "user", "content": USERS[u]}, a, {"role": "tool", "tool_call_id": "c1", "content": "Population: 522,000"}], "tools": [TOOL], "max_tokens": 30}
        with urllib.request.urlopen(urllib.request.Request(URL, json.dumps(body).encode(), H), timeout=120) as r:
            got.append(json.load(r)["usage"]["prompt_tokens"])
    print(f"user={u:5} content={c:5} none={got[0]} reasoning={got[1]} added={got[1]-got[0]}")
