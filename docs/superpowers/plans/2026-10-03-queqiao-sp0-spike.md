# queqiao SP0-spike Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Settle spikes S1–S13 of the spec with recorded evidence, so every later sub-project knows whether it builds the main path or the fallback.

**Architecture:** One recording proxy sits between the three agents and an upstream magpie gateway built from `main`; it logs what each request carries and also accepts log lines posted by three throwaway probes (a Claude Code mod, a Codex plugin hook, a Pi extension). Each spike is a short scripted session whose log is read against a decision rule copied from spec §10. Results go to one notes file; any fallback taken is written back into the spec before SP2 starts.

**Tech Stack:** Python 3 standard library (recorder, Codex hook, Jev timing), TypeScript (Claude Code mod, Pi extension), upstream magpie (`make cli` on `main`), Claude Code ≥ v2.1.287, Codex (current release), Pi with `tintinweb/pi-subagents`, TypeSafe API key.

**Spec:** `docs/superpowers/specs/2026-10-02-queqiao-design.md` (§10; §2 for the facts being checked; §11 row `SP0-spike`)

## Global Constraints

- Branch `qq/sp0-spike` from `queqiao`, own worktree; ends with a PR into `queqiao`.
- Spike code lives only under `docs/superpowers/spikes/` and is never imported by Go code; no Go files there (keeps `go test ./...` unaffected).
- Results file: `docs/superpowers/notes/spike-results.md`. Every spike gets one row: 编号 · 测试时的版本 · 观察到的事实 · 结论（成立 / 不成立）· 选用（主方案 / 备选）· 证据（日志片段的位置）.
- Raw logs stay out of git (`docs/superpowers/spikes/logs/` in `.gitignore`); commit only the excerpts quoted in the results file, with prompts reduced to their first 40 characters.
- Recorder listens on `127.0.0.1:3500` and forwards to the upstream magpie gateway on `127.0.0.1:3425`; agents point at `3500`. Gateway token `magpie`.
- Tier groups for the spikes, created on the upstream magpie with the human partner's own API-key provider: `qq-fast`, `qq-balanced`, `qq-perf`, and router group `queqiao` = `group/qq-balanced,group/qq-perf,group/qq-fast` (`routing=order stays=turn`), exactly as spec §4.4.
- Interactive steps (typing in Claude Code, Codex, Pi) are done by the human partner; the executor prepares, then reads the log and writes the row. A step marked **[human]** waits for them.
- Decision rules are spec §10's "不成立时" column verbatim; this plan does not invent new fallbacks. A result that fits neither column is a spec question: stop and raise it.

## Review Focus

