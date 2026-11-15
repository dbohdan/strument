"""What generate.py and judge.py share: the models, one chat call, fences.

The key is read from OPENROUTER_API_KEY and never written anywhere. Every
model is pinned to one provider with fallbacks off, and reasoning is pinned
low (experimenting.md, pin the provider; pin reasoning low). Terra runs on
OpenAI's flex tier, which OpenRouter passes through and bills at half price.
"""

import http.client
import json
import os
import random
import re
import ssl
import time
import urllib.error
import urllib.request

MODELS = {
    "mimo": {"model": "xiaomi/mimo-v2.6-flash", "provider": "xiaomi"},
    "terra": {"model": "openai/gpt-6.1-terra", "provider": "openai", "extra": {"service_tier": "flex"}},
    "deepseek": {"model": "deepseek/deepseek-v4.1-flash", "provider": "wafer"},
}

_CA = "/root/.ccr/ca-bundle.crt"
_CTX = ssl.create_default_context(cafile=_CA) if os.path.exists(_CA) else ssl.create_default_context()


# A judge's reasoning, disabled. At "low", both judges spent their whole budget
# deliberating over the judging prompts — 6,000 tokens for MiMo, 16,000 for
# DeepSeek — and wrote no verdict; a reasoning token cap was ignored by both
# providers. Disabled, DeepSeek answers in about 1,100 tokens and MiMo reasons
# in its visible text instead, which is where a verdict can be read.
NO_REASONING = {"enabled": False}


def chat(name, prompt, max_tokens, timeout=600, reasoning=None):
    """One user message to a model; returns a record of the call.

    Transient failures (429, 5xx, a dropped connection) are retried with
    backoff, up to five attempts. The record keeps the error of the last one
    otherwise, so a provider failure is never mistaken for an empty answer
    (experimenting.md §3).
    """
    m = MODELS[name]
    body = {
        "model": m["model"],
        "messages": [{"role": "user", "content": prompt}],
        "max_tokens": max_tokens,
        "reasoning": reasoning or {"effort": "low"},
        "provider": {"only": [m["provider"]], "allow_fallbacks": False},
        "usage": {"include": True},
    }
    body.update(m.get("extra", {}))
    req = urllib.request.Request(
        "https://openrouter.ai/api/v1/chat/completions", data=json.dumps(body).encode(),
        headers={"Authorization": "Bearer " + os.environ["OPENROUTER_API_KEY"],
                 "Content-Type": "application/json"})
    last = None
    for attempt in range(5):
        t0 = time.time()
        try:
            with urllib.request.urlopen(req, context=_CTX, timeout=timeout) as r:
                d = json.load(r)
            # A 200 can carry an error in the body and no choices: OpenRouter
            # reports some upstream failures that way. It is a failure, not an
            # empty answer, and it is retried like one.
            if d.get("error") or not d.get("choices"):
                last = "body error: " + json.dumps(d.get("error") or d)[:300]
                time.sleep(min(60, 2 ** attempt + random.random()))
                continue
            choice = d["choices"][0]
            # The provider ended the stream with an error: what came back is
            # a fragment, not an answer.
            if choice.get("finish_reason") == "error":
                last = "finish_reason error after " + str((d.get("usage") or {}).get("completion_tokens")) + " tokens"
                time.sleep(min(60, 2 ** attempt + random.random()))
                continue
            msg = choice.get("message") or {}
            u = d.get("usage") or {}
            return {
                "model": name, "slug": m["model"], "ok": True, "seconds": round(time.time() - t0, 1),
                "provider": d.get("provider"), "service_tier": d.get("service_tier"),
                "finish_reason": choice.get("finish_reason"),
                "text": msg.get("content") or "", "reasoning": msg.get("reasoning") or "",
                "prompt_tokens": u.get("prompt_tokens"), "completion_tokens": u.get("completion_tokens"),
                "reasoning_tokens": (u.get("completion_tokens_details") or {}).get("reasoning_tokens"),
                "cost": u.get("cost"), "attempts": attempt + 1,
            }
        except urllib.error.HTTPError as e:
            last = f"HTTP {e.code}: {e.read().decode(errors='replace')[:300]}"
            if e.code not in (408, 429) and e.code < 500:
                break
        except (urllib.error.URLError, TimeoutError, ConnectionError, json.JSONDecodeError,
                http.client.IncompleteRead, http.client.RemoteDisconnected) as e:
            last = repr(e)
        time.sleep(min(60, 2 ** attempt + random.random()))
    return {"model": name, "slug": m["model"], "ok": False, "error": last, "text": ""}


def fence(text):
    """A backtick fence longer than any run inside text."""
    longest = max((len(r) for r in re.findall(r"`+", text)), default=0)
    return "`" * max(3, longest + 1)


def fenced(text):
    f = fence(text)
    return f"{f}\n{text}{'' if text.endswith(chr(10)) else chr(10)}{f}"


FENCE_LINE = re.compile(r"^(`{3,}|~{3,})[^\n`]*$")


def extract_block(answer):
    """The one fenced block an answer was asked to be, and how it was found.

    Whole-answer first: an answer that opens with a fence and closes with the
    same one is that block, whatever fences sit inside it — a README with a
    code sample is the common case, and a first-block regex would cut it at
    the sample. Otherwise the first block that closes with its own fence.
    """
    lines = answer.strip("\n").split("\n")
    if len(lines) >= 2 and FENCE_LINE.match(lines[0]):
        f = re.match(r"^(`{3,}|~{3,})", lines[0]).group(1)
        if lines[-1].strip() == f:
            return "\n".join(lines[1:-1]) + "\n", "whole"
    for i, line in enumerate(lines):
        m = FENCE_LINE.match(line)
        if not m:
            continue
        f = re.match(r"^(`{3,}|~{3,})", line).group(1)
        for j in range(i + 1, len(lines)):
            if lines[j].strip() == f:
                return "\n".join(lines[i + 1:j]) + "\n", "first"
        break
    return None, "none"
