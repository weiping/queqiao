# SP8 设计：脱离 fork，queqiao 独立成伴随进程

- 日期：2026-10-10
- 状态：已实施（PR #21、#22），待本机验收与发版
- 上位规格：[`2026-10-02-queqiao-design.md`](2026-10-02-queqiao-design.md)（下称“总体规格”）与 [`2026-10-07-queqiao-sp7-review-calibration-design.md`](2026-10-07-queqiao-sp7-review-calibration-design.md)（下称“SP7 规格”）。本文只写 SP8 改动的部分，选档策略、复核、校准一律沿用这两份规格。

## 1. 为什么有 SP8

queqiao 现在是 yetone/magpie 的 fork，靠每周把上游 main 合并进 `queqiao` 分支跟上游。上游由 agent 维护，提交非常密：2026-10-07 到 2026-10-10 三天里上游多了 380 个提交。

2026-10-10 用上游 `0d5fdbb2` 对 `queqiao` 分支试合并，结果是 8 个文件冲突、20 处冲突块：

| 文件 | 冲突原因 |
| --- | --- |
| `main.go`、`model_cli.go`、`providers_cli.go`、`plugins_cli.go`、`library_cli.go`、`visible_cli.go` | 品牌替换（帮助文字里的 magpie 改成 queqiao）撞上上游新改的文字 |
| `internal/gui/assets/app.js` | 自定义 provider 表单，上游改了同一段 |
| `internal/davsync/stall_test.go` | 我们修过的不稳定测试，上游用另一种改法也修了 |

路由器本身的接入点（`gateway.go` 的挂钩和 `MuxRegister`、`rules.go` 的 `Router` 字段、`usage.Record` 的两个字段）全部干净合并。可见同步难的根源是“queqiao 必须是一个完整的 magpie”：品牌、桌面 App、GUI、全部 CLI 都得跟着上游走。

读上游源码后的判断（详见 §2）是 magpie 现有的扩展点都放不下路由器，但路由器需要的东西在 magpie 的公开 HTTP 接口、CLI 和 usage 日志里都有。所以 SP8 把 queqiao 从 magpie 里拆出来，做成和官方原版 magpie 并排运行的伴随进程，不再 fork，也不向上游提 PR。

## 2. 依据：magpie 的扩展点与公开接口

以下结论来自上游 `0d5fdbb2` 的源码和 `docs/subsystems/`。

### 2.1 扩展点放不下路由器

| 扩展点 | 能做什么 | 放不下的原因 |
| --- | --- | --- |
| 网关中间件（`internal/middleware`，JS 跑在 moejs 里） | `onRequest` 可以改写或拒绝请求体，包括 `model` | 没有网络能力，`ctx` 里没有请求头（拿不到会话 ID），`state` 只活在单次请求里，`onRequest` 时限 250ms，出错一律放行。Jev 分类要 300 到 1500ms，会话状态、hint、复核都做不了 |
| Provider 插件（`internal/plugin`，Bun 宿主） | 作为上游供应商，请求经路由选中后进它的 `fetch` | 设计目标是替代被弃用的订阅。拿来当路由器要让请求在网关里绕两圈，用量重复计算，还要防回环 |
| 原生分组规则 + Classifier（`grouprule.go`、`classify.go`） | 每轮开头按 Intent 分类，轮内保持；已接入 Jev 的 Sure 与 Effort | 只有单次判断，没有 R1–R8、滞后、hint、复核、校准 |

### 2.2 路由器要用到的公开接口都在

| 路由器需要 | fork 里怎么做 | SP8 改用 |
| --- | --- | --- |
| 调 Jev | `Server.AskDecider` → `systemOne` | `POST /v1/systemone`（`serveSystemOne`，按 `model` 前缀找决策 provider）。loopback 上不需要网关密钥 |
| 调普通分类模型 | `Server.AskChat` → 网关内部 `serve` | `POST /v1/chat/completions` |
| 建档位分组 | `provider.SaveGroup` | `magpie group add <name> models=… routing=order`，改动用 `magpie group set` |
| 解析预设成员 | `provider` 包里的模型表 | `GET /v1/models` |
| 报表的逐请求记录与成本 | `usage.Load` + `catalog.PriceOf` | `magpie usage --csv all`，列里有 `time`、`requested_model`、`provider`、`model`、`cost_usd`、`status`、`session`（`internal/usage/ledger.go` 的 `CSVHeader`） |
| Codex 的 provider 表 | 复用 magpie 写的 `[model_providers.magpie]` | 自己写 profile 文件 `~/.codex/queqiao.config.toml`（§5.6），`config.toml` 一行不写，magpie 的同步碰不到它（V1 实测） |

