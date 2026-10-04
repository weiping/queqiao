#!/usr/bin/env python3
"""queqiao-spike-codex hook: records and pins, nothing else.

Reads the hook event JSON from stdin, posts one probe line to the
recorder (1 s timeout, errors ignored), and — for a PreToolUse whose
tool_input carries no model — prints a PreToolUse decision that lets
the call run with `model` pinned to the fast tier group. Always exits 0.

Codex documents updatedInput as valid only alongside
permissionDecision "allow" (learn.chatgpt.com/docs/hooks), so both are
printed together.
"""

import json
import os
import sys
import time
import urllib.request

LOG_URL = os.environ.get("QUEQIAO_SPIKE_LOG_URL", "http://127.0.0.1:3500/spike/log")
FAST = os.environ.get("QUEQIAO_SPIKE_FAST", "group/qq-fast")


def main():
    t_start = time.time()
    raw = sys.stdin.read()
    try:
        e = json.loads(raw)
    except ValueError:
        e = {}
    if not isinstance(e, dict):
        e = {}

    out = ""
    tool_input = e.get("tool_input")
    if (
        e.get("hook_event_name") == "PreToolUse"
        and isinstance(tool_input, dict)
        and "model" not in tool_input
    ):
        updated = dict(tool_input)
        updated["model"] = FAST
        out = json.dumps(
            {
                "hookSpecificOutput": {
                    "hookEventName": "PreToolUse",
                    "permissionDecision": "allow",
                    "updatedInput": updated,
                }
            }
        )

    t_end = time.time()
    line = {
        "probe": "codex",
        "event": e.get("hook_event_name"),
        "session_id": e.get("session_id"),
        "turn_id": e.get("turn_id"),
        "model": e.get("model"),
        "permission_mode": e.get("permission_mode"),
        "tool_name": e.get("tool_name"),
        "tool_input": tool_input,
        "t_start": t_start,
        "t_end": t_end,
    }
    try:
        req = urllib.request.Request(
            LOG_URL,
            data=json.dumps(line).encode("utf-8"),
            headers={"Content-Type": "application/json"},
        )
        urllib.request.urlopen(req, timeout=1).read()
    except Exception:
        pass  # recorder down must never break the hook

    sys.stdout.write(out)
    sys.exit(0)


if __name__ == "__main__":
    main()
