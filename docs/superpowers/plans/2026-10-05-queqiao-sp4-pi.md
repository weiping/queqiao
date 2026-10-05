# SP4：Pi 包 `pi-queqiao`

## 执行结果（2026-10-05 回填）

- **状态**：✅ 完成并合并 —— PR #8（merge `10cec76`）；事后复审修复 PR #9（merge `76828b9`）；另有一处直接提交 `134e430`。
- **交付**：`clients/pi/`（`@weiping/pi-queqiao`：session_start / before_provider_headers / before_agent_start / model_select / tool_result / tool_call + `src/client.ts`；26 vitest）。
- **复审修复（合并后）**：`tool_call` 改用 `/turn` 返回的 `group`（原拼 `qq-${tier}`，performance 会拼成不存在的 `qq-performance`）；`QueqiaoClient` 剥 base URL 尾斜杠（`//v1` 会被网关路径清洗弄成重定向）；`/turn` 补 `cwd`（§4.1 项目级 criteria）；spec §8/§6.8 的 S12 过时措辞修订。
- **真机走查（2026-10-05，pi 1.0.0）**：检查 1（`/model` 手切 → `manual_model_switch` feedback + 钉档停自动）✅；检查 2 升档在换 `typesafe/jev-latest` 分类器后✅（deepseek 双问 3.7s 超 1500ms 预算，spec §5.7 已记）；`/fork` 继承**抳到真 bug**：pi 1.0.2 会话文件名带时间戳前缀，父 ID 解析错→继承静默失效（`134e430` 修复后用真实 UUID 实测 `balanced R4-escalation-hold` 继承生效）。
- **更正**：pi 并不回写 `models.json`（此前判断为人工恢复命令造成的假象）。
- **[human] 遗留**：`/model` 与 `/fork` 的完整抽检——见 `~/workspace/notes/areas/queqiao-验收清单.md` 第 2 节。

> Spec：`docs/superpowers/specs/2026-10-02-queqiao-design.md` §6.8（主）、§4.5（Pi 行）、§5.8（Pi 行）、§8（vitest 行）、§11（SP4-pi 验收）。
>
> 前置：SP2 已合并（网关端点在）；SP3 已合并（PR #7）。
>
> 产物：`clients/pi/`（npm 包 `@weiping/pi-queqiao`），TypeScript + vitest，零运行时依赖（Node 内置 fetch/crypto），不改任何 Go 代码。

## 从 SP0 带入的实测事实

| 事实 | 来源 | 对实现的影响 |
| --- | --- | --- |
| `pi.setModel()` 在 `before_agent_start` 内对本轮生效（`--model group/queqiao` 下 body_model 变 `group/qq-fast`） | S4 | 主路径照做 |
| 模型对象：`ctx.modelRegistry.find("magpie", "group/qq-<tier>")` | S4 | setModel 前先 find；找不到就不切 |
| 稳定会话 ID：`ctx.sessionManager.getSessionId()` 返回稳定 UUID | S4 | 不需要 randomUUID 兜底，但保留（拿不到时生成） |
| `before_provider_headers` 注入 `X-Magpie-Session` 成功 | S4 | 照做 |
| **`session_start.reason` 恒 `startup`（非 fork）**；父会话在 header 的 `parentSession` 字段（会话文件路径 `…/sessions/<id>.jsonl`） | S12 | 派生检测用 header 而非 reason；父 ID = 路径 basename 去 `.jsonl`；读不到时调 `/lineage` 标记 |
| pi-subagents 工具名 `subagent`/`dispatch_agent`（非 `Agent`），参数 `context` 枚举 fresh/fork/profile，**无 `inherit_context`** | S12 | tool_call 只处理这两个工具名；`context:"fork"` 的子代理按网关模式路由（不改模型），fresh 的才选档 |

## API 权威事实（pi 1.0.0 dist/types.d.ts，2026-10-05 核对）

- `model_select` 事件存在：`source: "set" | "cycle" | "restore"`；`set`/`cycle` 视为手动（发 feedback + 本会话停自动切换），`restore` 忽略。
- **隐患**：`pi.setModel()` 自身可能以 `source:"set"` 触发 `model_select` → 扩展要区分自切/手切：记录 `lastAutoModel`，事件里 `model.id === lastAutoModel` 的跳过（Task 3 测试覆盖；真机走查复核）。
- `SessionStartEvent { reason: "startup"|"reload"|"new"|"resume"|"fork", previousSessionFile? }`。
- `BeforeAgentStartEvent.prompt` 是本轮用户原话。
- `BeforeProviderHeadersEvent.headers` 可变（直接赋值）。
- `ToolCallEvent.input` 原地修改即生效（返回值只用于 block）。
- `ToolResultEvent { input, content, isError, ... }`。
- 网关地址：env `QUEQIAO_URL`，默认 `http://127.0.0.1:3425`。

## 工作区

- worktree：`.worktrees/qq-sp4-pi`，分支 `qq/sp4-pi`，基于 `queqiao`。
- 结构（§6.8）：

```
clients/pi/
  package.json        // name @weiping/pi-queqiao, keywords ["pi-package"],
                      // pi: {"extensions": ["./extensions"]},
                      // peerDependencies: {"@earendil-works/pi-coding-agent": "*"},
                      // devDependencies: typescript + vitest
  tsconfig.json
  extensions/queqiao.ts   // export default function (pi: ExtensionAPI)
  src/client.ts           // turn()/feedback()/lineage()，1500ms AbortSignal 超时
  src/session.ts          // 会话 ID 与父会话提取
  test/client.test.ts
  test/extension.test.ts  // 模拟 pi 对象（S4/S12 事件形状）
```

