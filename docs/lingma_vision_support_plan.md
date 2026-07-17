# Lingma Vision 支持实施记录

- **状态：** 已实现并通过真实上游验收
- **验收日期：** 2026-07-18
- **已验证范围：** 当前模型列表返回的全部 8 个模型

## 结论

Lingma 视觉请求不能只设置 `model_config.is_vl=true`。完整链路为：

```text
协议图片块
  → data URI 解码或安全下载
  → Lingma 图片上传
  → 顶层 image_urls + message.contents
  → is_vl=true + 完整模型元数据 + common 路由元数据
  → agent_chat_generation
```

使用包含埃菲尔铁塔的 `tower.jpg` 进行真实 E2E，`qmodel_latest` 已准确返回“埃菲尔铁塔”。仅发送 `is_vl=true + image_urls`、但缺少完整模型配置时，上游会静默把请求当成纯文本。

## 当前模型能力实测

2026-07-18 使用同一张 `tower.jpg`、同一个已上传的 Lingma VL URL 逐一探测当前模型列表。为排除文本模型盲猜“埃菲尔铁塔”，提示词要求模型同时按固定格式回答建筑、天空颜色、是否有云、是否有树木、是否有人物。测试同时记录模型列表的 `is_vl` 声明和强制发送完整原生 VL 请求后的真实响应：

| 模型 Key | 显示名称 | 列表 `is_vl` | 当前网关 | 强制上游探测 |
| --- | --- | ---: | --- | --- |
| `org_auto` | Auto | `true` | 放行 | 五项细节全部正确 |
| `dashscope_qmodel` | Qwen3.7-Plus | `true` | 放行 | 五项细节全部正确 |
| `qmodel_latest` | Qwen3.7-Max | `true` | 放行 | 五项细节全部正确 |
| `kmodel` | Kimi-K2.6 | `true` | 放行 | 五项细节全部正确 |
| `dmodel` | DeepSeek-V4-Pro | `true` | 放行 | 五项细节全部正确 |
| `dfmodel` | DeepSeek-V4-Flash | `true` | 放行 | 五项细节全部正确 |
| `gm51model` | GLM-5.2 | `false` | 返回 `400 vision_model_unsupported` | 绕过门禁后五项细节全部正确 |
| `mmodel` | MiniMax-M2.7 | `false` | 返回 `400 vision_model_unsupported` | 失败，声明看不到图片并返回 `unexpected EOF` |

因此，按当前动态模型配置，明确可用的 VL 模型是 6 个；这 6 个全部通过真实识图。若只看强制上游实测，则 8 个模型中有 7 个具备视觉能力。GLM-5.2 的上游实际具备视觉能力，但模型列表尚未声明，当前网关依照上游能力元数据继续拒绝它。单张控制图验证只确认当前链路可用，不代表所有图片类型和长对话场景都具有相同稳定性。

## 已确认的原生上传协议

- `PUT https://lingma-api.tongyi.aliyun.com/algo/api/v2/image/upload?request_id=<无连字符 UUID>`
- `multipart/form-data`，文件字段名为 `file`
- 当前响应 URL 位于 `result.url`
- 上传签名正文不是 multipart 字节，而是 multipart 总字节数的十进制字符串
- 额外 Header：
  - `Cosy-BodyHash = md5(<十进制长度字符串>)`
  - `Cosy-BodyLength = len(<十进制长度字符串>)`
  - `Cosy-SigPath = /api/v2/image/upload`
  - `AI-CLIENT-TIMESTAMP`

可用 `LINGMA_IMAGE_UPLOAD_URL` 覆盖上传地址，便于测试或区域差异排查。

## 上游聊天请求要求

视觉请求必须同时满足：

- `model_config.is_vl=true`
- 顶层 `image_urls` 使用上传返回的 Lingma CDN URL
- 用户消息保留文本 `content`，图片块写入原生字段 `contents`
- `contents` 图片形态为 `{"type":"image_url","image_url":{"url":"<CDN URL>","detail":"auto"}}`
- 从模型列表补齐 `display_name`、`format`、`source`、`max_input_tokens`，并设置 `enable=true`
- 使用已验证的原生 VL 路由元数据：`source=1`、`task_id=common`、`chat_task=common`、`session_type=assistant`，且 `request_set_id=chat_record_id`

