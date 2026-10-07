# SP7：轮末复核与置信度校准 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 主会话每轮结束后由 Jev（或结构化输出的普通模型）给出“这一轮没解决”的带置信度分数，下一轮据此升档；同时把全部分数记进 `router.jsonl`，提供 `router calibrate` 校准、分组阈值和报表监控。

**Architecture:** 网关侧全部在 `internal/router` 内完成：新增 `/v1/queqiao/review`（后台调分类器，结果存进会话状态），`Choose` 的 R3 增加条件 (c)，`calibrate.go` 是只读事件的纯函数。三个 harness 插件各在轮末“发出即返回”地调用 `/review`。不改任何上游文件。

**Tech Stack:** Go（`internal/router`、`internal/harness/codex`、CLI），TypeScript（Claude Code mod、Pi 扩展），Codex hooks JSON。

**Spec:** `docs/superpowers/specs/2026-10-07-queqiao-sp7-review-calibration-design.md`（主），总体规格 `docs/superpowers/specs/2026-10-02-queqiao-design.md` §5.2、§6.2、§6.4、§9。

## Global Constraints

- Go 测试命令：`go test -tags nogui ./...`；判断为不稳定之前先跑 `go test -tags nogui -race -count=20 -run <Test> ./internal/router/`（LESSONS.md）。
- 测试在临时 HOME 里跑，`GOPATH`、`GOMODCACHE`、`GOCACHE` 固定到原值（docs/code-standards.md）。
- 每个修复或功能都带一个没有它就失败的测试；一个提交一个目标。
- 不改上游文件：总体规格 §6.3 的两处挂钩之外不新增挂钩（总体规格 §11）。
- 默认值：`tier_min` 0.4、`dissatisfied_min` 0.7、`review_min` 0.7、`review_confidence_min` 0.5、`review.mode` `"off"`、`review.timeout_ms` 5000、`review.max_answer_chars` 6000。
- `answer` 截断：超过 `max_answer_chars` 时保留前 2000 字和后 4000 字（按 rune 计）。
- 建议阈值规则：某分段的选低率既 ≥ 相邻一侧各段合计选低率的 2 倍、又至少高出 10 个百分点，才算拐点（用 OpenRouter 原文的示意数据可复现其 0.7）。
- 校准分段固定为 `[0.95,1.00]`、`[0.85,0.95)`、`[0.70,0.85)`、`[0.50,0.70)`、`[0,0.50)`；每段样本 < 30 不给建议阈值，标“样本不足”。
- Reason 字符串：`R3-escalate`（不满）、`R3-tools`（工具失败过半）、`R3-review`（复核）。
- 插件对 `/review` 一律“发出即返回”，失败静默；Codex `Stop` hook 的 `timeout` 为 2。
- 结构化输出的数值范围写在 `description` 里，不用 `minimum`/`maximum`。
- 合并时用 `gh pr merge --match-head-commit <sha>`，合并前在 PR 上写明跑过什么；打 tag 前等 CI Test 在该提交上跑绿（LESSONS.md）。

## Review Focus

1. **复核晚于下一轮到达**：用户回车很快，下一轮 `/turn` 先到。期望下一轮立即返回、不升档；晚到的复核属于已经过去的轮次，直接丢弃，不作用于更后面的轮次（Task 5 测 `TestLateReviewIsDropped`）。
2. **用户中途钉档后又切回路由**：`manual_model_switch` 之后整个会话不再复核。期望切回路由后也不复核，直到新会话（Task 5 测 `TestPinnedSessionIsNotReviewed`）。
3. **旧 router.json 没有新字段**：期望照常加载、补默认值，`review.mode` 为 `off`，不报错（Task 1 测 `TestOldConfigGetsReviewDefaults`）。
4. **分类模型对 `json_schema` 返回 400 或返回非 JSON**：期望本次退回只回编号的老提示词，且同一进程里该模型之后直接走老路径（Task 4 测 `TestPlainSchemaRejectedFallsBackAndRemembers`）。
5. **校准数据里有 control 组和子代理轮次**：期望默认只统计 router 组主会话；`--agent sub` 时才统计子代理（Task 6 测 `TestCalibrateSkipsControlAndSubagents`）。

---

## 工作区

worktree `.worktrees/qq-sp7-review`，分支 `qq/sp7-review`，基于 `queqiao`。完成后 PR 合回 `queqiao`。

## 执行结果（2026-10-07）

Task 0–11 全部执行完毕（inline、逐任务 TDD、每任务一次提交，共 12 个提交，分支 `qq/sp7-review`）。
执行中的判断（Ruling）与真机验收步骤见执行台账 `.superpowers/sdd/2026-10-07-queqiao-sp7-review/progress.md`
与 `docs/queqiao-验收清单.md` 的 SP7 一节。已知未做：Pi 扩展发布到 npm；真机三 Agent 验收（待人工执行）。

