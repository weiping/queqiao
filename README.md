# 鹊桥 queqiao

给编码 Agent 用的模型路由器：在 Agent 的 harness 里判断每一轮任务有多难，在本地网关里把请求派给合适的模型档位。简单的提问交给便宜的快模型，跨文件改动和难查的 bug 交给最强的模型。

queqiao fork 自 [yetone/magpie](https://github.com/yetone/magpie)，保留 magpie 的全部功能，在它的本地网关之上加一层路由。名字取自“鹊桥”：喜鹊（magpie）搭的桥，连起 Agent 的 harness 和模型网关。

> **状态：已实现，线上验收中。** 全部七个子项目（SP0–SP6）已按设计完成并合并（PR #4–#11），Claude Code、Pi、Codex 三条通路的真机验收全部通过。当前处于线上 A/B 实验阶段，首份真实报表待实验跑满后产出。总体设计与各子项目的执行结果见 [`docs/superpowers/specs/2026-10-02-queqiao-design.md`](docs/superpowers/specs/2026-10-02-queqiao-design.md) 与 `docs/superpowers/plans/`。

## 要做什么

- **事前选档**：每一轮用户发话时，插件把用户原话、计划模式、Agent 和子代理类型交给分类器（默认用 TypeSafe 的 [Jev](https://docs.typesafe.ai/introduction)），选出 `fast`、`balanced`、`performance` 三档之一。
- **事后升档**：用户说上一轮不对，或者上一轮工具调用失败过半，下一轮自动升一档。
- **子代理单独选档**：从零开始的子代理单独选档；继承父会话上下文的 fork 子代理跟随父会话的档位，保住 prompt cache。
- **档位与模型解耦**：每一档是网关里的一个路由组，组内有跨厂商的失败转移成员。换模型只改配置。
- **失败安全**：分类器、hook、网关任何一环出错，请求照常完成，只是少了路由。
- **自带验收**：按会话分组做线上 A/B，统计成本、合并 PR 的比例和手动换模型的次数。

## 组件

| 组件 | 用于 | 子项目 | 状态 |
| --- | --- | --- | --- |
| queqiao 网关与路由核心（`internal/router/`、`internal/harness/`） | 所有 Agent | SP1、SP2 | ✅ 已合并 |
| Claude Code 插件 `queqiao-router`（`clients/claude-code/`） | Claude Code | SP3 | ✅ 已合并，真机验收 4/4 |
| Pi 包 `@weiping/pi-queqiao`（`clients/pi/`） | Pi | SP4 | ✅ 已合并，真机验收 4/4 |
| Codex 插件 `queqiao-router-codex`（`clients/codex/`） | Codex | SP6 | ✅ 已合并，hook 验收通过 |
| 验收报表 `queqiao router report` | 所有 Agent | SP5 | ✅ 已合并，首份真实报表待实验跑满 |

## 快速开始

一键安装（macOS / Linux / Termux，下载经 SHA-256 校验，装进 `~/.local/bin`）：

```sh
curl -fsSL https://raw.githubusercontent.com/weiping/queqiao/queqiao/install.sh | sh
```

升级重跑同一条命令即可（始终装最新 release）；也可以指定版本或目录：

```sh
… | sh -s -- --version qq-v0.1.0   # 装某个版本
… | sh -s -- --bin-dir ~/bin       # 装到别处
```

Windows 一键安装（PowerShell，同样校验 SHA-256，装进 `~\.local\bin`）：

```powershell
irm https://raw.githubusercontent.com/weiping/queqiao/queqiao/install.ps1 | iex
```

也可以到 [releases](https://github.com/weiping/queqiao/releases) 手动下载 `queqiao-cli-windows-<arch>.exe`。所有版本见 releases 页（`qq-v*` 标签触发构建，见「分支」一节）。

也可以从源码构建：

```sh
make cli                                # 构建 ./queqiao（纯终端版，不需要 cgo）
```

> ⚠️ 不要用 `go install github.com/yetone/magpie@…`：Go 模块路径保留为上游的 `github.com/yetone/magpie`（减少合并冲突），这样装到的是**上游 magpie**，不是 queqiao。

装好以后：

```sh
queqiao router init --preset cn       # 生成 router.json 和四个路由组（还有 frontier/anthropic 预设）
queqiao serve                         # 启动网关，默认 127.0.0.1:3425
queqiao router status                 # 检查配置、映射和最近的决策
queqiao router check                  # 只查配置：成员有没有厂商服务、上下文窗口够不够
queqiao router check --yes            # 再对每个成员发真实的带工具请求（会计费）
```

预设里有模型没有任何已配置的厂商提供时，`router init` 不会把它写进组里；某一档一个都解析不了就先借用最近一档的成员（performance 借 balanced），并打印 `queqiao group set qq-perf models=…` 提示你换成自己的模型。

把 Agent 指到网关（写各 Agent 自己的配置文件；`haiku`/`sonnet`/`opus`/`fable` 是 Claude Code 的四个别名，`queqiao claude group/queqiao` 会把 `fable` 也指到路由组，所以单独再设一次）：

```sh
queqiao claude group/queqiao
queqiao claude haiku group/qq-fast
queqiao claude sonnet group/qq-balanced
queqiao claude opus group/qq-perf
queqiao claude fable group/qq-perf
queqiao codex group/queqiao
queqiao pi group/qq-balanced
```

### 从 magpie 切换

不用卸载 magpie：两个二进制（`magpie` / `queqiao`）和配置目录（`~/.config/magpie` / `~/.config/queqiao`）互不相干。但**两个网关都监听 3425，不能同时运行**——退出 magpie（并 `magpie autostart off`）再 `queqiao serve`。要保留 providers、订阅登录、定价和使用记录，把配置整体拷过去：

```sh
magpie autostart off && pkill -x magpie   # 退出 magpie
cp -R ~/.config/magpie/. ~/.config/queqiao/
queqiao router init --preset cn           # 四个路由组 + router.json
queqiao serve
```

订阅登录态（Claude Code、Codex 等）存在 agent 自己的目录（`~/.claude`、`~/.codex`），不在 magpie 配置里，不受影响。跑稳一阵子再决定是否删 `~/.local/bin/magpie` 和 `/Applications/Magpie.app`。

再按你的 Agent 装对应的插件：

```sh
# Claude Code（需要 ≥ v2.1.287，见 clients/claude-code/README.md）
claude plugin marketplace add weiping/queqiao
claude plugin install queqiao-router@queqiao --config gateway_url=http://127.0.0.1:3425

# Codex（需要 queqiao 在 PATH 里，装完要在 /hooks 里信任 hook，见 clients/codex/README.md）
codex plugin marketplace add weiping/queqiao
codex plugin add queqiao-router-codex@queqiao

# Pi（见 clients/pi）
pi install npm:@weiping/pi-queqiao   # 升级：pi update
```

Pi 侧默认模型会被 `router init` 指到 `magpie/group/qq-balanced`，扩展逐轮换档；手动 `/model` 会触发 feedback 并停止自动换档。装完重启 Pi 会话生效。

线上 A/B 实验在 `~/.config/queqiao/router.json` 里开启（`experiment` 字段），跑满后用 `./queqiao router report --since 14d` 出报表。

没有插件的 Agent（如 OpenCode）也可以直接选用路由组，由网关自己分类，只是少了 harness 侧的上下文。

## 面板：`queqiao web` 与 `queqiao tui`

一键安装的是终端版，不含桌面 app；需要图形界面时用这两个（改的都是 `~/.config/queqiao`，与 CLI 等价）：

```sh
queqiao web
# ● queqiao web on http://127.0.0.1:3430/?k=<本次的 key>
```

`web` 就是 app 那套界面跑在浏览器里：Providers、订阅登录、路由组、用量、Settings 全能改，**自带网关**（不需要先 `serve`；已有网关在跑则直接用）。它自己服务的网关和 `serve` 一样带路由层（`/v1/queqiao/*`、按档选组）。路由页的实时请求只在网关由这个界面服务时可见；网关由 `queqiao serve` 服务时，路由页只显示一句「看不到」的提示——想在页面上看路由，就用 `queqiao web` 代替 `queqiao serve` 常驻。每次启动生成新 key，链接里直接带上；要固定 key（比如每天打开不想重新拿链接）设 `MAGPIE_WEB_KEY`（≥16 位，可用字母/数字/`-`、`.`、`_`、`~`，登入 400 天）：

```sh
MAGPIE_WEB_KEY=固定的一串字符 queqiao web --addr 0.0.0.0:3430 --lan   # --lan：局域网可访问
queqiao web --no-open        # 不自动开浏览器（无头/SSH 场景）
queqiao web --gateway        # 精简模式：只留网关页，无 Agents/Sessions/Library
```

```sh
queqiao tui                  # 同一套界面的终端版；没有别的网关在跑时，开着它就顺便把（带路由的）网关服务了
```

> magpie 的桌面 App（Magpie.app）改不了 queqiao 的配置——它只读写 `~/.config/magpie`，两个目录互不相干。

## 配置

queqiao 的路由配置有两处：网关里的四个路由组（存在 `~/.config/queqiao/providers.json`），和 `~/.config/queqiao/router.json`。两者都由 `queqiao router init --preset <frontier|anthropic|cn>` 生成，之后直接改文件即可。

### 路由组

| 组 | 用途 | 路由策略 |
| --- | --- | --- |
| `qq-fast` / `qq-balanced` / `qq-perf` | 三个档位组，各自是「主成员 + 失败转移成员」的列表 | `order`，`stays=auto`（缓存还热就留在同一账号） |
| `queqiao` | 路由组，Agent 的模型就指到它：`group/queqiao` | `order`，`stays=turn`（每轮重新选档，同一轮内不变） |

换模型只改档位组的成员，例如 `queqiao group set qq-fast models=glm/glm-5.3-flash:high,deepseek/deepseek-v4-flash`。成员可以带 `:effort` 后缀指定推理强度。

### `router.json`

`queqiao router init` 生成的默认值如下，字段都可以改：

```json
{
  "version": 1,
  "router_group": "queqiao",
  "tiers": {
    "fast":        { "group": "qq-fast",     "claude_alias": "haiku",  "criteria": "…" },
    "balanced":    { "group": "qq-balanced", "claude_alias": "sonnet", "criteria": "…" },
    "performance": { "group": "qq-perf",     "claude_alias": "opus",   "criteria": "…" }
  },
  "default_tier": "balanced",
  "classifier": "local",
  "classify_timeout_ms": 1500,
  "thresholds": { "tier_min": 0.4, "dissatisfied_min": 0.7 },
  "escalate_turns": 2,
  "cache_ttl_seconds": 300,
  "fixed_agents": { "Explore": "fast", "Plan": "performance" },
  "experiment": { "enabled": false, "router_percent": 50, "control_tier": "performance", "salt": "…" }
}
```

| 字段 | 默认 | 说明 |
| --- | --- | --- |
| `router_group` | `queqiao` | 路由组名，Agent 的模型指到 `group/<这个名字>` |
| `tiers.<档>.group` | — | 该档对应的网关路由组 |
| `tiers.<档>.claude_alias` | — | Claude Code 侧的别名映射（`haiku`/`sonnet`/`opus`） |
| `tiers.<档>.criteria` | — | 交给分类器的选档标准（自然语言） |
| `default_tier` | `balanced` | 分类失败或不可信时的落档（规则 R8） |
| `classifier` | `local` | 分类器：`typesafe/jev-latest`，或任意 `provider/model`（普通模型没有置信度，`tier_min` 按回答是否合法记 1/0） |
| `classify_timeout_ms` | `1500` | 分类超时；超时该轮不落新档，下一轮补偿 |
| `thresholds.tier_min` | `0.4` | 分类结果的最低置信度（R5） |
| `thresholds.dissatisfied_min` | `0.7` | 「用户在说上一轮不对」的判定阈值（R3 升档） |
| `escalate_turns` | `2` | 升档后保持的轮数（R4） |
| `cache_ttl_seconds` | `300` | 降档迟滞：距上次请求超过它才允许立即降档（R6） |
| `fixed_agents` | 见下 | 固定档位的子代理类型（R1）：`Explore`、`statusline-setup`、`claude-code-guide`、`explorer` → `fast`；`Plan` → `performance` |
| `experiment.enabled` | `false` | 开启线上 A/B |
| `experiment.router_percent` | `50` | 走路由的会话比例（0–100，其余走对照组） |
| `experiment.control_tier` | `performance` | 对照组钉死的档位 |
| `experiment.salt` | init 随机生成 | 分组哈希的盐，**写死后不要改**，否则实验前后不可比 |

配置无效时路由自动降级：网关照常服务，只是少了选档。`queqiao router status` 会报告具体的配置错误。

### 分类器：TypeSafe Jev

分类器默认是 `"local"`——这是个占位值，要换成实际的 `provider/model`（如 `ollama/qwen3-4b`），否则分类一直失败，路由只能走默认档。推荐的分类器是 TypeSafe 的 [Jev](https://docs.typesafe.ai/introduction)：单请求返回档位选择和置信度，以及「用户在说上一轮不对」的概率，不用自己拼提示词。

```sh
# 1. 加 TypeSafe provider（key 从 https://console.typesafe.ai/keys 拿）
queqiao provider add typesafe <api-key>
```

```jsonc
// 2. ~/.config/queqiao/router.json
{
  "classifier": "typesafe/jev-latest",
  "classify_timeout_ms": 1500
}
```

不想直连 TypeSafe 还有两个中继预设：`vercel-jev`（Vercel AI Gateway，分类器写 `vercel-jev/typesafe-ai/jev`）和 `cloudflare-jev`（Cloudflare Workers AI，写 `cloudflare-jev/typesafe/jev`）。

时延提示：实测本机到 TypeSafe 的请求在 300–1500ms 之间波动，偶发超时时该轮不落新档、下一轮自动补偿（spec §5.7）。如果网络到 TypeSafe 不稳定，用本地小模型更稳。

### 项目级覆盖

在项目根目录放 `.queqiao/router.json`，只能覆盖各档的 `criteria`（比如告诉分类器「这个仓库的改动大多是跨服务的」），其余字段一律忽略：

```json
{
  "tiers": {
    "balanced": { "criteria": "本项目是普通前端仓库，单文件改动居多" }
  }
}
```

### 环境变量

| 变量 | 说明 |
| --- | --- |
| `QUEQIAO_URL` | 插件和 hook 访问网关的地址，默认 `http://127.0.0.1:3425`。Codex 的 hook 子进程不继承自定义环境变量，生产部署请让网关跑在默认端口 |

Claude Code 插件的网关地址另有 `gateway_url` 设置（`claude plugin install queqiao-router@queqiao --config gateway_url=…`），与 `ANTHROPIC_BASE_URL` 是两条独立通道。

## 分支

| 分支 | 用途 |
| --- | --- |
| `queqiao`（默认分支） | queqiao 的开发主干，定期合并 `main` |
| `main` | 上游 yetone/magpie 的镜像，只做快进同步，不直接提交 |
| `qq/sp<N>-<名字>` | 各子项目的功能分支，完成后 PR 合回 `queqiao` |
| `qq-v*` 标签 | 发版标签：推送后自动构建各平台 CLI 并发布 Release（`install.sh` 从这里下载）；注意上游用的 `v*` 前缀会交接给 yetone/magpie-releases，queqiao 不用它 |

为了让合并上游尽量不冲突，queqiao 的代码放在 `internal/router/`、`internal/harness/`、`clients/` 这几个新目录里，对上游文件只做少量挂钩；Go 模块路径保留 `github.com/yetone/magpie` 不改。

## 构建与测试

Go 部分和上游相同：

```sh
make cli                          # 纯终端版 ./queqiao，不需要 cgo
go test -tags nogui ./...
```

客户端部分各自带测试：

```sh
cd clients/pi && npm test         # vitest（pi-queqiao）
```

## 参考

- LangChain：[How to Build a Model Router in the Harness](https://www.langchain.com/blog/how-to-build-a-model-router-in-the-harness)
- OpenRouter：[Confidence Thresholds for Model Escalation Routing](https://openrouter.ai/blog/insights/confidence-thresholds-for-model-escalation-routing/)
- TypeSafe：[Jev 文档](https://docs.typesafe.ai/introduction)

## 许可

MIT，与上游相同。上游的版权声明保留在 [LICENSE](LICENSE) 中。

---

**以下为上游 magpie 的原版 README。** 其中的安装、自动更新、官网和社区链接都指向上游 magpie，不适用于 queqiao。

---

<div align="center">

<a href="https://usemagpie.ai"><img src="site/public/img/icon-256.png" width="120" alt="magpie"></a>

# magpie

### Every agent's model. One place.

Claude Code on Kimi, Codex on DeepSeek, Gemini CLI on GLM, OpenCode on your ChatGPT plan.<br>
Switch them from the menu bar. One local gateway serves them all, and it moves to another account when a quota runs out.

[![Release](https://img.shields.io/github/v/release/yetone/magpie-releases?label=release&color=111111)](https://github.com/yetone/magpie-releases/releases/latest) [![Stars](https://img.shields.io/github/stars/yetone/magpie?style=flat&color=111111)](https://github.com/yetone/magpie/stargazers) [![Discord](https://img.shields.io/badge/Discord-join-5865F2?logo=discord&logoColor=white)](https://discord.gg/vGSnD3ZKQF) [![License](https://img.shields.io/badge/license-MIT-111111)](LICENSE)<br>
![macOS](https://img.shields.io/badge/macOS-000000?logo=apple&logoColor=white) ![Windows](https://img.shields.io/badge/Windows-0078D4?logo=windows&logoColor=white) ![Linux](https://img.shields.io/badge/Linux-FCC624?logo=linux&logoColor=black) ![Docker](https://img.shields.io/badge/Docker-2496ED?logo=docker&logoColor=white) ![Termux](https://img.shields.io/badge/Termux-000000?logo=android&logoColor=white)

**[Download](https://usemagpie.ai)** · **[Docs](https://usemagpie.ai/docs/start)** · **[Reference](docs/reference.md)** · **[Discord](https://discord.gg/vGSnD3ZKQF)** · **English** · [简体中文](README.zh-CN.md)

<br>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="site/public/img/agents-dark.png">
  <img src="site/public/img/agents-light.png" width="900" alt="magpie's Agents page: Claude Code on Kimi K3, Codex on DeepSeek V4 Pro, Gemini CLI on GLM-5.3, each picked from one list">
</picture>

</div>

<br>

## Why magpie

You probably use more than one coding agent. Each one keeps its model in its own file, in its own format, with its own keys and base URLs. Each one also has its own idea of which vendors it supports. A subscription you pay for works in one agent and nowhere else. When it runs out at 3 pm, you start editing config files.

magpie puts all of it in one place:

<table>
<tr>
<td width="33%" valign="top">

**🎛 One screen for every agent**<br>
Over 35 agents in one list. Click a model and pick another. magpie changes only that one key in the agent's own config file. Comments, ordering and formatting stay as they were.

</td>
<td width="33%" valign="top">

**🔌 One gateway for every API**<br>
`127.0.0.1:3425` speaks OpenAI Chat, OpenAI Responses, Anthropic Messages and Gemini. It translates between them, streaming, tool calls and reasoning included.

</td>
<td width="33%" valign="top">

**🔀 Routing that keeps going**<br>
Put several models from several providers in a routing group. When one hits a rate limit or runs out of quota, the next one answers. Your agent never sees the error.

</td>
</tr>
<tr>
<td valign="top">

**🔑 Subscriptions you can share**<br>
Your Claude, ChatGPT, Copilot, Gemini or Grok sign-in becomes a provider that every other agent can use. There is no key to copy.

</td>
<td valign="top">

**🧩 Plugins**<br>
OpenCode auth plugins and pi provider packages from npm run in magpie as they do in their own apps. Any plan a plugin signs in to works in every agent. A plugin can also be gateway middleware that reads and rewrites every request and reply.

</td>
<td valign="top">

**📊 Usage and cost tracking**<br>
See tokens, cache hits, cost at list price, balances and quota windows for every provider and account. You can also set a limit for each key.

</td>
</tr>
</table>

<br>

## Pick any model for any agent

Click a value and a filtered list opens. It holds every model of every provider you added, as `provider/model`. Pick one and the agent's config file is rewritten safely and atomically. Use **Profiles** to save the setup of every agent under one name ("Budget", "Focus") and switch them all at once.

magpie also lives in the **menu bar**. There is a tray panel on macOS, Windows and Linux, a full window, a **TUI** (`magpie tui`), a **web UI** (`magpie web`) and a plain **CLI**.

<table>
<tr>
<td width="50%">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="site/public/img/panel-dark.png">
  <img src="site/public/img/panel-light.png" alt="The menu bar panel">
</picture>
</td>
<td width="50%">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="site/public/img/picker-dark.png">
  <img src="site/public/img/picker-light.png" alt="The model picker">
</picture>
</td>
</tr>
</table>

## Add providers with one field

Pick a preset and paste a key. That's it. The model list comes from the vendor itself, with names and reasoning levels filled in from [models.dev](https://models.dev), so a model released this morning shows up on the next refresh. No model list is built into magpie.

**Presets include** Anthropic · OpenAI · Google Gemini · DeepSeek · Kimi · Zhipu GLM · MiniMax · StepFun · Qwen · Baidu Qianfan · Tencent Cloud · Huawei Cloud MaaS · Volcengine Ark · Mistral · Groq · xAI · OpenRouter · Together · Fireworks · SiliconFlow · NVIDIA NIM · ModelScope · Ollama · LM Studio … and any OpenAI-compatible or Anthropic-compatible URL.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="site/public/img/add-dark.png">
  <img src="site/public/img/add-light.png" width="900" alt="Add provider: subscriptions to sign in to, vendors, relays and local servers">
</picture>

Already set up somewhere else? **Import** reads the providers you set up in CC Switch, Claude Code, Codex and Alma. **[Add to magpie](https://usemagpie.ai/docs/import)** links let a provider's website hand its config to magpie in one click.

## Routing that keeps going

A **routing group** is several models that an agent picks as one: `group/daily-coding`. The gateway spreads requests over every member's keys and accounts:

| Mode | What it does |
| --- | --- |
| `smart` | Of the subscriptions with quota left, use the one whose allowance renews soonest, so less is lost at the reset |
| `order` | Use the first model until it can't answer, then the next |
| `rotate` | Move to the next member on each turn |
| `usage` | Use the least-used member first |
| `pace` | Use the account with the most of its week left per hour until its reset |

Conversations **stay with the account that answered them** while the vendor's prompt cache is still worth keeping. **Intent routing** goes further: a small model you choose reads each new turn, so tests can go to the strong model and quick questions to the fast, cheap one. Groups can contain other groups. The Routing tab shows each decision live.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="site/public/img/routing-dark.png">
  <img width="760" src="site/public/img/routing-light.png" alt="The Routing view: four agents through magpie to seven providers, live">
</picture>

<table>
<tr>
<td width="50%">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="site/public/img/intent-trace-dark.png">
  <img src="site/public/img/intent-trace-light.png" alt="An intent-routed turn, explained step by step">
</picture>
</td>
<td width="50%">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="site/public/img/nested-routing-dark.png">
  <img src="site/public/img/nested-routing-light.png" alt="A routing group inside a routing group">
</picture>
</td>
</tr>
</table>

→ [Intent routing, in depth](https://usemagpie.ai/docs/intent)

## Sign in once, use it everywhere

An agent you're signed in to is a subscription with models behind it, so magpie offers it as a provider. Several accounts per subscription are supported, and magpie fails over between them.

- **Claude**: drives the real local `claude` binary and bridges your agent's tools over MCP
- **Codex / ChatGPT**: your ChatGPT plan's models in every other agent
- **GitHub Copilot**, **Gemini (Code Assist)**, **Antigravity**, **Grok (SuperGrok)**, **Devin**, **Cursor** and others
- **Any OpenCode auth plugin or pi package** from npm:

```sh
magpie plugin add opencode-gemini-auth   # an OpenCode plugin from npm
magpie plugin add pi-antigravity         # a pi package, the same way
magpie plugin login google-plugin        # its own sign-in flow, in magpie
```

magpie runs plugins on [Bun](https://bun.sh), which it downloads the first time a plugin needs it. A plugin signs in, lists its models and makes each request. Agents use its models like any other provider's. Browse the community plugins at **[magpie-community/plugins](https://github.com/magpie-community/plugins)**, or [write your own](https://usemagpie.ai/docs/plugins).

A plugin can also be **gateway middleware**: JavaScript run inside magpie's gateway on what every agent sends and gets back, whatever the provider. `onRequest` can rewrite a request or turn it away, `onEvent` sees each streamed event, and `onResponse` sees a whole reply. It runs in-process, so an event costs about a microsecond, and a hook that throws or runs too long leaves the request as it was.

```js
// alias.middleware.js — magpie plugin add ./alias.middleware.js
export function onRequest(body, ctx) {
  if (body.model === "fast") return { ...body, model: "deepseek/deepseek-chat" };
}
```

Ready-made middleware, most of it what [New API](https://github.com/QuantumNous/new-api) does for its channels, with the same JSON, is under Plugins › Discover › Gateway middleware:

| Package | What it does |
|---|---|
| `param-override` | New API's `param_override`: set, delete, move or rewrite request fields, under conditions, or turn a request away |
| `model-map` | New API's `model_mapping`: send a model under another name; replies keep the name asked for |
| `system-prompt` | Your system prompt on every request, or some agents' or models' |
| `word-guard` | New API's sensitive-word filter: turn away or mask words in what users send, and in replies |
| `think-tags` | Take `<think>…</think>` out of replies, or put `reasoning_content` into them |

```sh
magpie plugin add @magpie-community/middleware-model-map
magpie plugin options model-map '{"mapping": {"fast": "deepseek/deepseek-chat"}}'
```

See [Gateway middleware](https://usemagpie.ai/docs/plugins#middleware).

## Usage and cost tracking

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="site/public/img/usage-dark.png">
  <img src="site/public/img/usage-light.png" width="900" alt="The Usage page: balances, tokens, cache hit rate and cost">
</picture>

- Tokens, cache reads and writes, reasoning tokens and calls, with **cost at list price**. You can set your own prices for each model.
- **Balances and quota windows** for every key, plan and subscription account (`magpie quota`). The **reset reminder** warns you before a window renews with much of it unused.
- Usage by **session**: each agent conversation with its cost and title, and a command to resume it.
- Usage by **account** and by **upstream key**, so you can check a vendor's bill line by line.
- **OTLP export** to your own observability stack.

## Share one magpie

- **Share it on your network.** Turn on *Share on local network*, then create a named **gateway key** for each client, each with its own daily, weekly or monthly token and cost limit.
- **Remote magpie.** A laptop can use the providers, accounts and routing groups of the magpie on your desktop, while still wiring its own agents.
- **Docker.** Run `ghcr.io/yetone/magpie` on a server or a NAS, and manage it from the web UI.
- **Sync.** Back up to a file, or keep machines in sync over WebDAV (Nutstore, Nextcloud…) or S3.

## Library: instructions, MCP servers and skills

Write your instructions, MCP servers and skills once. magpie writes them into each agent's own files in that agent's own format. It removes only what it wrote, so everything else in those files stays as it was.

## Supported agents

<table>
<tr><td>

Claude Code · Claude Desktop · Codex · Gemini CLI · OpenCode · OpenChamber · MiMo Code · Pi · Aside · OmO · Goose · Cursor CLI · Zed · VS Code Chat · VS Code Insiders · JetBrains Air · Copilot CLI · Crush · DeepSeek Harness · Command Code · fx · oh-my-pi · Devin · Hermes Agent · Mister Morph · Kimi Code · Muse Code · Empryo · MiniMax Code · Droid · Cline · Qoder · Qoder CN · Grok Build · ZCode · WorkBuddy · T3 Code · OpenHanako · AtomCode · Alma

</td></tr>
</table>

magpie shows only the agents installed on your machine. Anything else that takes a base URL can use the gateway too:

```sh
export OPENAI_BASE_URL=http://127.0.0.1:3425/v1     OPENAI_API_KEY=magpie
export ANTHROPIC_BASE_URL=http://127.0.0.1:3425     ANTHROPIC_API_KEY=magpie
export GOOGLE_GEMINI_BASE_URL=http://127.0.0.1:3425 GEMINI_API_KEY=magpie
```

### Zed-compatible Agent paths

Magpie can use a Zed-compatible fork or installation whose executable or
configuration directory is different from the upstream defaults. Set these
variables before starting Magpie:

```sh
export MAGPIE_ZED_BIN=/Applications/ZedG.app/Contents/MacOS/zedg
export MAGPIE_ZED_CONFIG_DIR="$HOME/.config/zed"
export MAGPIE_ZED_PROCESS_NAMES=zedg,ZedG
magpie serve
```

`MAGPIE_ZED_BIN` controls installation detection, `MAGPIE_ZED_CONFIG_DIR`
selects the directory containing `settings.json`, and
`MAGPIE_ZED_PROCESS_NAMES` supplies comma-separated process names used for
restart notices. The existing Zed configuration adapter is reused, so this is
intended for forks that keep Zed's `settings.json` and Agent model schema, such
as [ZedG](https://github.com/x6nux/zed-globalization). These variables affect
the Magpie process in which they are set; put them in the service environment
when running Magpie under systemd or Docker. They configure a Zed-compatible
Agent on the same machine as Magpie; they do not discover or modify an Agent
running on another host. On macOS, if the configured binary does not resolve to
a `.app` bundle, Magpie falls back to the standard Zed application locations
when authorizing the gateway credential.

## Quick start

**1. Install.** Download the app from **[usemagpie.ai](https://usemagpie.ai)**, or run:

```sh
curl -fsSL https://usemagpie.ai/install.sh | sh
```

<sub>Mac builds are signed and notarised. Every build updates itself. Behind a firewall, use `--proxy` or `--mirror`. You can also use `go install github.com/yetone/magpie@latest`, or the [Docker image](docs/reference.md#docker).</sub>

**2. Add a provider.** Open magpie, then go to **Providers → Add provider**. Pick a preset, or sign in with a subscription.

**3. Pick a model** for each agent on the **Agents** page. Start a new agent session and it uses the new model.

Or do the same from the terminal:

```sh
magpie provider add deepseek sk-…              # a preset needs only the key
magpie claude deepseek/deepseek-v4-pro          # Claude Code on DeepSeek
magpie codex moonshot/kimi-k2.5                 # Codex on Kimi
magpie group add "Opus anywhere" models=claude/claude-opus-5-5,copilot/claude-opus-5.5 routing=smart
magpie claude group/opus-anywhere               # fails over between subscriptions
magpie save work && magpie use work             # profiles
magpie quota                                    # what's left on every plan
magpie tui                                      # the whole thing, in a terminal
```

## Documentation

- **[Get started](https://usemagpie.ai/docs/start)**: the guided tour
- **[Reference](docs/reference.md)**: every agent, provider option, gateway endpoint, CLI command and file
- **[Plugins](https://usemagpie.ai/docs/plugins)**: use plugins and write your own
- **[Intent routing](https://usemagpie.ai/docs/intent)**: route each turn by what it asks for
- **[Import links](https://usemagpie.ai/docs/import)**: "Add to magpie" buttons for provider websites

## Community

Questions, ideas or a model that won't show up? Join us on **[Discord](https://discord.gg/vGSnD3ZKQF)** or [open an issue](https://github.com/yetone/magpie/issues).

If magpie saves you from editing one more config file, **a ⭐ helps others find it.**

<a href="https://star-history.com/#yetone/magpie&Date">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=yetone/magpie&type=Date&theme=dark">
    <img src="https://api.star-history.com/svg?repos=yetone/magpie&type=Date" width="600" alt="Star history">
  </picture>
</a>

## License

[MIT](LICENSE)
