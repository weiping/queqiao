# SP10: the clients wait as long as the classifier may take

> Status: approved by the author 2026-10-10 ("按方案 1 改").
> Changes the fixed 1500 ms of the original design (§6.6, §6.8, §6.9 of
> `2026-10-02-queqiao-design.md`) and SP3's note on it.

## 1. Problem

Every client gives `/v1/bridge/turn` a fixed 1500 ms: the Claude Code mod,
the Pi extension and the Codex hook. mbridge's own classify timeout
(`classify_timeout_ms`) defaults to 1500 as well. Jev answers a warm request
in 300–450 ms, but the first turn after 15–30 minutes idle takes longer than
1.5 s. Measured on the author's machine on 2026-10-10:

| turn (UTC) | since the last classify | latency |
| --- | --- | --- |
| 12:33 | first of the day | 1501 ms, R8-default |
| 12:59 ×2 | warm | 413 ms, 363 ms |
| 13:18 | ~20 min | 1455 ms |
| 14:06 Codex ×2 | ~28 min | 1500 ms, 1501 ms, R8-default |

So the first turn after a pause always falls to the default tier, and raising
`classify_timeout_ms` does nothing for Claude Code and Pi, because they stop
waiting at 1500 ms whatever mbridge allows.

## 2. Decision

mbridge's configuration decides how long a turn may wait, and every client
follows it.

- `turn_budget_ms = classify_timeout_ms + 500`, clamped to [1500, 8000].
  - The 500 ms covers mbridge's own work and the HTTP round trip.
  - 8000 keeps the Claude Code mod well inside its 10 s hook limit.
- `GET /v1/bridge/router` reports `turn_budget_ms` beside the rest of its
  status.
- **Claude Code mod:** reads `turn_budget_ms` from the reachability probe it
  already makes at `session.start` and keeps it in `$.state` (`turnBudget`).
  Without it (an older mbridge, or the probe failed) the mod waits 1500 ms
  as before.
- **Pi extension:** does the same at `session_start`, with one GET.
- **Codex hook:** runs as `mbridge hook`, on the same machine as mbridge, so
  it reads `router.json` itself; no extra request per turn. If the file
  can't be read, it waits 1500 ms.
- **Clients clamp too.** Each client applies the same [1500, 8000] range, so
  a bad value can't make it wait forever or give up sooner than before.
- **New default `classify_timeout_ms`: 2500** (budget 3000), used both when
  the key is missing and by `router init`. An existing `router.json` that sets
  `classify_timeout_ms` keeps its value; the user raises it themselves.

Cost: the first turn after a pause starts up to `turn_budget_ms` later than
it would have (at the default, 1.5 s more). Warm turns are unchanged, because
the wait ends as soon as `/turn` answers.

Rejected: keeping Jev warm with a small request every 10 minutes. It doesn't
add latency, but it spends about 340 tokens per ping while the user is away.

## 3. Tests

- **Go:**
  - `Config.TurnBudgetMs` checks the formula and both clamps.
  - The status endpoint reports `turn_budget_ms`.
  - A missing `classify_timeout_ms` becomes 2500, and `router init` writes
    2500.
  - The Codex hook waits for a `/turn` that answers in 1.8 s when
    `router.json` allows it, and gives up at 1.5 s when the file is missing.
- **Claude Code mod:**
  - After a `session.start` whose probe reports 3000, a `/turn` answering at
    2000 ms is used.
  - Without the probe, the 1500 ms timer still wins.
- **Pi:** the same two cases.
