# SP3 真机走查记录（2026-10-05）

环境：CC 2.1.289（`claude -p`，`--safe-mode` 一例）、queqiao@`3426`（隔离 XDG，
真实 provider：zhipu/deepseek/kimi-code-cn/minimax-cn/copilot）、分类器
`deepseek/deepseek-flash`（plain 双问）、mod 经 marketplace 本地安装
（`gateway_url=http://127.0.0.1:3426`）。CC 项目级 `.claude/settings.local.json`
提供 §4.5 的 env 映射（SP0 教训：全局 settings.json 的 env 覆盖进程变量，
必须用项目级覆盖）。

## §11 验收项

| # | 项 | 结果 | 证据 |
| --- | --- | --- | --- |
| 1 | 简单提问进 `qq-fast`，状态栏档位 | ✅ | 会话 47c00566：decision `fast R6-adopt`（conf 1），主请求由 glm-5.3-flash 服务（usage `agent=claude`）。状态栏为 TUI 概念，`-p` 下由 `$.ui.status` 无害跳过 |
| 2 | 「不对」→ 升档 balanced | ◐ 决策✅/请求⚠️ | decision `balanced R3-escalate` 正确；但 plain 分类器双问 2.4s > mod 1500ms 竞速 → mod 超时不改写，请求以 `group/queqiao` 到达，网关 hook 并发读到旧状态 R6 钉在 fast；下一轮 `balanced R4-escalation-hold` 补偿。见「发现」#1 |
| 3 | `/model` 钉档 → mod 不再介入 | ✅ | `--model group/qq-perf` 的轮次：decision 仍产生（R4），请求原样放行，由 MiniMax-M2.7 服务（usage `agent=claude`） |
| 4 | `/fork` 派生会话继承父档位 | ⏳ [human] | 需交互 TUI（`-p` 下 `classic.SessionStart` 不触发，S3 已证）。机制已有单测覆盖（SessionStart fork → $.store 查父 → parent_session） |
| 5 | Explore 子代理进 `qq-fast` | ✅ | `agent.spawn` R1 → alias `haiku` → env `ANTHROPIC_DEFAULT_HAIKU_MODEL` → `group/qq-fast`：子代理 4 个请求全部 glm-5.3-flash |
| 6 | plan mode → performance（R2） | ⏳ [human] | plan mode 是 TUI shift+tab（S8 旁证），`-p` 不可达 |
| 7 | `--safe-mode` → 会话照常 | ✅ | mod 未加载，请求以 `group/queqiao` 到网关，hook 兜底分类（deepseek）→ glm 服务，回答正常 |

## 发现

1. **1500ms 竞速 vs plain 分类器双问的时序缺口**：§6.7 固定 mod 预算 1500ms；
   §5.4 plain 分类器两次串行请求（tier + dissatisfied）实测 1.7–2.8s
   （deepseek-flash，max_tokens 400 后思考开销可见）。mod 超时本身是设计内
   （网关模式兜底），但兜底决策与在途 `/turn` 写状态存在同秒竞速：请求读到的
   还是上一轮的 TurnState，升档晚一轮落地。缓解方向（记入 spec §5.7 备注）：
   用 jev（单请求）或把 `classify_timeout_ms` 压到预算内；R4 保证最终一致。
2. **思考模型与 `max_tokens:8` 不兼容**（已修）：plain 分类器原样沿用 magpie
   classify.go 的 8 token 上限，glm/deepseek/kimi/minimax 全系思考模型把预算
   吃在 reasoning 里，content 恒空 → R8-default。已把上限提到 400
   （classify.go；spec §5.4 只约定复用提示词，未锁 token 数），单测同步。
3. **用户当前 copilot 渠道全部报「model not supported」**（claude-sonnet-5.5/
   gpt-6-sol/fable 均拒）——SP0 时可用，本日复测不可用；perf 档临时换
   minimax-cn/MiniMax-M2.7。渠道侧问题，与 mod 无关。
4. cosmetic `unrecognized_model` 警告如期出现（S1 已知）：CC 客户端对
   `group/*` 模型名的本地提示，不影响请求。
5. mod 超时后网关 hook 兜底会产生**同轮双 decision**（`/turn` 的与 hook 的各
   一条，router.jsonl 同秒两条）——`queqiao router report`（SP5）聚合时需按
   session+turn 去重，避免双计。
