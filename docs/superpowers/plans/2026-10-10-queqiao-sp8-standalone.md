# SP8：脱离 fork，queqiao 独立成伴随进程 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 queqiao 从 magpie fork 改成独立 Go 模块和后台进程 queqiaod，只通过公开 HTTP、CLI 和 usage 数据与官方原版 magpie 协作；选档、复核、校准、报表的行为保持不变。

**Architecture:** 先在 fork 内完成解耦（S8.1–S8.3）：新增 `internal/wire`、`internal/fsutil`、`internal/magpie`、`internal/proxy`、`internal/codexcfg`，router 不再引用任何 magpie 包。fork 期间可以用 magpie 原函数做对照测试。然后换骨（S8.4）：删掉 magpie 代码树，改模块名，CLI 收敛到 `cmd/queqiao`。最后补齐服务、迁移、发布和契约测试（S8.5），并做验收（S8.6）。

**Tech Stack:** Go 1.x（与当前 go.mod 相同），`net/http/httputil`，TypeScript（Claude Code mod、Pi 扩展），GitHub Actions，launchd、systemd --user、Windows 计划任务。

**Spec:** `docs/superpowers/specs/2026-10-10-queqiao-sp8-standalone-design.md`（主），总体规格 `docs/superpowers/specs/2026-10-02-queqiao-design.md` §5、§6.4、§6.5，SP7 规格。

## Global Constraints

- queqiaod 默认监听 `127.0.0.1:3426`；magpie 默认地址 `http://127.0.0.1:3425`。`router.json` 新增 `listen`、`magpie_url` 两个字段覆盖这两个值。
- 新模块名：`github.com/weiping/queqiao`。S8.4 之后 `go list -deps ./...` 里不能出现 `github.com/yetone/magpie`。
- 与 magpie 的交互只在 `internal/magpie` 里，而且只用这些：`POST /v1/systemone`、`POST /v1/chat/completions`、`GET /v1/models`、`GET /v1`、`magpie group add|set`、`magpie groups`、`magpie usage --csv <period>`、`magpie version`、`magpie <agent> <model>`。
- 分类请求的 User-Agent：`queqiao-router/1`。
- 会话头的顺序照搬 magpie：先 `X-Magpie-Session`，再 `x-opencode-session`、`x-session-affinity`、`x-session-id`、`session_id`、`session-id`、`x-claude-code-session-id`；值超过 128 字节截断。
- 代理只改请求体里的 `model` 一个字段，其余字节保持原样；请求体上限 16 MiB，超过就透传不改写；只监听 loopback。
- 路由组 `group/queqiao` 的成员顺序是 `group/qq-balanced,group/qq-perf,group/qq-fast`，`routing=order`。三档分组是 `qq-fast`、`qq-balanced`、`qq-perf`。
- Codex 只写独立的 profile 文件 `~/.codex/queqiao.config.toml`，`config.toml` 不写任何东西；唯一例外是清理 qq-v0.1.x 留在 `config.toml` 里的 `[profiles.queqiao]`、`[model_providers.queqiao]` 和顶层 `model = "group/queqiao"`（Task 0 实测：Codex 0.162 见到同名旧表会拒绝 `-p queqiao`）。
- 插件版本：Claude Code、Codex 插件 0.1.1 → 0.2.0；Pi `@weiping/pi-queqiao` 0.1.2 → 0.2.0。发布 tag `qq-v0.2.0`。
- 发布资产：darwin、linux、windows 各 amd64 与 arm64，共六个二进制，加 `checksums.txt`。
- launchd 标签 `io.github.weiping.queqiao`；systemd 单元 `queqiao.service`；Windows 计划任务名 `queqiao`。日志在 `~/.config/queqiao/logs/queqiaod.log`，按天轮转，保留 7 天。
- 迁移备份目录：`~/.config/queqiao-migration/sp8-<YYYYMMDD-HHMMSS>/`；只移动、不删除。
- 测试命令：fork 期间用 `go test -tags nogui ./...`，S8.4 之后用 `go test ./...`；都在临时 HOME 里跑，`GOPATH`、`GOMODCACHE`、`GOCACHE` 固定到原值。判断为不稳定之前先跑 `-race -count=20`（LESSONS.md）。
- 一个提交一个目标；合并用 `gh pr merge --match-head-commit <sha>`，合并前先在 PR 上写明跑过什么；打 tag 前等 CI Test 在该提交上跑绿（LESSONS.md）。

## Review Focus

1. **magpie 没在运行时用户发起一轮**：期望 Claude Code、Pi 的 `/turn` 照常返回默认档，Codex 经代理收到 502，响应体写明哪个地址不可达，queqiaod 不崩溃（Task 6 测 `TestProxyMagpieDownIs502WithAddress`，Task 4 测 `TestDecideWithMagpieDownFallsBackToDefault`）。
2. **用户已有自己的 Codex profile、http_headers 或 provider 表**：期望 init 只增改 queqiao 自己的两张表，其他内容逐字保留，重复执行结果不变（Task 7 测 `TestCodexInitKeepsUserTablesAndIsIdempotent`）。
3. **迁移时 `~/.config/magpie` 已经有数据，或者 queqiao 目录里有不在清单上的文件**：期望前者整体移进备份，后者算作 magpie 的；`restore` 后两个目录与迁移前逐文件一致（Task 12 测 `TestMigrateWithExistingMagpieDirRoundTrips`）。
4. **CSV 列顺序变化或新增列**：期望按列名解析照常工作；缺少必需列时报错并点名是哪一列（Task 3 测 `TestUsageCSVByNameNotPosition`、`TestUsageCSVMissingColumnNamesIt`）。
5. **Responses 请求里 `model` 不在第一层的位置、或请求体带 BOM、或是超长请求体**：期望只有顶层 `model` 被替换，BOM 原样保留，超过 16 MiB 透传（Task 6 测 `TestProxyRewritesOnlyTopLevelModel`、`TestProxyOversizeBodyPassesThrough`）。

