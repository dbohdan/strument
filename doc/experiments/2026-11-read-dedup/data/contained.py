"""Re-reads inside an earlier window of an unchanged file, and reads from run_code programs."""
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
sys.path.insert(0, os.path.dirname(__file__))
WRITERS = re.compile(r"\b(edit|write|bash|commit)\s*\(")
PROG_READ = re.compile(r"\bread(?:_text)?\s*\(\s*(?:\{\s*path\s*:\s*)?[\"'`]([^\"'`]+)[\"'`]")
def window(args, total=None):
    lo = int(args.get("offset") or 0) or 1
    lim = int(args.get("limit") or 0) or 2000
    return lo, lo + lim - 1
stats = collections.Counter()
for root in sys.argv[1:]:
    for lg in logs(root):
        seen = {}  # path -> list of (lo, hi, state)
        for line in open(lg):
            r = json.loads(line)
            if not (r["type"] == "message" and r.get("role") == "assistant"):
                continue
            for tc in r.get("tool_calls") or []:
                try:
                    args = json.loads(tc.get("arguments") or "{}")
                except ValueError:
                    args = {}
                if not isinstance(args, dict):
                    continue
                name = tc["name"]
                if name == "read":
                    p = (args.get("path") or "").lstrip("./")
                    lo, hi = window(args)
                    prior = [w for w in seen.get(p, []) if w[2] == "clean"]
                    if any(a <= lo and hi <= b for a, b, _ in prior):
                        stats["contained_clean"] += 1
                    elif any(not (hi < a or lo > b) for a, b, _ in prior):
                        stats["overlapping_clean"] += 1
                    seen.setdefault(p, []).append((lo, hi, "clean"))
                elif name in ("edit", "write"):
                    p = (args.get("path") or "").lstrip("./")
                    seen[p] = [(a, b, "edited") for a, b, _ in seen.get(p, [])]
                elif name == "bash":
                    seen = {p: [(a, b, "bash" if s == "clean" else s) for a, b, s in ws] for p, ws in seen.items()}
                elif name == "run_code":
                    code = args.get("code", "")
                    stats["programs"] += 1
                    for p in PROG_READ.findall(code):
                        stats["program_reads"] += 1
                        if any(s == "clean" for _, _, s in seen.get(p.lstrip("./"), [])):
                            stats["program_reread_clean"] += 1
                    if WRITERS.search(code):
                        seen = {p: [(a, b, "bash" if s == "clean" else s) for a, b, s in ws] for p, ws in seen.items()}
print(dict(stats))