另一条硬约束：magpie 的包都在 `internal/` 下，别的 Go 模块不能导入。所以独立出来的 queqiao 不能引用 magpie 的任何代码，现在 router 包用到的 `gateway.Request`、`provider.Group`、`catalog.Price`、`usage.Record`、`edit`、`codexcat` 都要换成自己的。

## 3. 目标、成功标准与非目标

### 3.1 目标

| 编号 | 目标 |
| --- | --- |
| G8.1 | **独立模块**：仓库只含 queqiao 自己的代码，模块名 `github.com/weiping/queqiao`，不含、不引用 magpie 源码 |
| G8.2 | **伴随进程 queqiaod**：提供 `/v1/queqiao/*`，并为 Codex 与网关模式做反向代理，按轮改写 `model` 后转发给官方 magpie |
| G8.3 | **只用公开接口**：与 magpie 的全部交互集中在 `internal/magpie` 一个包里，只走 §2.2 列出的 HTTP、CLI 和文件 |
| G8.4 | **行为不变**：R1–R8、复核、校准、实验分组、报表的结果与 qq-v0.1.4 一致 |
| G8.5 | **老用户迁移**：`queqiao migrate` 把 qq-v0.1.x 的数据交还官方 magpie，可预演、可回滚 |
| G8.6 | **契约监控**：CI 每天拿官方最新 magpie 跑契约测试，接口变了自动开 issue，取代每周合并 |

### 3.2 成功标准

1. `go list -deps ./...` 里没有 `github.com/yetone/magpie`，仓库里没有 magpie 的源码目录。
2. 官方 magpie 当前发布版加 queqiaod，三个 harness 的 e2e 全部通过（Claude Code、Codex、Pi 各一条主路径）。
3. 同一份 `router.jsonl` 与 usage 数据，`router report` 和 `router calibrate` 的输出与 qq-v0.1.4 相同。报表成本改用 magpie 的 `cost_usd`，与旧值的差异写进执行结果并说明原因。
4. Codex 经代理时，同一轮内所有请求的 `model` 相同；hint 命中率与 qq-v0.1.4 在同一组 fixture 上一致。
5. 代理对流式响应的首字节额外延迟 p95 不超过 5ms（本机 loopback）。
6. 从 qq-v0.1.4 的目录布局出发，`queqiao migrate` 后官方 magpie 能直接读到原来的 provider、账号、分组和用量；`queqiao migrate restore` 后恢复到迁移前，两次都由测试验证。
7. 契约测试在官方 magpie 最新版上通过，并在 CI 里每日运行。

### 3.3 非目标

| 不做 | 原因 |
| --- | --- |
| 桌面 App、托盘、GUI 路由页 | 用户已决定只保留路由器（2026-10-10）。状态用 `queqiao status` 查看 |
| 向 magpie 提 PR 增加扩展点 | 用户已决定（2026-10-10） |
| Claude Code、Pi 的请求经过代理 | 它们的插件已经自己切换 `group/qq-<档>`；经过代理会多一跳，还要和 magpie 抢 agent 配置的管理权 |
| 在 magpie 的 Routing 视图和 usage 记录里显示档位 | 没有公开接口可写。档位从 `requested_model` 读出，决策细节看 `queqiao status` 和 `router.jsonl` |
| 自带价格表 | 报表直接用 magpie 算好的 `cost_usd` |
| 改选档策略 | SP8 只搬家，策略改动另开子项目 |

## 4. 总体架构

```
            Claude Code / Pi                         Codex（codex -p queqiao）
                 │   │                                    │
   /v1/queqiao/turn   │ model=group/qq-<档>               │ model=group/queqiao
                 ▼   │                                    ▼
          ┌──────────────────────── queqiaod :3426 ─────────────────────┐
          │  /v1/queqiao/*（turn、review、feedback、session、lineage、 │
          │  router）              反向代理（/v1/responses、/v1/messages、│
          │  router.Decide ◄────── /v1/chat/completions，其余路径透传）  │
          │      │ 分类                         │ model 改写为 qq-<档>  │
          └──────┼──────────────────────────────┼──────────────────────┘
                 │ /v1/systemone、/v1/chat/completions
                 ▼                              ▼
          ┌──────────────── 官方 magpie :3425（不改一行）────────────────┐
          │ 分组 qq-fast / qq-balanced / qq-perf / queqiao（兜底）       │
          └──────────────────────────────────────────────────────────────┘
```