## Task 0：先行验证 S14–S17（不写产品代码）

**Files:**
- Modify: `docs/superpowers/notes/spike-results.md`（新增 “SP7（S14–S17）” 一节）

- [ ] **Step 1: S14 Pi 轮末事件**。读当前 `@earendil-works/pi-coding-agent` 的扩展类型定义，找出一轮结束的事件名和其中最后一条 assistant 消息文本的取法；写一个只 `console.error(JSON.stringify(event))` 的临时扩展，`pi -p "hi"` 跑一轮确认。记录事件名与字段路径。
- [ ] **Step 2: S15 Codex `Stop` hook**。读 Codex 当前版本 hooks 文档；装一个 `cat > /tmp/stop.json` 的 `Stop` hook，`codex exec "say hi"` 跑一轮，记录输入里最后一条 assistant 消息的字段名（预期 `last_assistant_message`）以及是否有 `transcript_path`。
- [ ] **Step 3: S16 Jev 复核可用性**。准备 20 组（请求，回复）：10 组明显解决、10 组明显没解决，用 §3.4 的请求各问 3 次，记录每次的 `unresolved`、`confidence`、时延。判定：两组 `unresolved` 中位数相差 ≥ 0.3 视为可用。需要 TypeSafe key，没有时标 `[human]` 留给维护者。
- [ ] **Step 4: S17 结构化输出支持**。对 `deepseek/deepseek-v4-flash`、`moonshot/kimi-k2.5`、`glm/glm-5.3-flash` 各发一次 spec §4 的 `response_format`，记录是否返回合法 JSON。需要对应 key，没有时标 `[human]`。
- [ ] **Step 5: 按结论处理**。S14 或 S15 不成立：在 SP7 spec §3.6 把对应 harness 标为不支持，并跳过 Task 9 或 Task 10。S16 不成立：spec §2.2 第 3 条注明不作为完成标准。提交：

```bash
git add docs/superpowers/notes/spike-results.md docs/superpowers/specs/2026-10-07-queqiao-sp7-review-calibration-design.md
git commit -m "docs(spike): SP7 S14–S17 results"
```

## Task 1：配置，阈值覆盖与 `review` 段

**Files:**
- Modify: `internal/router/config.go`
- Modify: `internal/router/types.go`（`PolicyConfig` 新字段）
- Modify: `router_cli.go`（`router init` 写默认值；`router status` 显示 `review.mode`）
- Test: `internal/router/config_test.go`、`router_cli_test.go`

**Interfaces:**
- Produces:
  - `type Thresholds struct { TierMin, DissatisfiedMin, ReviewMin, ReviewConfidenceMin float64; Overrides []ThresholdOverride }`，JSON 名 `tier_min`、`dissatisfied_min`、`review_min`、`review_confidence_min`、`overrides`
  - `type ThresholdOverride struct { Harness string; Agent string; TierMin, DissatisfiedMin, ReviewMin, ReviewConfidenceMin *float64 }`，`Agent` 取 `main`、`sub`、`gateway`，JSON 名 `harness`、`agent` 加上同名阈值字段，全部 `omitempty`
  - `type ReviewConfig struct { Mode string; TimeoutMs int; MaxAnswerChars int }`，JSON 名 `mode`、`timeout_ms`、`max_answer_chars`；`Config.Review ReviewConfig` JSON 名 `review`
  - `PolicyConfig` 新增 `ReviewMin, ReviewConfMin float64; ReviewMode string`
  - `func (c Config) PolicyConfigFor(harness, agent string) PolicyConfig`：`agent` 为 `main`、`gateway` 原样，其余视为 `sub`；`Overrides` 按“`harness` 与 `agent` 都写了的条目优先，其次只写一项的，同级按出现顺序”逐项覆盖写了的字段。现有 `PolicyConfig()` 保留，等价于 `PolicyConfigFor("", "main")`。

- [ ] **Step 1: 写失败测试**

```go
func TestOldConfigGetsReviewDefaults(t *testing.T) // 无新字段的 router.json：ReviewMin 0.7, ReviewConfidenceMin 0.5, Review.Mode "off", TimeoutMs 5000, MaxAnswerChars 6000，Load 不报错
func TestReviewModeValidated(t *testing.T)        // mode "maybe" → Load 的校验错误含 "review.mode"
func TestOverrideOrder(t *testing.T)              // overrides [{agent:sub,tier_min:0.5},{harness:codex,agent:sub,tier_min:0.6},{harness:codex,review_min:0.65}]：
                                                   // PolicyConfigFor("codex","Explore").TierMin == 0.6；ReviewMin == 0.65
                                                   // PolicyConfigFor("pi","Explore").TierMin == 0.5；PolicyConfigFor("pi","main").TierMin == 0.4
func TestOverrideThresholdRange(t *testing.T)     // override review_min 1.2 → 校验错误
func TestRouterInitWritesReviewDefaults(t *testing.T) // router_cli_test：init 后 router.json 含 "review":{"mode":"off",...} 与 "review_min":0.7
```

