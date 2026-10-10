# 鹊桥 Magpie Bridge

给编码 Agent 用的模型路由器：在 Agent 的 harness 里判断每一轮任务有多难，在本地网关里把请求派给合适的模型档位。简单的提问交给便宜的快模型，跨文件改动和难查的 bug 交给最强的模型。

Magpie Bridge 和官方 [magpie](https://github.com/yetone/magpie) 并排运行：magpie 是本地模型网关，Magpie Bridge 是它旁边的路由器，命令叫 `mbridge`，后台进程 `mbridge serve` 默认监听 `127.0.0.1:3426`。中文名“鹊桥”，英文名是它的直译：喜鹊（magpie）搭的桥，连起 Agent 的 harness 和模型网关。项目早期曾叫 queqiao（鹊桥的拼音）。Magpie Bridge 不含、也不修改 magpie 的代码：先装官方 magpie，再装 Magpie Bridge。

> **状态：** 自 SP8（2026-10-10）起不再是 magpie 的 fork，原来的上游镜像分支改名为 `archive/magpie-mirror`，开发主干是 `main`。设计见 [`docs/superpowers/specs/2026-10-10-queqiao-sp8-standalone-design.md`](docs/superpowers/specs/2026-10-10-queqiao-sp8-standalone-design.md) 和 [`docs/superpowers/specs/2026-10-10-magpie-bridge-sp9-rename-design.md`](docs/superpowers/specs/2026-10-10-magpie-bridge-sp9-rename-design.md)。

## 要做什么

- **事前选档**：每一轮用户发话时，插件把用户原话、计划模式、Agent 和子代理类型交给分类器（默认用 TypeSafe 的 [Jev](https://docs.typesafe.ai/introduction)），选出 `fast`、`balanced`、`performance` 三档之一。
- **事后升档**：用户说上一轮不对，或者上一轮工具调用失败过半，下一轮自动升一档。
- **事后复核**（可选，默认关闭）：轮末把这一轮的请求和最终回复交给分类器判「到底有没有解决」，命中就下一轮升档；先把 `review.mode` 设 `shadow` 攒样本、用 `mbridge router calibrate` 看阈值站不站得住，再切 `act`。开启后回复会发给你配的分类器厂商，所以默认关。
- **子代理单独选档**：从零开始的子代理单独选档；继承父会话上下文的 fork 子代理跟随父会话的档位，保住 prompt cache。
- **档位与模型解耦**：每一档是网关里的一个路由组，组内有跨厂商的失败转移成员。换模型只改配置。
- **失败安全**：分类器、hook、网关任何一环出错，请求照常完成，只是少了路由。
- **自带验收**：按会话分组做线上 A/B，统计成本、合并 PR 的比例和手动换模型的次数；`mbridge router calibrate` 另外用「下一轮的不满 / 手动升档 / 工具失败」当标签，给出各分数分段的选低率与建议阈值。

## 组件

| 组件 | 用于 | 状态 |
| --- | --- | --- |
| 守护进程（`mbridge serve`）：`/v1/bridge/*` 与 Codex、网关模式的代理（`internal/router/`、`internal/proxy/`） | 所有 Agent | SP8 起独立于 magpie |
| Claude Code 插件 `magpie-bridge`（`clients/claude-code/`） | Claude Code | 0.2.0，默认连 3426 |
| Pi 包 `@weiping/pi-magpie-bridge`（`clients/pi/`） | Pi | 0.2.0，自报工具统计 |
| Codex 插件 `magpie-bridge-codex`（`clients/codex/`） | Codex | 0.2.0，配合 `codex -p mbridge` |
| 报表、复核与校准（`mbridge router report`、`calibrate`） | 所有 Agent | 成本取 magpie 的 `usage --csv` |

## 快速开始

1. **先装官方 magpie**（[yetone/magpie](https://github.com/yetone/magpie)），让它的网关跑在默认的 `127.0.0.1:3425`，配好你的厂商和订阅。

2. **再装 mbridge**（下载经 SHA-256 校验，装进 `~/.local/bin`，并注册登录时启动的 mbridge）：

   ```sh
   curl -fsSL https://raw.githubusercontent.com/weiping/magpie-bridge/main/install.sh | sh
   ```

   Windows（PowerShell）：

   ```powershell
   irm https://raw.githubusercontent.com/weiping/magpie-bridge/main/install.ps1 | iex
   ```

   指定版本或目录：`… | sh -s -- --version v0.2.0 --bin-dir ~/bin`；不想开机启动加 `--no-service`，之后手动 `mbridge serve`。升级用 `mbridge update`。

3. **建档位并接上 Agent**：

   ```sh
   mbridge router init --preset cn       # 在 magpie 里建四个路由组，写 router.json，接好 Claude Code 和 Pi（还有 frontier/anthropic 预设）
   mbridge status                        # magpie、mbridge、分组是否齐全，最近的决策
   mbridge router check                  # 分组在不在、上下文窗口够不够（加 --yes 再发真实请求，会计费）
   ```

4. **装插件**：

   ```sh
   # Claude Code（需要 ≥ v2.1.287）
   claude plugin marketplace add weiping/magpie-bridge
   claude plugin install magpie-bridge@magpie-bridge        # 默认连 http://127.0.0.1:3426

   # Codex（需要 mbridge 在 PATH 里，装完在 /hooks 里信任 hook）
   codex plugin marketplace add weiping/magpie-bridge
   codex plugin add magpie-bridge-codex@magpie-bridge
   codex -p mbridge                                     # 用 mbridge 的 profile 启动，请求经 mbridge 选档

   # Pi
   pi install npm:@weiping/pi-magpie-bridge                   # 升级：pi update
   ```

Claude Code 和 Pi 的请求直接发给 magpie，插件逐轮把模型切到 `group/mb-<档>`；Codex 的请求先到 mbridge，代理按轮把 `group/mbridge` 改写成某一档再转给 magpie。没有插件的 Agent 把 base URL 设成 `http://127.0.0.1:3426/v1`、模型设成 `group/mbridge`，就是网关模式。

其他命令：

```sh
mbridge router calibrate              # 三个分数各自的选低率与建议阈值（先跑 shadow 攒样本）
mbridge router report --since 14d     # 线上实验报表：成本、合并率、升档来源、未升档轮次的选低率
mbridge service install|uninstall|status
mbridge version                       # mbridge 和 magpie 的版本
```

## 配置

mbridge 的路由配置有两处：magpie 里的四个路由组（存在 magpie 自己的配置里），和 `~/.config/magpie-bridge/router.json`。两者都由 `mbridge router init --preset <frontier|anthropic|cn>` 生成，之后直接改文件即可。

### 路由组

| 组 | 用途 | 路由策略 |
| --- | --- | --- |
| `mb-fast` / `mb-balanced` / `mb-perf` | 三个档位组，各自是「主成员 + 失败转移成员」的列表 | `order` |
| `mbridge` | 路由组：Codex 和网关模式的请求带着 `group/mbridge` 进 mbridge，代理按轮改写成某一档；绕过 mbridge 直接请求它时，magpie 按顺序落到 balanced | `order` |

换模型只改档位组的成员，例如 `magpie group set mb-fast models=glm/glm-5.3-flash:high,deepseek/deepseek-v4-flash`。成员可以带 `:effort` 后缀指定推理强度。

`mb-fast`、`mb-balanced`、`mb-perf`、`mbridge` 这四个分组名归 Magpie Bridge 使用：`mbridge router init` 会直接覆盖同名分组，别拿它们做别的用途。

### `router.json`

`mbridge router init` 生成的默认值如下，字段都可以改：

```json
{
  "version": 1,
  "router_group": "mbridge",
  "tiers": {
    "fast":        { "group": "mb-fast",     "claude_alias": "haiku",  "criteria": "…" },
    "balanced":    { "group": "mb-balanced", "claude_alias": "sonnet", "criteria": "…" },
    "performance": { "group": "mb-perf",     "claude_alias": "opus",   "criteria": "…" }
  },
  "default_tier": "balanced",
  "classifier": "local",
  "classify_timeout_ms": 1500,
  "thresholds": {
    "tier_min": 0.4,
    "dissatisfied_min": 0.7,
    "review_min": 0.7,
    "review_confidence_min": 0.5
  },
  "review": { "mode": "off", "timeout_ms": 5000, "max_answer_chars": 6000 },
  "escalate_turns": 2,
  "cache_ttl_seconds": 300,
  "fixed_agents": { "Explore": "fast", "Plan": "performance" },
  "experiment": { "enabled": false, "router_percent": 50, "control_tier": "performance", "salt": "…" }
}
```

| 字段 | 默认 | 说明 |
| --- | --- | --- |
| `router_group` | `mbridge` | 路由组名，Agent 的模型指到 `group/<这个名字>` |
| `listen` | `127.0.0.1:3426` | mbridge 的监听地址 |
| `magpie_url` | `http://127.0.0.1:3425` | 官方 magpie 网关的地址 |
| `tiers.<档>.group` | — | 该档对应的网关路由组 |
| `tiers.<档>.claude_alias` | — | Claude Code 侧的别名映射（`haiku`/`sonnet`/`opus`） |
| `tiers.<档>.criteria` | — | 交给分类器的选档标准（自然语言） |
| `default_tier` | `balanced` | 分类失败或不可信时的落档（规则 R8） |
| `classifier` | `local` | 分类器：`typesafe/jev-latest`，或任意 `provider/model`。普通模型用结构化输出给出档位置信度与不满分数（实测 Kimi 支持）；厂商拒绝 `response_format` 时（如 DeepSeek 返回 400）退回只回编号的提示词，此时置信度按回答是否合法记 1/0，事件里的 `classifier` 标 `#plain` |
| `classify_timeout_ms` | `1500` | 分类超时；超时该轮不落新档，下一轮补偿 |
| `thresholds.tier_min` | `0.4` | 分类结果的最低置信度（R5） |
| `thresholds.dissatisfied_min` | `0.7` | 「用户在说上一轮不对」的判定阈值（R3 升档） |
| `thresholds.review_min` | `0.7` | 轮末复核判「没解决」的阈值（R3 升档，`review.mode` 非 `off` 时才有用） |
| `thresholds.review_confidence_min` | `0.5` | 复核自身的置信度下限，低于它不算命中 |
| `thresholds.overrides` | — | 按 harness 或 agent 覆盖上面几个阈值，例如 `[{"harness":"codex","tier_min":0.5}]`；同时写 harness 和 agent 的条目优先于只写一个的 |
| `review.mode` | `off` | 轮末复核：`off` 不问；`shadow` 只记 `would_review` 不影响路由；`act` 命中 R3 升档 |
| `review.timeout_ms` | `5000` | 后台复核的超时（不影响本轮回复，回复早已发出） |
| `review.max_answer_chars` | `6000` | 送审的回复上限，超出保留前 2000 和后 4000 字 |
| `escalate_turns` | `2` | 升档后保持的轮数（R4） |
| `cache_ttl_seconds` | `300` | 降档迟滞：距上次请求超过它才允许立即降档（R6） |
| `fixed_agents` | 见下 | 固定档位的子代理类型（R1）：`Explore`、`statusline-setup`、`claude-code-guide`、`explorer` → `fast`；`Plan` → `performance` |
| `experiment.enabled` | `false` | 开启线上 A/B |
| `experiment.router_percent` | `50` | 走路由的会话比例（0–100，其余走对照组） |
| `experiment.control_tier` | `performance` | 对照组钉死的档位 |
| `experiment.salt` | init 随机生成 | 分组哈希的盐，**写死后不要改**，否则实验前后不可比 |

配置无效时路由自动降级：mbridge 照常启动，代理只做透传，`group/mbridge` 由 magpie 按顺序落到 balanced。`mbridge router status` 会报告具体的配置错误。

### 分类器：TypeSafe Jev

分类器默认是 `"local"`——这是个占位值，要换成实际的 `provider/model`（如 `ollama/qwen3-4b`），否则分类一直失败，路由只能走默认档。推荐的分类器是 TypeSafe 的 [Jev](https://docs.typesafe.ai/introduction)：单请求返回档位选择和置信度，以及「用户在说上一轮不对」的概率，不用自己拼提示词。

```sh
# 1. 加 TypeSafe provider（key 从 https://console.typesafe.ai/keys 拿）
magpie provider add typesafe <api-key>
```

```jsonc
// 2. ~/.config/magpie-bridge/router.json
{
  "classifier": "typesafe/jev-latest",
  "classify_timeout_ms": 1500
}
```

不想直连 TypeSafe 还有两个中继预设：`vercel-jev`（Vercel AI Gateway，分类器写 `vercel-jev/typesafe-ai/jev`）和 `cloudflare-jev`（Cloudflare Workers AI，写 `cloudflare-jev/typesafe/jev`）。

时延提示：实测本机到 TypeSafe 的请求在 300–1500ms 之间波动，偶发超时时该轮不落新档、下一轮自动补偿（spec §5.7）。如果网络到 TypeSafe 不稳定，用本地小模型更稳。

### 轮末复核与置信度校准

默认 `"review": {"mode": "off"}`——**默认关闭**，因为开启后每轮的回复会被发给分类器厂商（走你已配置的 classifier 渠道）。想用它，按这三步走：

1. `"mode": "shadow"`：复核照常发出，但只把 `would_review` 记进 `~/.config/magpie-bridge/router.jsonl`，路由行为一点不变。跑几天，攒样本。
2. `mbridge router calibrate`：看三个分数（档位置信度、不满分数、复核分数）各自的选低率和建议阈值。样本不足 30 的分段会标「样本不足」。标签只反映**用户表达出来的不满**，所以顶部有提示；要更准的判断用 `mbridge router calibrate --csv` 导出逐轮明细人工核对。
3. 数字站得住再切 `"mode": "act"`：复核命中时下一轮按 R3 升档，`router.jsonl` 的 decide 事件里 reason 会是 `R3-review`。

复核在后台跑，本轮回复不等它；结果晚到（下一轮已经开始）就丢弃，不会配错轮次。

### 项目级覆盖

在项目根目录放 `.mbridge/router.json`，只能覆盖各档的 `criteria`（比如告诉分类器「这个仓库的改动大多是跨服务的」），其余字段一律忽略：

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
| `MBRIDGE_URL` | 插件和 hook 访问 mbridge 的地址，默认 `http://127.0.0.1:3426`。Codex 的 hook 子进程不继承自定义环境变量，请让 mbridge 跑在默认端口 |

Claude Code 插件的地址另有 `gateway_url` 设置（`claude plugin install magpie-bridge@magpie-bridge --config gateway_url=…`）。

## 分支与发版

| 分支 | 用途 |
| --- | --- |
| `main`（默认分支） | 开发主干 |
| `archive/magpie-mirror` | SP8 之前的上游 magpie 镜像，已停止同步，只作历史参考 |
| `qq/<名字>` | 功能分支，完成后 PR 合回 `main` |
| `v*` 标签 | 推送后构建六个二进制（darwin、linux、windows 的 amd64 和 arm64）和 `checksums.txt` 并发布 Release |

Magpie Bridge 和 magpie 之间只走公开接口（HTTP、CLI、usage CSV），全部集中在 `internal/magpie`。CI 每天拿官方最新的 magpie 跑契约测试（`contract/`），接口变了会自动开 issue。

## 构建与测试

```sh
make build                        # ./mbridge，纯 Go，不需要 cgo
make test                         # go vet、go test 和安装脚本的测试
cd clients/pi && npx vitest run   # Pi 扩展
```

## 参考

- LangChain：[How to Build a Model Router in the Harness](https://www.langchain.com/blog/how-to-build-a-model-router-in-the-harness)
- OpenRouter：[Confidence Thresholds for Model Escalation Routing](https://openrouter.ai/blog/insights/confidence-thresholds-for-model-escalation-routing/)
- TypeSafe：[Jev 文档](https://docs.typesafe.ai/introduction)

## 许可

MIT。mbridge 曾是 [yetone/magpie](https://github.com/yetone/magpie) 的 fork，上游的版权声明保留在 [LICENSE](LICENSE) 中。