- Claude Code、Pi：插件照旧调 `/v1/queqiao/turn` 拿档位，再把请求直接发给 magpie。
- Codex：用 `queqiao` profile 时，请求先到 queqiaod 的代理，代理选档并改写 `model`，再流式转发给 magpie。
- 网关模式：其他 agent 把 base URL 设为 `http://127.0.0.1:3426/v1`，model 设为 `group/queqiao`，和 Codex 走同一条代理路径。
- magpie 里保留 `group/queqiao`，成员依次是 balanced、perf、fast，按 order 路由。插件没能改写模型、或者有人绕过代理直接请求它时，请求落到 balanced，不会失败。

## 5. 组件

### 5.1 仓库布局

`queqiao` 分支删掉 magpie 的代码树，保留并调整为：

```
cmd/queqiao/            main：CLI 与 queqiaod 共用一个二进制
internal/router/        原样保留，替换 magpie 类型（§5.2）
internal/wire/          新：三协议最小解析器
internal/magpie/        新：magpie 适配层（HTTP、CLI、usage 读取）
internal/proxy/         新：反向代理与按轮改写
internal/service/       新：后台服务注册
internal/codexcfg/      新：Codex config.toml 与模型目录的读写
internal/harness/       原样保留（Codex 命令型 hook），默认地址改 3426
internal/fsutil/        新：原子写、JSON 读写（替代 magpie 的 edit 包）
clients/claude-code/    默认地址改 3426
clients/codex/          不变
clients/pi/             默认地址改 3426，新增上报工具统计
.claude-plugin/ .agents/ .claude/jev/ .pi/jev/   不变
install.sh install.ps1  重写（§7.2）
docs/                   保留 superpowers 与 queqiao 文档，删掉 magpie 的
```

`main` 分支的上游镜像、`.github/workflows/queqiao-sync.yml` 退役。`main` 分支保留不删，作为历史参考，README 写明它已停止同步。

### 5.2 `internal/router` 的改动

只换依赖，不改逻辑：

| 现在引用 | 换成 |
| --- | --- |
| `gateway.Request`、`gateway.ToolResult`、`SessionOf`、`FirstWords`、`TurnOf`、`UserText`、`CodexTurnID` 的输入 | `wire.Request` 及同名函数（§5.3） |
| `gateway.RuleHit`、`gateway.RouterHit`、`RouterHookFunc` | 删除；代理直接调用 `router.Decide`，拿到 `Decision` |
| `provider.Group`、`provider.Member`、`provider.GroupPrefix` | 路由器自己的 `GroupRef`（只有 ID），前缀常量移到 `internal/magpie` |
| `usage.Record` | `magpie.UsageRow`（§5.4 的 CSV 行） |
| `catalog.Price`、`PriceOf` | 删除；报表用 `UsageRow.CostUSD` |
| `appdir` | `internal/fsutil` 里的 `ConfigDir()`，仍是 `~/.config/queqiao`（XDG） |

`Sessions.Observe` 改为接收 `wire.ToolStats{Calls, Failures}`，不再遍历 IR 消息。`SinceLast` 改为“同一会话上一次 `Decide` 或上一次被代理观察到的请求”到现在的时间，二者取近。

### 5.3 `internal/wire`：三协议最小解析器

只解析路由器需要的字段，其余原样忽略：

| 函数 | 作用 | 依据的协议字段 |
| --- | --- | --- |
| `Parse(path string, body []byte) (*Request, error)` | 按路径识别协议：`/v1/messages` 为 Anthropic，`/v1/responses` 为 Responses，`/v1/chat/completions` 为 Chat | 各协议的 `messages` 或 `input` |
| `UserText(r)` | 最后一条用户消息的文本，去掉 `<system-reminder>` 块 | Anthropic 的 `content` 文本块；Responses 的 `input_text`；Chat 的 `content` |
| `TurnOf(r)` | 第几轮（用户消息条数）及当前请求是否在轮内（最后一条是工具结果） | 同上 |
| `FirstWords(r)` | 第一条用户消息的前若干个词，算法照搬 magpie 的 `firstWords` | 同上 |
| `Tools(r)` | 本轮工具结果数与失败数 | Anthropic `tool_result.is_error`；Responses `function_call_output` 等；Chat `role=tool` |
| `SessionOf(h http.Header)` | 会话 ID | `X-Magpie-Session` 及 magpie `sessionHeaders` 列出的各家会话头，顺序与 128 字节截断照搬 |
| `CodexTurnID(h, body)` | Codex 的 turn_id | 沿用现在的 `turnmeta.go` |

