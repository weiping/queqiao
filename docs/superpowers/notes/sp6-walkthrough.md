# SP6 真机走查记录（2026-10-05）

环境：codex-cli 0.160.0（`codex exec --dangerously-bypass-hook-trust`，S7 路径）；
隔离 HOME（`router init` 的 Codex 步骤真机首验：config.toml 三键 + queqiao-models.json ✓）；
隔离网关 queqiao@3426（jev 分类器）；插件经本地 marketplace 安装进隔离 HOME。

## §11 SP6-codex 验收

| 项 | 结果 | 证据 |
| --- | --- | --- |
| hook 集成测试 | ✅ | `internal/harness/codex`：user-prompt 字段/turn_id/防御、pre-agent 保留参数+model、post-bash 全文 PR 匹配 |
| 插件与市场文件 | ✅ | legacy 格式 + 本地 `codex plugin marketplace add`/`plugin add` 实测通过（Agent Plugins Schema 校验不适用于 legacy 格式，见 spec 修订） |
| §8 Responses e2e | ✅ | `e2e_codex_test.go`：三轮 fast→balanced(R3)→balanced(R4)，hint 路由命中各档成员 1/2/0 |
| 真机：信任 hook 后简单提问进 qq-fast | ✅ | `decide codex fast R6-adopt 638ms` + `hint_consumed fast`；请求由 fast 档成员服务（usage `MiniMax-M2.7-highspeed codex`） |

## 发现（全部回写 spec/README）

1. **hooks.json 路径**：`.codex-plugin/plugin.json` 的 `"hooks"` 字段相对**插件根**解析——
   `"./hooks.json"` 要求文件在根目录（spike 布局）；spec §6.9 的 `hooks/hooks.json`
   布局需配 `"./hooks/hooks.json"`。首次实测 `"./hooks.json"` + 子目录文件 = hooks
   静默不加载。已修（plugin.json 用 `./hooks/hooks.json`）。
2. **hook 子进程不继承自定义环境变量**（S7 已知，本次再证）：`QUEQIAO_URL` 传不进
   hook，默认打 3425。生产部署网关必须在默认端口，或命令行 env 前缀（走查用
   `env QUEQIAO_URL=… queqiao hook …` 证明全链路）。README 排障节 + spec §6.6 备注。
3. jev 分类偶发 >1500ms（1502ms 实测）→ 该轮 R8-default；hint 机制保证请求仍按
   决策路由（hint_consumed balanced → k3 服务）。预算内轮次（638ms）正常出 fast。
4. 插件缓存位于 `~/.codex/plugins/cache/<marketplace>/<plugin>/<version>/`，改
   hooks.json 需重装才生效（走查用缓存内 patch + env 前缀验证）。
