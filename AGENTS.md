# Notes for coding agents

queqiao is a model router that runs beside official
[magpie](https://github.com/yetone/magpie). It does not contain or import
magpie's code: everything it asks of magpie goes through `internal/magpie`,
over magpie's public HTTP endpoints (`/v1/systemone`, `/v1/chat/completions`,
`/v1/models`), its CLI (`magpie group`, `magpie usage --csv`, `magpie <agent>`)
and its usage CSV. A change that needs anything else from magpie is a design
change: update the SP8 spec first
([`docs/superpowers/specs/2026-10-10-queqiao-sp8-standalone-design.md`](docs/superpowers/specs/2026-10-10-queqiao-sp8-standalone-design.md)).

## Where things are

| Path | What |
| --- | --- |
| `cmd/queqiao` | the one binary: CLI and `queqiao serve` (queqiaod) |
| `internal/router` | tier policy, classifier, review, calibration, reports |
| `internal/wire` | the three-protocol request parser the router and proxy read |
| `internal/proxy` | queqiaod's reverse proxy for Codex and gateway mode |
| `internal/magpie` | the only place that talks to magpie |
| `internal/codexcfg` | Codex's `queqiao.config.toml` and model catalog |
| `internal/harness` | Codex command hooks |
| `internal/runcmd` | every command queqiao runs (no window on Windows) |
| `clients/` | Claude Code mod, Codex plugin, Pi extension |

## Contract with magpie

`contract/` runs against the latest official magpie release every day. When it
goes red, magpie changed something queqiao relies on: fix `internal/magpie`
(and the contract test) rather than working around it elsewhere.

## Lessons

Before changing code, read [LESSONS.md](LESSONS.md): what merged work got
wrong and the rule that would have caught it.