- [ ] **Step 2: 运行，确认失败**：`go test -tags nogui ./internal/router/ -run 'TestOldConfigGetsReviewDefaults|TestReviewModeValidated|TestOverride' && go test -tags nogui . -run TestRouterInitWritesReviewDefaults`，预期编译失败或断言失败。
- [ ] **Step 3: 实现** 上述类型、默认值补齐（沿用 `config.go` 第 96 行起的写法）、校验（所有阈值在 [0,1]、mode 三选一）、`PolicyConfigFor`；`router init` 写入新默认值；`router status` 在实验开关下一行打印 `review: <mode>`。
- [ ] **Step 4: 运行，确认通过**，再跑 `go test -tags nogui ./internal/router/ .`。
- [ ] **Step 5: 提交** `feat(router): review config and per-harness threshold overrides`

## Task 2：策略，R3 条件 (c) 与 Reason 拆分

**Files:**
- Modify: `internal/router/types.go`、`internal/router/policy.go`
- Modify: `internal/router/report.go`（凡把 `R3-escalate` 当作“升档”的地方改为前缀 `R3-`）
- Test: `internal/router/policy_test.go`、`internal/router/report_test.go`

**Interfaces:**
- Consumes: Task 1 的 `PolicyConfig.ReviewMin/ReviewConfMin/ReviewMode`
- Produces:
  - `type ReviewVerdict struct { Unresolved, Confidence float64 }`
  - `PolicyInput.Review *ReviewVerdict`
  - `Decision.WouldReview bool`：`ReviewMode == "shadow"` 时，条件 (c) 若会命中则为 true，档位不受影响
  - Reason：(a) `R3-escalate`，(b) `R3-tools`，(c) `R3-review`，同时满足时按 a、b、c 顺序取第一个

- [ ] **Step 1: 写失败测试**（表驱动，`prev = &TurnState{Tier: TierFast}`，默认 `ReviewMin 0.7, ReviewConfMin 0.5`）

```go
// 1 toolsFailing only → Reason "R3-tools", Tier balanced, EscalatedLeft == cfg.EscalateTurns
// 2 Review{0.8,0.6}, mode "act" → "R3-review", balanced
// 3 Review{0.8,0.4}, mode "act" → 不命中 R3（置信度不足），落到 R6/R7
// 4 Review{0.8,0.6}, mode "shadow" → 不命中 R3，WouldReview == true
// 5 Review{0.8,0.6}, mode "off" → 不命中，WouldReview == false
// 6 dissatisfied 0.9 且 Review 命中 → "R3-escalate"
// 7 Prev == nil 且 Review 命中 → 不命中 R3（第一轮）
// 8 Prev.Tier performance，Review 命中 → Tier 仍 performance（封顶），Reason "R3-review"
func TestR3Sources(t *testing.T)
```

`report_test.go` 增加一条：含 `R3-tools`、`R3-review` 的事件，`Aggregate` 的已有档位分布结果与把它们记为 `R3-escalate` 时相同。

- [ ] **Step 2: 运行，确认失败**：`go test -tags nogui ./internal/router/ -run 'TestR3Sources|TestAggregate'`
- [ ] **Step 3: 实现**：在 `policy.go` 第 47 行起的 R3 块里加 `reviewing := cfg.ReviewMode == "act" && in.Review != nil && in.Review.Unresolved >= cfg.ReviewMin && in.Review.Confidence >= cfg.ReviewConfMin`，shadow 时只算 `WouldReview`；Reason 按优先级取。`git grep -n '"R3-escalate"'` 逐处核对报表与测试。
- [ ] **Step 4: 运行，确认通过**；`go test -tags nogui ./internal/router/`。
- [ ] **Step 5: 提交** `feat(router): R3 review condition and per-source reasons`

## Task 3：把分数记全

**Files:**
- Modify: `internal/router/events.go`、`internal/router/api.go`（`Decide` 写事件处）、`internal/router/types.go`（`Verdict.Source`）、`internal/router/classify.go`（填 `Source`）
- Test: `internal/router/api_test.go`、`internal/router/classify_test.go`

