# Seedance / 破晓视频 Task Plugin

将官方 New API 的 `openai_video` 协议（`POST /v1/videos`、任务查询和内容获取）接到[破晓视频站 API](https://api.hjmie.cc.cd/api-docs)。这是单文件 Task Plugin；安装文件是 [plugin.js](./plugin.js)，`fixture.json` 仅用于本地验证。

## 安装

1. 使用支持 Task Plugin API v1 的官方镜像。在管理员「任务插件」页面启用插件功能，上传 `plugin.js` 并启用 `seedance-hjmie`。本文件已用 `calciumion/new-api:latest`（`v1.0.0-rc.40`，2026-09-21 构建）运行 `plugin lint` 和 `plugin test`。
2. 新建 **Task Plugin** 类型渠道，插件选择 `seedance-hjmie`；Base URL 填 `https://poxiaoapi001.com`（**不要**加 `/v1`），Key 填破晓平台令牌，模型选择 `videos-mini`、`videos-fast`、`videos-standard` 中实际开通的项。不要绑定到官方内置 Sora 插件占用的 OpenAI 类型渠道。
3. 为每个开放模型设置任务用量表达式。插件上报 `seconds`（秒）和 `resolution`（`480p`、`720p`、`1080p`、`4k`）。下例数字**只为演示语法，并非破晓实际价格**，请按自己的价格表全部替换：

   ```text
   u("resolution") == "4k" ? tier("4k", u("seconds") * 0.08) : u("resolution") == "1080p" ? tier("1080p", u("seconds") * 0.04) : u("resolution") == "720p" ? tier("720p", u("seconds") * 0.02) : tier("480p", u("seconds") * 0.01)
   ```

   表达式输出美元总价；官方宿主负责预扣、结算和失败退款。提交时以请求的秒数和分辨率预扣；查询结果若明确返回合法的 `seconds`/`duration` 及 `resolution`/`size`，完成时改用这些值。当前公开查询示例没有这些字段，因此通常按已校验的请求值结算，不能从缺失的结果推断实际编码规格。

4. 用官方 New API 的令牌调用其 `/v1/videos`，不要直接把破晓令牌交给最终客户。创建返回官方宿主的任务 ID；查询和 `/content` 都使用该 ID。

### Windows 发送中文提示词

官方 New API 会在调用插件之前检查 JSON 请求体的原始字节。`JSON body must be valid UTF-8` 表示请求到达时不是有效的 UTF-8；`Content-Type` 里写 `charset=utf-8` 不会转换已经编码错误的字节，更新或重装插件也不能修复这一步。Windows PowerShell 中使用 `curl.exe --data-raw` 直接拼中文时，如遇到此错误，请先明确写出 UTF-8 文件，再让 curl 读取文件：

```powershell
$body = @{
  model = 'videos-standard'
  prompt = '一只猫在海边奔跑，电影感镜头'
  seconds = 5
  resolution = '4k'
  ratio = '16:9'
} | ConvertTo-Json -Compress
$file = Join-Path (Get-Location) 'video-request.json'
[System.IO.File]::WriteAllText($file, $body, [System.Text.UTF8Encoding]::new($false))
curl.exe -X POST 'https://YOUR-NEW-API/v1/videos' `
  -H 'Authorization: Bearer YOUR_NEW_API_TOKEN' `
  -H 'Content-Type: application/json; charset=utf-8' `
  --data-binary '@video-request.json'
```

插件的解码 hook 已用中文 4K 提示词通过离线用例；该用例不经过宿主的原始字节校验，仍需在客户实例上用上述请求验证实际链路。如果改成 UTF-8 文件后仍报同样错误，应检查中间反向代理是否改写了请求体。

内容代理优先使用任务结果里的对象存储直链，以避开上游 `/content` 的跨域 302 跳转；官方宿主会对该无凭据请求执行 SSRF 检查。直链和签名可能过期，出片后应及时保存视频。若结果没有直链，插件后备请求上游 `/content`；此时若该接口仍跨域重定向，官方宿主可能拒绝取片，需要上游提供可直接下载的链接。

## 参数映射

| 客户端字段 | 发往破晓平台 |
| --- | --- |
| `model`、`prompt` | 原模型 ID、提示词 |
| `seconds` 或 `duration` | 整数 `duration`，范围 1–3600；更窄的模型限制由上游校验 |
| `size=1280x720` 等尺寸 | 精确识别短边分辨率和画面比例，发 `resolution=720p`、`ratio=16:9` |
| `resolution` / `video_resolution`，`ratio` / `aspect_ratio` / `aspectRatio` | 规范化的 `resolution`、`ratio` |
| `referenceImages` / `referenceVideos` / `referenceAudios` | `images` / `videos` / `audios` 公网 URL 数组 |
| `image` / `input_reference` URL | 单个 `images` 项 |

JSON 和 multipart 文本字段均可用；multipart 中 `images[]` 等重复文本字段会组合成数组。**文件上传不能使用**：破晓接口只接受 JSON 和可公网下载的 URL。先将文件上传到自己的对象存储，再传 URL。`audios` 在公开文档中尚未实测，请先小额验证。

参考素材的输出字段采用破晓站当前文档中已验证的 `images/videos/audios`。本站网关的 Megabyai 渠道适配器会进一步把它们转换为上游方言 `referenceImages/referenceVideos/referenceAudios`；插件不会把两套同义字段一起发送。

插件要求显式提供 `resolution` 或可识别的 `size`，以免上游默认为 720p。`1792x1024`、`1440p`、`2k` 等未列入本插件四档的值会返回明确错误；冲突值、未知字段和无法映射的尺寸也会返回 400。你确认了插件要开放 `4k`，但[当前公开文档](https://api.hjmie.cc.cd/api-docs)尚未列出 4k；该档还须在破晓平台对应模型上实际开通并配置价格，否则上游可能返回 `invalid_resolution`。

## 验证

在支持插件的官方镜像上运行：

```sh
docker run --rm -v "$PWD/plugin.js:/tmp/plugin.js:ro" calciumion/new-api:latest plugin lint /tmp/plugin.js
docker run --rm -v "$PWD:/tmp/seedance:ro" calciumion/new-api:latest plugin test /tmp/seedance/plugin.js --fixture /tmp/seedance/fixture.json
```

成功结果应为 `plugin seedance-hjmie@1.0.0 is valid` 和 `plugin fixture passed: 24/24 cases`。可用官方 New API 的插件 Sandbox 再对 `protocols.openai_video.decodeRequest`、`buildSubmitRequest`、`extractUsage` 等 hook 做无上游调用的预演。

示例请求（发送到**官方 New API 实例**）：

```sh
curl -X POST "https://YOUR-NEW-API/v1/videos" \
  -H "Authorization: Bearer YOUR_NEW_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"model":"videos-standard","prompt":"海边日出","seconds":15,"size":"1920x1080"}'
```
