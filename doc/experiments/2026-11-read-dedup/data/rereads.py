"""Count re-reads of an unchanged window in Strument session logs.

    rereads.py ROOT...
"""
import collections, glob, json, os, re, sys

def logs(root):
    """Session logs under root: a trial's copies (session-*.jsonl) or a live
    state directory's (.../sessions/<name>/log/*.jsonl)."""
    # A trial archive holds both a runner's copy of each log and the original
    # in the session's state directory: counting both counts every session
    # twice, which is how the consult sessions first came out as 182 of 91.
    copies = glob.glob(os.path.join(root, "**", "session-2*.jsonl"), recursive=True)
    if copies:
        return sorted(copies)
    return sorted(glob.glob(os.path.join(root, "**", "sessions", "*", "log", "*.jsonl"), recursive=True))

WRITERS = re.compile(r"\b(edit|write|bash|commit)\s*\(")

def blob(logpath, digest):
    d = os.path.dirname(logpath)
    for base in (d, os.path.dirname(d)):
        hits = glob.glob(os.path.join(base, "**", "blobs", digest), recursive=True)
        if hits:
            return open(hits[0], errors="replace").read()
    return None

def model_of(path):
    m = re.search(r"_(mimo|deepseek|hy4f|glm|luna|terra|sol|qwen|kimi)[^/]*", path)
    return m.group(1) if m else "?"

def scan(logpath):
    calls, results, order = {}, {}, []
    rolled = False
    for line in open(logpath):
        r = json.loads(line)
        if r["type"] == "message" and r.get("role") == "assistant":
            for tc in r.get("tool_calls") or []:
                a = tc.get("arguments")
                if a is None and tc.get("blob"):
                    a = blob(logpath, tc["blob"]) or "{}"
                try:
                    args = json.loads(a or "{}")
                except ValueError:
                    args = {}
                calls[tc["id"]] = (tc["name"], args if isinstance(args, dict) else {})
                order.append(tc["id"])
        elif r["type"] == "message" and r.get("role") == "tool":
            n = r.get("bytes") or len(r.get("text") or "")
            results[r.get("tool_call_id")] = n
        elif r["type"] not in ("message", "reasoning", "request", "session", "turn"):
            rolled = True  # compaction or anything else that may drop context
    return calls, results, order, rolled

def main():
    stats = collections.Counter()
    by_model = collections.defaultdict(collections.Counter)
    total_tool_bytes = 0
    clean_bytes = 0
    types = collections.Counter()
    for root in sys.argv[1:]:
        for lg in logs(root):
            calls, results, order, rolled = scan(lg)
            model = model_of(lg)
            stats["sessions"] += 1
            by_model[model]["sessions"] += 1
            total_tool_bytes += sum(results.values())
            seen = {}  # window -> state since: "clean" | "bash"
            windows = []  # (path, lo, hi) for containment
            for cid in order:
                name, args = calls[cid]
                if name == "read":
                    p = (args.get("path") or "").lstrip("./")
                    key = (p, int(args.get("offset") or 0) or 1, int(args.get("limit") or 0))
                    stats["reads"] += 1
                    by_model[model]["reads"] += 1
                    if key in seen:
                        state = seen[key]
                        stats["reread_" + state] += 1
                        by_model[model]["reread_" + state] += 1
                        if state == "clean":
                            clean_bytes += results.get(cid, 0)
                    seen[key] = "clean"
                elif name in ("edit", "write"):
                    p = (args.get("path") or "").lstrip("./")
                    for k in list(seen):
                        if k[0] == p:
                            seen[k] = "edited"
                elif name == "bash" or (name == "run_code" and WRITERS.search(args.get("code", ""))):
                    for k in list(seen):
                        if seen[k] == "clean":
                            seen[k] = "bash"
    print(f"sessions {stats['sessions']}, reads {stats['reads']}")
    for k in ("reread_clean", "reread_bash", "reread_edited"):
        print(f"  {k}: {stats[k]} ({100*stats[k]/max(1,stats['reads']):.1f}% of reads)")
    print(f"  clean re-read bytes: {clean_bytes} of {total_tool_bytes} tool-result bytes ({100*clean_bytes/max(1,total_tool_bytes):.1f}%)")
    print("by model:")
    for m, c in sorted(by_model.items()):
        print(f"  {m}: sessions {c['sessions']}, reads {c['reads']}, clean {c['reread_clean']}, after bash {c['reread_bash']}, after edit {c['reread_edited']}")

main()
