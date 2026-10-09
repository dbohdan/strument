import re,sys,glob,os
LS=sys.argv[1]
def num(s):
    v=float(s[:-1])*1000 if s.endswith('k') else float(s); return v
rows={}
for f in sorted(glob.glob(f"{LS}/out-*-*-*.txt")):
    arm,model,turn=os.path.basename(f)[4:-4].split('-')
    t=open(f,errors='replace').read()
    m=re.findall(r"^Tokens: ([\d.]+k?) sent(?: \(([^)]*)\))?.*?Cost: \$([\d.]+) turn.*?(\d+) steps",t,re.M)
    if not m: continue
    sent,par,cost,steps=m[-1]
    hit=re.search(r"([\d.]+k?) cache hit",par or ""); wr=re.search(r"([\d.]+k?) cache write",par or "")
    rows.setdefault((arm,model),[]).append((int(turn),num(sent),num(hit.group(1)) if hit else 0,num(wr.group(1)) if wr else 0,float(cost),len(re.findall("Chat history compacted",t))))
for (arm,model),r in sorted(rows.items()):
    r.sort(); S=sum(x[1] for x in r); H=sum(x[2] for x in r); C=sum(x[4] for x in r); K=sum(x[5] for x in r)
    per=" ".join(f"{x[2]/x[1]*100:.0f}%" for x in r)
    print(f"{arm:5} {model:5} turns={len(r)} sent={S/1000:.0f}k hit={H/S*100:.1f}% cost=${C:.4f} compactions={K} per-turn hit: {per}")
