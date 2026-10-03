"""Tests for the SP0 recording proxy (rec.py).

A fake upstream (http.server in a thread) stands in for the magpie
gateway; the tests drive the recorder exactly the way the plan's review
focus demands: streaming must reach the client before the upstream
finishes, headers must be logged lower-cased, escaped Codex metadata
must be kept verbatim, and the probe endpoints must not be forwarded.
"""

import http.client
import json
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import rec

UPSTREAM_SEEN = []


class UpstreamHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def _ok(self, body=b"{}"):
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def do_GET(self):
        UPSTREAM_SEEN.append(("GET", self.path))
        if self.path == "/slowstream":
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Connection", "close")
            self.close_connection = True
            self.end_headers()
            self.wfile.write(b"chunk1 ")
            self.wfile.flush()
            time.sleep(1.0)
            self.wfile.write(b"chunk2")
            self.wfile.flush()
            return
        self._ok()

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        if n:
            self.rfile.read(n)
        UPSTREAM_SEEN.append(("POST", self.path))
        self._ok()


class Rig(unittest.TestCase):
    def setUp(self):
        UPSTREAM_SEEN.clear()
        self.upstream = ThreadingHTTPServer(("127.0.0.1", 0), UpstreamHandler)
        threading.Thread(target=self.upstream.serve_forever, daemon=True).start()
        import tempfile

        fd, self.logpath = tempfile.mkstemp(suffix=".jsonl")
        import os

        os.close(fd)
        os.unlink(self.logpath)
        host, port = self.upstream.server_address
        self.server = rec.make_server(
            "127.0.0.1", 0, f"http://127.0.0.1:{port}", self.logpath
        )
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.port = self.server.server_address[1]

    def tearDown(self):
        self.server.shutdown()
        self.upstream.shutdown()

    # -- helpers ---------------------------------------------------------

    def connect(self):
        conn = http.client.HTTPConnection("127.0.0.1", self.port, timeout=10)
        self.addCleanup(conn.close)
        return conn

    def wait_for_line(self, pred, timeout=5.0):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                with open(self.logpath, encoding="utf-8") as f:
                    for line in f:
                        obj = json.loads(line)
                        if pred(obj):
                            return obj
            except FileNotFoundError:
                pass
            time.sleep(0.02)
        self.fail("expected log line did not appear")

    # -- the six tests ---------------------------------------------------

    def test_logs_session_headers_lowercased(self):
        conn = self.connect()
        body = json.dumps(
            {"model": "group/queqiao", "messages": [{"role": "user", "content": "hi"}]}
        )
        conn.request(
            "POST",
            "/v1/messages",
            body=body,
            headers={
                "Content-Type": "application/json",
                "X-Claude-Code-Session-Id": "S1",
            },
        )
        resp = conn.getresponse()
        resp.read()
        line = self.wait_for_line(
            lambda o: o.get("kind") == "req" and o.get("path") == "/v1/messages"
        )
        self.assertEqual(line["headers"]["x-claude-code-session-id"], "S1")
        self.assertEqual(line["model"], "group/queqiao")

    def test_streams_before_upstream_finishes(self):
        conn = self.connect()
        conn.request("GET", "/slowstream")
        resp = conn.getresponse()
        t0 = time.monotonic()
        first = resp.read(7)
        dt = time.monotonic() - t0
        self.assertEqual(first, b"chunk1 ")
        self.assertLess(dt, 0.5, "first chunk must arrive before upstream finishes")
        rest = resp.read()
        self.assertEqual(rest, b"chunk2")

    def test_counts_anthropic_tool_errors(self):
        conn = self.connect()
        blocks = [
            {"type": "tool_result", "tool_use_id": "1", "content": "a"},
            {
                "type": "tool_result",
                "tool_use_id": "2",
                "content": "boom",
                "is_error": True,
            },
            {"type": "tool_result", "tool_use_id": "3", "content": "c"},
        ]
        body = json.dumps(
            {
                "model": "group/qq-fast",
                "messages": [{"role": "user", "content": blocks}],
            }
        )
        conn.request(
            "POST",
            "/v1/messages",
            body=body,
            headers={"Content-Type": "application/json"},
        )
        resp = conn.getresponse()
        resp.read()
        line = self.wait_for_line(
            lambda o: o.get("kind") == "req" and o.get("path") == "/v1/messages"
        )
        self.assertEqual(line["tool_results"], 3)
        self.assertEqual(line["tool_errors"], 1)

    def test_keeps_escaped_codex_metadata(self):
        conn = self.connect()
        raw = (
            b'{"model":"gpt-5.6","input":"hi","client_metadata":'
            b'{"x-codex-turn-metadata":"{\\"turn_id\\":\\"t\\u0031\\"}"}}'
        )
        conn.request(
            "POST",
            "/v1/responses",
            body=raw,
            headers={"Content-Type": "application/json"},
        )
        resp = conn.getresponse()
        resp.read()
        line = self.wait_for_line(
            lambda o: o.get("kind") == "req" and o.get("path") == "/v1/responses"
        )
        self.assertEqual(
            line["client_metadata"],
            '{"x-codex-turn-metadata":"{\\"turn_id\\":\\"t\\u0031\\"}"}',
        )

    def test_probe_log_not_forwarded(self):
        conn = self.connect()
        body = json.dumps({"probe": "cc", "event": "turn.step", "v": 1})
        conn.request(
            "POST",
            "/spike/log",
            body=body,
            headers={"Content-Type": "application/json"},
        )
        resp = conn.getresponse()
        resp.read()
        self.assertEqual(resp.status, 204)
        time.sleep(0.1)
        self.assertEqual(UPSTREAM_SEEN, [])
        line = self.wait_for_line(lambda o: o.get("kind") == "probe")
        self.assertEqual(line["event"], "turn.step")
        self.assertEqual(line["probe"], "cc")

    def test_slow_endpoint(self):
        conn = self.connect()
        t0 = time.monotonic()
        conn.request("GET", "/spike/slow?ms=300")
        resp = conn.getresponse()
        body = resp.read()
        dt = time.monotonic() - t0
        self.assertEqual(resp.status, 200)
        self.assertEqual(body, b"{}")
        self.assertGreaterEqual(dt, 0.3)


if __name__ == "__main__":
    unittest.main()
