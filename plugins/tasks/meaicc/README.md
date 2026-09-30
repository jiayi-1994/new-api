# Meaicc Video Task Plugin

把 [Meaicc 视频 API](https://api.meaicc.com/create/sd-2.html) 接入支持 Task Plugin API v1 的官方 New API。安装文件是 [plugin.js](./plugin.js)，支持截图中的 11 个付费模型，按生成任务次数计费。

## 安装与收费设置

1. 管理员打开「任务插件」，启用功能，上传 `plugin.js`，启用 **Meaicc Video / meaicc**。
2. 创建 **Task Plugin（任务插件）** 类型渠道，选择 `meaicc`。Base URL 填 `https://api.meaicc.com`，不加 `/v1`；Key 填 Meaicc 平台令牌。模型只勾选账号已开通、已配置售价的项。
3. 在模型价格设置中配置按次价格。推荐使用插件专属任务用量表达式，例如 `meaicc::sd-2-c1`：

   ```text
   tier("per_request", u("requests") * 1.5)
   ```

   表达式输出美元金额；这里的 `1.5` 是示例售价，请替换成自己的销售价格。可视化编辑器里只需配置 `requests` 对应的「视频生成单价」，额外的「加收费用（$/请求）」保持 `0`，除非确实需要每次再加收一笔。

   也可以沿用宿主的传统「按次」模型价格；插件返回的数量倍率始终是 `1`。已有任务表达式时，应更新为本插件的 `requests` 字段，避免旧的 `seconds`、`resolution` 等表达式仍然生效。插件本身不会写入或更改后台价格。

4. 客户端使用 **New API 发放的令牌**调用本站 `/v1/videos`。创建、查询和下载均使用宿主返回的公开 `id`。

插件只上报 `{ "requests": 1 }`：4 秒、10 秒、15 秒或 30 秒，每个合法任务都是 1 次；分辨率和参考素材数量不增加计费次数。每个模型的金额由后台价格及宿主分组倍率决定。若同一模型还需要按分辨率区分售价，应扩展用量契约后再配置，本版没有这种价格维度。

**价格填 0 不代表禁用。** 插件不能读取后台价格，也不会自动采用上游价格。只给已配置正售价的模型开放渠道；关闭模型应从渠道的可用模型中移除。

### 更新到 1.1.0：兼容客户端别名与额外参数

在「任务插件」页面重新上传本目录的 `plugin.js`，替换原 `meaicc` 插件。插件标识和用量字段 `requests` 没有变化，已有按次价格可以继续使用。

旧版的 `unsupported video request field: image_urls` 来自插件本地校验，请求尚未发到 Meaicc。新版会先识别别名、检查同义字段是否一致，然后重新组装上游支持的字段；其他参数在 JSON、multipart 文本字段及嵌套对象中都会被过滤。

- `images` 和 `image_urls` 同时出现且 URL 列表顺序、内容相同，只生成一份参考图片。不会把 3 张图片重复算成 6 张，也不会改变 `@图1` 等编号；别名列表不一致时报错。
- `aspect_ratio` / `aspectRatio` 映射为 `parameters.ratio`。同时提供 `ratio` 时必须相同；比例由参数决定，不会从提示词中提取。
- `seconds: "15"` 与 `duration: 15` 视为相同值；值不同时报错。
- `generate_audio`、`seed`、客户端自定义选项等未纳入上游契约的字段接受后会被过滤。**接受 `generate_audio` 不表示声音开关生效**，实际声音行为由上游默认设置决定。
- `n`、`requests`、`usage`、`quota`、`price`、`metadata` 等额外字段同样不转发、不参与计费；每个请求仍然只提交一个生成任务，不支持通过这些字段批量生成。

例如客户传 `aspect_ratio: "9:16"`，即使提示词写着“横向 16:9”，仍发送 `parameters.ratio: "9:16"`；需要横向视频应把参数改成 `16:9`。

## 上游价格参考

以下是 2026-09-28 读取 [Meaicc 公开价格接口](https://api.meaicc.com/api/pricing) 的结果，均为美元/次，供配置时参考，不会自动成为你的售价。

| 模型 | 上游参考价（$/次） | 插件接受的时长 | 分辨率 |
| --- | ---: | --- | --- |
| `mx-h3` | 1.20 | 4–15 秒整数 | 768p |
| `sd-2-c1` | 1.50 | 4–15 秒整数 | 720p |
| `sd-2-c3` | 2.70 | 4–15 秒整数 | 720p |
| `sd-2-c4` | 1.80 | 4–15 秒整数 | 720p |
| `sd-2-c5` | 2.50 | 4–15 秒整数 | 720p |
| `sd-2-c6` | 2.20 | 4–15 秒整数 | 720p |
| `sd-2-c7` | 3.20 | 4–15 秒整数 | 720p |
| `sd-2-c8` | 4.20 | 10 或 15 秒 | 720p |
| `sd-2-fast` | 1.00 | 10 秒 | 720p |
| `sd-2.5-c1` | 2.20 | 30 秒 | 720p |
| `w3-c1` | 3.50 | 5–30 秒整数 | 720p / 1080p |

截图中 `sd-2.5-c1` 为 $2.00/次，读取时公开接口已是 $2.20/次。价格可能变化；上表不用于运行时计算。

能力边界结合公开文档、价格表和该页面的[官方客户端插件](https://api.meaicc.com/create/zzdh/video_plugin_meaicc.zip)整理。其中 `sd-2-c3` 未单独声明时长/分辨率，按 SD2 通用范围处理。最终可用能力以对应模型和账号实际权限为准。

## 请求示例

你提供的嵌套 JSON 可以直接发往 New API，无需把 `input` 和 `parameters` 改成扁平参数。将下面内容保存为 UTF-8 的 `request.json`，替换素材地址：

```json
{
  "model": "sd-2-c1",
  "input": {
    "prompt": "图1中的人物穿上图2的装饰",
    "media": [
      { "type": "reference_image", "url": "https://YOUR-CDN/person.png" },
      { "type": "reference_image", "url": "https://YOUR-CDN/decoration.png" },
      { "type": "reference_voice", "url": "https://YOUR-CDN/voice.wav" },
      { "type": "reference_video", "url": "https://YOUR-CDN/motion.mp4" }
    ]
  },
  "parameters": { "resolution": "720p", "ratio": "16:9", "duration": 10 }
}
```

```sh
curl -X POST "https://YOUR-NEW-API/v1/videos" \
  -H "Authorization: Bearer YOUR_NEW_API_TOKEN" \
  -H "Content-Type: application/json; charset=utf-8" \
  --data-binary @request.json

curl "https://YOUR-NEW-API/v1/videos/PUBLIC_TASK_ID" \
  -H "Authorization: Bearer YOUR_NEW_API_TOKEN"

curl -L "https://YOUR-NEW-API/v1/videos/PUBLIC_TASK_ID/content" \
  -H "Authorization: Bearer YOUR_NEW_API_TOKEN" -o video.mp4
```

Windows PowerShell 使用 `curl.exe`，续行使用反引号，或将命令写成一行；不要将中文 JSON 直接拼进旧版 PowerShell 的命令行。`request.json` 必须以 UTF-8 保存。

也兼容扁平 JSON 或 multipart 文本字段：

```json
{
  "model": "sd-2-c4",
  "prompt": "海边日出",
  "seconds": "10",
  "size": "1280x720",
  "images": ["https://YOUR-CDN/reference.png"]
}
```

| 客户端字段 | 发往上游 |
| --- | --- |
| `prompt` | `input.prompt` |
| `media` | `input.media`，保留类型和顺序 |
| `seconds` / `duration` | `parameters.duration`，两个值同时出现时必须相同 |
| `resolution` | `parameters.resolution` |
| `ratio` / `aspect_ratio` / `aspectRatio` | `parameters.ratio`，多个别名同时出现时必须相同 |
| `size` | 可识别的像素尺寸转为分辨率与比例；也接受 `720p` / `768p` / `1080p` |
| `images` / `image_urls` / `referenceImages` / `reference_images` / `input_reference` | `reference_image` 素材 |
| `videos` / `video_urls` / `referenceVideos` / `reference_videos` | `reference_video` 素材 |
| `audios` / `audio_urls` / `referenceAudios` / `reference_audios` | `reference_voice` 素材 |

`input_reference` 支持 URL 字符串和 `{ "image_url": "https://..." }`。multipart 可以将 `input` / `parameters` / `media` 编码成 JSON 文本，也支持重复的 `images[]` 文本字段。不接收本地文件、base64 或 `file_id`；先上传到可公网访问的 HTTP(S) 地址。

嵌套形式和扁平形式不要混用；同类素材别名可以同时传，但规范化后的 URL 列表必须逐项一致。列表内原本重复的 URL 会保留，以维持素材编号。必须明确给出时长和分辨率；未知额外字段会被过滤，已识别字段的非法值、冲突参数和超出模型限制的参数仍会被拒绝，不会自动缩短时长或降低分辨率。嵌套的 `parameters` 也支持 `seconds`、`aspect_ratio` 和 `aspectRatio` 别名。

## 素材和模型限制

- 支持 `first_frame`、`last_frame`、`reference_image`、`reference_voice`、`reference_video`。首尾帧各最多 1 张，并计入图片数量上限。
- 普通 SD2 模型最多 9 图、3 视频、3 音频；`sd-2-fast` 只接收最多 9 张图片；`sd-2.5-c1` 只接收最多 10 张图片；`w3-c1` 最多 10 图、5 视频、5 音频。
- `mx-h3` 按官方客户端插件允许最多 9 图、3 视频、3 音频；价格表只提到了图片和音频，视频参考仍需在账号上验证。
- `sd-2-c1` 在接口文档中展示了音视频参考，而官方客户端插件将其音视频数量设为 0。本插件保留文档和你的请求中的 9 图、3 视频、3 音频能力；如果账号拒绝音视频参考，可改用明确支持的 `sd-2-c4` / `sd-2-c5` 等模型。
- 比例接受 `16:9`、`9:16`、`1:1`、`4:3`、`3:4`、`21:9`；`sd-2-c4` / `sd-2-c6` 按价格页限制为前 3 种。比例可省略并交给上游处理。
- 文档要求 SD2 输入与输出视频总时长不超过 25 秒、图片至少 300×300。插件只读取 URL，无法探测远端素材时长或尺寸，这两项由上游校验。
- 渠道模型映射只改变发给上游的模型及对应参数限制，客户端的模型身份仍用于展示和价格配置。自定义别名还需要按宿主要求完成模型路由绑定。

## 任务结算与下载

提交时上报 1 次用量，宿主按配置预扣。成功时仍上报 1 次，任务最终金额由宿主冻结的价格规则计算；返回的 `seconds`、`requests` 或自称的费用不会改变次数。客户端也不能注入 `n`、`requests`、`usage`、`metadata` 等字段绕过这一规则。

提交失败、轮询返回 `FAILED` / `FAILED: 原因`，以及宿主判断的终态失败，走 New API 的失败退款流程。HTTP 401/429/5xx 等暂时查询错误由宿主处理，不会仅因一次查询失败就主动退款。插件不直接修改钱包、令牌余额或数据库；退款是否执行成功仍取决于宿主的持久化和结算机制。

上游 `object` 存放视频地址；在 OpenAI 协议中，宿主拥有 `object` 字段，因此插件将可下载地址放到 `video_url` / `url`，同时提供 `/content` 的 GET/HEAD 内容代理。下载 CDN 直链时不附带渠道密钥，宿主负责 SSRF 检查。

上游建议轮询间隔至少 20 秒，结果只缓存约 10 小时。后台轮询由宿主控制，本插件 API 无单插件轮询间隔设置；部署时应检查宿主轮询配置，出片后及时下载。

## 验证

已使用官方 **New API v1.0.0-rc.40 Windows 发行版**执行，下载文件通过官方 SHA256 校验：

```text
plugin meaicc@1.1.0 is valid
plugin fixture passed: 94/94 cases
```

在本目录运行：

```sh
new-api plugin lint plugin.js
new-api plugin test plugin.js --fixture fixture.json
node --test plugin.test.mjs
```

Node 测试通过 96/96，包含全部 11 个模型的解码→提取用量→构建提交→完成用量链路，以及客户同时发送 `images`、`image_urls`、`aspect_ratio`、`generate_audio` 的回归用例。测试覆盖 JSON/multipart 别名、相同列表合并、额外字段过滤、冲突拒绝、混合参考、首尾帧、模型映射、次数固定、越界拒绝、终态失败和无凭据下载。`fixture.json` 和 `plugin.test.mjs` 仅用于验证，无需上传。

这些是离线插件契约测试；未使用真实上游令牌发起收费生成，也未在已部署实例中验证真实余额扣减/退款。接口契约参考 [New API Task Plugin API v1](https://github.com/QuantumNous/new-api/blob/main/docs/plugin-api/v1.md)。
