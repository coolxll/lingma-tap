# Lingma 视觉请求端到端改造计划

## 状态

计划已于 2026-07-17 实现。协议细节、限制和真实验收方式见 [lingma_vision_support_plan.md](./lingma_vision_support_plan.md)。

## 已完成项

- [x] 解析 Chat Completions、Responses 和 Anthropic Messages 图片输入
- [x] 校验模型 `IsVL`；非 VL 模型返回 400，元数据不可用返回 503
- [x] 解码 data URI，并安全下载 HTTP(S) 图片
- [x] 限制 JPEG/PNG/WebP、10 MiB、重定向和私网地址
- [x] 按 SHA-256 在单次请求内去重上传
- [x] 还原 Lingma 原生 multipart PUT 和图片上传签名
- [x] 使用 CDN URL 生成顶层 `image_urls` 和消息 `contents`
- [x] 设置 `model_config.is_vl=true`
- [x] 补齐模型列表中的原生元数据和 VL common 路由字段
- [x] 对日志中的 data URI 和 CDN URL 脱敏
- [x] 增加三协议模拟测试和真实上游 E2E
- [x] 使用 `tower.jpg` 验证 `qmodel_latest` 能识别埃菲尔铁塔

## 关键修正

原计划中的 `message.parts` 已根据当前 Lingma 原生类型修正为 `message.contents`。另外，真实测试确认 `is_vl=true`、CDN 上传和 `image_urls` 仍不充分；必须同时补齐模型配置与 common 路由元数据，否则上游会忽略图片。

## 后续项

- [ ] 支持 Responses `file_id`。代码位置见 `internal/bridge/vision.go` 的 `TODO(vision)`。
- [ ] 如需支持工具结果中的图片，先明确 Anthropic/OpenAI 工具图片到 Lingma 历史消息的原生映射，再解除当前 400 限制。
