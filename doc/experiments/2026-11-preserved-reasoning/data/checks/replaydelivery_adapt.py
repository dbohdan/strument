"""replaydelivery_adapt.py IN OUT: add replayed to a record written before the field, from its reasoning rows.

A request's prompt holds the reasoning of every earlier reply, not of the reply it produced, and the
recorder writes [reasoning, assistant message, request] per send, so a request carries what was
accumulated before its own reasoning row. Estimated at four characters a token, as Strument's own
estimate is, and added to estimated, which in these builds did not count it."""
import json, sys
cum = pending = 0
with open(sys.argv[2], "w") as out:
    for line in open(sys.argv[1]):
        r = json.loads(line)
        if r.get("type") == "reasoning":
            pending += len(r.get("text", "")) // 4
        elif r.get("type") == "request":
            if r.get("estimated"):
                r["replayed"] = cum
                r["estimated"] += cum
            cum += pending
            pending = 0
        out.write(json.dumps(r) + "\n")
