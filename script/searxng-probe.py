#!/usr/bin/env python3
"""Check a SearXNG instance against the assumptions Strument's websearch makes.

Stdlib only. Point it at your instance:

    python3 script/searxng-probe.py http://localhost:8888

Every check prints what it OBSERVED, not just a verdict, because the point is
to falsify the assumptions rather than confirm them. Anything marked FAIL or
SURPRISE is a place the design is wrong and should change.

Public instances are not a substitute: eleven were tried while designing this
and none served JSON. Three returned an HTTP 200 carrying an anti-bot HTML
page, which is why check 2 looks at the content type and not only the status.
"""

import argparse
import json
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

# What Strument will actually send. If your instance's limiter rejects this,
# that is a finding: the tool would fail for every user with the default UA.
STRUMENT_UA = "Strument/0.0.0-dev"
BROWSER_UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36"

results = []


def record(ok, name, detail=""):
    tag = {True: "PASS", False: "FAIL", None: "INFO"}[ok]
    results.append((tag, name))
    print(f"{tag:8} {name}")
    for line in str(detail).splitlines():
        if line.strip():
            print(f"         {line}")


def get(base, params, ua=STRUMENT_UA, method="GET", timeout=60):
    """Returns (status, content_type, body_bytes, seconds, error_or_None)."""
    qs = urllib.parse.urlencode(params)
    started = time.monotonic()
    if method == "GET":
        req = urllib.request.Request(f"{base}/search?{qs}", method="GET")
    else:
        req = urllib.request.Request(base + "/search", data=qs.encode(), method="POST")
        req.add_header("Content-Type", "application/x-www-form-urlencoded")
    req.add_header("User-Agent", ua)
    req.add_header("Accept", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, r.headers.get("Content-Type", ""), r.read(), time.monotonic() - started, None
    except urllib.error.HTTPError as e:
        return e.code, e.headers.get("Content-Type", ""), e.read(), time.monotonic() - started, None
    except Exception as e:  # noqa: BLE001 - a probe reports every failure the same way
        return None, "", b"", time.monotonic() - started, e


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base", help="instance base URL, e.g. http://localhost:8888")
    ap.add_argument("-q", "--query", default="golang http client timeout")
    args = ap.parse_args()
    base = args.base.rstrip("/")
    q = args.query

    print(f"Probing {base}\nQuery: {q!r}\nUser-Agent: {STRUMENT_UA}\n" + "-" * 68)

    # 1. Reachability.
    status, ctype, body, secs, err = get(base, {"q": q, "format": "json"})
    if err is not None:
        record(False, "1. instance is reachable", f"{type(err).__name__}: {err}")
        print("\nNothing else can run. Check the URL and that the instance is up.")
        sys.exit(1)
    record(True, "1. instance is reachable", f"HTTP {status} in {secs:.1f}s")

    # 2. JSON is actually enabled. A 403 means `formats` in settings.yml lacks
    #    json, which is SearXNG's shipped default. A 200 carrying text/html
    #    means an anti-bot interstitial, which looks like success to a client
    #    that only checks the status.
    if status == 403:
        record(False, "2. format=json is enabled",
               "HTTP 403. Add json under search.formats in settings.yml:\n"
               "  search:\n    formats:\n      - html\n      - json")
        sys.exit(1)
    if "json" not in ctype:
        record(False, "2. format=json is enabled",
               f"HTTP {status} with Content-Type: {ctype!r}\n"
               f"First bytes: {body[:120]!r}\n"
               "A 200 with HTML is a bot check, not a result.")
        sys.exit(1)
    record(True, "2. format=json is enabled", f"Content-Type: {ctype}")

    try:
        data = json.loads(body)
    except json.JSONDecodeError as e:
        record(False, "3. body parses as JSON", f"{e}\nFirst bytes: {body[:200]!r}")
        sys.exit(1)
    record(True, "3. body parses as JSON", f"{len(body)} bytes")

    # 4. Top-level shape, read off webutils.get_json_response.
    expected = {"query", "results", "answers", "corrections",
                "infoboxes", "suggestions", "unresponsive_engines"}
    got = set(data)
    record(not (expected - got), "4. top-level keys are as expected",
           f"got:      {sorted(got)}\n"
           f"missing:  {sorted(expected - got) or 'none'}\n"
           f"extra:    {sorted(got - expected) or 'none'}")
    if "number_of_results" in got:
        record(None, "4a. number_of_results IS present",
               "Current master drops it; this instance is older. Do not rely on it.")

    # 5. Results, and the fields worth keeping.
    res = data.get("results") or []
    if not res:
        record(False, "5. the query returned results",
               "Empty. Try another query, or check that engines are enabled.")
    else:
        keys = sorted(res[0])
        want = [k for k in ("url", "title", "content") if k in res[0]]
        record(len(want) == 3, "5. results carry url/title/content",
               f"{len(res)} results; first result keys:\n  {keys}\n"
               f"title:   {str(res[0].get('title'))[:90]!r}\n"
               f"url:     {str(res[0].get('url'))[:90]!r}\n"
               f"content: {str(res[0].get('content'))[:90]!r}")
        missing_content = sum(1 for r in res if not r.get("content"))
        if missing_content:
            record(None, "5a. some results have no snippet",
                   f"{missing_content}/{len(res)} — the renderer must tolerate it.")

    # 6. unresponsive_engines is a list of 2-element ARRAYS, not objects.
    ue = data.get("unresponsive_engines")
    if ue:
        shape = "2-element arrays" if isinstance(ue[0], list) and len(ue[0]) == 2 else type(ue[0]).__name__
        record(isinstance(ue[0], list), "6. unresponsive_engines shape",
               f"{len(ue)} entries, shape: {shape}\n  {json.dumps(ue[:4])}")
    else:
        record(None, "6. unresponsive_engines shape",
               "Empty on this query — every engine answered. Re-run when one is\n"
               "failing to confirm the shape; Strument decodes [name, message].")

    # 7. answers / infoboxes, which are often the whole answer.
    for key in ("answers", "infoboxes", "suggestions", "corrections"):
        v = data.get(key) or []
        if v:
            record(None, f"7. {key} present ({len(v)})", json.dumps(v[0])[:300])

    # 8. Paging uses pageno, not page.
    s2, _, b2, _, _ = get(base, {"q": q, "format": "json", "pageno": 2})
    try:
        page2 = json.loads(b2).get("results") or []
    except Exception:  # noqa: BLE001
        page2 = []
    first_urls = {r.get("url") for r in res}
    overlap = len({r.get("url") for r in page2} & first_urls)
    record(s2 == 200 and bool(page2) and overlap < len(page2),
           "8. pageno=2 returns a different page",
           f"HTTP {s2}, {len(page2)} results, {overlap} shared with page 1")

    # 9. Optional filters Strument may expose later.
    for name, params in (("categories=it", {"categories": "it"}),
                         ("time_range=year", {"time_range": "year"}),
                         ("language=en", {"language": "en"})):
        st, ct, bd, _, _ = get(base, {"q": q, "format": "json", **params})
        ok = st == 200 and "json" in ct
        n = len(json.loads(bd).get("results", [])) if ok else 0
        record(None, f"9. {name}", f"HTTP {st}, {n} results")

    # 10. POST, the form SearXNG documents alongside GET.
    st, ct, _, _, _ = get(base, {"q": q, "format": "json"}, method="POST")
    record(st == 200 and "json" in ct, "10. POST works too", f"HTTP {st}, {ct!r}")

    # 11. The user-agent question. Strument sends its own; if the limiter wants
    #     a browser string, that is a design constraint and not a detail.
    st_s, ct_s, _, _, _ = get(base, {"q": q, "format": "json"}, ua=STRUMENT_UA)
    st_b, _, _, _, _ = get(base, {"q": q, "format": "json"}, ua=BROWSER_UA)
    record(st_s == 200, "11. the Strument user-agent is accepted",
           f"Strument UA: HTTP {st_s}   browser UA: HTTP {st_b}" +
           ("\nThe limiter wants a browser string — worth knowing before shipping."
            if st_s != 200 and st_b == 200 else ""))

    # 12. A query with no results still has to be an answer, not an empty file.
    st, _, bd, _, _ = get(base, {"q": "zzqx" * 9, "format": "json"})
    try:
        empty = json.loads(bd)
        record(st == 200, "12. a no-result query is still valid JSON",
               f"HTTP {st}, {len(empty.get('results') or [])} results, "
               f"{len(empty.get('unresponsive_engines') or [])} engines unresponsive")
    except Exception as e:  # noqa: BLE001
        record(False, "12. a no-result query is still valid JSON", str(e))

    # 13. Timing, which sets the tool's timeout. A search fans out to many
    #     engines and is bounded by the slowest, so this is not a formality.
    times = []
    for _ in range(3):
        _, _, _, s, _ = get(base, {"q": q, "format": "json"})
        times.append(s)
    record(None, "13. how slow is a search",
           f"{', '.join(f'{t:.1f}s' for t in times)} — worst {max(times):.1f}s\n"
           "Strument's timeout should sit well above the worst you see here.")

    print("-" * 68)
    fails = [n for tag, n in results if tag == "FAIL"]
    print(f"{sum(1 for t, _ in results if t == 'PASS')} passed, "
          f"{len(fails)} failed, {sum(1 for t, _ in results if t == 'INFO')} informational")
    for n in fails:
        print(f"  FAILED: {n}")
    sys.exit(1 if fails else 0)


if __name__ == "__main__":
    main()
