# queqiao SP2-router-core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `internal/router/` — the gateway-side routing core that picks a tier per turn via a pure `Choose` policy (R1–R8), a classifier (Jev or local model), in-memory session/tool-stats state, hint storage for Codex, session arm assignment, a `router.jsonl` event log, and the `/v1/queqiao/*` HTTP API — and hook it into the magpie gateway (`ruleFor` callback + mux) so requests to the router group get tiered while requests to the tier groups just record stats.

**Architecture:** The policy (`Choose`) is a pure function over `PolicyInput`/`PolicyConfig` — no I/O, the most-tested unit. State lives in-memory in `Sessions` (24 h eviction). Classification is behind a `Classifier` interface: Jev via the gateway's existing `/v1/systemone` client, or a plain model that answers a number (reuse magpie's classify prompt idea). The gateway integration is a single optional `routerHook` injected into `ruleFor()` before its `Ruled()` gate (the router group has no rules, so the hook must run before that early return); the hook observes tier-group traffic and tiers router-group traffic. Everything new is under `internal/router/`; the only upstream-file changes are the `ruleFor` hook, one mux line, the `RuleHit.Router` field, and two `usage.Record` fields.

**Tech Stack:** Go 1.26 (module `github.com/yetone/magpie`, unchanged), stdlib only for router; existing `internal/provider`, `internal/gateway`, `internal/usage` packages.

**Spec:** `docs/superpowers/specs/2026-10-02-queqiao-design.md` (§4.4, §4.6, §5.1–5.4, §5.7, §5.8, §6.2–6.5, §6.6 SP2 rows, §7 gateway rows)

## Global Constraints

