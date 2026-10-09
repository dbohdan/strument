# Logging proxy for OpenRouter: forwards POSTs, streams the response back, and
# appends one JSON line per request to argv[2]: marker positions and the usage
# the stream reported. Never logs headers.  usage: proxy.py <port> <log.jsonl>
import http.server, json, os, ssl, sys, urllib.request
port, logpath = int(sys.argv[1]), sys.argv[2]
ctx = ssl.create_default_context(cafile=os.environ.get("SSL_CERT_FILE") or "/root/.ccr/ca-bundle.crt")
opener = urllib.request.build_opener(urllib.request.ProxyHandler(), urllib.request.HTTPSHandler(context=ctx))
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        req = json.loads(body)
        marks = []
        for i, m in enumerate(req.get("messages", [])):
            c = m.get("content")
            if isinstance(c, list) and any(isinstance(p, dict) and "cache_control" in p for p in c):
                marks.append(f"{i}:{m['role']}")
        fwd = urllib.request.Request("https://openrouter.ai" + self.path, data=body, method="POST")
        for k in ("Authorization", "Content-Type", "HTTP-Referer", "X-Title", "User-Agent", "X-Session-Id"):
            if self.headers.get(k): fwd.add_header(k, self.headers[k])
        usage, provider = None, None
        try:
            resp = opener.open(fwd, timeout=600)
            self.send_response(resp.status)
            self.send_header("Content-Type", resp.headers.get("Content-Type", "text/event-stream")); self.end_headers()
            for line in resp:
                self.wfile.write(line); self.wfile.flush()
                if line.startswith(b"data: {"):
                    try:
                        d = json.loads(line[6:])
                        provider = d.get("provider", provider)
                        if d.get("usage"): usage = d["usage"]
                    except Exception: pass
        except urllib.error.HTTPError as e:
            data = e.read(); self.send_response(e.code); self.end_headers(); self.wfile.write(data)
        with open(logpath, "a") as f:
            f.write(json.dumps({"model": req.get("model"), "n_messages": len(req.get("messages", [])), "marks": marks, "provider": provider, "usage": usage}) + "\n")
    def log_message(self, *a): pass
http.server.ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()
