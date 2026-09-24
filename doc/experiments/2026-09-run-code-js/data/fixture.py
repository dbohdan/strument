"""Generates the probe's fixture: a neutral Go project (no package.json, no
Python) with docs, data files, a CSV, a log and notes. Two seeded passes,
because the data files were enlarged after the pilot (see the
preregistration's amendment); the docs and Go files come from the first."""
import datetime, os, random, sys

root = sys.argv[1] if len(sys.argv) > 1 else "tree"
os.makedirs(root, exist_ok=True)
os.chdir(root)

random.seed(20260924)
for d in ("docs/guides", "docs/ops", "data", "logs", "cmd/relay"):
    os.makedirs(d, exist_ok=True)
topics = ["deploy", "config", "retries", "queues", "alerts", "backups", "auth", "limits",
          "metrics", "release", "storage", "tracing", "caching", "upgrades"]
for i, t in enumerate(topics):
    d = "docs/guides" if i % 2 else "docs/ops"
    body = [f"# {t.title()}", ""]
    for j in range(12):
        body.append(f"This section describes how {t} behaves under load, paragraph {j}.")
    if i in (1, 4, 6, 9, 12):
        body.insert(5, "Each client gets a retry budget that refills slowly.")
    open(f"{d}/{t}.md", "w").write("\n".join(body) + "\n")
# The first pass's small data files, generated only to keep the random
# stream identical to the tree the pilot ran on; the second pass replaces them.
for n in "abcde":
    [random.randint(1, 999) for _ in range(random.randint(8, 15))]
[random.choice([random.randint(5, 99), random.randint(100, 9999), random.randint(10000, 99999)]) for k in range(40)]
open("go.mod", "w").write("module example.com/relay\n\ngo 1.26\n")
open("cmd/relay/main.go", "w").write('package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Println("relay")\n}\n')
open("README.md", "w").write("# relay\n\nA small message relay. See docs/.\n")

random.seed(20260925)
for n in "abcde":
    open(f"data/{n}.txt", "w").write("\n".join(str(random.randint(1, 999)) for _ in range(random.randint(150, 250))) + "\n")
rows = ["name,bytes"] + [f"asset-{k:03d},{random.choice([random.randint(5, 99), random.randint(100, 9999), random.randint(10000, 99999)])}" for k in range(400)]
open("data/sizes.csv", "w").write("\n".join(rows) + "\n")
start = datetime.date(2026, 1, 3)
lines = []
for k in range(600):
    d = start + datetime.timedelta(days=random.randint(0, 240))
    lines.append(f"{d.isoformat()}T{random.randint(0, 23):02d}:{random.randint(0, 59):02d}:00 level=info msg=\"relay tick {k}\"")
open("logs/app.log", "w").write("\n".join(lines) + "\n")
notes = []
for k in range(300):
    notes.append("" if k % 7 == 3 else "note " + " ".join(random.choice(["alpha", "beta", "gamma", "delta", "epsilon"]) for _ in range(random.randint(2, 40))))
notes[211] = "The XYZZY marker is on this line."
open("notes.md", "w").write("\n".join(notes) + "\n")
