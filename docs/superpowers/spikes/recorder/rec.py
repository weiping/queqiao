#!/usr/bin/env python3
"""SP0 recording proxy.

Sits between an agent and the upstream magpie gateway and logs one JSON
object per line:

  python3 rec.py --listen 127.0.0.1:3500 --to http://127.0.0.1:3425 \
      --log logs/rig.jsonl

Proxied requests append {"kind":"req", ...}; POST /spike/log (never
forwarded) appends {"kind":"probe", ...} and answers 204; GET
/spike/slow?ms=N sleeps N ms and answers 200 {}.  Response bodies are
streamed through in 4 KiB reads with a flush after each read, never
decoded, so SSE timing measurements stay honest.
"""

import argparse
import hashlib
import http.client
import json
import re
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlsplit

# Header names logged (lower-cased) on every proxied request.
HEADER_KEYS = (
    "x-claude-code-session-id",
    "session_id",
    "x-session-id",
    "x-magpie-session",
    "x-codex-turn-metadata",
    "user-agent",
)

# Request headers dropped before forwarding (http.client adds its own).
HOP_HEADERS = {
    "connection",
    "keep-alive",
    "proxy-authenticate",
    "proxy-authorization",
    "te",
    "trailer",
    "transfer-encoding",
    "upgrade",
    "content-length",
    "host",
}

_log_lock = threading.Lock()


def append_log(path, obj):
    with _log_lock:
        with open(path, "a", encoding="utf-8") as f:
            f.write(json.dumps(obj, ensure_ascii=False) + "\n")


def raw_object(text, key):
    """Raw JSON text of object field `key`, escapes undecoded.

    Codex may send x-codex-turn-metadata as ASCII-escaped JSON
    (openai/codex#19620); re-serialising a parsed copy would turn
    \\u0031 into 1, so the substring is lifted verbatim instead.
    """
    marker = f'"{key}"'
    start = 0
    while True:
        i = text.find(marker, start)
        if i == -1:
            return None
        j = i + len(marker)
        while j < len(text) and text[j].isspace():
            j += 1
        if j < len(text) and text[j] == ":":
            j += 1
            while j < len(text) and text[j].isspace():
                j += 1
            if j < len(text) and text[j] == "{":
                depth, in_str, esc = 0, False, False
                for k in range(j, len(text)):
                    c = text[k]
                    if in_str:
                        if esc:
                            esc = False
                        elif c == "\\":
                            esc = True
                        elif c == '"':
                            in_str = False
                    else:
                        if c == '"':
                            in_str = True
                        elif c == "{":
                            depth += 1
                        elif c == "}":
                            depth -= 1
                            if depth == 0:
                                return text[j : k + 1]
                return None
        start = i + 1


def _content_text(content, block_type):
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = [
            b.get("text", "")
            for b in content
            if isinstance(b, dict) and b.get("type") == block_type
        ]
        if parts:
            return "\n".join(parts)
    return None


def first_user_text(obj):
    """Text of the conversation's first user message (both protocols)."""
    for m in obj.get("messages") or []:
        if not isinstance(m, dict) or m.get("role") != "user":
            continue
        t = _content_text(m.get("content"), "text")
        if t:
            return t
    inp = obj.get("input")
    if isinstance(inp, str):
        return inp
    if isinstance(inp, list):
        for it in inp:
            if not isinstance(it, dict) or it.get("role") != "user":
                continue
            t = _content_text(it.get("content"), "input_text")
            if t:
                return t
    return None


_EXIT_CODE = re.compile(r"^Exit code: (\d+)")


def tool_output_failed(text):
    m = _EXIT_CODE.match(text)
    return bool(m) and int(m.group(1)) != 0


def tool_counts(obj):
    """(tool_results, tool_errors) across Anthropic and Responses bodies."""
    results = errors = 0
    for m in obj.get("messages") or []:
        c = m.get("content") if isinstance(m, dict) else None
        if not isinstance(c, list):
            continue
        for b in c:
            if isinstance(b, dict) and b.get("type") == "tool_result":
                results += 1
                if b.get("is_error") is True:
                    errors += 1
    items = obj.get("input")
    if isinstance(items, list):
        for it in items:
            if not isinstance(it, dict) or it.get("type") != "function_call_output":
                continue
            results += 1
            out = it.get("output")
            if isinstance(out, dict):
                out = out.get("content") or out.get("output")
            if isinstance(out, str) and tool_output_failed(out):
                errors += 1
    return results, errors


class SpikeHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    upstream = None  # (host, port)
    logpath = None

    def log_message(self, *args):
        pass

    def _reply(self, status, body=b"{}", ctype="application/json"):
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if body:
            self.wfile.write(body)

    def do_GET(self):
        self.route()

    def do_POST(self):
        self.route()

    def do_PUT(self):
        self.route()

    def do_PATCH(self):
        self.route()

    def do_DELETE(self):
        self.route()

    def route(self):
        parts = urlsplit(self.path)
        if self.command == "POST" and parts.path == "/spike/log":
            return self.handle_probe()
        if self.command == "GET" and parts.path == "/spike/slow":
            return self.handle_slow(parts.query)
        return self.handle_proxy()

    def handle_probe(self):
        n = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(n) if n else b""
        try:
            body = json.loads(raw)
        except (ValueError, UnicodeDecodeError):
            body = {"unparsed": raw.decode("utf-8", "replace")}
        if not isinstance(body, dict):
            body = {"body": body}
        append_log(self.logpath, {"kind": "probe", "ts": time.time(), **body})
        self.send_response(204)
        self.end_headers()

    def handle_slow(self, query):
        try:
            ms = int(parse_qs(query).get("ms", ["0"])[0])
        except ValueError:
            ms = 0
        time.sleep(max(0, ms) / 1000)
        self._reply(200)

    def handle_proxy(self):
        n = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(n) if n else b""
        t0 = time.monotonic()
        host, port = self.upstream
        conn = http.client.HTTPConnection(host, port, timeout=600)
        headers = {
            k: v for k, v in self.headers.items() if k.lower() not in HOP_HEADERS
        }
        sent = False
        status = 502
        try:
            conn.request(
                self.command, self.path, body=raw if raw else None, headers=headers
            )
            resp = conn.getresponse()
            status = resp.status
            self.send_response(resp.status, resp.reason)
            for k, v in resp.getheaders():
                if k.lower() in (
                    "transfer-encoding",
                    "content-length",
                    "connection",
                    "keep-alive",
                ):
                    continue
                self.send_header(k, v)
            self.send_header("Connection", "close")
            self.close_connection = True
            self.end_headers()
            sent = True
            while True:
                chunk = resp.read1(4096)
                if not chunk:
                    break
                self.wfile.write(chunk)
                self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass  # agent hung up; keep what we have
        except OSError as e:
            if not sent:
                self._reply(502, json.dumps({"error": str(e)}).encode())
        finally:
            conn.close()
        ms = (time.monotonic() - t0) * 1000
        self.log_req(raw, status, ms)

    def log_req(self, raw, status, ms):
        headers = {k: self.headers[k] for k in HEADER_KEYS if k in self.headers}
        model = client_metadata = first_sha = first_head = None
        tool_results = tool_errors = 0
        if raw:
            try:
                obj = json.loads(raw)
            except ValueError:
                obj = None
            if isinstance(obj, dict):
                model = obj.get("model")
                text = first_user_text(obj)
                if text:
                    first_sha = hashlib.sha256(
                        text.encode("utf-8", "replace")
                    ).hexdigest()
                    first_head = text[:40]
                tool_results, tool_errors = tool_counts(obj)
                cm = raw_object(raw.decode("utf-8", "replace"), "client_metadata")
                if cm:
                    client_metadata = cm
        append_log(
            self.logpath,
            {
                "kind": "req",
                "ts": time.time(),
                "method": self.command,
                "path": urlsplit(self.path).path,
                "headers": headers,
                "model": model,
                "client_metadata": client_metadata,
                "first_user_sha256": first_sha,
                "first_user_head": first_head,
                "tool_errors": tool_errors,
                "tool_results": tool_results,
                "status": status,
                "ms": round(ms, 1),
            },
        )


def make_server(listen_host, listen_port, to_url, log_path):
    parts = urlsplit(to_url)
    target = (parts.hostname, parts.port or 80)

    class Handler(SpikeHandler):
        upstream = target

    Handler.logpath = log_path
    return ThreadingHTTPServer((listen_host, listen_port), Handler)


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--listen", required=True, help="host:port to listen on")
    ap.add_argument("--to", required=True, help="upstream base URL")
    ap.add_argument("--log", required=True, help="JSONL log path")
    a = ap.parse_args(argv)
    host, _, port = a.listen.rpartition(":")
    srv = make_server(host, int(port), a.to, a.log)
    print(f"recorder on http://{a.listen} -> {a.to}, log {a.log}", flush=True)
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
