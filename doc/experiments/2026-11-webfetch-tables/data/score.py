"""Score one run: run_code calls, failed programs, and whether each final table matches the page."""
import re, sys, json
truth_md, out_path = sys.argv[1], sys.argv[2]
order = ["0->R", "0->L", "R->2", "R->0", "2->L", "2->R", "L->0", "L->2"]
def tables(md):
    got, cur = {}, None
    for line in md.splitlines():
        if "Wall Kick Data" in line:
            cur = "jlstz" if "J, L, S, T, Z" in line else "i"; got[cur] = {}
        m = re.match(r"\| (\S->\S) \|(.*)\|$", line)
        if cur and m:
            got[cur][m[1]] = [tuple(int(v) for v in p) for p in re.findall(r"\(\s*([+-]?\d),\s*([+-]?\d)\)", m[2])]
    return {k: [v[r] for r in order] for k, v in got.items()}
truth = tables(open(truth_md).read())
out = open(out_path, encoding="utf8", errors="replace").read()
answer = out[out.rfind("‹/›"):] if "‹/›" in out else out
def final(name):
    m = re.search(name + r"\[8\][^{]*\{(.*?)\n\s*\}\s*;", answer, re.S)
    if not m: return None
    rows = [r for r in m[1].splitlines() if "{" in r]
    return [[tuple(int(v) for v in p) for p in re.findall(r"\{\s*([+-]?\d+)\s*,\s*([+-]?\d+)\s*\}", r)] for r in rows]
res = {"run_code": out.count("‹run_code›"), "failed": out.count("The program failed"),
       "jlstz_ok": final("kJlstzKicks") == truth["jlstz"], "i_ok": final("kIKicks") == truth["i"],
       "tokens": (re.findall(r"Tokens: .*", out) or [""])[-1]}
print(json.dumps(res))