---

## 工作区

worktree `.worktrees/qq-sp8`，分支 `qq/sp8-standalone`，基于 `queqiao`。S8.1–S8.3（Task 0–8）完成后先开一个 PR 合并，那时仍是 fork 形态，CI 不变；S8.4–S8.6（Task 9–15）再开第二个 PR。

## Task 0：先行验证 V1–V4（不写产品代码）

**Files:**
- Modify: `docs/superpowers/notes/spike-results.md`（新增“SP8（V1–V4）”一节）

- [ ] **Step 1: 准备隔离环境**。临时 HOME；从 yetone/magpie 的最新 Release 下载官方 magpie 二进制（不要从源码构建）；装 Codex CLI。记下 magpie 版本和 Codex 版本。
- [ ] **Step 2: V1**。在临时 HOME 的 `~/.codex/config.toml` 里写入 spec §5.6 的两张表，再用官方 magpie 接管 Codex（`magpie codex <某模型>`），切换一次模型，重启 magpie。比对前后的 config.toml：`[model_providers.queqiao]` 和 `[profiles.queqiao]` 必须逐字不变。记录 diff。
- [ ] **Step 3: V2**。用 qq-v0.1.4 二进制起网关，触发一次 Jev 分类，在 `router.jsonl` 里取出请求体；把同一请求体 POST 到官方 magpie 的 `/v1/systemone`（loopback，不带密钥）。比对响应的结构和状态码。
- [ ] **Step 4: V3**。用 qq-v0.1.4 跑一轮 Claude Code 和一轮 Codex；比对 `magpie usage --csv today` 的 `session` 列和 `router.jsonl` 的 `session` 字段是否一致。
- [ ] **Step 5: V4**。起一个只打印请求头和请求体的本机 HTTP 服务（临时脚本，不提交），用 V1 的配置把 `[model_providers.queqiao].base_url` 指向它，`codex -p queqiao exec "hi"`。记录会话头的名字、`client_metadata` 里的 `turn_id` 字段路径。
- [ ] **Step 6: 记录结论**。每项写“成立”或“不成立 → 采用 spec §9 的替代做法”。有任何一项不成立，先回到 spec 更新对应章节，再继续。
- [ ] **Step 7: 提交**

```bash
git add docs/superpowers/notes/spike-results.md
git commit -m "docs: SP8 pre-checks V1–V4"
```

## Task 1：`internal/fsutil`

**Files:**
- Create: `internal/fsutil/fsutil.go`、`internal/fsutil/fsutil_test.go`

**Interfaces:**
- Produces: `func ConfigDir() string`（`$XDG_CONFIG_HOME/queqiao`，没有就是 `~/.config/queqiao`；环境变量 `QUEQIAO_CONFIG_DIR` 优先，供测试用）；`func WriteAtomic(path string, b []byte) error`（同目录临时文件加 rename，保留原文件权限）；`func SetJSON(path string, kv ...KV) error`；`func GetJSON(path, dotted string) (json.RawMessage, bool, error)`；`func DelJSON(path, dotted string) error`；`type KV struct{ Path string; Value any }`（Path 用点分隔）。

- [ ] **Step 1: 写失败的测试**：`TestSetJSONKeepsOtherKeysAndOrder`（已有 `{"a":1,"env":{"X":"y"}}`，设 `env.ANTHROPIC_MODEL` 后 `a` 和 `env.X` 还在，键的顺序不变）；`TestSetJSONOnMissingFileCreatesIt`；`TestUnreadableJSONIsAnErrorNotEmpty`（文件内容是 `{bad`，`SetJSON` 返回错误且文件内容不变，LESSONS“A failed read … means unknown”）；`TestWriteAtomicKeepsMode`（原文件 0600，写后仍是 0600）；`TestConfigDirHonoursXDGAndOverride`。
- [ ] **Step 2: 运行确认失败**：`go test -tags nogui ./internal/fsutil/`，预期编译失败。
- [ ] **Step 3: 实现**。保留键序用 `json.Decoder` 的 token 流做有序 map，不引入第三方库。
- [ ] **Step 4: 运行确认通过**：同上命令，预期 PASS。
- [ ] **Step 5: 提交**：`feat(fsutil): config dir and atomic JSON edits without magpie's edit package`

## Task 2：`internal/wire`，三协议最小解析器

**Files:**
- Create: `internal/wire/wire.go`、`internal/wire/session.go`、`internal/wire/wire_test.go`、`internal/wire/parity_test.go`（fork 期间专用，Task 9 删除）、`internal/wire/testdata/*.json`

**Interfaces:**
- Produces:
  - `type Protocol int`，常量 `Anthropic`、`Responses`、`Chat`；`func ProtocolOf(path string) (Protocol, bool)`（`/v1/messages`、`/messages` 为 Anthropic；`/v1/responses`、`/responses` 为 Responses；`/v1/chat/completions`、`/chat/completions` 为 Chat）
  - `type Part struct{ Kind PartKind; Text string; IsError bool }`，`PartKind` 取 `Text`、`ToolCall`、`ToolResult`、`Other`；`type Message struct{ Role string; Parts []Part }`；`type Request struct{ Model string; Messages []Message }`
  - `func Parse(p Protocol, body []byte) (*Request, error)`
  - `func UserText(r *Request) string`、`func TurnOf(r *Request) (turn int, within bool)`、`func FirstWords(r *Request) string`、`type ToolStats struct{ Calls, Failures int }`、`func Tools(r *Request) ToolStats`
  - `const SessionHeader = "X-Magpie-Session"`、`var SessionHeaders []string`、`func SessionOf(h http.Header) string`
- Consumes: 无

