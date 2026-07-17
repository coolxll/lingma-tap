# Lingma 原生客户端 MITM 抓包实施记录

**状态：** P0/P1 已实现，待真实 Lingma 客户端传图验收

## 范围和关键结论

本改造针对“Lingma 客户端原生请求经过 lingma-tap 代理”的链路，不针对 Anthropic/OpenAI Gateway 请求。

现有数据库里的 24,107 字节 JPEG 是图片 GET 响应，不存在超过 5 MiB 的问题。没有看到对应上传请求时，必须通过请求头生命周期记录判断：上传是否绕过代理、是否没有发生、或是否在响应前失败；不能把该现象归因于大请求阈值。

## P0：完整且低开销的流量记录

- `Requestheaders`/`Responseheaders` 阶段立即创建 C2S/S2C 记录，失败和取消也可见。
- 使用 `StreamRequestModifier`/`StreamResponseModifier` 捕获流式 body，兼容 go-mitmproxy 的大 body streaming 模式。
- 每侧最多保留 16 MiB；1 MiB 后 spill 到临时文件，避免并发上传造成内存线性增长。
- 原始正文存 SQLite BLOB；`raw_json`、REST 列表和 WebSocket 只包含 4 KiB preview、大小、编码和完成状态。
- 正文通过 `GET /api/records/{id}/body` 按需读取；实时记录在异步落库后使用同一 `(session,index)` 补发数据库 ID。
- 异步 sink 按 `(session,index)` 合并 lifecycle 更新，队列满时不在代理回调中同步写 SQLite。

## P1：图片和视觉链路

- `/algo/api/v2/image/upload` 归类为 `image_upload`，图片响应归类为 `image_resource`。
- 完整 multipart 请求的 `file` part 写入 `record_artifacts`，记录文件名、MIME、大小和 SHA-256。
- 通过 `/api/artifacts/{id}` 按需读取图片，详情面板支持 lazy 图片预览和下载。
- 记录提取 `request_id`、`session_id`、`image_urls`、上传响应 `result.url` 和图片资源 URL。
- 前端使用明确 correlation key 把上传、聊天 turn、CDN GET 和 SSE 响应归组；缺少明确 key 时不按时间窗口猜测。

## 性能约束

- 列表和 WebSocket 不传 BLOB/Base64；大于 4 KiB 的正文只展示 preview，详情加载时才读取完整文本。
- 详情正文请求支持取消；图片使用 Blob/HTTP lazy loading，不在 React 列表状态中缓存图片字节。
- WebSocket 记录先按 key 合并，再通过 `requestAnimationFrame` 批量提交 React state。
- 前端记录数量保持 2,000 上限，列表行启用 `content-visibility: auto`；搜索只扫描摘要字段。
- persistence queue 的 body 内存预算为 64 MiB；超出时保留生命周期和错误状态，并将正文标记为持久化截断。

## 验证

已通过：

- `go test ./internal/... ./...`
- `cd web && npm run build`
- 二进制 BLOB 无损 round-trip 测试
- multipart 图片 artifact 解析、SHA-256 和详情读取测试
- 16 MiB body capture 上限、临时文件 spill 和 EOF finalize 测试

待真实验收：

1. 配置 Lingma 客户端使用 `127.0.0.1:9528` 并信任 lingma-tap CA。
2. 发送一张明确图片，观察是否先出现 `image_upload` 的 headers 记录。
3. 确认上传请求 body、上传响应 `result.url`、聊天 `image_urls` 和 CDN GET 是否进入同一视觉分组。
4. 若连 upload headers 都没有，说明该 Lingma 上传网络路径没有经过当前 MITM，需继续排查客户端网络栈或代理配置。
