import sys,json
for l in sys.stdin:
    try: d=json.loads(l)
    except Exception: print(l, end=""); continue
    print(d["model"][:24], d["placement"], d["field"], "HIT" if d["hit"] else ("UNK" if d["unknown"] else "-"), d.get("error",""), "|", d.get("content","")[:70].replace("\n"," "), "| fields", d.get("fields"), d.get("usage"))
