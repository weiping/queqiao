# SP5：验收实验与报表

> Spec：`docs/superpowers/specs/2026-10-02-queqiao-design.md` §9（主）、§6.4 的 feedback 消费、§6.6 的 `queqiao router report --since 14d`、§11（SP5-eval 验收：**用合成的 `usage.jsonl` 和 `router.jsonl` 测试报表的统计结果；实验能开能关**）。
>
> 前置：SP3/SP4/SP6 全部完成 ✓（feedback 事件三端都在发；SP2 已实现 `/turn` 的实验分组、`hint_consumed`/`shadow` 事件、usage 的 `router_tier`/`router_arm` 字段）。
>
> 产物：`internal/router/report.go`（纯聚合函数）+ `queqiao router report` + 测试。不改上游文件。
>
> **复审记录（2026-10-05）**：F1——SP2 实现里 control 组的事件 Kind 是 `shadow`（Tier=control_tier、ShadowTier=路由器本选），分组必须取「decide+shadow 中最后带 arm 的事件」，否则整个 control 组消失；control 组的档位分布用 ShadowTier 并排显示。F2——提示命中率分母只含**经路由组的请求**（CC mod 直改档位组的请求天然不在内），口径写明。F3——无价目请求排除出成本、计数披露（防中位数被 0 拉低）。F4——p90 用最近邻秩定义（合成测试可精确断言）。F5——PR 终态回退链：gh > feedback 的 pr_merged/pr_closed > 「未知」。F6——报表窗口跨实验开关边界时按事件 arm 如实分组并脚注。F7——usage 读取优先复用 internal/usage 的 Period 块读（已导出则用），ReportInput 仍是纯数据。

## 数据源（全部已存在，SP5 只读）

| 数据 | 位置 | 关键字段 |
| --- | --- | --- |
| 每请求用量 | `~/.config/queqiao/usage.jsonl`（usage.Record） | `session`、`req`（请求的组名）、`model`+`provider`（实际服务）、`in/out/cache_read/cache_write`、`t`、`router_tier`、`router_arm` |
| 路由事件 | `~/.config/queqiao/router.jsonl`（router.Event） | `decide`（session/harness/tier/reason/arm/shadow_tier）、`hint_consumed`、`feedback`（pr_created、manual_model_switch…） |
| 单价 | catalog（`usage.Totals.add` 同源：`price.Cost(in, out, cacheRead, cacheWrite)`） | 缓存写入费用按费率单算 |
| PR 终态 | `gh pr view <url> --json state`（report 时补查；无 `gh` → 「未知」） | §9 原文要求 |

## 口径决定（写入代码注释与 spec §9 备注）

1. **会话成本** = 该 session 全部 200 请求的 price.Cost 之和；无 session 的行（probe 等）不计。
2. **分组（arm）**：以 `decide` **和 `shadow`** 事件里该会话最后一次带 `arm` 的记录为准（router/control；control 组的会话只以 shadow 出现，Tier=control_tier、ShadowTier=路由器本选）；usage 的 `router_arm` 作交叉校验，不一致时以事件为准并在报表下注明行数。**档位分布按轮**（该组全部 decide+shadow 事件的 tier 占比）；control 组并列显示 ShadowTier 分布（路由器本会选的档）。
3. **手动换模型率**：会话内 ≥1 条 `manual_model_switch` 即计（SP6 的「每轮都发」在此按会话去重——正是当初的设计）。
4. **提示命中率** = `hint_consumed / (hint_consumed + harness="gateway" 的 decide)`——**经路由组请求**中靠提示完成的比例；CC mod 直改档位组的请求不经路由组，天然不计；分母为 0 时显示「—」。
5. **缓存写入费用占比** = Σ cache_write 费率费用 / Σ 总费用（按每请求实际价目）。**无价目请求**（catalog 无价）：排除出各项成本、计数披露。
6. **bootstrap**：中位数 95% CI，1000 次重采样，**固定随机种子**（测试可复现）；**p90 用最近邻秩**（小样本可精确断言）。
7. **双比例 z 检验**：合并率（merged/PR 会话）与「开出 PR 会话占比」的组间比较，正态近似；样本 <100/组时按 §9 标注「样本不足」，成本与手动换模型率仍作结论依据。
8. `--since`（默认 14d）按两个文件的 `t` 过滤；PR 终态查询单独记缓存（report 运行期，`gh` 一次一查）。

