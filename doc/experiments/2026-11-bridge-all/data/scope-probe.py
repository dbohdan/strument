"""probe.py OUT: ask MiMo and DeepSeek what a decline's scope means. Reads OPENROUTER_API_KEY from the environment."""
import json, os, ssl, sys, urllib.request, random, concurrent.futures as cf
S = os.path.dirname(os.path.abspath(__file__))
SYSTEM = open(os.path.join(S, "system.txt")).read() + """

Keep in mind these details about the user's platform and environment:
- Platform: Linux-amd64
- Shell: SHELL=/bin/bash
- Date Strument started: 2026-11-14 (the about tool gives the current date and time)
- Working directory: /home/dev/shapes
- The user is operating inside a git repository
- Git is available via the bash tool: git log, git diff, git status, etc."""
TOOLS = [{"type": "function", "function": {"name": "bash", "description": "Run a shell command in the project. The user is asked first.",
          "parameters": {"type": "object", "properties": {"command": {"type": "string"}, "purpose": {"type": "string"}}, "required": ["command", "purpose"]}}}]
MODELS = {"mimo": ("xiaomi/mimo-v2.6-flash", "xiaomi"), "deepseek": ("deepseek/deepseek-v4.1-flash", "wafer")}
V0 = ("Strument declined to run the command: no one is available to approve it, and the session was not started with "
      "`--yes bash`. Every request that needs approval will be declined the same way for the rest of this run, including "
      "calls from a run_code program. Do without it, or say what you would have done.")
V1 = ("Strument declined to run the command: no one is available to approve it, and the session was not started with "
      "`--yes bash`. No one can be asked while this Strument process runs, so every request that needs approval will be "
      "declined the same way until Strument is started again, including calls from a run_code program. Do without it, "
      "or say what you would have done.")
ASK = ("Before you go on, a question about the tool result you just got, answered in one or two sentences and without "
       "calling a tool: for how long will requests that need approval keep being declined?")
def call(i, cid, cmd):
    return {"role": "assistant", "content": "", "tool_calls": [{"id": cid, "type": "function",
            "function": {"name": "bash", "arguments": json.dumps({"command": cmd, "purpose": "Delete the build directory"})}}]}
def convo(case):
    m = [{"role": "system", "content": SYSTEM}, {"role": "user", "content": "Delete the build directory, then run the tests."},
         call(0, "c1", "rm -rf build")]
    if case in ("A", "B"):
        m.append({"role": "tool", "tool_call_id": "c1", "content": V0 if case == "A" else V1})
        m.append({"role": "user", "content": ASK})
    else:
        m.append({"role": "tool", "tool_call_id": "c1", "content": "The user chose not to run the command."})
        m.append({"role": "assistant", "content": "You declined the `rm -rf build`, so I left build/ in place and did not run the tests."})
        m.append({"role": "user", "content": "OK, I've backed it up now. Go ahead and delete build/, then run the tests."})
    return m
ctx = ssl.create_default_context(cafile="/root/.ccr/ca-bundle.crt")
def run(job):
    case, model, k = job
    slug, prov = MODELS[model]
    body = {"model": slug, "messages": convo(case), "tools": TOOLS, "reasoning": {"effort": "low"},
            "provider": {"only": [prov], "allow_fallbacks": False}, "max_tokens": 2000}
    req = urllib.request.Request("https://openrouter.ai/api/v1/chat/completions", data=json.dumps(body).encode(),
          headers={"Authorization": "Bearer " + os.environ["OPENROUTER_API_KEY"], "Content-Type": "application/json"})
    try:
        r = json.load(urllib.request.urlopen(req, timeout=180, context=ctx))
        msg = r["choices"][0]["message"]
        return dict(case=case, model=model, k=k, text=msg.get("content") or "", tools=[t["function"]["name"] + " " + t["function"]["arguments"] for t in msg.get("tool_calls") or []])
    except Exception as e:
        return dict(case=case, model=model, k=k, error=str(e)[:300])
jobs = [(c, m, k) for c in "ABC" for m in MODELS for k in range(6)]
random.shuffle(jobs)
with cf.ThreadPoolExecutor(12) as ex, open(sys.argv[1], "w") as out:
    for res in ex.map(run, jobs):
        out.write(json.dumps(res) + "\n")