算法照搬 magpie 的那几个函数时，在代码注释里写明来源文件和上游提交号。fixture 用三家协议的真实请求体，从 qq-v0.1.4 的 e2e 录制里取，并补截断、空体、只有工具结果的情况（LESSONS“Build fixtures from the reporter's literal bytes”）。

### 5.4 `internal/magpie`：适配层

所有和 magpie 的交互只在这里：

```go
type Client struct {
    BaseURL string // 默认 http://127.0.0.1:3425，router.json 的 magpie_url 可改
    Key     string // 可选；magpie 开了局域网鉴权时用的网关密钥
    Bin     string // magpie 可执行文件，默认在 PATH 里找
}

func (c *Client) SystemOne(ctx, body []byte) ([]byte, error)          // POST /v1/systemone
func (c *Client) Chat(ctx, model string, body []byte) (string, error) // POST /v1/chat/completions，UA queqiao-router/1
func (c *Client) Models(ctx) ([]string, error)                       // GET /v1/models
func (c *Client) Health(ctx) error                                   // GET /v1
func (c *Client) GroupAdd(name string, members []string) error      // magpie group add … routing=order
func (c *Client) GroupSet(id string, kv ...string) error             // magpie group set …
func (c *Client) Groups() ([]string, error)                          // magpie groups（只取 ID）
func (c *Client) Usage(since string) ([]UsageRow, error)             // magpie usage --csv <today|7d|30d|all>
```

- CLI 输出按列名取值，不按列序。遇到不认识的列忽略，缺少必需的列就报错并写明是哪一列。
- `UsageRow` 只含报表要用的列：`Time`、`Session`、`RequestedModel`、`Provider`、`Model`、`InputTokens`、`OutputTokens`、`CacheRead`、`CacheWrite`、`CostUSD`、`Status`。
- 失败一律返回带上下文的错误（哪个接口、HTTP 状态、magpie 的错误信息），由调用方决定是降级还是报错。读取或解析失败表示“未知”，不能当作“空”（LESSONS“A failed read … means unknown”）。

### 5.5 `internal/proxy`：反向代理

- 基于 `httputil.ReverseProxy`，`FlushInterval = -1`，原样透传请求头（含 `Authorization`、`x-openai-actor-authorization`、会话头）和响应流。
- 只对 `POST /v1/responses`、`/responses`、`/v1/messages`、`/messages`、`/v1/chat/completions`、`/chat/completions` 读取请求体。请求体里 `model` 等于 `group/queqiao`（router.json 的 `router_group`）时：
  1. `wire.Parse` 解析，`Sessions.Observe` 记录工具统计和时间。
  2. 会话加 FirstWords 加轮次已有决定，就沿用（轮内保持，逻辑搬自 `hook.go` 的 `stickyTurn`）。
  3. 新轮次按总体规格 §6.5 的顺序取 hint（会话加 turn_id，会话加提示哈希，只有提示哈希）。
  4. 取不到 hint 就调 `router.Decide`（harness 记为 `gateway`，Codex 请求记为 `codex`）。
  5. 把请求体的 `model` 改成 `group/qq-<档>`，只改这一个字段，其余字节保持原样（用 JSON 字段级替换，不整体重新序列化）。
- 其他路径、其他 model 一律透传，不读请求体。
- 请求体上限沿用 magpie 的 16 MiB。超过上限直接透传，不改写，记一条 `proxy_passthrough` 事件。
- 只监听 127.0.0.1。局域网访问不在 SP8 范围内。

### 5.6 `internal/codexcfg`：Codex 配置

`queqiao router init` 的 Codex 部分改成写一个独立的 profile 文件 `~/.codex/queqiao.config.toml`（V1/V4 实测修订，2026-10-10）：

```toml
model_provider = "queqiao"
model = "group/queqiao"
model_catalog_json = "<~/.codex/queqiao-models.json 的绝对路径>"

[model_providers.queqiao]
name = "queqiao"
base_url = "http://127.0.0.1:3426/v1"
wire_api = "responses"
experimental_bearer_token = "<从 [model_providers.magpie] 复制，没有就不写>"
http_headers = { "x-openai-actor-authorization" = "magpie" }
```