**Interfaces:**
- Produces:
  - `Event` 新字段：`TurnID string json:"turn_id,omitempty"`、`ClassifiedTier Tier json:"classified_tier,omitempty"`、`TierConfidence *float64 json:"tier_confidence,omitempty"`、`Dissatisfied *float64 json:"dissatisfied,omitempty"`、`WouldReview bool json:"would_review,omitempty"`、`Unresolved *float64 json:"unresolved,omitempty"`、`ReviewConfidence *float64 json:"review_confidence,omitempty"`、`Classifier string json:"classifier,omitempty"`。指针加 `omitempty` 只省略 nil，0 照常写出。
  - `Verdict.Source string`：Jev 为 `typesafe/jev-latest`；普通模型为模型 ID，走老提示词时追加 `#plain`
  - `DecideInput.TurnID` 已有；网关在空时生成 `"gw-" + <8 位随机十六进制>` 写入事件

- [ ] **Step 1: 写失败测试**

```go
func TestDecideLogsAllScores(t *testing.T)
// fakeClassifier 返回 Verdict{Tier: fast, TierConfidence: 0, Dissatisfied: 0.3, Source: "x/y"}，第二轮
// 断言 decide 事件 JSON 含 "tier_confidence":0、"dissatisfied":0.3、"classified_tier":"fast"、"classifier":"x/y"、非空 "turn_id"
func TestDecideFailedClassifyOmitsScores(t *testing.T)
// 分类返回 error → 事件 JSON 不含 "tier_confidence" 与 "dissatisfied" 键
func TestFirstTurnOmitsDissatisfied(t *testing.T)
// 第一轮（prev nil）→ 不含 "dissatisfied" 键
func TestShadowEventCarriesScores(t *testing.T)
// control 组：shadow 事件同样带上述字段
func TestPlainVerdictSourceMarksPlainPrompt(t *testing.T) // classify_test：老提示词路径的 Source == "<model>#plain"
```

- [ ] **Step 2: 运行，确认失败**：`go test -tags nogui ./internal/router/ -run 'TestDecideLogs|TestDecideFailed|TestFirstTurnOmits|TestShadowEvent|TestPlainVerdictSource'`
- [ ] **Step 3: 实现**：`Decide` 在构造 `ev` 处（`api.go` 第 170 行附近）填新字段；`Dissatisfied` 只在 `prev != nil && classified != nil` 时写；`WouldReview` 取 `decision.WouldReview`。现有 `Confidence` 字段保持原样。
- [ ] **Step 4: 运行，确认通过**；`go test -tags nogui ./internal/router/`。
- [ ] **Step 5: 提交** `feat(router): log raw tier, every score and the classifier per decision`

## Task 4：普通模型分类器的结构化输出

**Files:**
- Modify: `internal/router/classify.go`
- Test: `internal/router/classify_test.go`

**Interfaces:**
- Consumes: Task 3 的 `Verdict.Source`
- Produces:
  - `classifyPlain` 先发一次带 `response_format`（spec §4 的 `tier_verdict` schema，`strict: true`）的请求；`max_tokens` 400
  - `func parseTierVerdict(body string) (*Verdict, error)`：读 `choices[0].message.content` 中的 JSON；`tier` 不在三档内返回 error；`confidence`、`dissatisfied` 越界截到 [0,1]
  - 进程内记忆：`classifier.noSchema map[string]bool`（加锁），某模型返回 4xx 或连续 2 次解析失败后置 true，之后直接走老提示词
  - 第一轮（`PreviousTier == ""`）时结果的 `Dissatisfied` 置 0

- [ ] **Step 1: 写失败测试**（`ask` 用假函数，按请求体是否含 `"response_format"` 分支返回）

```go
func TestPlainSchemaGivesRealConfidence(t *testing.T)    // 返回 {"tier":"balanced","confidence":0.62,"dissatisfied":0.1} → Verdict{balanced, 0.62, 0.1}, Source "m/x"
func TestPlainSchemaClampsOutOfRange(t *testing.T)       // confidence 1.4, dissatisfied -0.2 → 1, 0
func TestPlainSchemaUnknownTierFallsBack(t *testing.T)   // tier "medium" → 本次改发老提示词，Source "m/x#plain"
func TestPlainSchemaRejectedFallsBackAndRemembers(t *testing.T)
// 第一次：带 schema 的请求返回 error("400 ...")，老提示词返回 "2" → Verdict{balanced,1}，Source "#plain"
// 第二次 Classify：假 ask 断言没有收到带 "response_format" 的请求
func TestPlainSchemaFirstTurnZeroDissatisfied(t *testing.T) // PreviousTier "" 且模型给 dissatisfied 0.9 → 0
```