- [ ] **Step 1: 收集 fixture**。从 qq-v0.1.4 的 e2e 测试（`internal/gateway/e2e_cc_test.go`、`e2e_codex_test.go`）和 `internal/gateway/testdata/` 里取真实请求体，三种协议各至少 3 个：首轮、轮内带工具结果、多轮，并且至少有一个工具结果带错误。另外补 4 个边界样本：空 messages、截断的 JSON、只有工具结果、带 `<system-reminder>` 的用户消息。放进 `testdata/`，文件名标明协议和场景。
- [ ] **Step 2: 写对照测试** `TestParityWithGateway`：对每个 fixture，`wire` 的 `UserText`、`TurnOf`、`FirstWords`、`Tools`、`SessionOf` 与 fork 内的 `gateway.UserText`、`gateway.TurnOf`、`gateway.FirstWords`、遍历 `gateway.ToolResult` 的计数、`gateway.SessionOf` 结果完全相等。截断的 JSON 两边都返回错误。
- [ ] **Step 3: 写单元测试**：`TestProtocolOfPaths`；`TestUserTextStripsSystemReminders`；`TestSessionOfOrderAndTruncation`（同时带 `X-Magpie-Session` 和 `x-claude-code-session-id` 时取前者；200 字节截到 128）；`TestToolsCountsErrorsPerProtocol`（Anthropic `is_error: true`、Responses `function_call_output` 和 `custom_tool_call_output`、Chat `role: tool` 各数一遍）。
- [ ] **Step 4: 运行确认失败**：`go test -tags nogui ./internal/wire/`。
- [ ] **Step 5: 实现**。`FirstWords`、`TurnOf`、`UserText` 的算法照搬 `internal/gateway/rules.go:firstWords`、`affinity.go:turnIn`、`classify.go:userText`，每个函数的注释写明来源文件和上游提交号 `0d5fdbb2`。只解析需要的字段，其他字段用 `json.RawMessage` 跳过。
- [ ] **Step 6: 运行确认通过**，再跑 `-race -count=20`。
- [ ] **Step 7: 提交**：`feat(wire): minimal three-protocol parser with gateway parity tests`

## Task 3：`internal/magpie`，适配层

**Files:**
- Create: `internal/magpie/client.go`、`internal/magpie/cli.go`、`internal/magpie/usage.go`、`internal/magpie/*_test.go`、`internal/magpie/testdata/usage.csv`

**Interfaces:**
- Produces:
  - `type Client struct{ BaseURL, Key, Bin string; HTTP *http.Client; Run func(ctx context.Context, name string, args ...string) ([]byte, error) }`（`Run` 默认用 `exec.CommandContext`，测试可替换）
  - `func New(baseURL string) *Client`（`Bin` 从 PATH 找 `magpie`）
  - `func (c *Client) SystemOne(ctx context.Context, body []byte) ([]byte, error)`
  - `func (c *Client) Chat(ctx context.Context, model string, body []byte) (string, error)`（返回 `choices[0].message.content`）
  - `func (c *Client) Models(ctx context.Context) ([]string, error)`、`func (c *Client) Health(ctx context.Context) error`
  - `func (c *Client) GroupAdd(ctx context.Context, id string, members []string) error`（执行 `magpie group add <id> models=<m1,m2…> routing=order`）、`func (c *Client) GroupSet(ctx context.Context, id string, kv ...string) error`、`func (c *Client) Groups(ctx context.Context) ([]string, error)`、`func (c *Client) Version(ctx context.Context) (string, error)`、`func (c *Client) SetAgentModel(ctx context.Context, agent, model string) error`（`magpie <agent> <model>`）
  - `type UsageRow struct{ Time time.Time; Session, RequestedModel, Provider, Model string; Input, Output, CacheRead, CacheWrite int; CostUSD float64; Status int }`
  - `func (c *Client) Usage(ctx context.Context, period string) ([]UsageRow, error)`、`func ParseUsageCSV(r io.Reader) ([]UsageRow, error)`
  - `type Error struct{ Op string; Status int; Msg string }`（实现 `error`，消息里带 Op、状态和 magpie 的错误信息）
  - `const GroupPrefix = "group/"`

- [ ] **Step 1: 录制 fixture**。用 Task 0 的官方 magpie 跑一次 `magpie usage --csv all` 存成 `testdata/usage.csv`（会话 ID 与 key 名换成假值），再记下 `/v1/models`、`/v1/systemone` 错误响应的真实样本。
- [ ] **Step 2: 写失败的测试**：`TestUsageCSVByNameNotPosition`（把列顺序打乱、加一列 `zzz`，解析结果不变）；`TestUsageCSVMissingColumnNamesIt`（去掉 `cost_usd`，错误信息包含 `cost_usd`）；`TestUsageColumnsMatch`：`input_tokens`、`output_tokens`、`cache_read_tokens`、`cache_write_tokens`、`cost_usd`、`status`、`session`、`requested_model`、`provider`、`model`、`time`；`TestSystemOnePostsBodyVerbatim`；`TestChatSendsQueqiaoUserAgent`；`TestGroupAddArgs`（替换 `Run`，断言参数正好是 `group add qq-fast models=a/x,b/y routing=order`）；`TestErrorCarriesMagpieMessage`（magpie 返回 `{"error":{"message":"knows no model"}}` 和 404，错误字符串包含两者）；`TestKeySentAsBearerWhenSet`。
- [ ] **Step 3: 运行确认失败**：`go test -tags nogui ./internal/magpie/`。
- [ ] **Step 4: 实现**。`Health` 用 2 秒超时；其余 HTTP 调用的超时由调用方的 ctx 控制。
- [ ] **Step 5: 运行确认通过**。
- [ ] **Step 6: 提交**：`feat(magpie): adapter over magpie's public HTTP, CLI and usage CSV`

## Task 4：router 去掉对 magpie 的引用

**Files:**
- Modify: `internal/router/session.go`（`Observe`）、`internal/router/hook.go`（改名为 `route.go`，见下）、`internal/router/presets.go`、`internal/router/report.go`、`internal/router/events.go`、`internal/router/prstates.go`、`internal/router/config.go`，以及对应的 `*_test.go`
- Modify: `router_wiring.go`（fork 期间的适配：把 `router.Route` 的结果包成 `gateway.RuleHit`）、`router_cli.go`（报表改用 `magpie.Usage`）

