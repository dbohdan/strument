#!/usr/bin/env python3
"""Audit a Strument JSONL transcript for look-before-act discipline.

Reads one or more JSONL session logs and counts, per session, whether the
model looked before it acted.

A1 — blind edits: edit calls on a path that no earlier successful read
in the session returned.

A2 — edit distance: for each edit that is not blind, how many tool calls
sit between the last successful read of its path and the edit.

A3 — look:act ratio: read-shaped calls over act-shaped ones, per session.

Usage:
    script/transcript-audit.py FILE.jsonl [FILE.jsonl ...]
"""

import json
import os
import re
import statistics
import sys


def read_sessions(path):
    """Yield one session dict per session boundary in the JSONL file.

    Each session dict has:
      tool_calls  – list of {id, name, arguments} (parsed from JSON string)
      tool_results – dict mapping tool_call_id to result text
      edit_paths  – list of (call_id, path) for every edit call
      read_results – dict mapping path to the most recent result text for
                     that path (overwritten by each new read)
    """
    session = None
    for line in open(path, encoding="utf-8", errors="replace"):
        line = line.strip()
        if not line:
            continue
        try:
            rec = json.loads(line)
        except json.JSONDecodeError:
            continue
        rtype = rec.get("type")
        if rtype == "session":
            # Start a fresh session (flush any in-progress one).
            session = {
                "model": rec.get("model", ""),
                "tool_calls": [],
                "tool_results": {},
                "edit_paths": [],
                "read_results": {},      # path -> result text of most recent read
            }
        elif rtype == "message":
            if session is None:
                # Records before the first session marker — skip.
                continue
            role = rec.get("role")
            if role == "assistant":
                for tc in rec.get("tool_calls", []):
                    try:
                        args = json.loads(tc["arguments"])
                    except (json.JSONDecodeError, KeyError):
                        args = {}
                    name = tc.get("name", "")
                    tc_id = tc.get("id", "")
                    session["tool_calls"].append(
                        {"id": tc_id, "name": name, "args": args}
                    )
                    if name == "edit":
                        path_arg = args.get("path", "")
                        session["edit_paths"].append((tc_id, path_arg))
            elif role == "tool":
                tc_id = rec.get("tool_call_id", "")
                text = rec.get("text", "")
                session["tool_results"][tc_id] = text
        elif rtype == "turn":
            # End of session — yield and reset.
            if session is not None:
                yield session
                session = None

    # Handle file ending without a "turn" record.
    if session is not None:
        yield session


def is_successful_read(result_text, path):
    """Return True if result_text looks like a successful read of *path*.

    A successful read starts with the filename followed by a line count,
    e.g. "calc.go (7 lines)".  A failed read says so in its text.
    """
    if result_text is None:
        return False
    # The file's own report style: starts with "filename (N lines)\n".
    if re.match(r"^" + re.escape(path) + r" \(\d+ lines?\)", result_text):
        return True
    return False


def count_blind_edits(session):
    """Return the set of paths that were edited without an earlier successful read."""
    # Build a lookup from tool_call_id to result text.
    results = session["tool_results"]

    # Walk tool calls in order.  For each read, record a success; for each
    # edit, check whether the path has been successfully read *so far*.
    read_successes = set()  # paths with at least one successful read so far
    blind = set()

    # We need to iterate through tool calls in the order they appear, matching
    # each to its result.  Use the ordered list from parsing.
    # Build id->call lookup for quick access.
    id_to_call = {tc["id"]: tc for tc in session["tool_calls"]}

    for tc in session["tool_calls"]:
        name = tc["name"]
        args = tc["args"]
        if name == "read":
            path = args.get("path", "")
            result = results.get(tc["id"])
            if is_successful_read(result, path):
                read_successes.add(path)
        elif name == "edit":
            path = args.get("path", "")
            if path and path not in read_successes:
                blind.add(path)

    return blind


def edit_distances(session):
    """Return a (path, distance) pair for each non-blind edit.

    Distance is the number of tool calls between the last successful read
    of the path and the edit: an edit immediately after its read is 0.
    Blind edits (no earlier successful read of the path) have no distance
    and are left out — A1 owns them.
    """
    results = session["tool_results"]
    last_read_index = {}  # path -> tool-call index of most recent successful read
    distances = []

    for i, tc in enumerate(session["tool_calls"]):
        name = tc["name"]
        path = tc["args"].get("path", "")
        if name == "read":
            if is_successful_read(results.get(tc["id"]), path):
                last_read_index[path] = i
        elif name == "edit":
            if path in last_read_index:
                distances.append((path, i - last_read_index[path] - 1))

    return distances


