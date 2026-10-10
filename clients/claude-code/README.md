# queqiao-router (Claude Code)

Route every Claude Code turn across queqiao's model tiers — `fast` for
questions and small changes, `balanced` for ordinary work, `performance`
for hard, multi-file or unknown-cause work — decided per turn by the
queqiao gateway's classifier.

## What it does

- `turn.start`: sends the prompt to the gateway (`POST /v1/queqiao/turn`,
  1500 ms budget) and records the tier it picks.
- `turn.step`: rewrites requests still on the routing group `group/queqiao`
  to this turn's tier group (`group/qq-fast` / `qq-balanced` / `qq-perf`).
  Anything else — a model you pinned with `/model`, a tier group a
  subagent already chose — passes through untouched.
- `agent.spawn`: picks a tier for subagents started from scratch
  (type table first, then the classifier); agents that inherit the main
  model are pinned to the main session's tier at the moment they appear.
- Reports tool-call stats, PR links from Bash output, and manual
  `/model` switches back to the gateway.

If the gateway is down, or any call fails, the mod gets out of the way:
requests go out as `group/queqiao` and the gateway's own fallback mode
routes them (or they fail like any other request would).

## Requirements

- Claude Code **v2.1.287 or newer** (mod hooks).
- Official magpie running (its gateway on `127.0.0.1:3425`), and queqiaod
  running (`queqiao service install`, or `queqiao serve`) on this machine.
- Routing groups and `router.json` set up (`queqiao router init`).

Not required: Node, Python, or `queqiao` on `PATH` — the mod talks to
queqiaod over HTTP only. Claude Code's own requests go straight to magpie,
the mod switching the model to `group/qq-<tier>` each turn.

## Install

```sh
claude plugin marketplace add weiping/queqiao
claude plugin install queqiao-router@queqiao
```

The gateway URL defaults to `http://127.0.0.1:3426` (queqiaod); change it in the
plugin's `gateway_url` setting if queqiaod listens elsewhere.

## Known limitations

- **Agent SDK runs don't load this mod** on macOS: the SDK's local spawn
  fails with `errno -88` before Claude Code starts (observed with SDK
  0.3.289 / CLI 2.1.289). SDK-driven sessions fall back to the gateway's
  own routing. Under investigation.
- `classic.SessionStart` (used to detect `/fork` / `/branch` sessions)
  does not fire in `-p` mode; derived-session tier inheritance applies to
  interactive sessions.
- `--safe-mode`, `allowManagedModsOnly`, and org policies that disable
  mods mean this mod never loads; requests then route via the gateway's
  fallback mode.

## Development

```sh
claude plugin validate --strict clients/claude-code
claude plugin test clients/claude-code
```
