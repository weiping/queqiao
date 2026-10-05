# SP3：Claude Code 插件 `queqiao-router`（mod）

> Spec：`docs/superpowers/specs/2026-10-02-queqiao-design.md` §6.7（主）、§5.5、§5.8（Claude Code 行）、§4.5（Claude Code 部分，env 已由 SP2 `router init` 写入，本计划只验证）、§8（mod 行的测试）、§11（验收标准 SP3-claude-code 行）。
>
> 前置：SP2 已合并（PR #6，`e8179bb`/`aef17af`）——网关的 `/v1/queqiao/turn`、`/feedback`、`/router` 端点已在 `internal/router/api.go` 实现。
>
> 本计划以 TypeScript 为主（`clients/claude-code/` + 仓库根 `.claude-plugin/marketplace.json`）；唯一的 Go 新增是 §8 要求的端到端测试（新文件，不改上游文件）——与 SP4/SP6 目录零重叠，可并行。
>
> **复审记录（2026-10-05）**：修复 2 阻断 + 1 spec 不符 + 3 补强——F1 补 Task 6 Step 0 的 §8 端到端 Go 测试（CC 部分）；F2 marketplace `source` 路径语义无实测证据，Task 1 加本地 add+install 实测步骤；F3 spec §6.7 的 plan_mode 来源改为 S1 实测的 `classic.UserPromptSubmit.permission_mode`（spec 已同步修订）；F4 `/turn` 补 `cwd`（防御式捕获）；F5 `mainTier` 为 null 时钉档不改写；F6 约束清单补齐 + 走查加 plan mode 项。

## 从 SP0 带入的实测事实（不再重新探测）

| 事实 | 来源 | 对实现的影响 |
| --- | --- | --- |
| `turn.step` 改写 `model` 成立（72/72 请求）；`/model opus` 后 `turn.step` 见 `group/qq-perf` 且不改写即放行 | S1 | 主路径照 §6.7 实现 |
| `count_tokens` 预检带未改写的 `group/queqiao`（不走 turn.step，网关 200 受理） | S1 旁路① | 无需处理，属预期 |
| 分类器等辅助调用走 haiku/sonnet 别名 → `group/qq-*`，触发无害的 `unrecognized_model` 警告 | S1 旁路② | 无需处理 |
| **CC 2.1.288+ 无 `fork` 子代理类型**；`agent.spawn` 的 fork 分支是防御性死代码 | S3 | `agent.spawn` 第 1 步保留但注释标注死代码 |
| `classic.SessionStart` 在 `-p` 模式被引擎跳过（不触发）；派生检测只在交互模式有效 | S3 附注① | README 说明 |
| Agent SDK 模式本地 spawn 失败（errno -88），CC 根本没启动，mod 不加载 | S10 | README 把 SDK 模式列为「无法本地 spawn（errno -88）待查」 |
| `/fork` 与 `/branch` 都报 `source:"fork"`（无独立 branch 字面量）；派生会话请求复用父会话 `x-claude-code-session-id` | S11 | 不区分 fork/branch；网关侧 fork 树视作同一会话是预期 |
| `$.store` 查找派生会话命中（first_fnv1a 相等） | S11 | §5.8 的 hash→session 机制照做 |
| Codex 只读 `.agents/plugins/marketplace.json`，忽略 `.claude-plugin/marketplace.json` | S7 | 根 `.claude-plugin/marketplace.json` 不会让 Codex 误装 CC 插件 |

## 工作区

- worktree：`.worktrees/qq-sp3-claude-code`，分支 `qq/sp3-claude-code`，基于 `queqiao` 分支最新提交。
- 产物结构（§6.7）：

```
clients/claude-code/
  .claude-plugin/plugin.json     // name=queqiao-router, types=./types/index.d.ts, userConfig.gateway_url
  hooks/hooks.json               // {"modules": ["./register.ts"]}
  hooks/register.ts
  hooks/register.test.ts         // claude plugin test
  types/index.d.ts               // PluginState 契约
  agents/repo-scout.md           // model: haiku, tools: Read/Grep/Glob
  README.md
.claude-plugin/marketplace.json  // 仓库根，source 指向 ./clients/claude-code
```

