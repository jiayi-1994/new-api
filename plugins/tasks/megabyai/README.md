# Mega Video Task Plugin

在官方 New API 中安装单文件 `plugin.js`，通过 Mega 的公开 `/v1/videos` API 创建和查询视频。上游文档：[Mega 通用视频 API](https://newapi.megabyai.cc/video-docs)；宿主接口：[Task Plugin API v1](https://github.com/QuantumNous/new-api/blob/main/docs/plugin-api/v1.md)。

请求链路：用户 → 我们的官方 New API 实例 → 本插件 → `https://newapi.megabyai.cc`。本插件直接向 Mega 发送 `referenceImages/referenceVideos/referenceAudios`，不再依赖当前定制版的 Go 参数转换。

## 安装与渠道

1. 在支持 Task Plugin API v1 的官方 New API 实例中，以 Root 登录 `/task-plugins`，打开任务插件总开关，上传本目录的 `plugin.js` 并启用 `megabyai`。`fixture.json` 用于开发验证，不需要上传。
2. 新建 **Task Plugin（任务插件）** 类型渠道，插件选择 **Mega Video / megabyai**。
3. Base URL 填 `https://newapi.megabyai.cc`，不加 `/v1` 或 `/video-docs`；Key 填 **Mega 站签发给我们的 API 令牌**。
4. 配置实际开通的模型和分组。插件默认声明 `videos-mini`、`videos-fast`、`videos-standard`；这只是路由声明，不能替代 Mega 账号的模型授权。若 Mega 指定了其他模型 ID，可配置渠道模型映射；转发使用映射后的模型，计费与客户端展示保留原模型身份。新增公开模型须同步修改源码顶部 `MODELS` 后重新上传。
5. 在模型价格配置中为该插件设置任务用量表达式，再开放流量。同名模型有其他供应商时，优先配置 `megabyai::模型名` 对应的插件专属价格，避免改动其他供应商的计费。

插件同时声明 `new_api` 上游支持；如果另一个官方 New API 也安装了本插件，可以通过支持该扩展的 New API 类型渠道绑定它，使用对方网关令牌。普通接入使用上述 Task Plugin 渠道即可。

### 更新到 2.1.0

上传新版 `plugin.js` 替换原插件，重新打开该插件的模型价格编辑页。每档保留一行，将原来按秒收取的附加单价移到新列 **「按秒加收单价（$/秒）」**，并将官方原有的 **「加收费用（$/请求）」设为 0**，再保存价格。

例如截图中 480p 的基础价 `0.56` 保持不变，`0.2` 填入「按秒加收单价」，原「$/请求」列的 `0.2` 改为 `0`。否则会同时收取按秒加收和按请求加收。升级不会自动改写已保存的价格。

仍只按分辨率定价，不区分有无参考素材。1.x 旧表达式中的 `reference_type` 已移除；从 1.x 升级应重新配置全部价格。

### 手动开关分辨率（保留官方镜像）

插件顶部的允许列表是开关，默认只开放这四档：

```js
const RESOLUTIONS = ["480p", "720p", "1080p", "4k"];
```

例如暂不开放 4k，就从数组中删除 `"4k"`，重新上传插件并保存价格。列表至少保留一档，只使用上述四个合法值。价格条件、使用示例、`resolution` 校验和 `size` 换算校验都使用同一列表；被移除的档位会在转发给 Mega 前拒绝，也无法通过像素尺寸或别名绕过。列表对本插件的所有模型生效。

**官方插件无法读取后台价格，留空或填 0 不等于禁用。** 要关闭某档必须从允许列表移除；支持档位与价格需要分别配置。不会将 1440p、2k 的空价格继续展示成可选档位。

## 请求接口与参数

- 创建：`POST /v1/videos`
- 查询：`GET /v1/videos/{id}`
- 下载：`GET` / `HEAD /v1/videos/{id}/content`

这些是**我们站点**对客户端提供的接口。客户端使用我们站点发放的令牌和宿主返回的 `id`，不能用 Mega 的私有任务 ID。宿主负责统一任务日志、轮询、预扣、结算和失败退款。

| 客户端字段 | 实际发送给 Mega |
| --- | --- |
| `model` | 渠道映射后的模型名 |
| `prompt` | 原样保留，包括中文与 emoji |
| `duration` / `seconds` | 整数 `duration`，1–3600；上游可有更严格限制 |
| `resolution` / `video_resolution` | 允许列表中的 `480p`、`720p`、`1080p`、`4k`；忽略大小写，`2160p` 转为 `4k`（仍须开通 4k） |
| `size` / `video_size` | 根据像素短边和比例解析为 `resolution` 与 `ratio`，也可直接填上述分辨率字符串 |
| `ratio` / `aspect_ratio` / `aspectRatio` | `16:9`、`9:16`、`1:1`、`21:9`、`4:3`、`3:4` |
| `referenceImages` / `reference_images` / `images` / `image` | `referenceImages` URL 数组 |
| `input_reference: "https://..."` 或 `input_reference: {"image_url":"https://..."}` | `referenceImages` URL 数组 |
| `referenceVideos` / `reference_videos` / `videos` | `referenceVideos` URL 数组 |
| `referenceAudios` / `reference_audios` / `audios` | `referenceAudios` URL 数组 |

JSON 与 multipart 的文本字段均可使用，重复的 `referenceImages[]` 等字段会组成数组。同一素材类型不要同时传多个别名。最多接收 128 个素材 URL；每个模型自身的图片/视频/音频数量限制仍由 Mega 检查。

必须明确提供时长，以及 `resolution` 或可识别的 `size`，不会默认改成 720p。`1280x720`、`1920x1080`、`3840x2160` 分别对应 720p、1080p、4k；`854x480` 对应 480p / 16:9。`1792x1024` 和 `720x480` 无法无歧义映射到本插件的分辨率/比例组合，因此返回明确错误，应改传 `resolution` 和 `ratio`。`1440p`、`2k`、`2560x1440` 不在本插件的开放范围，会直接拒绝。

实际可用档位还取决于 Mega 模型和账号权限；本地开放某档不代表上游已开通该档。只开放准备好价格的分辨率，其他档位从允许列表中移除。

当前不接收二进制文件上传、base64、OpenAI `file_id`、未知扩展参数或 `metadata` 覆盖。图片/音视频须先上传到可公网访问的 HTTP(S) 地址。未知字段会报错，不会被悄悄丢弃。需要上游特有的首尾帧、种子等扩展时，应按其实际字段契约增加校验与用例。

## 按秒与加收费用

插件提供三个经过校验的用量字段，转发和计费共用同一份归一化逻辑：

| 用量字段 | 含义 |
| --- | --- |
| `seconds` | 本次提交的输出视频时长，秒 |
| `surcharge_seconds` | 同一输出视频时长，供按秒加收使用；由插件计算，客户端不能覆盖 |
| `resolution` | 本次提交的规范化分辨率 |

每个已开放分辨率只显示一行价格，不区分有无图片、视频或音频。在官方页面填写：

| 配置项 | 单位 | 作用 |
| --- | --- | --- |
| 视频生成单价 | $/秒 | 基础秒价 |
| 按秒加收单价 | $/秒 | 每个输出视频秒数加收的价格；留空或填 `0` 不加收 |
| 加收费用（官方原有列） | $/请求 | 固定请求费，此方案保持 `0` |

**费用 = 输出视频秒数 ×（基础秒价 + 按秒加收单价）**，之后由宿主应用分组倍率等设置。例：480p 基础价 `0.56`、按秒加收 `0.2`、输出 5 秒，分组倍率等调整前为 `5 × (0.56 + 0.2) = 3.8`。

官方原有「加收费用」始终按请求计费，单文件插件不能改变或隐藏这列。新增的按秒加收项使用官方支持的秒数用量字段，因此可继续使用官方镜像。不要把按秒加收金额填回「$/请求」列。清空新的按秒加收输入框按 `0` 保存；基础秒价留空仍不是禁用开关。

下面按截图中的基础价格及 480p 加收 `0.2` 演示，其他档位不加收；用于实际模型前请核对售价。表达式返回美元总价：

```text
u("resolution") == "480p" ? tier("480p", u("seconds") * 0.56 + u("surcharge_seconds") * 0.2) : u("resolution") == "720p" ? tier("720p", u("seconds") * 1) : u("resolution") == "1080p" ? tier("1080p", u("seconds") * 3) : tier("4k", u("seconds") * 5)
```

可在表达式模式粘贴后切回可视化编辑，在每档后面的「按秒加收单价」补充金额。移除允许档位后，按剩余档位重新保存表达式。

预扣使用提交时校验后的字段；Mega 的公开查询契约没有定义实际输出时长/计费用量，因此完成时保留冻结的提交用量，不从缺失字段或视频 URL 推算价格。失败退款由官方宿主处理。插件返回用量而不自行扣减用户余额。

两个秒数字段都使用输出视频时长，当前计费不测量参考视频时长，也不按有无参考视频自动加价。图片、视频、音频 URL 仍正常转发。旧定制站的输入视频时长附加费不在此逻辑中。

## 请求示例

向我们自己的官方 New API 实例发送，令牌也使用我们站点签发的令牌：

```json
{
  "model": "videos-standard",
  "prompt": "图片1 这只小狗在倒立",
  "duration": 5,
  "ratio": "16:9",
  "resolution": "480p",
  "referenceImages": ["https://assets.example/dog.png"]
}
```

Windows PowerShell 一行请求，明确以 UTF-8 字节发送中文：

```powershell
Invoke-RestMethod -Method Post -Uri 'https://YOUR-NEW-API/v1/videos' -Headers @{Authorization='Bearer YOUR_TOKEN'} -ContentType 'application/json; charset=utf-8' -Body ([Text.Encoding]::UTF8.GetBytes((@{model='videos-standard';prompt='图片1 这只小狗在倒立';duration=5;ratio='16:9';resolution='480p';referenceImages=@('https://assets.example/dog.png')} | ConvertTo-Json -Compress -Depth 3)))
```

`JSON body must be valid UTF-8` 是官方宿主在执行插件前对请求原始字节的校验错误。只加 `charset=utf-8` 请求头不会转码；上述写法可避免中文被终端编码成非 UTF-8。URL 必须是原始地址，不能包含 Markdown 的 `[网址](网址)`。

## 查询、下载与错误

支持文档中的 `queued`、`in_progress`、`completed`、`failed`，兼容明确的 pending/processing/succeeded/cancelled 等状态。未知状态上报 UNKNOWN，由宿主累计轮询错误；不会把未知回包当作生成中或已完成。

取片支持顶层 `video_url` / `url`、`data.url` 以及 `results` 中的 URL。优先下载外部存储直链，完全不附带 Mega 渠道令牌。返回当前任务的同源 `/v1/videos/{id}/content` 或 `/content.mp4` 代理地址时使用渠道鉴权。外部下载仍受官方宿主的 SSRF 检查。

若 Mega 只返回带鉴权、跨域重定向的代理地址，官方宿主可能拒绝下载；应让 Mega 返回可直接获取的文件或签名直链。上游没有结果 URL 时会明确报错，不猜测下载地址。查询响应只展示视频字段及错误，不把上游账单或其他原始元数据暴露给最终用户。

## 验证与升级范围

本插件已在本机官方 `calciumion/new-api:latest`（v1.0.0-rc.40）中通过 lint 和 77 个离线契约用例，覆盖中文、OpenAI 字段映射、三类素材、参数边界、计费用量、异步状态和下载凭据隔离，包括素材类型不增加计费条件、空视频数组、字段别名和未开放分辨率拒绝。这些验证没有向真实 Mega 提交收费任务，也未验证真实预扣/退款和所有模型的出片。

另以仅开放 480p/720p 的配置通过官方 lint 和 5 项用例，验证关闭档位后不能用分辨率、别名或像素尺寸绕过。使用官方价格编辑、市场展示和表达式计算源码的 4 项回归测试也已通过：每档只展示一行、开关同步隐藏档位、未填按秒加收单价保持基础价、配置后按输出时长加收且不区分参考素材。

在本目录运行：

```sh
docker run --rm -v "${PWD}:/tmp/mega:ro" calciumion/new-api:latest plugin lint /tmp/mega/plugin.js
docker run --rm -v "${PWD}:/tmp/mega:ro" calciumion/new-api:latest plugin test /tmp/mega/plugin.js --fixture /tmp/mega/fixture.json
```

当前定制版没有官方 Task Plugin 宿主，先在官方镜像测试实例安装。本插件不迁移生产数据库、旧渠道、历史任务、旧分辨率价格表或输入视频时长附加费。正式切换镜像前，应在数据库副本上迁移价格与渠道，验证生成、查询、取片和失败退款，并妥善处理旧实例仍在执行的任务。
