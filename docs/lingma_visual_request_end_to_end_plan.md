# Lingma 视觉请求端到端改造计划

## 总结

`is_vl=true` 是必要条件，但不是充分条件。实测 gm51model 会忽略图片并编造结果，mmodel 会 EOF 或声明无法看图，因此图片请求选择 `IsVL=false` 模型时返回 400，不自动换模型。

视觉链路按以下顺序实现：

```
协议图片块 → 图片下载/解码 → Lingma CDN 上传 → image_urls/parts → is_vl=true → 上游聊天
```

保留调用方的 `reasoning_effort/thinking` 设置，不因图片请求强制关闭 reasoning。

---

## 实现改动

### 1. MITM 捕获验证

先通过 MITM 捕获一次原生 Lingma 图片上传，确认当前区域的上传主机、请求编码、响应结构及后续聊天字段；旧的 `/api/v2/image/upload` 主机当前返回 302/503，不能直接硬编码。只有 Auto 模型能准确识别控制图片后才进入代码实现。

### 2. 统一视觉输入结构

增加统一视觉输入结构，覆盖：

- **Chat Completions** 的 `content[].image_url`
- **Responses** 顶层及 `message.content[]` 中的 `input_image`
- **Anthropic** `image.source` 的 `base64` 和 `url`

> 第一版仅支持用户消息图片。Responses `file_id`、Anthropic `tool_result` 内图片和无法解析的图片块返回对应协议的 400，不能静默丢弃。

### 3. 模型能力校验

根据模型缓存中的 `ModelInfo.IsVL` 校验能力：

- 支持视觉的模型继续处理。
- `IsVL=false` 返回 400。
- 模型能力列表不可用时返回 503。
- 不自动切换到 `org_auto`。

### 4. 图片准备与上传

在上游客户端增加图片准备与上传：

- `data URI` 解码后上传。
- 普通 HTTP(S) URL 安全下载后重新上传到 Lingma CDN。
- 阻止私网、回环、链路本地地址及危险重定向；限制 JPEG/PNG/WebP 和单图 10 MiB。
- 同一请求按 SHA-256 去重上传，并保持多图原始顺序。

### 5. 请求体构造

用 `LingmaBodyOptions` 替代继续增加 `BuildLingmaBody` 的位置参数，包含 `IsReasoning`、`IsVL`、`ImageURLs`、`ToolChoice`。

视觉请求体必须同时写入：

- `model_config.is_vl=true`
- 顶层 `image_urls` 为 Lingma CDN URL
- 对应 `user` message 的 `parts`，包含文本和 CDN `image_url`
- 普通 `content` 保留文本，兼容现有上游行为

### 6. 错误处理

- 图片下载/格式错误返回 400
- Lingma 上传失败返回 502
- 现有文本请求保持不变

---

## 测试计划

1. 分别验证 Chat Completions、Responses、Anthropic 的文本加单图、多图、base64 和远程 URL 转换。
2. 验证嵌套 Responses `input_image` 不再原样透传，Anthropic 图片不再被丢弃。
3. 验证 VL 模型生成 `is_vl=true`、`image_urls` 和 `parts`，非 VL 模型不会发起上游请求。
4. 使用 mock 上传服务覆盖成功、非 2xx、非法响应、重复图片、超限、危险 URL 和重定向。
5. 增加显式开启的真实上游 E2E：用固定几何图验证 `org_auto` 准确识别；普通测试不读取真实凭据或访问上游。
6. 完成后运行 `go test ./...`。当前 Responses 分支存在 `addToolCall` 返回值数量不匹配的预存编译错误，应先在其原任务中解决，不混入视觉补丁。

---

## 文档与默认假设

- 修正文档中“只传 `image_url` + `is_vl=true` 即可识图”的表述，明确 CDN 上传、`image_urls` 和 `parts` 同样是必要条件。
- 默认采用“非 VL 模型明确拒绝”和“完整视觉链路”方案；这是根据实测结果选择的安全默认。
- 不改变模型、不关闭 reasoning、不把图片内容或 CDN URL 写入普通日志。