- 测试 rig：`claude-code/testing` 的 mock 模式（SP0 spike `register.test.ts` 已验证）：`on('http.fetch', …)` 充当假网关、`on('turn.step', …)` 在 mod 之下记录到达引擎的模型。
- 状态一律走 `$.state` 的 `atom`/`read`/`update`（hot reload 保留；模块变量会丢——spike 的 `firstCompleteDone` 模块变量在正式版必须改成 state）。

## Task 1：脚手架与 `claude plugin validate --strict`

**Step 1** 建目录树与全部静态文件：
- `plugin.json`：`name: "queqiao-router"`（不得以 `claude-` 开头）、`version: 0.1.0`、`types: "./types/index.d.ts"`、`userConfig.gateway_url`（默认 `http://127.0.0.1:3425`）。
- `types/index.d.ts`：`interface PluginState { "queqiao-router": { turn: {turnId,tier,group}|null; mainTier: string|null; agentTier: Record<string,string>; toolStats: {calls,failures}; derived: {is:boolean, checked:boolean} } }`。
- `hooks/hooks.json`：`{"modules": ["./register.ts"]}`。
- `agents/repo-scout.md`：frontmatter `name: repo-scout`、`model: haiku`、`tools: Read, Grep, Glob`；正文：只读代码搜索子代理的职责说明。
- `README.md`：安装（marketplace add → plugin install）、需要 CC ≥ v2.1.287、需要网关在跑、`gateway_url` 配置、SDK 模式 errno -88 说明、`--safe-mode`/`allowManagedModsOnly` 下 mod 不加载。
- 仓库根 `.claude-plugin/marketplace.json`：`name: "queqiao"`、plugins 数组仅 `queqiao-router`、`source: "./clients/claude-code"`——**已实测**（Task 1：marketplace add 本地路径 + install 成功，根目录相对形式正确）。

**Step 2** `register.ts` 先放最小空实现（每个事件 `return next(e)` 原样放行）。

**Step 3** 验证：
1. `claude plugin validate --strict clients/claude-code` 通过；
2. **marketplace 实测（F2）**：`claude plugin marketplace add <worktree 绝对路径>` → `claude plugin list` 能看到 queqiao-router → 安装后 mod 加载（可 quickest：跑一轮 `claude -p` 看 session.start 探活日志）。两种 source 写法按实测取一，失败的删掉；实测结论回写本计划。
3. 确认本机 `claude plugin test` 命令可用（CC 2.1.289）。

**Step 4** 提交 `feat(cc): queqiao-router plugin scaffold`。

## Task 2：主路径——`session.start` / `turn.start` / `turn.step`

**Step 1** 在 `claude-code/testing` rig 里先确认 `$.store`（get/set）、`$.clock`（sleep/now）、`$.ui.status` 可用 `on(...)` mock（spike 只验证过 `http.fetch`/`session.id`/`turn.step`）；不可 mock 的改用真实实现验证。后续各任务的测试都依赖这一点。

**Step 2（失败测试）** `register.test.ts`：
- `turn.start` 有提示文本 → 假网关收到 `POST /v1/queqiao/turn`，字段：`harness:"claude-code"`、`session`、`prompt`、`agent:"main"`、`plan_mode`、`cwd`（有则发）、`tool_calls`/`tool_failures`（上一轮的统计）、`store_hint:false`；成功后 state 的 `turn`/`mainTier` 写入。
- `turn.start` 与 1500ms 计时竞速：假网关 `$.clock.sleep(1500)` 先赢（fetch 挂起）→ `turn` 为空、`mainTier` 不变、不抛错。
- `e.text` 为空（续写轮）→ 不发 `/turn`，沿用 `mainTier`。
- `turn.step`：`group/queqiao` 且无 `agentId` → 改写为 `turn.group`；`turn` 为空 → 原样放行（网关模式兜底）；非路由组（`group/qq-perf`、`claude-sonnet-5`）→ 原样放行；`effort` 不改。
- `session.start`：假网关 `GET /v1/queqiao/router` 200 → 无 `$.ui.status`；失败 → `$.ui.status("queqiao: 网关未运行")`，后续事件照常。

**Step 3** 跑 `claude plugin test clients/claude-code` 看失败。

