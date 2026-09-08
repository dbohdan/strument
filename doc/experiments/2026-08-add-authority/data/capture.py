"""A recording stand-in for the provider.

Verifying a variant by reading its diff is exactly the mistake the scorer bug
taught: an instrument that looks right and is not. This captures the bytes the
binary actually sends, then returns a minimal SSE reply so the run completes.
"""

import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

CAPTURED = []


class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(n) or b"{}")
        CAPTURED.append(body)
        with open(sys.argv[2], "w") as fh:
            json.dump(CAPTURED, fh, indent=1)

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        for chunk in (
            {"choices": [{"delta": {"content": "done"}, "index": 0}]},
            {"choices": [{"delta": {}, "finish_reason": "stop", "index": 0}]},
        ):
            self.wfile.write(b"data: " + json.dumps(chunk).encode() + b"\n\n")
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()


HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