- Codex 0.162 起，`codex -p <名字>` 读 `~/.codex/<名字>.config.toml`；`config.toml` 里如果还有同名的 `[profiles.<名字>]` 表或 `profile = "<名字>"`，`-p` 直接报错拒绝加载。所以 queqiao 不再往 `config.toml` 写任何东西，`config.toml` 完全归 magpie 和用户。
- 用户用 `codex -p queqiao` 启动。顶层 `model`、`model_provider`、`profile` 都不动。
- qq-v0.1.x 曾在 `config.toml` 写过 `[profiles.queqiao]` 或 `[model_providers.queqiao]`，init 和迁移时把它们从 `config.toml` 移除（先备份），否则 `-p queqiao` 无法加载。
- qq-v0.1.4 的 init 改过顶层 `model = "group/queqiao"`。迁移时如果顶层 `model` 仍是 `group/queqiao`，就删掉这一行，顶层交还 magpie（下次 magpie 同步时写回它的值）。
- 模型目录 `queqiao-models.json` 的格式取自 Codex 自己的数据：实施时先读 magpie 当前写出的目录文件和 Codex 源码里的结构，再写生成函数，用真实 Codex 加载验证（LESSONS“Take another app's field types from that app's own data”）。
- TOML 只做表级读写：读出整个表，改完整表写回，其他内容逐字保留。复用 qq-v0.1.4 `codexConfigKeys` 的行级做法，扩展到表。

### 5.7 `internal/service`：后台服务

`queqiao service install|uninstall|status`：

| 平台 | 方式 | 文件 |
| --- | --- | --- |
| macOS | launchd 用户代理，`RunAtLoad`、`KeepAlive` | `~/Library/LaunchAgents/io.github.weiping.queqiao.plist` |
| Linux | systemd 用户服务，`Restart=on-failure` | `~/.config/systemd/user/queqiao.service` |
| Windows | 计划任务，登录时启动 | 任务名 `queqiao` |

服务执行 `queqiao serve`。`serve` 在前台运行 queqiaod，日志写到 `~/.config/queqiao/logs/queqiaod.log`，按天轮转，保留 7 天。

### 5.8 插件端改动

| 插件 | 改动 | 版本 |
| --- | --- | --- |
| Claude Code `queqiao-router` | `gateway_url` 默认值从 3425 改成 3426 | 0.1.1 → 0.2.0 |
| Pi `@weiping/pi-queqiao` | 默认地址改 3426；`/turn` 里新增 `tool_calls`、`tool_failures`，从 Pi 的工具事件里统计主会话上一轮的结果 | 0.1.2 → 0.2.0 |
| Codex `queqiao-router-codex` | hook 走 `queqiao hook`，`internal/harness` 的默认地址改 3426 | 0.1.1 → 0.2.0 |

`/turn` 的请求和响应格式不变。Pi 新增的两个字段在总体规格 §6.4 里已经存在，只是以前 Pi 不填。

### 5.9 CLI

| 命令 | 状态 |
| --- | --- |
| `queqiao serve` | 新：前台运行 queqiaod |
| `queqiao service install\|uninstall\|status` | 新 |
| `queqiao status` | 新：magpie 和 queqiaod 是否可达、版本、分组是否齐全、最近 20 条决策（合并原 `router status`，旧命令保留为别名） |
| `queqiao migrate [--dry-run] [--yes]`、`queqiao migrate restore` | 新（§7.1） |
| `queqiao router init\|check\|report\|calibrate` | 保留，内部改走 `internal/magpie` |
| `queqiao hook …` | 保留 |
| `queqiao update` | 改为从 GitHub Release 下载 `qq-v*` 的 CLI 资产并校验 checksums.txt；去掉 mirror 与 auto 子命令 |
| `queqiao version` | 保留，同时显示探测到的 magpie 版本 |
| magpie 的其余命令（agent、provider、group、usage 等） | 删除，用户直接用 `magpie` |

## 6. 错误处理

| 情况 | 处理 |
| --- | --- |
| 分类失败或超时 | 沿用 fail-open，落到默认档，`source=default` |
| queqiaod 没在运行 | Claude Code、Pi：`/turn` 失败时用默认档继续，状态栏显示“queqiao 未运行”。Codex 的 queqiao profile 连不上，Codex 自己报错；`queqiao status` 提示启动服务或改用默认 profile |
| magpie 没在运行 | 代理返回 502，响应体写明 magpie 地址不可达；`queqiao status` 写明原因。`/turn` 照常返回档位（分类失败就落默认档） |
| magpie 返回错误 | 代理原样转发状态码和响应体，不重试、不改写 |
| `router.json` 缺失或无效 | queqiaod 照常启动，代理改为纯透传（不改写 `model`，`group/queqiao` 由 magpie 按 order 兜底到 balanced），`queqiao status` 写明原因（沿用总体规格 §6.2 的降级） |
| magpie 的 CLI 输出缺少必需的列，或接口形状变了 | `internal/magpie` 返回明确错误，`report`、`calibrate` 失败退出，不输出不完整的结果；契约测试同步变红（§8） |
| 端口 3426 被占用 | `serve` 退出并说明；`router.json` 可配 `listen` |