- [ ] **Step 2: 运行，确认失败**：`go test -tags nogui ./internal/router/ -run TestPlainSchema`
- [ ] **Step 3: 实现** 如上；schema 字面量放在包级 `const tierVerdictSchema`，与 spec §4 一字不差（`description` 文案照抄）。
- [ ] **Step 4: 运行，确认通过**；`go test -tags nogui -race -count=20 -run TestPlainSchema ./internal/router/`。
- [ ] **Step 5: 提交** `feat(router): structured-output scores for plain-model classifiers`

## Task 5：复核后端，`/v1/queqiao/review`

**Files:**
- Create: `internal/router/review.go`
- Modify: `internal/router/classify.go`（`Review` 方法）、`internal/router/session.go`、`internal/router/api.go`（注册路由；`Decide` 读复核；`feedback` 标记钉档）
- Test: `internal/router/review_test.go`、`internal/router/api_test.go`（`fakeClassifier` 补 `Review`）

**Interfaces:**
- Consumes: Task 1 `Config.Review`、`PolicyConfigFor`；Task 2 `ReviewVerdict`、`PolicyInput.Review`；Task 3 事件字段
- Produces:
  - `Classifier` 接口新增 `Review(ctx context.Context, q ReviewQuestion) (*ReviewVerdict, error)`
  - `type ReviewQuestion struct { Request, Answer string; ToolCalls, ToolFailures int; Tier Tier }`
  - Jev：spec §3.4 的单题请求（`state` 键 `request`、`answer`、`tool_calls`、`tool_failures`、`tier`；题名 `unresolved`，类型 `noul`，`instructions` 照抄）；普通模型：`json_schema` 名 `review_verdict`，属性 `unresolved`、`confidence`，与 Task 4 同样的退回与记忆
  - `Sessions` 新增：`PutReview(key string, turn int, v ReviewVerdict)`、`TakeReview(key string) *ReviewVerdict`、`Turn(key string) int`（`Commit` 时加 1）、`MarkPinned(session string)`、`Pinned(session string) bool`
  - `func truncateAnswer(s string, max int) string`：超过 `max` rune 时保留前 2000、后 `max-2000` rune
  - HTTP：`POST /v1/queqiao/review`，请求体字段同 spec §3.3；`202` 已接受，`204` 不符合条件，`400` 缺 `session`、`prompt` 或 `answer`
  - `Decide`：`agent` 为 `main` 时调用 `TakeReview(in.Key)` 填 `PolicyInput.Review`，复核分数写入 decide 事件的 `unresolved`、`review_confidence`

复核条件（全部满足才 202）：`Review.Mode != "off"`；该会话 `Arm` 不是 `control`；`!Pinned(session)`；会话当前 `TurnState.Tier != performance`；`Get(key) != nil`。后台用 `context.WithTimeout(context.Background(), Review.TimeoutMs)` 调 `Classify.Review`，成功后 `PutReview(key, turnAtAccept, v)` 并写 `kind:"review"` 事件（带 `turn_id`、`unresolved`、`review_confidence`、`classifier`）。`PutReview` 只在 `Turn(key) == turnAtAccept` 时生效，否则丢弃（下一轮已经开始）。`feedback` 收到 `manual_model_switch` 时调用 `MarkPinned`。

- [ ] **Step 1: 写失败测试**

```go
func TestReviewAcceptedThenEscalates(t *testing.T)
// mode "act"；第 1 轮 /turn → fast；POST /review → 202；等待假分类器完成（channel）
// 第 2 轮 /turn（假分类器 tier fast, conf 0.9）→ Reason "R3-review", tier balanced；router.jsonl 有 1 条 review 事件
func TestReviewShadowLogsWouldReview(t *testing.T)   // mode "shadow"：第 2 轮 Reason 不是 R3-review，decide 事件 would_review == true
func TestReviewOffIs204(t *testing.T)
func TestReviewPerformanceIs204(t *testing.T)        // 上一轮 performance
func TestReviewControlArmIs204(t *testing.T)
func TestPinnedSessionIsNotReviewed(t *testing.T)     // 先 feedback manual_model_switch → 204；之后的轮次仍 204
func TestReviewMissingAnswerIs400(t *testing.T)
func TestLateReviewIsDropped(t *testing.T)
// 假分类器阻塞；POST /review → 202；第 2 轮 /turn 立即返回（断言耗时 < 100ms 且 Reason 不是 R3-review）；
// 放行分类器；第 3 轮 /turn 也不是 R3-review（晚到结果被丢弃）
func TestReviewIsReadOnce(t *testing.T)               // 第 2 轮 R3-review 之后，第 3 轮不再因同一复核升档（R4 保持除外：断言 Reason 为 R4-escalation-hold）
func TestTruncateAnswer(t *testing.T)                 // 7000 个 "a"+末尾 "Z" 截到 6000 rune，首 2000 与尾 4000 保留，末字符为 "Z"
func TestJevReviewRequestShape(t *testing.T)          // classify_test：请求体含 "unresolved" 题、"type":"noul"、state.answer
func TestTurnLatencyUnaffectedByReview(t *testing.T)  // 假分类器复核耗时 2s；mode act 与 off 各跑 50 次 /turn（复核在途），p95 差 < 50ms（spec §2.2 第 2 条）
```

