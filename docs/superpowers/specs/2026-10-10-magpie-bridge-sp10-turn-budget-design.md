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
  An older mbridge that reports no budget means 1500 ms, as before. A failed
  probe means the next turn asks again (see below).
- **Pi extension:** does the same at `session_start`, with one GET.
- **Codex hook:** runs as `mbridge hook`, once per event. Each run asks the
  running mbridge for the budget (see below), with `router.json` as the
  fallback; if neither answers, it waits 1500 ms.
- **Clients clamp too.** Each client applies the same [1500, 8000] range, so
  a bad value can't make it wait forever or give up sooner than before.
- **New default `classify_timeout_ms`: 2500** (budget 3000), used both when
  the key is missing and by `router init`. An existing `router.json` that sets
  `classify_timeout_ms` keeps its value; the user raises it themselves.

- `classify_timeout_ms` bounds the whole classification, not each request.
  The plain classifier asks up to three times, so otherwise mbridge could
  take longer than the budget it reports. A request that runs out of time
  doesn't mark the model as refusing structured output.
- Each client also learns the budget when a turn needs it:
  - **Claude Code:** when the `session.start` probe failed, or `/clear`
    reset `$.state`.
  - **Pi:** when the `session_start` probe found mbridge not up yet.

  The probe gives up after 500 ms.
- **Codex hook:** asks the running mbridge first and gives up after 300 ms.
  The daemon's configuration can differ from the file on disk (an edit since
  it started, another config dir, `MBRIDGE_URL` elsewhere); then the hook
  falls back to `router.json`, then to 1500 ms.
- **Codex hook timeouts:** `hooks.json` gives `user-prompt` and `pre-agent`
  10 s, more than the 8 s maximum budget, and the hook's own cap is the
  budget plus 1 s. Before this, Codex killed them at 2 s.

Cost:
- The first turn after a pause starts up to `turn_budget_ms` later than it
  would have (at the default, 1.5 s more). Warm turns are unchanged, because
  the wait ends as soon as `/turn` answers.
- When Claude Code still gives up (classification longer than the budget),
  the turn goes out as `group/mbridge`, and gateway mode classifies again,
  once more up to `classify_timeout_ms`. The worst case is about
  budget + classify, 5.5 s at the default, against about 3 s before.
- In plain gateway mode, a cold turn can wait up to 1 s longer than before.

Rejected: keeping Jev warm with a small request every 10 minutes. It doesn't
add latency, but it spends about 340 tokens per ping while the user is away.

## 3. Tests

- **Go:**
  - `Config.TurnBudgetMs` checks the formula and both clamps.
  - The status endpoint reports `turn_budget_ms`.
  - A missing `classify_timeout_ms` becomes 2500, and `router init` writes
    2500.
  - The Codex hook waits for a `/turn` that answers in 1.8 s when its
    budget allows it.
  - The Codex hook takes the running mbridge's budget before the file's,
    gives up on a hung mbridge within the probe time, and falls back to the
    file, then to 1500 ms.
  - The `user-prompt` and `pre-agent` timeouts in `hooks.json` are above
    8 s.
  - One deadline covers a plain classification that asks twice.
- **Claude Code mod:**
  - After a `session.start` whose probe reports 3000, a `/turn` answering at
    2000 ms is used.
  - When mbridge reports no budget, the 1500 ms timer still wins.
  - A turn with no budget learned yet asks for one before `/turn`.
  - Out-of-range budgets are clamped.
- **Pi:** the same cases. The probe also gives up on a hung mbridge within
  500 ms.
