"""effort.py MODEL: does reasoning_effort on the replay request change whether replayed reasoning counts?"""
import json, os, sys, urllib.request
URL = "https://api.commandcode.ai/provider/v1/chat/completions"
H = {"Authorization": "Bearer " + os.environ["COMMAND_CODE_API_KEY"], "Content-Type": "application/json", "User-Agent": "reasoning-probe/1"}
TOOLS = [{"type": "function", "function": {"name": "noop", "description": "Does nothing.", "parameters": {"type": "object", "properties": {}}}}]
thought = "I am thinking carefully about this. " * 12
user = {"role": "user", "content": "Call noop, then say done."}
call = [{"id": "c1", "type": "function", "function": {"name": "noop", "arguments": "{}"}}]
for effort in (None, "low", "medium", "high"):
    for field in (None, "reasoning"):
        a = {"role": "assistant", "content": "", "tool_calls": call}
        if field: a[field] = thought
        body = {"model": sys.argv[1], "messages": [user, a, {"role": "tool", "tool_call_id": "c1", "content": "ok"}], "tools": TOOLS, "max_tokens": 50}
        if effort: body["reasoning_effort"] = effort
        with urllib.request.urlopen(urllib.request.Request(URL, json.dumps(body).encode(), H), timeout=120) as r:
            print(f"effort={effort!s:7} field={field!s:10} prompt_tokens={json.load(r)['usage']['prompt_tokens']}")