**Interfaces:**
- Consumes: Task 1 `fsutil.ConfigDir`；Task 2 `wire.Request`、`wire.ToolStats`、`wire.SessionOf`、`wire.FirstWords`、`wire.TurnOf`、`wire.UserText`；Task 3 `magpie.UsageRow`、`magpie.GroupPrefix`
- Produces:
  - `func (s *Sessions) Observe(session string, st wire.ToolStats)`；`SinceLast` 改为同一会话上一次 `Decide` 与上一次 `Observe` 中较近的那次到现在的时间
  - `type Route struct{ Tier Tier; Group string; Reason, Source, Arm string; Hint bool }`
  - `type Router struct{…}`、`func NewRouter(deps *Deps) *Router`、`func (r *Router) Route(ctx context.Context, h http.Header, body []byte, req *wire.Request, harness string) (Route, bool)`（`ok=false` 表示配置里没有这个档位）、`func (r *Router) Observe(h http.Header, req *wire.Request)`。逻辑原样搬自 `hook.go` 的 `routeGroup` 和 `hold`，`Route.Group` 带 `group/` 前缀
  - `func (p Preset) Resolve(served []string) (map[Tier][]string, []string)`（`served` 是 `/v1/models` 的 ID 列表，形如 `deepseek/deepseek-chat`）
  - `ReportInput.Records []magpie.UsageRow`；删除 `PriceOf`；成本取 `CostUSD`；`CacheWriteCost` 改为按 `CacheWrite` 占输入类 token 的比例从 `CostUSD` 里分摊，注释写明这是近似
  - `Config` 新增 `Listen string json:"listen"`（默认 `127.0.0.1:3426`）、`MagpieURL string json:"magpie_url"`（默认 `http://127.0.0.1:3425`）

- [ ] **Step 1: 改测试**：`hook_test.go` 改名 `route_test.go`，用例全部保留，只把输入换成 `wire.Request` 和 `http.Header`，期望由 `RuleHit` 改为 `Route`；新增 `TestDecideWithMagpieDownFallsBackToDefault`（ask 回调返回连接错误，`Decide` 返回默认档，`Source == "default"`）；`TestSinceLastUsesLatestOfDecideAndObserve`；`TestResolveUsesServedModels`；`TestOldConfigGetsListenAndMagpieDefaults`；`report_test.go` 的 fixture 换成 `UsageRow`，断言每组的成本等于 fixture 里 `CostUSD` 之和。
- [ ] **Step 2: 运行确认失败**：`go test -tags nogui ./internal/router/`。
- [ ] **Step 3: 实现**。`prstates.go` 改用 `exec.CommandContext`，Windows 上设 `SysProcAttr.HideWindow`（放在 `prstates_windows.go`）。`events.go` 改用 `fsutil.ConfigDir()`。
- [ ] **Step 4: 改 fork 的接线**：`router_wiring.go` 里的 gateway 回调改为调用 `Router.Route` 和 `Router.Observe`，再包成 `gateway.RuleHit{N: 1, Use: route.Group, Router: &gateway.RouterHit{…}}`。fork 的 e2e 必须原样通过。
- [ ] **Step 5: 确认 router 已无 magpie 引用**：`go list -deps ./internal/router/ | grep yetone/magpie`，除 `internal/wire`、`internal/fsutil`、`internal/magpie` 三个新包外，不应再有任何 `yetone/magpie` 的包（S8.4 换模块名后一个都不会有）。
- [ ] **Step 6: 跑全部测试**：`go test -tags nogui ./...`，包括 `internal/gateway` 的 e2e，预期 PASS。
- [ ] **Step 7: 提交**：`refactor(router): depend on wire, fsutil and magpie adapter only`

## Task 5：`router.json` 加载与分类调用改走 HTTP

**Files:**
- Create: `internal/router/ask.go`、`internal/router/ask_test.go`

**Interfaces:**
- Consumes: Task 3 `magpie.Client.SystemOne`、`Chat`
- Produces: `func AskVia(c *magpie.Client) func(ctx context.Context, model, body string) (string, error)`：模型以 `typesafe/` 开头时走 `SystemOne`，其余走 `Chat`。它替代 `router_wiring.go` 的 `askRouter`

- [ ] **Step 1: 写失败的测试**：`TestAskViaRoutesJevToSystemOne`（假 magpie 记录收到的路径，`typesafe/jev-latest` 落在 `/v1/systemone`，`deepseek/deepseek-chat` 落在 `/v1/chat/completions`）；`TestAskViaClassifierParityWithFork`：把 `classify_test.go` 里 Jev 和普通模型的几个用例接到假 magpie 上跑一遍，`Verdict` 与原来相同。
- [ ] **Step 2: 运行确认失败**。
- [ ] **Step 3: 实现**。
- [ ] **Step 4: 运行确认通过**。
- [ ] **Step 5: 提交**：`feat(router): classifier asks magpie over its public endpoints`

## Task 6：`internal/proxy` 与 `queqiao serve`

**Files:**
- Create: `internal/proxy/proxy.go`、`internal/proxy/rewrite.go`、`internal/proxy/proxy_test.go`
- Create: `serve_cli.go`（fork 期间放根目录；Task 9 移到 `cmd/queqiao/`）

**Interfaces:**
- Consumes: Task 2 `wire.ProtocolOf`、`wire.Parse`；Task 4 `Router.Route`、`Router.Observe`；Task 5 `AskVia`
- Produces:
  - `func New(target *url.URL, rt *router.Router, routerGroup string) http.Handler`
  - `func RewriteModel(body []byte, model string) ([]byte, bool)`：只替换顶层 `"model"` 的字符串值，返回是否替换了
  - `func Serve(ctx context.Context, cfg router.Config, deps *router.Deps) error`：一个 mux，挂 `router.Register(mux, deps)` 和代理（`/` 兜底），监听 `cfg.Listen`
  - CLI：`queqiao serve`

