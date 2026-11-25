"""probe.py MODEL...: does a code word planted in a replayed assistant's reasoning reach the model?

Arms: none (no reasoning field), reasoning_content, reasoning. Placements: within (before a
tool call, same turn) and across (an earlier turn). The word is random per call, so only the
replayed field can supply it. Key from COMMAND_CODE_API_KEY. Prints one JSON line per call."""
import json, os, random, string, sys, urllib.request
URL = "https://api.commandcode.ai/provider/v1/chat/completions"
KEY = os.environ["COMMAND_CODE_API_KEY"]
TOOLS = [{"type": "function", "function": {"name": "noop", "description": "Does nothing and returns ok.",
          "parameters": {"type": "object", "properties": {}}}}]

def word():
    return random.choice(["PERIWINKLE", "TAMARIND", "OBSIDIAN", "QUILLWORT", "MARZIPAN", "HALCYON"]) + "-" + "".join(random.choices(string.digits, k=4))

def messages(placement, field, secret):
    thought = f"I'll pick my code word now. My code word is {secret}. I won't reveal it until asked."
    asst = {"role": "assistant"}
    if field not in ("none", "inline"):
        asst[field] = thought
    inline = f"<think>{thought}</think>" if field == "inline" else ""
    if placement == "within":
        asst.update(content=inline, tool_calls=[{"id": "call_1", "type": "function", "function": {"name": "noop", "arguments": "{}"}}])
        return [{"role": "user", "content": "First, privately choose a code word in your reasoning (a word, a hyphen, four digits). Then call the noop tool. After it returns, tell me the code word. If you cannot see one, answer UNKNOWN."},
                asst, {"role": "tool", "tool_call_id": "call_1", "content": "ok"}]
    asst["content"] = inline + "Ready."
    return [{"role": "user", "content": "Privately choose a code word in your reasoning (a word, a hyphen, four digits). Don't reveal it; reply only 'Ready.'"},
            asst, {"role": "user", "content": "You may reveal it now. What was your code word? If you cannot see it, answer UNKNOWN."}]

def call(model, msgs):
    body = {"model": model, "messages": msgs, "tools": TOOLS, "max_tokens": 4000, "reasoning_effort": "low"}
    req = urllib.request.Request(URL, json.dumps(body).encode(), {"Authorization": "Bearer " + KEY, "Content-Type": "application/json", "User-Agent": "reasoning-probe/1"})
    try:
        with urllib.request.urlopen(req, timeout=180) as r:
            d = json.load(r)
    except urllib.error.HTTPError as e:
        return {"error": f"HTTP {e.code}: {e.read()[:200]!r}"}
    except Exception as e:
        return {"error": repr(e)[:200]}
    m = d["choices"][0]["message"]
    return {"content": m.get("content") or "", "reasoning": (m.get("reasoning_content") or m.get("reasoning") or "")[:300],
            "fields": sorted(k for k in m if m.get(k)), "usage": d.get("usage", {}).get("prompt_tokens")}

reps = int(os.environ.get("REPS", "2"))
jobs = [(m, p, f, r) for m in sys.argv[1:] for p in ("within", "across") for f in os.environ.get("FIELDS", "none,reasoning_content,reasoning").split(",") for r in range(reps)]
random.seed(int(os.environ.get("SEED", "7")))
random.shuffle(jobs)
for model, placement, field, rep in jobs:
    secret = word()
    res = call(model, messages(placement, field, secret))
    res.update(model=model, placement=placement, field=field, secret=secret,
               hit=secret in res.get("content", ""), unknown="UNKNOWN" in res.get("content", "").upper())
    print(json.dumps(res), flush=True)