**Step 4** 实现：
- `session.start`：读 `options.gateway_url`，探活一次（try/catch）。
- `turn.start`：`e.text` 非空才调 `/turn`，`Promise.race([fetch, $.clock.sleep(1500)])`；`plan_mode` 从 `classic.UserPromptSubmit` 的 `e.permission_mode === 'plan'` 记入 state（**F3**：S1 实测的唯一读法，spec §6.7 已按此修订；该 hook 只记状态不发网络；空文本轮沿用最近记录值）；`cwd` 防御式捕获（**F4**）：`classic.SessionStart` 的 `e.cwd` 存在则记入 state（`-p` 模式该事件不触发，则缺省不发，网关只用全局配置）；`parent_session` 组装留到 Task 5。成功 → `update` state + `$.ui.status("queqiao: <tier> · <reason>")`；失败/超时 → `turn` 置空。末尾 `return next(e)`。`/turn` 字段含 `cwd`（有则发）。
- `turn.step`（generator）：只读 state，零 I/O。`mainTier` 为 null（会话开始就派子代理、`/turn` 从未成功）且带 `agentId` 的请求：不钉不写，原样放行（网关模式兑底，**F5**）。
- 约束（**F6**）：不用 `$.model.classify` 兼底；不写 `prompt.compose`/`additionalContext`；所有网络调用 fire-and-forget 或有界等待，全部 try/catch 包裹（失败安全）；`toolStats` 在 `turn.start` 尝试上报后即清（无论成败）。

**Step 5** 测试转绿。**Step 6** 提交 `feat(cc): main-path turn routing (session.start/turn.start/turn.step)`。

## Task 3：子代理——`agent.spawn` 五步 + `agentId` 钉档

**Step 1（失败测试）**：
- `agent.spawn`：`e.fork===true` → 不改（死代码分支，注释引 S3）；`e.model` 已有值 → 不改；`subagentType` 命中 R1 表（`Explore`→fast、`Plan`→performance 等）→ `next({...e, model:"haiku"|"opus"})`；其余 → 假网关 `/turn` 带 `prompt`、`agent:<subagentType>`、`store_hint:false` 得档位 → 别名改写；`/turn` 失败 → 不改。
- `turn.step` 带 `agentId` 且模型仍是 `group/queqiao`：第一次出现 → 钉在**此刻** `mainTier` 对应组并写入 `agentTier`；主会话随后换档（下一轮 `/turn` 返回别的档）→ 该 `agentId` 的后续 step 仍用钉住的组；已有 `agentTier` → 沿用；`mainTier` 为 null → 不钉不写，原样放行（F5，与 Task 2 约束一致）。
- 别名映射：`fast→haiku`、`balanced→sonnet`、`performance→opus`（§5.5 第 5 步；env 再解析成 `group/qq-*`）。

**Step 2** 看失败。**Step 3** 实现（R1 固定表与 Go 侧 `fixed_agents` 保持一致：Explore/statusline-setup/claude-code-guide→fast，Plan→performance，explorer→fast）。**Step 4** 绿。**Step 5** 提交 `feat(cc): subagent tier selection and agentId pinning`。

## Task 4：反馈——`tool.call` 统计与 PR 链接、`classic.PostModelSwitch`

**Step 1（失败测试）**：
- `tool.call`：主会话（无 `agentId`）调用计入 `toolStats.calls`，`r.isError===true` 计入 `failures`；带 `agentId` 的不计；`e.tool==="Bash"` 且 `r.text` 含 `https://github.com/<owner>/<repo>/pull/<n>` → 假网关收到 `POST /v1/queqiao/feedback` `{kind:"pr_created", value:<URL>}`；结果原样返回。
- `turn.start` 发 `/turn` 后 `toolStats` 清零（上报即清）。
- `classic.PostModelSwitch`：`source` 为 `command`/`picker` → `/feedback` `{kind:"manual_model_switch", value:"<from_model>→<to_model>"}`；`source` 为 `sdk`/`auto`/`resume` → 不发。

**Step 2–5** 同前。提交 `feat(cc): tool stats and manual-switch/pr feedback`。

## Task 5：派生会话——`classic.SessionStart` / `turn.complete` / `parent_session`