- [ ] **Step 1: 写失败的测试**（假 magpie 用 httptest，逐块写 SSE 并 flush）：
  - `TestProxyStreamsChunksAsTheyCome`：假 magpie 每 50ms 发一块，客户端在第二块发出前就收到第一块
  - `TestProxyRewritesOnlyTopLevelModel`：请求体里 `metadata.model` 也等于 `group/queqiao`，只有顶层被改；改写后除 `model` 的值以外，其余字节与原请求逐字节相同；带 BOM 的请求体 BOM 保留
  - `TestProxySameTurnSameTier`、`TestProxyNewTurnDecidesAgain`
  - `TestProxyHintOrder`：会话加 turn_id、会话加提示哈希、只有提示哈希三种情况依次命中
  - `TestProxyPassesOtherModelsUntouched`：`model` 是 `deepseek/deepseek-chat` 时请求体逐字节不变，路由器不被调用
  - `TestProxyOversizeBodyPassesThrough`：16 MiB 加 1 字节，不改写，记一条 `proxy_passthrough` 事件
  - `TestProxyMagpieDownIs502WithAddress`：目标端口没人监听，状态 502，响应体包含 `127.0.0.1:` 加端口
  - `TestProxyKeepsAuthAndActorHeaders`：`Authorization`、`x-openai-actor-authorization`、`X-Magpie-Session` 原样到达假 magpie
  - `TestServePortInUseExplains`：`listen` 端口已被占用时 `Serve` 返回的错误包含端口号和 `router.json` 的 `listen` 字段名
  - `TestServeBadConfigPassesThrough`：`router.json` 无效时 `group/queqiao` 请求不改写，`/v1/queqiao/router` 报配置错误
- [ ] **Step 2: 运行确认失败**：`go test -tags nogui ./internal/proxy/`。
- [ ] **Step 3: 实现**。用 `httputil.ReverseProxy`，`FlushInterval: -1`，`Rewrite` 里不改 Host 以外的请求头；`ErrorHandler` 写 502 和目标地址。读请求体用 `io.LimitReader(r.Body, 16<<20+1)`。`RewriteModel` 用 `json.Decoder` 逐 token 找顶层 `model` 的字节区间，再拼接，不重新序列化。
- [ ] **Step 4: 运行确认通过**，再跑 `-race -count=20 ./internal/proxy/`。
- [ ] **Step 5: 测额外延迟**：`BenchmarkProxyFirstByte`，假 magpie 立即返回首块，对比直连；在 PR 描述里记下 p95 差值，要求不超过 5ms（spec §3.2 第 5 条）。
- [ ] **Step 6: 提交**：`feat(proxy): queqiaod reverse proxy that routes group/queqiao per turn`

## Task 7：`internal/codexcfg`，Codex 配置与模型目录

**Files:**
- Create: `internal/codexcfg/toml.go`、`internal/codexcfg/codex.go`、`internal/codexcfg/catalog.go`、`internal/codexcfg/*_test.go`、`internal/codexcfg/testdata/`
- Modify: `router_cli.go`（`routerInitCodex` 改为调用 `codexcfg.Init`；删 `codexConfigKeys`）

**Interfaces:**
- Produces:
  - `type Table struct{ Name string; Lines []string }`；`func ReadTables(b []byte) (top []string, tables []Table)`；`func SetTable(b []byte, name string, kv [][2]string) []byte`（整表替换或追加到文件末尾，其他行逐字保留）
  - `func Init(codexHome, queqiaoURL string) (changed []string, err error)`：写 spec §5.6 的 `queqiao.config.toml`；从 `config.toml` 的 `[model_providers.magpie]` 复制 `experimental_bearer_token`（有才写）；写 `queqiao-models.json`；先调用 `CleanLegacy`
  - `func CleanLegacy(codexHome string) (bool, error)`：从 `config.toml` 移除 `[profiles.queqiao]`、`[model_providers.queqiao]`（含其子表）和值为 `"group/queqiao"` 的顶层 `model`，改动前把原文件复制为 `config.toml.queqiao-bak`，返回是否改动
  - `func Catalog(ids []string) []byte`

- [ ] **Step 1: 确认目录格式**：读官方 magpie 在 Task 0 环境里写出的 Codex 目录文件，以及当前 Codex 源码中读取 `model_catalog_json` 的结构体，把字段和类型记到 `catalog.go` 的注释里。用 magpie 写出的那份文件作为 `testdata/catalog-from-magpie.json`。
- [ ] **Step 2: 写失败的测试**：`TestCodexInitKeepsUserTablesAndIsIdempotent`（fixture 的 `config.toml` 里有用户自己的 `[profiles.work]`、`[model_providers.magpie]` 及其 `http_headers`、注释和空行；执行两次 `Init`，`config.toml` 逐字不变，第二次 `changed` 为空）；`TestCodexInitCopiesBearerOnlyWhenPresent`；`TestCleanLegacyRemovesOnlyQueqiaoTables`（qq-v0.1.4 风格的 `config.toml`：旧表和顶层 `model = "group/queqiao"` 被移除并留下 `.queqiao-bak`；顶层是别的模型时不动）；`TestCatalogShapeMatchesMagpies`（`Catalog` 输出的字段集合与 `catalog-from-magpie.json` 每个条目的字段集合相同）。
- [ ] **Step 3: 运行确认失败**：`go test -tags nogui ./internal/codexcfg/`。
- [ ] **Step 4: 实现**。只做表级读写，不引入 TOML 库。
- [ ] **Step 5: 运行确认通过**。
- [ ] **Step 6: 真机验证**：在 Task 0 的隔离环境里执行 `Init`，`codex -p queqiao exec` 的请求到达 3426 且 `model` 为 `group/queqiao`。结果写进 PR 描述。
- [ ] **Step 7: 提交**：`feat(codexcfg): queqiao's own Codex provider and profile`

