# alliex-proxy

Alliex 的参考图必须先传到 `/api/upload`，而 New API 插件每次提交只能发一个请求。这个服务放在两者之间：接收图片链接，上传到 Alliex，再提交视频任务。

```text
客户 → New API → Alliex 插件 → alliex-proxy → Alliex
                     └──── 下载成片直连 Alliex ────┘
```

## 行为

- `POST /api/video`：有 `reference_urls` 时先下载并上传图片，按原顺序换成 `images` 后提交。没有时原样转发。
- `GET /api/video/{id}`、`POST /api/video/{id}/query`：原样转发。
- 其他路径返回 404。
- 提交任务不重试，避免上游重复扣费。

## 限制

- 最多 30 张，单张 30 MB，单次合计 128 MB，只收 PNG、JPEG、WebP。
- 支持 `http(s)` 链接和 base64 的 `data:` 图片。
- 只下载公网地址的 80、443 端口，跳转最多 3 次，每次都重新校验。
- 只接受 Key 哈希匹配的请求。服务不保存 Key 本身，也不记录图片链接的路径和参数。

## 配置

| 环境变量 | 说明 |
| --- | --- |
| `ALLIEX_UPSTREAM` | 必填，`https://main.alliex.space` |
| `ALLIEX_KEY_SHA256` | 必填，渠道 Key 的 SHA-256 |
| `LISTEN_ADDR` | 监听地址，镜像默认 `0.0.0.0:8787` |

计算哈希时不要把 Key 写进命令行：

```bash
read -rs KEY && printf '%s' "$KEY" | shasum -a 256 && unset KEY
```

## 部署

```bash
docker build -t alliex-proxy .
```

```bash
docker run -d --name alliex-proxy --restart unless-stopped -e ALLIEX_UPSTREAM=https://main.alliex.space -e ALLIEX_KEY_SHA256=这里填哈希 -p 127.0.0.1:8787:8787 alliex-proxy
```

端口不要对公网开放。New API 在 Docker 里时，把两个容器放进同一网络，用容器名访问。

部署后按顺序操作：

1. 确认 `http://地址:8787/healthz` 返回 204。
2. 在任务插件页上传 Alliex 插件 v1.1.0。插件单独维护，不在本目录，线上的 v1.0.0 会拒绝图片链接。
3. 把渠道 #23 的 API 地址改成转发服务地址。

第 2、3 步要连着做。新插件直连 Alliex 时，带图请求不会真正带上参考图。

## 测试

```bash
go test ./...
```
