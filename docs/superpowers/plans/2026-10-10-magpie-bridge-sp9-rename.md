# SP9：英文名改为 Magpie Bridge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 queqiao 对外可见的名字全部换成 Magpie Bridge / `mbridge`，并删掉只为 qq-v0.1.x 用户写的迁移代码；行为不变。

**Architecture:** 先删迁移代码，再改 Go 侧名字（模块、命令、目录、环境变量、接口、分组、服务、Codex profile），再改三个插件，最后改发版、CI、文档，并加一条防漏改的测试。每一步都先改测试里的期望值看它失败，再改代码。

**Tech Stack:** Go，TypeScript（Claude Code mod、Pi 扩展），GitHub Actions，sh/PowerShell。

**Spec:** `docs/superpowers/specs/2026-10-10-magpie-bridge-sp9-rename-design.md`

## Global Constraints

- 名字一律按 spec §3 的对照表，原样照抄，不另起名。
- 模块 `github.com/weiping/magpie-bridge`；命令目录 `cmd/mbridge`；配置目录 `~/.config/magpie-bridge`（`MBRIDGE_CONFIG_DIR` 覆盖）；日志 `logs/mbridge.log`。
- 接口前缀 `/v1/bridge/`；分组 `mb-fast`、`mb-balanced`、`mb-perf`，路由组 `mbridge`；User-Agent `mbridge/1`。
- 不保留任何旧名字的别名或兼容读取。
- `docs/superpowers/` 下已有文件不改（新文件除外）；SP8 spec 只在末尾追加一句作废说明。
- 插件与 npm 版本保持 0.2.0；发版标签 `v*`。
- 每个任务一个提交；测试命令 `go vet ./... && go test -race ./...`，Windows 相关改动由 CI 的 Windows 任务验证。

## Review Focus

1. **插件仍请求旧接口或旧端口变量**：Claude Code mod、Pi 扩展、Codex hook 必须都打 `/v1/bridge/turn` 并读 `MBRIDGE_URL`；漏一个就是静默落默认档（Task 3 的三个插件测试各断言新路径）。
2. **`router init` 生成的 `router.json` 与 magpie 分组名不一致**：init 写的 `router_group`、各档 `group` 必须与它用 `magpie group add` 建的 id 相同（Task 2 `TestRouterInitCnWritesGroupsAndConfig` 断言两边都是 `mb-*`/`mbridge`）。
3. **Codex profile 文件名与 `-p` 参数不一致**：写出 `~/.codex/mbridge.config.toml`，profile 内 provider 表名 `mbridge`，目录文件 `mbridge-models.json`（Task 2 codexcfg 测试断言三处）。
4. **安装脚本下载的资产名与发版产物不一致**：install.sh/ps1 取 `mbridge-<os>-<arch>[.exe]`，Makefile 产出同名（Task 4 install 测试和 release 构建核对）。
5. **漏改的旧名字**：`TestNoQueqiaoLeft` 兜底（Task 4）。

---

## Task 1：删掉迁移代码

**Files:**
- Delete: `internal/migrate/`、`cmd/queqiao/migrate_cli.go`
- Modify: `cmd/queqiao/main.go`（命令表去掉 `migrate`）、`cmd/queqiao/cli_test.go`（删迁移相关测试）、`internal/codexcfg/codex.go` 与测试（删 `CleanLegacy` 及其 fixture）、`install.sh`、`install.ps1`、`scripts/install_test.sh`、`scripts/install_test.ps1`（删迁移提示与对应用例）、`README.md`（删“从 qq-v0.1.x 升级”一节）
- Modify: `docs/superpowers/specs/2026-10-10-queqiao-sp8-standalone-design.md`（末尾加一行：§3.2 第 6 条与 §7.1 由 SP9 作废）

- [ ] **Step 1: 改测试**：`TestCLIListsOnlyQueqiaoCommands` 的期望命令表去掉 `migrate`，并断言 `help` 输出不含 `migrate`；`install_test.sh` 的 “qq-v0.1.x data” 用例改为“有 `~/.config/queqiao/providers.json` 时输出不含 `migrate`”。
- [ ] **Step 2: 运行确认失败**：`go test ./cmd/queqiao/ -run TestCLIListsOnly` 和 `sh scripts/install_test.sh`，预期各有一处 FAIL。
- [ ] **Step 3: 删除上列代码与文档段落**。
- [ ] **Step 4: 运行确认通过**：`go vet ./... && go test -race ./... && sh scripts/install_test.sh`。
- [ ] **Step 5: 提交**：`refactor!: drop the qq-v0.1.x migration (no one outside ever installed it)`