1. The recorder must stream SSE through unbuffered; a buffering proxy would make every agent look broken and corrupt S1/S5-style timing → `test_rec.py::test_streams_before_upstream_finishes`.
2. Header names arrive in mixed case (`X-Claude-Code-Session-Id`) → recorder logs them lower-cased, test asserts.
3. `x-codex-turn-metadata` may be ASCII-escaped JSON (openai/codex#19620) or absent from headers and only in `client_metadata` → recorder logs both raw, test covers the escaped form.
4. The spike mod's own failure must not look like "Claude Code rejected the rewrite" (S1) → the mod logs `rewrite_sent` before `next` and `rewrite_seen` from the recorder side, and S1's rule is read from the recorder, not the mod.
5. A `/fork` background session may start without the mod or without network → S11's absence of a log line is recorded as "未加载", distinct from "加载但未查到".

---

### Task 1: Recording proxy

**Files:**
- Create: `docs/superpowers/spikes/recorder/rec.py`
- Create: `docs/superpowers/spikes/recorder/test_rec.py`
- Create: `docs/superpowers/spikes/recorder/setup-groups.sh`
- Create: `docs/superpowers/spikes/.gitignore` (`logs/`)

**Interfaces:**
- Produces: `python3 rec.py --listen 127.0.0.1:3500 --to http://127.0.0.1:3425 --log logs/<name>.jsonl`. One JSON object per line. For proxied requests: `{"kind":"req","ts","method","path","headers":{<lower-cased: x-claude-code-session-id, session_id, x-session-id, x-magpie-session, x-codex-turn-metadata, user-agent>},"model","client_metadata","first_user_sha256","first_user_head","tool_errors","tool_results","status","ms"}`. `POST /spike/log` (not forwarded) appends `{"kind":"probe","ts", ...body}` and answers 204. `GET /spike/slow?ms=N` sleeps N ms then answers 200 `{}`.
- `tool_errors`/`tool_results`: Anthropic `tool_result` blocks with `is_error: true` / all; Responses `function_call_output` items (errors counted when the output text starts with `Exit code: ` followed by a non-zero number, otherwise 0 — S8 checks whether anything better exists).
- `setup-groups.sh <fast> <balanced> <perf>` runs the four `magpie group add` commands of spec §4.4 with the given `provider/model` ids.

- [x] **Step 1: Write the failing tests** (`unittest`; a fake upstream `http.server` in a thread)

```python
def test_logs_session_headers_lowercased(self)      # sends X-Claude-Code-Session-Id: S1 → log headers["x-claude-code-session-id"] == "S1"
def test_streams_before_upstream_finishes(self)     # upstream sends chunk 1, sleeps 1 s, chunk 2 → client reads chunk 1 in < 0.5 s
def test_counts_anthropic_tool_errors(self)         # body with 3 tool_result, 1 is_error → tool_results 3, tool_errors 1
def test_keeps_escaped_codex_metadata(self)         # client_metadata {"x-codex-turn-metadata": "{\"turn_id\":\"t\\u0031\"}"} kept verbatim
def test_probe_log_not_forwarded(self)              # POST /spike/log → 204, upstream saw nothing, log kind "probe"
def test_slow_endpoint(self)                        # GET /spike/slow?ms=300 → 200 after ≥ 0.3 s
```

- [x] **Step 2: Run them to see them fail**

Run: `cd docs/superpowers/spikes/recorder && python3 -m unittest -v test_rec`
Expected: FAIL, `ModuleNotFoundError: No module named 'rec'`

- [x] **Step 3: Implement `rec.py`** with `http.server.ThreadingHTTPServer` and `http.client`, copying the response body to the client in 4 KiB reads with a flush after each; never decode the response.

- [x] **Step 4: Run the tests**

Run: `python3 -m unittest -v test_rec`
Expected: 6 tests OK

- [x] **Step 5: Prepare the shared rig [human]**

Run (human partner, once): build upstream magpie from `main` (`git -C <main worktree> pull && make cli`), start `./magpie serve`, add their API-key provider, then `bash setup-groups.sh <fast> <balanced> <perf>`; start `python3 rec.py ... --log logs/rig.jsonl`; `curl -s -H 'Authorization: Bearer magpie' http://127.0.0.1:3500/v1/models | grep -c qq-` 
Expected: `curl` prints ≥ 3; `logs/rig.jsonl` has one `req` line for `/v1/models`

- [x] **Step 6: Commit**

```bash
git add docs/superpowers/spikes/.gitignore docs/superpowers/spikes/recorder
git commit -m "spike: recording proxy for SP0"
```

### Task 2: Jev latency (S5)

**Files:**
- Create: `docs/superpowers/spikes/jev/latency.py`, `docs/superpowers/spikes/jev/test_latency.py`
- Create: `docs/superpowers/notes/spike-results.md` (header, the column list from Global Constraints, 13 empty rows S1–S13)

**Interfaces:**
- Produces: `python3 latency.py -n 100` reads `TYPESAFE_API_KEY`, posts the §5.3 request body (`jev-latest`, questions `tier` + `dissatisfied`, a 200-character `message`) to `https://api.typesafe.ai/v1/systemone` sequentially, prints `p50_ms p95_ms errors` on one line. `def percentile(xs: list[float], p: float) -> float` (nearest-rank).

- [x] **Step 1: Write the failing test**

```python
def test_percentile(self):
    xs = [float(i) for i in range(1, 101)]
    self.assertEqual(percentile(xs, 50), 50.0)
    self.assertEqual(percentile(xs, 95), 95.0)
```

- [x] **Step 2: Run it to see it fail**

Run: `cd docs/superpowers/spikes/jev && python3 -m unittest -v test_latency`
Expected: FAIL, `No module named 'latency'`

- [x] **Step 3: Implement `latency.py`**

- [x] **Step 4: Run the test, then measure [human supplies the key]**

Run: `python3 -m unittest -v test_latency && TYPESAFE_API_KEY=... python3 latency.py -n 100`
Expected: test OK; one result line

- [x] **Step 5: Record S5 and commit**

Rule (spec §10): `p95 > 1000 ms` → 备选（`classify_timeout_ms` 1500，默认分类器改为本地小模型）; otherwise 主方案.

```bash
git add docs/superpowers/spikes/jev docs/superpowers/notes/spike-results.md
git commit -m "spike: S5 Jev latency"
```

### Task 3: Claude Code probe mod

**Files:**
- Create: `docs/superpowers/spikes/cc-mod/.claude-plugin/plugin.json` (`name` `queqiao-spike`, `userConfig`: `log_url` default `http://127.0.0.1:3500/spike/log`, `main_to` default `group/qq-fast`, `sub_to` default `group/qq-balanced`)
- Create: `docs/superpowers/spikes/cc-mod/hooks/hooks.json` (`{"modules": ["./register.ts"]}`)
- Create: `docs/superpowers/spikes/cc-mod/hooks/register.ts`
- Create: `docs/superpowers/spikes/cc-mod/hooks/register.test.ts`

**Interfaces:**
- Produces: every hook posts `{"probe":"cc", "event":<name>, "session":<$.session.id()>, ...fields}` to `log_url` (fire-and-forget, errors swallowed), then:
  - `session.start` — logs `isInteractive`, `surface`.
  - `classic.SessionStart` — logs `source`, `seconds_since_last_response`, and the first user message of `$.session.messages()` (first 80 chars + FNV-1a hex of the full text); reads `$.store` key `spike:first:<hash>` and logs what it finds.
  - `classic.UserPromptSubmit` — logs `permission_mode` (plan-mode source for S1).
  - `turn.start` — logs `text` head, `turnId`; races `$.http.fetch(<recorder>/spike/slow?ms=3000)` against `$.clock.sleep(1500)` and logs `race_winner` (`timer` expected).
  - `turn.step` — logs `turnId,index,model,effort,agentId`; when `model === "group/queqiao"` logs `rewrite_sent` and continues with `model: agentId ? sub_to : main_to`; otherwise passes through. Written as `async function*` with `yield* next(...)`.
  - `agent.spawn` — logs `fork, subagentType, model, parentModel, background`; when `!fork && !model` calls `next({...e, model: "haiku"})`, logs the result's `model` and `agentId`.
  - `tool.call` — after `next`, logs `tool, agentId, isError`.
  - `classic.PostModelSwitch` — logs `from_model, to_model, source`.
  - `turn.complete` — on the session's first completed turn, writes `$.store` `spike:first:<hash of first user message>` = `{session, at}`.

- [x] **Step 1: Write the failing test** (`claude-code/testing`): mock `$.http.fetch`; drive a `turn.step` with `model: "group/queqiao"` and no `agentId` → the step that reaches the engine names `group/qq-fast`; with `agentId: "a1"` → `group/qq-balanced`; with `model: "group/qq-perf"` → unchanged.

- [x] **Step 2: Run it to see it fail**

Run: `claude plugin test docs/superpowers/spikes/cc-mod`
Expected: FAIL (module has no `turn.step` hook yet)

- [x] **Step 3: Implement `register.ts`**

- [x] **Step 4: Validate and test**

Run: `claude plugin validate docs/superpowers/spikes/cc-mod && claude plugin test docs/superpowers/spikes/cc-mod`
Expected: validate reports no refusals; tests pass

- [x] **Step 5: Commit**

```bash
git add docs/superpowers/spikes/cc-mod
git commit -m "spike: Claude Code probe mod"
```

### Task 4: Claude Code main path and session id (S1, S2)

**Files:** results file only.

- [x] **Step 1: Run the session [human]**

`ANTHROPIC_BASE_URL=http://127.0.0.1:3500 ANTHROPIC_AUTH_TOKEN=magpie claude --plugin-dir docs/superpowers/spikes/cc-mod --model group/queqiao` with `python3 rec.py ... --log logs/s1.jsonl`; do 20 turns: 10 plain questions, 5 that make Claude run tools (one failing `ls /nope`), 1 with `@file`, 1 with a pasted image, 1 after `/model opus` then `/model default`, 2 in plan mode (Shift+Tab).

- [x] **Step 2: Read the log**

Run: `jq -c 'select(.kind=="req" and .path=="/v1/messages") | [.model, .headers["x-claude-code-session-id"]]' logs/s1.jsonl | sort | uniq -c` and `jq -c 'select(.event=="turn.step") | [.model, .agentId]' logs/s1.jsonl | sort | uniq -c`
Expected to decide:
- S1 main path holds when every main request after a `rewrite_sent` reaches the recorder as `group/qq-fast`, `turn.step` showed `model == "group/queqiao"` for main steps, the `/model opus` turn showed `group/qq-perf` and was not rewritten, `classic.UserPromptSubmit` showed `permission_mode == "plan"` in plan mode, and cache reads (`usage.cache_read_input_tokens` in the responses, logged by upstream magpie `usage.jsonl`) are non-zero from the second step of a turn.
- S2 holds when the `session` field of the mod's lines equals `x-claude-code-session-id` on the requests of the same turn.

- [ ] **Step 3: If S1 fails on the custom name, rerun with `main_to=sonnet`/`sub_to=haiku` [human] and record which form works**

Rule: names rejected → 备选 1 (aliases); aliases also fail → 备选 2 (command hook + hint path (b)), and stop to revise the spec §3.1/§6.7 before SP3.

- [x] **Step 4: Record S1, S2 and commit** (`git commit -m "spike: S1 S2 results"`)

### Task 5: Claude Code subagents and forks (S3)

**Files:** results file only.

- [x] **Step 1: Run the session [human]** with the probe mod (log `logs/s3.jsonl`): ask Claude to "use the Explore agent to list the Go packages", then "use a general-purpose agent to summarise README.md", then `/subtask summarise the last answer in one line`, then ask for a fork via the Agent tool with `subagent_type: "fork"`.

- [x] **Step 2: Read the log**

Expected to decide S3 holds when: `agent.spawn` for the general-purpose agent logged `model: "haiku"` back with an `agentId`, and that agent's requests reached the recorder as `group/qq-fast`; the fork's `agent.spawn` showed `fork: true`; its `turn.step` lines carry an `agentId` with `model == "group/queqiao"`; its requests reached the recorder as `group/qq-balanced` (the rewrite was accepted); its first request's `usage` shows a cache read close to the main session's prompt size.

Rule: alias ignored → 备选（drop selection in `agent.spawn`, pin in `turn.step`）; fork rewrite rejected → 备选（restore the gateway-side fork heuristic）.

- [x] **Step 3: Record S3 and commit** (`git commit -m "spike: S3 result"`)

### Task 6: Where the mod runs (S10)

**Files:** results file only.

- [x] **Step 1: Run each mode [human]**, one prompt each, log `logs/s10.jsonl`: interactive REPL; `claude -p "say hi" --plugin-dir ...`; an Agent SDK script (`@anthropic-ai/claude-agent-sdk` `query()` with `plugins: [{type:"local", path}]`); `/fork` from the REPL then one prompt in the forked session; `claude --version`.

- [x] **Step 2: Read the log**

Expected to decide, per mode: mod loaded (`session.start` line present) · `$.http.fetch` to `127.0.0.1` reached the recorder · `race_winner == "timer"` (the 1500 ms race gives up on a 3000 ms reply).

Rule: a mode without the mod → 主方案 holds, README lists it under "falls back to gateway mode"; localhost refused → 备选（`socketPath`）; `race_winner` not `timer` → 备选（no race; G5 exception in README）. Record the Claude Code version as the minimum tested.

- [x] **Step 3: Record S10 and commit** (`git commit -m "spike: S10 result"`)

### Task 7: Derived Claude Code sessions (S11)

**Files:** results file only.

- [x] **Step 1: Run [human]**, log `logs/s11.jsonl`: new session, two prompts; `/fork` and one prompt in the fork; back in the original, `/branch` and one prompt.

- [x] **Step 2: Read the log**

Expected to decide S11 holds when: `classic.SessionStart` fired in both new sessions with `source` recorded (expect `fork` for `/fork`; record whatever `/branch` gives, or that it did not fire); the first-message hash logged in each derived session equals the original's; the `$.store` lookup found the original session id.

Rule (spec §10): `/branch` without `SessionStart` → `/branch` treated as a new session; hash differs or store unreadable → derived sessions treated as new sessions.

- [x] **Step 3: Record S11 and commit** (`git commit -m "spike: S11 result"`)

### Task 8: Codex probe plugin

**Files:**
- Create: `docs/superpowers/spikes/codex-plugin/plugin.json` (`name` `queqiao-spike-codex`)
- Create: `docs/superpowers/spikes/codex-plugin/hooks/hooks.json` — `UserPromptSubmit`, `PreToolUse` (matcher `^(spawn_agent|Agent)$`), `PostToolUse` (matcher `^Bash$`), each `python3 "$PLUGIN_ROOT/hooks/hook.py"`, `timeout` 2
- Create: `docs/superpowers/spikes/codex-plugin/hooks/hook.py`, `docs/superpowers/spikes/codex-plugin/hooks/test_hook.py`
- Create: `docs/superpowers/spikes/codex-market/.agents/plugins/marketplace.json` (lists the Codex plugin) and `docs/superpowers/spikes/codex-market/.claude-plugin/marketplace.json` (lists the Claude Code probe mod) — the S7 precedence check

**Interfaces:**
- Produces: `hook.py` reads stdin JSON, posts `{"probe":"codex","event":hook_event_name, "session_id","turn_id","model","permission_mode","tool_input"}` to the recorder (1 s timeout, errors ignored), records `time.time()` at start and end in the same line; for `PreToolUse` whose `tool_input` has no `model`, prints `{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{...tool_input,"model":"group/qq-fast"}}}`; otherwise prints nothing; always exits 0.

- [x] **Step 1: Write the failing tests**

```python
def test_prompt_submit_prints_nothing(self)        # stdin UserPromptSubmit sample → stdout "" and exit 0
def test_spawn_without_model_gets_fast(self)       # PreToolUse spawn_agent {"message":"x"} → updatedInput.model == "group/qq-fast", message kept
def test_spawn_with_model_untouched(self)          # tool_input has model → stdout ""
def test_recorder_down_still_exits_zero(self)      # log_url points at a closed port → exit 0 within 1.5 s
```

- [x] **Step 2: Run them to see them fail**

Run: `cd docs/superpowers/spikes/codex-plugin/hooks && python3 -m unittest -v test_hook`
Expected: FAIL, `No module named 'hook'`

- [x] **Step 3: Implement `hook.py`**

- [x] **Step 4: Run the tests**

Expected: 4 tests OK

- [x] **Step 5: Commit** (`git commit -m "spike: Codex probe plugin"`)

### Task 9: Codex hooks, ids, plan mode, subagents (S6, S7, S8, S9, S13)

**Files:** results file only.

- [x] **Step 1: Install and trust [human]**: `codex plugin marketplace add <abs path>/docs/superpowers/spikes/codex-market`; install `queqiao-spike-codex`; `/hooks` → trust; point Codex at the recorder (`magpie codex group/queqiao` on the upstream magpie, then set the `magpie` provider's `base_url` to `http://127.0.0.1:3500/v1`); `codex --version`.

- [x] **Step 2: Run [human]**, log `logs/codex.jsonl`: 20 turns (S6); one turn in plan mode and one in default (S8); a turn with a failing shell command (S8); "spawn an explorer subagent to list files" (S9); "spawn a subagent with fork_context true to summarise this conversation" (S13).

- [x] **Step 3: Read the log**

Expected to decide:
- S7: hook lines appear at all (plugin hooks run); `t_end - t_start` vs the 2 s limit tells nothing about units, so also run once with `hook.py` sleeping 3 s and see whether Codex cut it off (seconds) or not (milliseconds would cut at 2 ms — every call would fail); the marketplace listing shows only the Codex plugin.
- S6: hook `turn_id` equals the `turn_id` inside the same turn's `x-codex-turn-metadata` (header or `client_metadata`); note where it was found.
- S8: `permission_mode` value in plan mode; whether the failing command's `function_call_output` lets the recorder count it (`tool_errors > 0`).
- S9: the explorer's requests reach the recorder as `group/qq-fast`.
- S13: the fork_context child's requests are `group/qq-fast`, and its metadata `parent_thread_id` equals the parent's `thread_id`.

Rules: spec §10 rows S6–S9, S13 verbatim.

- [x] **Step 4: Record S6–S9, S13 and commit** (`git commit -m "spike: Codex results"`)

### Task 10: Pi probe extension and spikes (S4, S12)

**Files:**
- Create: `docs/superpowers/spikes/pi-ext/package.json` (`"keywords":["pi-package"]`, `"pi":{"extensions":["./spike.ts"]}`)
- Create: `docs/superpowers/spikes/pi-ext/spike.ts`

**Interfaces:**
- Produces: logs to the recorder `session_start` (`reason`, every field of the event and of the session object that looks like an id or parent), `before_agent_start` (then `const ok = await pi.setModel(<the magpie/group/qq-fast model object>)`, logs `ok` and how the model object was obtained), `before_provider_request` (model in the outgoing body), `before_provider_headers` (adds `X-Magpie-Session: spike-<session id or random>`), `tool_call` for tool `Agent` (full `event.input`; when `inherit_context` is true, sets `event.input.model = "magpie/group/qq-fast"`).

- [x] **Step 1: Write `spike.ts` and load it [human]**: `pi install <abs path>/docs/superpowers/spikes/pi-ext`, `pi install npm:<pi-subagents package>`, Pi's `magpie` provider `baseUrl` → `http://127.0.0.1:3500/v1`.

Expected: Pi starts with no extension error; first prompt produces a `before_agent_start` line in `logs/pi.jsonl`.

- [x] **Step 2: Run [human]**: 3 prompts; `/fork` and one prompt; `/tree` to a branch and one prompt; ask for an Agent subagent with `inherit_context: true`.

- [x] **Step 3: Read the log**

Expected to decide:
- S4: `setModel` returned true and the same turn's request reached the recorder as `group/qq-fast`; the session id source (a stable field, or none → `randomUUID`).
- S12: `session_start` with `reason == "fork"` exposes a parent session id (which field) or not; the Agent tool's parameter names; whether the `inherit_context` child's first request shares the parent's prompt prefix (compare `first_user_sha256` and cache-read tokens).

Rules: spec §10 rows S4, S12 verbatim.

- [x] **Step 4: Record S4, S12 and commit** (`git add docs/superpowers/spikes/pi-ext && git commit -m "spike: Pi results"`)

### Task 11: Close SP0

**Files:**
- Modify: `docs/superpowers/notes/spike-results.md` (summary section)
- Modify: `docs/superpowers/specs/2026-10-02-queqiao-design.md` (only where a spike took the fallback)

- [x] **Step 1: Check the results file is complete**

Run: `grep -cE '^\| S(1[0-3]|[1-9]) \|' docs/superpowers/notes/spike-results.md` and `grep -n '（待填）\|TBD' docs/superpowers/notes/spike-results.md`
Expected: `13`, and no matches

- [x] **Step 2: Write the summary**: one line per later sub-project (SP2, SP3, SP4, SP6) saying which spec sections change because of a fallback, or "无变化".

- [x] **Step 3: Apply each fallback to the spec** (spec §11 rule: revise the spec before the dependent plan is written), and add a revision note to the spec header naming the spikes.

- [ ] **Step 4: PR**

```bash
git push -u origin qq/sp0-spike
gh pr create -R weiping/queqiao --base queqiao --head qq/sp0-spike \
  --title "SP0: spike results S1–S13" \
  --body "Results in docs/superpowers/notes/spike-results.md; plan docs/superpowers/plans/2026-10-03-queqiao-sp0-spike.md."
```
Expected: PR checks pass (no Go code changed); merge with a merge commit after the human partner reads the results
