"""Live pass: does an image survive each wire dialect, on both routes?

Three dialects on one OpenRouter key with the model held constant, so the
dialect is the only variable. The metric is a count -- did the model report the
exact five digits in the probe -- rather than a judgment, so the author is not
also the judge.

Order is randomized. Running every arm of one dialect and then the next would
confound the arm with the time it ran, which cost a previous experiment here a
p=0.0009 that turned into p=0.15 under shuffling alone.
"""
import json, os, pathlib, random, re, shutil, subprocess, sys, time

D = pathlib.Path(__file__).resolve().parent.parent
BIN = str(D / "strument")
PROBE = D / "probe.png"
DIGITS = PROBE.with_suffix(".txt").read_text().strip()
# Deliberately different from DIGITS. notes.txt exists only for the counter-
# metric route; if these digits ever turn up in an image run, the scorer is
# reading a file rather than the model's vision.
TEXT_DIGITS = "41258"

DIALECTS = {
    "chat": 'provider("openrouter", api_key = env("OPENROUTER_API_KEY"))',
    "anthropic": 'provider("anthropic", base_url = "https://openrouter.ai/api/v1",'
                 ' api_key = env("OPENROUTER_API_KEY"))',
    "responses": 'provider("responses", base_url = "https://openrouter.ai/api/v1",'
                 ' api_key = env("OPENROUTER_API_KEY"))',
}

# The standard panel, with image support as reported by OpenRouter /models on
# 2026-09-14. deepseek and hy3 are text-only and are the projection subjects.
PANEL = {
    "mimo": ("xiaomi/mimo-v2.5", True),
    "glm": ("z-ai/glm-5.3-flash", True),
    "luna": ("openai/gpt-5.6-luna", True),
    "qwen3.8-27b": ("qwen/qwen3.8-27b", True),
    "deepseek": ("deepseek/deepseek-v4-flash-0731", False),
    "hy3": ("tencent/hy3", False),
}

ASK = ("Read probe.png and tell me the five-digit number shown in the image. "
       "Reply with just the number.")
ASK_ATTACHED = ("Tell me the five-digit number shown in the attached image. "
                "Reply with just the number.")
# The counter-metric task: no image involved at all.
ASK_TEXT = ("Read notes.txt and reply with just the five-digit number it names.")

REPS = int(os.environ.get("REPS", "1"))
SEED = int(os.environ.get("SEED", "20260914"))
LIMIT = int(os.environ.get("LIMIT", "150"))


def config_for(dialect, slug, declare_image):
    mods = ', input_modalities = ["text", "image"]' if declare_image else ""
    return (
        f"p = {DIALECTS[dialect]}\n"
        f'models = {{"m": model(p, "{slug}", context = 128000, max_output = 2048{mods})}}\n'
        'default = "m"\n'
        'sandbox = ""\n'
        "max_steps = 6\n"
    )


def setup(out, dialect, slug, declare_image, route):
    shutil.rmtree(out, ignore_errors=True)
    (out / "cfg" / "strument").mkdir(parents=True)
    (out / "work").mkdir(parents=True)
    (out / "cfg" / "strument" / "config.star").write_text(config_for(dialect, slug, declare_image))
    # One file per route, never both. A working directory holding the probe
    # image and a text file with the same answer lets a model that cannot see
    # the image score as though it could -- caught when deepseek, blind to the
    # image, went hunting and read the answer off the disk.
    if route == "text":
        (out / "work" / "notes.txt").write_text(f"The verification code is {TEXT_DIGITS}.\n")
    else:
        shutil.copy(PROBE, out / "work" / "probe.png")
    return {**os.environ, "XDG_CONFIG_HOME": str(out / "cfg")}


def run_scripted(out, env, prompt):
    """The read and text routes: -m goes straight to Coder.Run, no TTY needed."""
    started = time.time()
    proc = subprocess.run(
        [BIN, "chat", "-m", prompt, "--no-git", "--no-history", "--no-color",
         "--no-shell", "--jsonl", str(out / "session.jsonl")],
        cwd=out / "work", env=env, stdin=subprocess.DEVNULL,
        capture_output=True, text=True, timeout=LIMIT,
    )
    return proc.stdout + proc.stderr, proc.returncode, time.time() - started


ANSI = re.compile(rb"\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b[=>]")