## Task 2：Go 侧改名

**Files:**
- Modify: `go.mod`（模块名）及全部 import；`git mv cmd/queqiao cmd/mbridge`
- Modify: `internal/fsutil`（配置目录与 `MBRIDGE_CONFIG_DIR`）、`internal/harness`（`MBRIDGE_URL`）、`internal/router`（`/v1/bridge/*` 注册、默认 `router_group`、事件里的命名）、`internal/router/ask.go` 或 `internal/magpie`（User-Agent `mbridge/1`）、`internal/proxy`（错误文案里的 queqiaod）、`internal/service`（launchd 标签、systemd 单元名、计划任务名、golden 文件）、`internal/codexcfg`（profile 文件名、provider 表名、目录文件名）、`cmd/mbridge`（帮助文字、`router init` 的分组 id、serve 的日志文件名、update 的资产名与标签前缀 `v`）、`internal/testmagpie`（`MBRIDGE_TEST_MAGPIE`）、`contract/`、`e2e/`
- Test: 各包现有测试

**Interfaces:**
- Produces: HTTP 接口 `/v1/bridge/{turn,review,feedback,session,lineage,router}`；环境变量 `MBRIDGE_URL`、`MBRIDGE_CONFIG_DIR`；Codex `-p mbridge`；分组 `mb-*`/`mbridge`。Task 3 的插件只依赖这几项。

- [ ] **Step 1: 改测试期望**：用脚本把各 `_test.go` 和 golden 文件里的旧名字换成 spec §3 的新名字（`group/qq-` → `group/mb-`、`qq-perf` → `mb-perf`、`"queqiao"` 路由组 → `"mbridge"`、`/v1/queqiao/` → `/v1/bridge/`、`QUEQIAO_` → `MBRIDGE_`、`queqiao.config.toml` → `mbridge.config.toml`、`queqiao-models.json` → `mbridge-models.json`、`io.github.weiping.queqiao` → `io.github.weiping.magpie-bridge`、`queqiao.service` → `mbridge.service`、`queqiaod.log` → `mbridge.log`、`queqiao-router/1` → `mbridge/1`、资产 `queqiao-<os>` → `mbridge-<os>`、标签 `qq-v` → `v`），测试函数名里的 Queqiao 也改掉（`TestCLIListsOnlyQueqiaoCommands` → `TestCLIListsOnlyItsCommands`）。生产代码先不动。`internal/wire/testdata/` 是抓到的真实请求体，原样保留（LESSONS“Build fixtures from the reporter's literal bytes”）。
- [ ] **Step 2: 运行确认失败**：`go test ./...`，预期多个包 FAIL，且失败都指向旧名字。
- [ ] **Step 3: 改生产代码与模块名**：同一脚本作用于非测试文件，再手工检查注释里的来历说明（保留“曾叫 queqiao”的不改）；`git mv cmd/queqiao cmd/mbridge`，`go mod edit -module github.com/weiping/magpie-bridge`，全部 import 跟改。
- [ ] **Step 4: 运行确认通过**：`go vet ./... && go test -race ./...`；`go test -tags "contract e2e" ./contract/ ./e2e/ -magpie=<官方 magpie>`；`GOOS=windows go vet ./...`。
- [ ] **Step 5: 提交**：`refactor!: Go side becomes mbridge (module, command, dirs, env, endpoints, groups)`

## Task 3：三个插件改名

**Files:**
- Modify: `clients/claude-code/`（plugin.json 名字 `magpie-bridge`，`register.ts` 的接口路径、状态栏前缀 `mbridge:`、README）、`.claude-plugin/marketplace.json`（市场名、插件名）
- Modify: `clients/codex/`（plugin.json 名字 `magpie-bridge-codex`，`hooks.json` 里的命令 `mbridge hook …`，README）、`.agents/plugins/marketplace.json`
- Modify: `clients/pi/`（`package.json` 名字 `@weiping/pi-magpie-bridge`，`git mv extensions/queqiao.ts extensions/mbridge.ts`，`src/client.ts` 接口路径与 `MBRIDGE_URL`，README）
- Modify: `cmd/mbridge/plugin_versions_test.go`（市场清单测试跟新名字）

