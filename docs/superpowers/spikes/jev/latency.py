#!/usr/bin/env python3
"""Measure the real latency from this machine to Jev (System One).

Posts the spec §5.3 request body (jev-latest, questions tier +
dissatisfied, a 200-character message) sequentially to
https://api.typesafe.ai/v1/systemone and prints one line:

    p50_ms p95_ms errors

Reads TYPESAFE_API_KEY from the environment.
"""

import argparse
import json
import math
import os
import time
import urllib.error
import urllib.request

URL = "https://api.typesafe.ai/v1/systemone"

# A 200-character stand-in for a user message (spec §5.3 shape only;
# content is irrelevant to the latency measurement).
MESSAGE = (
    "Please look at the failing test in router_test.go and tell me which "
    "assertion is wrong, then propose the smallest fix that keeps the "
    "existing behaviour for the other cases. Answer in one paragraph."
    " Now."
)[:200]

BODY = {
    "model": "jev-latest",
    "state": {
        "message": MESSAGE,
        "previous_tier": "balanced",
        "agent": "main",
    },
    "questions": {
        "tier": {
            "type": "choice",
            "instructions": (
                "The `message` is what a user asked a coding assistant. "
                "Which tier can most cheaply handle it well? A message "
                "that only carries on from the turn before (go on, yes, "
                "do it) is of `previous_tier`."
            ),
            "criteria": {
                "fast": "quick lookup, rename, format, one-liner answer",
                "balanced": "everyday coding: read a file, edit, run tests",
                "performance": "deep reasoning, tricky bug, large refactor",
            },
        },
        "dissatisfied": {
            "type": "noul",
            "instructions": (
                "The `message` says the assistant's previous result was "
                "wrong, broken, incomplete, or not what the user asked for."
            ),
        },
    },
}


def percentile(xs, p):
    """Nearest-rank percentile of a non-empty list."""
    if not xs:
        raise ValueError("percentile of empty list")
    ordered = sorted(xs)
    k = max(1, math.ceil(p / 100 * len(ordered)))
    return ordered[k - 1]


def one_request(key, timeout):
    body = json.dumps(BODY).encode("utf-8")
    req = urllib.request.Request(
        URL,
        data=body,
        headers={
            "Content-Type": "application/json",
            "Authorization": f"Bearer {key}",
        },
    )
    t0 = time.monotonic()
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        resp.read()
    return (time.monotonic() - t0) * 1000


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("-n", type=int, default=100, help="number of requests")
    ap.add_argument("--timeout", type=float, default=30.0)
    a = ap.parse_args(argv)
    key = os.environ.get("TYPESAFE_API_KEY")
    if not key:
        raise SystemExit("TYPESAFE_API_KEY is not set")
    times, errors = [], 0
    for i in range(a.n):
        try:
            times.append(one_request(key, a.timeout))
        except (urllib.error.URLError, OSError, ValueError):
            errors += 1
        if (i + 1) % 10 == 0:
            print(f"... {i + 1}/{a.n}", flush=True)
    if not times:
        raise SystemExit("every request failed")
    print(
        f"{percentile(times, 50):.0f} {percentile(times, 95):.0f} {errors}",
        flush=True,
    )


if __name__ == "__main__":
    main()
