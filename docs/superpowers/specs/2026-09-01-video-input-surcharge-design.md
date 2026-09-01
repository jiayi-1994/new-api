# 输入参考视频按秒附加费（Video Input Surcharge）设计与实施计划

## 摘要

为视频生成任务增加"输入参考视频按秒附加费"：管理员在页面上按**模型 × 输出分辨率**
配置每秒单价（如 720p +0.3 元/秒、1080p +1.05 元/秒）。提交请求时，网关探测每个
参考视频（`video_url`）的实际时长，按 `Σ round(秒数，最少 1 秒) × 单价` 计入预扣
与结算。该附加费是**加法项**，叠加在现有 `VideoResolutionPrice` 按秒分辨率计费
之上，不改变现有乘法倍率体系。

首期接入 **sora adaptor**（OpenAI /v1/videos 格式）——这是直连二道贩子上游
（megabyai / zz1cc 等挂 seedance-2-5 类模型）的通道；`VideoBillingSelection` 上的
字段设计为通用，doubao 等其他 adaptor 之后按需增量接入。

## 目标

- 附加费单价按模型页面可配置（复用现有模型名通配匹配语义）。
- 输入视频秒数由网关自己探测（MP4/MOV 的 `moov/mvhd`），不信任客户端申报。
- 预扣（pre-consume）与结算（settle）两端使用同一份快照，金额一致。
- **配置了附加费的模型，若请求含参考视频但时长解析失败 ⇒ 返回 400，绝不提交上游。**
- 未配置附加费的模型行为零变化（不发起任何探测请求）。
- 遵守 AGENTS.md 计费不变量：用户可控乘数有上界、`QuotaFromFloatChecked` 转换、
  clamp 审计挂到 `admin_info.quota_saturation`。

## 非目标

- 不做 doubao/ali/vidu 等其他 adaptor 的接入（字段通用，后续增量）。
- sora 的 remix 路径不加收附加费（沿用 origin 任务快照，行为不变）。
- 不支持 MP4/MOV（ISO-BMFF）以外的容器（webm 等解析失败 → 按失败语义拒单）。
- 不改 `EstimateBilling` 的跨渠道预估展示（它只返回倍率 map，附加费不在其中）。
- 不把附加费写进 `TaskBillingPlan` 冻结（快照落在 `TaskBillingContext`，见下）。
- 不改用户侧 pricing 公示页。

## 一、配置模型

新增 option `VideoInputSecondPrice`，JSON 形如：

```json
{
  "seedance-2-5": { "720p": 0.3, "1080p": 1.05 },
  "seedance-2.0-fast": { "480p": 0.2, "720p": 0.4 }
}
```

外层键是**计费用的 origin 模型名**（sora 通道挂的二道贩子模型名直接照填；后续
doubao 直连通道也用同一份配置），内层键是 canonical 输出分辨率——形状、解析与
校验与 `VideoResolutionPrice` 完全一致（复用同一解析器）。该分辨率未配置 = 该档
不收附加费、不探测。

- 值为**每秒单价**，单位与 `VideoResolutionPrice` 一致（同一货币基准），
  必须为有限正数；0 或缺失 = 未启用。
- 后端新文件 `setting/ratio_setting/video_input_second_price.go`，
  照抄 `video_resolution_price.go` 的骨架裁剪：
  `parse/Validate/UpdateByJSONString/2JSONString/GetVideoInputSecondPrice(model)`，
  模型名匹配复用与 `matchingVideoResolutionPricesLocked` 相同的通配语义。
- 注册进 option 读写链：`model/option.go`（加载/保存分支）、
  `controller/option.go`（校验分支）。参照 `VideoResolutionPriceOptionKey` 的接线方式。

## 二、时长探测（service/video_probe.go）

`func ProbeVideoDurationSeconds(ctx context.Context, rawURL string) (int, error)`

- **只接受 http/https** URL；其他 scheme（data:、file: 等）直接返回错误。
- **必须用 SSRF 防护客户端**：`service.GetSSRFProtectedHTTPClient()` +
  `ValidateSSRFProtectedFetchURL`（这是用户提交的任意 URL，属于信任边界）。
- 抓取策略（全程有界读，不落盘）：
  1. `Range: bytes=0-2097151` 拉头部 2 MiB，流式扫 ISO-BMFF box；
     命中 `moov` → 解析 `mvhd`（timescale + duration，注意 version 0/1 两种布局）。
  2. 头部没有 `moov` 且响应含 `Content-Range` 总长：再 Range 拉**尾部 2 MiB** 扫一次
     （moov-at-end 的常见情况）。
  3. 服务器不支持 Range（返回 200 全量）：用 `io.LimitReader` 只读前 2 MiB 即关闭，
     等价于步骤 1，且没有尾部回退（直接失败）。
- 上限与超时：单次请求超时 10s；每个 URL 最多 2 次请求；解析出的秒数
  `ceil` 取整后必须落在 `(0, MaxInputReferenceVideoSeconds]`，否则报错。
