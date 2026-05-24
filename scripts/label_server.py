#!/usr/bin/env python3
"""Tiny HTTP server backing scripts/label.html. Serves the UI plus the
examples JSON, and persists every A/B/skip click to a JSONL file so
partial labeling survives an early exit or browser crash.

Run:
    python3 scripts/label_server.py
then open http://localhost:8765/ in a browser.

Files:
    scripts/label.html              UI (served at /)
    data/label-examples.json        examples (served at /examples.json)
    data/label-decisions.jsonl      one decision per line (POST /save appends)

The JSONL format is intentionally append-only with no rewrites — every
click flushes one line to disk, fsync'd. If the labeler quits or the
machine crashes, every click already in the file is preserved. To
replay or analyse, just `cat data/label-decisions.jsonl`.
"""

from __future__ import annotations

import json
import os
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
HTML_PATH = os.path.join(ROOT, "scripts", "label.html")
EXAMPLES_PATH = os.path.join(ROOT, "data", "label-examples.json")
DECISIONS_PATH = os.path.join(ROOT, "data", "label-decisions.jsonl")

HOST = "127.0.0.1"
PORT = 8765

# Single mutex around the decisions file — writes are tiny but two
# concurrent POSTs would otherwise interleave bytes mid-line.
DECISIONS_LOCK = threading.Lock()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, format: str, *args: object) -> None:  # noqa: A002 — base signature uses "format"
        # Quieter logs — one line per request, no extra noise.
        sys.stderr.write("[%s] %s\n" % (self.address_string(), format % args))

    def _send(self, status: int, body: bytes, ctype: str = "text/plain; charset=utf-8") -> None:
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:
        path = self.path.split("?", 1)[0]
        if path == "/" or path == "/label.html":
            return self._send_file(HTML_PATH, "text/html; charset=utf-8")
        if path == "/examples.json":
            return self._send_file(EXAMPLES_PATH, "application/json; charset=utf-8")
        if path == "/decisions.jsonl":
            return self._send_file(DECISIONS_PATH, "application/jsonl; charset=utf-8", allow_missing=True)
        self._send(404, b"not found\n")

    def _send_file(self, path: str, ctype: str, allow_missing: bool = False) -> None:
        try:
            with open(path, "rb") as f:
                body = f.read()
        except FileNotFoundError:
            if allow_missing:
                return self._send(200, b"", ctype)
            return self._send(404, f"missing: {path}\n".encode())
        self._send(200, body, ctype)

    def do_POST(self) -> None:
        if self.path != "/save":
            return self._send(404, b"not found\n")
        length = int(self.headers.get("Content-Length") or 0)
        if length <= 0 or length > 64 * 1024:
            return self._send(400, b"bad length\n")
        raw = self.rfile.read(length)
        try:
            decision = json.loads(raw.decode("utf-8"))
        except Exception as e:
            return self._send(400, f"bad json: {e}\n".encode())
        if not isinstance(decision, dict) or "id" not in decision or "choice" not in decision:
            return self._send(400, b"need {id, choice}\n")

        # Append one line, fsync to disk so a crash doesn't lose the
        # most recent clicks. JSONL with one decision per line.
        line = json.dumps(decision, ensure_ascii=False, separators=(",", ":")) + "\n"
        with DECISIONS_LOCK:
            os.makedirs(os.path.dirname(DECISIONS_PATH), exist_ok=True)
            with open(DECISIONS_PATH, "a", encoding="utf-8") as f:
                f.write(line)
                f.flush()
                os.fsync(f.fileno())
        self._send(200, b'{"ok":true}\n', "application/json")


def main() -> None:
    if not os.path.exists(EXAMPLES_PATH):
        print(f"WARN: {EXAMPLES_PATH} missing — run `go run ./cmd/label-gen {EXAMPLES_PATH}` first.", file=sys.stderr)
    server = ThreadingHTTPServer((HOST, PORT), Handler)
    print(f"labeling UI:  http://{HOST}:{PORT}/", file=sys.stderr)
    print(f"decisions log: {DECISIONS_PATH}", file=sys.stderr)
    print("Ctrl-C to stop.", file=sys.stderr)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nstopped.", file=sys.stderr)


if __name__ == "__main__":
    main()