LOOK_SHAPED = ("read", "grep", "glob", "ls", "symbol")
ACT_SHAPED = ("edit", "write")


def rechecked_paths(session):
    """Return edited paths that are looked at again after their last edit."""
    calls = session["tool_calls"]
    last_edit = {}
    for i, tc in enumerate(calls):
        if tc["name"] == "edit":
            path = tc["args"].get("path", "")
            if path:
                last_edit[path] = i

    rechecked = set()
    for path, edit_index in last_edit.items():
        for tc in calls[edit_index + 1:]:
            if tc["name"] == "read" and tc["args"].get("path", "") == path:
                rechecked.add(path)
                break
            if tc["name"] in ("check", "bash"):
                rechecked.add(path)
                break
    return rechecked


def unused_reads(session):
    """Return successfully read paths with no later read and no edit or write."""
    calls = session["tool_calls"]
    results = session["tool_results"]
    successful_reads = []
    for i, tc in enumerate(calls):
        if tc["name"] != "read":
            continue
        path = tc["args"].get("path", "")
        if is_successful_read(results.get(tc["id"]), path):
            successful_reads.append((i, path))

    edited_or_written = {
        tc["args"].get("path", "")
        for tc in calls
        if tc["name"] in ("edit", "write") and tc["args"].get("path", "")
    }
    unused = set()
    for read_index, path in successful_reads:
        if path in edited_or_written:
            continue
        used_again = any(
            tc["name"] == "read"
            and tc["args"].get("path", "") == path
            for tc in calls[read_index + 1:]
        )
        if not used_again:
            unused.add(path)
    return unused


def look_act_counts(session):
    """Return (looks, acts) — the tool-call counts A3's ratio is over."""
    looks = 0
    acts = 0
    for tc in session["tool_calls"]:
        if tc["name"] in LOOK_SHAPED:
            looks += 1
        elif tc["name"] in ACT_SHAPED:
            acts += 1
    return looks, acts


def audit(paths):
    """Print one report block per session, then a total across all transcripts."""
    totals = {"A1": 0, "A2_edits": 0, "A3_looks": 0, "A3_acts": 0,
              "A4": 0, "A5": 0}
    for path in paths:
        for i, session in enumerate(read_sessions(path), 1):
            blind = count_blind_edits(session)
            distances = edit_distances(session)
            looks, acts = look_act_counts(session)
            edited = {p for _, p in session["edit_paths"] if p}
            unrechecked = edited - rechecked_paths(session)
            unused = unused_reads(session)

            print(f"{path}  session {i}  model={session['model']}")
            print(f"  A1 blind edits      {len(blind)}" +
                  (f"   ({', '.join(sorted(blind))})" if blind else ""))
            if distances:
                values = [d for _, d in distances]
                median = statistics.median(values)
                print(f"  A2 edit distance    {len(distances)} edits, median distance "
                      f"{median}, max {max(values)}")
            else:
                print("  A2 edit distance    0 edits, median distance 0, max 0")
            if acts == 0:
                print(f"  A3 look:act         {looks} look-shaped calls, no act-shaped calls")
            else:
                print(f"  A3 look:act         {looks}:{acts} = {looks / acts:.2f}")
            print(f"  A4 unrechecked      {len(unrechecked)}" +
                  (f"   ({', '.join(sorted(unrechecked))})" if unrechecked else ""))
            print(f"  A5 reads w/o follow-up  {len(unused)}" +
                  (f"   ({', '.join(sorted(unused))})" if unused else ""))
            if unused:
                print("    (a read can inform work on another file; not all of these are waste)")

            totals["A1"] += len(blind)
            totals["A2_edits"] += len(distances)
            totals["A3_looks"] += looks
            totals["A3_acts"] += acts
            totals["A4"] += len(unrechecked)
            totals["A5"] += len(unused)

    print("\nTOTAL across all transcripts")
    print(f"  A1 blind edits          {totals['A1']}")
    print(f"  A2 edits                {totals['A2_edits']}")
    if totals["A3_acts"] == 0:
        print(f"  A3 look:act             {totals['A3_looks']} look-shaped calls, no act-shaped calls")
    else:
        print(f"  A3 look:act             {totals['A3_looks']}:{totals['A3_acts']} = {totals['A3_looks'] / totals['A3_acts']:.2f}")
    print(f"  A4 unrechecked          {totals['A4']}")
    print(f"  A5 reads w/o follow-up  {totals['A5']}")


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("usage: transcript-audit.py FILE.jsonl [FILE.jsonl ...]", file=sys.stderr)
        sys.exit(1)
    audit(sys.argv[1:])