- 新常量 `relaycommon.MaxInputReferenceVideoSeconds = 300`（单个视频上限），
  与 `MaxTaskDurationSeconds` 放在一起；多个视频求和后再校验一次总和 ≤ 900。
  超界一律拒单，不做静默 clamp（拒单比多收安全）。
- 解析器本体 `parseISOBMFFDuration(r io.Reader) (float64, error)` 独立成纯函数，
  方便用手工构造的 box 字节做表驱动测试，不依赖网络。

## 三、计费管道改动（加法项贯穿预扣与结算）

### 3.1 Selection 与校验（relay/common/video_billing.go）

`VideoBillingSelection` 增加两个字段：

```go
InputVideoSeconds        int     // Σ ceil(每个参考视频秒数)，探测所得
InputVideoPricePerSecond float64 // 提交时刻的附加费单价快照
```

`NewResolvedVideoBilling` 增加校验：seconds ∈ [0, 900]；price 为有限非负；
两者必须同零或同正（有秒数没单价、有单价没秒数都是 bug，直接报错）。

### 3.2 金额公式（CalculateVideoResolutionQuotaAtUnit）

签名追加 `inputSeconds int, inputPricePerSecond float64` 两参（调用点少，直接改签名，
不加变体函数）：

```
quotaValue = [ resolutionPrice × duration × ∏independentRatios
             + inputPricePerSecond × inputSeconds ] × groupRatio × quotaPerUnit
quota, clamp = rootcommon.QuotaFromFloatChecked(quotaValue)   // 保持单点转换
```

**附加费不乘 independentRatios**（`video_input` 档位倍率只作用于基础按秒价），
**乘 groupRatio**（分组折扣对全单生效）。在函数注释里写死这条语义。

### 3.3 快照（model/task.go TaskBillingContext）

新增：

```go
InputVideoSeconds        int     `json:"input_video_seconds,omitempty"`
InputVideoPricePerSecond float64 `json:"input_video_price_per_second,omitempty"`
```

- `controller/relay.go`（~692 构造 BillingContext 处）从 `validated.Selection` 抄入。
- `service/task_billing.go` `CalculateVideoResolutionSnapshotQuota` 把这两个字段
  透传给新版 `CalculateVideoResolutionQuotaAtUnit`。
- 结算语义：`settleTaskBillingOnComplete` 用上游实际输出时长重算时，
  **输入秒数取快照常量不变**——上游不会重报输入时长，探测值就是结算值。
- 审计：`service/task_billing.go:65` 附近的 other map 加
  `input_video_seconds` / `input_video_price_per_second`；
  `controller/task.go` BillingDetails（~123）同步透出，管理端日志可见。
  clamp 路径不用动——公式仍走 `QuotaFromFloatChecked`，现有
  `attachQuotaSaturation` 链路自动覆盖。

### 3.4 冻结校验（relay/relay_task.go ValidateFrozenResolutionBilling）

不把附加费单价纳入 `TaskBillingPlan` 冻结（它不参与渠道路由决策）；
`NewResolvedVideoBilling` 的字段校验即覆盖。管理员在请求进行中改价的竞态
以提交时刻快照为准，双端一致，无 TOCTOU 风险。

## 四、sora adaptor 接入（relay/channel/task/sora/adaptor.go）

### 4.1 参考视频从哪来

sora 通道的参考视频有两条入口，附加费两条都要覆盖：

1. **JSON body（主路径）**：客户端在顶层或 `metadata` 里用
   `referenceVideos` / `reference_videos` / `videos` / `video` 传 URL（即
   `megabyaiReferenceURLs` 现有 video 键组，adaptor.go:684）。
   `input_reference` 官方语义是图片，**不算**视频键。
   把该收集逻辑抽成 `collectReferenceVideoURLs(bodyMap) []string`，
   计费端与 `buildMegabyaiVideoPayload` 共用同一个函数——沿用
   "计费与载荷不分叉"原则（adaptor.go:746 注释）。非 megabyai 的透传上游
   body 原样转发，计费同样按这份别名表收集。
2. **multipart 文件（次路径）**：`ParseMultipartFormReusable` 后文件字节已在本地，
   对 Content-Type 为 `video/*`（或 sniff 出视频）的文件部分**直接就地解析**
   `mvhd`，不需要任何下载。

### 4.2 接线点

`ResolveVideoBilling` 非 remix 分支，`normalizeSoraVideoRequest` 成功之后：

```go
price, ok := ratio_setting.GetVideoInputSecondPrice(info.OriginModelName) // 未配置或<=0 → 跳过
if ok && price > 0 {
    // JSON：common.GetBodyStorage(c) 取原始 body → Unmarshal → collectReferenceVideoURLs
    // multipart：遍历 formData.File 中的 video/* 部分
    total := 0
    for _, u := range urls {
        sec, err := probeVideoDuration(ctx, u)   // 包级 var，测试可替换
        if err != nil { return 400 }             // 不提交上游
        total += sec
    }
    for _, fh := range videoFileParts {
        sec, err := parseISOBMFFDuration(有界 reader)  // 本地解析，失败同样 400
        ...
    }
    if total > 0 {   // 总和上界 ≤900，超界 400
        normalized.Selection.InputVideoSeconds = total
        normalized.Selection.InputVideoPricePerSecond = price
    }
}
c.Set(normalizedSoraVideoRequestKey, normalized)   // 现有缓存，保证每请求只探测一次
```

