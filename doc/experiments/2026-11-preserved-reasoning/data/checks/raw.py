"""raw.py MODEL N: does the model's own returned content carry think-tag residue?"""
import json, os, sys, urllib.request
URL = "https://api.commandcode.ai/provider/v1/chat/completions"
H = {"Authorization": "Bearer " + os.environ["COMMAND_CODE_API_KEY"], "Content-Type": "application/json", "User-Agent": "reasoning-probe/1"}
TOOLS = [{"type": "function", "function": {"name": "lookup", "description": "Look up a city's population.",
          "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}}]
user = {"role": "user", "content": "Which of Lyon, Porto and Graz has the largest population? Think it through, then call lookup on the city you think is largest to confirm."}
for i in range(int(sys.argv[2])):
    body = {"model": sys.argv[1], "messages": [user], "tools": TOOLS, "max_tokens": 3000}
    d = json.load(urllib.request.urlopen(urllib.request.Request(URL, json.dumps(body).encode(), H), timeout=300))
    m = d["choices"][0]["message"]; c = m.get("content") or ""
    print(i, "len", len(c), "think-tags:", [t for t in ("<think>", "</think>", "<|", "◁") if t in c], "| tail:", repr(c[-160:]))
