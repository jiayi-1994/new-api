# Paipu Video Task Plugin

用于支持 **Task Plugin API v1** 的官方 New API，安装文件为 [plugin.js](./plugin.js)。客户端通过自己的 New API 创建任务，插件向 `https://api.paipu.net` 提交并查询视频。

**2.2.0 已按价格页补充 39 个视频型号，用 `const MODELS = { 模型名: [分辨率] }` 管理。** 这些型号直接在渠道中添加真实名称即可，自动获得各自的视频计费字段；价格和计费方式仍由管理员配置。后续新型号也可以继续映射到通用规格模板，无需每次更新插件。

本次核对价格页全部 50 个条目：39 个已接入视频型号、8 个图片型号、1 个聊天型号，以及 2 个特殊/资料不足的待适配条目。完整清单与原因见 [MODEL_CATALOG.md](./MODEL_CATALOG.md)，核对日期为 2026-09-28。

## 在定价页选择计费方式

已明确分辨率的模型有「视频按秒单价」和「视频按条单价」两列，按该型号实际支持的分辨率分行。填哪一列就是哪种模式：

| 想要的模式 | 定价页填法 |
| --- | --- |
| 按分辨率按秒 | 每档分辨率填「视频按秒单价」，「视频按条单价」留 0 |
| 统一按条 | 每档分辨率填相同的「视频按条单价」，「视频按秒单价」留 0 |
| 按分辨率按条 | 每档分辨率分别填「视频按条单价」，「视频按秒单价」留 0 |

费用 = 按秒单价 × 秒数 + 按条单价 × 1，再乘分组倍率。两列都填非零会相加，所以不用的一列保持 0。模式随时可改：改价格保存即可，在途任务按提交时冻结的价格结算。

`lec-ac-seedance-2-5-10-image`、`lec-seed-2-0-900`、`lec-seed-2-5-900` 的官方资料没有公布分辨率，`MODELS` 中使用 `[]`。它们只展示秒价和条价，不虚构分辨率计费行；其中固定时长分别为 30、15、30 秒。

## 规格模板（只描述上游接口，不决定价格）

**仅插件尚未声明的新型号需要映射。** 模板用于向宿主提供计费用量定义，不替你配置价格。已声明型号直接调用，或将自定义别名映射到真实型号。

| 映射目标 | 适用模型 | 上游 `resolution` | 定价页分辨率行 |
| --- | --- | --- | --- |
| `paipu-video` | 请求可选分辨率的型号 | 必填，按请求发送 | 480p / 720p / 1080p |
| `paipu-video-480p` | 固定输出 480p、接口不接受分辨率字段 | 省略 | 480p |
| `paipu-video-720p` | 固定输出 720p、接口不接受分辨率字段 | 省略 | 720p |
| `paipu-video-768p` | 固定输出 768p、接口不接受分辨率字段 | 省略 | 768p |
| `paipu-video-1080p` | 固定输出 1080p、接口不接受分辨率字段 | 省略 | 1080p |
| `paipu-video-1440p` | 固定输出 1440p、接口不接受分辨率字段 | 省略 | 1440p |

已有四个模板名及其计费字段保持兼容，新增两个固定规格模板。`paipu-video` 仍只有原有的 480p / 720p / 1080p，避免旧价格表达式误用新分辨率档位。已知型号即使走模板也要满足其实际分辨率和固定时长；选择不匹配的模板会被拒绝。

## 支持的模型

支持清单中使用 `/v1/videos` 创建、`/v1/videos/{id}` 查询与 `/content` 取片的视频型号。`prompt` 必填；可变时长必须为整数 1–3600 秒；画幅限 `16:9`、`9:16`、`1:1`、`4:3`、`3:4`、`21:9`；每类参考素材最多 30 个 HTTPS URL。已声明型号按 `MODELS` 校验分辨率，并对固定 15/30 秒接口省略上游 `duration`、按真实固定时长计费。其余具体时长范围、素材上限和画幅支持由 Paipu 校验。