- `var probeVideoDuration = service.ProbeVideoDurationSeconds` 作为测试注入点。
- 失败路径复用现有 TaskError 包装，用可辨识错误码
  `video_input_duration_unresolved`（HTTP 400），错误信息带上失败的 URL/文件名。
- **remix 分支不动**：origin 快照沿用，不重复加收。
- 模型名用 `info.OriginModelName`（计费一律以 origin 名为准，与
  `VideoResolutionPrice` 匹配语义一致）。
- 未配置附加费 ⇒ 一行都不执行，现网行为与延迟零变化。

### 4.3 后续（不在首期）

doubao adaptor 用同一套字段接入：在 `resolveVideoRequest` 里收集
`content[].video_url` 后走同一个探测器。

## 五、前端

- `web/src/features/system-settings/`：在计费/倍率设置区（`billing/section-registry.tsx`
  + `models/ratio-settings-card.tsx` 一带，参照 `VideoResolutionPrice` 的接线）
  新增 `VideoInputSecondPrice` 的 JSON 编辑项 + 保存；`types.ts` 补类型。
- `web/src/features/models/components/drawers/model-mutate-drawer.tsx`：
  模型编辑抽屉里在视频分辨率价格旁加一个可选数字输入
  "输入参考视频每秒价格"，读写同一份 JSON。
- i18n：`web/src/i18n/locales/en.json` + `zh.json` 加 key（英文原文为 key），
  其余语言 `bun run i18n:sync`。

## 六、失败语义汇总

| 场景 | 行为 |
|---|---|
| 模型未配置附加费 | 不探测，行为不变 |
| 配置了附加费，请求无参考视频 | 不探测，只有基础计费 |
| 配置了附加费，探测全部成功 | 预扣 = 基础 + Σ秒×单价 |
| 配置了附加费，任一 URL 探测失败/超界/非 http(s)/容器不支持 | 400 `video_input_duration_unresolved`，**不提交上游**，不扣费 |
| 结算时上游输出时长变化 | 只重算基础部分；附加费按提交快照不变 |

## 七、测试清单（testify，表驱动）

1. `setting/ratio_setting/video_input_second_price_test.go`：
   parse/validate/round-trip、非法值（负数、NaN 字符串、坏 JSON）拒绝、通配匹配。
2. `service/video_probe_test.go`：
   - `parseISOBMFFDuration`：moov 在头部 / version1 mvhd / 无 moov / 截断 → 精确值或错误；
   - `httptest.Server` 场景：支持 Range（头部命中）、moov-at-end（尾部回退）、
     不支持 Range 的 200 全量（有界读）、超时、非 2xx。
3. `relay/common/video_billing_test.go`：
   新公式金额精确断言；附加费不乘 independentRatios、乘 groupRatio；
   字段校验（同零同正、上界、NaN/Inf 拒绝）；超大输入触发 clamp 且返回 QuotaClamp。
4. `relay/channel/task/sora/` 测试（video_billing_test.go / adaptor_test.go）：
   - JSON body 各别名键（顶层 + metadata）收集正确，与 megabyai 载荷共用同一收集器；
   - 配置了单价 + 有视频 URL + 探测成功 → selection 两字段正确、缓存生效只探测一次；
   - 配置了单价 + 探测失败 → TaskError 400（不产出上游请求）；
   - multipart video/* 文件本地解析成功/失败两分支；
   - 未配置单价 → 不调用探测（注入 spy 断言零调用）；
   - remix 分支不受影响。
5. `service/task_billing_test.go`：
   快照含附加费时 `CalculateVideoResolutionSnapshotQuota` 金额正确；
   settle 阶段输出时长变化时附加费保持提交值。

## 八、实施顺序

1. `relaycommon` 常量 + `VideoBillingSelection` 字段 + 公式改造 +（3）的测试。
2. `ratio_setting` 新 option + option 接线 +（1）的测试。
3. `service/video_probe.go` + （2）的测试。
4. sora adaptor 接入（含收集器抽取）+ snapshot/controller/结算透传 +（4）（5）的测试。
5. 前端设置项 + i18n。
6. 验证：`make test-backend`；前端 `bun run build` + 相关 vitest；
   `cd relaykit && GOWORK=off go build ./...`（本改动不应触碰 relaykit，跑一次确认）。

## 备注

- 上游（火山）对含视频输入本身有 ¥28/¥46 档位差，已由 `video_input`
  IndependentRatio 表达；本附加费是站长在此之上的加法加价，两者独立叠加。
- megaapi 这类回包带 `billing_amount` 的二道贩子上游不适用本方案（它们不暴露
  usage/时长）；那条链路如需成本跟随，另行走 `AdjustBillingOnComplete`，不在本计划内。
