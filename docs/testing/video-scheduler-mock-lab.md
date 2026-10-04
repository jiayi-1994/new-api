# 智能视频调度 mock 实验室：清理、恢复与复测

命令从仓库根目录运行，使用 PowerShell。容器管理入口为 `scripts/testing/video-mock-lab.py`；本机 Compose、私有凭据及测试程序位于 `.scratch/`，不保证随仓库克隆分发。所有上游均为本地模拟器。

**2026-10-04 清理已完成：删除 46 个容器。** 旧实验室删除 41 个，统一实验室删除实际存在的 `mock01`–`mock05` 共 5 个；其余 15 个统一 mock 原本不存在。容器总数从 178 降至 132，所有非目标容器的 ID 和状态未变，59 个卷、41 条镜像标签、19 个网络均未变化。证据保存在本机 `.scratch/video-mock-maintenance/cleanup-verification-20261004.json`。

本次通过 Rancher 自有 WSL 中的同一 Docker 引擎执行清理，未执行全局 `wsl --shutdown`。清理时 Windows Docker 接口仍有 Hyper-V socket 超时；随后补建 Seedance 实验室时，该接口已恢复，实际完成了容器创建和 HTTP 验证。两套旧实验室保留的 7 个网关、数据库和缓存容器在清理前就已停止，清理后仍保留该状态。2026-10-03 的 HTTP 快照保存在 `.scratch/video-mock-maintenance/snapshot-20261003T144157Z/`，属于历史证据，不代表当前运行状态。

## 新增：统一售价与 20 个 Seedance 上游

独立网关：`http://127.0.0.1:35000`。Compose 项目为 `codex-uvm-stress-seedance-20261004-f1567e0d`，包含网关、PostgreSQL、Redis 和 20 个上游；另有同项目标签的参考媒体容器。旧实验室不参与这些测试。上游管理端口为 `35010`–`35029`，媒体统计端口为 `35005`，数据库和 Redis 不发布宿主端口。

公开模型统一为 `seedance-2.0`，时长逐秒覆盖 **4–15 秒**，分辨率为 **480p / 720p / 1080p / 4K**；原需求中的 1024 已按用户确认替换为 1080p。实验售价分别为 **0.56 / 1 / 3 / 5 USD 每输出秒**。这些是实验参数，不代表真实供应商报价。不同采购模式、模型映射和参考视频费用均不改变公开模型的统一售价。

| 采购类别 | 渠道编号 | 能力与价格规则 |
| --- | --- | --- |
| 单分辨率按条 | 01–04 | 固定 480p、720p、720p、4K；02 仅允许 5/10/15 秒 |
| 多分辨率按条 | 05–08 | 各分辨率独立条价；05 的 720p 上限为 12 秒 |
| 单分辨率按秒 | 09–12 | 分别支持 480p、720p、1080p、4K |
| 多分辨率按秒 | 13–16 | 各档独立秒价；16 的 4K 仅允许 10–15 秒 |
| 多分辨率加参考视频费用 | 17–20 | 17/18/20 按参考视频输入秒数加价；19 按输出秒数加价 |

20 个容器使用 `seedance-hjmie`、`megabyai`、`meaicc`、`paipu`、`pidoi` 五种真实插件协议。MeAI `sd-2-c1` 只支持 720p，因此 03 的能力表仅包含 720p。各渠道详细价格及独立的 Decimal 选渠核算在 `scripts/testing/seedance-scheduler-e2e.py` 的 `PROFILES` / `expected_candidates` 中。

测试经管理 API 配置售价、分组、渠道和令牌，经 `/v1/videos` 提交真实网关请求，经真实轮询进入终态。数据库仅作只读核对，不写入健康样本或伪造资格。先满足能力、毛利、健康和容量门槛，再按既有优先级、稳定性、质量、采购成本规则选渠；“最优”不等同于无条件选最低价格。