本插件未实现图片/聊天接口、人脸处理专用 `mode`、Omni 未公开的尺寸协议、`process_face` 扩展开关、视频编辑的 `duration: -1` 自动时长和 `adaptive` 画幅。视频编辑可使用明确的正整数时长及普通画幅。不能仅因价格页端点标签是 `openai-video` 就认为可调用。

依据 2026-09-28 的 [价格页](https://api.paipu.net/pricing)、[公开模型目录](https://api.paipu.net/api/media-models/catalog)、[任务查询](https://api.paipu.net/docs/videos/task-query)与[内容获取](https://api.paipu.net/docs/videos/task-content)实现。部分型号如下，完整 50 条核对结果见 [MODEL_CATALOG.md](./MODEL_CATALOG.md)：

| Paipu 模型 | `MODELS` 中的分辨率 |
| --- | --- |
| `lec-gt-seedance-2-0-mini` | `["480p", "720p"]` |
| `lec-gt-seedance-2-0-full` | `["480p", "720p", "1080p"]` |
| `lec-seedance-2-0-mini-c4-480p` | `["480p"]` |
| `lec-wan3-720p` | `["480p", "720p", "1080p"]`，不能按型号后缀猜测 |
| `lec-minimax-h3-768p` | `["768p"]` |
| `lec-h3video-2k` | `["1440p"]`，官方定义的 2K 对应 1440p |

## 安装

1. 在官方 New API 管理员的「任务插件」页面打开总开关，上传 `plugin.js`，启用 `paipu`。
2. 新建 **Task Plugin（任务插件）** 类型渠道，选择 **Paipu Video / paipu**。
3. Base URL 填 `https://api.paipu.net`，不加 `/v1` 或 `/docs`；Key 填 Paipu 签发的 API 令牌。
4. 在「连接与模型」添加 **Paipu 的真实模型名称**；移除自动填入的 `paipu-video*` 模板名称，模板不能直接调用，留在列表里会出现在模型广场。
5. `MODELS` 已包含的真实型号可以直接使用；只有未声明的新型号需要在「路由与映射」填写：**真实模型名称 → 规格模板**。保存并启用渠道。
6. 重新打开该真实名称的「定价」，按上节选择计费方式填价并保存。
7. 客户端使用**自己的 New API 令牌**请求自己的 `/v1/videos`，`model` 填真实名称；查询与下载都使用创建响应返回的宿主任务 `id`。

未来新型号的映射示例（将占位名称换成真实型号）：

```json
{
  "lec-your-new-fixed-720p-model": "paipu-video-720p"
}
```

模型广场按渠道模型列表中的真实名称逐行展示，各自显示自己的价格；客户用真实名称调用，看不到模板。多个模型可以映射到同一个模板，价格互不影响。

自定义别名可以映射到本插件已声明的真实型号，此时上游收到映射目标。例如 `my-mini → lec-gt-seedance-2-0-mini`。对未来未声明的型号，可以用 `paipu/真实模型名称` 映射到模板；模板接入会去掉开头的 `paipu/`，其他前缀不会自动转换。

生效条件：插件已启用、渠道绑定 `paipu`、模型列表包含对外名称。使用模板别名时还需要渠道已启用，且同一对外名称在不同渠道映射到相同模板。宿主的别名缓存最长约 60 秒；保存后重新打开定价页。若仍显示 Token 编辑器，先检查实际加载的插件版本和模型关联。

Paipu 将创建操作标为 `never_replay`：客户端提交超时、结果不明时不要自动重发创建请求，避免重复生成。

插件使用官方 `usageProfiles` 和 `unitLabel`；如果上传提示未知字段，需要更新官方宿主（rc.40 已验证）。当前仓库的定制 Go 服务没有 Task Plugin 宿主，单独放入此目录不会自动增加后台渠道。`fixture.json` 和 `test.mjs` 仅用于开发验证，不需要上传。

## 计费用量

插件上报以下用量，只有定价表达式里用到的字段才计费：

| 字段 | 来源 |
| --- | --- |
| `requests` | 固定 `1`，不随时长或参考素材数量变化 |
| `seconds` | 可变时长取校验后的 `duration` / `seconds`；固定时长型号取已验证的 15 / 30 秒，省略该上游请求字段，拒绝传入其他秒数 |
| `resolution` | 已声明型号取其实际规格；模板取固定规格或请求；`MODELS` 值为 `[]` 时不声明也不上报该字段 |

用户请求不能指定计费模式、条数或绕过时长校验（`metadata`、`parameters`、`requests` 等字段会被拒绝）。完成时保留提交阶段冻结的用量，不读取 Paipu 的 `billing.charged_quota`，也不用上游账单或实测时长覆盖。预扣、成功结算和失败退款由官方宿主处理。

定价页保存后生成的表达式示例，也可直接粘贴到表达式编辑器（数字仅演示，不是 Paipu 报价）。带分辨率的两种表达式仅适用于有 `resolution` 字段的模型；规格未公开的三款型号直接在可视化编辑器填写秒价或条价。按分辨率按秒：

```text
u("resolution") == "1440p" ? tier("1440p", u("seconds") * 0.06) : u("resolution") == "1080p" ? tier("1080p", u("seconds") * 0.04) : u("resolution") == "768p" ? tier("768p", u("seconds") * 0.03) : u("resolution") == "720p" ? tier("720p", u("seconds") * 0.02) : tier("480p", u("seconds") * 0.01)
```

统一按条：

```text
tier("video", u("requests") * 0.10)
```

按分辨率按条：

```text
u("resolution") == "1440p" ? tier("1440p", u("requests") * 0.60) : u("resolution") == "1080p" ? tier("1080p", u("requests") * 0.40) : u("resolution") == "768p" ? tier("768p", u("requests") * 0.30) : u("resolution") == "720p" ? tier("720p", u("requests") * 0.20) : tier("480p", u("requests") * 0.10)
```

### 从旧版升级到 2.2.0

1. 上传并启用 2.2.0，刷新模型列表。39 个已声明型号可直接添加并配置价格，不再需要逐个映射到模板。
2. 现有且规格正确的模板映射继续有效。价格按模型名称保存，不自动重写。固定时长型号现在拒绝与实际长度不符的秒数。
3. 新增 768p / 1440p 型号时，在该模型的定价页明确设置新档位价格。其余模型的可变时长仍必须显式传入。

## 请求示例

向**自己的 New API 地址**发送 `POST /v1/videos`，请求头为 `Authorization: Bearer YOUR_NEW_API_TOKEN` 和 `Content-Type: application/json`：

```json
{
  "aspect_ratio": "16:9",
  "audios": ["https://example.com/reference-audio.mp3"],
  "duration": 5,
  "images": ["https://example.com/reference-image.jpg"],
  "model": "lec-mj-seedance-2-5-bd-720p",
  "prompt": "根据人物和动作参考生成短片。",
  "videos": ["https://example.com/reference-video.mp4"]
}
```

可选分辨率的模型必须带 `resolution` 或 `size`：

```json
{
  "model": "lec-gt-seedance-2-0-mini",
  "prompt": "根据人物和场景参考生成连贯的电影感短片。",
  "duration": 5,
  "resolution": "720p",
  "aspect_ratio": "16:9",
  "images": ["https://example.com/reference-image.jpg"]
}
```

Windows PowerShell 发送中文时可将 JSON 保存为 UTF-8 的 `request.json`，再执行：

```powershell
curl.exe -X POST 'https://YOUR-NEW-API/v1/videos' `
  -H 'Authorization: Bearer YOUR_NEW_API_TOKEN' `
  -H 'Content-Type: application/json; charset=utf-8' `
  --data-binary '@request.json'
```

## 参数与兼容字段

| 客户端字段 | Paipu 请求字段 |
| --- | --- |
| `duration` 或 `seconds` | 可变时长必填整数（1–3600）；固定时长型号可省略，显式传入必须与固定秒数相同 |
| `aspect_ratio` 或 `ratio` | `aspect_ratio`，可选 |
| `resolution` | 支持 `480p` / `720p` / `768p` / `1080p` / `1440p`，仍须属于对应型号的规格；固定型号会校验一致后省略上游字段 |
| `size` | 分辨率标签或可映射的像素尺寸，如 `1280x720`、`768x1365`、`1920x1080`、`2560x1440`，同时推导画幅；规格未公开型号不能使用此字段 |
| `images` / `referenceImages` / `reference_images` / `image` | `images` |
| `input_reference` URL 或 `{"image_url":"https://..."}` | `images` |
| `videos` / `referenceVideos` / `reference_videos` | `videos` |
| `audios` / `referenceAudios` / `reference_audios` | `audios` |

支持 JSON 与 multipart 文本字段，重复的 `images[]` 等文本字段会组合成数组，也接受 JSON 编码的 URL 数组文本。单类素材不能同时使用多个别名。所有参考素材必须是可公开访问的 HTTPS URL；插件不上传文件。

冲突参数、超限时长、超限素材、未知字段和文件上传都会明确拒绝，不会悄悄丢弃。`stream_options` 不适用于这里的异步视频接口。

## 查询与取片

- 查询：`GET /v1/videos/{id}`。
- 下载：`GET` / `HEAD /v1/videos/{id}/content`；宿主负责转发允许的 Range 等下载请求头。
- 状态映射：`queued` → 排队，`in_progress` → 生成中，`completed` → 成功，`failed` → 失败。未知状态交给宿主按轮询异常处理。
- `phase=RESULT_STORAGE_WAIT` 不代表成功，继续等待 `status=completed`。错误保留 `code`、`message`、`phase`、`retryable`；原始账单和其他元数据不展示给客户端。
- 识别 Paipu 的 `url`、`result_url`、`metadata.url`。有效签名链接和 CDN 直链通过无凭据模式下载，绝不把渠道 Key 发给结果链接所在服务器。

Paipu 的本站签名链接默认 15 分钟有效。插件发现当前任务的标准 `/content` 签名已过期、即将过期或没有结果 URL 时，会请求文档支持的固定 `/content` 接口并使用渠道鉴权。若该接口返回跨域 302，官方宿主可能拒绝有凭据的跨域跳转；此时需要从 Paipu 重新查询有效签名链接或让上游直接提供内容。建议出片后及时保存。

详见 [Task Plugin API v1](https://github.com/QuantumNous/new-api/blob/main/docs/plugin-api/v1.md)。

## 离线验证

插件 **2.2.0** 已通过官方 New API **v1.0.0-rc.40** 的 lint、**266/266** 个插件契约用例，以及 **27 项**价格编辑/表达式/展示检查。覆盖 39 个型号的声明、各自分辨率计费字段、直接调用、别名和模板路由、固定时长计费及全部已有请求/查询/取片回归用例。隔离宿主与模拟上游验证 5 个未来型号别名和 6 个直接调用型号的实际余额扣费；未调用真实 Paipu 付费生成，也未修改线上实例。

在本目录执行，不会向 Paipu 提交收费任务：

```sh
node test.mjs
new-api plugin lint plugin.js
new-api plugin test plugin.js --fixture fixture.json
```

也可使用官方镜像（PowerShell）：

```powershell
docker run --rm -v "${PWD}:/tmp/paipu:ro" calciumion/new-api:latest plugin lint /tmp/paipu/plugin.js
docker run --rm -v "${PWD}:/tmp/paipu:ro" calciumion/new-api:latest plugin test /tmp/paipu/plugin.js --fixture /tmp/paipu/fixture.json
```