- [ ] **Step 2: 运行，确认失败**：`go test -tags nogui ./internal/router/ -run 'TestReview|TestPinned|TestLateReview|TestTruncate|TestJevReview'`
- [ ] **Step 3: 实现** `review.go` 与上述改动。后台 goroutine 只持有它自己的数据，写会话状态走 `Sessions` 的锁；不在持锁时调分类器或发事件（LESSONS.md “Never send on a channel … while holding a lock”）。
- [ ] **Step 4: 运行，确认通过**；`go test -tags nogui -race -count=20 -run 'TestReview|TestLateReview|TestPinned' ./internal/router/`。
- [ ] **Step 5: 提交** `feat(router): end-of-turn review endpoint feeding R3`

## Task 6：校准，`calibrate.go` 与 `queqiao router calibrate`

**Files:**
- Create: `internal/router/calibrate.go`、`internal/router/calibrate_test.go`
- Modify: `router_cli.go`（`case "calibrate"`）、`internal/router/render.go`（文字表）
- Test: `router_cli_test.go`

**Interfaces:**
- Consumes: Task 3、Task 5 的事件字段
- Produces:
  - `type CalibrateFilter struct { Since time.Time; Score, Harness, Agent string }`，`Score` 为 `""`（全部）、`tier`、`dissatisfied`、`review`；`Agent` 默认 `main`
  - `type CalibrationBand struct { Lo, Hi float64; N int; Share, UnderRate float64 }`
  - `type ScoreCalibration struct { Score string; HigherEscalates bool; Bands []CalibrationBand; Current float64; Suggested *float64; SampleShort bool; EscalateRate, UnderRateKept, ProjEscalateRate, ProjUnderRateKept float64 }`
  - `func Calibrate(events []Event, cfg Config, f CalibrateFilter) []ScoreCalibration`（纯函数）
  - CLI：`queqiao router calibrate [--since 14d] [--score tier|dissatisfied|review] [--harness h] [--agent main|sub] [--csv]`

算法（spec §5.2、§5.3）：
- 轮次按 `(session, 事件顺序)` 排列，只取 `arm != "control"`、harness/agent 匹配的 `decide` 事件；`review` 事件按 `turn_id` 并到所属轮次。
- 第 N 轮“选低了”：第 N+1 轮 `dissatisfied ≥ 0.5`；或第 N 与 N+1 轮之间有 `manual_model_switch` 且其 `extra`/`value` 指向的档位高于第 N 轮（解析不出档位时不算）；或第 N 轮下一轮的 decide 带 `R3-tools`。
- `tier` 分数用全部三项标签；`dissatisfied` 用“第 N 轮为 R3-escalate 且第 N+1 轮仍 dissatisfied ≥ 0.5，或 N 与 N+1 之间手动钉到更高档”；`review` 只用前两项。
- 建议阈值：`HigherEscalates == false`（tier）时从高分段往低走，第一个满足 `UnderRate ≥ 2 × above` 且 `UnderRate − above ≥ 0.10` 的分段（`above` 为其上方各段合计选低率），取其上界；`HigherEscalates == true`（dissatisfied、review）时从低分段往高走，用其下方各段合计做同样比较，取其下界。任一段 `N < 30` 时 `Suggested = nil`、`SampleShort = true`。
- `Proj*`：假设阈值换成 `Suggested` 后，按同样的样本重算升档比例与未升档轮次的选低率；`Suggested == nil` 时为 0。
- `--csv` 列：`session,turn_id,harness,agent,tier,tier_confidence,dissatisfied,unresolved,under_tiered`，不含原话。

- [ ] **Step 1: 写失败测试**（合成事件，手算期望）