本机入口依赖保留的 `.scratch/unified-video-model-plan/stress/{fixture.py,run_stress.py}`、mock 源码、媒体/TLS 文件及已缓存 Docker 镜像。它是该本地实验室的复测入口，不能在仅克隆仓库后直接运行。凭据保存在运行目录的 `credentials.private.json`、`relay-tokens.private.json`，不要提交这些文件。

```powershell
# 恢复现有实验室；不重建数据库、不重放请求。
python scripts/testing/seedance-scheduler-e2e.py --label seedance-20261004 --resume --stage prepare

# 用新证据前缀实际复测；同名前缀的已完成阶段不会重复提交。
# 资格过期或模式重新开启后，先用真实任务重新取得资格。
$seedanceCheck = Get-Date -Format 'yyyyMMdd_HHmmss'
python scripts/testing/seedance-scheduler-e2e.py --label seedance-20261004 --resume --stage qualify --qualification-label "recheck_$seedanceCheck"
python scripts/testing/seedance-scheduler-e2e.py --label seedance-20261004 --resume --stage matrix --phase-prefix "recheck_$seedanceCheck-"

# 仅查看新增 20 个 mock；旧清理命令的默认范围仍是 legacy + unified。
python scripts/testing/video-mock-lab.py status --lab seedance
# 仅预览清理计划；加 --apply 才会删除这 20 个 mock，保留其数据卷和网关。
python scripts/testing/video-mock-lab.py clean --lab seedance

# 全新隔离实验：标签与端口都必须未被使用。
python scripts/testing/seedance-scheduler-e2e.py --label seedance-fresh-run --port-base 35100 --stage all
```

`all` 依次执行真实资格验证、三套 48 格矩阵、逐上游参考视频计价、输入边界与毛利、优先级与质量、售价快照、模式切换、容量与 429、提交/生成/轮询故障、未知接纳、操作员复核及恢复。测试结束保留服务运行，便于查看界面与审计。参考视频仅使用本地 HTTPS 媒体；`fetch_setting.allow_private_ip=true` 只配置在这个隔离网关中。

统一售价模型在 `off` / `shadow` 模式下应拒绝新提交且不扣费；切回 `on` 会开始新的健康验证轮次。不能把“切回开启”当成“已恢复正常资格”，也不能把验证槽位限制误判成业务容量故障。入口通过真实请求积累至少 20 个完成样本；本实验资格有效期为 1 小时，过期或重新激活后使用新的 `--qualification-label` 运行 `--stage qualify`，再以新的 `--phase-prefix` 复测。失败阶段的原始证据始终保留。

结果位于 `.scratch/unified-video-model-plan/stress/runs/seedance-20261004/results/`。每阶段同时核对返回任务 ID、请求令牌、公开模型、冻结的售价事实、每条消费/退款日志、钱包与令牌余额，以及 mock 接纳事件是否重复；矩阵还核对审计采购成本与独立报价。最终结果须以报告为准，容器启动或请求 HTTP 200 均不代表完成验证。

**2026-10-04 实测结果：30 项要求全部通过，最终选定阶段共 381 次请求、3361 项检查。** 正常矩阵与故障恢复后的矩阵各为 `12 个时长 × 4 档分辨率 × 3 种参考视频输入`，共 288 格。三种输入分别为无参考视频、2.5 秒参考视频，以及 8+12 秒两个参考视频。五种采购类别均实际成为过最优候选并被选中。

| 实测结果 | 数量与核对 |
| --- | --- |
| 成功任务 | 359；公开售价、采购成本、模型映射、钱包及令牌逐项一致 |
| 预期终态失败 | 6；各恰好一笔消费、一笔等额退款，最终扣费为零 |
| 拒绝或未知接纳 | 16；未返回公开任务，未保留扣费；其中 3 次未知接纳均只被一个上游接纳，未转投备用渠道 |
| 资格恢复 | 20 个渠道全部 `normal` 且证据完整；人工恢复没有直接授予正常资格 |
| 最终环境 | 无在途任务，价格/优先级/容量/故障配置恢复基线，3 条未知接纳记录已通过管理 API 复核 |
| 参考媒体 | 未收到网关鉴权凭据；整数、小数、多视频及重复 URL 的计价均已核对 |

