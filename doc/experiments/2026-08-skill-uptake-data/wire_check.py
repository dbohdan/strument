#!/usr/bin/env python3
"""Rig check 1: prove the three arms differ on the wire, and differ correctly.

doc/experimenting.md 18: a trial once compared two binaries that were the same
program, and reported no difference for that excellent reason. The standing
rule is to compare the built arms and refuse to spend if they are the same.
Here the arms are not binaries but environments, so the artifact to compare is
the request each one actually sends.

Required, per arm:
  none    no "skill" tool in the tool list; no palette hex anywhere
  skill   a "skill" tool whose enum is exactly ["chart-style"]; and, before the
          model loads anything, still no palette hex -- the catalog carries the
          DESCRIPTION only, which is what makes R1 a description-proof measure
  inline  no "skill" tool; the palette present in a user message
"""
import json
import re
import sys

PALETTE = ["A5415A", "7A490D", "648C43", "00A49B", "007DB7", "54397D"]


def requests(path):
    for line in open(path):
        try:
            row = json.loads(line)
        except ValueError:
            continue
        if row.get("kind") != "raw_request":
            continue
        body = row.get("body")
        if isinstance(body, str):
            try:
                body = json.loads(body)
            except ValueError:
                continue
        if isinstance(body, dict) and "messages" in body:
            yield body


def summarize(body):
    tools = [t.get("function", {}).get("name") for t in body.get("tools", [])]
    skill = next((t["function"] for t in body.get("tools", [])
                  if t.get("function", {}).get("name") == "skill"), None)
    enum = None
    if skill:
        try:
            enum = skill["parameters"]["properties"]["name"]["enum"]
        except (KeyError, TypeError):
            enum = "MISSING"
    blob = json.dumps(body)
    return {
        "n_tools": len(tools),
        "has_skill": "skill" in tools,
        "enum": enum,
        "desc_has_hex": bool(skill) and any(h in skill.get("description", "").upper()
                                            for h in PALETTE),
        "palette_in_request": [h for h in PALETTE if h in blob.upper()],
        "user_msgs": [m["content"][:70] for m in body["messages"]
                      if m.get("role") == "user" and isinstance(m.get("content"), str)][:1],
    }


def main():
    bodies = list(requests(sys.argv[1]))
    print(f"{len(bodies)} captured chat requests\n")
    seen = {}
    for b in bodies:
        s = summarize(b)
        # First request of each shape; later ones carry tool results.
        key = (s["has_skill"], bool(s["palette_in_request"]))
        seen.setdefault(key, s)
        print(f"tools={s['n_tools']:<3} skill={str(s['has_skill']):<5} "
              f"enum={str(s['enum']):<16} desc_hex={s['desc_has_hex']} "
              f"palette_in_req={len(s['palette_in_request'])}/6  "
              f"user={s['user_msgs']}")
    print("\ndistinct request shapes:", len(seen))
    return 0


if __name__ == "__main__":
    sys.exit(main())
