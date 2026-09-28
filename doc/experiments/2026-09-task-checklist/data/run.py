import json, os, random, re, shutil, subprocess, sys, time, glob
T = '/tmp/claude-0/task-trial'
BIN = {'base': '/tmp/claude-0/strument-base', 'task': '/tmp/claude-0/strument-task'}
PROMPT = ("A few things for ledger before the 0.4.0 release. First, add a -csv flag: when it is set, "
          "print the entries as CSV instead of the text summary, with a header row date,amount,memo, and quote "
          "any memo that contains a comma. Add tests for the CSV output, including a memo with a comma in it. "
          "Then some smaller things. The page size should be 25 rather than 20, "
          "and amounts should show two decimal places instead of one. Dates written with slashes, like "
          "2024/03/05, should parse too; mention that in the README's Dates section and add a test for it. "
          "sumAll is a poor name, so call it Total everywhere. When a line fails to parse, the error message "
          "should say \"invalid entry\" and include the underlying error instead of the bare \"bad input\". "
          "Add a -version flag that prints the version, and bump the version to 0.4.0. legacyFormat isn't "
          "used by anything, so delete it. Amounts may be written with a leading plus sign, like +1250; make "
          "sure that parses, with a test. The README should document both new flags. Finally, add a 0.4.0 "
          "section at the top of CHANGELOG.md describing these changes.")
EXPECTED = {'main.go', 'parse.go', 'format.go', 'ledger_test.go', 'README.md', 'CHANGELOG.md'}

def read(d, f):
    try: return open(os.path.join(d, f)).read()
    except OSError: return ''

def score(d):
    main, parse, fmt_, test, readme, log = (read(d, f) for f in
        ('main.go', 'parse.go', 'format.go', 'ledger_test.go', 'README.md', 'CHANGELOG.md'))
    gofiles = ''.join(read(d, f) for f in os.listdir(d) if f.endswith('.go'))
    dates = readme.split('## Dates', 1)[1] if '## Dates' in readme else ''
    err_lines = [l for l in main.splitlines() if 'invalid entry' in l]
    top = log.split('## 0.3.0', 1)[0] if '## 0.3.0' in log else log
    items = {
        'page_size': bool(re.search(r'pageSize\s*=\s*25\b', main)),
        'two_decimals': '%.2f' in fmt_,
        'slash_layout': '"2006/01/02"' in parse,
        'readme_dates': bool(re.search(r'\d{4}/\d{2}/\d{2}', dates)),
        'slash_test': bool(re.search(r'\d{4}/\d{2}/\d{2}', test)),
        'rename_total': 'sumAll' not in gofiles and bool(re.search(r'func Total\(', gofiles)),
        'error_message': bool(err_lines) and 'bad input' not in main and any('err' in l.replace('invalid entry', '') for l in err_lines),
        'version_flag': bool(re.search(r'flag\.\w+\(\s*"version"', main)),
        'version_bump': bool(re.search(r'version\s*=\s*"0\.4\.0"', main)),
        'delete_legacy': 'legacyFormat' not in gofiles,
        'changelog': '## 0.4.0' in top and len(re.findall(r'^\s*[-*] ', top, re.M)) >= 3,
        'csv_flag': bool(re.search(r'flag\.\w+\(\s*"csv"', main)),
        'csv_test': bool(re.search(r'(?i)csv', test)) and bool(re.search(r'[A-Za-z], ', test)),
        'plus_test': bool(re.search(r'"[^"\n]*\+\d+', test)),
        'readme_flags': '-csv' in readme and '-version' in readme,
    }
    sample = os.path.join(d, '.sample.txt')
    open(sample, 'w').write('2024-03-05 1250 coffee beans\n2024-03-06 300 lunch, with Bob\n')
    def go_run(*args):
        try:
            r = subprocess.run(['go', 'run', '.', *args], cwd=d, capture_output=True, text=True, timeout=120)
            return r.stdout
        except Exception:
            return ''
    csv_out = go_run('-csv', sample)
    ver_out = go_run('-version')
    os.remove(sample)
    items['csv_header'] = csv_out.lstrip().startswith('date,amount,memo')
    items['csv_quote'] = '"lunch, with Bob"' in csv_out
    items['version_flag'] = items['version_flag'] and '0.4.0' in ver_out
    vet = subprocess.run(['go', 'vet', './...'], cwd=d, capture_output=True).returncode == 0
    tst = subprocess.run(['go', 'test', './...'], cwd=d, capture_output=True).returncode == 0
    st = subprocess.run(['git', 'status', '--porcelain'], cwd=d, capture_output=True, text=True).stdout
    changed = {l[3:].strip() for l in st.splitlines()}
    changed = {f for f in changed if not (f.endswith('.go') and '/' not in f)} | ({f for f in changed if f.endswith('.go') and '/' not in f} & EXPECTED)
    extra = {f for f in changed - EXPECTED if not (f.endswith('.go') and '/' not in f and f.startswith('??') is False)}
    return items, vet, tst, sorted(extra)

