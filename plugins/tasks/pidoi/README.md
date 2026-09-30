# Pidoi Video Task Plugin

将 [Pidoi / QIQI API](https://zizi.pidoi.com/docs/index.html) 的视频接口接入官方 New API。上传文件为 [plugin.js](./plugin.js)，按照你提供的 `/api/pricing` 数据，为 **13 个模型按次计费、15 个模型按秒计费**。

这是官方 [Task Plugin API v1](https://github.com/QuantumNous/new-api/blob/main/docs/plugin-api/v1.md) 单文件插件。需要支持 `usageProfiles` 的宿主；已在官方 v1.0.0-rc.40 发行版验证。当前工作区的定制 Go 服务没有这一插件宿主，放入目录不会自动增加后台渠道。

## 安装

1. 管理员进入「任务插件」，打开总开关，上传 `plugin.js` 并启用 **Pidoi Video / pidoi**。
2. 新建 **Task Plugin（任务插件）** 渠道，选择 `pidoi`，Base URL 填 `https://pidoi.com`，不加 `/v1`。Key 填 Pidoi 签发的 **API 令牌**，不是浏览器登录 Cookie。
3. 只选择对应上游令牌分组已开通的模型。`default`、`seedace官转`、`特价SD`、`北部` 等可能需要分别创建渠道和上游令牌；下表列出原始分组。
4. 在每个模型的插件专属价格中配置任务用量表达式，配置键为 `pidoi::模型名`。按次模型只有 `requests`，按秒模型只有 `seconds`；可视化页面会显示对应单位。原有「加收费用（$/请求）」保持 `0`，除非需要另收固定费用。
5. 客户端用你站点签发的令牌请求你站点的 `/v1/videos`。查询、下载均使用你站点返回的公开任务 ID。

仅需上传 `plugin.js`；其他文件是说明、价格参考和验证用例。插件不会修改后台价格。**价格为 0 不等于禁用**，未配好价格的模型应从渠道模型列表移除。

## 收费方式

按次模型：每个成功任务计 1 次，不随时长、分辨率或参考素材数量增加次数。例如售价设为 **$1/次**：

```text
tier("per_request", u("requests") * 1)
```

按秒模型：费用为请求的生成时长 × 秒价。例如售价设为 **$0.7/秒**，10 秒为 $7：

```text
tier("per_second", u("seconds") * 0.7)
```

宿主任务表达式直接返回美元金额，不需要再除以一百万。分组倍率由宿主应用；不要在表达式中重复乘分组倍率。推荐配置插件专属价格，以免同名模型影响其他供应商。

插件校验、转发和提取计费用量共用同一份参数。完成时保留冻结的提交用量；上游返回的费用、Token 数、`requests` 或异常 `seconds` 不会改写收费依据。Pidoi 当前公开查询协议中的 `seconds` 是任务参数，没有给出另一套实测时长结算规则。提交失败、终态失败退款由宿主负责；插件不直接修改余额。

## 价格参考

下表是你提供的价格快照，版本 `a42d372ccf0b5dd13ecf71203521f9d2`，**不是插件自动生效的售价**。快照的普通 `model_price` 没有声明币种，因此这里保留原数值，不擅自标为美元或人民币。填入官方 New API 价格时，先统一成你站点的美元计价口径。完整原始视频条目见 [pricing-reference.json](./pricing-reference.json)。

| 模型 | 收费单位 | `model_price` 原值 | 上游分组 |
| --- | --- | ---: | --- |
| `dola-seedance-2.5` | 次 | 1 | seedace官转 |
| `sd-2.5-720p-pro` | 秒 | 0.7 | seedace官转 |
| `Bt-sd2.0-720p` | 秒 | 0.58 | seedace官转 |
| `wan30-1080p-fast` | 秒 | 0.35 | default |
| `wan30-720p` | 秒 | 0.2 | default |
| `seedace-2.0-480p` | 秒 | 0.45 | seedace官转 |
| `jydancan-1.5` | 次 | 1.5 | jiuyuegrok |
| `seedace-2.0-720p` | 秒 | 0.7 | seedace官转 |
| `sd-2.5-720p-3000` | 次 | 9 | 特价SD |
| `sd-2.5-480p-plus` | 秒 | 0.5 | seedace官转 |
| `sd-2.5-720p-ultra-cf` | 秒 | 0.5 | 特价SD |
| `sora-v3-933-pro` | 次 | 4.5 | default |
| `sd-2.5-720p-plus` | 秒 | 0.85 | seedace官转 |
| `seedace-2.5-480p` | 秒 | 0.58 | seedace官转 |
| `sora-v3-933-plus` | 次 | 7 | 北部 |
| `veo-3.1-fast` | 次 | 0.4 | default |
| `sd2-pro-933-480p` | 次 | 3.3 | default |
| `tejiasd-mini-720p` | 次 | 0.8 | 特价SD |
| `sd-2.5-1080p-max` | 秒 | 0.5 | seedace官转 |
| `jiuyue111` | 次 | 3.5 | cccc |
| `tejiasd` | 次 | 4.5 | 特价SD |
| `sd-2.5-720p-900` | 次 | 7 | seedace官转 |
| `seedace-2.5-720p` | 秒 | 1.05 | seedace官转 |
| `sd-2.5-480p-pro` | 秒 | 0.45 | seedace官转 |
| `sora-933-720P-fast` | 次 | 3.5 | default |
| `H3video-2k` | 次 | 1.8 | 特价SD |
| `sd-2.5-720p-ultra` | 秒 | 0.5 | seedace官转 |
| `Bt-sd2.0-480p` | 秒 | 0.35 | seedace官转 |

单位以 `price_unit` / `price_unit_by_group` 为准。`sd-2.5-1080p-max` 的描述写“按条”，但两个结构化字段都为 `second`，所以本版按秒。两个 `plus` 型号的描述价格也与 `model_price` 不一致，上表采用结构化原值。`jydancan-1.5` 原文标注“测试模型，不要碰”，虽然保留声明，普通渠道建议不勾选。

以下两项保留在价格清单，**未加入插件可用模型**：

| 模型 | 已有信息 | 补齐后才能启用 |
| --- | --- | --- |
| `grok-imagine-video-1.5-preview` | `mixed`，参考值 0.8，文字描述按次 | 各时长、分辨率、单图/多图对应的完整价格规则；不能把混合计费直接当统一 0.8/次 |
| `doubao-seedance-2-5-yq` | CNY/百万 Token；480p/720p 无视频 58.8、有视频 35.28；1080p 无视频 77、有视频 46 | 创建请求契约、预估 Token 规则、完成响应的实际计费 Token 字段和含义，以及币种换算口径 |

## 请求示例

```json
{
  "model": "sd-2.5-720p-plus",
  "prompt": "保持图1人物外貌，参考视频的运镜与音频节奏",
  "seconds": "10",
  "resolution": "720p",
  "aspect_ratio": "9:16",
  "image_url": "https://YOUR-CDN/person.png",
  "reference_image_urls": ["https://YOUR-CDN/scene.png"],
  "reference_videos": ["https://YOUR-CDN/motion.mp4"],
  "audio_urls": ["https://YOUR-CDN/music.mp3"]
}
```

保存为 UTF-8 的 `request.json`，然后调用：

```sh
curl "https://YOUR-NEW-API/v1/videos" \
  -H "Authorization: Bearer YOUR_NEW_API_TOKEN" \
  -H "Content-Type: application/json" --data-binary @request.json

curl "https://YOUR-NEW-API/v1/videos/PUBLIC_TASK_ID" \
  -H "Authorization: Bearer YOUR_NEW_API_TOKEN"

curl -L "https://YOUR-NEW-API/v1/videos/PUBLIC_TASK_ID/content" \
  -H "Authorization: Bearer YOUR_NEW_API_TOKEN" -o video.mp4
```

PowerShell 使用 `curl.exe`，将命令写成一行或使用 PowerShell 续行语法。

## 参数兼容与限制

- 接受 JSON 和 multipart 文本字段；不接受本地文件、base64 或 `file_id`，素材应先上传到公网 HTTP(S) 地址。
- `seconds` / `duration` 接受正整数或整数字符串，同时传入必须相同。所有调用在计费前校验全局 1–3600 秒上限，再校验已知模型限制。
- `aspect_ratio` / `aspectRatio` / `ratio` 统一发送为 `aspect_ratio`；`size` 支持如 `1280x720`、`16:9`、`720p`，与显式分辨率/比例冲突时拒绝。提示词里的“横屏”“竖屏”不会覆盖参数。
- `images` / `image_urls` / `referenceImages` 是完整图片列表；`image_url` / `image` / `input_reference` 是单张主图；`reference_image_urls` / `reference_images` 是额外图片。主图 + 额外图片按顺序合并。完整列表与主图写法同时传时必须一致，避免重复添加或错乱素材编号。
- `input_reference` 接受 URL 字符串或 `{ "image_url": "https://..." }`；视频和音频支持文档中的单值、数组及常见别名。重复别名必须完全相同；列表中本来就重复的 URL 保留。
- `n`、`usage`、`metadata`、`price`、`billing_mode`、`generate_audio`、`seed` 等未纳入上游契约的字段会被过滤，既不转发也不参与计费。接受 `generate_audio` 不表示声音开关生效。采用文档中的扁平请求格式，不支持嵌套 `input/parameters`。
- `tejiasd`、`sora-v3-933-pro/plus`、`H3video-2k` 固定 15 秒，省略时自动填 15，提供其他值拒绝。`H3video-2k` 实际为 768p。`sora-933-720P-fast` 只接受 10/15 秒。
- `sd-2.5-480p-pro/720p-pro` 按你提供的较新描述限制为 5–30 秒、30 图/0 视频/10 音频；9 月 7 日文档中旧的 4 秒和参考视频能力不在本版开放。
- `wan30-720p` 和 `wan30-1080p-fast` 带参考视频时最多生成 15 秒；不带参考视频最多 30 秒。Mini 的 720p 最多 12 秒、480p 最多 15 秒。其他明确限制已写入插件。
- 固定分辨率取模型文档、描述或 ID 标明的档位；明确有多个档位的 Mini 必须显式选择。尚无完整能力说明的型号使用全局边界并交给上游进一步校验，不表示支持所有时长或素材数量。参考素材的实际时长/尺寸由上游检查。
- 渠道别名按最终映射模型验证参数与用量。禁止将一个已声明的按次型号映射成按秒型号，反之亦然；可使用新的自定义别名并单独配置相应价格。

下载使用配置的上游地址、持久化的私有任务 ID 和渠道 Bearer Key 构造 `/content` 请求。插件不把密钥发送到响应中任意提供的 URL；客户端获取宿主自己的下载入口。Pidoi 若改成跨域重定向下载，需要按新的实际契约适配。

## 验证

- 官方 New API v1.0.0-rc.40：插件 lint 通过，160/160 个契约用例通过。
- Node：162/162 测试通过，包含全部 28 个模型的完整参数/计费链路。
- 隔离官方宿主 + 本地模拟上游：28 个模型的价格字段、实际钱包扣费均符合预期；下载鉴权、终态失败退款、非法参数不转发/不扣费均通过。

```sh
new-api plugin lint plugin.js
new-api plugin test plugin.js --fixture fixture.json
node --test plugin.test.mjs
```

没有提交真实 Pidoi 收费任务，也没有修改线上渠道、令牌或价格；真实模型权限和出片仍需使用实际 API 令牌验证。
