#!/usr/bin/env python3
"""Render a Strument JSONL session log as a readable Markdown transcript.

Strument's real transcript is rendered terminal output; this works from the
JSONL log instead, which has the thing itself (internal/coder/record.go): every
message in order, plus reasoning and tool arguments as sent. The output is
cruder than what the user saw — everything is listed flat, in log order — but
prompt, reasoning, answer, tool calls, and tool results are labelled so you can
tell them apart.

The --filter option takes a Python expression evaluated with each record bound
to the name `record`; records for which it is true are kept. It runs on the raw
dict, so `record["type"] == "message"` and `record.get("text", "")` both work:

    script/jsonl-transcript.py LOG.jsonl
    script/jsonl-transcript.py --filter 'record["role"] != "tool"' LOG.jsonl
    script/jsonl-transcript.py --filter 'record["type"] in ("reasoning", "message")' LOG.jsonl
"""

import argparse
import json
import sys


def read_log(path):
    """Yield every non-blank line parsed as JSON; skip lines that will not parse."""
    with open(path, encoding="utf-8", errors="replace") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except json.JSONDecodeError:
                continue
            yield rec


def code_block(text):
    """Wrap text in a fenced block, using a longer fence if the text has one."""
    longest = 0
    for line in text.splitlines():
        if line.startswith("```"):
            longest = max(longest, len(line) - len(line.lstrip("`")))
    fence = "`" * (longest + 3)
    return f"{fence}\n{text}\n{fence}"


def render(rec):
    """Return the Markdown for one record, or None for records with no output."""
    t = rec.get("type")

    if t == "session":
        parts = ["# Transcript", ""]
        meta = [
            f"{k}: `{rec[k]}`" for k in ("model", "root", "edit_format") if rec.get(k)
        ]
        if meta:
            parts += ["- " + m for m in meta] + [""]
        return "\n".join(parts)

    if t == "turn":
        return None  # metadata, not conversation

    if t == "reasoning":
        return f"## Reasoning\n\n{code_block(rec.get('text', ''))}\n"

    if t == "message":
        role = rec.get("role", "?")
        out = []
        if role == "user":
            out.append("## Prompt (user)")
        elif role == "assistant":
            out.append("## Assistant")
        elif role == "tool":
            out.append(f"## Tool result ({rec.get('tool_call_id', '?')})")
        else:
            out.append(f"## Message ({role})")
        if rec.get("text"):
            out += ["", code_block(rec["text"])]
        for tc in rec.get("tool_calls") or []:
            args = tc.get("arguments", "")
            try:
                args = json.dumps(json.loads(args), indent=2)
            except (json.JSONDecodeError, TypeError):
                pass
            out += [
                "",
                f"### Tool call: `{tc.get('name', '?')}`"
                + (f" ({tc['id']})" if tc.get("id") else ""),
                "",
                code_block(args),
            ]
        out.append("")
        return "\n".join(out)

    return None  # unknown type: skip rather than guess


def main():
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    ap.add_argument("log", help="Strument JSONL log")
    ap.add_argument(
        "--filter",
        metavar="EXPR",
        help="keep only records for which this Python expression is true, "
        "with the record available as `record`",
    )
    args = ap.parse_args()

    filt = None
    if args.filter:
        try:
            filt = compile(args.filter, "<--filter>", "eval")
        except SyntaxError as e:
            print(f"bad filter: {e}", file=sys.stderr)
            return 2

    rendered = []
    for rec in read_log(args.log):
        if filt is not None:
            try:
                if not eval(filt, {"__builtins__": __builtins__}, {"record": rec}):
                    continue
            except KeyError:
                # record["key"] on a record without that key just means the
                # record is of a different kind than the filter is about.
                continue
            except Exception as e:
                print(f"filter error on a record: {e}", file=sys.stderr)
                continue
        md = render(rec)
        if md:
            rendered.append(md)

    print("\n".join(rendered))
    return 0


if __name__ == "__main__":
    sys.exit(main())
