# Lingma Vision 端到端支持计划

## 文档与 TODO

- 在 `internal/bridge/client.go` 的硬编码 `"is_vl": false` 前加入：
  ```go
  // TODO(vision): implement Lingma image upload and VL request assembly; see docs/lingma_vision_support_plan.md.
  ```
- 新建 `docs/lingma_vision_support_plan.md`，记录完整端到端实现方案、测试标准和未确认事项。
- 修正 `docs/lingma_mitm_protocol_parity.md` 中“`is_vl=true` 后 data URI 可被正确识别”的结论：现有严格测试表明图片并未可靠送达；`is_vl=true` 是必要标志，但不足以完成视觉请求。

## Vision 实施方案

- 统一解析三种入口：
  - Chat Completions 的 `content[].image_url`
  - Responses 的顶层及 `message.content[].input_image`
  - Anthropic Messages 的 base64/URL `image` block
- 将图片解码或安全下载后上传到 Lingma 图片服务，再使用返回的 CDN URL 构造顶层 `image_urls` 和用户消息 `parts`。
- 图片请求设置 `model_config.is_vl=true`；纯文本请求保持当前行为；保留调用方原有 reasoning 设置。
- 用 `LingmaBodyOptions` 归并 `IsVL`、`IsReasoning`、`ImageURLs`、`ToolChoice` 等组装参数，避免继续增加位置参数。
- 模型明确不支持 VL 时返回协议对应的 400，不自动切换模型；模型元数据不可用时返回 503。
- 首版只支持用户消息图片；Responses `file_id` 和 Anthropic `tool_result` 内嵌图片返回明确的 400，不能静默丢弃。
- 远程图片下载需限制协议、重定向、私网地址、类型和大小；默认支持 JPEG、PNG、WebP，单图上限 10 MiB，并按内容哈希去重上传。

## 前置验证与错误处理

- 先通过原生 Lingma 客户端图片请求抓包，确认当前区域的上传主机、鉴权和 `/api/v2/image/upload` 请求格式。
- 上传成功是发送聊天请求的前置条件；图片解析失败返回 400，下载或上传失败返回 502。
- 不直接把 data URI 或公共 URL 传给聊天接口，因为现有测试中上游会忽略图片或产生幻觉回答。
- 以一张内容和答案均明确的控制图片做验收，要求 `org_auto` 能稳定识别关键文字或准确计数后，才认为上传链路可用。

## 测试与验收

- 覆盖三种协议的图片归一化、Responses 嵌套图片、Anthropic base64/URL 图片及文本与图片顺序。
- 使用 mock 上传服务验证 CDN URL 被同时写入 `image_urls` 和消息 `parts`，并确认 `is_vl=true`。
- 覆盖非 VL 模型拒绝、非法 base64、超限图片、SSRF、上传失败和纯文本回归。
- 增加可选真实上游 E2E 测试；测试图片必须有客观唯一答案，不能只检查请求成功或响应非空。
- 当前 `responses_stream.go` 的 `addToolCall` 返回值数量编译错误属于已有独立问题，不混入 Vision 改造，但运行全量测试前必须先解决。