```go
func TestCalibrateBandsAndUnderRate(t *testing.T)
// 200 个主会话轮次的 tier 分数：0.97×80（选低 1）、0.9×50（选低 2）、0.78×40（选低 4）、0.6×20（选低 7）、0.3×10（选低 6）
// 断言 Bands[0..4].N == 80,50,40,20,10；UnderRate 依次 0.0125,0.04,0.1,0.35,0.6
func TestCalibrateSuggestsAtJump(t *testing.T)      // OpenRouter 示意数据放大：N 410,270,180,90,50，选低 4,11,20,31,30 → Suggested == 0.70
func TestCalibrateSampleShort(t *testing.T)         // 原数据（有 20、10 的段）：Suggested == nil, SampleShort == true
func TestCalibrateHigherEscalatesDirection(t *testing.T) // review 分数：高分段选低率高，断言建议为某段下界
func TestCalibrateSkipsControlAndSubagents(t *testing.T)
func TestCalibrateLabelsIgnoreOwnScore(t *testing.T) // review 校准的标签不随 R3-tools 变化（加/去掉 R3-tools 事件结果不变）
func TestRouterCalibrateCSV(t *testing.T)            // router_cli_test：--csv 首行为上面的列名，行中不含提示文本
```

- [ ] **Step 2: 运行，确认失败**：`go test -tags nogui ./internal/router/ -run TestCalibrate && go test -tags nogui . -run TestRouterCalibrate`
- [ ] **Step 3: 实现**；文字输出顶部固定一行说明标签只反映用户表达出来的不满（spec §5.2）。
- [ ] **Step 4: 运行，确认通过**。
- [ ] **Step 5: 提交** `feat(router): calibrate scores against next-turn under-tier signals`

## Task 7：报表监控

**Files:**
- Modify: `internal/router/report.go`、`internal/router/render.go`
- Test: `internal/router/report_test.go`、`internal/router/render_test.go`

**Interfaces:**
- Consumes: Task 6 的标签函数（同包内复用，不复制逻辑）、分段常量
- Produces: `ArmReport` 新增 `ScoreBands map[string][]CalibrationBand`（键 `tier`、`dissatisfied`、`review`）、`EscalateRates map[string]float64`（键 `R3-escalate`、`R3-tools`、`R3-review`、`would_review`，分母为该组主会话轮次）、`UnderRateKept float64`（未升档主会话轮次的选低率）

- [ ] **Step 1: 写失败测试**：在 SP5 的合成台账上新建一个只有 10 个主会话轮次的 router 组会话集（R3-escalate 1、R3-tools 1、R3-review 2；其余 6 轮未升档，其中 1 轮带 would_review，3 轮按标签为选低），断言 `EscalateRates["R3-review"] == 0.2`、`EscalateRates["would_review"] == 0.1`、`UnderRateKept == 0.5`；`render_test` 断言文字输出含 “升档率” 与 “未升档轮次的选低率”。
- [ ] **Step 2: 运行，确认失败**：`go test -tags nogui ./internal/router/ -run 'TestAggregate|TestRender'`
- [ ] **Step 3: 实现**；SP5 已有字段与输出不变。
- [ ] **Step 4: 运行，确认通过**；`go test -tags nogui ./internal/router/`。
- [ ] **Step 5: 提交** `feat(router): report score bands, escalation sources and kept-turn under-tier rate`

## Task 8：Claude Code mod 轮末发复核

**Files:**
- Modify: `clients/claude-code/hooks/register.ts`（`turn.complete`）
- Test: `clients/claude-code/hooks/register.test.ts`

**Interfaces:**
- Consumes: Task 5 的 `POST /v1/queqiao/review`
- Produces: `turn.complete` 在 `e.agentId === undefined && !e.isAborted` 且 `stTurn` 非空、`stTurn.tier !== 'performance'` 时，发 `{session, harness:'claude-code', turn_id: e.turnId, prompt: <本轮 turn.start 的 text>, answer: e.answer, tool_calls, tool_failures}`，不 `await` 结果。本轮 `text` 在 `turn.start` 里存进新 atom `stPrompt`；工具统计读 `stToolStats`（它在下一轮 `turn.start` 才清零）。

- [ ] **Step 1: 写失败测试**

```ts
test('turn.complete posts a review for a routed main turn and does not wait', …)
// 假网关记录请求；/review 的响应挂起 5 秒；断言 turn.complete 在 100ms 内返回，请求体 answer === 'ok', prompt === 第一轮 text
test('aborted, performance, subagent and gateway-mode turns post no review', …)
```

- [ ] **Step 2: 运行，确认失败**：`claude plugin test clients/claude-code`
- [ ] **Step 3: 实现**：用现有 `post($, gateway, path, body)` 包一层 `void`，异常吞掉。
- [ ] **Step 4: 运行，确认通过**；`claude plugin validate --strict clients/claude-code`。
- [ ] **Step 5: 提交** `feat(claude-code): post an end-of-turn review`

## Task 9：Pi 扩展轮末发复核（S14 成立时）

**Files:**
- Modify: `clients/pi/extensions/queqiao.ts`、`clients/pi/src/client.ts`
- Test: `clients/pi/test/extension.test.ts`