## 7. 迁移与发布

### 7.1 `queqiao migrate`

qq-v0.1.x 把 magpie 的数据放在 `~/.config/queqiao`（`appdir.SetName("queqiao")`）。官方 magpie 只读 `~/.config/magpie`。

1. 预检：官方 magpie 已安装（`magpie version` 能运行）；旧的 queqiao App 和 `queqiao` 网关进程已退出，否则提示退出后重试。
2. 备份：`~/.config/queqiao` 和缓存目录整体复制到 `~/.config/queqiao-migration/sp8-<时间>/`。
3. 如果 `~/.config/magpie` 已存在，整体移进备份的 `magpie-before/`。
4. 把 `~/.config/queqiao` 里属于 magpie 的文件复制到 `~/.config/magpie`。属于 queqiao 的文件留在原处：`router.json`、`router.jsonl`、hint 与会话状态文件、`logs/`。判断依据是一张显式的 queqiao 文件清单，清单外的都算 magpie 的。
5. 旧的 `Queqiao.app`（macOS）或安装目录移进备份的 `removed/`；去掉它的登录自启。
6. Codex 配置按 §5.6 改写；Claude Code、Pi 的 agent 配置指向的仍是 3425，不用改。
7. 打印每一步做了什么、备份在哪、怎么回滚。

`--dry-run` 只打印不改动。`queqiao migrate restore` 把最近一次备份放回原位：恢复 `~/.config/queqiao`，`magpie-before/` 放回 `~/.config/magpie`（没有就删掉迁移时建的目录，删前先移进备份），`removed/` 里的东西放回原处。迁移和回滚都不真正删除文件，只移动。

### 7.2 发布

- 资产：六个平台的 CLI 二进制（darwin、linux、windows 各 amd64 与 arm64）和 `checksums.txt`，tag 仍是 `qq-v*`，SP8 发布为 `qq-v0.2.0`。
- 不再发布桌面 App，签名流程（`docs/signing.md`、`scripts/setup_apple_secrets.sh`）删除。
- `install.sh`、`install.ps1`：检查 magpie 是否已安装，没有就提示先装官方 magpie 并给出地址，然后退出；装好 queqiao 后执行 `queqiao service install`；如果检测到 `~/.config/queqiao` 里有 qq-v0.1.x 的 magpie 数据，提示运行 `queqiao migrate --dry-run`。
- 发版前检查：CI Test 在该提交上已完成且通过；六个资产和 checksums 齐全（LESSONS“Don't tag until …”）。

## 8. 测试

| 层 | 内容 |
| --- | --- |
| 单元 | `internal/router` 现有测试全部保留并通过；`wire` 用三协议真实请求体做 fixture；`magpie` 用录制的 CSV 和 HTTP 响应；`codexcfg` 覆盖已有用户表、profile 已存在、顶层 `model` 迁移 |
| 代理 | 假 magpie（httptest）：流式逐块透传、只改 `model` 字段且其余字节不变、同一轮保持同档、新轮次换档、hint 三种命中顺序、超过上限透传、magpie 不可达返回 502 |
| 契约 | 在隔离 HOME 里下载并启动官方 magpie 最新发布版：`group add` 后 `groups` 能看到、`/v1/models` 列出分组、`/v1/systemone` 对未配置的决策 provider 返回 magpie 的明确错误、`usage --csv` 的列包含 §5.4 要的列。每日定时运行，失败时开 issue（标签 `contract`） |
| e2e | 原 `internal/gateway/e2e_cc_test.go`、`e2e_codex_test.go` 改为 queqiaod 加官方 magpie 加假上游：Claude Code 主路径、Codex 经代理的主路径（含 hint）、Pi 主路径 |
| 迁移 | 用 qq-v0.1.4 的目录布局造 fixture，`migrate` 后用官方 magpie 读 provider、账号、分组；`restore` 后与迁移前逐文件比对；已有 `~/.config/magpie` 的情况 |
| 服务 | 三个平台各生成一次服务文件并与期望比对；macOS、Linux 在 CI 上实际安装、启动、停止 |

所有测试在 CI 的三个平台上跑，开 `-race`。

## 9. 先行验证（SP8 开工前完成）

