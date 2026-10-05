# queqiao 验收清单（剩余真机抽检）

## 执行结果（2026-10-05 22:14–22:24，隔离环境 3426）

| 项 | 结果 | 证据（router.jsonl 决策） |
| --- | --- | --- |
| 1.1 简单提问→fast | ✅ | `claude-code fast R6-adopt 330–723ms`（会话 1b24fd0d / d708227d）；首轮一次 1501ms 压线 → R8-default，重跑即 fast |
| 1.2 不对→升档 | ✅ | d708227d：`fast R6-adopt 330ms` → `balanced R3-escalate 331ms` |
| 1.3 `/fork` 继承 | ✅ | `--fork-session`：fork 会话 58ba6df9 首条 `balanced R4-escalation-hold 467ms`（父 d708227d 已 balanced） |
| 1.4 plan mode→performance | ✅ | `--permission-mode plan`：会话 365042c8 `performance R2-plan-mode` |
| 2.1 Pi 简单提问→fast | ✅ | 01a10c70 `pi fast R6-adopt 314ms` |
| 2.2 `/model` 手切→feedback+停自动 | ⏳ 需人工 | 非交互探测：`pi -p --model k3` **不算手切**（无 feedback，且仍被自动切档）——中段 `/model` 仍须 TUI 人工验 |
| 2.3 锤档停自动 | ⏳ 随 2.2 | |
| 2.4 Pi `/fork` 继承 | ✅ | `pi -p --fork 01a10c70`：fork 会话 01a10c72 首条 `balanced R4-escalation-hold 868ms` |
| 3. 两周实验/首份报表 | ⏳ 条件触发 | 需正式启用 queqiao 网关并跑满第一阶段 |

**本次执行对清单的修订（下次按修订后的做）**

1. **CC 四项在 `-p` 下全部可自动化**：`--fork-session` 会让 `classic.SessionStart` 触发（S3 的「`-p` 下不触发」只适用于不带 fork/resume 的普通 `-p`）；`--permission-mode plan` 可非交互进 plan mode。1.3/1.4 不再需要人工。
2. **插件的 `gateway_url` 必须显式指向检查网关**：mod 的 `/turn` 走 `gateway_url`（默认 3425），与 `ANTHROPIC_BASE_URL` 是两条独立通道——只改后者会出现「模型流量走对了、但决策层其实是网关兑底（harness=gateway）」的假象。检查前执行 `claude plugin install queqiao-router@queqiao --config gateway_url=http://127.0.0.1:3426`，收尾还原 3425。
3. **jev 分类器时延波动 314–1526ms**：>1500ms 时 mod 竞速超时（R7-carry 或 R8-default）且网关兑底也超时，该轮档位不切换（下一轮补偿）。验收时首轮若见 R8-default，重跑一次即可——spec §5.7 已记录此时序现实。
4. Pi 的 `--fork <id>` 同样可非交互验证继承（2.4 不必人工）；`pi -p --model X` 不算手切。
5. `models.json` 还原要**写完再核验**：本次发现备份内容已是 3426（来源未定，疑为某次 pi 运行回写），`mv` 还原后必须 `grep` 确认——清单原有该步，正因此才抳到。

---

> 项目状态：spec §11 全部子项目已合并（SP0–SP6，PR #4–#11）。以下三项是**只有在真机才能确认**的部分。
> 走查记录：`~/workspace/dev/queqiao/docs/superpowers/notes/sp{3,4,6}-walkthrough.md`。
> 每项都给了「命令 → 预期 → 怎么判定失败」。

---

## 0. 隔离环境搭建（三处检查共用，约 2 分钟）

用隔离配置目录跑网关，**不动你 3425 上的正式 magpie**。