## Task 8：插件端

**Files:**
- Modify: `clients/claude-code/hooks/register.ts`（`gateway_url` 默认 3426）、`clients/claude-code/.claude-plugin/plugin.json`、`clients/claude-code/hooks/register.test.ts`
- Modify: `clients/pi/src/client.ts`、`clients/pi/src/session.ts`、`clients/pi/extensions/queqiao.ts`、`clients/pi/package.json`、`clients/pi/test/*.test.ts`
- Modify: `internal/harness/harness.go`（默认 `http://127.0.0.1:3426`）、`internal/harness/harness_test.go`、`clients/codex/.codex-plugin/plugin.json`
- Modify: `.claude-plugin/marketplace.json`、`.agents/plugins/marketplace.json`（版本号）

**Interfaces:**
- Produces: Pi 的 `/turn` 请求体新增 `tool_calls`、`tool_failures`，值为主会话上一轮工具结果的总数与失败数（统计口径与 Claude Code mod 相同：只数主会话；子代理不计）

- [ ] **Step 1: 写失败的测试**：Claude Code `register.test.ts` 加“未配置 gateway_url 时请求发往 `http://127.0.0.1:3426`”；Pi 加 `reports tool stats of the previous turn`（模拟一轮里 3 次工具调用、2 次失败，下一轮的 `/turn` 请求体里 `tool_calls: 3`、`tool_failures: 2`；第一轮不带这两个字段）；两个插件都加“queqiaod 不可达时用默认档继续，状态显示 queqiao 未运行”；Go 的 `TestHarnessDefaultURLIs3426`；保留现有 `TestMarketplaceListsThePluginsOwnVersion`，版本号改成 0.2.0 后应通过。
- [ ] **Step 2: 运行确认失败**：`claude plugin test clients/claude-code`、`cd clients/pi && npx vitest run`、`go test -tags nogui ./internal/harness/ .`。
- [ ] **Step 3: 实现**。Pi 从工具执行结束事件里读出错标志，具体事件名以 Pi 的类型定义为准，在 `session.ts` 的注释里写明出处。
- [ ] **Step 4: 运行确认通过**。
- [ ] **Step 5: 提交**：`feat(clients): default to queqiaod on 3426; Pi reports tool stats`
- [ ] **Step 6: 开第一个 PR**（Task 0–8，仍是 fork 形态）：在 PR 上写明跑过的命令和结果，CI 三平台绿后用 `--match-head-commit` 合并。插件和 npm 包这时不发布，等 Task 13。

## Task 9：换骨，新模块与目录

**Files:**
- Create: `cmd/queqiao/main.go`
- Move: 根目录的 `router_cli.go`、`hook_cli.go`、`serve_cli.go` 及其测试移到 `cmd/queqiao/`
- Delete: magpie 的全部代码与文档：根目录其余 `*.go`、`internal/` 下除 `router`、`wire`、`magpie`、`proxy`、`codexcfg`、`fsutil`、`harness`、`service`（Task 11 新建）以外的目录、`internal/gateway/router_hook*.go`、`router_wiring*.go`、`internal/wire/parity_test.go`、`scripts/brand_cli.py`、`queqiao_brand_cli_test.go`、`build/`（`queqiao-smoke.sh` 保留到 `scripts/`）、`docs/` 下 magpie 的文档（保留 `docs/superpowers/`、`docs/queqiao-*`）、`AGENTS.md` 与 `LESSONS.md` 里 magpie 专属的部分（见 Step 4）
- Modify: `go.mod`（`module github.com/weiping/queqiao`，`go mod tidy`）、所有 import 路径、`Makefile`、`.gitignore`

**Interfaces:**
- Consumes: Task 1–8 的全部包
- Produces: `cmd/queqiao` 的命令表（Task 10 填实现）；`go build ./cmd/queqiao` 产出唯一的二进制

- [ ] **Step 1: 写失败的测试** `TestNoMagpieDependency`（放在 `cmd/queqiao/deps_test.go`：执行 `go list -deps ./...`，断言输出里没有 `github.com/yetone/magpie`）；`TestCLIListsOnlyQueqiaoCommands`（`queqiao help` 的输出只列 spec §5.9 的命令）。
- [ ] **Step 2: 运行确认失败**。
- [ ] **Step 3: 删除、移动、改 import**；`go mod tidy` 之后，`go.sum` 里应只剩本仓库实际用到的依赖。
- [ ] **Step 4: 改 AGENTS.md 和 LESSONS.md**：删掉 magpie 的订阅插件表、`internal/gui` 等 magpie 专属内容；保留与本仓库仍相关的规则（复现、fixture、红测试、合并、一个提交一个目标）。在 README 开头写明 queqiao 依赖官方 magpie，以及 `main` 分支已停止同步。
- [ ] **Step 5: 运行全部测试**：`go test ./...`、`go vet ./...`，预期 PASS。
- [ ] **Step 6: 提交**：`refactor!: standalone module github.com/weiping/queqiao, magpie tree removed`

## Task 10：CLI 收敛

**Files:**
- Modify: `cmd/queqiao/router_cli.go`、`cmd/queqiao/main.go`
- Create: `cmd/queqiao/status_cli.go`、`cmd/queqiao/update_cli.go`、对应测试

**Interfaces:**
- Consumes: Task 3 `magpie.Client`（`GroupAdd`、`Models`、`SetAgentModel`、`Usage`、`Version`、`Health`）；Task 7 `codexcfg.Init`；Task 4 `Preset.Resolve(served)`
- Produces: 命令 `serve`、`service`（Task 11）、`status`（`router status` 是别名）、`migrate`（Task 12）、`router init|check|report|calibrate`、`hook`、`update`、`version`