| 编号 | 要验证的事 | 方法 | 不成立时 |
| --- | --- | --- | --- |
| V1 | magpie 同步 Codex 配置时不改 `[profiles.queqiao]` 和它引用的 `[model_providers.queqiao]` | 在隔离 HOME 里装官方 magpie 和 Codex，写入 §5.6 的配置，运行 magpie 的 Codex 同步（切换一次 Codex 模型、重启 magpie），比对配置文件 | 改为每次 queqiaod 启动时检查并修复两张表，`queqiao status` 显示被改动的记录 |
| V2 | `POST /v1/systemone` 在 loopback 不带密钥可用，响应与 fork 内 `AskDecider` 相同 | 同一请求体分别发给 qq-v0.1.4 的 `AskDecider` 和官方 magpie 的 `/v1/systemone`，比对响应 | `internal/magpie` 增加密钥配置，`router init` 引导用户创建网关密钥 |
| V3 | `magpie usage --csv` 的 `session` 与 queqiao 事件日志里的会话 ID 一致 | 跑一轮 Claude Code、一轮 Codex，比对两边的会话 ID | 报表改为按时间窗口加 `requested_model` 关联，并在报表里注明 |
| V4 | Codex 用自定义 provider 时，会话头和 `turn_id` 原样到达代理 | 代理里打印收到的请求头和 `client_metadata` | 回到 hint 的提示哈希路径，并在 Codex 插件 README 里写明 |

## 10. 改动范围与拆分

按顺序做，每步都能单独合并：

1. **S8.1 解耦 router**：新建 `internal/wire`、`internal/fsutil`，把 `internal/router` 对 magpie 的引用全部换掉，此时仍在 fork 里编译，现有测试全绿。
2. **S8.2 适配层与代理**：`internal/magpie`、`internal/proxy`、`queqiao serve`，在 fork 里用假 magpie 测通。
3. **S8.3 Codex 配置与插件**：`internal/codexcfg`、三个插件的默认地址和 Pi 的工具统计。
4. **S8.4 换骨**：新模块名、`cmd/queqiao`，删除 magpie 代码树、`router_hook.go`、`MuxRegister` 和品牌脚本；CLI 收敛到 §5.9。
5. **S8.5 服务、迁移与发布**：`internal/service`、`queqiao migrate`、安装脚本、发布 workflow、契约测试的每日任务；停用 `queqiao-sync.yml`。
6. **S8.6 验收**：§3.2 的七条逐条验证，结果回填到本文末尾。

## 11. 对总体规格的改动

- §3 总体架构：网关从“queqiao 内置的 magpie”改为“官方 magpie”，路由器从网关挂钩改为 queqiaod。
- §6.1 fork 的基础改动、§6.3 两处挂钩：作废。
- §6.4 HTTP 接口：地址从 3425 改为 3426；`tool_calls`、`tool_failures` 由 Claude Code 和 Pi 填写，Codex 与网关模式由代理统计。
- §6.5 网关对提示的消费：执行者从 `routerHook` 改为 queqiaod 的代理，取 hint 的顺序不变。
- §6.6 CLI：按本文 §5.9。
- 总体规格 §6.3 写到的 `usage.Record` 新字段 `router_tier`、`router_arm`：作废，档位从 `requested_model` 读出，实验分组从 `router.jsonl` 关联。

## 12. 风险

| 风险 | 应对 |
| --- | --- |
| magpie 改了 CLI 输出或 HTTP 接口 | 全部交互集中在 `internal/magpie`；契约测试每日运行，变了第二天就知道 |
| magpie 的会话头列表或 `firstWords` 算法变了，导致会话或轮次识别偏差 | 照搬的函数注明上游来源；契约测试里加一条跨两轮的会话识别检查 |
| Codex 用户忘了 `-p queqiao`，请求走 magpie 的默认 provider，没有选档 | `queqiao status` 检查最近的 Codex 会话是否经过代理；README 和 init 输出都写明启动方式 |
| 迁移时文件归属判断错误 | 用显式的 queqiao 文件清单；迁移只复制不删除；`--dry-run` 先看；`restore` 可回滚 |
| 失去桌面 App 后用户不知道 queqiaod 是否在运行 | 插件状态栏显示 queqiao 未运行；`queqiao status`；服务设为自动重启 |

## 执行结果（2026-10-10）

