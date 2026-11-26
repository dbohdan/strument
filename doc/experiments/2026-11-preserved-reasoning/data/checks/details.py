"""details.py MODEL REPS: is the reasoning string a lossy copy of reasoning_details?

Request 1 lets the model reason and call a tool. Request 2 replays that assistant message
four ways (none, reasoning string, reasoning_details, both) plus the tool result, and records
prompt_tokens. Key from COMMAND_CODE_API_KEY."""
import json, os, sys, urllib.request
URL = "https://api.commandcode.ai/provider/v1/chat/completions"
H = {"Authorization": "Bearer " + os.environ["COMMAND_CODE_API_KEY"], "Content-Type": "application/json", "User-Agent": "reasoning-probe/1"}
TOOLS = [{"type": "function", "function": {"name": "lookup", "description": "Look up a city's population.",
          "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}}]
def post(body):
    req = urllib.request.Request(URL, json.dumps(body).encode(), H)
    with urllib.request.urlopen(req, timeout=300) as r:
        return json.load(r)
model, reps = sys.argv[1], int(sys.argv[2])
user = {"role": "user", "content": "Which of Lyon, Porto and Graz has the largest population? Think it through, then call lookup on the city you think is largest to confirm."}
for rep in range(reps):
    d = post({"model": model, "messages": [user], "tools": TOOLS, "max_tokens": 3000, "reasoning_effort": "low"})
    m = d["choices"][0]["message"]
    rtok = d["usage"].get("completion_tokens_details", {}).get("reasoning_tokens")
    text = m.get("reasoning") or m.get("reasoning_content") or ""
    det = m.get("reasoning_details")
    calls = m.get("tool_calls")
    if not calls:
        print(json.dumps({"rep": rep, "skip": "no tool call"})); continue
    detail_text = sum(len(x.get("text", "") or x.get("summary", "") or "") for x in (det or []))
    row = {"rep": rep, "reasoning_tokens": rtok, "string_chars": len(text), "details_types": [x.get("type") for x in (det or [])],
           "details_text_chars": detail_text, "details_same_as_string": bool(det) and "".join((x.get("text") or "") for x in det) == text}
    for arm in ("none", "string", "details", "both"):
        a = {"role": "assistant", "content": m.get("content") or "", "tool_calls": calls}
        if arm in ("string", "both"): a["reasoning"] = text
        if arm in ("details", "both"): a["reasoning_details"] = det
        tool = {"role": "tool", "tool_call_id": calls[0]["id"], "content": "Population: 522,000"}
        r = post({"model": model, "messages": [user, a, tool], "tools": TOOLS, "max_tokens": 200, "reasoning_effort": "low"})
        row["prompt_" + arm] = r["usage"]["prompt_tokens"]
    print(json.dumps(row), flush=True)
