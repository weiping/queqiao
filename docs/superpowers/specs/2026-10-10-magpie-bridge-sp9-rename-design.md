# SP9 设计：英文名改为 Magpie Bridge

- 日期：2026-10-10
- 状态：书面审阅通过（2026-10-10）
- 上位规格：[`2026-10-10-queqiao-sp8-standalone-design.md`](2026-10-10-queqiao-sp8-standalone-design.md)（下称“SP8 规格”）。本文只改名字，不改行为。

## 1. 为什么改

中文名“鹊桥”保留。英文名 queqiao 是拼音，外国用户不会读、记不住，也搜不到，不利于推广。“鹊桥”直译就是 Magpie Bridge：中英文意思一致，也直接点明了它和 magpie 的关系。

改名放在第一次公开发布（v0.2.0）之前做。queqiao 的包和插件都还没有公开过，用户只有作者本人，所以不做任何迁移和兼容。作者本机的 qq-v0.1.x 由作者手动卸载（2026-10-10 已给出步骤）。

## 2. 目标、成功标准与非目标

### 2.1 目标

把所有对外可见的名字统一成 Magpie Bridge 或 `mbridge`，同时删掉 SP8 为 qq-v0.1.x 用户写的迁移和清理代码。

### 2.2 成功标准

1. 除 `docs/superpowers/` 下的历史文档外，仓库里出现 `queqiao`、`qq-` 分组名、`QUEQIAO_`、`/v1/queqiao` 的地方只剩 README 里一句来历说明（`git grep` 核对）。
2. `go vet ./...`、`go test -race ./...`、安装脚本测试、Pi 扩展测试，以及针对官方 magpie 最新发布版的 contract 和 e2e 测试全部通过。
3. 构建出的命令叫 `mbridge`，`mbridge help` 里没有 `migrate`。
4. 三个插件的清单、市场文件、npm 包名都是新名字，版本仍是 0.2.0（之前从未发布过）。

### 2.3 非目标

| 不做 | 原因 |
| --- | --- |
| 迁移 qq-v0.1.x 的数据或配置 | 没有外部用户；作者手动卸载 |
| 旧接口、旧环境变量、旧目录的兼容别名 | 同上 |
| 改写 `docs/superpowers/` 下已有的 spec、plan 和 spike | 那是历史记录；新文档用新名字 |
| 改选档行为 | 只改名 |

## 3. 名字对照

| 类别 | 现在 | 改成 |
| --- | --- | --- |
| 产品名 | queqiao / 鹊桥 | Magpie Bridge / 鹊桥 |
| GitHub 仓库 | `weiping/queqiao` | `weiping/magpie-bridge`（作者在 GitHub 设置里改名，旧地址自动跳转） |
| Go 模块 | `github.com/weiping/queqiao` | `github.com/weiping/magpie-bridge` |
| 命令 | `queqiao`（`cmd/queqiao`） | `mbridge`（`cmd/mbridge`） |
| 后台进程 | queqiaod（`queqiao serve`） | `mbridge serve`，文档里叫 Magpie Bridge daemon |
| 配置目录 | `~/.config/queqiao` | `~/.config/magpie-bridge`；里面的 `router.json`、`router.jsonl`、`logs/` 文件名不变 |
| 日志 | `logs/queqiaod.log` | `logs/mbridge.log` |
| 环境变量 | `QUEQIAO_URL`、`QUEQIAO_CONFIG_DIR`、`QUEQIAO_VERSION`、`QUEQIAO_BIN_DIR`、`QUEQIAO_DOWNLOAD_BASE`、`QUEQIAO_TEST_MAGPIE` | `MBRIDGE_URL`、`MBRIDGE_CONFIG_DIR`、`MBRIDGE_VERSION`、`MBRIDGE_BIN_DIR`、`MBRIDGE_DOWNLOAD_BASE`、`MBRIDGE_TEST_MAGPIE` |
| HTTP 接口 | `/v1/queqiao/{turn,review,feedback,session,lineage,router}` | `/v1/bridge/{…}`，子路径不变 |
| magpie 分组 | `qq-fast`、`qq-balanced`、`qq-perf`，路由组 `queqiao` | `mb-fast`、`mb-balanced`、`mb-perf`，路由组 `mbridge`；Agent 用 `group/mbridge` |
| `router.json` 默认值 | `router_group: "queqiao"`，各档 `qq-*` | `router_group: "mbridge"`，各档 `mb-*` |
| 分类请求的 User-Agent | `queqiao-router/1` | `mbridge/1` |
| Codex profile | `~/.codex/queqiao.config.toml`（`codex -p queqiao`）、`queqiao-models.json`、`[model_providers.queqiao]` | `~/.codex/mbridge.config.toml`（`codex -p mbridge`）、`mbridge-models.json`、`[model_providers.mbridge]` |
| 服务 | launchd `io.github.weiping.queqiao`、systemd `queqiao.service`、计划任务 `queqiao` | `io.github.weiping.magpie-bridge`、`mbridge.service`、`mbridge` |
| 发版标签与资产 | `qq-v*`；`queqiao-<os>-<arch>[.exe]` | `v*`；`mbridge-<os>-<arch>[.exe]` |
| Workflow 文件 | `queqiao-release.yml`、`queqiao-contract.yml` | `release.yml`、`contract.yml`；issue 标签 `contract` 不变 |
| Claude Code 插件 | 市场 `queqiao`，插件 `queqiao-router` | 市场 `magpie-bridge`，插件 `magpie-bridge`；状态栏前缀 `mbridge:` |
| Codex 插件 | `queqiao-router-codex`，hook 命令 `queqiao hook …` | `magpie-bridge-codex`，`mbridge hook …` |
| Pi 扩展 | `@weiping/pi-queqiao`，`extensions/queqiao.ts` | `@weiping/pi-magpie-bridge`，`extensions/mbridge.ts` |
| 安装脚本 | `install.sh`、`install.ps1`，装成 `queqiao` | 文件名不变，装成 `mbridge`，地址 `raw.githubusercontent.com/weiping/magpie-bridge/main/…` |

