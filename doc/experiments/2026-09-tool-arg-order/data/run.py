#!/usr/bin/env python3
"""Argument-order trial: does advertising `path` first change what a model sends?

Two arms, differing only in the JSON Schema Strument sends for edit and write:

  A  properties as a Go map — encoding/json sorts them, so write reads
     content, path and edit reads new_string, old_string, path
  B  properties ordered, path first

One message per run, in a fresh empty directory. The prompt asks for a file to
be created and then changed, so a run exercises both tools.

The job list is shuffled with a recorded seed. That is not ceremony at this
size: in the prompt-scope trial, running every A and then every B confounded
the arm with the hour it ran, and shuffling alone moved a p=0.0009 to p=0.15.
"""

import json
import os
import pathlib
import random
import shutil
import subprocess
import sys
import time

D = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(D))
from score import path_first  # noqa: E402

MODELS = {
    # alias: (openrouter id, reasoning effort or None)
    # qwen3.6-35b-a3b first because it is the model that showed the bug; the
    # rest are the standard panel. GLM and the Qwen pair default to maximum
    # thinking, which costs minutes per run and buys nothing here, so they are
    # pinned low — the same in both arms, so it cannot confound the comparison.
    "qwen3.6-35b": ("qwen/qwen3.6-35b-a3b", "low"),
    "mimo": ("xiaomi/mimo-v2.5", None),
    "deepseek": ("deepseek/deepseek-v4-flash-0731", None),
    "glm": ("z-ai/glm-5.3-flash", "low"),
    "hy3": ("tencent/hy3", None),
    "luna": ("openai/gpt-5.6-luna", None),
    "qwen3.8-27b": ("qwen/qwen3.8-27b", "low"),
}

PROMPT = (
    "Create hello.py holding a function greet(name) that returns a greeting "
    "string. Then change that function to also accept an optional greeting "
    "word, defaulting to \"Hello\"."
)

REPS = int(os.environ.get("REPS", "4"))
SEED = int(os.environ.get("SEED", "20260905"))
LIMIT = int(os.environ.get("LIMIT", "240"))


def config_for(alias: str) -> str:
    model_id, effort = MODELS[alias]
    reasoning = f', reasoning = "{effort}"' if effort else ""
    return (
        'p = provider("openrouter", api_key = env("OPENROUTER_API_KEY"))\n'
        f'models = {{"m": model(p, "{model_id}", context = 128000, '
        f"max_output = 4096{reasoning})}}\n"
        'default = "m"\n'
        "sandbox = \"\"\n"
    )


def main() -> None:
    runs = D / "runs"
    runs.mkdir(exist_ok=True)
    jobs = [(a, arm, r) for a in MODELS for arm in "AB" for r in range(REPS)]
    random.Random(SEED).shuffle(jobs)
    (runs / "_order.json").write_text(json.dumps({"seed": SEED, "jobs": jobs}, indent=1))

    for n, (alias, arm, rep) in enumerate(jobs, 1):
        name = f"{alias}-{arm}-{rep}"
        out = runs / name
        if (out / "session.jsonl").exists():
            print(f"[{n}/{len(jobs)}] {name}: already done")
            continue
        shutil.rmtree(out, ignore_errors=True)
        (out / "cfg" / "strument").mkdir(parents=True)
        (out / "work").mkdir(parents=True)
        (out / "cfg" / "strument" / "config.star").write_text(config_for(alias))

        started = time.time()
        proc = subprocess.run(
            [
                str(D / "bin" / f"strument-{arm}"), "chat",
                "-m", PROMPT,
                "--no-git", "--no-history", "--no-color", "--no-shell",
                "--yes", "steps",
                "--jsonl", str(out / "session.jsonl"),
            ],
            cwd=out / "work",
            env={**os.environ, "XDG_CONFIG_HOME": str(out / "cfg")},
            stdin=subprocess.DEVNULL,
            capture_output=True,
            text=True,
            timeout=LIMIT + 30,
        )
        (out / "stdout.txt").write_text(proc.stdout)
        (out / "stderr.txt").write_text(proc.stderr)
        calls = read_calls(out / "session.jsonl")
        print(
            f"[{n}/{len(jobs)}] {name}: exit={proc.returncode} "
            f"{time.time() - started:5.1f}s calls={calls}"
        )


def read_calls(path: pathlib.Path) -> list[tuple[str, bool | None]]:
    if not path.exists():
        return []
    out = []
    for line in path.read_text().splitlines():
        if not line.strip():
            continue
        try:
            rec = json.loads(line)
        except json.JSONDecodeError:
            continue
        for tc in rec.get("tool_calls") or []:
            verdict = path_first(tc["name"], tc["arguments"])
            if verdict is not None:
                out.append((tc["name"], verdict))
    return out


if __name__ == "__main__":
    main()
