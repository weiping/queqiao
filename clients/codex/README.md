# queqiao-router-codex (Codex)

Route every Codex turn across queqiao's model tiers — `fast` for questions
and small changes, `balanced` for ordinary work, `performance` for hard
or multi-file work — decided per turn by the queqiao gateway.

## What it does

- `UserPromptSubmit` asks the gateway (`POST /v1/queqiao/turn`, 1.5 s
  budget) which tier the prompt deserves; the gateway's prompt hint then
  routes the request that follows. A prompt sent while you pinned a model
  with `/model` reports `manual_model_switch` instead.
- `PreToolUse` on `spawn_agent` pins a tier's group on spawns that carry
  no model of their own; spawns you configured stay untouched.
- `PostToolUse` on Bash reports GitHub PR links it sees (`pr_created`).

Every hook is fail-safe: gateway down, timeouts, malformed input — the
hook stays silent, exits 0, and the request simply goes out as
`group/queqiao`, which the gateway's own fallback mode routes.

## Install

```sh
codex plugin marketplace add weiping/queqiao
codex plugin install queqiao-router-codex
```

Then three steps inside Codex:

1. **Trust the hooks**: `/hooks` lists the plugin's hooks — review and
   trust them (they run `queqiao hook …` commands). Non-interactive runs
   can use `codex exec --dangerously-bypass-hook-trust` instead.
2. **Restart Codex** so the hooks load.
3. Check the status message「queqiao: 选档」appears on each prompt.

## Requirements

- `queqiao` on `PATH` (the hooks call `queqiao hook …`)
- the queqiao gateway running (`queqiao serve`), groups and router.json
  set up (`queqiao router init`); Codex's `model_provider` pointing at
  the gateway with `model = group/queqiao`
- the gateway URL from `QUEQIAO_URL` (default `http://127.0.0.1:3425`)

## Troubleshooting

- Nothing routes, no status message: is `queqiao` on PATH?
  (`which queqiao`) Are the hooks trusted (`/hooks`)? Is the gateway on
  its default port? (Hook subprocesses do not inherit custom
  environment variables, so `QUEQIAO_URL` set in your shell does not
  reach the hook — the gateway must serve on `127.0.0.1:3425`, the
  default.)
- Status message but wrong tier: `queqiao router status` shows the
  config and recent decisions.