## Task 1：脚手架 + vitest 跑通

`package.json`/`tsconfig`/空扩展（各事件直接放行）；`vitest run` 空测试通过；`tsc --noEmit` 干净。提交 `feat(pi): pi-queqiao package scaffold`。

## Task 2：`src/client.ts`（HTTP 客户端）

**失败测试**：`turn()` POST `/v1/queqiao/turn`（字段 `harness:"pi"`、session、prompt、agent、`store_hint:false`；有 parent 时带 `parent_session`）返回 `{tier, group}`；1500ms 超时返回 null（vitest fake timers + AbortController）；非 200/解析失败 → null；`feedback()` fire-and-forget 不抛；`lineage()` 同理。
**实现**：fetch + `AbortSignal.timeout(1500)`；泛型错误吞掉。
提交 `feat(pi): gateway http client`。

## Task 3：核心扩展（session/headers/before_agent_start）

**失败测试**（模拟 pi 对象 + fetch mock）：
- `session_start`：`ctx.sessionManager.getSessionId()` 有值 → 用之；无值 → randomUUID 兜底；header 带 `parentSession` 路径 → 解析父 ID 存内存、不调 `/lineage`；无 `parentSession` 且 reason 为 `fork`（防御，未来版本）→ 调 `/lineage`；重置手动钉档标记。
- `before_provider_headers`：headers 添加 `X-Magpie-Session: <会话 ID>`。
- `before_agent_start`：prompt 空串 → 不调 `/turn`；非空 → `/turn`（带派生首查的 `parent_session`）→ 档位与上次不同 → `ctx.modelRegistry.find("magpie", group)` 成功后 `pi.setModel(model)`；同档 → 不调 setModel；`/turn` 失败/超时 → 不动模型；find 失败 → 不切。`lastAutoModel` 在 setModel 前记录。
- 自切不触发手动逻辑：`model_select(source:"set", model=lastAutoModel)` → 不发 feedback、不停自动切换；`source:"set"` 且 model ≠ lastAutoModel → 发 `manual_model_switch`（`from→to`）+ 本会话停自动；`source:"restore"` → 忽略。
**实现**：extensions/queqiao.ts + src/session.ts。提交 `feat(pi): per-turn tier routing extension`。

## Task 4：`tool_result` PR 链接 + `tool_call` 子代理选档

**失败测试**：
- `tool_result`：content 文本含 `https://github.com/<o>/<r>/pull/<n>` → `feedback({kind:"pr_created"})`；无链接/`isError` → 不发。
- `tool_call`：`toolName` 为 `subagent`/`dispatch_agent` 且未预设 `input.model` 且 `context !== "fork"` → `/turn`（`agent:"subagent"`, prompt 取任务描述字段 `task`/`prompt`/`description`）→ `input.model = "magpie/group/qq-<档位>"`；`context:"fork"` → 不改（网关模式）；已有 `model` → 不改；`/turn` 失败 → 不改；其他工具名 → 不动。
**实现**。提交 `feat(pi): pr feedback and subagent tier pick`。

## Task 5：真机走查（§11 SP4 验收）

前置：`make cli`；pi 的 magpie Provider（用户已有）默认模型设为 `magpie/group/qq-balanced`（§4.5，`~/.pi/agent/models.json`——**改用户真实配置前先征得同意**，或用 `PI_*` 环境变量/worktree 级配置覆盖）；`QUEQIAO_URL=http://127.0.0.1:3426`（隔离网关，同 SP3 走查环境）。装扩展：`npm link` 或 pi settings 的 extensions 指向 `clients/pi/extensions`。依次验证：
1. 一轮简单提问 → `pi` 显示模型切到 `magpie/group/qq-fast`（或 `/model` 查询确认），决策日志 `fast`。
2. 追问「不对…」→ R3 升档 balanced，模型随之切换。
3. `/model` 手动切到别的模型 → `manual_model_switch` 事件 + 本会话不再自动切。
4. `/fork` 后新会话第一轮 → `parent_session` 生效（决策日志或 /v1/queqiao/session）。
[human] 交互项可代跑（`pi -p` 非交互先行 + 用户抽检交互项）。

## Task 6：回写与收尾

走查结果回写 spec（§6.8 偏差记录）；全量 `go test -tags nogui ./...`（确认零 Go 改动）+ `claude plugin` 侧不回归；PR `qq/sp4-pi` → **base queqiao**；清理 worktree/分支；memory。

## 风险与对策

| 风险 | 对策 |
| --- | --- |
| `setModel` 触发 `model_select` 导致自切被当手切 | `lastAutoModel` 屏蔽（Task 3 测试 + 走查复核）；仍误判时降级为「从不清 manual 标记」并记 spec |
| pi-subagents 未装时 `tool_call` 的 subagent 工具不出现 | 天然无操作，无风险 |
| `parentSession` 路径格式变化（非 .jsonl 结尾） | basename 提取失败 → 调 `/lineage` 兜底 |
| 包发布（npm publish）不在本阶段 | 计划只到 PR 合并；发布时机由用户定 |
