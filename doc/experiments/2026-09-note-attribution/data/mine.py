"""Count how models refer to harness notes in Strument session logs.

A harness note is a user-role message the harness wrote: the marked kind,
starting "[strument]", and the unmarked kinds (the automatic-check report and
the new-files note, which reach the model as plain user messages). For each
note, the reasoning and assistant text up to the next message the human typed
is searched for phrases that attribute the note to the user or to the harness.

Prints counts only, unless --snippets is given, in which case it also prints
80 characters either side of each match. Nothing else leaves the logs.

Usage: mine.py [--snippets] [LOG_DIR_OR_FILE ...]
Default: every session log under $XDG_STATE_HOME/strument (or ~/.local/state/strument).
"""
import collections, glob, json, os, re, sys

UNMARKED = {
    "The automatic checks ran after your changes": "auto-check",
    "Strument noticed that commands": "new-files",
}
# Speech or wishes put in the user's mouth. Events the note reports ("the user
# pressed Ctrl-C", "the user ran /undo") are the note read correctly, so they
# are not matched.
USER = re.compile(
    r"\b(the user|user)(?:'s)? (?:says|said|asks|asked|is asking|wants|wanted|told|tells|"
    r"mentions|mentioned|notes|noted|requests|requested|instructs|instructed|suggests|suggested)\b"
    r"|\byou (?:asked|said|told|requested|wanted|mentioned)\b"
    r"|\bas (?:you )?(?:requested|asked|instructed)\b|\bper your\b|\buser's (?:note|message|request|instruction)\b",
    re.I)
HARNESS = re.compile(
    r"\bstrument\b|\bthe harness\b|\bharness (?:note|message)\b|\bsystem (?:note|message|notice)\b"
    r"|\bthe system (?:says|said|notes|noted|tells|told)\b|\[strument\]|\bautomated (?:note|message|check)\b",
    re.I)


def kind_of(text):
    if text.startswith("[strument]"):
        words = re.sub(r"\s+", " ", text[10:]).strip().split(" ")
        return "marked: " + " ".join(words[:5])
    for prefix, name in UNMARKED.items():
        if text.startswith(prefix):
            return "unmarked: " + name
    return None


def logs(args):
    paths = args or [os.path.join(os.environ.get("XDG_STATE_HOME", os.path.expanduser("~/.local/state")), "strument")]
    for p in paths:
        if os.path.isfile(p):
            yield p
        else:
            yield from glob.glob(os.path.join(p, "**", "*.jsonl"), recursive=True)


def main():
    snippets = "--snippets" in sys.argv
    args = [a for a in sys.argv[1:] if a != "--snippets"]
    notes = collections.Counter()
    hits = collections.Counter()
    nlogs = 0
    for path in logs(args):
        try:
            recs = [json.loads(l) for l in open(path, errors="replace") if l.lstrip().startswith("{")]
        except (OSError, json.JSONDecodeError):
            continue
        nlogs += 1
        for i, r in enumerate(recs):
            if r.get("type") != "message" or r.get("role") != "user":
                continue
            k = kind_of(r.get("text") or "")
            if k is None:
                continue
            notes[k] += 1
            found = {"user": False, "harness": False}
            for later in recs[i + 1:]:
                if later.get("type") == "message" and later.get("role") == "user" and kind_of(later.get("text") or "") is None:
                    break  # the human spoke again
                if later.get("type") not in ("reasoning", "message") or later.get("role") in ("user", "tool"):
                    continue
                where = "answer" if later.get("type") == "message" else "reasoning"
                text = later.get("text") or ""
                for label, rx in (("user", USER), ("harness", HARNESS)):
                    for m in rx.finditer(text):
                        found[label] = True
                        if snippets:
                            s = text[max(0, m.start() - 80):m.end() + 80].replace("\n", " ")
                            print(f"[{k} | {label} in {where}] …{s}…")
            for label, seen in found.items():
                if seen:
                    hits[(k, label)] += 1
    print(f"\n{nlogs} logs, {sum(notes.values())} harness notes")
    print("| note | count | attributed to the user | attributed to the harness |")
    print("|---|---|---|---|")
    for k, n in notes.most_common():
        print(f"| {k} | {n} | {hits[(k, 'user')]} | {hits[(k, 'harness')]} |")


if __name__ == "__main__":
    main()
