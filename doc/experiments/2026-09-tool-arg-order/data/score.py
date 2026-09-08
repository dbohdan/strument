"""Read the top-level key order out of a tool call's raw arguments JSON.

The order is the measurement, so it cannot come from json.loads: a dict
records what the keys are, not the order the model produced them in (and
Python would preserve insertion order only by accident of the parser). This
scans the raw string instead.

The trap it has to survive is a `"path":` that appears *inside* the file
content — the model writing a program that mentions a path. A naive regex over
the whole arguments string finds that one and calls the call path-first when it
was not. So this tracks string and escape state and only records keys at depth
1.
"""


def top_level_keys(args: str) -> list[str]:
    keys: list[str] = []
    depth = 0
    in_str = False
    esc = False
    buf: list[str] = []
    expect_val = False
    for ch in args:
        if in_str:
            if esc:
                esc = False
            elif ch == "\\":
                esc = True
            elif ch == '"':
                in_str = False
                if depth == 1 and not expect_val:
                    keys.append("".join(buf))
            elif depth == 1 and not expect_val:
                buf.append(ch)
            continue
        if ch == '"':
            in_str = True
            buf = []
        elif ch in "{[":
            depth += 1
        elif ch in "}]":
            depth -= 1
        elif ch == ":" and depth == 1:
            expect_val = True
        elif ch == "," and depth == 1:
            expect_val = False
    return keys


# The field whose arrival unblocks the streaming diff, per tool.
PAYLOAD = {"write": {"content"}, "edit": {"old_string", "new_string", "anchor"}}


def path_first(tool: str, args: str) -> bool | None:
    """True if `path` is named before the payload; None if not applicable."""
    keys = top_level_keys(args)
    if "path" not in keys or not (PAYLOAD.get(tool, set()) & set(keys)):
        return None
    payload_at = min(keys.index(k) for k in PAYLOAD[tool] if k in keys)
    return keys.index("path") < payload_at


if __name__ == "__main__":
    # Self-test, offline and free: the scorer must read both directions, and
    # must not be fooled by the content of the file being written. A rig that
    # can only report one answer is not a rig.
    cases = [
        ("write", r'{"path":"a.py","content":"x"}', True),
        ("write", r'{"content":"x","path":"a.py"}', False),
        # The trap: the file being written mentions a path.
        ("write", r'{"content":"cfg = {\"path\": 1}","path":"a.py"}', False),
        # And an escaped quote right before it, to break a lazy escape scan.
        ("write", r'{"content":"say \"path\": no","path":"a.py"}', False),
        ("edit", r'{"path":"a.py","old_string":"x","new_string":"y"}', True),
        ("edit", r'{"new_string":"y","old_string":"x","path":"a.py"}', False),
        ("edit", r'{"old_string":"x","path":"a.py","new_string":"y"}', False),
        # Not applicable: no payload field at all.
        ("ls", r'{"path":"."}', None),
        ("write", r'{"path":"a.py"}', None),
    ]
    bad = 0
    for tool, args, want in cases:
        got = path_first(tool, args)
        if got != want:
            print(f"  SCORER BROKEN: {tool} {args!r} -> {got}, want {want}")
            bad += 1
    if bad:
        raise SystemExit("the scorer cannot read both directions — refusing to spend")
    print(f"scorer self-test: {len(cases)} cases, reads both directions and survives the trap")
