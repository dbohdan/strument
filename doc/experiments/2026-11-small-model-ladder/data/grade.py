"""grade.py TETRIS_PY HIDDEN_DIR: run every hidden test, print pass count and failures."""
import glob, os, subprocess, sys
prog, hid = sys.argv[1], sys.argv[2]
tests = sorted(glob.glob(os.path.join(hid, "*.in")))
if not os.path.exists(prog):
    print(f"PASS 0/{len(tests)} (no tetris.py)"); sys.exit()
passed, fails = 0, []
for t in tests:
    want = open(t[:-3] + ".out").read()
    try:
        r = subprocess.run([sys.executable, prog], stdin=open(t), capture_output=True, text=True, timeout=10,
                           cwd=os.path.dirname(prog))
        ok = r.returncode == 0 and r.stdout == want
        why = "" if ok else (f"exit {r.returncode}" if r.returncode else "output differs")
    except subprocess.TimeoutExpired:
        ok, why = False, "timeout"
    passed += ok
    if not ok:
        fails.append(f"{os.path.basename(t)[:-3]}: {why}")
print(f"PASS {passed}/{len(tests)}")
print("\n".join(fails))