实施分两个 PR：[#21](https://github.com/weiping/queqiao/pull/21)（S8.1–S8.3，仍是 fork 形态，已合并）和 [#22](https://github.com/weiping/queqiao/pull/22)（S8.4–S8.6）。逐任务记录和全部裁定见计划的执行台账。

### 成功标准逐条

| # | 标准 | 结果 |
| --- | --- | --- |
| 1 | 不含、不引用 magpie 源码 | 通过。`TestNoMagpieDependency` 检查 `go list -deps ./...`；magpie 代码树已删 |
| 2 | 官方 magpie 加 queqiaod，三个 harness 的 e2e | 沙箱通过：`e2e/` 对官方 magpie 0.1.1154 发布版（SHA-256 校验）跑 Claude Code、Codex、Pi 三条主路径。真实会话待本机验收 |
| 3 | report、calibrate 与 qq-v0.1.4 一致 | calibrate 只读 `router.jsonl`，计算代码未改，结果相同。report 的会话数、选档分布、升档来源计算不变；成本改用 magpie CSV 的 `cost_usd`，与 qq-v0.1.4 自带价格表的差异来自计价来源不同（magpie 按它对每个 provider 的实际价格计，未定价的模型计入“未定价”而非 0）。缓存写入的成本份额 CSV 不单列，按输入侧 token 占比估算。magpie 导入的本地会话行（status 0）不计成本 |
| 4 | Codex 同一轮同一档，hint 命中与 qq-v0.1.4 一致 | 通过。`TestE2ECodex` 用 magpie 的 usage 记录核对每个请求被改写成的分组；代理单元测试覆盖三种 hint 顺序 |
| 5 | 代理首字节额外延迟 p95 ≤ 5ms | 通过：本机直连 137µs，经 queqiaod 466µs，多 0.33ms |
| 6 | migrate 与 restore 往返 | 通过。测试覆盖已有 magpie 目录、清单外文件、二次 restore、无可迁移内容、网关在跑；沙箱用 qq-v0.1.4 二进制造数据做了真机演练 |
| 7 | 契约测试每日运行 | `queqiao-contract.yml` 三平台每日运行，失败开 issue（标签 `contract`） |

### 先行验证

- V1 成立，但有变化：Codex 0.162 在 `config.toml` 里有 `[profiles.queqiao]` 时拒绝 `-p queqiao`，profile 改为单独的 `~/.codex/queqiao.config.toml`（§5.6 已按此实施，`codexcfg.CleanLegacy` 清理 qq-v0.1.4 写进 `config.toml` 的条目）。
- V2 部分成立：loopback 上不带密钥可以调用 `/v1/systemone`，未配置 Jev 时返回 magpie 的明确 4xx。真实 Jev 回答待本机验证。
- V3 成立（沙箱）：`x-claude-code-session-id` 和 `X-Magpie-Session` 原样出现在 `magpie usage --csv` 的 `session` 列，契约测试固化了这一点。
- V4 成立（沙箱）：Codex 用自定义 provider 时会话头和 turn 元数据原样到达代理。

### 与本文设计不同的地方

- queqiaod 每秒最多检查一次 `router.json` 的大小和修改时间，变了就重新加载，会话状态和 hint 保留；`listen` 改动仍需重启。原因是安装脚本先启动服务、后执行 `router init`。
- 服务重装和 `queqiao update` 会重启 queqiaod。Windows 的计划任务以安装用户身份运行 `serve --detach`（释放控制台，不留窗口），状态读 `Get-ScheduledTask` 的 State。
- `listen` 只接受 loopback 地址，其他地址直接报错（§5.5 原只写“只监听 127.0.0.1”）。
- 迁移把 magpie 的文件“移动”到 `~/.config/magpie`（§7.1 写的是复制；备份里已有完整副本）。另外三条前置检查：3425 上有任何网关在跑就拒绝；queqiao 目录里没有 magpie 的文件就拒绝；同一备份只能 restore 一次。
- 发布资产名为 `queqiao-<os>-<arch>[.exe]`，不再有 Android 构建。

### 未完成与已知限制

- 本机验收（真实 Jev、三个 Agent 的真实会话、迁移真实数据、Windows 普通账户上的计划任务）和 `qq-v0.2.0` 发版、`@weiping/pi-queqiao@0.2.0` 发布，需要在用户本机完成。
- §12 风险表里“`queqiao status` 检查最近的 Codex 会话是否经过代理”没有实现；目前只能从 magpie usage 的 `requested_model` 看出（经过代理的是 `group/qq-<档>`，绕过的是 `group/queqiao`）。
- 终审列出的 13 条次要问题延后处理，见计划执行台账的 `minor (deferred)` 行。

> 2026-10-10 补记：SP9 删除了迁移功能（queqiao 的包从未公开发布，没有需要迁移的用户），本文 §3.2 第 6 条与 §7.1 作废。见 [SP9 规格](2026-10-10-magpie-bridge-sp9-rename-design.md)。
