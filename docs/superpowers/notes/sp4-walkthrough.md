# SP4 真机走查记录（2026-10-05）

环境：pi 1.0.0 + 本地包 `clients/pi`（settings.json packages 已把 SP0 死链
替换为本包，可逆）；隔离网关 queqiao@3426（XDG 临时目录，分类器
deepseek/deepseek-flash）；QUEQIAO_URL 指向 3426。

## §11 SP4-pi 验收项

| # | 项 | 结果 | 证据 |
| --- | --- | --- | --- |
| 1 | 一轮简单提问后档位为 fast、模型随之切换 | ✅ | 3 个会话（01a10a2a/2b/2d）决策日志 `pi fast R6-adopt`（773–1029ms，预算内）；首轮请求被切到 `group/qq-fast` 后打到 zhipu 成员（错误文本「Zhipu GLM 429」本身证明请求带着档位模型出发） |
| 2 | 「不对」升档 balanced | ⏳ | 被上游限额阻断：zhipu 5h 限额 13:55 才重置；copilot 渠道当日全 400。决策链路（tier+dissatisfied 双问 2.4s）在 SP3 走查已验证 |
| 3 | `/model` 手动切换 → feedback + 停自动 | ⏳ | 需交互 TUI；`model_select` 三来源语义与自切屏蔽有单测覆盖 |
| 4 | `/fork` 继承父档位 | ⏳ | 需交互 TUI；机制有单测（header parentSession → parent_session） |

## 发现

1. **pi 会回写 models.json**：外部手工改 provider 配置在 pi 进程退出时被
   内存状态覆盖还原。改 magpie 网关地址不能用编辑器——要么在 pi 内
   `/model provider` 流程改，要么接受「决策走 QUEQIAO_URL、模型走
   pi 自己 provider」的 split（本走查即后者）。apiKey 支持 `$NAME` env
   插值，**baseUrl 不支持**（实测 "Invalid URL"）。
2. 首轮 `/turn` 单问（tier）约 800ms < 1500ms 预算；带 dissatisfied 的双问
   2.4s 会超时——与 SP3 走查结论一致（§5.7 竞速备注已回写 spec）。
3. 走查期间为绕限额把 fast 档成员从 zhipu/glm-5.3-flash 换成
   minimax-cn/MiniMax-M2.7-highspeed（隔离 XDG 内的临时配置，用户
   真实配置未动）。