```bash
ISO=$(mktemp -d /tmp/qq-check.XXXX); echo "ISO=$ISO"
XDG=$ISO/cfg; mkdir -p $XDG/queqiao
# ⚠️ 这一步会复制含真实 key 的 providers.json（合并后的新规则要求：用完立即删）
cp ~/.config/magpie/providers.json $XDG/queqiao/providers.json
cd ~/workspace/dev/queqiao && make cli          # 产出 ./queqiao（分支 queqiao 上就是最新）
export XDG_CONFIG_HOME=$XDG XDG_CACHE_HOME=$ISO/cache

# 四个组（成员是当时实测可用的；额度用尽就换一个再 add，见文末）
./queqiao group add qq-fast     "models=minimax-cn/MiniMax-M2.7-highspeed" routing=order stays=auto
./queqiao group add qq-balanced "models=kimi-code-cn/k3"                    routing=order stays=auto
./queqiao group add qq-perf     "models=minimax-cn/MiniMax-M2.7"             routing=order stays=auto
./queqiao group add queqiao "models=group/qq-balanced,group/qq-perf,group/qq-fast" routing=order stays=turn

# router.json（分类器用你配置里自带的 typesafe/jev-latest —— 单请求 ~300ms，必须在 1500ms 预算内）
python3 - <<'PY'
import json, os
p = os.path.join(os.environ["XDG_CONFIG_HOME"], "queqiao", "router.json")
cfg = {
  "version": 1, "router_group": "queqiao",
  "tiers": {
    "fast":        {"group": "qq-fast",     "claude_alias": "haiku",  "criteria": "Little work: a question, an explanation, reading logs, running a command, a one-line or mechanical change, a read-only code search"},
    "balanced":    {"group": "qq-balanced", "claude_alias": "sonnet", "criteria": "Some work: an ordinary bug fix or a small feature in code already understood, adding tests, a single-file refactor"},
    "performance": {"group": "qq-perf",     "claude_alias": "opus",   "criteria": "Much work: a change across several files, a bug whose cause is unknown, concurrency, performance or security issues, an architecture or design decision, a long multi-step plan"}},
  "default_tier": "balanced", "classifier": "typesafe/jev-latest", "classify_timeout_ms": 1500,
  "thresholds": {"tier_min": 0.4, "dissatisfied_min": 0.7}, "escalate_turns": 2, "cache_ttl_seconds": 300,
  "fixed_agents": {"Explore": "fast", "statusline-setup": "fast", "claude-code-guide": "fast", "Plan": "performance", "explorer": "fast"},
  "experiment": {"enabled": False, "router_percent": 50, "control_tier": "performance", "salt": "check-salt"}}
json.dump(cfg, open(p, "w"), ensure_ascii=False, indent=2)
PY

# 起网关（3426，避开你 3425 的 magpie）
MAGPIE_ADDR=127.0.0.1:3426 nohup ./queqiao serve > $ISO/serve.log 2>&1 &
sleep 2 && curl -s http://127.0.0.1:3426/v1/queqiao/router | python3 -c "import json,sys;print('网关就绪:', list(json.load(sys.stdin)['tiers']))"
```

看日志/决策（所有检查共用）：

```bash
tail -5 $XDG/queqiao/router.jsonl | python3 -c "
import json,sys
for l in sys.stdin:
    d=json.loads(l); print(d['t'][11:19], d.get('kind'), d.get('session','')[:12], d.get('harness'), d.get('tier'), d.get('reason'))"
```

---

## 1. Claude Code：`/fork` 继承 + plan mode（TUI 交互，约 5 分钟）

插件已从 GitHub 装好（`queqiao-router@queqiao`），**无需重装**。

```bash
mkdir -p /tmp/qq-cc && cd /tmp/qq-cc
cat > .claude/settings.local.json <<'EOF'   # 项目级覆盖：全局 settings.json 的 env 会压过进程变量（SP3 实测）
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:3426",
    "ANTHROPIC_AUTH_TOKEN": "magpie",
    "ANTHROPIC_MODEL": "group/queqiao",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "group/qq-fast",
    "ANTHROPIC_SMALL_FAST_MODEL": "group/qq-fast",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "group/qq-balanced",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "group/qq-perf",
    "ANTHROPIC_DEFAULT_FABLE_MODEL": "group/qq-perf"
  }
}
EOF
claude
```

**步骤与判定**

| 步 | 做什么 | 预期 | 失败判定 |
| --- | --- | --- | --- |
| 1.1 | 问一句 `这个目录下的文件有哪些？` | 决策日志出现该会话 `fast` | 无决策 → 插件未加载（`/plugin list` 看 queqiao-router 是否 enabled） |
| 1.2 | 同一会话发 `不对，我要的是…` | 决策 `balanced R3-escalate`，且**模型真的切到 balanced** | 有决策但模型没切 → 分类耗时超预算（看 `latency_ms` 是否逼近 1500） |
| 1.3 | `/fork`，在新会话问一句简单问题 | fork 会话首条决策 **`balanced`**（继承），而非 `fast` | 首条是 fast → 继承失效，回报该会话 ID 与首条决策 |
| 1.4 | 主会话 Shift+Tab 进 plan mode，问一句 | 决策 `performance R2-plan-mode` | 仍 fast/balanced → plan 检测失效 |

---

## 2. Pi：`/model` 手切反馈 + `/fork` 继承（TUI 交互，约 5 分钟）

