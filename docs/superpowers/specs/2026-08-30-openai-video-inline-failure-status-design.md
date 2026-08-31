# OpenAI 视频内联失败状态兼容设计

## 背景

部分 OpenAI 兼容视频上游把任务状态和失败原因合并在 `status` 字段中，例如：

```json
{"status":"FAILED: For 肖像保护, Dreamina Seedance 2.5 只支持生成包含您自己的视频. 请换一张参考图, or create a video from text。"}
```

当前 Sora/OpenAI 视频适配器只识别完整等于 `failed`、`cancelled` 或 `canceled` 的状态。上述响应能够正常反序列化，但无法匹配状态枚举，随后被公共轮询逻辑改写为 `upstream returned unrecognized message`，导致真实失败原因丢失。

## 目标

- 兼容“已知失败状态 + 冒号 + 原因”的上游格式。
- 将任务正确标记为失败，并原样保留冒号后的失败原因。
- 保持现有标准状态映射、进度处理、退款流程及 `error.message` 优先级不变。
- 将改动限制在 Sora/OpenAI 视频适配器，不改变其他渠道或公共轮询器的状态语义。

## 方案比较

1. **适配器内补充失败状态前缀（采用）**：保留现有完整状态匹配；仅在未匹配时按第一个 ASCII 冒号拆分原始 `status`，并且只接受 `FAILED`、`CANCELLED` 和 `CANCELED` 前缀。失败时依次选择 `error.message`、内联原因、默认文案。范围小，也不会放宽成功或处理中状态的语义。
2. **公共轮询器兜底**：所有适配器返回空状态时解析 `status` 前缀。影响面过大，可能把其他渠道的未知状态误判为终态。
3. **只硬编码 `FAILED:`**：改动最少，但重复了已有状态映射，且同类取消状态仍会丢失原因。

## 数据流与行为

`TaskAdaptor.ParseTaskResult` 先完整执行现有的 `TrimSpace`、不区分大小写匹配。只有完整状态未匹配时，才按首个 `:` 分成状态词和可选详情；状态词同样做空白和大小写归一化，并且只有已知的失败/取消状态能够产生失败结果。其他未知前缀仍返回空状态，维持现有公共轮询兜底行为。

失败原因优先级保持为：

1. 响应中的 `error.message`；
2. `status` 冒号后的非空原文；
3. 现有默认值 `task failed`。

因此，规范响应不会改变；异常内联格式会从“空状态 → 通用错误”转为“失败状态 → 上游原始原因”，后续仍由现有轮询代码完成进度更新、持久化与退款。

## 测试

在 `relay/channel/task/sora/adaptor_test.go` 添加真实响应回归测试，验证：

- `FAILED: <reason>` 映射为 `TaskStatusFailure`；
- `Reason` 精确等于冒号后的原文，中文和英文大小写不被改写；
- 同时存在 `error.message` 时仍优先使用结构化错误；
- 未知的 `PAUSED: <reason>` 等前缀仍不被适配器识别；
- 现有状态映射测试继续全部通过。

验证范围包括 Sora 适配器定向测试、相关 relay/task 测试以及根模块构建或完整 Go 测试中与改动相称的检查。
