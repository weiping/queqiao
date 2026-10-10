# magpie-bridge-codex (Codex)

Route every Codex turn across mbridge's model tiers — `fast` for questions
and small changes, `balanced` for ordinary work, `performance` for hard
or multi-file work — decided per turn by the mbridge gateway.

## What it does

- `UserPromptSubmit` asks the gateway (`POST /v1/bridge/turn`, waiting
  mbridge's `turn_budget_ms`, 1.5–8 s) which tier the prompt deserves; the gateway's prompt hint then
  routes the request that follows. A prompt sent while you pinned a model
  with `/model` reports `manual_model_switch` instead.
- `PreToolUse` on `spawn_agent` pins a tier's group on spawns that carry
  no model of their own; spawns you configured stay untouched.
- `PostToolUse` on Bash reports GitHub PR links it sees (`pr_created`).

Every hook is fail-safe: gateway down, timeouts, malformed input — the
hook stays silent, exits 0, and the request simply goes out as
`group/mbridge`, which the gateway's own fallback mode routes.

## Install

```sh
codex plugin marketplace add weiping/magpie-bridge
codex plugin add magpie-bridge-codex@magpie-bridge
```

(`codex plugin add` needs the `@magpie-bridge` marketplace suffix; without it
Codex asks for `--marketplace`. Codex has no `plugin install` subcommand.)

Then three steps inside Codex:

1. **Trust the hooks**: `/hooks` lists the plugin's hooks — review and
   trust them (they run `mbridge hook …` commands). Non-interactive runs
   can use `codex exec --dangerously-bypass-hook-trust` instead.
2. **Restart Codex** so the hooks load.
3. Check the status message「mbridge: 选档」appears on each prompt.

## Requirements

- `mbridge` on `PATH` (the hooks call `mbridge hook …`)
- official magpie running, and mbridge running (`mbridge serve`, or the
  service `mbridge service install` sets up); groups, router.json and
  Codex's profile set up by `mbridge router init`
- Codex started as `codex -p mbridge`: the profile
  (`~/.codex/mbridge.config.toml`) sends its requests through mbridge
  with `model = group/mbridge`
- mbridge's URL from `MBRIDGE_URL` (default `http://127.0.0.1:3426`)

## Troubleshooting

- Nothing routes, no status message: is `mbridge` on PATH?
  (`which mbridge`) Are the hooks trusted (`/hooks`)? Is the gateway on
  its default port? (Hook subprocesses do not inherit custom
  environment variables, so `MBRIDGE_URL` set in your shell does not
  reach the hook — mbridge must serve on `127.0.0.1:3426`, the
  default.) Was Codex started with `-p mbridge`?
- Status message but wrong tier: `mbridge router status` shows the
  config and recent decisions.

## SP7: the Stop hook needs re-trusting

mbridge 0.1.x adds a `Stop` hook (the end-of-turn review). Codex treats
changed hook content as untrusted, so after upgrading run `/hooks` once and
approve the `mbridge hook stop --harness codex` entry — until then Codex
skips it, and the router simply gets no reviews (no errors).