## 4. 删除的部分

SP8 里只为 qq-v0.1.x 用户服务的代码一并删除：

- `mbridge migrate` 命令和 `internal/migrate` 包，以及它的测试和 fixture。
- `codexcfg.CleanLegacy`（清理 qq-v0.1.4 写进 `config.toml` 的条目）。
- 安装脚本里检测 `~/.config/queqiao/providers.json` 并提示迁移的那段，以及对应的测试用例。
- README 里“从 qq-v0.1.x 升级”一节，以及 `migrate-from-magpie.sh` 备份的说明。
- SP8 规格 §3.2 第 6 条、§7.1 作废（在 SP8 规格末尾注明，正文不改）。

## 5. 实施顺序

1. 一个 PR 完成第 3、4 节的全部改动。机械替换用脚本做，脚本放在 PR 描述里；替换后逐个检查有歧义的地方（例如 `queqiao` 出现在注释的来历说明里）。
2. PR 合并后，作者在 GitHub 把仓库改名为 `magpie-bridge`，并确认默认分支是 `main`。
3. 作者推送 `v0.2.0` 标签，发布 `@weiping/pi-magpie-bridge@0.2.0`。
4. 作者按 SP8 的本地验证清单（换成新名字）做真机验收。

### 5.1 作者本机的一次性收尾（不写成代码）

作者卸载 qq-v0.1.x 并把数据拷回 `~/.config/magpie` 后，magpie 里还留着 queqiao 时期的四个分组。用官方 magpie 改名，保留调好的成员和顺序（2026-10-10 用 magpie 0.1.1154 验证过，路由组引用的子分组会跟着改）：

```sh
magpie group set qq-fast     id=mb-fast     name=mb-fast
magpie group set qq-balanced id=mb-balanced name=mb-balanced
magpie group set qq-perf     id=mb-perf     name=mb-perf
magpie group set queqiao     id=mbridge     name=mbridge
```

指向旧分组 id 的 Agent 先改回普通模型（`magpie claude <模型>`，Codex、Pi 同理），装好 `mbridge` 后执行 `mbridge router init` 再接回 `group/mbridge` 和 `group/mb-balanced`。

模块路径在仓库改名之前就写成新名字：本仓库内的编译和测试不依赖远程地址；对外的 `go install` 和插件市场地址在仓库改名后才生效，这一步排在发版之前。

## 6. 测试

现有测试全部保留，只换名字。另加两条：

- `TestNoQueqiaoLeft`：用 `git grep` 列出仓库里的 `queqiao`、`QUEQIAO_`、`/v1/queqiao`、`group/qq-`，排除 `docs/superpowers/` 和 README 的来历说明后，结果必须为空。
- `mbridge help` 的输出不含 `migrate`（并入现有的 `TestCLIListsOnlyQueqiaoCommands`，改名为 `TestCLIListsOnlyItsCommands`）。

## 7. 风险

| 风险 | 应对 |
| --- | --- |
| 漏改的地方运行时才暴露（例如插件仍请求 `/v1/queqiao/turn`） | `TestNoQueqiaoLeft` 兜底；e2e 走完三个 Agent 的主路径 |
| 仓库改名前有人按新地址安装 | 发版排在仓库改名之后；README 只写新地址 |
| `mb-` 前缀和用户自己的分组撞名 | `router init` 会直接覆盖同名分组（SP8 终审记下的次要问题，本次不改）；README 写明 `mb-*` 和 `mbridge` 这几个分组名归 Magpie Bridge 使用 |
