import json, os, random, shutil, subprocess, sys, time, glob
T = '/tmp/claude-0/nudge-trial'
SCR = '/tmp/claude-0/-home-user-strument/81348424-3872-590a-840d-1e776b0797d9/scratchpad'
BIN = {'calls': '/tmp/claude-0/strument-calls', 'steps': '/tmp/claude-0/strument-steps'}
PROMPT = 'Hi! Please get yourself familiar with this repo.'

def run(rid, arm, model, world):
    d = f'{T}/runs/{rid}'
    if os.path.exists(f'{d}/done.json'):
        return json.load(open(f'{d}/done.json'))
    shutil.rmtree(d, ignore_errors=True)
    shutil.copytree(f'{T}/worlds/{world}', f'{d}/proj', symlinks=True)
    env = dict(os.environ, XDG_CONFIG_HOME=f'{T}/cfg', XDG_STATE_HOME=f'{d}/state')
    t0 = time.time()
    p = subprocess.run([BIN[arm], '--model', model, '--yes', 'steps', '-m', PROMPT],
                       cwd=f'{d}/proj', env=env, stdin=subprocess.DEVNULL,
                       capture_output=True, text=True, timeout=900)
    open(f'{d}/out.txt', 'w').write(p.stdout + p.stderr)
    seg = glob.glob(f'{d}/state/strument/projects/*/sessions/*/log/*.jsonl')[0]
    shutil.copy(seg, f'{d}/record.jsonl')
    recs = [json.loads(l) for l in open(seg) if l.strip()]
    steps = calls = 0; files = set(); nudges = 0; turn = {}
    for r in recs:
        if r.get('type') == 'message' and r.get('role') == 'assistant' and r.get('tool_calls'):
            steps += 1
            for tc in r['tool_calls']:
                calls += 1
                if tc.get('name') == 'read':
                    try: files.add(json.loads(tc.get('arguments') or '{}').get('path'))
                    except Exception: pass
        if r.get('type') == 'message' and r.get('role') == 'user' and 'read-only tool calls' in (r.get('text') or ''):
            nudges += 1
        if r.get('type') == 'turn': turn = r
    res = dict(rid=rid, arm=arm, model=model, world=world, exit=p.returncode, secs=round(time.time()-t0),
               steps=steps, calls=calls, files=len(files), nudged=nudges, outcome=turn.get('outcome'),
               answer_chars=len(turn.get('answer') or ''), cost=turn.get('cost'))
    json.dump(res, open(f'{d}/done.json', 'w'))
    return res

if __name__ == '__main__':
    mode = sys.argv[1]
    if mode == 'pilot':
        for arm in ('calls', 'steps'):
            print(json.dumps(run(f'pilot-{arm}', arm, 'luna', 'catchup')))
    else:
        plan = [(a, m, w, k) for a in ('calls', 'steps') for m in ('luna', 'mimo')
                for w in ('catchup', 'larkspur', 'strument') for k in range(3)]
        random.Random(20260928).shuffle(plan)
        json.dump(plan, open(f'{T}/plan.json', 'w'))
        with open(f'{T}/results.jsonl', 'a') as out:
            for i, (a, m, w, k) in enumerate(plan):
                r = run(f'{i:02d}-{m}-{a}-{w}-{k}', a, m, w)
                out.write(json.dumps(r) + '\n'); out.flush()
                print(json.dumps(r), flush=True)
