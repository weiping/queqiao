# queqiao SP2-router-core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `internal/router/` — the gateway-side routing core that picks a tier per turn via a pure `Choose` policy (R1–R8), a classifier (Jev or local model), in-memory session/tool-stats state, hint storage for Codex, session arm assignment, a `router.jsonl` event log, and the `/v1/queqiao/*` HTTP API — and hook it into the magpie gateway (`ruleFor` callback + mux) so requests to the router group get tiered while requests to the tier groups just record stats.

**Architecture:** The policy (`Choose`) is a pure function over `PolicyInput`/`PolicyConfig` — no I/O, the most-tested unit. State lives in-memory in `Sessions` (24 h eviction). Classification is behind a `Classifier` interface: Jev via the gateway's existing `/v1/systemone` client, or a plain model that answers a number (reuse magpie's classify prompt idea). The gateway integration is a single optional `routerHook` injected into `ruleFor()` before its `Ruled()` gate (the router group has no rules, so the hook must run before that early return); the hook observes tier-group traffic and tiers router-group traffic. Everything new is under `internal/router/`; the only upstream-file changes are the `ruleFor`-adjacent hook (call site + top-of-function short-circuit), a `MuxRegister` registrar in `gateway.Handler()`, the `RuleHit.Router` field, and two `usage.Record` fields. **Dependency direction is strictly `router` → `gateway`** (router references gateway request types); `gateway` never imports `router`; all wiring happens in `main.go` (SP2 review correction — the original spec line "gateway.go 加一行 router.Register" would create an import cycle with §6.2's `Observe(session, req *gateway.Request)`).

**Tech Stack:** Go 1.26 (module `github.com/yetone/magpie`, unchanged), stdlib only for router; existing `internal/provider`, `internal/gateway`, `internal/usage` packages.

**Spec:** `docs/superpowers/specs/2026-10-02-queqiao-design.md` (§4.4, §4.6, §5.1–5.4, §5.7, §5.8, §6.2–6.5, §6.6 SP2 rows, §7 gateway rows)

## Global Constraints

- Branch `qq/sp2-router-core` cut from `queqiao`, in its own worktree (superpowers:using-git-worktrees); finish with a PR into `queqiao` merged with a merge commit.
- New Go code only under `internal/router/`. Upstream-file changes are limited to: `internal/gateway/rules.go` (`RuleHit.Router` field + hook-related types), `internal/gateway/gateway.go` (hook call-site block + parse-gate extension + `MuxRegister` consulted in `Handler()`), `internal/gateway/otel.go` (two attributes), `internal/usage/usage.go` (`Record` two fields), and `main.go` (wiring only). Nothing else upstream.
- Test command: `go test -tags nogui ./...` and `go vet -tags nogui ./...`.
- The classifier default is `local` (a low-latency local model), `classify_timeout_ms` 1500 (spec §4.6, S5 fallback). Jev is selectable via `classifier: "typesafe/jev-latest"`.
- Policy values (thresholds, escalate_turns, cache_ttl) are config, not code.
- **Import direction:** `internal/router` imports `internal/gateway` (for `Request`/`RuleHit`/`Group` types in the hook callback and `Observe`); `internal/gateway` does NOT import `internal/router`. Wiring (`gateway.SetRouterHook`, `gateway.MuxRegister`) happens in `main.go`.
- **S8 (conducted):** the gateway cannot count tool failures from Codex Responses bodies (`tool_errors` stays 0 — recorder `_EXIT_CODE` only matches Anthropic's `^Exit code: N`). `Sessions.Observe` extracts failures from Anthropic `tool_result.is_error` only; for Responses-protocol sessions the tool-failure signal is expected to be 0 and R3 fires on `dissatisfied` alone. Do not "fix" this in SP2 — it is recorded behaviour, consistent with spec §6.9.
- **S12 (conducted):** Pi derived sessions expose the parent via the header's `parentSession` field (`session_start.reason` is always `startup`, never `fork`). `/lineage` + `ParentOf(firstWords)` is the fallback path only.
- **S11 (conducted):** Claude Code fork/branch requests reuse the parent's session header, and the fork's first user message equals the parent's — so in gateway mode a fork shares the parent's rule key and TurnState. This matches spec §5.8 ("treated as another turn of the main session"); add a test for it in Task 6.

## Types (shared, from spec §5.1)

Put these in `internal/router/types.go`; every later file builds on them. (This is the single source of truth for the shapes in §5.1.)

```go
package router

import "time"

type Tier string // "fast" | "balanced" | "performance"

const (
	TierFast        Tier = "fast"
	TierBalanced    Tier = "balanced"
	TierPerformance Tier = "performance"
)

type Verdict struct {
	Tier           Tier
	TierConfidence float64 // 0–1
	Dissatisfied   float64 // noul 0–1
}

type TurnState struct {
	Tier          Tier
	EscalatedLeft int // turns of escalation left
	LowerStreak   int // consecutive rounds classified below current tier
}

type PolicyInput struct {
	Agent        string        // "main" | "gateway" | subagent type
	PlanMode     bool
	Classified   *Verdict      // nil = classify failed/timed out
	Prev         *TurnState    // nil = first round
	ToolFailures int
	ToolCalls    int
	SinceLast    time.Duration
	Now          time.Time
}

type PolicyConfig struct {
	FixedAgents    map[string]Tier
	DefaultTier    Tier
	TierMin        float64
	DissatisfiedMin float64
	EscalateTurns  int
	CacheTTL       time.Duration
}

type Decision struct {
	Tier   Tier
	Reason string // e.g. "R3-escalate"
	Next   TurnState
}
```

---

### Task 1: `policy.go` — the pure `Choose`

**Files:**
- Create: `internal/router/policy.go`
- Test: `internal/router/policy_test.go`

**Interfaces:**
- Produces: `func Choose(in PolicyInput, cfg PolicyConfig) Decision` implementing §5.2 rules R1–R8, in order, first match wins.

- [ ] **Step 1: Write the failing tests** in `internal/router/policy_test.go`. One focused test per rule plus hysteresis and subagent-statelessness; cover every row of §5.2. Examples:

```go
func TestR1FixedAgent(t *testing.T) {
	cfg := PolicyConfig{FixedAgents: map[string]Tier{"Explore": TierFast}, DefaultTier: TierBalanced}
	d := Choose(PolicyInput{Agent: "Explore", Now: time.Now()}, cfg)
	if d.Tier != TierFast || d.Reason != "R1-fixed" { t.Fatalf("%+v", d) }
}

func TestR2PlanMode(t *testing.T) { /* Agent main, PlanMode true → performance */ }

func TestR3EscalateOnDissatisfied(t *testing.T) {
	// Prev balanced + Classified.Dissatisfied 0.8 → performance, EscalatedLeft=cfg.EscalateTurns
}

func TestR3EscalateOnToolFailures(t *testing.T) {
	// Prev fast, ToolCalls 4, ToolFailures 2 (2*2>=4) → balanced
}

func TestR4EscalationHolds(t *testing.T) {
	// Prev{Tier:balanced, EscalatedLeft:1} + classified fast → stays balanced, EscalatedLeft→0
}

func TestR6HysteresisKeepsWarmCache(t *testing.T) {
	// Prev performance, classified balanced, SinceLast < CacheTTL → stays performance, LowerStreak+1
}

func TestR6LowersWhenCold(t *testing.T) {
	// same but SinceLast >= CacheTTL → balanced, LowerStreak 0
}

func TestR7CarryOnClassifyFailure(t *testing.T) {
	// Prev fast, Classified nil → fast
}

func TestR8Default(t *testing.T) {
	// no Prev, Classified nil → cfg.DefaultTier
}

func TestSubagentIsStateless(t *testing.T) {
	// Agent "Explore" (not fixed) with Prev set must behave as Prev==nil and not require state
}
```

- [ ] **Step 2: Run to see them fail**

Run: `go test -tags nogui ./internal/router/ -run . -v`
Expected: FAIL — `undefined: Choose` (package doesn't compile).

- [ ] **Step 3: Implement `Choose`** in `internal/router/policy.go` per §5.2, exactly: R1 fixed → R2 plan → R3 escalate (dissatisfied OR ≥3 tool calls with failures*2 ≥ calls) → R4 escalation-hold → R5 (classified confidence ≥ TierMin enters R6) → R6 hysteresis (only rule that lowers) → R7 carry → R8 default. "Tier +1" caps at performance. R3/R4/R7/R8 set `EscalatedLeft`/`LowerStreak` to 0 unless stated; only R6 manages `LowerStreak`. Subagent (`Agent` not main/gateway) treats `Prev` as nil.

- [ ] **Step 4: Run tests, iterate until green**

Run: `go test -tags nogui ./internal/router/ -count=1`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add internal/router/policy.go internal/router/policy_test.go internal/router/types.go
git commit -m "feat(router): pure Choose policy with R1-R8 and hysteresis"
```

---

### Task 2: `config.go` — load and validate `router.json`

**Files:**
- Create: `internal/router/config.go`
- Test: `internal/router/config_test.go`

**Interfaces:**
- Produces: `func Load(globalPath, cwd string) (Config, error)`; `type Config` matching §4.6 (`Tiers map[Tier]TierCfg{Group,ClaudeAlias,Criteria}`, `DefaultTier`, `Classifier`, `ClassifyTimeoutMs`, `Thresholds`, `EscalateTurns`, `CacheTTLSeconds`, `FixedAgents`, `Experiment`); `func (c Config) PolicyConfig() PolicyConfig`. Project-level `<cwd>/.queqiao/router.json` may override only `criteria`.

- [ ] **Step 1: Write failing tests**: a valid file loads; a missing tiers key or a bad group errors; thresholds out of [0,1] error; project `criteria` override merges; a project file trying to change `default_tier` is ignored.

- [ ] **Step 2: Run to fail** — `undefined: Load`.
- [ ] **Step 3: Implement** `Load` (read, parse, validate three tiers present + thresholds in range; merge project `criteria` only).
- [ ] **Step 4: Run green.**
- [ ] **Step 5: Commit** `feat(router): load and validate router.json`

---

### Task 3: `session.go`, `hint.go`, `experiment.go`, `events.go` — state

**Files:**
- Create: `internal/router/session.go`, `internal/router/hint.go`, `internal/router/experiment.go`, `internal/router/events.go`
- Test: `session_test.go`, `hint_test.go`, `experiment_test.go`, `events_test.go`

**Interfaces:**
- `type Sessions` with `Get(key)`, `Commit(key, TurnState)`, `Observe(session string, req *gateway.Request)`, `MarkDerived(session, parent string)`, `ParentOf(session, firstWords string) (string, bool)`, `InheritFrom(child, parent string)`; 24 h idle eviction; harness key = session ID, gateway key = session + "|" + firstWords. (Signature per spec §6.2 — router importing `gateway` is allowed by the dependency direction above; `Observe` extracts tool stats from the parsed IR request: Anthropic `tool_result.is_error` counts as failure, Responses `function_call_output` never does, S8.)
- `Hint`/`HintKey`/`Put`/`Take` with 120 s TTL, take-once (spec §6.2). `Put` is called by `/v1/queqiao/turn` when `store_hint` is true (the Codex `user-prompt` hook path); `Take` with the §6.5 matching order (a session+turn_id, b session+prompt-hash, c prompt-hash only) is called by the gateway hook in Task 6 — NOT by `Decide`. Keep the two sides separate.
- `func Arm(session string, e ExperimentConfig) string` — stable hash of session+salt into router|control by `router_percent`.
- `func Append(ev Event) error` — append one JSON line to `~/.config/queqiao/router.jsonl` (mkdir -p the dir).

- [ ] **Step 1: failing tests** for each: round-trip Commit/Get; Observe accumulates tool stats and stamps `SinceLast`; eviction after 24 h (inject a clock); Hint TTL + take-once; Arm determinism + percent boundary; Append writes a parseable line.
- [ ] **Step 2: run to fail; Step 3: implement; Step 4: green; Step 5: commit** `feat(router): sessions, hints, experiment arms, event log`

---

### Task 4: `turnmeta.go` + `classify.go` — turn id and classifier

**Files:**
- Create: `internal/router/turnmeta.go`, `internal/router/classify.go`
- Test: `turnmeta_test.go`, `classify_test.go`

**Interfaces:**
- `func CodexTurnID(h http.Header, body []byte) string` — read `x-codex-turn-metadata` header first; else parse Responses body's `client_metadata["x-codex-turn-metadata"]` and read its `turn_id`; else "".
- `type Classifier interface { Classify(ctx context.Context, q Question) (*Verdict, error) }`; `func NewClassifier(cfg Config, ask func(ctx context.Context, model, body string) (string, error)) Classifier`. For `typesafe/jev-latest` build the §5.3 two-question System One request and post it via magpie's existing decider routing — `provider.RouteDecider(model)` → `Provider.DecideURL(ctx)` (`internal/provider/decide.go:113-130` builds the Vercel TypeSafe `/v1/systemone` URL) — with the provider's key; do NOT call the gateway's `serveSystemOne` (that is the server side, for agents). For a plain `provider/model`, reuse magpie's "answer one number" prompt: ask tier as a number, then dissatisfied as a separate yes/no; confidence = 1 if the answer parsed, else 0. Honour `cfg.ClassifyTimeoutMs`.

- [ ] **Step 1: failing tests** — `CodexTurnID` from header, from body, absent → "". Classifier: fake `ask` returns a Jev-shaped JSON and a numbered answer; timeout returns error.
- [ ] **Step 2–4** as usual; **Step 5** commit `feat(router): codex turn id and classifier`

---

### Task 5: `api.go` — the `/v1/queqiao/*` HTTP handlers

**Files:**
- Create: `internal/router/api.go`
- Test: `api_test.go` (httptest)

**Interfaces:**
- `func Register(mux *http.ServeMux, deps Deps)` where `Deps` carries Config, Sessions, Hint store, Classifier, Arm, Append, and the session-key/first-words helpers. **Auth (spec §6.4):** all endpoints reject non-loopback clients unless LAN sharing is on, in which case a valid gateway key is required — reuse the gateway's existing key/LAN guard helper, do not invent a new one.
- `POST /v1/queqiao/turn` → the §6.4 response; compute `prompt_sha256` (strip `<system-reminder>…</system-reminder>`, trim, LF); `control` arm returns control tier and logs `shadow_tier`; only `400` for missing required fields; classification failure → R7/R8 with `source:"default"`. When `store_hint` is true, `Put` a Hint `{Session, TurnID, PromptHash}` for the gateway hook to `Take` (Task 6).
- `POST /v1/queqiao/feedback` → 204 + event. `GET /v1/queqiao/session?id=` → tier or 404. `POST /v1/queqiao/lineage` → 204. `GET /v1/queqiao/router` → config validity + mapping + last 20 decisions.
- The shared `Decide(ctx, input)` (§6.5) — `/turn` and gateway mode both call it; it owns session-state read/write and §5.8 parent inheritance (`parent_session` > `forked_from_thread_id` > `/lineage`-marked `firstWords`; S12: Pi normally arrives with `parent_session` already set).
- **Degradation (spec §6.2/§7):** if `Load` fails at startup, the gateway still starts; the router group behaves as plain magpie (member order) and `router status` reports the error. Wire this in main: a failed Load → nil router deps → hook stays nil → queqiao-managed groups pass through untouched.

- [ ] **Step 1: failing tests** — turn returns a tier + reason; missing session/prompt → 400; feedback → 204; session lookup 200/404; lineage → 204; router status JSON.
- [ ] **Step 2–4**; **Step 5** commit `feat(router): /v1/queqiao turn, feedback, session, lineage, status`

---

### Task 6: gateway hook — call site + callback + registrar + fields

**Files:**
- Modify: `internal/gateway/rules.go` (add `Router *RouterHit` to `RuleHit`; add the hook-invocation block is NOT here — see gateway.go)
- Modify: `internal/gateway/gateway.go` (the hook call-site block + parse-gate extension + `var MuxRegister []func(*http.ServeMux)` invoked in `Handler()`)
- Modify: `internal/gateway/rules.go` or `gateway.go`: `var routerHook func(h http.Header, body []byte, req *Request, g provider.Group, ms []provider.Member, agent string) *RuleHit` + `func SetRouterHook(f ...)` — the callback TYPE is declared in the gateway package (it references `Request`/`RuleHit`/`Group`/`Member`); the IMPLEMENTATION lives in `internal/router` (allowed: router imports gateway); `main.go` wires `gateway.SetRouterHook(router.NewHook(deps))` and `gateway.MuxRegister = append(gateway.MuxRegister, func(mux){ router.Register(mux, deps) })`.
- Modify: `internal/usage/usage.go` (`Record` += `RouterTier`, `RouterArm` json `router_tier`/`router_arm`) and `internal/gateway/otel.go` (add the two attributes, spec §6.3末段)
- Modify: `main.go` (wiring only)
- Test: `internal/gateway/router_hook_test.go`

**CRITICAL context the original plan missed (verified in code):**

1. `ruleFor` is only *called* at gateway.go:1083 under `if ruleReq != nil && g.Ruled()` — and `ruleReq` is only *parsed* when `g.Ruled() || any member is ruled` (gateway.go:1076-1082). The router group and the three tier groups are NOT ruled, so today neither parse nor ruleFor runs for them. **A hook placed "at the top of ruleFor" would never fire.** The spec §6.3 sentence "挂在 g.Ruled() 之后会被提前返回" assumed ruleFor is invoked for the router group — it is not.
2. Therefore the hook fires at the **call site**, before the existing `if ruleReq != nil && g.Ruled()` block:
   - Extend the parse gate to also parse when `routerHook != nil && isRouterGroup(g)` (router group needs the IR request for `turnIn`/`userText`/`firstWords`).
   - Add: `if routerHook != nil && isGroup && queqiaoManaged(g) { hit = routerHook(r.Header, body, ruleReq, g, ms, agent) }` where `queqiaoManaged` = router group or one of the three tier groups (match by group ID from the loaded router config, not by name heuristics). For tier groups the callback only `Observe`s and returns nil (no reorder); for the router group it runs the full §6.5 flow.
3. **In-turn stickiness reuses the existing `turnRules` table** (spec §6.3): the router-group branch of the callback computes `key := ruleKey(g, h, req)`, checks `turnRules.m[key]` exactly like `ruleFor` does (a stored entry whose `turn` matches `turnIn(req)` and age ≤ stickKeep → reuse `use` as the tier member, return hit without calling Choose), and on a new turn writes `turnRules.m[key] = turnRule{turn, use: tierMember, n: 1, at: now, ...}`. `n: 1` matters: `ruleFor`'s deferred `Then` logic and the trace read `hit.N >= 1`. Return `&RuleHit{Turn: turn, N: 1, Use: tierMember, Router: &RouterHit{Tier, Reason, Source, HintHit}}`.
4. The returned hit flows into the EXISTING `ruled = ruleMembers(hit, ms)` (gateway.go:1084 area — ensure the call-site block assigns `ruled` too) so the chosen tier member is ordered first and `applyRule` handles failover order. `GET /v1/magpie/route` serializes `RuleHit`, so `Router` appears automatically.
5. **`agent` and `ask`**: pass the same `agent` the call site already computed; the callback never calls the group classifier (router groups have none).

**Interfaces:**
- `type RouterHit struct { Tier string; Reason string; Source string; Hint bool }` in the gateway package (json-tagged), field `Router *RouterHit` on `RuleHit`.
- `func SetRouterHook(f func(h http.Header, body []byte, req *Request, g provider.Group, ms []provider.Member, agent string) *RuleHit)` in gateway.
- `var MuxRegister []func(*http.ServeMux)` consulted at the top of `Handler()`.

- [ ] **Step 1: failing test** — `internal/gateway/router_hook_test.go`: with a fake hook installed via `SetRouterHook`, (a) a router-group new-turn request gets the hook's member first and an in-turn follow-up (tool-result message, same `ruleKey`) keeps it WITHOUT a second hook call (assert a call counter); (b) a tier-group request invokes the hook but member order is unchanged; (c) a fork-shaped request (S11: same session header, same firstWords as the parent) lands on the same rule key. Also `Handler()` consults `MuxRegister`: registering a probe function adds a route.
- [ ] **Step 2: run to fail** — `SetRouterHook` undefined.
- [ ] **Step 3: implement** per the 5 points above; keep `ruleFor` itself untouched (the hook lives at the call site, which the spec's intent — "fire before the Ruled gate" — requires; record this as a reviewed deviation from the spec's literal "inside ruleFor" wording, justified by point 1).
- [ ] **Step 4: `go test -tags nogui ./internal/gateway/ ./internal/router/ -count=1` green** (plus `go vet`).
- [ ] **Step 5: commit** `feat(router): hook routing into the gateway`

---

### Task 7: `queqiao router` CLI — init, status, check

**Files:**
- Create: `router_cli.go` (root, alongside `update_cli.go`), `internal/router/presets.go`
- Test: `router_cli_test.go`

**Interfaces:**
- `queqiao router init --preset frontier|anthropic|cn` performs the full §4.5/§6.6 mapping — enumerate every write, this command mutates the user's agent configs: (1) four routing groups in `~/.config/queqiao/providers.json` (§4.4's four `group add` equivalents, via the internal provider group APIs — do NOT shell out to the CLI); (2) `~/.config/queqiao/router.json` (tiers, `classifier:"local"`, thresholds, `fixed_agents` per §4.6, experiment salt random); (3) Codex `~/.codex/config.toml`: ensure `[model_providers.magpie]` exists (it should — magpie wrote it), set `model="group/queqiao"`, rewrite `model_catalog_json` to include `group/queqiao` + the three tier groups, print "restart Codex"; (4) Claude Code: write the §4.5 env block (`ANTHROPIC_DEFAULT_HAIKU_MODEL=group/qq-fast`, etc.) — prefer the project-local settings file in the target project, print what was written; (5) Pi: set the magpie provider default model to `magpie/group/qq-balanced`. Every file write is printed before it happens; `--groups-only` skips steps 3–5 (useful for CI/tests and for SP3/SP4/SP6 dev loops).
- `queqiao router status` prints config validity + tier→group map + last 20 decisions (from `GET /v1/queqiao/router`).
- `queqiao router check` runs the §4.6 smoke test: per §4.2 门槛, 20 tool-carrying requests per primary AND failover member over BOTH Anthropic Messages and OpenAI Responses, plus window-size checks. Requires configured providers with real keys — it is a live, billable test: print cost warning and require `--yes`, exit non-zero on any failure.

- [ ] **Step 1: failing tests** — init writes a valid file + groups; status prints mapping; check passes on a good config and fails loudly on a bad one.
- [ ] **Step 2–4**; **Step 5** commit `feat(router): queqiao router init/status/check`

---

### Task 8: full suite, vet, PR

- [ ] **Step 1: `go vet -tags nogui ./... && go test -tags nogui ./... -count=1`** — every package `ok` except known flaky races in `internal/gateway`.
- [ ] **Step 2: `make cli && bash build/queqiao-smoke.sh ./queqiao`** → `smoke: ok`.
- [ ] **Step 3: push + PR** into `queqiao` with merge commit.

```bash
git push -u origin qq/sp2-router-core
gh pr create -R weiping/queqiao --base queqiao --head qq/sp2-router-core \
  --title "SP2: router core — policy, classifier, sessions, /v1/queqiao API, gateway hook" \
  --body "Implements §6.2–6.5 of docs/superpowers/specs/2026-10-02-queqiao-design.md (plan: docs/superpowers/plans/2026-10-04-queqiao-sp2-router-core.md)."
```

- [ ] **Step 4: wait for checks (macOS flaky races may need a rerun) and merge with a merge commit.**

## Self-review notes

- Every §5.2 rule has a dedicated test (Task 1). The §6.5 invariant (one Choose + one TurnState write per turn, shared `Decide`) is enforced by routing both `/turn` and gateway mode through `router.Decide`.
- The gateway hook is the only upstream-facing change; it reuses `turnRules` so in-turn stickiness is proven by the existing table, per spec §6.3.
- Types in Task 1 are the single definition; later tasks import them (type-consistency self-check passes).

## 复审记录（2026-10-04，实施前）

对照 spec 与上游源码（gateway.go:1074-1084 调用点、rules.go:133 ruleFor、decide.go:113-130、usage.go:24）逐条复核后的修正，已并入上文：

### 阻断级（不修则编译不过或 hook 永不触发）

1. **ruleFor 调用点门控（原 Task 6 核心错误）**：`ruleFor` 仅在 `ruleReq != nil && g.Ruled()` 时被调用，`ruleReq` 仅在 ruled 组时解析。router 组与三档组都不 ruled → 「在 ruleFor 开头插 hook」永不触发。已改为调用点挂钩 + 解析门控扩展（见 Task 6 第 1-2 点）。
2. **import cycle**：spec §6.2 `Observe(session, req *gateway.Request)`（router→gateway）与原文「gateway.go 加一行 router.Register」（gateway→router）矛盾。定方向 router→gateway，接线全部移到 main.go；spec §6.3 第 2 条已同步修订为 `MuxRegister` 注册回调。

### 重要级

3. **S8 传导**：Observe 对 Responses 体的工具失败统计恒 0（recorder 实测），R3 工具升档仅 Anthropic 有效——计划已注明，不「修」。
4. **S12 传导**：Pi 父会话经 header.parentSession 直读，`/lineage`+firstWords 降为兜底。
5. **S11 传导**：CC fork/branch 与父同 ruleKey 共享 TurnState——Task 6 增加对应测试。
6. **hint 分工**：Put 在 `/turn`（store_hint）、Take 在网关 hook（匹配序 a→b→c）——原 Task 5/6 均未写，已补。
7. **`router init` 范围**：原一句带过；实为写 providers.json + router.json + Codex catalog/config + CC env + Pi provider 五个目标，已枚举并加 `--groups-only`。
8. **classifier 复用落地**：明确为 `provider.RouteDecider` + `DecideURL`（decide.go:113-130），非 gateway 的 serveSystemOne（那是 server 端）。

### 次要级

9. `/v1/queqiao/*` 鉴权（本机/网关密钥）补入 Task 5。
10. `usage.Record` 两字段需同时进 OTLP 属性（otel.go），补入 Task 6。
11. config 的「group 必须已存在」校验依赖 provider 组存储，不在 `Load`（纯文件）里做，归启动接线/status。
12. Load 失败降级路径（网关照启、路由组退化、status 报错）写清。
