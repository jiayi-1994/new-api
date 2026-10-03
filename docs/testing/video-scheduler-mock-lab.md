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

以 `summary.json` 的 `requirements`、`complete=true`、`all_checks_passed=true`，以及 `fault-classification.json`、`final-baseline-verification.json` 为本轮结论。`earlier_failed_trials` 保留了四次前期试验：首次校准未完成、误把关闭模式视为可提交、模式切换后尚未重新取得资格，以及误以为全阻断时不会运行受限恢复探测。修正测试前提后，使用 `guards-`、`qualified-`、`recovery2-` 等新前缀复测，未删除失败证据，也未修改生产逻辑去迎合错误预期。

实测运行时为 Docker 29.5.3、PostgreSQL 15.19、Redis 8.10.2；网关由 Go 1.25.1 构建为 Linux amd64，生产源码基线为 `d1147a10e`，精确二进制和前端哈希见 `fixture.json`。本轮提交仅增加实验入口、管理范围和文档，不修改数据库或计费实现。本轮属于功能与记账验证；600 秒持续吞吐使用下文的独立性能入口。管理界面登录信息单独保存在本机运行目录的 `admin-login.private.json`，不要提交。

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
