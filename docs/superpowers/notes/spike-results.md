# SP0 spike 结果（S1–S13）

> 计划：`docs/superpowers/plans/2026-10-03-queqiao-sp0-spike.md`；决策规则来自 spec §10「不成立时」列，逐字执行。
> 原始日志不入 git（`docs/superpowers/spikes/logs/`），此处只引用片段，提示词截取前 40 字符。

| 编号 | 版本 | 观察到的事实 | 结论 | 选用 | 证据 |
| --- | --- | --- | --- | --- | --- |
| S1 | claude-code 2.1.288 · 2026-10-03 · 档位 fast=zhipu/glm-5.3 / balanced=kimi-code-cn/k3 / perf=copilot/claude-sonnet-5.5 | 21 会话 71 请求：72/72 first_user_sha256 非空；x-claude-code-session-id 每会话恒定（21 会话各 1 个）；/v1/messages 主请求 68×group/qq-fast + 2×group/qq-perf + 1×group/qq-balanced（sonnet 别名辅助调用）；多步轮次 cache_read 非零（64/832/5376/39616/76352/85952/92864，magpie usage.jsonl）；/model opus 轮 turn.step 见 group/qq-perf 且 rewrite_sent=null；计划模式轮次同一档同一账号（单成员档内恒定） | 成立 | 主方案（mod 钩子改写） | `docs/superpowers/spikes/logs/s1.jsonl`（未入库，gitignore）；两条值得记录的旁路事实：① /v1/messages/count_tokens 预检请求带未改写的 group/queqiao（不走 turn.step 路径，magpie 200 受理，40 次）；② 分类器等辅助调用走 haiku/sonnet 别名 → group/qq-fast/balanced，并触发 cosmetic 的 unrecognized_model 警告 |
| S2 | claude-code 2.1.288 · 2026-10-03 | 全部 21 个会话：mod 探针 session 字段 == 请求的 x-claude-code-session-id（21/21 match，0 mismatch）；会话内 header 恒定 | 成立 | 主方案（mod session id 即 x-claude-code-session-id，可直接透传给 magpie） | `docs/superpowers/spikes/logs/s1.jsonl`；session 来源：UserPromptSubmit 钩子 input.session_id（uuid 形态，如 50f6400d-…） |
| S3 | （待填） | （待填） | （待填） | （待填） | （待填） |
| S4 | （待填） | （待填） | （待填） | （待填） | （待填） |
| S5 | 2026-10-03 · jev-latest · n=100 串行 | p50=1260ms，p95=13783ms，3/100 失败（本机 → api.typesafe.ai/v1/systemone，§5.3 请求体） | 不成立（p95 ≫ 1000ms） | 备选：`classify_timeout_ms` 提高到 1500，默认分类器改为延迟更低的本地小模型 | `docs/superpowers/spikes/jev` 输出行：`1260 13783 3`；命令 `TYPESAFE_API_KEY=… python3 latency.py -n 100` |
| S6 | （待填） | （待填） | （待填） | （待填） | （待填） |
| S7 | （待填） | （待填） | （待填） | （待填） | （待填） |
| S8 | （待填） | （待填） | （待填） | （待填） | （待填） |
| S9 | （待填） | （待填） | （待填） | （待填） | （待填） |
| S10 | （待填） | （待填） | （待填） | （待填） | （待填） |
| S11 | （待填） | （待填） | （待填） | （待填） | （待填） |
| S12 | （待填） | （待填） | （待填） | （待填） | （待填） |
| S13 | （待填） | （待填） | （待填） | （待填） | （待填） |
