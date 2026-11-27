"""strip_or.py MODEL [PROVIDER...]: strip.py's delivery check over OpenRouter, one upstream provider at a time.

The same fixed history with an 85-token planted thought on turn 1's tool call, measured mid-turn and
after a new user message, with and without the thought. Each provider is pinned with
allow_fallbacks=false, because OpenRouter otherwise picks one per request and a mixed pair means
nothing."""
import json, os, sys, urllib.request
URL = "https://openrouter.ai/api/v1/chat/completions"
H = {"Authorization": "Bearer " + os.environ["OPENROUTER_API_KEY"], "Content-Type": "application/json"}
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
def prompt(model, provider, msgs):
    body = {"model": model, "messages": msgs, "tools": TOOLS, "max_tokens": 16, "reasoning": {"effort": "low"}}
    if provider:
        body["provider"] = {"order": [provider], "allow_fallbacks": False}
    d = json.load(urllib.request.urlopen(urllib.request.Request(URL, json.dumps(body).encode(), H), timeout=120))
    return d["usage"]["prompt_tokens"], d.get("provider")
model, providers = sys.argv[1], sys.argv[2:] or [None]
for p in providers:
    for field in ("reasoning_content", "reasoning"):
        res = []
        for new_turn in (False, True):
            try:
                (w, served), (wo, _) = prompt(model, p, hist(field, new_turn)), prompt(model, p, hist(None, new_turn))
                res.append(f"{w - wo:+d}")
            except Exception as e:
                res.append(f"error {str(e)[:60]}"); served = "?"
        print(f"{model:28} {str(p or 'auto'):14} served={served:14} {field:18} mid-turn={res[0]}  after a new user turn={res[1]}")