## 工作区

worktree `.worktrees/qq-sp5-report`，分支 `qq/sp5-report`，基于 `queqiao`。

## Task 1：纯聚合 + 统计函数（TDD，合成数据）

`internal/router/report.go`：
- 输入类型 `ReportInput{ Records []usage.Record; Events []Event; Now time.Time; Since time.Duration }`——**不读文件**，CLI 层负责装载（合成测试零 IO）。
- `stats.go` 同包：`median/p90/mean`、`bootstrapMedianCI`（定种子）、`twoProportionZ`。
- 聚合产出 `ArmReport`（§9 的每项指标）×2 + 对照行。
- **失败测试先行**：手工构造 3+3 会话的合成 input——成本精确可算（固定单价目录）、一组的 PR 会话 2/3 且 1 merged、manual_switch 会话 1 个、hint 事件 4/6、cache_write 已知占比——断言每个指标到具体数值（含 p90、CI 边界范围、z 值符号）。
提交 `feat(router): report aggregation and stats over synthetic data`。

## Task 2：PR 终态补查 + 文本渲染

- `prstates.go`：终态回退链 `gh pr view <url> --json state`（`exec`，10s 超时）> feedback 事件里的 `pr_merged`/`pr_closed` > 「未知」；同 URL 去重缓存。测试：PATH 里放假 `gh` 脚本（输出 OPEN/MERGED）、无 `gh` 但有 feedback 事件、两者皆无，三种。
- `render.go`：§9 的每指标一行 × 两组并排 + 样本不足标注 + 口径脚注；`--json` 输出同结构。
提交 `feat(router): pr-state lookup and report rendering`。

## Task 3：CLI 接线

`router_cli.go` 加 `report` 子命令（`--since 14d`、`--json`）：读 `appdir.Config()` 下 usage.jsonl / router.jsonl（usage 只需要 router 相关行——按 `session != ""` 或 `router_*` 字段过滤后传入），调 Task 1/2。端到端测试：临时 XDG 放合成两文件 + 假 gh，跑命令断言输出含关键行。
提交 `feat(router): queqiao router report`。

## Task 4：实验开关验证

- 已有：SP2 的 experiment 分组（`sha256(salt+session)%100 < router_percent`）与 `control_tier`、shadow 事件有单测。
- 补一条 **API 级**验证：`router.json` 翻 `experiment.enabled` → `/turn` 对同一 session 的响应 `arm` 从 `router` 变 `control`（tier=control_tier、shadow_tier 进事件）——走 `testServer`（api_test 的假网关），断言两种状态。§11 的「实验能开能关」即此。
提交 `test(router): experiment toggle changes the turn arm`。

## Task 5：文档 + 收尾

- spec §9 补口径备注（上面的 1–8 条）；`queqiao router report --help` 与 README（两阶段设计的操作建议：第一阶段 control=performance ≥2 周或每组 150 个 PR 会话）。
- 全量 `go vet`/`go test -tags nogui ./...` + e2e 标签回归；PR `qq/sp5-report` → **base queqiao**；清理；memory。

## 风险与对策

| 风险 | 对策 |
| --- | --- |
| usage.Record 字段在旧行缺失（session/req 等是后来加的） | 聚合对零值字段自然跳过；报表注明「无 session 的行未计入」 |
| 两个文件的会话 ID 口径不一（usage 的 X-Magpie-Session vs 事件的 session） | 三端发送时已统一（CC header / Pi X-Magpie-Session / Codex hint 的 session）；不一致行数在报表脚注披露 |
| gh 慢/限流 | 10s 超时 + 失败即「未知」，绝不阻塞报表 |
| bootstrap 的随机性破坏测试断言 | 固定种子；测试断言 CI 含真中位数且宽度合理 |