- [ ] **Step 1: 写失败的测试**（magpie 用替换过 `Run` 的假 Client 和 httptest）：
  - `TestRouterInitCreatesFourGroupsViaMagpieCLI`：断言依次调用了 `group add qq-fast …`、`qq-balanced`、`qq-perf`，以及 `group add queqiao models=group/qq-balanced,group/qq-perf,group/qq-fast routing=order`
  - `TestRouterInitPiUsesMagpieAgentCommand`：调用的是 `magpie pi group/qq-balanced`
  - `TestRouterInitClaudeCodeWritesSettingsLocal`：内容与 qq-v0.1.4 相同
  - `TestStatusReportsMagpieAndQueqiaod`：magpie 不可达、queqiaod 不可达、分组缺 `qq-perf` 三种情况，每种都有一行说明
  - `TestReportUsesMagpieUsageCSV`：在 `testdata/usage.csv` 和事件 fixture 上，报表的各组会话数、选档分布与 qq-v0.1.4 报表输出一致；成本列等于 CSV 的 `cost_usd` 之和
  - `TestUpdateVerifiesChecksum`：下载的资产哈希对不上时拒绝替换
  - `TestVersionShowsMagpieVersion`
- [ ] **Step 2: 运行确认失败**：`go test ./cmd/queqiao/`。
- [ ] **Step 3: 实现**。`router check` 的在线检查改成经 magpie 的 `/v1/chat/completions` 向每个档位分组各发一次。`update` 从 GitHub Releases API 找最新的 `qq-v*`，按 `GOOS`、`GOARCH` 选资产，核对 `checksums.txt` 后原子替换自身。
- [ ] **Step 4: 运行确认通过**。
- [ ] **Step 5: 提交**：`feat(cli): queqiao commands over the magpie adapter`

## Task 11：`internal/service`

**Files:**
- Create: `internal/service/service.go`、`service_darwin.go`、`service_linux.go`、`service_windows.go`、`service_test.go`、`cmd/queqiao/service_cli.go`
- Create: `internal/service/logrotate.go`（`serve` 用的按天轮转写入器）

**Interfaces:**
- Produces: `func Install(exe string) error`、`func Uninstall() error`、`func Status() (running bool, detail string, err error)`；`func Unit(exe string) (path string, content []byte)`（纯函数，测试用）；`func NewDailyLog(dir string, keep int) io.WriteCloser`

- [ ] **Step 1: 写失败的测试**：`TestUnitContent`（三个平台各一个 golden 文件：launchd plist 含 `io.github.weiping.queqiao`、`RunAtLoad`、`KeepAlive`、`queqiao serve`；systemd 单元含 `Restart=on-failure`；Windows 计划任务 XML 含登录触发）；`TestDailyLogKeepsSeven`（模拟 9 天，目录里剩 7 个文件）；`TestInstallTwiceIsIdempotent`（在临时 HOME 里，只检查文件，不真正 load）。
- [ ] **Step 2: 运行确认失败**。
- [ ] **Step 3: 实现**。macOS 用 `launchctl bootstrap gui/<uid>`，Linux 用 `systemctl --user enable --now`，Windows 用 `schtasks /Create /XML`。
- [ ] **Step 4: 运行确认通过**。
- [ ] **Step 5: CI 真机**：在 macOS 和 Linux runner 上 `queqiao service install`、确认 `curl 127.0.0.1:3426/v1/queqiao/router` 有响应、`queqiao service uninstall`。写进 workflow（Task 14 合并到同一个 workflow 文件）。
- [ ] **Step 6: 提交**：`feat(service): run queqiaod at login on macOS, Linux and Windows`

## Task 12：`queqiao migrate`

**Files:**
- Create: `cmd/queqiao/migrate_cli.go`、`internal/migrate/migrate.go`、`internal/migrate/migrate_test.go`、`internal/migrate/testdata/qq-v0.1.4-layout/`
- Delete: `migrate-from-magpie.sh`（它的 `restore` 仍需可用：在 README 里写明旧备份目录的手动恢复方法）

**Interfaces:**
- Consumes: Task 7 `codexcfg.CleanLegacy`、`codexcfg.Init`；Task 3 `magpie.Client.Version`
- Produces: `type Plan struct{ Steps []Step; Backup string }`；`func Prepare(home string, now time.Time) (Plan, error)`；`func (p Plan) Apply() error`；`func Restore(home string) error`；`var QueqiaoFiles = []string{"router.json", "router.jsonl", "logs", …}`（用当前 `internal/router` 实际写的文件名填满：会话状态、hint、复核结果文件，以 `grep -rn "ConfigDir()" internal/router` 的结果为准）

- [ ] **Step 1: 造 fixture**：在 `testdata/qq-v0.1.4-layout/` 里放一份 qq-v0.1.4 的 `~/.config/queqiao` 布局（`providers.json`、账号文件、`usage.jsonl`、`router.json`、`router.jsonl` 等，内容是假数据），文件清单取自真实安装的 `ls -R`。
- [ ] **Step 2: 写失败的测试**：`TestMigrateMovesMagpieFilesKeepsQueqiaos`；`TestMigrateWithExistingMagpieDirRoundTrips`（先有一个 `~/.config/magpie`；`Apply` 后它在备份的 `magpie-before/` 里；`Restore` 后两个目录与迁移前逐文件一致，包括权限）；`TestMigrateUnknownFileGoesToMagpie`；`TestMigrateDryRunChangesNothing`；`TestMigrateRefusesWhileOldGatewayRuns`（3425 上有响应且 `/v1/queqiao/router` 存在，说明旧的 queqiao 网关还在跑，拒绝执行并说明）；`TestMigrateCleansLegacyCodexTables`；`TestMigrateNeverDeletes`（Apply 加 Restore 全程统计 `os.Remove` 调用次数为 0，用可替换的文件系统函数）。
- [ ] **Step 3: 运行确认失败**：`go test ./internal/migrate/`。
- [ ] **Step 4: 实现**。“只移动”用 `os.Rename`，跨设备时回退为复制后再把源移进备份。macOS 上 `Queqiao.app` 移进备份的 `removed/`，并删除它的登录项（`osascript` 的调用放在可替换的函数里）。
- [ ] **Step 5: 运行确认通过**。
- [ ] **Step 6: 真机演练**：在 Task 0 的隔离环境里装 qq-v0.1.4，造数据，跑 `queqiao migrate --dry-run`、`queqiao migrate --yes`，再用官方 magpie 的 `magpie providers`、`magpie groups`、`magpie usage` 看到原数据；然后 `queqiao migrate restore`，qq-v0.1.4 能照常启动。结果写进 PR。
- [ ] **Step 7: 提交**：`feat(migrate): hand qq-v0.1.x data back to official magpie, reversibly`

