# Paipu 价格页模型核对（2026-09-28）

核对 [价格页](https://api.paipu.net/pricing)、其[公开价格数据](https://api.paipu.net/api/pricing)和[官方媒体模型参数目录](https://api.paipu.net/api/media-models/catalog)。
价格页共 50 个型号：39 个视频型号注册到 `plugin.js` 的 `MODELS`；8 个图片型号、1 个聊天型号不加入视频插件；2 个特殊/资料不足的视频端点另列。价格页的端点标签与型号后缀都不能代替参数定义。

36 个已注册型号有明确分辨率，另 3 个为 `[]`：表示上游没有公布规格，按条/按秒计费，不提供分辨率计费行。数组为空不代表支持任意分辨率，也不会发送 `resolution` 字段。

10 个接口固定生成 15 或 30 秒，不接受 `duration` 请求字段；插件按真实固定时长上报用量，省略该上游字段，拒绝客户端传入不一致的秒数。可变时长型号仍需客户端明确指定秒数。

以下只同步型号和规格，不导入上游售价。本站三个收费模式和售价仍在各模型定价页独立设置。所有型号的参考素材、时长区间、可选字段仍须遵守上游文档；插件保留通用校验，不声称支持每个厂商扩展字段。

| 价格页模型 | 类型 | 已公开分辨率 | 固定时长 | 处理 |
| --- | --- | --- | --- | --- |
| `lec-seed-2-5-900` | video | 未公开 / 不适用 | 30 秒 | 分辨率未公开；空数组，不展示分辨率计费 |
| `lec-wan3-720p` | video | 480p / 720p / 1080p | — | 已接入 |
| `lec-ac-image-2-5-flare` | image | 1K / 2K / 4K | — | 图片接口，不加入视频插件 |
| `lec-ac-image-2-5-sunburst` | image | 1K / 2K / 4K | — | 图片接口，不加入视频插件 |
| `lec-gt-seedance-2-0-mini` | video | 480p / 720p | — | 已接入 |
| `lec-seedance-2-0-full-933-720p` | video | 720p | — | 已接入 |
| `lec-seedance-2-0-933-stable-edit` | video | 480p / 720p | — | 使用显式正整数时长；不支持 -1 自动时长与 adaptive 画幅 |
| `lec-haya-seedance-2-5-xg-720` | video | 720p | — | 已接入 |
| `lec-haya-seedance-2-0-xg-720` | video | 720p | — | 已接入 |
| `lec-ty-seedance-2-0-mini-933-j-720p` | video | 720p | — | 已接入 |
| `lec-grok-4.5` | chat | 未公开 / 不适用 | — | 聊天接口，不加入视频插件 |
| `lec-mj-seedance-2-0-full-933-jd` | video | 720p | — | 已接入 |
| `lec-rs-seedance-2-0-fast` | video | 720p | — | 已接入 |
| `lec-seedance-2-0-mini-c4-480p` | video | 480p | — | 已接入 |
| `lec-md-seedance-2-0-fast-900-720p` | video | 720p | — | 已接入 |
| `lec-mj-wan-3-0-1080p` | video | 720p / 1080p | 30 秒 | 已接入 |
| `lec-ty-seedream-5-pro` | image | 1K / 2K | — | 图片接口，不加入视频插件（价格页端点标为视频，官方目录实际为图片） |
| `lec-ac-seedance-2-0-fast-2-720p` | video | 720p | 15 秒 | 已接入 |
| `lec-vp-seedance-2-0-933-s2` | video | 720p | — | 已接入 |
| `lec-vp-seedance-2-5-m2` | video | 480p / 720p / 1080p | — | 已接入 |
| `lec-ty-seedance-2-0-mini-933-j-480p` | video | 480p | — | 已接入 |
| `lec-vg-seedance-2-5-wd` | video | 720p | — | 已接入 |
| `lec-omni-video-redraw` | video | 未公开 / 不适用 | — | 缺少公开请求参数与三种像素尺寸定义，暂不声明支持 |
| `lec-bk-video-30s` | video | 720p | 30 秒 | 已接入 |
| `lec-ac-banana-flash` | image | 1K / 2K / 4K | — | 图片接口，不加入视频插件 |
| `lec-seedance-2-0` | video | 720p | — | 已接入 |
| `lec-ty-face-processing-1-0` | video | 未公开 / 不适用 | — | 人脸处理专用 mode 接口，无 prompt；不兼容现有视频请求 |
| `lec-seed-2-0-900` | video | 未公开 / 不适用 | 15 秒 | 分辨率未公开；空数组，不展示分辨率计费 |
| `lec-ty-wan-3-0-1055-1080p` | video | 1080p | — | 已接入 |
| `lec-yu25-grok-video-1-5-preview` | video | 480p / 720p | — | 已接入 |
| `lec-ac-banana-pro` | image | 1K / 2K / 4K | — | 图片接口，不加入视频插件 |
| `lec-ty-wan-3-0-1055-720p` | video | 720p | — | 已接入 |
| `lec-md-seedance-2-5-900-720p` | video | 720p | 30 秒 | 已接入 |
| `lec-md-seedance-2-0-900-720p` | video | 720p | — | 已接入 |
| `lec-seedance-2-0-933-stable` | video | 480p / 720p | — | 已接入 |
| `lec-gt-seedance-2-0-full` | video | 480p / 720p / 1080p | — | 已接入 |
| `lec-h3video-2k` | video | 1440p | 15 秒 | 已接入 |
| `lec-seedance-2-0-fast-933-720p` | video | 720p | — | 已接入 |
| `lec-ac-image-2` | image | 1K / 2K / 4K | — | 图片接口，不加入视频插件 |
| `lec-gt-seedance-2-5-720p` | video | 480p / 720p / 1080p | — | 已接入 |
| `lec-image-2` | image | 未公开 / 不适用 | — | 图片接口，不加入视频插件 |
| `lec-mj-seedance-2-5-bd-720p` | video | 720p | — | 已接入 |
| `lec-minimax-h3-768p` | video | 768p | — | 已接入 |
| `lec-vp-wan-3-0-prime` | video | 720p / 1080p | — | 已接入 |
| `lec-ty-seedance-2-0-full-933-me-720p` | video | 720p | — | 已接入 |
| `lec-ac-seedance-2-5-10-image` | video | 未公开 / 不适用 | 30 秒 | 分辨率未公开；空数组，不展示分辨率计费 |
| `lec-minimax-h3` | video | 720p | — | 已接入 |
| `lec-tinysnow-image-2` | image | 1K / 2K / 4K / auto | — | 图片接口，不加入视频插件 |
| `lec-mj-seedance-2-5-vd-720p` | video | 720p | 30 秒 | 已接入 |
| `lec-brx-seedance-2-5-3010` | video | 720p | 30 秒 | 已接入 |

## 对应常量

`plugin.js` 的 `const MODELS = { 模型名: [分辨率] }` 是安装到宿主的实际声明，自动生成 `meta.models` 和各型号的 `usageProfiles`。后续未知型号仍可走已有规格模板，不要求每次改此常量。

`model-catalog.json` 保存本次来源版本和全部 50 个条目的核对结果，供离线回归验证。它不是运行时网络依赖，也不需要上传。