旧资料中的消息字段 `parts` 已不适用于当前 `definition.Message`。当前原生二进制的 JSON tag 是 `contents,omitempty`；继续使用 `parts` 会被上游静默忽略。

## 入口与边界

已统一支持：

- Chat Completions：`messages[].content[].image_url`
- Responses：`input_image.image_url`
- Anthropic Messages：`image.source` 的 base64 和 URL

### Claude Code 等 Anthropic 客户端

普通 Anthropic Messages 图片块可以直接使用。2026-07-18 已用 Claude Code 风格的流式 `/v1/messages` 请求和 `tower.jpg` 做真实上游验收，请求形态为用户消息中的 `{"type":"image","source":{"type":"base64",...}}`：

- 本机当前 `opus -> qmodel_latest`：成功识别全部五项画面细节
- 本机当前 `haiku -> dashscope_qmodel`：成功识别全部五项画面细节
- 本机当前 `sonnet -> gm51model`：模型列表声明 `is_vl=false`，上传前返回 400
- 未匹配模型名使用 `default_anthropic_model=dashscope_qmodel`，具备 VL 能力

但 Claude Code 2.1.202 的本机真实历史显示，`Read` 读取图片和 Playwright 截图会放在用户消息的 `tool_result.content[]` 内。当前转换器会把 `tool_result` 变为 `role=tool`，而视觉边界只允许 `role=user` 包含图片，因此这条实际工具链会返回 `unsupported_image_location`。所以目前应区分：

- 客户端直接构造普通 user image block：受支持，但最终映射模型必须是 VL 模型。
- Claude Code 通过 `Read`、截图工具返回图片：尚未支持。

后续需要在保留 `tool_use_id` 和工具文本结果的同时，将 `tool_result` 中的图片规范化为上游允许的用户图片消息，并补充多轮工具历史真实 E2E；不能简单放宽角色校验后直接把 `role=tool` 图片发往 Lingma。

首版约束：

- 只允许用户消息包含图片
- 非 VL 模型返回 400，不自动换模型
- 模型元数据不可用返回 503
- Responses `file_id` 暂不支持并返回 400；后续工作见 `internal/bridge/vision.go` 的 `TODO(vision)`
- 图片读取/格式错误返回 400，Lingma 上传失败返回 502

远程图片下载只允许 HTTP(S)，限制重定向并拒绝私网、回环、链路本地等地址；支持 JPEG、PNG、WebP，单图上限 10 MiB。同一请求内按 SHA-256 去重上传，同时保持图片顺序。

图片 data URI 和 Lingma CDN URL 在 Gateway 日志中会被脱敏。

## 测试与验收

普通测试覆盖：

- 三种兼容协议到原生 `contents` 的转换
- multipart PUT、专用签名 Header 和嵌套 `result.url`
- 完整 VL 模型配置和 common 路由元数据
- 重复图片上传去重、非 VL 拒绝、非法 data URI、图片位置限制和日志脱敏
- 纯文本请求继续使用原有路径

真实上游测试为显式 opt-in：

```bash
LINGMA_VISION_E2E=1 \
LINGMA_VISION_IMAGE=/path/to/tower.jpg \
LINGMA_VISION_MODEL=qmodel_latest \
go test -tags=integration -count=1 -run '^TestIntegration_VisionAuto$' -v ./internal/bridge
```

动态读取当前模型列表并逐一探测全部模型：

```bash
LINGMA_VISION_ALL_E2E=1 \
LINGMA_VISION_IMAGE=/path/to/tower.jpg \
go test -tags=integration -count=1 -run '^TestIntegration_VisionAllModels$' -v ./internal/bridge
```

全模型测试只上传一次图片；对 `is_vl=false` 的模型先验证网关门禁，再仅在测试内部绕过门禁发送完整原生 VL 请求。正式请求路径不会绕过模型能力检查。

Claude Code 风格 Anthropic 客户端路径验收：

```bash
LINGMA_ANTHROPIC_VISION_E2E=1 \
LINGMA_VISION_IMAGE=/path/to/tower.jpg \
go test -tags=integration -count=1 -run '^TestIntegration_AnthropicVisionClientPath$' -v ./internal/bridge
```

单模型验收要求回答包含“埃菲尔”或 `Eiffel`；全模型验收还要求天空、云、树木和人物四项画面细节全部正确，不能只以请求成功或响应非空为准。
