# 鹊桥（queqiao）总体设计：Agent Harness 模型路由器

- 日期：2026-10-02（2026-10-03 修订：增加 Codex harness 支持；增加 fork 子代理与派生会话的档位继承；确定仓库与分支策略）
- 状态：待审批（审批后按第 11 节交给 `superpowers:writing-plans`）
- 仓库：[weiping/queqiao](https://github.com/weiping/queqiao)，fork 自 [yetone/magpie](https://github.com/yetone/magpie)，MIT 协议
- 分支：`main` 只与上游同步；queqiao 的全部开发在 `queqiao` 分支上进行（第 6.1 节）。本规格以 `main` 的 commit `49ee7d8`（2026-10-03）为基线，规格中引用的上游代码均已在该 commit 上核对
- 对标：LangChain《How to Build a Model Router in the Harness》（事前选档）与 OpenRouter《Confidence Thresholds for Model Escalation Routing》（事后复核）
- 记号：`<p>` 指提供某模型的 Provider ID
- 术语：**仓库 fork** 指 queqiao 从 magpie 分叉出来这件事（第 6.1 节）；**fork 子代理**指继承父会话全部上下文的子代理；**派生会话**指把一段对话复制成一个新会话（Claude Code 的 `/fork`、`/branch`，Codex 的线程分叉，Pi 的 `/fork`、`/tree` 分支）。后两者见第 5.8 节

---

## 1. 目标、成功标准与非目标

### 1.1 目标

queqiao 是一个**放在 Agent Harness 里做决策、在本地网关里执行**的模型路由器，服务 Claude Code、Codex 和 Pi 三个 Agent，分别以 Claude Code 插件、Codex 插件和 Pi 包的形式提供。

| 编号 | 目标 |
| --- | --- |
| G1 | 每个用户轮次开始、模型请求发出之前，选定这一轮的**档位**（`fast` / `balanced` / `performance`）。依据是 harness 才拿得到的上下文：用户原话、计划模式、Agent 类型、项目级判定标准 |
| G2 | 子代理单独选档：从零开始的子代理在派生时单独选档（Claude Code、Codex，以及 Pi 上的 pi-subagents 扩展）；继承父会话上下文的 fork 子代理跟随父会话当前的档位；派生会话的第一轮继承父会话的档位状态（第 5.8 节） |
| G3 | 事后复核（附件“开关二”的 Agent 版）：上一轮的结果信号表明这一档不够时，下一轮自动升档 |
| G4 | 档位与具体模型解耦。换模型只改配置，不改代码，不重装插件 |
| G5 | 失败安全：路由链路的任何一环失败，请求都照常完成；hook 带来的额外延迟不超过 1.5 秒 |
| G6 | 内置线上 A/B 验收，能回答“省了多少钱、质量退了没有” |

### 1.2 成功标准

和固定使用 `performance` 档相比，路由组满足以下三条（数据来自第 9 节的实验）：

1. 每会话成本中位数下降 ≥ 40%（附件中 Open SWE 为 64%）。
2. “以合并 PR 结束的会话占比”差异在统计上不显著（双比例 z 检验，p ≥ 0.05）。
3. 用户手动换模型的会话占比不高于对照组。

另做一组“固定 `fast` 档”的对照，确认路由组质量明显好于全用最便宜档位。附件的结论来自这两组对照放在一起看。

### 1.3 非目标（YAGNI）

| 不做 | 原因 |
| --- | --- |
| 单次调用的“便宜模型自报置信度 → 低于阈值换强模型重答”（OpenRouter 原样） | 编码 Agent 的一轮要跑几十次模型调用，单次调用的答案没法独立打分；Claude Code 后台的小调用（标题、摘要）不值得升级。G3 用“轮次级升档”覆盖同一需求 |
| 离线评测基准（如 DeepSWE） | 附件也只列为后续计划；v1 只做线上 A/B |
| 自训分类器 | 用 TypeSafe Jev，备选为任意小模型 |
| OpenCode、Gemini CLI 等其他 Agent 的插件 | 它们仍可通过网关模式使用路由组（第 5.7 节），但没有 harness 上下文 |
| 修改 magpie 的桌面界面 | 路由状态通过 CLI、状态栏命令和现有的 `/v1/magpie/route` 查看 |
| 多用户服务端部署 | 定位是本机网关；局域网共享沿用 magpie 现有的网关密钥机制 |
| 自动更新 | v1 关闭（第 6.1 节）。fork 不能沿用上游的更新通道 |

---

## 2. 背景与约束（设计依据）

以下事实决定了架构，均已查证（来源见第 13 节）。

**Claude Code**
- 没有任何 hook 能更换主会话的模型。`PreModelSwitch` 只能拦截用户发起的切换。
- `UserPromptSubmit` 的输入包含 `session_id`、`prompt_id`、`prompt`、`transcript_path`、`cwd`、`permission_mode`、`agent_type`，可以拦截提示或补充上下文，但改不了提示本身。
- `PreToolUse` 可以用 `hookSpecificOutput.updatedInput` 改写工具参数。子代理调用的单次 `model` 参数在子代理模型解析顺序中优先级最高，可选值为 `sonnet`、`opus`、`haiku`、`fable`、完整模型 ID 或 `inherit`。
- `PostToolUse`、`PostModelSwitch`、`Stop`、`SubagentStart` 等事件可以用来观察结果。
- fork 子代理：由 `/subtask` 或 Claude 调用 `Agent` 工具时指定 `subagent_type: "fork"` 创建，继承主会话的全部对话、系统提示和工具，**模型与主会话相同**，第一次请求复用主会话的 prompt cache。交互模式下 fork 模式默认开启（v2.1.232 起），`CLAUDE_CODE_FORK_SUBAGENT=0` 关闭。调用时传入的 `model` 参数对 fork 是否生效，文档没有说明。
- 派生会话：`/fork` 把对话复制成一个新的后台会话（v2.1.212 起；v2.1.161–v2.1.211 的 `/fork` 即现在的 `/subtask`）；`/branch` 在当前位置建一个分支并切换进去。`SessionStart` 的 `source` 取值包括 `fork`。
- 插件结构：`.claude-plugin/plugin.json`、`hooks/hooks.json`、`agents/`。hook 进程能拿到 `CLAUDE_PLUGIN_ROOT`、`CLAUDE_PLUGIN_DATA` 等环境变量。插件不能设置主状态栏。插件子代理支持 `model` 字段，但不支持 `hooks`、`mcpServers`、`permissionMode`。

**Pi**
- 扩展可调用 `pi.setModel()` 和 `pi.setThinkingLevel()`。
- 可订阅 `before_agent_start`（拿到 `prompt` 与 `systemPrompt`）、`before_provider_headers`（改请求头）、`before_provider_request`（替换请求体）、`model_select`（含 `source`）、`tool_result`、`session_start` 等事件。
- 包通过 `package.json` 的 `pi.extensions` 声明，`.ts` 直接加载，用 `pi install npm:<pkg>` 安装；运行时依赖放在 `dependencies` 里。
- `tool_call` 事件的 `event.input` 可以直接修改，用来改写任意工具的参数。`session_start` 的 `reason` 取值包括 `fork`。
- 会话分支：`/fork`、`/tree` 生成新的会话文件；会话可以记录父会话（`newSession({ parentSession })`）；模型以 `ModelChangeEntry` 记在会话里。
- Pi 核心没有子代理。常用的第三方扩展 [tintinweb/pi-subagents](https://github.com/tintinweb/pi-subagents) 在同一进程内运行子代理，提供 `Agent` 工具，调用时可传 `model`、`thinking`，以及 `inherit_context`（把父对话放进子代理上下文）。

**Codex**
- 和 Claude Code 一样，hook 换不了主会话的模型。hook 输出能做的是拦截、补充上下文（`additionalContext`）、审批决定，以及在 `PreToolUse` 里用 `updatedInput` 改写工具参数。
- 所有 hook 的输入都有 `session_id`、`transcript_path`、`cwd`、`hook_event_name`、`model`、`permission_mode`；轮次内的 hook 另有 `turn_id`。`UserPromptSubmit` 另有 `prompt`。
- `PreToolUse` 可以匹配本地函数工具，`spawn_agent` 的匹配别名为 `Agent`，`updatedInput` 对本地函数工具有效。
- 子代理：`spawn_agent` 的工具说明只列出 `message`、`items`、`fork_context`，但运行时接受并执行 `model` 和 `reasoning_effort`（openai/codex#26948，Codex 0.137.0，issue 仍开放）。`fork_context: true` 时把父线程的完整历史复制给子代理（openai/codex#14981），此时传入的 `model` 同样会生效，只是子会话里显示的仍是父代理的模型（openai/codex#16371）。内置子代理角色为 `default`、`worker`、`explorer`；自定义子代理放在 `~/.codex/agents/*.toml`，可以写 `model` 与 `model_reasoning_effort`。
- 插件：根目录放可移植的 `plugin.json`，`skills/`、`mcp.json`、`hooks/hooks.json` 放在插件根目录下；`.codex-plugin/plugin.json` 只是兼容旧版。hook 命令里可用 `PLUGIN_ROOT`、`PLUGIN_DATA`（另有 `CLAUDE_PLUGIN_ROOT`、`CLAUDE_PLUGIN_DATA` 兼容别名）。仓库级插件市场文件为 `.agents/plugins/marketplace.json`，旧版也读 `.claude-plugin/marketplace.json`；安装命令为 `codex plugin marketplace add <owner>/<repo>`。
- 插件自带的 hook 和其他非托管 hook 一样，首次运行前要用户审核并信任（`/hooks`），信任按 hook 内容的哈希记录。hook 功能默认开启，`[features] hooks = false` 可关闭。
- 历史风险：Codex 0.118.0 时有报告称插件自带的 hook 不会执行（openai/codex#16430）。当前官方文档已写明插件可以自带 hook，实际行为在 S7 中验证。
- Codex 把每轮的元数据放在 Responses 请求体的 `client_metadata["x-codex-turn-metadata"]` 中，同时投影到同名请求头，字段包括 `session_id`、`thread_id`、`turn_id`（openai/codex#27122）。magpie 已经会读这份元数据里的线程来源与父线程字段（`session_parent.go`），会话 ID 从 `session_id` 请求头读取。
- Codex 只在启动时读模型目录，改了 `model_catalog_json` 要重启 Codex。
- 线程分叉生成新线程，每轮元数据里带 `forked_from_thread_id`；子代理线程的元数据里带 `parent_thread_id`。magpie 的 `session_parent.go` 已经能解析这两个字段。

**magpie（上游）**
- 路由组 `group/<id>`：成员可嵌套其他组（最多 8 层），成员可固定推理强度（`provider/model:<effort>`）；有 `routing`、`stays`、`effort=auto` 和分类器设置。
- 规则引擎在每个用户轮次开始时决定一次，同一轮内的工具往返都留在原模型上，子代理的请求单独算轮次。上下文涨到窗口的 95% 时会换到窗口更大的成员。
- 网关会话 ID 依次取自 `X-Magpie-Session`、`x-claude-code-session-id`、`x-session-id` 等请求头（`gateway.go` 中的 `sessionOf()`）。
- 已经实现了 TypeSafe System One 的调用和结果解析（`decide.go` 的 `jevBody()`、`readJev()`），intent 的置信度门槛为 0.4。
- 中间表示 `Part` 带有 `IsError` 字段，网关能从下一次请求里看出上一轮哪些工具调用失败了。

**Jev（TypeSafe System One）**
- 接口为 `POST https://api.typesafe.ai/v1/systemone`，题型有 `choice`（最多 255 个选项）、`score`（2–10 级）、`noul`（0–1），返回概率和置信度。
- 价格：输入每百万 token 0.042 美元，输出不收费。单次请求上限 64k token，其中 state 加最长的一道题不超过 32k。限流为每秒 40 次请求、10 万 token。
- 官方测试中 13 个问题合在一次调用里用时 0.27 秒（jev-1.12）。
- 弱点：数数和数值比较不可靠，无关上下文会干扰判断，不防提示注入。
- 零数据保留只对企业客户提供。

---

## 3. 总体架构

```mermaid
flowchart LR
  subgraph H["Harness 侧（决策）"]
    CC["Claude Code 插件<br/>queqiao-router"]
    CX["Codex 插件<br/>queqiao-router-codex"]
    PI["Pi 包<br/>pi-queqiao"]
  end
  subgraph Q["queqiao 网关（执行，127.0.0.1:3425）"]
    TURN["POST /v1/queqiao/turn<br/>选档 + 写提示"]
    FB["POST /v1/queqiao/feedback"]
    POL["router.Policy<br/>纯函数"]
    HS["router.HintStore"]
    RG["路由组 group/queqiao<br/>成员 = 三个档位组"]
    TG["档位组<br/>qq-fast / qq-balanced / qq-perf"]
  end
  JEV["Jev<br/>TypeSafe System One"]
  UP["模型厂商与订阅"]
  CC -- "① queqiao hook user-prompt" --> TURN
  CX -- "① queqiao hook user-prompt --harness codex" --> TURN
  PI -- "① HTTP" --> TURN
  TURN --> POL
  TURN -- "分类" --> JEV
  TURN --> HS
  CC -- "② 模型请求<br/>model=group/queqiao" --> RG
  CX -- "② 模型请求<br/>model=group/queqiao" --> RG
  HS -. "按会话与轮次 ID 或提示哈希取提示" .-> RG
  PI -- "② setModel 后的请求<br/>model=group/qq-*" --> TG
  RG --> TG --> UP
  CC -- "③ 结果信号" --> FB
  CX -- "③ 结果信号" --> FB
  PI -- "③ 结果信号" --> FB
```

**分工**

- **harness 侧**提供网关拿不到的东西：用户原话（不含系统附加内容）、计划模式、Agent 和子代理类型、项目级判定标准，以及手动换模型、PR 结果这类结果信号。
- **网关侧**提供 harness 拿不到的东西：同一会话里所有请求的历史（上一轮档位、工具失败次数、距上次请求的时间）、厂商与账号的失败转移，以及用量和成本记账。

### 3.1 一轮对话的数据流（Claude Code）

```mermaid
sequenceDiagram
  participant U as 用户
  participant CC as Claude Code
  participant HK as queqiao hook（插件）
  participant GW as queqiao 网关
  participant J as Jev
  participant M as 档位模型
  U->>CC: 输入提示并回车
  CC->>HK: UserPromptSubmit（stdin JSON）
  HK->>GW: POST /v1/queqiao/turn {session, prompt, plan_mode, cwd, agent:"main", store_hint:true}
  GW->>GW: 读会话状态（上一轮档位、工具失败数、冷热）
  GW->>J: tier（choice）+ dissatisfied（noul）
  J-->>GW: choice + 置信度
  GW->>GW: Policy.Choose → tier；写 HintStore；记 router.jsonl
  GW-->>HK: {tier, reason}
  HK-->>CC: 退出码 0，无输出
  CC->>GW: POST /v1/messages model=group/queqiao（带 x-claude-code-session-id）
  GW->>GW: ruleFor 命中提示 → 档位组成员排第一
  GW->>M: 请求（按需翻译协议）
  M-->>CC: 流式回复；同一轮的工具往返留在该档
```

Codex 的流程与 Claude Code 相同，区别有两处：hook 把 `turn_id` 一起发给 `/turn`；网关从请求的 `x-codex-turn-metadata` 里读出同一个 `turn_id`，按 `(会话, turn_id)` 精确取提示，不需要靠提示文本的哈希去对。Codex 走 OpenAI Responses 协议。

Pi 的流程相同，只是第 ① 步由 `before_agent_start` 发起，`store_hint:false`。扩展拿到档位后直接调用 `pi.setModel()`，所以 Pi 的请求不经过 `group/queqiao`。

---

## 4. 模型配置策略

### 4.1 档位定义

档位是路由器唯一的决策输出。每一档等于“一个模型加一个固定的推理强度”，外加同档的失败转移成员。

| 档位 | 接什么活 | Jev 判定标准（默认值，写入 `router.json`） |
| --- | --- | --- |
| `fast` | 提问、解释、查日志、跑命令、一行或机械式改动、只读的代码搜索 | `Little work: a question, an explanation, reading logs, running a command, a one-line or mechanical change, a read-only code search` |
| `balanced` | 已知代码里的普通 bug 修复、小功能、补测试、单文件重构 | `Some work: an ordinary bug fix or a small feature in code already understood, adding tests, a single-file refactor` |
| `performance` | 跨多文件改动、原因未知的 bug、并发 / 性能 / 安全问题、架构与方案设计、长计划 | `Much work: a change across several files, a bug whose cause is unknown, concurrency, performance or security issues, an architecture or design decision, a long multi-step plan` |

默认判定标准改编自 magpie `decide.go` 中的 `levelWork`。项目可以在仓库里放 `.queqiao/router.json` 覆盖这三条标准，写进本项目的领域知识，例如“改动 `migrations/` 下的文件一律算 performance”。这对应 Open SWE 用自家任务分布写出的分档标准。

### 4.2 选型原则

1. **沿成本—能力的帕累托前沿取三个点**（参照 Artificial Analysis Intelligence Index），相邻两档的单任务成本最好拉开 3 倍以上，这样路由才有省钱空间。附件的三档之间差了约 30 倍。
2. **推理强度属于档位的一部分**，用成员后缀固定，例如 `glm/glm-5.3-flash:xhigh`。便宜模型配高推理强度、贵模型配低推理强度，常常比反过来更划算，附件里的快速档就是这样配的。
3. **跨厂商**：三档尽量来自不同厂商，档内的失败转移成员也要来自另一家厂商，避免单家故障或限流时整档不可用。
4. **硬性门槛**（入选前用第 4.6 节的冒烟测试验证）：
   - 工具调用稳定：连续 20 次带工具的请求中，参数解析失败 ≤ 1 次。
   - 上下文窗口：`fast` ≥ 128k，`balanced` ≥ 200k，`performance` ≥ 另外两档的最大值，保证 95% 换窗口的规则只会往上换。
   - 能通过 queqiao 的协议翻译，在 Anthropic Messages 和 OpenAI Responses 两种协议下都正常工作（Claude Code 走前者，Codex 走后者，Pi 视 Provider 配置而定）。
5. **失败转移不降级**：档内成员的能力必须与本档相当或更高。整档失败时先升一档；`performance` 档整档失败才落到 `balanced`。宁可多花钱，也不悄悄把质量降下去。
6. **价格必须真实**：中转站要用 `queqiao model price` 填实际单价，否则成本统计会按官方定价算，A/B 的结论会失真。
7. **订阅只作补充**：Claude、ChatGPT 等订阅共享可能违反厂商条款。实验期间三档的主成员只用 API Key 类 Provider，订阅只能当失败转移成员。

### 4.3 预设方案

`queqiao router init --preset <名字>` 生成第 4.4 节的组配置。下表的模型 ID 以 `queqiao models` 的实际输出为准，`<p>` 表示提供该模型的 Provider ID。这些预设只是起点，第 9 节的实验数据才是调档的依据。

**预设 `frontier`（与附件相同，跨厂商）**

| 档位 | 主成员 | 同档失败转移 |
| --- | --- | --- |
| fast | `<p>/glm-5.3-flash:xhigh` | `deepseek/deepseek-v4-flash` |
| balanced | `<p>/gpt-5.6-sol:medium` | `<p>/claude-sonnet-5:medium` |
| performance | `<p>/gpt-6-astra:low` | `<p>/claude-opus-5-5:high` |

**预设 `anthropic`（只用 Claude 系，适合只有 Anthropic Key 的用户）**

| 档位 | 主成员 | 同档失败转移 |
| --- | --- | --- |
| fast | `anthropic/claude-haiku-4-5` | `openrouter/anthropic/claude-haiku-4-5` |
| balanced | `anthropic/claude-sonnet-5:medium` | `openrouter/anthropic/claude-sonnet-5:medium` |
| performance | `anthropic/claude-opus-5-5:high` | `openrouter/anthropic/claude-opus-5.5:high` |

**预设 `cn`（国内厂商优先）**

| 档位 | 主成员 | 同档失败转移 |
| --- | --- | --- |
| fast | `deepseek/deepseek-v4-flash` | `glm/glm-5.3-flash:high` |
| balanced | `moonshot/kimi-k2.5` | `glm/glm-5.3:high` |
| performance | 沿用 `frontier` 的 performance 主成员 | 沿用 `frontier` 的 performance 失败转移 |

`cn` 预设的 performance 档暂不指定国内模型。初始化时 CLI 会提示用户从 `queqiao models` 里挑一个当前公认最强的国内模型替换，或者保持沿用 `frontier`。

### 4.4 落到网关配置

预设展开成 4 个路由组，都写入 `~/.config/queqiao/providers.json`。`init` 实际执行的等价命令如下。

```sh
queqiao group add qq-fast        models=<fast 主>,<fast 备>               routing=order stays=auto
queqiao group add qq-balanced    models=<balanced 主>,<balanced 备>       routing=order stays=auto
queqiao group add qq-perf        models=<perf 主>,<perf 备>               routing=order stays=auto
queqiao group add queqiao        models=group/qq-balanced,group/qq-perf,group/qq-fast routing=order stays=turn
```

- `queqiao` 是路由组。成员顺序决定没有提示、也没有分类结果时的默认档（`balanced`），以及整档失败后的转移方向：选中档排第一，其余按 balanced、perf、fast 的顺序，`fast` 永远排在最后。
- 路由组的 `stays=turn`：每轮重新决定档位，同一轮之内保持不变。
- 档位组的 `stays=auto`：沿用 magpie 的默认行为，缓存还热就留在同一账号上。

### 4.5 Agent 侧的模型映射

| Agent | 设置 | 值 |
| --- | --- | --- |
| Claude Code | 主模型 `model` | `group/queqiao` |
| | `ANTHROPIC_DEFAULT_HAIKU_MODEL`、`ANTHROPIC_SMALL_FAST_MODEL` | `group/qq-fast` |
| | `ANTHROPIC_DEFAULT_SONNET_MODEL` | `group/qq-balanced` |
| | `ANTHROPIC_DEFAULT_OPUS_MODEL`、`ANTHROPIC_DEFAULT_FABLE_MODEL` | `group/qq-perf` |
| | `CLAUDE_CODE_SUBAGENT_MODEL` | 不设置，由插件按调用选档 |
| Codex | `model_provider` | `queqiao`（magpie 原来写的 `[model_providers.magpie]` 改名，`wire_api = "responses"`） |
| | `model` | `group/queqiao` |
| | `model_catalog_json` | 由 `router init` 重写，包含 `group/queqiao` 与三个档位组；写完提示用户重启 Codex |
| | `model_reasoning_effort` | 不改。档位成员用 `:<effort>` 后缀固定推理强度，覆盖 Codex 自己请求的强度 |
| | 子代理 | 不写 `[agents]` 默认模型，由插件在 `spawn_agent` 时选档 |
| Pi | Provider | `queqiao`（magpie 原来写的 `magpie` Provider 改名） |
| | 默认模型 | `queqiao/group/qq-balanced`，扩展每轮调用 `setModel` 切换 |

这样映射后，用户在 Claude Code 里用 `/model opus` 手动换模型，请求的模型会变成 `group/qq-perf`，不再是 `group/queqiao`，路由器自然不再介入，相当于用户手动把档位钉住了。`/model` 选回默认模型，路由就恢复。Codex 同理：`/model` 选了 `group/qq-perf`，请求就不再经过路由组；Codex 的 hook 输入自带当前的 `model`，插件据此识别用户已手动钉住档位（第 6.9 节）。

### 4.6 选型的冒烟测试与例行复审

- `queqiao router check`：对三档主成员和失败转移成员逐个检查第 4.2 节第 4 条的门槛。分别用 Anthropic Messages 和 OpenAI Responses 协议发 20 次带工具的请求，并检查窗口大小是否满足要求。任何一项不过，返回非零退出码。
- 每季度或某一档出现新的帕累托点时复审：新模型先作为同档失败转移成员加入，跑一轮 A/B（第 9 节），数据不劣于现任主成员再提升为主成员。

---

## 5. 路由策略（`router.Policy`）

策略是一个**纯函数**，不做 I/O，所有输入由调用方准备好。这是整个系统里测试最密集的一个单元。

### 5.1 输入与输出

```go
type Tier string // "fast" | "balanced" | "performance"

type PolicyInput struct {
    Agent          string    // "main"、"gateway" 或子代理类型，如 "Explore"、"explorer"
    PlanMode       bool      // harness 报告处于计划模式（Claude Code 为 permission_mode == "plan"；Codex 见 S8）
    Classified     *Verdict  // Jev 结果；nil 表示分类失败或超时
    Prev           *TurnState // 本会话上一轮；nil 表示第一轮
    ToolFailures   int       // 上一轮工具结果中 IsError 的个数（网关统计）
    ToolCalls      int       // 上一轮工具调用总数（网关统计）
    SinceLast      time.Duration // 距本会话上一次模型请求的时间
    Now            time.Time
}

type Verdict struct {
    Tier            Tier
    TierConfidence  float64 // 0–1
    Dissatisfied    float64 // noul 0–1：用户在说上一轮的结果不对
}

type TurnState struct {
    Tier          Tier
    EscalatedLeft int  // 升档剩余的轮数
    LowerStreak   int  // 连续几轮分类结果低于当前档位
}

type Decision struct {
    Tier   Tier
    Reason string     // 见 5.2 的规则编号，如 "R3-escalate"
    Next   TurnState  // 写回会话状态
}

func Choose(in PolicyInput, cfg PolicyConfig) Decision
```

### 5.2 决策规则（按顺序，命中即止）

| 规则 | 条件 | 结果 |
| --- | --- | --- |
| R1 固定子代理 | `Agent` 在 `cfg.FixedAgents` 里（默认：Claude Code 的 `Explore`、`statusline-setup`、`claude-code-guide` 与 Codex 的 `explorer` → `fast`；Claude Code 的 `Plan` → `performance`） | 对应档位 |
| R2 计划模式 | `PlanMode` | `performance` |
| R3 升档 | `Prev != nil`，并且 `Classified.Dissatisfied ≥ cfg.DissatisfiedMin`（默认 0.7）或上一轮 `ToolCalls ≥ 3` 且 `ToolFailures*2 ≥ ToolCalls` | `max(分类档, Prev.Tier + 1)`；分类不可用或不可信时为 `Prev.Tier + 1`。`EscalatedLeft = cfg.EscalateTurns`（默认 2） |
| R4 升档保持 | `Prev.EscalatedLeft > 0` | `max(分类档, Prev.Tier)`，`EscalatedLeft - 1` |
| R5 分类可信 | `Classified != nil` 且 `TierConfidence ≥ cfg.TierMin`（默认 0.4） | 进入 R6 的迟滞判断 |
| R6 迟滞 | 没有上一轮，或 R5 的档位高于或等于上一轮：直接采用，`LowerStreak = 0`。低于上一轮：`SinceLast ≥ cfg.CacheTTL`（默认 300 秒，此时缓存已凉）或 `Prev.LowerStreak + 1 ≥ 2` 时降档并令 `LowerStreak = 0`；否则保持 `Prev.Tier`，`LowerStreak = Prev.LowerStreak + 1` | 见左 |
| R7 沿用 | 分类失败或不可信，且有上一轮 | `Prev.Tier` |
| R8 默认 | 以上都不满足 | `cfg.DefaultTier`（默认 `balanced`） |

说明：
- “档位 + 1”在 `performance` 封顶。
- 只有 R6 允许降档。升档总是立即生效，降档要么等缓存凉了，要么连续两轮都判低。附件提到中途换模型会扔掉 prompt cache，迟滞规则就是为这一点设计的。
- 数值判断（工具失败比例、时间间隔）全部在代码里做，不交给 Jev。Jev 数数和比较数值不可靠。
- 除 R1 外，`Decision.Next` 一律写回会话状态，`EscalatedLeft` 与 `LowerStreak` 在未提到的规则里保持 0。
- **子代理无状态**：`Agent` 既不是 `main` 也不是 `gateway` 时（即子代理），`Prev` 恒为 nil，`Decide` 不读写会话状态。子代理的选档不会影响主会话下一轮的判断。

### 5.3 Jev 的问法

`/v1/queqiao/turn` 发给 Jev 的请求一次带两道题。官方文档说明，多加题目对响应时间影响很小。

```json
{
  "model": "jev-latest",
  "state": {
    "message": "<用户原话，去掉 <system-reminder> 等附加内容，超过 4000 字时保留前 3000 字和后 1000 字>",
    "previous_tier": "balanced",
    "agent": "main"
  },
  "questions": {
    "tier": {
      "type": "choice",
      "instructions": "The `message` is what a user asked a coding assistant. Which tier can most cheaply handle it well? A message that only carries on from the turn before (go on, yes, do it) is of `previous_tier`.",
      "criteria": { "fast": "<4.1 的标准>", "balanced": "<4.1 的标准>", "performance": "<4.1 的标准>" }
    },
    "dissatisfied": {
      "type": "noul",
      "instructions": "The `message` says the assistant's previous result was wrong, broken, incomplete, or not what the user asked for."
    }
  }
}
```

`state` 里刻意不放对话原文、文件列表和数字特征，因为无关内容会干扰 Jev。第一轮没有上一轮档位，`previous_tier` 省略，`tier` 的说明里也去掉“carries on”那一句。

### 5.4 分类器备选

`router.json` 的 `classifier` 字段可以写 `typesafe/jev-latest`（默认），也可以写任意 `provider/model`。用普通模型时，复用 magpie `classify.go` 的“只回答一个编号”的提示词：`tier` 题照常回答，`dissatisfied` 题单独再问一次“yes/no”。普通模型拿不到置信度，此时 `TierConfidence` 按回答格式是否合法记为 1 或 0。

### 5.5 子代理（Claude Code）

插件的 `PreToolUse` hook 匹配子代理工具：

1. `tool_input.subagent_type` 为 `fork` 的，不改。fork 子代理的模型由 Claude Code 固定为主会话的模型，它发出的请求由网关按第 5.8 节跟随主会话的档位。
2. `tool_input` 里已经有 `model` 的，不改，尊重 Claude 自己的选择。
3. 子代理类型命中 R1 的，直接映射成别名（`fast` → `haiku`，`balanced` → `sonnet`，`performance` → `opus`）。
4. 其余的，以子代理任务描述（`tool_input.prompt`）为 `message`、子代理类型为 `agent` 调用 `/v1/queqiao/turn`（`store_hint:false`），把得到的档位映射成别名，写进 `updatedInput.model`。

子代理请求带的模型是 `group/qq-*`，直接进入对应档位组，不经过路由组。

### 5.6 子代理（Codex）

插件的 `PreToolUse` hook 匹配 `spawn_agent`（matcher 写 `^(spawn_agent|Agent)$`）：

1. `tool_input` 里已经有 `model` 的，不改。
2. `tool_input.fork_context` 为 true 的（fork 子代理），调用 `GET /v1/queqiao/session?id=<父会话>` 取父会话当前的档位，把 `model` 写成 `group/qq-<该档位>`，不分类。查不到父会话的档位时不改。这样子代理和父会话用同一个档位组，缓存能接上，网关也不会对它重新分类。
3. 子代理类型（`tool_input.agent_type`，缺省为 `default`）命中 R1 的，直接取对应档位。
4. 其余的，以 `tool_input.message` 为 `message`、子代理类型为 `agent` 调用 `/v1/queqiao/turn`（`store_hint:false`）。
5. 输出 `updatedInput`：原参数加上 `"model": "group/qq-<档位>"`。不写 `reasoning_effort`，推理强度由档位成员的后缀决定。

`model` 是 `spawn_agent` 未写进工具说明的参数（第 2 节），能否生效在 S9 中验证。不生效时删去这个 hook，Codex 子代理的请求按网关模式路由。

### 5.7 网关模式（没有插件的 Agent）

请求的模型是 `group/queqiao`、但没有匹配到提示时（例如 OpenCode 直接用这个组，或者 hook 超时、Codex 的 hook 还没被信任），网关用自己能拿到的输入走同一个 `Choose`：`message` 取 magpie `userText()` 的结果，`Agent` 记为 `gateway`，`PlanMode` 为 false。这样插件只是给路由加上下文，没有插件时路由照样工作。

### 5.8 fork 子代理与派生会话的档位继承

**原则**：从零开始的子代理单独选档（第 5.5、5.6 节）；继承父会话上下文的 fork 子代理**跟随父会话当前的档位**，不分类、不写会话状态；派生会话是一个新会话，但**第一轮继承父会话的档位状态**，之后正常路由。依据是继承上下文的请求换了模型就复用不了父会话的 prompt cache，而 fork 和派生会话的省钱之处正在于此。

| 情形 | 识别方式 | 处理 |
| --- | --- | --- |
| Claude Code fork 子代理（`/subtask`、`subagent_type: "fork"`） | 网关看到一个新轮次请求：模型为 `group/queqiao`，会话 ID 与 harness 路径下的主会话相同，对话开头（`firstWords`）与主会话相同，且没有可取的提示 | 用主会话当前的档位。这一轮不分类，也不写主会话的 `TurnState` |
| Codex fork 子代理（`spawn_agent` 带 `fork_context: true`） | 插件的 `pre-agent` 看到 `fork_context: true` | 第 5.6 节第 2 步：把 `model` 钉在父会话当前的档位组 |
| Pi 上 pi-subagents 的 `inherit_context` | 扩展的 `tool_call` 看到 `Agent` 工具带 `inherit_context: true` | 默认与 fork 子代理相同，把 `model` 钉在父会话当前的档位组。若 S12 证实父对话是以文本重新放入、本来就复用不了缓存，改为像普通子代理一样单独选档 |
| Codex 线程分叉 | 新线程第一轮的元数据带 `forked_from_thread_id` | 父会话 = 该线程；子会话的 `Prev` 取父会话 `TurnState` 的副本 |
| Claude Code `/fork`、`/branch` | 插件的 `SessionStart` hook 看到 `source` 为 `fork`（`/branch` 的取值在 S11 中确认），调用 `POST /v1/queqiao/lineage` 把这个会话标记为“派生会话、父会话未知”；网关在它的第一轮请求里，在 24 小时内的其他会话中找 `firstWords` 相同、最近活跃的一个作为父会话 | 同上 |
| Pi `/fork`、`/tree` 分支 | 扩展的 `session_start` 看到 `reason` 为 `fork`，若能读到父会话 ID（S12）则随 `/turn` 的 `parent_session` 一起发送；读不到则与 Claude Code 一样调用 `/lineage` 标记，由网关按 `firstWords` 找父会话 | 同上 |

补充规则：
- **继承什么**：`Prev` 取父会话 `TurnState` 的副本（`Tier`、`EscalatedLeft` 原样保留，`LowerStreak` 置 0）；`SinceLast` 按父会话最后一次请求的时间计算。这样 R6 的迟滞规则会自然地在缓存还热时保持父会话的档位。
- **父会话的确定顺序**：`/turn` 或 `/lineage` 显式给出的 `parent_session` > Codex 的 `forked_from_thread_id` > 标记为派生会话后按 `firstWords` 找到的会话。找不到父会话时按新会话处理（`Prev = nil`）。
- **只对标记过的会话做跨会话匹配**：两个毫不相关的会话可能以同一句话开头（例如都以“hi”开头），所以 `firstWords` 的跨会话匹配只用于已经被 `/lineage` 标记为派生的会话，或带有 `forked_from_thread_id` 的请求。
- **与 hook 失败的区分**：Claude Code 主会话某一轮的 hook 超时或失败时，这一轮也满足上表第一行的条件，同样会沿用主会话当前的档位。这相当于 R7，不会出错，代价是这一轮少了升档判断。`router status` 中把“fork 轮次”和“主会话缺提示”合并计数，并在 S10 中用 `SubagentStart` 的 `agent_type` 核对二者的比例。

---

## 6. 组件设计

### 6.1 仓库 fork 的基础改动（`SP1`）

| 文件 | 改动 |
| --- | --- |
| `go.mod` 及全部 import | **不改**，保留 `github.com/yetone/magpie`。仓库里有 770 个 Go 文件 import 这个路径，改名会让每次从 `main` 合并上游都在这些文件上冲突。代价是不支持 `go install github.com/weiping/queqiao@...`，只通过 Release 二进制和 `make` 构建分发 |
| `main.go`、`Makefile` | 二进制名改为 `queqiao` |
| `internal/appdir/appdir.go` | 配置目录改为 `~/.config/queqiao`，缓存目录改为 `~/.cache/queqiao` |
| `internal/update/update.go` | 关闭自动更新：`queqiao update` 只打印“请从 `weiping/queqiao` Releases 下载”，后台检查不再运行 |
| `internal/stats` | 关闭上游的用户计数上报（fork 不能向上游的 PostHog 发数据） |
| `internal/agent` 中写 Pi、OpenCode、Codex 配置的代码 | Provider 名由 `magpie` 改为 `queqiao`（Codex 为 `[model_providers.queqiao]`，模型目录文件改为 `~/.codex/queqiao-models.json`） |
| 其余 | `MAGPIE_*` 环境变量、`X-Magpie-*` 请求头、`/v1/magpie/*` 端点保持原名，尽量减小与上游的差异 |

**分支与同步上游的策略**

| 分支 | 用途 | 规则 |
| --- | --- | --- |
| `main` | 上游 `yetone/magpie` 的镜像 | 只做快进同步（GitHub 的 Sync fork，或 `git fetch upstream && git push origin upstream/main:main`），从不在上面直接提交 |
| `queqiao` | queqiao 的开发主干，从 `main` 切出 | 每周一次把 `main` 合并进来，上游有需要的修复时随时合并。用 merge，不用 rebase：`queqiao` 是已推送的共享分支，rebase 会改写历史，打乱进行中的 worktree 和 PR。合并提交的说明写明上游的 commit 范围 |
| `qq/sp<N>-<名字>` | superpowers 子项目的功能分支，如 `qq/sp2-router-core` | 从 `queqiao` 切出，在独立的 git worktree 里实施，完成后以 PR 合回 `queqiao` |

- 建议把 GitHub 仓库的**默认分支设为 `queqiao`**：Claude Code 与 Codex 的插件市场文件都在 `queqiao` 分支上，默认分支是 `queqiao` 时，`claude plugin marketplace add weiping/queqiao` 和 `codex plugin marketplace add weiping/queqiao` 不用另外指定分支。默认分支保持 `main` 时，Codex 用 `--ref queqiao`，Claude Code 的指定方式在 SP3 中按当时的文档确认。
- 版本标签用 `queqiao-v<主>.<次>.<修订>`，从 `queqiao` 分支打，避免与上游的标签混淆。
- 新代码全部放在 `internal/router/`、`internal/harness/`、`clients/` 三个新目录里；对上游文件的改动只限于本节表格、第 6.3 节的两处挂钩和 `usage.Record` 新增的字段。合并 `main` 时出现冲突，只可能出在这几处。

### 6.2 `internal/router`（`SP2`）

每个文件只负责一件事，对外暴露的接口如下。

| 文件 | 职责 | 对外接口 |
| --- | --- | --- |
| `config.go` | 加载并校验 `~/.config/queqiao/router.json`，合并项目级 `<cwd>/.queqiao/router.json`（只允许覆盖 `criteria`） | `func Load(globalPath, cwd string) (Config, error)` |
| `policy.go` | 第 5.2 节的纯函数 | `func Choose(in PolicyInput, cfg PolicyConfig) Decision` |
| `classify.go` | 组装第 5.3 节的请求；调用 magpie 已有的 System One 客户端，或第 5.4 节的备选分类器；1000 ms 超时 | `type Classifier interface { Classify(ctx context.Context, q Question) (*Verdict, error) }` |
| `turnmeta.go` | 从 Codex 请求中读出 `turn_id`：先读 `x-codex-turn-metadata` 请求头，再读 Responses 请求体的 `client_metadata["x-codex-turn-metadata"]`；都没有时返回空串。只读这一个字段 | `func CodexTurnID(h http.Header, body []byte) string` |
| `session.go` | 保存 `TurnState`，以及网关统计到的 `ToolCalls`、`ToolFailures`、上一次请求时间；只在内存里，空闲 24 小时淘汰。**两种键**：`TurnState` 的键在 harness 路径（`/turn`，`agent=main`）为会话 ID，在网关模式下为 magpie 的会话加 `firstWords`（与 `ruleKey` 同义，用来区分子代理）；工具统计和请求时间只按会话 ID 汇总，子代理的工具调用也计入 | `type Sessions struct`；`Get(key)`、`Observe(session string, req *gateway.Request)`、`Commit(key, TurnState)`；第 5.8 节用到的 `MainFirstWords(session) string`、`MarkDerived(session string, parent string)`、`ParentOf(session string, firstWords string) (parent string, ok bool)`、`InheritFrom(child, parent string)` |
| `hint.go` | 提示存储：每条提示带 `Session`、`TurnID`（只有 Codex 有）和 `PromptHash`，TTL 120 秒，取出即删除。匹配顺序见第 6.5 节 | `Put(Hint)`、`Take(k HintKey, now time.Time) (*Hint, bool)`，`type HintKey struct { Session, TurnID, PromptHash string }` |
| `experiment.go` | 按会话分配实验组 | `func Arm(session string, e ExperimentConfig) string // "router" | "control"` |
| `events.go` | 把决策、提示消费、反馈追加写入 `~/.config/queqiao/router.jsonl` | `func Append(ev Event) error` |
| `api.go` | 第 6.4 节的 HTTP 处理函数 | `func Register(mux *http.ServeMux, deps Deps)` |

`router.json` 的完整结构：

```json
{
  "version": 1,
  "router_group": "queqiao",
  "tiers": {
    "fast":        { "group": "qq-fast",     "claude_alias": "haiku",  "criteria": "<4.1>" },
    "balanced":    { "group": "qq-balanced", "claude_alias": "sonnet", "criteria": "<4.1>" },
    "performance": { "group": "qq-perf",     "claude_alias": "opus",   "criteria": "<4.1>" }
  },
  "default_tier": "balanced",
  "classifier": "typesafe/jev-latest",
  "classify_timeout_ms": 1000,
  "thresholds": { "tier_min": 0.4, "dissatisfied_min": 0.7 },
  "escalate_turns": 2,
  "cache_ttl_seconds": 300,
  "fixed_agents": { "Explore": "fast", "statusline-setup": "fast", "claude-code-guide": "fast", "Plan": "performance", "explorer": "fast" },
  "experiment": { "enabled": false, "router_percent": 50, "control_tier": "performance", "salt": "<init 时随机生成>" }
}
```

`"<4.1>"` 表示 `router init` 写入 §4.1 表中对应档位的英文默认标准。校验规则：三个档位必须齐全；`group` 必须是已存在的路由组；阈值在 0 到 1 之间；`router_percent` 在 0 到 100 之间。校验失败时网关照常启动，但路由组退化为 magpie 原有的行为，`queqiao router status` 会报出错误原因。

### 6.3 对上游代码的两处挂钩

1. **`internal/gateway/rules.go` 的 `ruleFor()`**：在函数开头、`g.Ruled()` 判断**之前**插入一个可选的回调 `routerHook`（包级变量，默认为 nil，`SP2` 在启动时注入）。路由组本身不配规则，挂在 `g.Ruled()` 之后会被提前返回。回调的参数包含请求头、IR 请求，以及 Responses 协议下的原始请求体（读 Codex 的 `turn_id` 要用）；如果 `ruleFor` 的调用处拿不到原始请求体，就只传请求头，`turnmeta.go` 退回只读请求头。回调只在请求的组是 `router_group`、而且这是一个新轮次的开始时调用。回调返回要排在第一的成员（`group/qq-*`），由挂钩代码包装成一个 `RuleHit`，写入 `ruleFor` 现有的按轮记录表，这样同一轮里的后续请求沿用已有的“轮内保持”逻辑，不再调用回调。SP2 的计划必须先完整阅读 `ruleFor()` 及其轮次记录表，确认“新轮次”的判定方式和记录表的写入入口，再写这个挂钩；§8 集成测试的 (b) 用来证明轮内保持确实生效。
2. **`internal/gateway/gateway.go` 的 mux 注册**：加一行 `router.Register(mux, deps)`。

另外：`RuleHit` 新增 `Router *RouterHit` 字段（包含 tier、reason、source 和是否命中提示），会出现在 `GET /v1/magpie/route` 的结果里；`usage.Record` 新增 `router_tier`、`router_arm` 两个字段，写入 `usage.jsonl` 和 OTLP 属性。

### 6.4 HTTP 接口

所有接口只接受本机请求。开启局域网共享时，必须带有效的网关密钥（沿用 magpie 的鉴权）。

**`POST /v1/queqiao/turn`**

```json
// 请求
{
  "session": "string，必填",
  "prompt": "string，必填，用户原话",
  "harness": "claude-code | codex | pi | gateway",
  "turn_id": "string，可选，Codex 的 turn_id",
  "agent": "main | <子代理类型> | gateway",
  "plan_mode": false,
  "cwd": "string，可选，用于加载项目级 criteria",
  "parent_session": "string，可选，派生会话的父会话 ID（第 5.8 节）",
  "store_hint": true
}
// 响应 200
{
  "tier": "balanced",
  "group": "group/qq-balanced",
  "claude_alias": "sonnet",
  "reason": "R5-classified",
  "source": "jev | llm | default",
  "confidence": 0.83,
  "arm": "router | control",
  "latency_ms": 212
}
```

- 网关先计算 `prompt_sha256`（规范化方式：去掉 `<system-reminder>…</system-reminder>`、首尾空白，统一为 LF 换行）。
- `arm` 为 `control` 时，`tier` 返回 `experiment.control_tier`。路由器本来会选的档位作为 `shadow_tier` 写进 `router.jsonl`，不放进响应。
- 只有 `400`（缺少必填字段）一种错误。分类失败不算错误，按策略落到 R7 或 R8，`source` 记为 `default`。

**`POST /v1/queqiao/feedback`**

```json
{ "session": "string", "kind": "pr_created | pr_merged | pr_closed | manual_model_switch | thumbs_up | thumbs_down", "value": "string，可选，如 PR URL", "at": "RFC3339，可选" }
```

返回 `204`，事件写入 `router.jsonl`。

**`GET /v1/queqiao/session?id=<会话 ID>`**：返回该会话当前的档位，`{"tier":"balanced","group":"group/qq-balanced","updated_at":"RFC3339"}`；会话不存在时返回 `404`。供 Codex 与 Pi 的 fork 子代理钉档使用（第 5.8 节）。

**`POST /v1/queqiao/lineage`**：`{"session":"string","parent_session":"string，可选","source":"claude-code-fork | claude-code-branch | pi-fork"}`，把会话标记为派生会话，返回 `204`。只记录关系，不分类、不写 `TurnState`；真正的继承在该会话第一轮的 `Decide` 里完成。

**`GET /v1/queqiao/router`**：返回配置是否有效、档位到组的映射、实验开关、最近 20 条决策。供 `queqiao router status` 使用。

### 6.5 网关对提示的消费

`routerHook` 收到新轮次的请求后，按以下步骤处理：

1. 用 `sessionOf(header)` 取会话 ID，用 `CodexTurnID()` 取轮次 ID（非 Codex 请求为空），用 `userText(req)` 加上第 6.4 节的规范化算出提示哈希。
2. 按以下顺序取提示，命中即止：（a）会话与 `turn_id` 都相同，且两者非空（Codex 的主路径）；（b）会话与提示哈希都相同（Claude Code 的主路径）；（c）只有提示哈希相同（会话 ID 对不上时的兜底）。
3. 取到提示：直接用提示里的档位。实验组和会话状态都已经在 `/turn` 里处理过，这里只记一条 `hint_consumed` 事件。
4. 取不到：先判断是不是 fork 子代理的轮次（第 5.8 节表格第一行），是就直接返回主会话当前的档位，不调用 `Decide`；否则走第 5.7 节的网关模式，同步调用分类器，再执行 `Choose`。
5. 返回 `group/<tier 对应的组>`。

**不变式**：每个会话、每一轮只调用一次 `Choose`，并且只写回一次 `TurnState`，无论这一轮的决定来自提示还是来自网关模式；fork 子代理的轮次不调用 `Choose`，也不写 `TurnState`。派生会话的第一轮在 `Decide` 内先按第 5.8 节确定父会话并继承，再执行 `Choose`。实现上，`/turn` 和网关模式共用 `router.Decide(ctx, input) Decision`，由 `Decide` 统一负责读写会话状态。

### 6.6 `queqiao` 新增的 CLI 子命令（`SP2`、`SP3`、`SP5`、`SP6`）

| 命令 | 子项目 | 作用 |
| --- | --- | --- |
| `queqiao router init --preset frontier\|anthropic\|cn` | SP2 | 生成 `router.json`，建四个路由组，把 Claude Code、Codex 和 Pi 指向第 4.5 节的映射 |
| `queqiao router status` | SP2 | 显示配置是否有效、映射和最近的决策 |
| `queqiao router check` | SP2 | 第 4.6 节的冒烟测试 |
| `queqiao hook user-prompt --harness claude-code\|codex` | SP3、SP6 | `UserPromptSubmit` 的处理程序 |
| `queqiao hook pre-agent --harness claude-code\|codex` | SP3、SP6 | `PreToolUse`（子代理工具）的处理程序 |
| `queqiao hook post-bash --harness claude-code\|codex` | SP3、SP6 | `PostToolUse`（`Bash`）的处理程序，识别 PR 链接 |
| `queqiao hook model-switch` | SP3 | Claude Code `PostModelSwitch` 的处理程序 |
| `queqiao hook session-start --harness claude-code` | SP3 | Claude Code `SessionStart` 的处理程序，`source` 为派生类取值时调用 `/lineage` |
| `queqiao statusline` | SP3 | 供用户配置到 Claude Code 状态栏，显示本会话当前的档位和模型 |
| `queqiao router report --since 14d` | SP5 | 第 9 节的实验报表 |

hook 处理程序的公共部分（读 stdin、调用网关、超时、一律以退出码 0 结束）放在 `internal/harness/`，两种 harness 的输入解析与输出格式分别放在 `internal/harness/claudecode/` 与 `internal/harness/codex/`。`--harness` 决定用哪一种。两个插件因此都不依赖 Node 或 Python，只要求 `queqiao` 在 `PATH` 里。

### 6.7 Claude Code 插件 `queqiao-router`（`SP3`）

仓库内路径为 `clients/claude-code/`，仓库根目录放 `.claude-plugin/marketplace.json`，`source` 指向这个目录，用户执行 `claude plugin marketplace add weiping/queqiao` 后即可安装。Codex 也会把 `.claude-plugin/marketplace.json` 当作旧版市场文件读取，所以仓库根目录同时放 `.agents/plugins/marketplace.json`（只列 Codex 插件，第 6.9 节），让 Codex 优先读它；S7 验证 Codex 不会把 Claude Code 插件装进来。

```
clients/claude-code/
  .claude-plugin/plugin.json
  hooks/hooks.json
  agents/repo-scout.md
  README.md
```

`plugin.json` 的要点：`name` 为 `queqiao-router`（不得以 `claude-` 开头，见插件命名规则）；`userConfig` 有一项 `gateway_url`（默认 `http://127.0.0.1:3425`），hook 进程通过 `CLAUDE_PLUGIN_OPTION_GATEWAY_URL` 读取。

`hooks/hooks.json`：

```json
{
  "hooks": {
    "UserPromptSubmit": [
      { "hooks": [ { "type": "command", "command": "queqiao", "args": ["hook", "user-prompt", "--harness", "claude-code"], "timeout": 2 } ] }
    ],
    "PreToolUse": [
      { "matcher": "Agent|Task", "hooks": [ { "type": "command", "command": "queqiao", "args": ["hook", "pre-agent", "--harness", "claude-code"], "timeout": 2 } ] }
    ],
    "PostToolUse": [
      { "matcher": "Bash", "hooks": [ { "type": "command", "command": "queqiao", "args": ["hook", "post-bash", "--harness", "claude-code"], "timeout": 2 } ] }
    ],
    "PostModelSwitch": [
      { "hooks": [ { "type": "command", "command": "queqiao", "args": ["hook", "model-switch", "--harness", "claude-code"], "timeout": 2 } ] }
    ],
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "queqiao", "args": ["hook", "session-start", "--harness", "claude-code"], "timeout": 2 } ] }
    ]
  }
}
```

各处理程序的行为：

| 处理程序 | 行为 | 输出 |
| --- | --- | --- |
| `user-prompt` | 调用 `/turn`，参数为 `{session_id, prompt, agent: agent_type 为空时记为 "main", plan_mode: permission_mode=="plan", cwd, store_hint:true}`，HTTP 超时 1500 ms | 无论成功失败，一律退出码 0、stdout 为空。不写 `additionalContext`，避免改动提示破坏缓存 |
| `pre-agent` | 按第 5.5 节处理（`fork` 子代理不改） | 需要改模型时输出 `{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{...原参数, "model":"<别名>"}}}`；否则不输出 |
| `post-bash` | 在 `tool_output` 里匹配 `https://github.com/<owner>/<repo>/pull/<n>`，找到就发送 `pr_created` | 不输出 |
| `model-switch` | 发送 `manual_model_switch`，`value` 为 `from_model→to_model` | 不输出 |
| `session-start` | `source` 为 `fork`（以及 S11 确认的 `/branch` 取值）时调用 `/lineage`，`parent_session` 留空；其他取值不做任何事 | 不输出 |

`agents/repo-scout.md`：一个只读的代码搜索子代理，`model: haiku`（映射到 `qq-fast`），`tools: Read, Grep, Glob`。

### 6.8 Pi 包 `pi-queqiao`（`SP4`）

仓库内路径为 `clients/pi/`，发布为 npm 包 `@weiping/pi-queqiao`。

```
clients/pi/
  package.json          // "keywords":["pi-package"], "pi":{"extensions":["./extensions"]}
  extensions/queqiao.ts
  src/client.ts         // /turn、/feedback 的 HTTP 客户端，带超时
  src/session.ts        // 会话 ID 的获取与保存
  test/*.test.ts        // vitest
```

扩展的行为：

| 事件 / 接口 | 行为 |
| --- | --- |
| `session_start` | 确定会话 ID（第 10 节 S4 验证 Pi 提供的会话标识；拿不到时用 `crypto.randomUUID()` 生成，在扩展内存中保留到会话结束）。`reason` 为 `fork` 时记下父会话 ID（S12）；读不到父会话 ID 时调用 `/lineage` 标记 |
| `before_provider_headers` | 给每个请求加 `X-Magpie-Session: <会话 ID>`，让网关能统计本会话的工具失败次数和请求间隔 |
| `before_agent_start` | 调用 `/turn`（`store_hint:false`，`agent:"main"`，`prompt` 取 `event.prompt`），超时 1500 ms；拿到档位后，若档位变了，`pi.setModel(<queqiao/group/qq-*>)`。失败时不改模型 |
| `model_select` | `source` 表示用户手动切换时，发送 `manual_model_switch`，并在本会话剩余时间里不再自动切换（与 Claude Code 中 `/model` 的效果一致） |
| `tool_result` | 匹配 PR 链接，发送 `pr_created` |
| `tool_call` | 只在装了 pi-subagents 时生效（工具名为 `Agent`，参数结构在 S12 中确认）。参数里已有 `model` 的不改；`inherit_context` 为 true 的按第 5.8 节钉在父会话当前的档位组；其余的以任务描述调用 `/turn`（`store_hint:false`），把 `event.input.model` 改为 `queqiao/group/qq-<档位>` |
| `before_agent_start`（补充） | 本会话是派生会话的第一轮时，`/turn` 请求带上 `parent_session` |

运行时依赖只用 Node 内置的 `fetch` 和 `crypto`，`dependencies` 为空。Pi 自带的包放进 `peerDependencies`，版本写 `"*"`。

### 6.9 Codex 插件 `queqiao-router-codex`（`SP6`）

仓库内路径为 `clients/codex/`，在仓库根目录的 `.agents/plugins/marketplace.json` 里登记（`source` 为 `{"source":"local","path":"./clients/codex"}`）。用户执行 `codex plugin marketplace add weiping/queqiao` 安装，首次使用时在 Codex 里用 `/hooks` 审核并信任插件的 hook。

```
clients/codex/
  plugin.json          // name: queqiao-router-codex；extensions.com.openai.interface 写显示名与简介
  hooks/hooks.json
  README.md            // 安装、信任 hook、重启 Codex 三步
```

`hooks/hooks.json`：

```json
{
  "hooks": {
    "UserPromptSubmit": [
      { "hooks": [ { "type": "command", "command": "queqiao hook user-prompt --harness codex", "timeout": 2, "statusMessage": "queqiao: 选档" } ] }
    ],
    "PreToolUse": [
      { "matcher": "^(spawn_agent|Agent)$", "hooks": [ { "type": "command", "command": "queqiao hook pre-agent --harness codex", "timeout": 2 } ] }
    ],
    "PostToolUse": [
      { "matcher": "^Bash$", "hooks": [ { "type": "command", "command": "queqiao hook post-bash --harness codex", "timeout": 2 } ] }
    ]
  }
}
```

Codex 文档没有写明 hook 是否支持 Claude Code 那种 `args` 数组形式，这里用整条命令字符串；`timeout` 的单位在 S7 中确认，默认按秒。

各处理程序在 `--harness codex` 下的行为：

| 处理程序 | 行为 | 输出 |
| --- | --- | --- |
| `user-prompt` | 输入的 `model` 不是 `group/queqiao` 时（用户已用 `/model` 钉住档位或换了模型），发送 `manual_model_switch`（每个会话只发一次），不调用 `/turn`。否则调用 `/turn`，参数为 `{harness:"codex", session: session_id, turn_id, prompt, agent:"main", plan_mode: <按 S8 结论>, cwd, store_hint:true}`，HTTP 超时 1500 ms | 一律退出码 0、stdout 为空，不写 `additionalContext` |
| `pre-agent` | 按第 5.6 节处理（`fork_context: true` 时钉在父会话当前的档位组） | 需要改模型时输出 `{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{...原参数, "model":"group/qq-<档位>"}}}`；否则不输出 |
| `post-bash` | 在整个 stdin JSON 文本里匹配 `https://github.com/<owner>/<repo>/pull/<n>`，找到就发送 `pr_created`。不依赖工具结果的字段名，因为 Codex 与 Claude Code 的字段名不同 | 不输出 |

Codex 没有与 `PostModelSwitch` 对应的事件，所以手动换模型的信号由 `user-prompt` 根据输入里的 `model` 判断。

**R3 在 Codex 下的信号来源**：Codex 的工具结果走 Responses 协议的 `function_call_output`，magpie 的中间表示里不一定标出 `IsError`。S8 一并确认网关能否从 Codex 的请求中统计出工具失败数；统计不出时，Codex 会话的 R3 只由 `dissatisfied` 触发，`router status` 中注明这一点。

---

## 7. 错误处理

| 失败点 | 表现 | 处理 | 对用户的影响 |
| --- | --- | --- | --- |
| 网关没在运行 | hook 连接被拒绝 | hook 立即以退出码 0 结束；Claude Code 请求失败的提示由 Claude Code 自己给出 | 与没装插件时相同 |
| Jev 超时、返回 429 或 529 | `Classify` 返回错误 | `Classified=nil`，按 R7 或 R8 处理；连续失败 3 次后 60 秒内不再请求 Jev（沿用 magpie `classifyRest` 的思路） | 这一轮用上一轮的档位或默认档 |
| Jev 回答无法解析 | 同上 | 同上，在 `router.jsonl` 里记录原始回答的前 200 字 | 同上 |
| 提示没被消费（会话 ID 或提示哈希对不上） | 提示 120 秒后过期 | 网关走网关模式；`router status` 里统计提示命中率 | 少了 harness 上下文，路由照常工作 |
| `router.json` 无效 | `Load` 返回错误 | 路由组退化为 magpie 原有的行为（按成员顺序，即 balanced 优先） | 不再路由，但可用 |
| 某档整档失败 | 档位组的所有成员都失败 | 路由组把选中档排第一，其余按 §4.4 的成员顺序（balanced、perf、fast）转移：fast 失败依次到 balanced、perf；balanced 失败先到 perf；perf 失败先到 balanced，最后才到 fast | 可能多花钱，不会中断 |
| `pre-agent` 输出了不合法的 JSON | Claude Code 忽略这个 hook | 处理程序先在内部校验再输出 | 子代理用默认模型 |
| Codex 的 hook 尚未被用户信任 | hook 不运行 | 路由走网关模式；`queqiao router status` 在近 1 小时有 Codex 请求、却没有收到过 Codex hook 调用时，提示用户在 Codex 里执行 `/hooks` 信任插件 | 少了 harness 上下文，路由照常工作 |
| Codex 忽略 `spawn_agent` 的 `model` | 子代理仍用主会话的模型 | S9 不成立时删去 Codex 的 `pre-agent`；子代理请求若带的是 `group/queqiao`，按网关模式路由 | 子代理选档精度下降 |
| Pi 的 `setModel` 返回 false | 模型没切换 | 改用 `before_provider_request` 替换请求体里的 `model`（S4 结论决定哪一种为主路径） | 无 |
| 派生会话找不到父会话 | `ParentOf` 返回 false | 按新会话处理，`Prev = nil` | 第一轮可能换档，缓存没接上 |
| 把主会话缺提示的一轮误当成 fork 轮次 | 沿用主会话当前的档位 | 等同于 R7，不影响正确性；`router status` 统计其比例（第 5.8 节） | 这一轮少了升档判断 |
| `GET /v1/queqiao/session` 查不到父会话（Codex、Pi 的 fork 子代理） | 返回 `404` | `pre-agent` / `tool_call` 不改参数，子代理继承父代理的模型，请求按网关逻辑处理 | 可能换档 |
| 网关重启导致会话状态丢失 | `Prev=nil` | 当作第一轮处理 | 一轮的档位可能不准 |

---

## 8. 测试策略

| 层级 | 内容 | 工具 |
| --- | --- | --- |
| 单元 | `policy.go`：第 5.2 节每条规则至少一个用例，外加规则之间的优先级、`performance` 封顶、迟滞（降档的三种情形）、升档保持计数。以表驱动方式编写 | `go test -tags nogui ./internal/router/...` |
| 单元 | `config.go`：合法、缺档位、阈值越界、项目级覆盖只作用于 `criteria` | 同上 |
| 单元 | `policy.go` 的继承：`Prev` 来自父会话副本时，缓存热、分类判低时保持父会话档位；`EscalatedLeft` 原样继承 | `go test` |
| 单元 | `session.go` 的派生关系：显式 `parent_session` 优先于 `forked_from_thread_id`，二者优先于 `firstWords` 匹配；未标记的会话不做跨会话匹配；24 小时窗口 | `go test` |
| 集成 | fork 轮次：主会话经 hook 选为 performance 后，同会话、同开头、无提示的新轮次请求进入 `qq-perf`，且主会话 `TurnState` 不变；Codex 请求带 `forked_from_thread_id` 时，新线程第一轮继承父线程的档位 | `internal/gateway` 测试包 |
| 集成 | hook：Claude Code `pre-agent` 遇到 `subagent_type: "fork"` 不输出；Codex `pre-agent` 遇到 `fork_context: true` 输出父会话的档位组；`session-start` 在 `source: "fork"` 时调用 `/lineage` | `internal/harness/...` |
| 单元 | `hint.go`：TTL、取出即删、按空会话兜底匹配；`experiment.go`：同一会话分组稳定、比例偏差 < 2%（1 万个随机会话） | 同上 |
| 单元 | `classify.go`：用本地假的 System One 服务器，覆盖正常、超时、429、无法解析、备选 LLM 编号回答 | `httptest` |
| 集成 | 仿照上游 `rules_test.go` 的写法：路由组配三个假档位成员，验证（a）有提示时首个成员是提示档位；（b）同一轮的工具往返留在原档；（c）没有提示时走网关模式；（d）子代理请求单独算轮次；（e）整档失败时的转移方向 | `internal/gateway` 测试包 |
| 集成 | hook 处理程序：分别用 Claude Code 与 Codex 的 hook 输入 JSON 样例作为 stdin，假网关校验收到的 `/turn` 请求（Codex 的要带 `turn_id`）；`pre-agent` 的输出能被 JSON 解析，并保留原参数；Codex 输入里的 `model` 不是路由组时不调用 `/turn` | `internal/harness/claudecode`、`internal/harness/codex` |
| 单元 | `turnmeta.go`：从请求头、从 `client_metadata` 读 `turn_id`；元数据是转义成 ASCII 的 JSON 字符串时也能解析（openai/codex#19620 之后的格式）；两处都没有时返回空串 | `go test` |
| 集成 | 提示匹配：同一会话里两条提示文本相同、`turn_id` 不同，Codex 请求按 `turn_id` 各取各的提示 | `internal/gateway` 测试包 |
| 端到端 | 启动 `queqiao serve`、假上游（会流式回复的 OpenAI 兼容服务）和假 Jev；先用 Anthropic Messages 格式、再用带 `x-codex-turn-metadata` 的 OpenAI Responses 格式，各模拟一遍“第一轮简单提问 → 第二轮说‘不对’ → 第三轮继续”，断言三轮档位依次为 fast、balanced（R3 升档）、balanced（R4 保持） | Go 测试，带 `e2e` 构建标签 |
| 插件 | `claude plugin validate --strict clients/claude-code` 通过；`clients/codex/plugin.json` 与 `.agents/plugins/marketplace.json` 通过 JSON Schema 校验（Schema 取自 Codex 插件文档引用的 Agent Plugins schema） | CI |
| Pi 包 | 扩展在给定事件下发出的 HTTP 请求和 `setModel` 调用（模拟 `pi` 对象）；`tool_call` 对 pi-subagents `Agent` 工具参数的改写，包括 `inherit_context` 的钉档 | vitest |

CI 中所有测试都不访问真实的 TypeSafe 和模型厂商。

---

## 9. 验收实验（`SP5`）

- **开启**：`router.json` 设 `experiment.enabled=true`。分组键为会话 ID，`sha256(salt + session)` 对 100 取模后小于 `router_percent` 的进入 `router` 组。
- **第一阶段**：`control_tier=performance`，至少运行 2 周，或者每组积累到 150 个以 PR 结束的会话，取先达到的那个。
- **第二阶段**：`control_tier=fast`，至少运行 1 周；任一方明显无法正常工作时提前终止（附件里这一组不到一天就停了）。
- **`queqiao router report`** 每组输出：会话数、每会话成本（中位数、均值、p90，中位数带 bootstrap 95% 置信区间）、档位分布、开出 PR 的会话占比、以合并 PR 结束的会话占比（双比例 z 检验）、手动换模型的会话占比、提示命中率、缓存写入费用占总费用的比例。
- PR 的最终状态由 `report` 调用 `gh pr view <url> --json state` 补查。没有安装 `gh` 时，这一列显示“未知”，其余指标照常输出。
- **样本量提醒**：个人使用很难攒够让合并率差异显著的样本量。报表在样本少于每组 100 个时，在合并率旁标注“样本不足”，只把成本和手动换模型率作为结论依据。

---

## 10. 先行验证（`SP0`，在其他子项目开工前完成）

每项都预先定好了不成立时的备选方案，所以结论不会让设计停在待定状态。结果记录到 `docs/superpowers/notes/spike-results.md`。

| 编号 | 要验证的事 | 方法 | 不成立时 |
| --- | --- | --- | --- |
| S1 | `UserPromptSubmit` 里的 `prompt`，在规范化后与网关 `userText()` 规范化后的结果，哈希一致的比例 ≥ 95% | 装一个记录用的 hook，与上游 magpie 的请求日志对照 50 轮，包含 `@文件` 引用和粘贴图片的情形 | 改为只按会话 ID 匹配“该会话的下一个新轮次”，`HintStore` 的键退化为 `session` |
| S2 | hook 里的 `session_id` 与请求头 `x-claude-code-session-id` 是否一致 | 同上 | 只按提示哈希匹配（第 6.5 节第 2 步的兜底变成主路径） |
| S3 | `PreToolUse` 改写子代理工具参数里的 `model` 后是否生效，子代理工具的名字是 `Agent` 还是 `Task` | 改成 `haiku` 后，看网关收到的子代理请求的模型是否为 `group/qq-fast` | 删去 `pre-agent`；子代理请求按网关模式路由（magpie 已把子代理当作单独的轮次） |
| S4 | Pi 在 `before_agent_start` 中调用 `setModel` 是否对本轮生效；模型对象如何获取；扩展能否拿到稳定的会话 ID | 写一个最小扩展做实验 | 用 `before_provider_request` 替换 `model`；会话 ID 用 `randomUUID()` |
| S5 | 从本机到 Jev 的实际延迟（p50、p95） | 发送 100 次第 5.3 节的请求 | p95 > 1000 ms 时，把 `classify_timeout_ms` 提高到 1500，并把默认分类器换成延迟更低的本地小模型 |
| S6 | Codex 的 hook 输入里的 `session_id`、`turn_id`，与同一轮模型请求里的 `session_id` 请求头和 `x-codex-turn-metadata` 中的 `turn_id` 是否一致；元数据在请求头里还是只在请求体里 | 装一个记录用的 hook，在上游 magpie 前面记录 20 轮 Codex 请求 | `turn_id` 对不上：Codex 改用提示哈希匹配，与 Claude Code 相同；只在请求体里：确认 `ruleFor` 调用处拿得到原始请求体，拿不到就同样退回提示哈希 |
| S7 | Codex 是否执行插件自带的 `hooks/hooks.json`（参见 openai/codex#16430）；`timeout` 的单位；有 `.agents/plugins/marketplace.json` 时是否忽略 `.claude-plugin/marketplace.json` | 用实施当天的 Codex 安装插件并触发各事件 | 插件 hook 不执行：`queqiao router init` 改为把同样的 hook 写进 `~/.codex/hooks.json`；两个市场文件都被读：把 Claude Code 插件的市场文件移到 `clients/claude-code/` 下，安装命令改为带子目录的形式 |
| S8 | Codex hook 的 `permission_mode` 在计划模式下的取值；网关能否从 Codex 的 Responses 请求中识别失败的工具调用 | 在计划模式和普通模式下各触发一次 hook；构造一次失败的 shell 命令 | 没有计划模式的标识：Codex 下 `plan_mode` 恒为 false；识别不了工具失败：见第 6.9 节最后一段 |
| S9 | 通过 `PreToolUse` 给 `spawn_agent` 加上 `model` 后，子代理的请求是否使用该模型 | 加上 `group/qq-fast`，看网关收到的子代理请求的模型 | 删去 Codex 的 `pre-agent` |
| S10 | Claude Code fork 子代理的请求是否带主会话的 `x-claude-code-session-id`，`firstWords` 是否与主会话相同；`SubagentStart` 中 fork 的 `agent_type` 取值；调用时传 `model` 对 fork 是否生效 | `/subtask` 触发 fork，在网关记录请求 | 会话 ID 不同：fork 轮次改为按 `firstWords` 在 1 小时内活跃的会话里找父会话；`agent_type` 可识别 fork 时，`SubagentStart` hook 额外调用 `/lineage` 标记，使识别更准确 |
| S11 | Claude Code `/fork`、`/branch` 后的新会话：`SessionStart` 的 `source` 取值、新会话 ID、第一轮请求的 `firstWords` 是否与原会话相同 | 分别执行一次并记录 hook 输入与请求 | `/branch` 不触发 `SessionStart`：只支持 `/fork`，`/branch` 按新会话处理 |
| S12 | Pi `session_start`（`reason: fork`）能否读到父会话 ID；pi-subagents 的 `Agent` 工具参数名、`inherit_context` 时子代理的第一次请求是否与父会话共享前缀（能否命中缓存） | 最小扩展加 pi-subagents 实验 | 读不到父会话 ID：用 `/lineage` 加 `firstWords`；共享前缀：保持钉档；不共享：`inherit_context` 改为单独选档 |
| S13 | Codex `fork_context: true` 的子代理请求是否使用钉上的 `group/qq-<档位>`，以及元数据里 `parent_thread_id` 是否指向父线程 | 在 Codex 里让主代理派生一个 fork 子代理 | 钉档不生效：删去第 5.6 节第 2 步，fork 子代理按网关模式路由 |

---

## 11. 实施拆分（交给 superpowers 的方式）

按 `writing-plans` 的要求，“一个规格对应一份计划”，而本规格包含多个子系统，因此按下表拆分。**每个子项目单独执行一次 `superpowers:writing-plans`**，计划文件保存为 `docs/superpowers/plans/<日期>-queqiao-<子项目>.md`。本规格与所有计划都提交在 `queqiao` 分支上；每个子项目在自己的 `qq/sp<N>-<名字>` 分支和 worktree 里实施，完成后 PR 合回 `queqiao`（第 6.1 节）。

| 子项目 | 范围（本规格中的章节） | 依赖 | 完成标准 |
| --- | --- | --- | --- |
| `SP0-spike` | §10 | 无 | `spike-results.md` 记录 S1–S13 的结论，以及每项选用了主方案还是备选方案 |
| `SP1-fork` | §6.1 | SP0 | 仓库默认分支与标签规则按 §6.1 设好；`go test -tags nogui ./...` 全部通过；`queqiao serve` 可以启动；配置写在 `~/.config/queqiao`；不再自动更新、不再上报统计 |
| `SP2-router-core` | §4.4、§4.6、§5.1–5.4、§5.7、§5.8 中网关侧的识别与继承、§6.2–6.5（含 `turnmeta.go`、`/session`、`/lineage`）、§6.6 中标为 SP2 的命令、§7 中与网关相关的行 | SP1 | §8 中单元测试和网关集成测试全部通过；`router init --preset frontier` 能生成合法的配置 |
| `SP3-claude-code` | §4.5（Claude Code 部分）、§5.5、§5.8 中 Claude Code 的行、§6.6 中标为 SP3 的命令、§6.7；同时建立 `internal/harness/` 的公共部分 | SP2 | hook 集成测试通过；`claude plugin validate --strict` 通过；§8 的端到端用例通过 |
| `SP4-pi` | §4.5（Pi 部分）、§5.8 中 Pi 的行、§6.8 | SP2 | vitest 通过；在真实的 Pi 里手动走一遍：一轮简单提问后档位为 fast，模型随之切换 |
| `SP6-codex` | §4.5（Codex 部分）、§5.6、§5.8 中 Codex 的行、§6.6 中标为 SP6 的命令、§6.9 | SP3（复用 `internal/harness/` 的公共部分） | hook 集成测试通过；插件与市场文件通过 Schema 校验；§8 的 Responses 端到端用例通过；在真实的 Codex 里手动走一遍：信任 hook 后，一轮简单提问的请求进入 `qq-fast` |
| `SP5-eval` | §6.4 的 `feedback`、§6.6 中标为 SP5 的命令、§9 | SP3、SP4、SP6 至少完成一个 | 用合成的 `usage.jsonl` 和 `router.jsonl` 测试报表的统计结果；实验能开能关 |

执行顺序：`SP0 → SP1 → SP2 → (SP3 → SP6) ∥ SP4 → SP5`。SP4 与 SP3、SP6 互不依赖，可以放在不同的 git worktree 里并行；SP6 在 SP3 之后做，因为它复用 SP3 建立的 hook 公共代码。

**给执行者的约定**

- 推荐用 `superpowers:subagent-driven-development` 执行计划，每个任务启动一个新的子代理，任务完成后做代码审查。
- 所有 Go 测试的命令为 `go test -tags nogui ./...`，与上游一致。
- 对上游文件的改动仅限 §6.1 的表格和 §6.3 的挂钩。计划里如果出现第三处上游改动，先回到本规格修订，再继续实施。
- 每个子项目开始前，重新读一遍 §2 中与本子项目相关的约束。Claude Code、Codex 和 Pi 的接口更新都很快，以实施当天的官方文档为准；与本规格冲突时，在 `spike-results.md` 里记录，并回到本规格修订。

---

## 12. 风险

| 风险 | 影响 | 应对 |
| --- | --- | --- |
| 上游 magpie 迭代很快，几天发一版 | 合并 `main` 时冲突 | 新代码集中在新目录；对上游的挂钩只有两处；不改模块路径；每周合并一次 |
| 用户在提示里写“这是非常难的任务”诱导选高档 | 多花钱 | 后果仅限于费用；`report` 中能看到 performance 档的占比异常；必要时在 `router.json` 中设置每日 performance 轮次上限（v1 不做，记录在后续计划里） |
| 提示内容会发送给 TypeSafe | 隐私 | `router init` 时明确告知；敏感项目把 `classifier` 改为本地模型；企业用户可以与 TypeSafe 签零数据保留 |
| 订阅共享违反厂商条款 | 账号风险 | §4.2 第 7 条：实验期间主成员只用 API Key |
| Claude Code、Codex 或 Pi 的 hook / 事件接口变化 | 插件失效 | 失败安全设计保证路由退回网关模式；CI 里跑 `claude plugin validate` 和 Codex 插件的 Schema 校验 |
| Codex 的 `spawn_agent` 参数 `model` 没有写进工具说明，以后可能被拒收或改名 | Codex 子代理选档失效 | S9 记录实施时的版本；`pre-agent` 输出前不校验 Codex 版本，失效时退回网关模式（§7） |
| Codex 的 hook 需要用户逐个信任，插件更新后 hook 内容一变就要重新信任 | 用户没注意时 hook 不运行 | `router status` 的提示（§7）；hook 命令保持稳定，参数不随版本变化，减少重新信任的次数 |
| fork 与派生会话的行为在各 harness 里变化很快（Claude Code 一个月内两次改写 `/fork`） | 识别规则失效 | 识别失败时的后果只是“可能换档、缓存没接上”，不影响请求完成；S10–S13 记录实施时的版本 |
| 样本量太小，结论不可靠 | 误判 | §9 中报表的“样本不足”标注 |

---

## 13. 来源

- magpie 源码（`main` 的 commit `49ee7d8`）：`internal/gateway/rules.go`、`classify.go`、`decide.go`、`gateway.go`、`ir.go`，`internal/usage/usage.go`，`groups_cli.go`、`groups_rules_cli.go`
- [Claude Code Hooks](https://code.claude.com/docs/en/hooks)
- [Claude Code Subagents（含 fork）](https://code.claude.com/docs/en/sub-agents)、[Claude Code Commands（`/fork`、`/branch`、`/subtask`）](https://code.claude.com/docs/en/commands)
- [Claude Code Plugin manifest reference](https://code.claude.com/docs/en/plugins-reference)
- [Codex Hooks](https://developers.openai.com/codex/hooks)、[Codex 插件打包](https://developers.openai.com/plugins/build/plugins)、[Codex Subagents](https://learn.chatgpt.com/docs/agent-configuration/subagents)
- [openai/codex#26948（spawn_agent 的 model 与 reasoning_effort）](https://github.com/openai/codex/issues/26948)、[#16430（插件 hook 未执行）](https://github.com/openai/codex/issues/16430)、[#14981（fork_context）](https://github.com/openai/codex/issues/14981)、[#16371（fork_context 下的模型覆盖）](https://github.com/openai/codex/issues/16371)、[#27122（Responses 元数据）](https://github.com/openai/codex/pull/27122)、[#19620（元数据转义为 ASCII）](https://github.com/openai/codex/pull/19620)
- magpie 源码 `internal/gateway/session_parent.go`、`codex_backend.go`（Codex 元数据与请求头的处理）
- [Pi Session](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/session.md)、[tintinweb/pi-subagents](https://github.com/tintinweb/pi-subagents)
- [Pi Extensions](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/extensions.md)、[Pi Packages](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/packages.md)
- [TypeSafe Models](https://docs.typesafe.ai/models.md)、[API](https://docs.typesafe.ai/api.md)、[Confidence](https://docs.typesafe.ai/confidence.md)、[Jev 1.13 jaggedness](https://docs.typesafe.ai/model-jaggedness/jev-1.13.md)、[Parallel questions](https://docs.typesafe.ai/cookbooks/parallel_questions.md)、[Legal](https://docs.typesafe.ai/legal.md)
- [LangChain：How to Build a Model Router in the Harness](https://www.langchain.com/blog/how-to-build-a-model-router-in-the-harness)、[Open SWE model_selection.py](https://github.com/langchain-ai/open-swe/blob/main/agent/middleware/model_selection.py)
- [OpenRouter：Confidence Thresholds for Model Escalation Routing](https://openrouter.ai/blog/insights/confidence-thresholds-for-model-escalation-routing/)
- [superpowers brainstorming](https://github.com/obra/superpowers/blob/main/skills/brainstorming/SKILL.md)、[writing-plans](https://github.com/obra/superpowers/blob/main/skills/writing-plans/SKILL.md)
