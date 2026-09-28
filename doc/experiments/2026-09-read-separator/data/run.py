import json, os, random, re, shutil, subprocess, sys, time, glob
T = '/tmp/claude-0/sep-trial'
BIN = {'tab': '/tmp/claude-0/strument-tab', 'arrow': '/tmp/claude-0/strument-arrow'}
PROMPT = """First read deep.go and page.html with the read tool. Then make each change below with its own edit call. In each old_string, include the whole line being changed and the line above it, with their indentation exactly as in the file.

1. deep.go: the "too long" string becomes "message too long".
2. deep.go: the " joined" string becomes " has joined".
3. deep.go: the "unknown event" string becomes "unknown event kind".
4. deep.go: the condition `if part.Text != "" {` becomes `if part.Text != "" && ev.User != "bot" {`.
5. page.html: the nick alice becomes bob.
6. page.html: the text "hello there" becomes "hello, world".
7. page.html: the footer text "end of group" becomes "end".
8. page.html: the header element gets id="h1" after its class.

Do not commit, and do not change anything else."""

def run(rid, arm, model):
    d = f'{T}/runs/{rid}'
    if os.path.exists(f'{d}/done.json'):
        return json.load(open(f'{d}/done.json'))
    shutil.rmtree(d, ignore_errors=True)
    shutil.copytree(f'{T}/world', f'{d}/proj', symlinks=True)
    env = dict(os.environ, XDG_CONFIG_HOME='/tmp/claude-0/nudge-trial/cfg', XDG_STATE_HOME=f'{d}/state')
    t0 = time.time()
    p = subprocess.run([BIN[arm], '--model', model, '--yes', 'steps', '--no-auto-commits', '-m', PROMPT],
                       cwd=f'{d}/proj', env=env, stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=900)
    open(f'{d}/out.txt', 'w').write(p.stdout + p.stderr)
    seg = glob.glob(f'{d}/state/strument/projects/*/sessions/*/log/*.jsonl')[0]
    shutil.copy(seg, f'{d}/record.jsonl')
    recs = [json.loads(l) for l in open(seg) if l.strip()]
    edits = [r for r in recs if r.get('type') == 'edit']
    offsets = []
    for r in edits:
        m = re.search(r'has (\d+) tabs?, and you sent (\d+) tabs?', r.get('summary') or '')
        if m: offsets.append(int(m.group(2)) - int(m.group(1)))
    reads = sum(1 for r in recs if r.get('type') == 'message' and r.get('role') == 'assistant'
                for tc in (r.get('tool_calls') or []) if tc.get('name') == 'read')
    diff = subprocess.run(['git', 'diff', '--stat'], cwd=f'{d}/proj', capture_output=True, text=True).stdout.strip().splitlines()
    res = dict(rid=rid, arm=arm, model=model, exit=p.returncode, secs=round(time.time()-t0), reads=reads,
               edits=len(edits), exact=sum(r['outcome'] == 'exact' for r in edits),
               loose=sum(r['outcome'] == 'loose' for r in edits),
               failed=sum(r['outcome'] in ('not_found', 'ambiguous') for r in edits),
               offsets=offsets, diffstat=diff[-1] if diff else '')
    json.dump(res, open(f'{d}/done.json', 'w'))
    return res

if __name__ == '__main__':
    if sys.argv[1] == 'pilot':
        for arm in ('tab', 'arrow'):
            print(json.dumps(run(f'pilot-{arm}', arm, 'luna')), flush=True)
    else:
        plan = [(a, m, k) for a in ('tab', 'arrow') for m in ('luna', 'mimo') for k in range(5)]
        random.Random(20260929).shuffle(plan)
        json.dump(plan, open(f'{T}/plan.json', 'w'))
        with open(f'{T}/results.jsonl', 'a') as out:
            for i, (a, m, k) in enumerate(plan):
                r = run(f'{i:02d}-{m}-{a}-{k}', a, m)
                out.write(json.dumps(r) + '\n'); out.flush()
                print(json.dumps(r), flush=True)
