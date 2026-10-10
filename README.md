# 鹊桥 queqiao

给编码 Agent 用的模型路由器：在 Agent 的 harness 里判断每一轮任务有多难，在本地网关里把请求派给合适的模型档位。简单的提问交给便宜的快模型，跨文件改动和难查的 bug 交给最强的模型。

queqiao 和官方 [magpie](https://github.com/yetone/magpie) 并排运行：magpie 是本地模型网关，queqiao 是它旁边的路由器（后台进程 queqiaod，默认 `127.0.0.1:3426`）。名字取自“鹊桥”：喜鹊（magpie）搭的桥，连起 Agent 的 harness 和模型网关。queqiao 不含、也不修改 magpie 的代码，先装官方 magpie，再装 queqiao。

> **状态：** 自 SP8（2026-10-10）起 queqiao 不再是 magpie 的 fork，`main` 分支的上游镜像已停止同步，只作历史参考。qq-v0.1.x 的用户用 `queqiao migrate` 把数据交还官方 magpie。设计见 [`docs/superpowers/specs/2026-10-10-queqiao-sp8-standalone-design.md`](docs/superpowers/specs/2026-10-10-queqiao-sp8-standalone-design.md)。

## 要做什么

- **事前选档**：每一轮用户发话时，插件把用户原话、计划模式、Agent 和子代理类型交给分类器（默认用 TypeSafe 的 [Jev](https://docs.typesafe.ai/introduction)），选出 `fast`、`balanced`、`performance` 三档之一。
- **事后升档**：用户说上一轮不对，或者上一轮工具调用失败过半，下一轮自动升一档。
- **事后复核**（可选，默认关闭）：轮末把这一轮的请求和最终回复交给分类器判「到底有没有解决」，命中就下一轮升档；先把 `review.mode` 设 `shadow` 攒样本、用 `queqiao router calibrate` 看阈值站不站得住，再切 `act`。开启后回复会发给你配的分类器厂商，所以默认关。
- **子代理单独选档**：从零开始的子代理单独选档；继承父会话上下文的 fork 子代理跟随父会话的档位，保住 prompt cache。
- **档位与模型解耦**：每一档是网关里的一个路由组，组内有跨厂商的失败转移成员。换模型只改配置。
- **失败安全**：分类器、hook、网关任何一环出错，请求照常完成，只是少了路由。
- **自带验收**：按会话分组做线上 A/B，统计成本、合并 PR 的比例和手动换模型的次数；`queqiao router calibrate` 另外用「下一轮的不满 / 手动升档 / 工具失败」当标签，给出各分数分段的选低率与建议阈值。

## 组件

| 组件 | 用于 | 子项目 | 状态 |
| --- | --- | --- | --- |
| queqiao 网关与路由核心（`internal/router/`、`internal/harness/`） | 所有 Agent | SP1、SP2 | ✅ 已合并 |
| Claude Code 插件 `queqiao-router`（`clients/claude-code/`） | Claude Code | SP3 | ✅ 已合并，真机验收 4/4 |
| Pi 包 `@weiping/pi-queqiao`（`clients/pi/`） | Pi | SP4 | ✅ 已合并，真机验收 4/4 |
| Codex 插件 `queqiao-router-codex`（`clients/codex/`） | Codex | SP6 | ✅ 已合并，hook 验收通过 |
| 验收报表 `queqiao router report` | 所有 Agent | SP5 | ✅ 已合并，首份真实报表待实验跑满 |
| 轮末复核与置信度校准（`internal/router/review.go`、`calibrate.go`） | 所有 Agent | SP7 | ✅ 已合并，三 Agent 真机验收（Claude Code / Pi 通过；Codex 需在 `/hooks` 重新信任 Stop，`codex exec` 下不发） |

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
queqiao router calibrate              # 三个分数各自的选低率与建议阈值（先跑 shadow 攒样本）
queqiao router report --since 14d     # 线上实验报表：成本、合并率、升档来源、未升档轮次的选低率
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

仓库里有一条经过完整往返测试的迁移脚本（迁移配置 → 停 magpie → 卸载 binary/app/缓存，一切只转移进备份，`restore` 一键回滚）：

```sh
curl -fsSL https://raw.githubusercontent.com/weiping/queqiao/queqiao/migrate-from-magpie.sh -o migrate-from-magpie.sh
sh migrate-from-magpie.sh            # --dry-run 先看动作；--migrate-only 只迁配置；restore 回滚
```

手动等价步骤（脚本内部即如此）：退出 magpie（两个网关同抢 3425，不能并行）→ `cp -R ~/.config/magpie/. ~/.config/queqiao/` → `queqiao router init --preset cn` → `queqiao serve`。订阅登录态（Claude Code、Codex 等）存在 agent 自己的目录（`~/.claude`、`~/.codex`），不在 magpie 配置里，不受影响。

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

## 面板：`queqiao web`、`queqiao tui` 与桌面 App

一键安装的是终端版；需要图形界面时有三个等价入口（同一套界面，改的都是 `~/.config/queqiao`）：

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

### 桌面 App（菜单条/托盘）

每个 `qq-v*` release 都带三个平台的桌面 App：

| 平台 | 资产 | 安装 |
| --- | --- | --- |
| macOS | `queqiao-app-macos.zip` | 解压拖进 `/Applications`；**当前未签名**：首次打开前 `xattr -dr com.apple.quarantine /Applications/queqiao.app`（配好签名后自动变为签名+公证版，双击即开） |
| Windows | `queqiao-windows-<arch>.exe` | 放入 PATH 或双击；未签名，SmartScreen 首次会问一次（「更多信息 → 仍要运行」，与上游 magpie 一致） |
| Linux | `queqiao-linux-<arch>` | 放入 `~/.local/bin`；需系统已装 GTK 3 + WebKitGTK 4.1（发行版通常自带运行库），否则用 `queqiao web` |

开发中也可以从源码构建：`make app`（macOS，需 Xcode 命令行工具）、`make release-windows`（任意平台交叉编译）、`make release-linux`（需 libgtk-3-dev + libwebkit2gtk-4.1-dev，本机构建）。

App 与 CLI/web/tui 同一二进制：内嵌带路由的网关，Providers/Routing/Settings/Usage 各页直接改 `~/.config/queqiao`；菜单条 tooltip、窗口标题、页面标题都是 queqiao（页面内部文案仍为上游的 magpie 拼写——刻意保留，见「分支」节的同步策略）。开机自启二选一：`queqiao autostart on`（app 形态）或 launchd 无头 daemon（`queqiao serve`）——两者同抢 3425 不能并存。

路由器专属字段（tier 分类器、Jev、A/B 实验）不在任何图形界面里，用 `queqiao router status / init` 管理。

**签名与公证（维护者，一次性）**：有 Apple 开发者账号时，跑 `scripts/setup_apple_secrets.sh`——它生成 CSR、引导仅有的两次网页操作（签发 Developer ID Application 证书、创建 App Store Connect API 密钥），然后自动导出 `.p12`、提取 Team ID 并设齐 6 个 GitHub secrets。之后每个 `qq-v*` release 自动签名 + 公证 + staple。详见 [`docs/signing.md`](docs/signing.md)。

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
| `router_group` | `queqiao` | 路由组名，Agent 的模型指到 `group/<这个名字>` |
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

### 轮末复核与置信度校准

默认 `"review": {"mode": "off"}`——**默认关闭**，因为开启后每轮的回复会被发给分类器厂商（走你已配置的 classifier 渠道）。想用它，按这三步走：

1. `"mode": "shadow"`：复核照常发出，但只把 `would_review` 记进 `~/.config/queqiao/router.jsonl`，路由行为一点不变。跑几天，攒样本。
2. `queqiao router calibrate`：看三个分数（档位置信度、不满分数、复核分数）各自的选低率和建议阈值。样本不足 30 的分段会标「样本不足」。标签只反映**用户表达出来的不满**，所以顶部有提示；要更准的判断用 `queqiao router calibrate --csv` 导出逐轮明细人工核对。
3. 数字站得住再切 `"mode": "act"`：复核命中时下一轮按 R3 升档，`router.jsonl` 的 decide 事件里 reason 会是 `R3-review`。

复核在后台跑，本轮回复不等它；结果晚到（下一轮已经开始）就丢弃，不会配错轮次。

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
| `qq-v*` 标签 | 发版标签：推送后自动构建 7 平台 CLI、三平台桌面 App（mac 签名公证待配证书、Windows/Linux 未签名）与 SHA-256 校验并发布 Release（`install.sh` 从这里下载）；注意上游用的 `v*` 前缀会交接给 yetone/magpie-releases，queqiao 不用它 |

为了让合并上游尽量不冲突，queqiao 的代码放在 `internal/router/`、`internal/harness/`、`clients/` 这几个新目录里，对上游文件只做少量挂钩；Go 模块路径保留 `github.com/yetone/magpie` 不改。品牌化同理按冲突面分层：应用身份（bundle、窗口/菜单条/页面标题）与 CLI 帮助文本是 queqiao（提交分叉），页面内部文案保持上游拼写；每周同步时工作流会自动重刷 CLI 品牌（`scripts/brand_cli.py`，合并后跑一次、幂等），手动解冲突时 issue 指引里也带这一步。

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

Claude Code · Claude Desktop · Codex · Gemini CLI · OpenCode · OpenChamber · MiMo Code · Pi · Aside · OmO · Goose · Cursor CLI · Zed · VS Code Chat · VS Code Insiders · VSCodium Chat · JetBrains Air · Copilot CLI · Crush · DeepSeek Harness · Reasonix Studio · Command Code · fx · oh-my-pi · Devin · Hermes Agent · Mister Morph · Kimi Code · Muse Code · Empryo · MiniMax Code · Droid · Cline · Qoder · Qoder CN · Grok Build · ZCode · WorkBuddy · CodeBuddy Code · T3 Code · OpenHanako · AtomCode · Alma

</td></tr>
</table>

magpie shows only the agents installed on your machine. Anything else that takes a base URL can use the gateway too:

```sh
export OPENAI_BASE_URL=http://127.0.0.1:3425/v1     OPENAI_API_KEY=magpie
export ANTHROPIC_BASE_URL=http://127.0.0.1:3425     ANTHROPIC_API_KEY=magpie
export GOOGLE_GEMINI_BASE_URL=http://127.0.0.1:3425 GEMINI_API_KEY=magpie
```

### Reasonix Studio

Reasonix Studio is detected from its desktop installation or a native Go
`reasonix` 2.x or 1.39.x CLI; historical npm wrappers and a shared config alone
do not count. Tested with
[Studio 2.24.0](https://github.com/esengine/DeepSeek-Reasonix/releases/tag/studio-v2.24.0).

Pick the Executor model in the app, or use
`magpie reasonix magpie/deepseek/deepseek-chat` (`magpie reasonix
magpie/group/code` for a routing group). Select Plan independently with
`magpie reasonix planner magpie/deepseek/deepseek-chat`, or use `magpie reasonix
planner off` to disable the separate planner. `magpie reasonix effort high` sets
an advertised Executor reasoning level. `magpie reasonix default` restores the
previous Executor selection; `magpie reasonix planner default` restores Plan.
Magpie's provider and private `.env` key are removed when neither role needs
them. Restart Studio for new sessions; project/session overrides still take
precedence. Other providers and credentials are preserved. A non-managed
provider named `magpie` is a conflict, reported without overwriting it. Studio
and the native CLI share these files, so updating only Studio does not isolate
their settings.

The released-client stream/tool contract test is run with
`MAGPIE_TEST_REASONIX_CLI=/path/to/reasonix go test ./internal/agent -run
'^TestReasonixStudioCLIIntegration$' -count=1`. It uses isolated settings and
a local test upstream, with no vendor credentials.

### VSCodium Chat

VSCodium's Chat features are disabled by default. To use the Chat model
picker with magpie, set `"chat.disableAIFeatures": false` in VSCodium's
settings and add the `defaultChatAgent` and `trustedExtensionAuthAccess`
entries required by [VSCodium's Copilot guide](https://github.com/VSCodium/vscodium/blob/master/docs/ext-github-copilot.md)
to VSCodium's `product.json`. The guide also explains how to install a
compatible GitHub Copilot Chat extension, since the Open VSX registry does
not normally provide Microsoft's extension. Restart VSCodium, or run
**Developer: Reload Window**, after changing these files. Magpie's models
then appear under the `magpie` custom endpoint group.

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