def run(rid, arm, model):
    d = f'{T}/runs/{rid}'
    if os.path.exists(f'{d}/done.json'):
        return json.load(open(f'{d}/done.json'))
    shutil.rmtree(d, ignore_errors=True)
    shutil.copytree(f'{T}/world', f'{d}/proj', symlinks=True)
    env = dict(os.environ, XDG_CONFIG_HOME='/tmp/claude-0/nudge-trial/cfg', XDG_STATE_HOME=f'{d}/state')
    t0 = time.time()
    p = subprocess.run([BIN[arm], '--model', model, '--yes', 'steps', '--no-auto-commits', '-m', PROMPT],
                       cwd=f'{d}/proj', env=env, stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=1200)
    open(f'{d}/out.txt', 'w').write(p.stdout + p.stderr)
    seg = glob.glob(f'{d}/state/strument/projects/*/sessions/*/log/*.jsonl')[0]
    shutil.copy(seg, f'{d}/record.jsonl')
    recs = [json.loads(l) for l in open(seg) if l.strip()]
    task_calls = sum(1 for r in recs if r.get('type') == 'message' and r.get('role') == 'assistant'
                     for tc in (r.get('tool_calls') or []) if tc.get('name') == 'task')
    lists = [r['summary'] for r in recs if r.get('type') == 'task']
    last = lists[-1] if lists else ''
    turn = next((r for r in recs if r.get('type') == 'turn'), {})
    items, vet, tst, extra = score(f'{d}/proj')
    res = dict(rid=rid, arm=arm, model=model, exit=p.returncode, secs=round(time.time()-t0),
               done=sum(items.values()), items=items, vet=vet, test=tst, extra_files=extra,
               task_calls=task_calls, task_items=last.count('\n'), task_done=last.count('[x]'),
               task_open=last.count('[ ]') + last.count('[~]'), steps=turn.get('steps'), cost=turn.get('cost'),
               outcome=turn.get('outcome'), answer=(turn.get('answer') or '')[-600:])
    json.dump(res, open(f'{d}/done.json', 'w'))
    return res

if __name__ == '__main__':
    if sys.argv[1] == 'check':
        print(json.dumps(score(f'{T}/world')))
    elif sys.argv[1] == 'pilot':
        for m in ('luna', 'mimo', 'glm', 'ling'):
            for k in range(1):
                r = run(f'pilot-base-{m}-{k}', 'base', m)
                print(json.dumps({x: r[x] for x in ('rid', 'done', 'vet', 'test', 'extra_files', 'steps', 'cost')}), [i for i, v in r['items'].items() if not v], flush=True)
    else:
        n = int(sys.argv[2])
        plan = [(a, m, k) for a in ('base', 'task') for m in ('luna', 'mimo') for k in range(n)]
        random.Random(20261001).shuffle(plan)
        json.dump(plan, open(f'{T}/plan.json', 'w'))
        with open(f'{T}/results.jsonl', 'a') as out:
            for i, (a, m, k) in enumerate(plan):
                r = run(f'{i:02d}-{m}-{a}-{k}', a, m)
                out.write(json.dumps(r) + '\n'); out.flush()
