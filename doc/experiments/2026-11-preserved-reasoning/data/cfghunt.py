#!/usr/bin/env python3
"""cfghunt.py: phase 3's fixture, a service whose live config is chosen by a rule in three places.

    cfghunt.py build DIR     write the project as shipped
    cfghunt.py grade DIR     print each environment's live request_timeout and the verdict
    cfghunt.py check         verify the claims below in a temp dir

The rule: deploy/<env>.env names APP_PROFILE; the loader splits a profile
"<region>-<size>" and layers config/base.conf, config/sizes/<size>.conf,
config/regions/<region>.conf, then overrides/<profile>.conf, later winning.
Production's timeout lives in an override; staging's in a size file. So the
file turn 1 edits is the wrong one for turn 2, and only the rule finds the
right one.

check verifies: as shipped, prod 10, staging 12, dev 5; the reference turn-1
fix gives prod 30 and leaves staging at 12; the reference turn-2 fix then gives
staging 30 with prod and dev unchanged; and the tempting wrong turn-2 edits
(turn 1's file again, base.conf, sizes/large.conf) leave staging at 12.
"""
import os, subprocess, sys, tempfile

FILES = {
"svc/__init__.py": "",
"svc/config.py": '''"""Configuration for one deployment environment.

Settings come in layers, and a later layer wins over an earlier one.
"""
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def _read(path):
    values = {}
    for line in path.read_text().splitlines():
        line = line.split("#", 1)[0].strip()
        if line:
            key, value = (part.strip() for part in line.split("=", 1))
            values[key] = int(value) if value.isdigit() else value
    return values


def layers(env):
    """The files that make up env's configuration, in the order they apply."""
    profile = _read(ROOT / "deploy" / f"{env}.env")["APP_PROFILE"]
    files = [ROOT / "config" / "base.conf"]
    if "-" in profile:
        region, size = profile.split("-", 1)
        files.append(ROOT / "config" / "sizes" / f"{size}.conf")
        files.append(ROOT / "config" / "regions" / f"{region}.conf")
    files.append(ROOT / "overrides" / f"{profile}.conf")
    return [f for f in files if f.exists()]


def load(env):
    values = {}
    for f in layers(env):
        values.update(_read(f))
    return values
''',
"svc/client.py": '''from svc import config


class Client:
    def __init__(self, env):
        cfg = config.load(env)
        self.timeout = cfg["request_timeout"]
        self.retries = cfg["retries"]
        self.endpoint = cfg.get("endpoint", "http://localhost:8080")
''',
"deploy/prod.env": "APP_PROFILE=eu-large\nLOG_LEVEL=warn\n",
"deploy/staging.env": "APP_PROFILE=eu-small\nLOG_LEVEL=info\n",
"deploy/dev.env": "APP_PROFILE=local\nLOG_LEVEL=debug\n",
"config/base.conf": "# Defaults for every environment.\nrequest_timeout = 15\nretries = 3\npool = 4\n",
"config/sizes/large.conf": "request_timeout = 20\npool = 16\n",
"config/sizes/small.conf": "request_timeout = 12\npool = 2\n",
"config/regions/eu.conf": "endpoint = https://eu.api.internal\n",
"config/regions/us.conf": "endpoint = https://us.api.internal\nrequest_timeout = 12\n",
"overrides/eu-large.conf": "# Lowered during the March failover; see the incident notes.\nrequest_timeout = 10\n",
"overrides/local.conf": "request_timeout = 5\nretries = 0\n",
"README.md": "# svc\n\nThe API client service. Each deployment environment (`deploy/*.env`) has its own\nconfiguration, assembled by `svc/config.py`.\n",
}

ENVS = ("prod", "staging", "dev")


def write(root, files):
    for rel, text in files.items():
        p = os.path.join(root, rel)
        os.makedirs(os.path.dirname(p) or ".", exist_ok=True)
        with open(p, "w") as f:
            f.write(text)


def live(root):
    code = "import json; from svc import config; print(json.dumps({e: config.load(e).get('request_timeout') for e in %r}))" % (ENVS,)
    r = subprocess.run([sys.executable, "-I", "-c", "import sys; sys.path.insert(0, %r); " % root + code],
                       capture_output=True, text=True, timeout=30)
    if r.returncode:
        return {"error": r.stderr.strip().splitlines()[-1] if r.stderr.strip() else "failed"}
    import json
    return json.loads(r.stdout)


def verdict(v):
    """Turn-by-turn pass marks against the goal: prod 30, staging 30, dev untouched at 5."""
    return {"t1": v.get("prod") == 30, "t2": v.get("staging") == 30, "dev_ok": v.get("dev") == 5}


def edit(root, rel, old, new):
    p = os.path.join(root, rel)
    s = open(p).read() if os.path.exists(p) else ""
    assert old in s or old == "", (rel, old)
    with open(p, "w") as f:
        f.write(s.replace(old, new) if old else new)


def check():
    ok = True
    def expect(label, root, want):
        nonlocal ok
        got = live(root)
        good = all(got.get(k) == v for k, v in want.items())
        ok &= good
        print(f"{'ok ' if good else 'BAD'} {label}: {got}")
    with tempfile.TemporaryDirectory() as t:
        def fresh(name):
            d = os.path.join(t, name); write(d, FILES); return d
        expect("shipped", fresh("shipped"), {"prod": 10, "staging": 12, "dev": 5})
        r = fresh("ref")
        edit(r, "overrides/eu-large.conf", "request_timeout = 10", "request_timeout = 30")
        expect("reference turn 1", r, {"prod": 30, "staging": 12, "dev": 5})
        edit(r, "config/sizes/small.conf", "request_timeout = 12", "request_timeout = 30")
        expect("reference turn 2", r, {"prod": 30, "staging": 30, "dev": 5})
        for label, rel, old, new in [
            ("wrong: turn 1's file again", "overrides/eu-large.conf", "request_timeout = 30", "request_timeout = 30\n"),
            ("wrong: base.conf", "config/base.conf", "request_timeout = 15", "request_timeout = 30"),
            ("wrong: sizes/large.conf", "config/sizes/large.conf", "request_timeout = 20", "request_timeout = 30"),
        ]:
            w = fresh("w" + str(abs(hash(label))))
            edit(w, "overrides/eu-large.conf", "request_timeout = 10", "request_timeout = 30")
            edit(w, rel, old, new)
            expect(label, w, {"staging": 12})
        a = fresh("alt")  # an override for staging is also a correct fix
        edit(a, "overrides/eu-large.conf", "request_timeout = 10", "request_timeout = 30")
        edit(a, "overrides/eu-small.conf", "", "request_timeout = 30\n")
        expect("alternative turn 2: an override", a, {"prod": 30, "staging": 30, "dev": 5})
    print("CHECK", "OK" if ok else "FAILED")
    return ok


if __name__ == "__main__":
    cmd = sys.argv[1]
    if cmd == "build":
        write(sys.argv[2], FILES)
    elif cmd == "grade":
        v = live(sys.argv[2]); print(v, verdict(v))
    elif cmd == "check":
        sys.exit(0 if check() else 1)