- Branch `qq/sp2-router-core` cut from `queqiao`, in its own worktree (superpowers:using-git-worktrees); finish with a PR into `queqiao` merged with a merge commit.
- New Go code only under `internal/router/`. Upstream-file changes are limited to: `internal/gateway/rules.go` (the `routerHook` var + its call), `internal/gateway/gateway.go` (one mux line), `internal/gateway/rules.go` `RuleHit` struct (one field), `internal/usage/usage.go` `Record` (two fields). Nothing else upstream.
- Test command: `go test -tags nogui ./...` and `go vet -tags nogui ./...`.
- The classifier default is `local` (a low-latency local model), `classify_timeout_ms` 1500 (spec §4.6, S5 fallback). Jev is selectable via `classifier: "typesafe/jev-latest"`.
- Policy values (thresholds, escalate_turns, cache_ttl) are config, not code.

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
- `type Sessions` with `Get(key)`, `Commit(key, TurnState)`, `Observe(session string, toolCalls, toolFailures int, at time.Time)`, `MarkDerived(session, parent string)`, `ParentOf(session, firstWords string) (string, bool)`, `InheritFrom(child, parent string)`; 24 h idle eviction; harness key = session ID, gateway key = session + "|" + firstWords.
- `Hint`/`HintKey`/`Put`/`Take` with 120 s TTL, take-once.
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
- `type Classifier interface { Classify(ctx context.Context, q Question) (*Verdict, error) }`; `func NewClassifier(cfg Config, ask func(ctx context.Context, model, body string) (string, error)) Classifier`. For `typesafe/jev-latest` build the §5.3 two-question System One request (reuse the gateway's `/v1/systemone` client via the injected `ask`). For a plain `provider/model`, reuse magpie's "answer one number" prompt: ask tier as a number, then dissatisfied as a separate yes/no; confidence = 1 if the answer parsed, else 0. Honour `cfg.ClassifyTimeoutMs`.

- [ ] **Step 1: failing tests** — `CodexTurnID` from header, from body, absent → "". Classifier: fake `ask` returns a Jev-shaped JSON and a numbered answer; timeout returns error.
- [ ] **Step 2–4** as usual; **Step 5** commit `feat(router): codex turn id and classifier`

---

### Task 5: `api.go` — the `/v1/queqiao/*` HTTP handlers

**Files:**
- Create: `internal/router/api.go`
- Test: `api_test.go` (httptest)

**Interfaces:**
- `func Register(mux *http.ServeMux, deps Deps)` where `Deps` carries Config, Sessions, Hint store, Classifier, Arm, Append, and the session-key/first-words helpers.
- `POST /v1/queqiao/turn` → the §6.4 response; compute `prompt_sha256` (strip `<system-reminder>…</system-reminder>`, trim, LF); `control` arm returns control tier and logs `shadow_tier`; only `400` for missing required fields; classification failure → R7/R8 with `source:"default"`.
- `POST /v1/queqiao/feedback` → 204 + event. `GET /v1/queqiao/session?id=` → tier or 404. `POST /v1/queqiao/lineage` → 204. `GET /v1/queqiao/router` → config validity + mapping + last 20 decisions.
- The shared `Decide(ctx, input)` (§6.5) — `/turn` and gateway mode both call it; it owns session-state read/write and §5.8 parent inheritance.

- [ ] **Step 1: failing tests** — turn returns a tier + reason; missing session/prompt → 400; feedback → 204; session lookup 200/404; lineage → 204; router status JSON.
- [ ] **Step 2–4**; **Step 5** commit `feat(router): /v1/queqiao turn, feedback, session, lineage, status`

---

### Task 6: gateway hook — `ruleFor` callback + mux + fields

**Files:**
- Modify: `internal/gateway/rules.go` (add `var routerHook func(...)` + call at top of `ruleFor`; add `Router *RouterHit` to `RuleHit`)
- Modify: `internal/gateway/gateway.go` (one mux line `router.Register(mux, deps)`; pass header+body+IR to the hook; write `hit.Router`)
- Modify: `internal/usage/usage.go` (`Record` += `RouterTier`, `RouterArm`)
- Test: `internal/gateway/router_hook_test.go`

**Read first (required by spec §6.3):** the full `ruleFor()` (rules.go:133) and its `turnRules` table — the hook must reuse the existing per-turn record so in-turn requests keep the turn's tier without re-calling Choose.

**Interfaces:**
- `var routerHook func(h http.Header, body []byte, req *Request, g provider.Group, ms []provider.Member, agent string) *RuleHit` (package-level, default nil; SP2 injects at startup from `internal/router`).
- Behaviour: tier-group request → only `Sessions.Observe`, return nil (no reorder). Router-group new turn → `Observe`, then `Decide` (gateway mode §5.7) and return a `RuleHit{Use: "group/"+tierGroup, Router:&RouterHit{...}}`; the existing `ruleMembers`/`applyRule` then order the tier member first and the `turnRules` table carries it for the rest of the turn.
- `GET /v1/magpie/route` already serializes `RuleHit`, so `Router` shows up automatically.

- [ ] **Step 1: failing test** — a router-group request gets the chosen tier first and in-turn follow-ups keep it (spec §8 case b); a tier-group request does not reorder.
- [ ] **Step 2: run to fail** — hook var undefined.
- [ ] **Step 3: implement** the hook call in `ruleFor` (before `g.Ruled()` gate), wire `routerHook` from `router`, add the mux line and the two `usage.Record` fields.
- [ ] **Step 4: run `go test -tags nogui ./internal/gateway/ ./internal/router/ -count=1` green.**
- [ ] **Step 5: commit** `feat(router): hook routing into the gateway`

---

### Task 7: `queqiao router` CLI — init, status, check

**Files:**
- Create: `router_cli.go` (root, alongside `update_cli.go`), `internal/router/presets.go`
- Test: `router_cli_test.go`

**Interfaces:**
- `queqiao router init --preset frontier|anthropic|cn` writes `~/.config/queqiao/router.json` (default tiers, `classifier:"local"`, thresholds, fixed_agents per §4.6) and creates the four routing groups.
- `queqiao router status` prints config validity + tier→group map + last 20 decisions (from `GET /v1/queqiao/router`).
- `queqiao router check` runs the §4.6 smoke test (groups exist, a `group/queqiao` request routes, `router.json` valid).

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