def run_attached(out, env, prompt):
    """The /attach route needs a TTY: -m bypasses command dispatch entirely."""
    import pexpect
    started = time.time()
    child = pexpect.spawn(
        BIN, ["chat", "--no-git", "--no-history", "--no-color", "--no-shell",
              "--jsonl", str(out / "session.jsonl")],
        cwd=str(out / "work"), env=env, dimensions=(40, 100), timeout=LIMIT, encoding=None)
    buf = b""

    def pump(seconds, until=None):
        nonlocal buf
        deadline = time.time() + seconds
        mark = len(buf)
        while time.time() < deadline:
            try:
                chunk = child.read_nonblocking(size=8192, timeout=2)
            except Exception:
                if not child.isalive():
                    break
                continue
            buf += chunk
            # readline re-queries the cursor on every redraw; answer each time
            # or it blocks after exactly one command.
            for _ in range(chunk.count(b"\x1b[6n")):
                child.send("\x1b[1;1R")
            # Stop as soon as the turn is over rather than waiting out the
            # clock: the usage line is the last thing a completed turn prints.
            if until and until in ANSI.sub(b"", buf[mark:]):
                break
        return buf

    pump(8, b"> ")
    child.send("/attach probe.png\r")
    pump(6, b"Attached ")
    child.send(prompt + "\r")
    pump(LIMIT - 25, b"Tokens:")
    child.send("/exit\r")
    pump(3)
    try:
        child.close(force=True)
    except Exception:
        pass
    return ANSI.sub(b"", buf).decode("utf-8", "replace"), 0, time.time() - started


def jobs():
    out = []
    # Dialect arms: the model held constant, so the dialect is the variable.
    for dialect in DIALECTS:
        for route in ("read", "attach"):
            out.append((dialect, "mimo", route, True))
    # Vendor arms: the rest of the vision-capable panel, one dialect.
    for alias in ("glm", "luna", "qwen3.8-27b"):
        for route in ("read", "attach"):
            out.append(("chat", alias, route, True))
    # The projection: real text-only models, declared honestly.
    for alias in ("deepseek", "hy3"):
        out.append(("chat", alias, "read", False))
    # The counter-arm: a text-only model declared as image-capable. If the
    # provider rejects it, the declaration is load-bearing; if it sails
    # through, the whole capability mechanism is belt-and-braces.
    out.append(("chat", "deepseek", "read", True))
    # Counter-metric: the same shape of task with no image anywhere.
    for dialect in DIALECTS:
        out.append((dialect, "mimo", "text", True))
    return out


def main():
    runs = D / "runs"
    runs.mkdir(exist_ok=True)
    plan = [(d, a, r, m, rep) for (d, a, r, m) in jobs() for rep in range(REPS)]
    random.Random(SEED).shuffle(plan)
    (runs / "_order.json").write_text(json.dumps(
        {"seed": SEED, "digits": DIGITS, "jobs": plan}, indent=1))

    results = []
    for n, (dialect, alias, route, declare, rep) in enumerate(plan, 1):
        name = f"{dialect}-{alias}-{route}-{'decl' if declare else 'nodecl'}-{rep}"
        out = runs / name
        slug, _ = PANEL[alias]
        env = setup(out, dialect, slug, declare, route)
        prompt = {"read": ASK, "attach": ASK_ATTACHED, "text": ASK_TEXT}[route]
        runner = run_attached if route == "attach" else run_scripted
        try:
            text, code, secs = runner(out, env, prompt)
        except subprocess.TimeoutExpired:
            text, code, secs = "TIMEOUT", -1, float(LIMIT)
        except Exception as exc:  # a harness fault, not a result
            text, code, secs = f"HARNESS ERROR: {exc!r}", -2, 0.0
        (out / "stdout.txt").write_text(text)
        want = TEXT_DIGITS if route == "text" else DIGITS
        hit = want in text
        leak = (TEXT_DIGITS in text) if route != "text" else (DIGITS in text)
        results.append({"name": name, "dialect": dialect, "model": alias,
                        "route": route, "declared_image": declare, "rep": rep,
                        "exit": code, "seconds": round(secs, 1), "digits": hit,
                        "leak": leak})
        print(f"[{n}/{len(plan)}] {name:<46} exit={code:<3} {secs:5.1f}s "
              f"digits={'YES' if hit else 'no '}", flush=True)
        (runs / "_results.json").write_text(json.dumps(results, indent=1))


if __name__ == "__main__":
    main()
