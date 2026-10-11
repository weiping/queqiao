# SP11：判档时看到上一轮回答

> 状态：作者 2026-10-11 确认（「按方案1 做」）。

## 1. 问题

判档时，Jev 只看用户这一轮输入的文字。用户说「按这个思路修改」「就这么办」「照方案 2 做」这类话时，真正的工作量写在上一轮助手的回答里，Jev 看不到。

作者 2026-10-11 在 Claude Code 里的实例：

- 上一轮回答提出了一个涉及多个工作流文件的 CI 调整方案（后来的 PR #31）。
- 下一轮用户输入「按这个思路修改」。
- Jev 判为 fast，置信度 0.73，没有切到更高的档。

## 2. 设计

**给 Jev 的输入**

- 判档请求的 `state` 多一项 `previous_answer`：上一轮助手的最终回答。
- 回答较长时，留开头 400 字和末尾 800 字，中间用「…」代替。方案的要点和结尾的提问通常在末尾。
- 档位问题的说明多一句：`previous_answer` 是上一轮的回答；当 `message` 只是同意它或指向它（按这个改、就这么办、照方案 2 做）时，按回答里提出的工作量判档。
- 没有上一轮回答时（会话的第一轮），不加这一项，请求和以前完全一样。
- 不用 Jev、改用普通模型做分类器时，提示词里也加上同样的内容。

**mbridge**

- `POST /v1/bridge/turn` 接受一个新字段 `previous_answer`（字符串，可选）。
- 网关模式和 Codex 代理这两条路由路径，在 mbridge 内部判档，由 mbridge 自己从请求中取上一轮回答：请求本来就带着整段对话，取最后一条用户消息之前的那条助手消息的文字。

**三个客户端**，每轮结束时把最终回答记下来，下一轮发 `/turn` 时带上：

- **Claude Code 插件**：在 `turn.complete` 里记下主会话的 `e.answer`，存到 `$.state` 的 `lastAnswer`。不论这一轮是什么档都记，子 Agent 的回答不记。
- **Pi 扩展**：在 `agent_end` 里用 `lastAssistantText` 取回答并记下。手动固定模型、或者 performance 档的轮次也照记，只是这些轮次照旧不发审查。
- **Codex hook**：Stop hook 拿到 `last_assistant_message`，写进一个以会话区分的临时文件；下一次 UserPromptSubmit 读出来一起发。做法和现有的 prompt 状态文件一样。
- 客户端最多发送 4000 个字符；超出时保留开头 1000 字和末尾 3000 字，最终由 mbridge 截取。

## 3. 代价

- 每次判档请求多大约 300–400 个 token，只在有上一轮回答时才有。
- 判档耗时略有增加（输入变长），仍在 SP10 的等待时间之内。

## 4. 测试

- **router**
  - 有上一轮回答时，Jev 请求的 `state` 带上截取后的 `previous_answer`，说明里多出那一句；没有时请求不变。
  - 普通分类器的提示词也带上这段回答。
  - 截取：开头 400 字 + 末尾 800 字。
- **api**：`/turn` 的 `previous_answer` 一路传到分类器。
- **网关路径**：从真实录制的 Claude Code 第 2 轮请求（`internal/wire/testdata/anthropic-cc-turn2.json`）和 Codex 第 2 轮请求中取出上一轮回答。
- **Claude Code 插件**：一轮结束后的下一次 `/turn` 带着上一轮的 `answer`；子 Agent 的回答不记录。
- **Pi**：同上；performance 档那一轮的回答也会在下一轮带上。
- **Codex hook**：Stop 写入回答，下一次 UserPromptSubmit 读出并带上；是别的会话的文件则不用。