功能验证结束时的完整报告已保存在 `functional-before-pressure.json`，其中 `requirements` 全部通过、`complete=true`、`all_checks_passed=true`；另见 `fault-classification.json`、`final-baseline-verification.json`。后续压力阶段会更新 `summary.json`，功能通过不能代替压力通过。`earlier_failed_trials` 保留了四次前期试验：首次校准未完成、误把关闭模式视为可提交、模式切换后尚未重新取得资格，以及误以为全阻断时不会运行受限恢复探测。修正测试前提后，使用 `guards-`、`qualified-`、`recovery2-` 等新前缀复测，未删除失败证据，也未修改生产逻辑去迎合错误预期。

实测运行时为 Docker 29.5.3、PostgreSQL 15.19、Redis 8.10.2；网关由 Go 1.25.1 构建为 Linux amd64，生产源码基线为 `d1147a10e`，精确二进制和前端哈希见 `fixture.json`。后续提交仅增加实验入口、管理范围和文档，不修改数据库或计费实现。上述结果属于功能与记账验证，600 秒持续吞吐另见下节。管理界面登录信息单独保存在本机运行目录的 `admin-login.private.json`，不要提交。

### 对这套混合计价配置压测

```powershell
$pressureStamp = Get-Date -Format 'yyyyMMdd_HHmmss'
python scripts/testing/seedance-scheduler-e2e.py --label seedance-20261004 --resume --stage pressure --phase-prefix "pressure_$pressureStamp-" --ramp-seconds 20 --sustain-seconds 600
```

此入口保留 20 个渠道的差异化能力、采购价格和统一售价，循环提交 144 种时长、分辨率及参考视频组合。先启动新的健康验证轮次，通过真实任务为每个渠道取得至少 20 个成功样本，避免旧资格在测量中到期；不延长资格有效期或直接改健康表。随后测并发 `1/4/16/32/64`，每档提交 20 秒并等待全部任务结算；再从实测的端到端完成吞吐选择有 20% 余量的速率，持续提交 600 秒。选择满足实测最佳完成吞吐 90% 的最低并发，短档提交延迟 p95 须低于 1.5 秒且任务全部成功。保护拒绝超过 10% 或 p95 超过 5 秒时停止升档，保留该档结果。

每 5 秒记录待完成任务、网关 RSS/CPU、Rancher VM CPU/可用内存和公开状态接口；CPU、内存、磁盘保护保持原配置。持续阶段要求完整发出计划请求、无未派发请求、全部成功结算、逐任务选渠和记账一致、预热后积压不持续增长，最终 20 个渠道仍具备真实资格。积压门槛同时要求后 120 秒均值不比 120–240 秒均值增加超过 `max(5, 目标请求/秒)` 个任务，且预热后的线性趋势不超过 `max(0.01, 目标请求/秒 × 0.01)` 个任务/秒。报告区分提交吞吐与计入轮询排空等待的完成吞吐，短时峰值不作为可持续速率。

证据包括 `<前缀>pressure-report.json`、各档 `*-analysis.json`、逐请求 `*-requests.jsonl` 和资源 `*-resources.jsonl`。失败证据不覆盖；先查明原因，再使用新前缀。`--resume` 继续测保留的二进制，不会重新编译网关。生产源码修改后应使用新实验标签和空闲端口运行 `--stage all` 建立当前源码的隔离环境，再对该标签运行 `--resume --stage pressure`。

