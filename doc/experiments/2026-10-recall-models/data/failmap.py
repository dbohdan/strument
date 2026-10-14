import re,glob,json,sys
S='/tmp/claude-0/-home-user-strument/81348424-3872-590a-840d-1e776b0797d9/scratchpad'
ANSI=re.compile(rb'\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07]*\x07|\r')
R=re.compile(rb"(?:(?:request|rate_limit): HTTP \d{3}|network: )[^\n]*\n(?!Retrying)")
for st in sys.argv[1:]:
    jobs={j['id']:j['mode'] for j in json.load(open(f'{S}/recall/models-{st}/jobs.json'))}
    print('==',st)
    for d in sorted(glob.glob(f'{S}/recall/models-{st}/s*/transcript.raw')):
        sid=d.split('/')[-2]
        t=ANSI.sub(b'',open(d,'rb').read())
        segs=re.split(rb'Tokens: [^\n]*\n',t)
        out=[]
        for i,sg in enumerate(segs):
            for m in R.finditer(sg):
                line=sg[sg.rfind(b'\n',0,m.start())+1:m.end()]
                if sg[m.end():].startswith(b'Could not summarize'): continue
                kind='sum' if b'Could not summarize' in line else 'cut'
                if (i,kind) not in out: out.append((i,kind))
        if out: print('  %s %-20s %s' % (sid, jobs[sid], ' '.join('%d%s'%x for x in out)))
