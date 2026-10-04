"""Tests for the queqiao-spike-codex hook (hook.py).

Run as subprocess with stdin JSON, exactly the way Codex invokes it.
The recorder URL points at a closed port so the probe post fails fast
and silently, which is also what one test asserts.
"""

import json
import os
import subprocess
import sys
import time
import unittest

HOOK = os.path.join(os.path.dirname(__file__), "hook.py")
CLOSED = "http://127.0.0.1:1/spike/log"  # nothing listens here


def run_hook(payload, env=None):
    e = dict(os.environ)
    e.setdefault("QUEQIAO_SPIKE_LOG_URL", CLOSED)
    if env:
        e.update(env)
    return subprocess.run(
        [sys.executable, HOOK],
        input=json.dumps(payload).encode(),
        capture_output=True,
        env=e,
        timeout=10,
    )


class HookTest(unittest.TestCase):
    def test_prompt_submit_prints_nothing(self):
        p = run_hook(
            {
                "hook_event_name": "UserPromptSubmit",
                "session_id": "thr_1",
                "turn_id": "turn_1",
                "prompt": "list the files",
                "permission_mode": "default",
                "model": "gpt-5.6",
                "cwd": "/tmp",
            }
        )
        self.assertEqual(p.returncode, 0)
        self.assertEqual(p.stdout, b"")

    def test_spawn_without_model_gets_fast(self):
        p = run_hook(
            {
                "hook_event_name": "PreToolUse",
                "tool_name": "spawn_agent",
                "tool_input": {"message": "explore the repo"},
                "session_id": "thr_1",
                "turn_id": "turn_2",
                "permission_mode": "plan",
                "model": "gpt-5.6",
                "cwd": "/tmp",
            }
        )
        self.assertEqual(p.returncode, 0)
        out = json.loads(p.stdout)
        hso = out["hookSpecificOutput"]
        self.assertEqual(hso["hookEventName"], "PreToolUse")
        self.assertEqual(hso["updatedInput"]["model"], "group/qq-fast")
        self.assertEqual(hso["updatedInput"]["message"], "explore the repo")

    def test_spawn_with_model_untouched(self):
        p = run_hook(
            {
                "hook_event_name": "PreToolUse",
                "tool_name": "spawn_agent",
                "tool_input": {"message": "y", "model": "group/qq-perf"},
                "session_id": "thr_1",
                "turn_id": "turn_3",
                "permission_mode": "default",
                "model": "gpt-5.6",
                "cwd": "/tmp",
            }
        )
        self.assertEqual(p.returncode, 0)
        self.assertEqual(p.stdout, b"")

    def test_recorder_down_still_exits_zero(self):
        t0 = time.monotonic()
        p = run_hook(
            {
                "hook_event_name": "UserPromptSubmit",
                "session_id": "thr_1",
                "turn_id": "turn_4",
                "prompt": "hi",
                "permission_mode": "default",
                "model": "gpt-5.6",
                "cwd": "/tmp",
            },
            env={"QUEQIAO_SPIKE_LOG_URL": CLOSED},
        )
        dt = time.monotonic() - t0
        self.assertEqual(p.returncode, 0)
        self.assertLess(dt, 1.5)


if __name__ == "__main__":
    unittest.main()