修正测试客户端后，如网关二进制、价格和实验环境均未变化，可用 `--reuse-ramp-prefix pressure1-` 复用该实验室已完成且对账通过的梯度结果，仅以新前缀重测资格与持续阶段。客户端使用 `perf_counter()` 安排负载。本机 Python 3.10 的 `monotonic()` 精度为 15.625 毫秒；即使换用高精度时钟，操作系统晚唤醒也可能越过截止时刻。持续阶段按 600 秒内的计划时刻计算请求数，不丢弃已计划的尾部请求；允许最多晚派发 1 秒，记录 `last_request_start_seconds`、全部派发延迟和实际耗时，超过限制仍失败。并发梯度保留严格的提交窗口。确定性回归覆盖正常时钟、晚唤醒 15.625 毫秒、超过 1 秒延迟，脚本保存在运行目录的 `test_load_clock.py`。

### 2026-10-04 压力测量记录

所有测量使用上述同一网关二进制、本地模拟上游及原有保护配置，覆盖网关选渠、接纳、轮询和记账；不代表真实供应商生成视频的吞吐。

| 提交并发 | 请求数（全部成功并逐项对账） | 提交延迟 p95 | 完成吞吐，含排空等待 | 短档门槛 |
| --- | --- | --- | --- | --- |
| 1 | 139 | 194 ms | 2.18 个/秒 | 通过 |
| 4 | 498 | 233 ms | 10.72 个/秒 | 通过 |
| 16 | 1119 | 398 ms | 18.26 个/秒 | 通过 |
| 32 | 1228 | 842 ms | 13.88 个/秒 | 通过 |
| 64 | 1284 | 2127 ms | 18.08 个/秒 | 未通过 p95 < 1500 ms |

持续阶段选中并发 16、目标 14.6119 个请求/秒，每轮计划窗口 600 秒、8768 个请求。第一轮 `pressure1-` 实际派发 8767 个，全部成功且对账一致，但有一个尾部请求未派发、6 个渠道旧资格到期，积压比较均值从 277.96 增至 342.96，整体未通过。第二轮 `pressure2-` 已重新取得全部资格，实际派发的 8767 个也全部成功且对账一致，但仍漏发尾部请求；积压比较均值从 274.63 增至 339.79，整体仍未通过。更换高精度时钟不足以修复晚唤醒边界，第三轮同时采用上述有界派发规则。两次积压差值均超过原定约 14.61 个任务的容差，不能因线性趋势较小或最终排空而抹去失败结论；不据此断言积压无限增长，也不把首次资格到期当成未经验证的积压原因。

第三轮实际命令：

```powershell
python -B scripts/testing/seedance-scheduler-e2e.py --label seedance-20261004 --resume --stage pressure --phase-prefix pressure3- --reuse-ramp-prefix pressure1- --ramp-seconds 20 --sustain-seconds 600
```

**最终 `pressure3-` 复测通过全部检查，8768 个请求全部成功。** 最后请求在第 599.992 秒派发，未派发数为零；派发延迟 p95 为 15.27 ms、最大 363.61 ms，均在门槛内。提交吞吐为 14.61 个/秒，计入排空等待后的完成吞吐为 14.32 个/秒。提交延迟 p50/p95/p99 为 137/181/214 ms；任务从提交到完成 p50/p95/p99 为 22/47/51 秒，最长 59 秒。积压比较均值从 344.17 降至 336.79，预热后趋势为 -0.153 个任务/秒；最终无在途任务，20 个渠道均为 `normal` 且资格证据完整。

本轮网关峰值 RSS 为 151.56 MiB，Rancher VM 最少可用内存为 4341.09 MiB、峰值 CPU 为 23.86%；网关进程峰值 CPU 为 162.23%（100% 表示一个逻辑核）。公开状态接口采样全部成功，无资源保护拒绝；全部返回任务、上游接纳、采购选渠、冻结售价、消费日志、钱包和令牌均逐项一致。五档压力与三轮持续负载累计实际派发 **30570** 个请求，均成功并通过各自的业务对账；前两轮的持续稳定性失败仍有效，最后一轮通过不代表所有压力档位或更长时间负载都已验证。两次重新取得资格各额外提交 400 个真实成功任务，不计入该压力请求总数。