## Task 13：安装脚本、发布与文档

**Files:**
- Modify: `install.sh`、`install.ps1`、`.github/workflows/queqiao-release.yml`、`README.md`、`clients/*/README.md`
- Delete: `.github/workflows/queqiao-sync.yml`、`docs/signing.md`、`scripts/setup_apple_secrets.sh`、App 打包相关的 Makefile 目标

**Interfaces:**
- Produces: `qq-v*` tag 触发的 Release：六个二进制加 `checksums.txt`；npm 发布 `@weiping/pi-queqiao@0.2.0`

- [ ] **Step 1: 写失败的测试**：`scripts/install_test.sh`（用 bats 风格的纯 sh 断言）：没有 magpie 时脚本以非零退出并打印官方 magpie 的地址；有 magpie 时下载、校验、调用 `queqiao service install`；检测到 `~/.config/queqiao/providers.json` 时提示 `queqiao migrate --dry-run`。Windows 脚本在 CI 的 Windows runner 上跑同样三种情况。
- [ ] **Step 2: 运行确认失败**。
- [ ] **Step 3: 实现**；release workflow 只构建六个二进制，`checksums.txt` 每行只有“哈希 两个空格 文件名”（qq-v0.1.x 出过带后缀的行，见 d011a4c）。
- [ ] **Step 4: 运行确认通过**。
- [ ] **Step 5: 文档**：README 写安装顺序（先官方 magpie，再 queqiao）、`codex -p queqiao`、`queqiao status`、迁移；插件 README 写默认地址 3426。
- [ ] **Step 6: 提交**：`chore(release): CLI-only releases; retire the upstream sync and app signing`

## Task 14：契约测试与端到端

**Files:**
- Create: `contract/contract_test.go`（build tag `contract`）、`.github/workflows/queqiao-contract.yml`
- Create: `e2e/e2e_test.go`（build tag `e2e`），由原 `internal/gateway/e2e_cc_test.go`、`e2e_codex_test.go` 改写

**Interfaces:**
- Consumes: 全部包；官方 magpie 最新 Release 二进制；假上游（httptest 扮演一个 OpenAI 兼容的 provider，在 magpie 里用 `magpie provider add` 注册）

- [ ] **Step 1: 写契约测试**：`TestContractGroupAddThenList`（`group add qq-fast models=fake/m1 routing=order` 后 `groups` 里有 `qq-fast`，ID 等于传入的名字）；`TestContractModelsListsGroups`；`TestContractSystemOneUnknownDeciderIsClearError`；`TestContractUsageCSVHasRequiredColumns`；`TestContractSessionHeaderReachesUsage`（带 `X-Magpie-Session: s1` 发一次请求，`usage --csv today` 里有 `s1`）；`TestContractFirstWordsStable`（同一请求体两次经 magpie 都落在同一会话，跨两轮的识别与 `wire` 一致）。
- [ ] **Step 2: 写 e2e**：Claude Code 主路径（`/turn` 后以 `group/qq-<档>` 请求 magpie，假上游收到对应成员的模型）；Codex 主路径（UserPromptSubmit hook 存 hint，经代理的请求被改写为 hint 的档位，同一轮的第二个请求同档）；Pi 主路径（带工具统计的 `/turn`，R3-tools 生效）。
- [ ] **Step 3: 本地跑通**：`go test -tags contract ./contract/ -magpie=<路径>`、`go test -tags e2e ./e2e/ -magpie=<路径>`，预期 PASS。
- [ ] **Step 4: workflow**：每天 UTC 01:17 和每次 push 跑契约测试与 e2e；下载官方 magpie 最新 Release；失败时用 `gh issue create --label contract` 开 issue，标题含 magpie 版本，同一版本已有未关闭的 issue 就追加评论。三平台矩阵。
- [ ] **Step 5: 提交**：`test: daily contract tests against official magpie, e2e on queqiaod`

## Task 15：验收与回填

**Files:**
- Modify: `docs/superpowers/specs/2026-10-10-queqiao-sp8-standalone-design.md`（末尾新增“执行结果”一节）、本计划的“执行结果”一节

- [ ] **Step 1: 逐条核对 spec §3.2 的七条成功标准**，每条写出命令和输出摘要。第 3 条报表成本与 qq-v0.1.4 的差异写明原因（magpie 的计价对比 queqiao 原来的价格表）。
- [ ] **Step 2: 真机验收**：本机执行 `queqiao migrate`，三个 harness 各跑一轮真实会话，`queqiao status` 显示三条决策，`magpie usage` 里 `requested_model` 是对应的 `group/qq-<档>`。
- [ ] **Step 3: 开第二个 PR**（Task 9–15）：PR 上写明跑过的测试和真机结果，CI 三平台和契约 workflow 都绿后，用 `--match-head-commit` 合并。
- [ ] **Step 4: 发布**：确认合并提交的 CI Test 已跑绿，由用户推送 tag `qq-v0.2.0`（沙箱推 tag 会被 403 拒绝）；确认六个资产和 `checksums.txt` 齐全；发布 `@weiping/pi-queqiao@0.2.0`，确认 `npm view` 能解析；插件市场清单已是 0.2.0。
- [ ] **Step 5: 回填并提交**：`docs: SP8 results`