```bash
# ① 把 pi 的 magpie provider 临时指向 3426（走查后必须还原！见收尾）
cp ~/.pi/agent/models.json ~/.pi/agent/models.json.bak
python3 -c "
import json,os
p=os.path.expanduser('~/.pi/agent/models.json'); d=json.load(open(p))
d['providers']['magpie']['baseUrl']='http://127.0.0.1:3426/v1'
json.dump(d,open(p,'w'),ensure_ascii=False,indent=1); print('baseUrl → 3426')"

# ② 起 pi（扩展从 ~/workspace/dev/queqiao/clients/pi 加载，settings.json 已指好）
QUEQIAO_URL=http://127.0.0.1:3426 pi
```

| 步 | 做什么 | 预期 | 失败判定 |
| --- | --- | --- | --- |
| 2.1 | 问一句简单的 | 决策 `pi fast`，pi 顶栏模型切到 fast 档 | 无决策 → QUEQIAO_URL 没生效（hook 子进程不继承环境变量是 codex 的问题，pi 是进程内不受影响） |
| 2.2 | `/model` 切到任意别的模型 | router.jsonl 出现 `feedback … manual_model_switch …→…` | 无 feedback → `model_select` 屏蔽逻辑误判（把我们自己的 setModel 当手切） |
| 2.3 | 再问一句 | **不再**出现新决策（钉档后停自动） | 仍有新决策 → manualPinned 未生效 |
| 2.4 | 重启 pi（新会话），发简单问题 → 发 `不对…`（同会话）→ `/fork` → 新会话问一句 | 父会话 `balanced R3`，fork 首条 `balanced` | fork 首条 fast → 继承失效（2026-10-05 修过一次同款 bug，回报新会话 ID） |

---

## 3. 两周实验与首份真实报表（条件触发：正式开始用 queqiao 路由后）

**前置：让 queqiao 成为你的正式网关（3425）**——目前 3425 跑的是上游 magpie，它没有 router 端点。

```bash
# 配置迁移：queqiao 用自己的配置目录（appdir 名不同）
#   ~/.config/magpie/*  →  ~/.config/queqiao/*   （providers/logins/等；先备份两边）
# 然后停 magpie，用 queqiao 起 3425：
cd ~/workspace/dev/queqiao && ./queqiao router init --preset cn   # 写四组 + router.json（按实际 provider 调整成员）
./queqiao serve                                                   # 默认 127.0.0.1:3425
# AGENT 侧无需改配置：CC/Codex/Pi 的 baseUrl 本来就指 3425
```

**开启实验**（`~/.config/queqiao/router.json`）：

```json
"experiment": {"enabled": true, "router_percent": 50, "control_tier": "performance", "salt": "<随机字符串，写死别改>"}
```

- **第一阶段**：`control_tier=performance`，至少 2 周**或**每组攒到 150 个以 PR 结束的会话（先到为准）
- **第二阶段**：改 `control_tier=fast`，至少 1 周；任一组明显工作不正常就提前停

**出报表**：

```bash
./queqiao router report --since 14d          # 文本表
./queqiao router report --since 14d --json   # 机器可读
```

判定要点（spec §9）：样本 <100/组会标「样本不足」，此时**只看成本与手动换模型率**，合并率差异不作结论；PR 终态由 `gh pr view` 补查（没装 gh 显示「未知」，其余指标照常）。

---

## 收尾（每次抽检完必跑）

```bash
# 1) 停隔离网关
pkill -f "queqiao serve"

# 2) 还原 pi 的 provider（检查 2 做过才需要）
mv ~/.pi/agent/models.json.bak ~/.pi/agent/models.json
grep -o '"baseUrl": "[^"]*"' ~/.pi/agent/models.json | head -1   # 应显示 3425

# 3) 删隔离目录（含 provider key 副本 —— 新规则：用完立即删）
trash "$ISO"        # 或 rm -rf "$ISO"
grep -rl "apikey_" /tmp --include="*.json" 2>/dev/null | head -3   # 应无输出
```

## 附：成员不可用时的替换

```bash
export XDG_CONFIG_HOME=$XDG XDG_CACHE_HOME=$ISO/cache
./queqiao models | grep -i "<候选模型>"        # 找可用 provider/模型
./queqiao group add qq-fast "models=<provider>/<model>" routing=order stays=auto
```

已知波动（2026-10-05 实测）：copilot 全系 400（账号侧）、anthropic/gemini provider 在配置里是关闭状态、zhipu 有 5h/周额度、kimi 部分模型需高等级订阅。可用的稳定成员：`minimax-cn/MiniMax-M2.7[-highspeed]`、`kimi-code-cn/k3`、`deepseek/deepseek-flash`（思考型，仅作分类器备选）。
