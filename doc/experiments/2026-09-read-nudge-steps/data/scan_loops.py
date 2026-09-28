import json, glob, sys, collections
files = glob.glob('doc/experiments/**/*.jsonl', recursive=True) + ['/tmp/claude-0/cu/zip/log/20260928T110629.655Z.jsonl']
EXEMPT = {'bash','check','commit','interrupt'}
MUT_OK = ('Applied','Created','Overwrote','Replaced')
turns = []
for f in files:
    try:
        recs = [json.loads(l) for l in open(f) if l.strip()]
    except Exception:
        continue
    if not any(r.get('type') == 'message' and r.get('tool_calls') for r in recs):
        continue
    model = None; cur = None
    results = {}
    for r in recs:
        if r.get('type') == 'message' and r.get('role') == 'tool':
            results[r.get('tool_call_id')] = r.get('text') or r.get('summary') or ''
    def close():
        if cur and cur['steps']: turns.append(cur)
    for r in recs:
        t = r.get('type')
        if t == 'session' and r.get('model') and not model: model = r['model']
        if t == 'request' and r.get('model'): model = r['model']
        if t == 'turn' and r.get('model'): model = r['model']
        if t == 'message' and r.get('role') == 'user' and not (r.get('text') or '').startswith('[strument]'):
            close(); cur = dict(file=f, model=None, steps=0, calls=0, cs=0, ss=0, maxc=0, maxs=0, nudged=False, prompt=(r.get('text') or '')[:60])
        if cur is None: continue
        if t == 'message' and r.get('role') == 'user' and (r.get('text') or '').startswith('[strument]') and 'read-only tool calls' in r.get('text',''):
            cur['nudged'] = True
        if t == 'message' and r.get('role') == 'assistant' and r.get('tool_calls'):
            cur['steps'] += 1; cur['model'] = model
            reads_this_step = 0; mutated = False
            for tc in r['tool_calls']:
                name = tc.get('name'); cur['calls'] += 1
                res = results.get(tc.get('id'), '')
                if name in ('edit','write'):
                    if res.startswith(MUT_OK): mutated = True
                    continue
                if name in ('bash','commit'):
                    mutated = True; continue
                if name in EXEMPT: continue
                reads_this_step += 1
            if mutated:
                cur['cs'] = cur['ss'] = 0
            else:
                cur['cs'] += reads_this_step
                if reads_this_step: cur['ss'] += 1
            cur['maxc'] = max(cur['maxc'], cur['cs']); cur['maxs'] = max(cur['maxs'], cur['ss'])
    close()
by = collections.defaultdict(list)
for t in turns: by[(t['model'] or '?').split('/')[-1]].append(t)
print(f"{'model':28} turns  calls/step  calls>=20  calls>=40  steps>=10  steps>=20  nudged")
for m, ts in sorted(by.items(), key=lambda x: -len(x[1])):
    cps = sum(t['calls'] for t in ts)/max(1,sum(t['steps'] for t in ts))
    print(f"{m:28} {len(ts):5}  {cps:10.2f}  {sum(t['maxc']>=20 for t in ts):9}  {sum(t['maxc']>=40 for t in ts):9}  {sum(t['maxs']>=10 for t in ts):9}  {sum(t['maxs']>=20 for t in ts):9}  {sum(t['nudged'] for t in ts):6}")
json.dump(turns, open('/tmp/claude-0/loop_turns.json','w'))