**Interfaces:**
- Consumes: S14 记录的事件名与回复字段；Task 5 接口
- Produces: `QueqiaoClient.review(body): void`（fire-and-forget，`fetch` 不 await，`.catch(() => {})`）；扩展在 S14 的轮末事件里，`!manualPinned && lastTier !== null && lastTier !== 'performance'` 时调用，`prompt` 取本轮 `before_agent_start` 记下的 `event.prompt`

- [ ] **Step 1: 写失败测试**：vitest 断言轮末事件触发一次 `review`，body 含 `harness:'pi'`、`answer`；`manualPinned` 时不触发；网关不可达时事件处理不抛错。
- [ ] **Step 2: 运行，确认失败**：`cd clients/pi && npx vitest run`
- [ ] **Step 3: 实现。**
- [ ] **Step 4: 运行，确认通过。**
- [ ] **Step 5: 提交** `feat(pi): post an end-of-turn review`；发布 `@weiping/pi-queqiao` 新补丁版本并用 `npm view @weiping/pi-queqiao version` 确认已上 npm。

## Task 10：Codex `Stop` hook（S15 成立时）

**Files:**
- Modify: `internal/harness/codex/codex.go`（`Stop`）、`internal/harness/harness.go`（`Client.Review`）、`hook_cli.go`（`case "stop"`）、`clients/codex/hooks/hooks.json`
- Test: `internal/harness/codex/codex_test.go`、`internal/harness/harness_test.go`

**Interfaces:**
- Consumes: S15 记录的字段名（下文按 `last_assistant_message` 写，S15 不同时以记录为准）；Task 5 接口
- Produces:
  - `func Stop(ctx context.Context, stdin []byte, c *harness.Client) ([]byte, error)`：解析 `session_id`、`turn_id`、`last_assistant_message`；`input` 结构不含本轮 prompt，Stop 处理器从同目录状态文件读取 `UserPrompt` 时写下的 prompt（`UserPrompt` 新增：把 `{session, turn_id, prompt}` 写到 `$TMPDIR/queqiao-codex-<session>.json`）；缺任一项时什么都不做；输出永远为空
  - `func (c *Client) Review(ctx context.Context, body map[string]any)`：1 秒超时，忽略错误
  - `hooks.json` 新增 `"Stop": [{"hooks":[{"type":"command","command":"queqiao hook stop --harness codex","timeout":2}]}]`

- [ ] **Step 1: 写失败测试**

```go
func TestStopPostsReview(t *testing.T)            // 先跑 UserPrompt（写状态文件），再 Stop：假网关收到 /review，prompt 与 answer 正确，harness "codex"
func TestStopWithoutPromptStateIsSilent(t *testing.T)
func TestStopGatewayDownExitsQuietly(t *testing.T) // 网关地址不可达：返回 (nil, nil)，耗时 < 1.5s
func TestCodexHooksJSONHasStop(t *testing.T)      // hooks.json 通过现有 Schema 校验且含 Stop
```

- [ ] **Step 2: 运行，确认失败**：`go test -tags nogui ./internal/harness/... -run 'TestStop|TestCodexHooksJSON'`
- [ ] **Step 3: 实现。** Codex 的 hook 内容变了，用户需要在 `/hooks` 里重新信任，写进 `clients/codex/README.md`。
- [ ] **Step 4: 运行，确认通过**；`go test -tags nogui ./internal/harness/...`。
- [ ] **Step 5: 提交** `feat(codex): Stop hook posts an end-of-turn review`

## Task 11：文档、真机验收与回填

**Files:**
- Modify: `README.md`（配置节加 `review` 与 `overrides`；命令表加 `router calibrate`）
- Modify: `docs/queqiao-验收清单.md`（新增 SP7 一节）
- Modify: SP7 spec（“执行结果”回填）与本计划（顶部“执行结果”）

- [ ] **Step 1: README** 写明默认关闭的原因（回复会发给分类器厂商）与推荐流程：先 `shadow`，`calibrate` 后再 `act`。
- [ ] **Step 2: 真机**（`[human]` 若无对应 Agent 或 key）：三个 Agent 各在 `shadow` 下跑一轮故意答不好的请求（“读取 ./no-such-file.md 并总结”），确认 `router.jsonl` 有 `review` 事件；切 `act` 后同一请求的下一轮 decide 为 `R3-review`。记录到验收清单。
- [ ] **Step 3: 全量测试**：`go test -tags nogui ./...`；`claude plugin test clients/claude-code`；`cd clients/pi && npx vitest run`。红的测试先查是否本分支引起，`-race -count=20` 后再下结论。
- [ ] **Step 4: 提交** `docs: SP7 review and calibration`，开 PR，PR 描述写明跑过的命令和真机结果，CI 绿后 `gh pr merge --match-head-commit <sha>`。
