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
    """Print a per-file report of A1 (blind edits)."""
    grand_total = 0
    for path in paths:
        file_total = 0
        for i, session in enumerate(read_sessions(path), 1):
            blind = count_blind_edits(session)
            file_total += len(blind)
            if blind:
                print(f"{path}  session {i}: A1 = {len(blind)}")
                for p in sorted(blind):
                    print(f"  blind edit of {p}")
            else:
                print(f"{path}  session {i}: A1 = 0")

            distances = edit_distances(session)
            if distances:
                print(f"  A2: {len(distances)} edits, "
                      f"median distance {statistics.median(distances)}, "
                      f"max {max(d for _, d in distances)}")
                for path, d in sorted(distances, key=lambda pd: pd[1], reverse=True):
                    if d >= 5:
                        print(f"  distant edit: {path} at distance {d}")

            looks, acts = look_act_counts(session)
            if acts == 0:
                print(f"  A3: {looks} look-shaped calls, no act-shaped calls")
            else:
                print(f"  A3: look:act = {looks}:{acts} "
                      f"= {looks / acts:.2f}")
        grand_total += file_total
        print(f"{path}  total A1 = {file_total}")
    print(f"grand total A1 = {grand_total}")


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("usage: transcript-audit.py FILE.jsonl [FILE.jsonl ...]", file=sys.stderr)
        sys.exit(1)
    audit(sys.argv[1:])
