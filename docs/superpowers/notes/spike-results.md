# SP0 spike 结果（S1–S13）

> 计划：`docs/superpowers/plans/2026-10-03-queqiao-sp0-spike.md`；决策规则来自 spec §10「不成立时」列，逐字执行。
> 原始日志不入 git（`docs/superpowers/spikes/logs/`），此处只引用片段，提示词截取前 40 字符。

| 编号 | 版本 | 观察到的事实 | 结论 | 选用 | 证据 |
| --- | --- | --- | --- | --- | --- |
| S1 | claude-code 2.1.288 · 2026-10-03 · 档位 fast=zhipu/glm-5.3 / balanced=kimi-code-cn/k3 / perf=copilot/claude-sonnet-5.5 | 21 会话 71 请求：72/72 first_user_sha256 非空；x-claude-code-session-id 每会话恒定（21 会话各 1 个）；/v1/messages 主请求 68×group/qq-fast + 2×group/qq-perf + 1×group/qq-balanced（sonnet 别名辅助调用）；多步轮次 cache_read 非零（64/832/5376/39616/76352/85952/92864，magpie usage.jsonl）；/model opus 轮 turn.step 见 group/qq-perf 且 rewrite_sent=null；计划模式轮次同一档同一账号（单成员档内恒定） | 成立 | 主方案（mod 钩子改写） | `docs/superpowers/spikes/logs/s1.jsonl`（未入库，gitignore）；两条值得记录的旁路事实：① /v1/messages/count_tokens 预检请求带未改写的 group/queqiao（不走 turn.step 路径，magpie 200 受理，40 次）；② 分类器等辅助调用走 haiku/sonnet 别名 → group/qq-fast/balanced，并触发 cosmetic 的 unrecognized_model 警告 |
| S2 | claude-code 2.1.288 · 2026-10-03 | 全部 21 个会话：mod 探针 session 字段 == 请求的 x-claude-code-session-id（21/21 match，0 mismatch）；会话内 header 恒定 | 成立 | 主方案（mod session id 即 x-claude-code-session-id，可直接透传给 magpie） | `docs/superpowers/spikes/logs/s1.jsonl`；session 来源：UserPromptSubmit 钩子 input.session_id（uuid 形态，如 50f6400d-…） |
| S3 | claude-code 2.1.288 · 2026-10-03/04 | agent.spawn 钩子正常触发（Explore、general-purpose 各一次）：hook input fork=false、model=null、subagentType 正确 → mod 钉 haiku → 经 ANTHROPIC_DEFAULT_HAIKU_MODEL 解析 → result_model=group/qq-fast、result_agentId 返回；子代理 step 以 group/qq-fast 到达（未被二次改写），其请求 8×200 记录为 group/qq-fast；主会话改写不受影响；无 mod 错误。**fork 子代理类型在 2.1.288 不存在**：subagent_type=fork 三次复现均 Agent type 'fork' not found（含 3 轮带历史的 --resume 会话；实际可用类型 claude/general-purpose/Explore/Plan 等）→ fork 标记分支无法触发，规则所防的「fork 被钉」情形在本版本不可能发生 | 成立（fork 场景为 vacuous truth：本版本无 fork 子代理类型，mod 的 fork 分支属防御性死代码） | 主方案 | `docs/superpowers/spikes/logs/s3.jsonl`；附注①：classic.SessionStart 在 -p 模式被引擎跳过（no session is bound in this process，0 次触发）——S11 判定须用交互模式；附注②：count_tokens 旁路同 S1 |
| S4 | （待填） | （待填） | （待填） | （待填） | （待填） |
| S5 | 2026-10-03 · jev-latest · n=100 串行 | p50=1260ms，p95=13783ms，3/100 失败（本机 → api.typesafe.ai/v1/systemone，§5.3 请求体） | 不成立（p95 ≫ 1000ms） | 备选：`classify_timeout_ms` 提高到 1500，默认分类器改为延迟更低的本地小模型 | `docs/superpowers/spikes/jev` 输出行：`1260 13783 3`；命令 `TYPESAFE_API_KEY=… python3 latency.py -n 100` |
| S6 | （待填） | （待填） | （待填） | （待填） | （待填） |
| S7 | （待填） | （待填） | （待填） | （待填） | （待填） |
| S8 | （待填） | （待填） | （待填） | （待填） | （待填） |
| S9 | （待填） | （待填） | （待填） | （待填） | （待填） |
| S10 | claude-code 2.1.288（会话中自动升级至 2.1.289）· 2026-10-03/04 | 四种实际运行的模式 mod 均加载（session.start 探针在场，race_winner 均为 timer，localhost 可达）：① 交互 REPL（session cbaf0fa6，interactive 模式）② REPL 中 /fork 的派生会话（c9f95c88，session.start 在场）③ claude -p（194d0e3f）④ Agent SDK：**spawn 失败**——@anthropic-ai/claude-agent-sdk 0.3.289 的 query() 在发起任何请求前抛 `spawn Unknown system error -88`（macOS EFTYPE），有/无 plugins、设/不设 CLAUDE_CODE_ENTRYPOINT 均复现；直接 child_process.spawn(claude 二进制) 正常，问题出在 SDK 的 spawnLocalProcess（经 bun/node 拉起 CLI）；SDK 模式下 CC 根本没启动 → mod 自然不加载 | 成立（主方案） | 主方案；README 需把 SDK 模式列为「无法本地 spawn（errno -88）待查」，比「回退网关模式」更强 | `docs/superpowers/spikes/logs/s10.jsonl`；SDK 版本 0.3.289，claude 二进制 2.1.289；附注：fork 派生会话的 22 个请求仍带父会话 x-claude-code-session-id（见 S11） |
| S11 | claude-code 2.1.289 · 2026-10-04 | 四个 classic.SessionStart 事件全部触发：原会话 startup（15590e6e）→ /fork 派生会话 source="fork"（8558f482）→ /branch 再次触发 source="fork"（仍报 15590e6e，**无独立 branch 字面量**）→ 一个 source="resume"（63aa1659）。所有派生会话 first_fnv1a 均为 f2f4946（与原会话首消息 hash 相等）；$.store 查找均命中原会话 15590e6e。附注：派生会话的请求仍带父会话的 x-claude-code-session-id（fork 的请求全部挂在 15590e6e 下） | 成立 | 主方案 | `docs/superpowers/spikes/logs/s11.jsonl`；两条实现事实：① /branch 无独立 source，报 "fork"（可另见 "resume"）→ mod 无法区分 fork/branch，但两者都正确映射到父会话；② fork/branch 请求复用父会话头 → 网关侧会话级缓存/亲和会把整个 fork 树视作同一会话 |
| S12 | （待填） | （待填） | （待填） | （待填） | （待填） |
| S13 | （待填） | （待填） | （待填） | （待填） | （待填） |