- [ ] **Step 1: 改测试**：Claude Code `register.test.ts` 断言请求路径 `/v1/bridge/turn`、状态文字以 `mbridge:` 开头；Pi 测试断言 `/v1/bridge/turn` 与读取 `MBRIDGE_URL`；`TestMarketplaceListsThePluginsOwnVersion` 按新插件名查找。
- [ ] **Step 2: 运行确认失败**：`claude plugin test clients/claude-code`、`cd clients/pi && npx vitest run`、`go test ./cmd/mbridge/ -run TestMarketplace`。
- [ ] **Step 3: 改插件代码、清单和 README**；Pi 的 `package-lock.json` 用 `npm install --package-lock-only` 重新生成。
- [ ] **Step 4: 运行确认通过**：同 Step 2 三条命令；`claude plugin validate clients/claude-code`。
- [ ] **Step 5: 提交**：`refactor!: plugins become magpie-bridge, magpie-bridge-codex, @weiping/pi-magpie-bridge`

## Task 4：发版、CI、文档与防漏改

**Files:**
- Create: `cmd/mbridge/name_test.go`（`TestNoQueqiaoLeft`）
- Modify: `git mv .github/workflows/queqiao-release.yml .github/workflows/release.yml`（触发 `v*`，校验 `mbridge-*`）、`git mv .github/workflows/queqiao-contract.yml .github/workflows/contract.yml`、`test.yml`、`Makefile`（`BIN = mbridge`，`./cmd/mbridge`）、`install.sh`、`install.ps1`（资产名、仓库 `weiping/magpie-bridge`、`MBRIDGE_*` 变量、装成 `mbridge`）、`scripts/install_test.sh`、`scripts/install_test.ps1`、`scripts/fetch-magpie.sh`（注释）、`README.md`（标题 “鹊桥 Magpie Bridge”，一句来历说明，`mb-*` 分组名归 Magpie Bridge 使用的说明，全部命令与地址换新）、`AGENTS.md`、`LESSONS.md` 开头说明、`.gitignore`、`.claude/jev/`、`.pi/jev/`（Jev 开发规则里的项目名）、`scripts/queqiao-smoke.sh`（还能用就改名 `mbridge-smoke.sh` 并换名字；只适用于 fork 的就删掉）
- Move: `docs/queqiao-验收清单.md` → `docs/superpowers/notes/`（fork 时期的验收记录，作历史保留）

- [ ] **Step 1: 写防漏改测试** `TestNoQueqiaoLeft`：执行 `git grep -n -i -E 'queqiao|QUEQIAO_|/v1/queqiao|group/qq-|qq-v[0-9]'`（仓库根目录），排除 `docs/superpowers/` 下的文件、`internal/wire/testdata/`（真实请求体）、本测试文件自身，以及 README 中含“曾叫 queqiao”的那一行；断言剩余结果为空，失败时列出每一处。仓库外运行（没有 `.git`）时跳过。
- [ ] **Step 2: 运行确认失败**：`go test ./cmd/mbridge/ -run TestNoQueqiaoLeft`，预期列出 workflow、安装脚本、README 等处。
- [ ] **Step 3: 改上列文件**；`install_test.sh` 的期望跟着改（资产 `mbridge-<os>-<arch>`、装出 `mbridge`）。
- [ ] **Step 4: 运行确认通过**：`go test ./cmd/mbridge/ -run TestNoQueqiaoLeft`、`sh scripts/install_test.sh`、`make release VERSION=v0.0.0-test` 后 `cd dist && sha256sum -c checksums.txt` 且六个文件都叫 `mbridge-*`、`actionlint .github/workflows/*.yml`、`go test -race ./...`。
- [ ] **Step 5: 提交**：`chore!: releases, CI and docs say Magpie Bridge; guard against the old name`

## Task 5：PR 与收尾

- [ ] **Step 1: 开 PR** 到 `main`，描述里写明替换脚本、跑过的命令和结果，以及作者要做的三件事：仓库改名 `magpie-bridge`、推 `v0.2.0`、发布 `@weiping/pi-magpie-bridge@0.2.0`。
- [ ] **Step 2: 独立审查**：整个分支交给一个新上下文的审查者，重点看 Review Focus 五条。严重和重要的问题先写失败测试再修。
- [ ] **Step 3: CI 全绿后**，在 PR 上写明验证内容，用 `--match-head-commit` 合并。
- [ ] **Step 4: 更新本机验证清单**：把 SP8 清单里的命令、分组、地址换成新名字，加上 spec §5.1 的一次性分组改名，发给作者。