**Step 1（失败测试）**：
- `turn.complete`：非派生会话、主会话（无 `agentId`）的第一轮结束 → `$.store.set("qq:first:<fnv1a(首条用户消息)>", {session, at})`；第二轮不再写。24 小时清理用一个索引键 `qq:first-index`（`{hash:{session,at}}` 整体读写，写时剔除过期项）——$.store 无枚举 API，索引键是唯一途径（spike 只验证了 set/get，未验证枚举；实现按此约束设计）。
- `classic.SessionStart`：`source==="fork"`（`/branch` 同报 fork，S11）→ state `derived.is=true`；`startup`/`resume`/`clear` → false。
- 派生会话首个 `turn.start`：用 `$.session.messages()` 首条用户消息算同一 FNV-1a → `$.store.get("qq:first:<hash>")` 命中 → `/turn` 请求带 `parent_session`；查不到 → 不带（按新会话）；只查一次（`derived.checked`）。
- 非派生会话永不查 store（§5.8「只对标记过的会话做跨会话匹配」）。

**Step 2–5** 同前。提交 `feat(cc): derived-session tier inheritance`。

## Task 6：验收——§8 端到端 Go 测试 + 真实 Claude Code 人工走查

**Step 0（F1）：§8 端到端用例的 Claude Code 部分**——新文件 `internal/gateway/e2e_cc_test.go`（`//go:build e2e` 标签，`go test -tags nogui,e2e ./internal/gateway -run E2ECC` 跑）：
- 假上游（会流式回复的 OpenAI 兼容服务，复用 gateway 测试夹具）+ 假 Jev（按提示脚本化：第一轮 little work；第二轮 some work + dissatisfied；第三轮同二）+ `queqiao serve`。
- 按 CC mod 的方式模拟三轮：每轮先 `POST /v1/queqiao/turn`（带 prompt），再以返回的 `group` 发 Anthropic Messages 请求（带会话头）。
- 断言三轮档位依次为 fast、balanced（R3 升档）、balanced（R4 保持）；断言假上游收到的模型在档位组内。
- Codex 方式（带 `x-codex-turn-metadata` 的 Responses 请求）不在本任务范围，SP6 补。

前置：`make cli`；`./queqiao router init --preset <用户实际预设>` + 网关 `queqiao serve` 跑起来；`claude plugin marketplace add weiping/queqiao`（本地可先 `claude plugin install ./clients/claude-code` 直装验证）。依次验证并记进 `docs/superpowers/notes/sp3-walkthrough.md`：

1. 一轮简单提问（"这个仓库用什么 license？"）→ `queqiao router status` / usage 显示请求进 `qq-fast`；状态栏显示 `queqiao: fast · …`。
2. 追问"不对，我说的是…"之类不满意表述 → 升档 balanced（R3），状态栏更新。
3. `/model opus` → 请求不再被改写（手动钉档）；`/model` 选回默认 → 路由恢复。
4. 派生：`/fork` 后新会话第一轮 → 继承父会话档位（`parent_session` 生效）。
5. 子代理：问一个触发 Explore 的问题 → 子代理请求进 `qq-fast`。
6. plan mode（Shift+Tab）提问 → `/turn` 的 `plan_mode` 为 true，档位 performance（R2）。
7. `--safe-mode` 或停网关 → 会话照常工作（失败安全）。

[human] 标注项按 SP0 惯例：用户授权后可由助手在交互终端代跑（`script`/`expect` 驱动交互 CC，或用户亲自跑 1–2 项抽检）。

## Task 7：回写与收尾

1. 走查结果回写 spec（发现的偏差记进 §5.5/§6.7 对应行）与本计划勾选。
2. `claude plugin validate --strict` 终检 + 全量 `go test -tags nogui ./...`（确认无 Go 侧意外改动）。
3. PR `qq/sp3-claude-code` → `queqiao` 分支（**注意 base 用 queqiao，不是 main**——PR #6 的教训），等 CI，merge commit 合并。
4. 清理 worktree 与分支；memory 记录。

## 风险与对策

| 风险 | 对策 |
| --- | --- |
| `claude plugin test` rig 与真实引擎行为有差 | Task 6 真机走查兜底；spike 已在真机验证过全部事件面 |
| `$.state` 的 atom/update API 形状与 spike 模块变量写法不同 | Task 2 Step 3 先用最小 atom 验证热重载语义；不行则退回 `read`/`update` 函数式读写（validate --strict 会拦不合规用法） |
| `.claude-plugin/marketplace.json` 在根目录影响上游合并 | 根目录新增文件，上游不会有同名路径；`clients/` 同理 |
| `userConfig` 读取时机（session.start 的 options）与网关未起竞态 | 探活只做一次、失败只 `$.ui.status` 提示，不阻塞任何后续行为 |
