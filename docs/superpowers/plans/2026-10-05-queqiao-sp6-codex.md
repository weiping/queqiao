# SP6：Codex 插件 `queqiao-router-codex` + `internal/harness/`

> Spec：`docs/superpowers/specs/2026-10-02-queqiao-design.md` §6.9（主）、§5.6、§5.8（Codex 行）、§6.6 的 hook 命令行、§8 的 harness 行与 e2e 行、§11（SP6-codex 验收）。
>
> 前置：SP2 已合并（`/turn` 端点、`turn_id` 元数据读取在）；SP3/SP4 已合并。
>
> 产物：`internal/harness/`（Go 公共部分 + Codex 解析）、`clients/codex/`（插件）、仓库根 `.agents/plugins/marketplace.json`。`main.go` 加 `hook` 子命令分发（允许改动的接线点）。
>
> **复审记录（2026-10-05）**：F1——§8「plugin.json 通过 Agent Plugins Schema 校验」与 S7 legacy 格式矛盾（legacy 无 $schema），Task 3 改为 marketplace.json 走 Schema/CLI 校验、legacy plugin.json 以 Codex CLI 加载成功为准，Task 6 修订 spec 该行；F2——Task 4 抽共享 e2e helper（与 e2e_cc_test.go 同包）；F3——Task 5 用隔离 HOME 跑 `router init` 的 Codex 步骤写 config.toml（该路径首获真机验证）；F4——user-prompt 对空 session/prompt 的防御跳过入测试清单。

## 从 SP0 带入的实测事实

| 事实 | 来源 | 影响 |
| --- | --- | --- |
| hooks 只能 legacy `.codex-plugin/plugin.json` 声明 `"hooks": "./hooks.json"`（AGENT 格式带 $schema 不支持，实测不触发） | S7 | 插件格式照 spike 的 `codex-market` 样板 |
| `timeout` 单位是**秒**（3s sleep 在 timeout=2 被截断） | S7 | hooks.json 全写 2 |
| Codex 只读 `.agents/plugins/marketplace.json`，忽略 `.claude-plugin/` | S7 | 根目录放 `.agents` 版（仅列 Codex 插件）；SP3 的 `.claude-plugin/marketplace.json` 不动 |
| hook 需信任（TUI 弹窗）或 `--dangerously-bypass-hook-trust`（hook 子进程不继承 `QUEQIAO_SPIKE_*` 环境变量） | S7 | 非交互走查用 bypass；文档写清信任步骤；处理程序不得依赖进程环境变量传配置（网关地址走 `QUEQIAO_URL` 环境或默认 3425） |
| PreToolUse 给 `spawn_agent` 加 `model=group/qq-fast` 后子代理请求确实以该模型发出（`updatedInput` + `permissionDecision:"allow"` 一起输出才生效） | S9 | pre-agent 输出格式照 spike 的 hook.py |
| `spawn_agent` 无 `fork_context`（实际 `subagent_kind`/`forked_from_thread_id`） | S13 | §5.6 第 2 步已删；fork 子代理走网关模式 |
| 网关从 Codex Responses 统计不出工具失败；非交互 `permission_mode` 恒 `bypassPermissions` | S8 | `user-prompt` 的 `plan_mode` 恒 false；R3 只靠 dissatisfied（spec 已记） |

## 工作区

- worktree：`.worktrees/qq-sp6-codex`，分支 `qq/sp6-codex`，基于 `queqiao`。
- 结构：

```
internal/harness/
  harness.go        // 公共：读 stdin（上限 1MiB）、网关 POST（1500ms）、一律退出码 0
  harness_test.go
  codex/codex.go    // Codex 输入解析（UserPromptSubmit/PreToolUse/PostToolUse）与输出格式
  codex/codex_test.go
hook_cli.go         // root 包：quexiao hook <user-prompt|pre-agent|post-bash> --harness <name>
clients/codex/
  .codex-plugin/plugin.json   // legacy 格式，"hooks": "./hooks.json"
  hooks/hooks.json            // 三个命令型 hook，timeout 2（秒）
  README.md                   // 安装、/hooks 信任、重启 三步
.agents/plugins/marketplace.json   // 仓库根，仅列 queqiao-router-codex
```

## Task 1：`internal/harness/` 公共部分

**失败测试**：stdin 读取（超 1MiB 截断）；`Call` POST JSON 到网关、1500ms 超时返回失败、非 2xx 视为失败；`Run` 包装处理程序——任何 panic/错误都收敛为退出码 0 + 空 stdout。
**实现**：`harness.go`（`func Run(h Handler) int` 形态，main 直接 `os.Exit`）。提交 `feat(harness): common hook runtime (stdin, gateway call, exit 0)`。

## Task 2：`internal/harness/codex` 解析 + 三个处理程序

