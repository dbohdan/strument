#!/usr/bin/env python3
"""Layer 4, post hoc: how much of Terra's draft survives into MiMo's page?

    survival.py DIR

For each consult session with a page: the share of the page's 8-word
sequences found in Terra's answer (and of its lines, and the reverse), set
against the same share for the task's `alone` pages, which never saw the
draft. Then where in the turn the consult came and when the target was
written, and the rank correlation between survival and the judge's defects.
"""
import sys,glob,json,os,re,statistics as st
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import mech_score as MS
T4=sys.argv[1]
def words(t): return re.findall(r"[\w`'.-]+",t.lower())
def grams(t,n=8):
    w=words(t); return {tuple(w[i:i+n]) for i in range(len(w)-n+1)}
def lines(t): return {re.sub(r"\s+"," ",l.strip()) for l in t.splitlines() if len(l.strip())>20}
def cont(page,draft):
    g=grams(page); return len(g & grams(draft))/len(g) if g else 0
def lcont(page,draft):
    L=lines(page); return len(L & lines(draft))/len(L) if L else 0
sess={}
for d in glob.glob(T4+"/sessions/*/done.json"):
    j=json.load(open(d)); sess[j["id"]]=j
rows=[]
for i,j in sorted(sess.items()):
    if j["arm"]!="consult" or not j["target_text"]: continue
    calls,_=MS.consults(os.path.dirname(os.path.join(T4,"sessions",i,"x"))+"/wire.jsonl")
    draft="\n".join(MS.answer_text(r) for _,r in calls)
    page=j["target_text"]
    alones=[s["target_text"] for s in sess.values() if s["task"]==j["task"] and s["arm"]=="alone" and s["target_text"]]
    base=st.mean(cont(a,draft) for a in alones) if alones else None
    rows.append(dict(id=i,page=len(words(page)),draft=len(words(draft)),g=cont(page,draft),l=lcont(page,draft),
        rev=cont(draft,page),base=base))
for r in rows: print(f"{r['id']:28} page {r['page']:5} draft {r['draft']:5} page-in-draft {r['g']:.2f} lines {r['l']:.2f} draft-in-page {r['rev']:.2f} alone-baseline {r['base']:.2f}")
for k in ("g","l","rev","base"): print(k,"median",round(st.median(r[k] for r in rows),2))
print("median page/draft words",st.median(r['page'] for r in rows),st.median(r['draft'] for r in rows))

# order of tool calls in each consult session: first consult step vs first write to target, and edits after
import tasks as T, defects as D, collections
T.use("long")
def steps(wire):
    last=None
    for line in open(wire):
        r=json.loads(line)
        if r["kind"]!="raw_request": continue
        b=json.loads(r["body"])
        if b["model"]!=MS.TERRA: last=b
    seq=[]
    for m in last["messages"]:
        for tc in m.get("tool_calls") or []:
            seq.append((tc["function"]["name"], tc["function"]["arguments"][:200]))
    return seq
dc=collections.defaultdict(list)
for f in glob.glob(T4+"/defects/*.json"):
    r=json.load(open(f)); x=D.parse(r)
    if x is not None: dc[(r["task"],r["writer"],r["n"])].append(len(x))
mean={k:sum(v)/len(v) for k,v in dc.items()}
print()
for r in rows:
    j=sess[r["id"]]; tgt=T.LONG_TASKS[j["task"]]["target"]
    seq=steps(os.path.join(T4,"sessions",r["id"],"wire.jsonl"))
    names=[n for n,_ in seq]
    ci=names.index("consult")
    wi=[k for k,(n,a) in enumerate(seq) if tgt in a and n in ("write","edit","create","run_code","bash")]
    r["before"]=sum(1 for k in wi if k<ci); r["after"]=sum(1 for k in wi if k>ci)
    r["pos"]=ci; r["n"]=len(seq)
    r["def"]=mean.get((j["task"],"consult",j["rep"])); r["alone"]=mean.get((j["task"],"alone",j["rep"]))
    print(f"{r['id']:28} consult at step {ci}/{len(seq)} target writes before {r['before']} after {r['after']} kept {r['g']:.2f} defects {r['def']} alone {r['alone']}")
def rank(x):
    s=sorted(range(len(x)),key=lambda i:x[i]); rk=[0]*len(x)
    for p,i in enumerate(s): rk[i]=p
    return rk
pr=[r for r in rows if r["def"] is not None]
a,b=rank([r["g"] for r in pr]),rank([r["def"] for r in pr])
ma,mb=st.mean(a),st.mean(b)
rho=sum((x-ma)*(y-mb) for x,y in zip(a,b))/ (sum((x-ma)**2 for x in a)*sum((y-mb)**2 for y in b))**.5
print("spearman kept vs defects", round(rho,2), "n",len(pr))
hi=[r for r in pr if r["g"]>=0.3]; lo=[r for r in pr if r["g"]<0.3]
print("kept>=0.3 n",len(hi),"mean defects",st.mean(r['def'] for r in hi), "paired alone", [r['alone'] for r in hi])
print("kept<0.3 n",len(lo),"mean defects",round(st.mean(r['def'] for r in lo),2))