结论依据为 `pressure3-pressure-report.json` 与 `pressure3-pressure-sustain-analysis.json`；原始前两轮报告和请求、资源、对账证据保留在相同目录。测试入口修复由确定性时钟回归、第三轮真实 600 秒负载、Python 语法检查及文档 PowerShell 解析验证。本次未修改生产调度、计费或数据库实现。

### 压测收尾与下次恢复

最终复测共通过 **61413 项检查**。代码与压测报告已提交为 `cc6efa9d750a5f49b8d7cd00097ec1cd1bdaec8a`，[CI 检查及镜像发布成功](https://github.com/jiayi-1994/new-api/actions/runs/37167479881)。GHCR `ghcr.io/jiayi-1994/new-api` 与 Docker Hub `xjy94/new-api` 的 `feature-v1.1.0-cc6efa9d7` 标签均对应此提交，镜像摘要一致：`sha256:f0c56393f647544f006b5ec25523a4de5e35f6a19fa6563d8a69d17cd1042360`。这是压测入口的发布版本，实验网关仍使用上文记录的冻结二进制。

日常停用只停止本次 `seedance` 实验室的 `mock01`–`mock20`，保留容器、镜像、卷、配置和原始测试证据。网关、数据库、Redis 与参考媒体服务继续保留。停止前须确认没有在途任务；`clean --apply` 会删除容器，不用于这种保留现场的停用。

**2026-10-04 已完成停用：20 个 mock 均为 `exited`，没有删除容器。** 停止前在途任务为零；停止后核对全部 156 个容器仍存在，非目标容器的 ID 与状态未变，数据卷、镜像和网络清单完全一致。核对记录保存在本机运行目录的 `mock-stop-verification.json`，六项检查全部通过；恢复命令已完成无副作用预览。

```powershell
# 在确认当前实验没有在途任务后，只停止这 20 个上游。
$seedanceCompose = Join-Path (Get-Location) '.scratch/unified-video-model-plan/stress/runs/seedance-20261004/compose.private.json'
$seedanceMocks = 1..20 | ForEach-Object { 'mock{0:D2}' -f $_ }
docker compose -p codex-uvm-stress-seedance-20261004-f1567e0d -f $seedanceCompose stop --timeout 10 @seedanceMocks
python scripts/testing/video-mock-lab.py status --lab seedance

# 下次测试：先预览，再恢复所保留的模拟器，不拉镜像、不构建、不提交任务。
python scripts/testing/video-mock-lab.py start --lab seedance
python scripts/testing/video-mock-lab.py start --lab seedance --apply
python scripts/testing/video-mock-lab.py status --lab seedance
foreach ($mockPort in 35010..35029) {
    Invoke-RestMethod "http://127.0.0.1:$mockPort/health"
    Invoke-RestMethod "http://127.0.0.1:$mockPort/_config"
}

# 容器恢复不等于调度资格仍有效；先重新积累真实样本，再使用新证据前缀。
$resumeStamp = Get-Date -Format 'yyyyMMdd_HHmmss'
python scripts/testing/seedance-scheduler-e2e.py --label seedance-20261004 --resume --stage qualify --qualification-label "resume_$resumeStamp" --phase-prefix "resume_$resumeStamp-"
python scripts/testing/seedance-scheduler-e2e.py --label seedance-20261004 --resume --stage matrix --phase-prefix "resume_$resumeStamp-"
```

若网关或参考媒体也被另行停用，先运行前文的 `--resume --stage prepare` 恢复完整环境。恢复后核对 `/health` 与 `/_config`，确认未遗留故障注入或 `hold_ack`；保留的历史健康状态不能替代重新取得资格。复测当前保留版本可沿用本标签；验证修改后的生产代码须用新标签和空闲端口建立隔离实验。

## 1. 查看、清理和恢复

```powershell
# 查看实时容器；不输出环境变量或密钥。
python scripts/testing/video-mock-lab.py status

# 预览后执行清理，再核对结果。
python scripts/testing/video-mock-lab.py clean
python scripts/testing/video-mock-lab.py clean --apply
python scripts/testing/video-mock-lab.py status
```

`clean` / `start` 默认只预览，`--apply` 才执行；默认处理两套实验室。脚本按确切容器 ID 操作，逐个复核项目/服务标签，保留镜像、卷、网络，并检查非目标服务的 ID 和状态不变。Docker 不可达时停止执行，不改用其他 Docker context 继续删除。

若仅 Windows Docker 连接转发故障，而 Rancher 自有发行版内的 Docker 正常，可显式使用下面的连接方式。先确认返回的是同一台 Rancher 引擎及上述实验室；这不会切换 Docker context，也不会重启其他 WSL 发行版。

```powershell
wsl --distribution rancher-desktop --exec docker info --format '{{.Name}} {{.ID}}'
python scripts/testing/video-mock-lab.py status --rancher-wsl
python scripts/testing/video-mock-lab.py clean --rancher-wsl
python scripts/testing/video-mock-lab.py clean --rancher-wsl --apply
python scripts/testing/video-mock-lab.py status --rancher-wsl
```

`--rancher-wsl` 仅支持 `status` / `clean`。`start` 使用 Windows Compose 路径，必须先恢复 Windows Docker 接口，再使用正常启动命令；脚本会拒绝 `start --rancher-wsl`。本次扩展已通过 8 组模拟检查，覆盖连接方式、预览、启动拒绝、标签复核、第二项目读取失败时零删除等行为，并通过真实清理前后清单核对。

| 实验室 | Compose 项目 | 允许操作 | 必须保留 |
| --- | --- | --- | --- |
| `legacy` | `codex-vsched-analysis-20261002` | `mock01`–`mock20`、`multi01`–`multi20`、`inputmedia`，41 个服务 | `app/db/cache`；`database/gateway_data` 卷；宿主媒体与 TLS 目录 |
| `unified` | `codex-unified-video-20261002` | `mock01`–`mock20`，20 个服务 | `app/postgres/mysql/cache`；数据库、网关及全部 `mockNN_data` 卷 |
| `seedance`（须显式选择） | `codex-uvm-stress-seedance-20261004-f1567e0d` | `mock01`–`mock20`，20 个服务 | `app/db/cache`、独立媒体容器及全部数据卷 |

实际容器数量以 `status` 为准。保留 `33800` / `33960` 网关；`33880` / `33881` 独立预览及 Windows 原生进程不在清理范围。不要使用 `compose down`、`prune`、`-v/--volumes`、`--remove-orphans`，不要删除宿主实验目录。

最小恢复两个统一模拟器，足够进行跨插件选路演示：

```powershell
python scripts/testing/video-mock-lab.py start --lab unified --services mock01 mock02
python scripts/testing/video-mock-lab.py start --lab unified --services mock01 mock02 --apply
python scripts/testing/video-mock-lab.py status --lab unified
Invoke-RestMethod http://127.0.0.1:33970/health
Invoke-RestMethod http://127.0.0.1:33971/health
Invoke-RestMethod http://127.0.0.1:33970/_config
Invoke-RestMethod http://127.0.0.1:33971/_config
```

`mock01` / `mock02` 分别模拟 `seedance-hjmie` / `megabyai`。五插件演示选择 `mock01 mock02 mock03 mock04 mock05`；统一全池用 `start --lab unified --apply`；两套全池用 `start --apply`。启动禁止拉取/构建镜像和启动依赖，也不提交视频任务；镜像缺失时应先解决镜像问题。

**`start` 成功只证明容器 running，还需检查 HTTP 与故障配置。** 统一 mock 保卷恢复后会保留历史配置、任务及 `hold_ack`，不会自动变回健康状态。旧 mock/multi 的任务和配置仅在内存中，停止进程即丢失；清理前应确认在途任务并保存事件证据。演示结束只清理所选服务：

```powershell
python scripts/testing/video-mock-lab.py clean --lab unified --services mock01 mock02 --apply
```

## 2. 用当前代码做最小自动回归

恢复容器不会更新旧网关。验证当前工作区优先使用现有 Windows 原生程序：自动编译当前网关/mock，创建独立端口和新 PostgreSQL 数据库，结束后关闭本次进程并保留证据。无需恢复 Rancher 的 mock，也不修改旧网关配置。

前提：Go、Bun、Python 可用；Python 能加载 `requests/psutil` 和本机 `.scratch/feature-v110-review/python/` 中的 `psycopg2`；保留的 PostgreSQL `127.0.0.1:33962` 可连接；`.scratch/unified-video-model-plan/lab/private-credentials.json` 存在。目标端口须空闲，结束其他编译/压测，保持 CPU/磁盘保护开启。

先完成代码修改，将本次终端的临时目录放在仓库磁盘，再构建当前前端并生成唯一结果标签。本机曾因系统盘空间不足导致构建失败；原生程序会设置子进程的 `TEMP/TMP`，编译前仍需设置下面三项：

```powershell
$testTemp = Join-Path (Get-Location) '.scratch/video-mock-maintenance/tmp'
New-Item -ItemType Directory -Path $testTemp -Force | Out-Null
$env:TEMP = (Resolve-Path -LiteralPath $testTemp).Path
$env:TMP = $env:TEMP
$env:GOTMPDIR = $env:TEMP
Push-Location web
try {
    bun run build
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }
} finally {
    Pop-Location
}
$runStamp = Get-Date -Format 'yyyyMMdd_HHmmss'

python .scratch/feature-v110-review/run_native.py --stage p1 --backend memory --port-base 34100 --label "quick_p1_$runStamp"
```

该入口启动一个网关和 20 个本地 mock，对 3 个渠道通过真实请求积累资格，验证容量限制与备用选路、429 冷却、明确失败后切换、全阻断零留存扣费、真实探测恢复，以及逐任务/令牌/钱包对账和无重复接纳。首次校准约需 65 秒，随后还要等待真实资格与终态，通常需数分钟；不含 600 秒吞吐阶段。不得通过改健康表或伪造样本缩短等待。

根据改动范围追加以下入口，按顺序运行：

| 范围 | 命令 |
| --- | --- |
| 视频故障期间的聊天、流式、嵌入、图片及公开读取 | `python .scratch/feature-v110-review/service_e2e.py --port-base 34300 --label "quick_service_$runStamp" --run` |
| 共享容量、配置变更、未知接纳、校准与释放 | `python .scratch/feature-v110-review/capacity_e2e.py --backend redis --port-base 34400 --label "quick_capacity_$runStamp" --run` |
| 操作员恢复与真实探测 | `python .scratch/feature-v110-review/recovery_e2e.py --backend redis --port-base 34600 --label "quick_recovery_$runStamp" --run` |

Redis 场景还需 Python `redis` 包和已有 `.scratch/feature-v110-review/redis/` 运行时，使用独立 Redis 进程。独立探针必须传 `--run` 才执行；`run_native.py` 直接执行。每次使用新标签，不覆盖失败证据或盲目重放未完成阶段。

## 3. 看哪些证据才算通过

结果在 `.scratch/unified-video-model-plan/stress/runs/<label>/`：`fixture.json` 记录源码/二进制/前端哈希和进程生命周期；`results/summary.json` 与各阶段 `*-requests.*`、`*-evidence.json` 保存请求和对账；独立探针分别写 `service-availability.json`、`capacity-ownership.json`、`manual-recovery.json`；网关日志、mock 日志与持久事件保留在运行目录。

判定报告按入口选择：`run_native.py` 看 `summary.json` 的 `complete` 与 `all_checks_passed`；服务/容量探针分别看独立报告的 `complete` 与全部检查，不要求其辅助 `summary.json` 的完成标志；恢复探针同时检查 `manual-recovery.json` 和 `summary.json`。上述标志和检查均应为真，程序正常退出，阶段已接纳任务均到终态，逐任务/令牌/钱包一致且无重复接纳。原生入口还应确认本次进程已停止；Seedance Docker 入口则刻意保留容器运行，供后续查看。只看提交 HTTP 200、总余额或编译通过均不够；故障场景允许明确预期的非 200 响应。

遇到资源保护拒绝，保留报告，解决负载/空间后用新标签重跑；不要关闭保护。`video_health_unavailable` 先检查首次校准和任务轮询；未知接纳先核对 mock 事件与健康尝试，不能直接重发同一请求。新建数据库会保留用于复核，删除数据库是单独的数据清理操作。

需要完整性能复测时，在独占窗口运行：

```powershell
python .scratch/feature-v110-review/run_native.py --stage all --backend redis --port-base 34100 --ramp-seconds 12 --sustain-seconds 600 --label "full_video_$runStamp"
```

此入口含 20 渠道资格、并发梯度、600 秒持续阶段、负载对照和成本/质量/优先级/利润/故障场景，会产生大量请求。`--stage remaining` 也含压力阶段。必须区分提交吞吐与完成吞吐；有积压、未终态或保护拒绝时，不能宣称持续压力通过。

## 4. 资料与高级入口

| 用途 | 本机位置与注意事项 |
| --- | --- |
| 旧 / 统一 Compose | `.scratch/video-load-lab/compose.json`、`.scratch/unified-video-model-plan/lab/compose.json`；含私有环境变量，不输出完整 `compose config` |
| 最初场景与环境说明 | `.scratch/video-load-lab/SCENARIOS.md`、`.scratch/unified-video-model-plan/lab/README.md`；属于历史实验配置 |
| 所有新回归证据 | `.scratch/unified-video-model-plan/stress/runs/`；以每次源码/二进制哈希为准，不用旧结果证明新代码 |
| 售卖规格与分组计费演示 | `.scratch/seedance-sales-preview/duration-demo/run_demo.py`、`group-privacy-demo/run_demo.py`；绑定 `33880` 预览，先核对价格/渠道；分组脚本会建用户/令牌并修改测试分组 |
| 已有任务隐私复核 | `.scratch/seedance-sales-preview/group-privacy-demo/verify_privacy.py`；除正常登录刷新外只读，不代替新提交验证 |
| 清理记录 | 2026-10-04 成功核对：`.scratch/video-mock-maintenance/cleanup-verification-20261004.json`；2026-10-03 首次失败：同目录 `cleanup-attempt.json`；只保留本地，不公开私有配置/事件 |

不要把 `create_lab.py`、`lab.py setup`、`setup_multi.py`、`prepare_*.py` 当作容器恢复步骤，它们会生成或修改环境。尤其旧 `create_lab.py` 会直接覆写现有 Compose。`experiments.py`、`throughput.py`、`steady_backlog.py`、`run_remaining.py`、`recover_multi.py` 等会发起批量任务或改实验配置；名字含“恢复”不代表无副作用。`lab/run_tests.py` 使用共享测试数据库，也不是容器恢复命令。

若管理脚本不可用，确认项目/服务归属后可使用以下 Compose 备用命令；仅示例两个统一模拟器，其余服务仍必须逐一明确选择：

```powershell
$uvCompose = Join-Path (Get-Location) '.scratch/unified-video-model-plan/lab/compose.json'
$uvArgs = @('compose', '-p', 'codex-unified-video-20261002', '-f', $uvCompose, '--profile', 'full-pool')
docker @uvArgs up -d --no-deps --pull never --no-build mock01 mock02
docker @uvArgs rm --stop --force mock01 mock02
```

官方参数参考：[Compose up](https://docs.docker.com/reference/cli/docker/compose/up/)、[Compose rm](https://docs.docker.com/reference/cli/docker/compose/rm/)、[profiles](https://docs.docker.com/compose/how-tos/profiles/)。对外只分享必要的脱敏检查结果，不附带私有凭据、Redis 私有配置或完整 Compose。