**失败测试**（输入用 spike 记录过的 Codex hook JSON 形状）：
- `user-prompt`：`session_id` 或 `prompt` 为空 → 直接退出 0（防御）；`model` 为 `group/queqiao` → `/turn` 收到 `{harness:"codex", session: session_id, turn_id, prompt, agent:"main", plan_mode:false, cwd, store_hint:true}`（**注意 store_hint:true**——与 mod 的 false 相反，Codex 无本地状态，靠网关提示匹配）；stdin 里出现 `forked_from_thread_id`（任何层级，防御式搜索）→ 带 `parent_session`；`model` 不是路由组 → 不发 `/turn`，发一次 `manual_model_switch`（每会话一次——处理程序无状态，怎么记「一次」？**设计：用 hint 存状态不行（无写权限路径）；方案：manual_model_switch 去重交给网关 feedback 端点天然幂等不可行……改设计：每次非路由组轮次都发 feedback，value 带 model，报表侧去重**——或处理程序用 `/tmp` 状态文件？不干净。最终方案：**每次都发**（§6.4 feedback 是事件日志，重复 manual_model_switch 对报表影响可忽略，SP5 去重）。本条写进 spec §6.9 备注）；stdout 恒空、退出码 0。
- `pre-agent`：`tool_input.model` 已有 → 无输出；`agent_type` 命中 R1 表（与 Go 侧 `fixed_agents` 一致）→ 输出 `hookSpecificOutput{hookEventName:"PreToolUse", permissionDecision:"allow", updatedInput:{…原参数, model:"group/qq-<档位>"}}`；其余 → `/turn`（`agent:<agent_type|default>`, `message→prompt` 取 `tool_input.message`）成功才输出，失败无输出；输出 JSON 可解析且保留原参数。
- `post-bash`：整个 stdin 原文匹配 PR 链接 → `pr_created`；不匹配/无 → 无网络调用；无输出。
**实现**：`codex/codex.go` + `hook_cli.go` + `main.go` 分发（`case "hook"`）。提交 `feat(harness): codex hook handlers and quexiao hook command`。

## Task 3：`clients/codex/` 插件 + 市场文件

- `.codex-plugin/plugin.json`（legacy：`name: queqiao-router-codex`, `"hooks": "./hooks.json"`）
- `hooks/hooks.json`：按 §6.9 原文（UserPromptSubmit / PreToolUse matcher `^(spawn_agent|Agent)$` / PostToolUse matcher `^Bash$`，command 为 `quexiao hook … --harness codex`，timeout 2，user-prompt 带 statusMessage）
- 根 `.agents/plugins/marketplace.json`：`name: "queqiao"`、plugins 仅 `queqiao-router-codex`、`source: {"source":"local","path":"./clients/codex"}`、`policy.installation: "AVAILABLE"`（照 spike 样板）
- README：marketplace add → 安装 → TUI `/hooks` 信任 → 重启；`quexiao` 需在 PATH；排障（网关未起的表现=全部不干预）。
- **验证**：`codex plugin marketplace add <worktree 绝对路径>` + 安装成功 +（可选）`codex plugin list`；`plugin.json`/`hooks.json`/marketplace 三者 JSON 合法性。提交 `feat(codex): queqiao-router-codex plugin and marketplace`。

## Task 4：§8 e2e 的 Codex 半边

`internal/gateway/e2e_codex_test.go`（`e2e` 标签，同 `e2e_cc_test.go` 的环境）：假上游 + 脚本化分类器；按 Codex 方式模拟三轮——每轮先 `POST /v1/quexiao/turn`（带 `turn_id`，经 `internal/harness/codex` 的真实处理程序函数驱动，不经子进程），再以 `group/queqiao` 发 OpenAI Responses 请求、header `x-codex-turn-metadata` 带 `turn_id`；断言 fast → balanced(R3) → balanced(R4)（同 §8 脚本）。提交 `test(gateway): e2e codex pattern over fakes`。

## Task 5：真实 Codex 走查

隔离网关（同 SP3/SP4 套路，3426）+ 隔离 HOME（`router init` 的 Codex 步骤会写 `$HOME/.codex/config.toml` 与 `queqiao-models.json`——该代码路径首获真机验证）；`codex plugin marketplace add` 本地路径 + 安装 + `codex exec --dangerously-bypass-hook-trust`（S7 路径）跑一轮简单提问：决策日志 `codex fast`；再一轮「不对」看 R3（分类器双问 2.4s > hook 无竞速问题——hook 的 /turn 是同步等待 1500ms，超时则本轮无提示，下一轮网关模式补；与 §5.7 一致）。记录到 `docs/superpowers/notes/sp6-walkthrough.md`。[human] TUI 信任弹窗项留给用户。

## Task 6：回写与收尾

- **spec 修订**：§8 过时行「`pre-agent` 遇到 `fork_context: true` 输出父会话的档位组」按 S13 改写（fork_context 已不存在）；§8 插件校验行按 S7 改写（marketplace.json 走 Agent Plugins Schema + CLI 校验，legacy `plugin.json` 以 Codex CLI 加载成功为准）；§6.9 补「manual_model_switch 每轮都发、报表去重」备注。
- 全量 `go vet`/`go test -tags nogui ./...` + e2e 标签跑两个 e2e。
- PR `qq/sp6-codex` → **base queqiao**；清理 worktree/分支；memory。

## 风险与对策

| 风险 | 对策 |
| --- | --- |
| `forked_from_thread_id` 在 user-prompt 输入里的实际形状未验证（S13 只见于 spawn_agent 元数据） | 防御式全文搜索字段名；找不到就不发 parent_session（网关按 §5.8 顺序兜底） |
| Codex hook 输入字段名随版本漂移 | post-bash 用全文正则（§6.9 已如此设计）；user-prompt/pre-agent 的字段解析集中 `codex.go` 一处，漂移改一处 |
| hook 子进程环境变量不继承（S7） | 处理程序只依赖 `QUEQIAO_URL`（有默认）与 stdin，不依赖 spike 式环境 |
| `quexiao hook` 不在 PATH 时插件静默失效 | README 排障节 + 走查验证 |
